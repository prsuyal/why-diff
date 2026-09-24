// Package entity extracts code entities from checkpoint blobs and relates
// entity versions across repository mutations.
package entity

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	treesitter "github.com/tree-sitter/go-tree-sitter"
	treego "github.com/tree-sitter/tree-sitter-go/bindings/go"
	treejavascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	treepython "github.com/tree-sitter/tree-sitter-python/bindings/go"
	treetypescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// Entity is one syntax-level code entity at one Git tree. VersionID identifies
// this exact version; lineage is represented separately by Edge records.
type Entity struct {
	VersionID     string
	TreeID        string
	Path          string
	Language      string
	Kind          string
	Name          string
	QualifiedName string
	StartLine     int
	EndLine       int
	ContentHash   string
	StructureHash string
	Structure     string
}

// Edge is deterministic or heuristic evidence that two versions represent the
// same logical entity. Method explains the matching rule behind Confidence.
type Edge struct {
	FromVersionID string
	ToVersionID   string
	Relation      string
	Method        string
	Confidence    float64
	Evidence      string
}

type languageConfig struct {
	name     string
	language *treesitter.Language
}

// Extract parses supported source code with Tree-sitter. Unsupported files
// return no entities and no error so indexing mixed-language repositories is
// best-effort without weakening capture.
func Extract(path, treeID string, source []byte) ([]Entity, error) {
	config, ok := languageForPath(path)
	if !ok {
		return nil, nil
	}
	parser := treesitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(config.language); err != nil {
		return nil, fmt.Errorf("set %s parser language: %w", config.name, err)
	}
	tree := parser.Parse(source, nil)
	if tree == nil {
		return nil, fmt.Errorf("parse %s source: parser returned no tree", config.name)
	}
	defer tree.Close()

	var entities []Entity
	walk(tree.RootNode(), source, config.name, path, treeID, nil, &entities)
	sort.Slice(entities, func(i, j int) bool {
		if entities[i].StartLine == entities[j].StartLine {
			return entities[i].EndLine < entities[j].EndLine
		}
		return entities[i].StartLine < entities[j].StartLine
	})
	return entities, nil
}

// LanguageForPath returns the parser identity used in the blob cache.
func LanguageForPath(path string) (string, bool) {
	config, ok := languageForPath(path)
	return config.name, ok
}

// Rebase reuses syntax extracted from an identical Git blob at another tree
// or path while assigning version IDs for the new checkpoint location.
func Rebase(values []Entity, path, treeID string) []Entity {
	rebased := make([]Entity, 0, len(values))
	for _, value := range values {
		value.Path = filepath.ToSlash(path)
		value.TreeID = treeID
		value.VersionID = versionID(value)
		rebased = append(rebased, value)
	}
	return rebased
}

// Templates removes checkpoint-specific identity before syntax is persisted in
// the blob cache.
func Templates(values []Entity) []Entity {
	templates := make([]Entity, 0, len(values))
	for _, value := range values {
		value.VersionID = ""
		value.TreeID = ""
		value.Path = ""
		templates = append(templates, value)
	}
	return templates
}

func languageForPath(path string) (languageConfig, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return languageConfig{"go", treesitter.NewLanguage(treego.Language())}, true
	case ".py", ".pyi":
		return languageConfig{"python", treesitter.NewLanguage(treepython.Language())}, true
	case ".js", ".jsx", ".mjs", ".cjs":
		return languageConfig{"javascript", treesitter.NewLanguage(treejavascript.Language())}, true
	case ".ts", ".mts", ".cts":
		return languageConfig{"typescript", treesitter.NewLanguage(treetypescript.LanguageTypescript())}, true
	case ".tsx":
		return languageConfig{"tsx", treesitter.NewLanguage(treetypescript.LanguageTSX())}, true
	default:
		return languageConfig{}, false
	}
}

func walk(node *treesitter.Node, source []byte, language, path, treeID string, parents []string, entities *[]Entity) {
	kind, name, isEntity := describeNode(node, source, language, parents)
	nextParents := parents
	if isEntity && name != "" {
		qualified := strings.Join(append(append([]string(nil), parents...), name), ".")
		start, end := int(node.StartByte()), int(node.EndByte())
		if start >= 0 && end >= start && end <= len(source) {
			content := source[start:end]
			structure := structuralSignature(node)
			entity := Entity{
				TreeID:        treeID,
				Path:          filepath.ToSlash(path),
				Language:      language,
				Kind:          kind,
				Name:          name,
				QualifiedName: qualified,
				StartLine:     int(node.StartPosition().Row) + 1,
				EndLine:       int(node.EndPosition().Row) + 1,
				ContentHash:   digest(content),
				StructureHash: digest([]byte(structure)),
				Structure:     structure,
			}
			entity.VersionID = versionID(entity)
			*entities = append(*entities, entity)
			nextParents = append(append([]string(nil), parents...), name)
		}
	}
	for index := uint(0); index < node.NamedChildCount(); index++ {
		walk(node.NamedChild(index), source, language, path, treeID, nextParents, entities)
	}
}

