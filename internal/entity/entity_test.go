package entity_test

import (
	"testing"

	"github.com/prsuyal/why-diff/internal/entity"
)

func TestExtractSupportedLanguages(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path   string
		source string
		want   string
		kind   string
	}{
		{"auth.go", "package auth\nfunc Timeout() int { return 30 }\n", "Timeout", "function"},
		{"auth.py", "class Session:\n    def timeout(self):\n        return 30\n", "Session.timeout", "method"},
		{"auth.js", "export function timeout() { return 30; }\n", "timeout", "function"},
		{"auth.ts", "export const timeout = (): number => 30;\n", "timeout", "function"},
		{"Auth.tsx", "export class Auth { timeout(): number { return 30; } }\n", "Auth.timeout", "method"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.path, func(t *testing.T) {
			t.Parallel()
			entities, err := entity.Extract(test.path, "tree-a", []byte(test.source))
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, candidate := range entities {
				if candidate.QualifiedName == test.want && candidate.Kind == test.kind {
					found = candidate.VersionID != "" && candidate.StructureHash != ""
				}
			}
			if !found {
				t.Fatalf("entities = %+v, want %s %s", entities, test.kind, test.want)
			}
		})
	}
}

func TestRelateRenameAndMove(t *testing.T) {
	t.Parallel()
	before, err := entity.Extract("old/auth.go", "before", []byte("package auth\nfunc Timeout() int { return 30 }\n"))
	if err != nil {
		t.Fatal(err)
	}
	after, err := entity.Extract("new/session.go", "after", []byte("package auth\nfunc SessionTimeout() int { return 30 }\n"))
	if err != nil {
		t.Fatal(err)
	}
	edges := entity.Relate(before, after)
	if len(edges) != 1 {
		t.Fatalf("edges = %+v", edges)
	}
	if edges[0].Relation != "moved_and_renamed" || edges[0].Method != "exact_structure" || edges[0].Confidence < 0.9 {
		t.Fatalf("edge = %+v", edges[0])
	}
}

func TestRelateOmitsAmbiguousStructuralMatch(t *testing.T) {
	t.Parallel()
	before, _ := entity.Extract("old.go", "before", []byte("package demo\nfunc A() int { return 1 }\nfunc B() int { return 2 }\n"))
	after, _ := entity.Extract("new.go", "after", []byte("package demo\nfunc C() int { return 3 }\n"))
	if edges := entity.Relate(before, after); len(edges) != 0 {
		t.Fatalf("ambiguous edges = %+v, want none", edges)
	}
}
