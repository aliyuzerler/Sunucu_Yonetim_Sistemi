package audit

import (
	"sort"
	"strings"
)

// redactedPlaceholder replaces any value stored under a sensitive key.
const redactedPlaceholder = "[REDACTED]"

// sensitiveKeyWords are matched case-insensitively against underscore/dash/dot
// separated key components. Matching is recall-first: false positives only
// reduce debuggability, false negatives would leak secrets. Keep this list in
// sync with docs/security/secure-development-checklist.md ("secret values not
// returned/stored after create; redaction tested").
var sensitiveKeyWords = map[string]struct{}{
	"password":      {},
	"passwd":        {},
	"pwd":           {},
	"secret":        {},
	"token":         {},
	"credential":    {},
	"credentials":   {},
	"key":           {},
	"apikey":        {},
	"auth":          {},
	"authorization": {},
	"cookie":        {},
	"session":       {},
	"signature":     {},
}

// redactKeyMatch reports whether a metadata key must be redacted.
func redactKeyMatch(key string) bool {
	parts := strings.FieldsFunc(strings.ToLower(key), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	if len(parts) == 0 {
		return false
	}
	for _, p := range parts {
		if _, ok := sensitiveKeyWords[p]; ok {
			return true
		}
	}
	return false
}

// Redact deep-copies v replacing every value under a sensitive key with the
// redaction placeholder. Input maps are never mutated.
func Redact(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if redactKeyMatch(k) {
				out[k] = redactedPlaceholder
				continue
			}
			out[k] = Redact(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = Redact(val)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(t))
		for i, val := range t {
			if r, ok := Redact(val).(map[string]any); ok {
				out[i] = r
			}
		}
		return out
	default:
		return v
	}
}

// SortedKeys returns sorted keys of a metadata map (deterministic logs/tests).
func SortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