func versionID(value Entity) string {
	return digest([]byte(strings.Join([]string{
		value.TreeID, value.Path, value.Kind, value.QualifiedName,
		fmt.Sprint(value.StartLine), value.ContentHash,
	}, "\x00")))
}

func describeNode(node *treesitter.Node, source []byte, language string, parents []string) (kind, name string, ok bool) {
	nodeKind := node.Kind()
	switch language {
	case "go":
		switch nodeKind {
		case "function_declaration":
			return "function", fieldText(node, "name", source), true
		case "method_declaration":
			name = fieldText(node, "name", source)
			receiver := compactReceiver(fieldText(node, "receiver", source))
			if receiver != "" {
				name = receiver + "." + name
			}
			return "method", name, true
		case "type_spec":
			return "type", fieldText(node, "name", source), true
		}
	case "python":
		switch nodeKind {
		case "class_definition":
			return "class", fieldText(node, "name", source), true
		case "function_definition":
			kind = "function"
			if len(parents) > 0 {
				kind = "method"
			}
			return kind, fieldText(node, "name", source), true
		}
	default:
		switch nodeKind {
		case "function_declaration", "generator_function_declaration":
			return "function", fieldText(node, "name", source), true
		case "class_declaration", "abstract_class_declaration":
			return "class", fieldText(node, "name", source), true
		case "method_definition", "method_signature", "abstract_method_signature":
			return "method", fieldText(node, "name", source), true
		case "interface_declaration":
			return "interface", fieldText(node, "name", source), true
		case "type_alias_declaration":
			return "type", fieldText(node, "name", source), true
		case "enum_declaration":
			return "enum", fieldText(node, "name", source), true
		case "variable_declarator":
			value := node.ChildByFieldName("value")
			if value != nil && (value.Kind() == "arrow_function" || value.Kind() == "function_expression" || value.Kind() == "generator_function") {
				return "function", fieldText(node, "name", source), true
			}
		}
	}
	return "", "", false
}

func fieldText(node *treesitter.Node, field string, source []byte) string {
	child := node.ChildByFieldName(field)
	if child == nil {
		return ""
	}
	return strings.TrimSpace(child.Utf8Text(source))
}

func compactReceiver(receiver string) string {
	receiver = strings.TrimSpace(receiver)
	receiver = strings.Trim(receiver, "()")
	fields := strings.Fields(receiver)
	if len(fields) == 0 {
		return ""
	}
	value := fields[len(fields)-1]
	value = strings.TrimLeft(value, "*[]")
	if bracket := strings.IndexByte(value, '['); bracket >= 0 {
		value = value[:bracket]
	}
	return value
}

// structuralSignature records named AST node kinds while intentionally
// replacing identifiers and literal values. It preserves program shape while
// allowing rename and small value-only edits to match.
func structuralSignature(node *treesitter.Node) string {
	parts := make([]string, 0, node.DescendantCount())
	var visit func(*treesitter.Node)
	visit = func(current *treesitter.Node) {
		kind := current.Kind()
		switch {
		case strings.Contains(kind, "identifier") || kind == "property_identifier" || kind == "type_identifier":
			parts = append(parts, "identifier")
		case strings.Contains(kind, "string") || strings.Contains(kind, "number") || kind == "true" || kind == "false" || kind == "none":
			parts = append(parts, "literal")
		default:
			parts = append(parts, kind)
		}
		for index := uint(0); index < current.NamedChildCount(); index++ {
			visit(current.NamedChild(index))
		}
	}
	visit(node)
	return strings.Join(parts, " ")
}

