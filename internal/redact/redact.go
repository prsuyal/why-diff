// Package redact removes likely secrets before WhyDiff persists an event.
package redact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/prsuyal/why-diff/internal/event"
)

const (
	defaultMaxStringBytes      = 64 * 1024
	defaultMaxRedactionRecords = 100
	replacement                = "[REDACTED]"
)

var sensitiveKeys = map[string]struct{}{
	"accesstoken":   {},
	"apikey":        {},
	"authorization": {},
	"clientsecret":  {},
	"cookie":        {},
	"password":      {},
	"privatekey":    {},
	"refreshtoken":  {},
	"secret":        {},
	"setcookie":     {},
	"token":         {},
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}\b`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{16,}\b`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{16,}\b`),
	regexp.MustCompile(`\bAKIA[A-Z0-9]{16}\b`),
	regexp.MustCompile(`(?i)\bBearer[ \t]+[A-Za-z0-9._~+/=-]{8,}`),
	regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|refresh[_-]?token|password|secret)[ \t]*[:=][ \t]*[^\s,;]+`),
	regexp.MustCompile(`(?s)-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----.*?-----END (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`),
}

type Redactor struct {
	MaxStringBytes      int
	MaxRedactionRecords int
}

func Default() Redactor {
	return Redactor{
		MaxStringBytes:      defaultMaxStringBytes,
		MaxRedactionRecords: defaultMaxRedactionRecords,
	}
}

// Apply redacts both the normalized payload and retained source payload. It
// must run before an event is handed to any durable store.
func (r Redactor) Apply(e *event.Event) error {
	if r.MaxStringBytes <= 0 {
		r.MaxStringBytes = defaultMaxStringBytes
	}
	if r.MaxRedactionRecords <= 0 {
		r.MaxRedactionRecords = defaultMaxRedactionRecords
	}

	var err error
	e.Payload, err = r.redactJSON(e, "payload", e.Payload)
	if err != nil {
		return fmt.Errorf("redact normalized payload: %w", err)
	}
	e.SourcePayload, err = r.redactJSON(e, "source_payload", e.SourcePayload)
	if err != nil {
		return fmt.Errorf("redact source payload: %w", err)
	}
	return nil
}

func (r Redactor) redactJSON(e *event.Event, scope string, raw json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}

	value = r.walk(e, scope, "$", value)
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func (r Redactor) walk(e *event.Event, scope, path string, value any) any {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			childPath := path + "." + key
			if isSensitiveKey(key) {
				current[key] = replacement
				r.record(e, scope, childPath, "sensitive_key")
				continue
			}
			current[key] = r.walk(e, scope, childPath, child)
		}
		return current
	case []any:
		for i, child := range current {
			current[i] = r.walk(e, scope, fmt.Sprintf("%s[%d]", path, i), child)
		}
		return current
	case string:
		redacted := current
		matched := false
		for _, pattern := range secretPatterns {
			if pattern.MatchString(redacted) {
				redacted = pattern.ReplaceAllString(redacted, replacement)
				matched = true
			}
		}
		if matched {
			r.record(e, scope, path, "secret_pattern")
		}
		if len(redacted) > r.MaxStringBytes {
			redacted = truncateUTF8(redacted, r.MaxStringBytes) + "…[TRUNCATED]"
			e.Capture.Truncated = true
			e.AddWarning("content_truncated", scope+" contained a string larger than the capture limit")
		}
		return redacted
	default:
		return value
	}
}

func (r Redactor) record(e *event.Event, scope, path, reason string) {
	if len(e.Capture.Redactions) < r.MaxRedactionRecords {
		e.Capture.Redactions = append(e.Capture.Redactions, event.Redaction{
			Scope:  scope,
			Path:   path,
			Reason: reason,
		})
		return
	}
	for _, warning := range e.Capture.Warnings {
		if warning.Code == "redaction_records_truncated" {
			return
		}
	}
	e.AddWarning("redaction_records_truncated", "additional redaction locations were omitted from capture metadata")
}

func isSensitiveKey(key string) bool {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, key)
	_, ok := sensitiveKeys[normalized]
	return ok
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	truncated := value[:maxBytes]
	for !utf8.ValidString(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated
}