// Relate matches entity versions across one repository mutation. Each entity
// participates in at most one edge; ambiguous structural matches are omitted.
func Relate(before, after []Entity) []Edge {
	usedBefore := make(map[int]bool)
	usedAfter := make(map[int]bool)
	var edges []Edge

	match := func(method string, confidence float64, predicate func(Entity, Entity) bool) {
		for afterIndex, candidate := range after {
			if usedAfter[afterIndex] {
				continue
			}
			matches := make([]int, 0, 1)
			for beforeIndex, previous := range before {
				if !usedBefore[beforeIndex] && predicate(previous, candidate) {
					matches = append(matches, beforeIndex)
				}
			}
			if len(matches) != 1 {
				continue
			}
			beforeIndex := matches[0]
			edges = append(edges, makeEdge(before[beforeIndex], candidate, method, confidence))
			usedBefore[beforeIndex] = true
			usedAfter[afterIndex] = true
		}
	}

	match("qualified_name", 0.98, func(left, right Entity) bool {
		return left.Path == right.Path && left.Kind == right.Kind && left.QualifiedName == right.QualifiedName
	})
	match("exact_content", 0.99, func(left, right Entity) bool {
		return left.Kind == right.Kind && left.ContentHash == right.ContentHash
	})
	match("exact_structure", 0.90, func(left, right Entity) bool {
		return left.Kind == right.Kind && left.StructureHash == right.StructureHash && comparableSize(left, right)
	})

	for afterIndex, candidate := range after {
		if usedAfter[afterIndex] {
			continue
		}
		bestIndex, best, second := -1, 0.0, 0.0
		for beforeIndex, previous := range before {
			if usedBefore[beforeIndex] || previous.Kind != candidate.Kind || !comparableSize(previous, candidate) {
				continue
			}
			score := shingleSimilarity(previous.Structure, candidate.Structure)
			if score > best {
				second, best, bestIndex = best, score, beforeIndex
			} else if score > second {
				second = score
			}
		}
		if bestIndex < 0 || best < 0.72 || best-second < 0.12 {
			continue
		}
		confidence := 0.65 + 0.25*best
		edges = append(edges, makeEdge(before[bestIndex], candidate, "structural_similarity", confidence))
		usedBefore[bestIndex] = true
		usedAfter[afterIndex] = true
	}
	return edges
}

func makeEdge(before, after Entity, method string, confidence float64) Edge {
	relation := "modified"
	pathChanged := before.Path != after.Path
	nameChanged := before.QualifiedName != after.QualifiedName
	switch {
	case pathChanged && nameChanged:
		relation = "moved_and_renamed"
	case pathChanged:
		relation = "moved"
	case nameChanged:
		relation = "renamed"
	case before.ContentHash == after.ContentHash:
		relation = "unchanged"
	}
	return Edge{
		FromVersionID: before.VersionID,
		ToVersionID:   after.VersionID,
		Relation:      relation,
		Method:        method,
		Confidence:    confidence,
		Evidence: fmt.Sprintf("%s %s:%d-%d -> %s:%d-%d",
			before.QualifiedName, before.Path, before.StartLine, before.EndLine,
			after.Path, after.StartLine, after.EndLine),
	}
}

func comparableSize(left, right Entity) bool {
	leftLines := max(1, left.EndLine-left.StartLine+1)
	rightLines := max(1, right.EndLine-right.StartLine+1)
	ratio := float64(leftLines) / float64(rightLines)
	return ratio >= 0.5 && ratio <= 2.0
}

func shingleSimilarity(left, right string) float64 {
	leftSet, rightSet := shingles(strings.Fields(left), 3), shingles(strings.Fields(right), 3)
	if len(leftSet) == 0 || len(rightSet) == 0 {
		return 0
	}
	intersection := 0
	union := make(map[string]struct{}, len(leftSet)+len(rightSet))
	for value := range leftSet {
		union[value] = struct{}{}
	}
	for value := range rightSet {
		if _, ok := leftSet[value]; ok {
			intersection++
		}
		union[value] = struct{}{}
	}
	return float64(intersection) / float64(len(union))
}

func shingles(values []string, width int) map[string]struct{} {
	result := make(map[string]struct{})
	if len(values) < width {
		if len(values) > 0 {
			result[strings.Join(values, "\x00")] = struct{}{}
		}
		return result
	}
	for index := 0; index+width <= len(values); index++ {
		result[strings.Join(values[index:index+width], "\x00")] = struct{}{}
	}
	return result
}

// AtLine returns the narrowest entity containing a one-based source line.
func AtLine(entities []Entity, path string, line int) (Entity, bool) {
	var best Entity
	found := false
	for _, candidate := range entities {
		if candidate.Path != filepath.ToSlash(path) || line < candidate.StartLine || line > candidate.EndLine {
			continue
		}
		if !found || candidate.EndLine-candidate.StartLine < best.EndLine-best.StartLine {
			best, found = candidate, true
		}
	}
	return best, found
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
