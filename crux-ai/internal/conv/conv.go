// Package conv provides shared conversion and parsing utilities for crux-ai.
//
// This package consolidates small helpers (e.g. JSON number extraction,
// map merging, surrogate sanitization) that were previously duplicated
// across multiple provider implementations. The functions are kept
// dependency-free so they can be used from any layer of the project.
//
// Reference: pi-mono (packages/ai/src/utils/*) — extracted to internal to
// eliminate the 5+ duplicate getFloat definitions found in providers/.
package conv

import (
	"strings"
	"unicode/utf8"
)

// GetFloat extracts a float64 from a map[string]any by key.
// Returns 0 when the key is missing or the value is not a JSON number.
//
// JSON unmarshalling in Go produces float64 for any number, so this is the
// canonical accessor. Older code used string-keyed dot paths; those callers
// should use GetFloatPath instead.
func GetFloat(m map[string]any, key string) float64 {
	if v, ok := m[key]; ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0
}

// GetFloatPath retrieves a nested float64 by walking the dotted path
// "a.b.c". Returns 0 on any missing segment or type mismatch.
// This was historically used (incorrectly) by some provider SSE handlers.
func GetFloatPath(m map[string]any, path string) float64 {
	parts := strings.Split(path, ".")
	cur := any(m)
	for _, p := range parts {
		mm, ok := cur.(map[string]any)
		if !ok {
			return 0
		}
		cur, ok = mm[p]
		if !ok {
			return 0
		}
	}
	if f, ok := cur.(float64); ok {
		return f
	}
	return 0
}

// GetString extracts a string from a map by key. Returns "" if missing
// or not a string.
func GetString(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// GetBool extracts a bool from a map by key. Returns false if missing
// or not a bool.
func GetBool(m map[string]any, key string) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// GetInt extracts an int from a map by key. JSON numbers unmarshal to
// float64; we coerce to int via truncation. Returns 0 if missing or
// not numeric.
func GetInt(m map[string]any, key string) int {
	return int(GetFloat(m, key))
}

// MergeMaps merges multiple map[string]string into a single map.
// Later maps overwrite earlier ones on key conflicts. The input maps
// are not modified; a fresh map is always returned. Passing no maps
// returns an empty (non-nil) map.
func MergeMaps(maps ...map[string]string) map[string]string {
	result := make(map[string]string)
	for _, m := range maps {
		for k, v := range m {
			result[k] = v
		}
	}
	return result
}

// MergeAny merges multiple map[string]any into a single map.
// Later maps overwrite earlier ones on key conflicts.
func MergeAny(maps ...map[string]any) map[string]any {
	result := make(map[string]any)
	for _, m := range maps {
		for k, v := range m {
			result[k] = v
		}
	}
	return result
}

// JoinStrings concatenates a slice of strings with the given separator.
// Returns "" for empty input. A single-element slice is returned as-is.
func JoinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	var b strings.Builder
	b.Grow(len(parts[0]) + (len(parts)-1)*len(sep) + len(parts[len(parts)-1]))
	b.WriteString(parts[0])
	for _, p := range parts[1:] {
		b.WriteString(sep)
		b.WriteString(p)
	}
	return b.String()
}

// SanitizeSurrogates removes invalid UTF-16 surrogate halves that occasionally
// appear in LLM output (often from non-Unicode code paths). It preserves
// well-formed surrogate pairs (which encode supplementary code points).
//
// Reference: pi-mono (packages/ai/src/utils/sanitize-unicode.ts).
func SanitizeSurrogates(s string) string {
	if !strings.ContainsRune(s, '\uFFFD') && !hasUnpairedSurrogate(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			// Invalid UTF-8 byte sequence — drop it.
			i++
			continue
		}
		if isSurrogate(r) {
			// Check if this is the high half of a valid surrogate pair.
			if r >= 0xD800 && r <= 0xDBFF && i+size < len(s) {
				next, nextSize := utf8.DecodeRuneInString(s[i+size:])
				if next >= 0xDC00 && next <= 0xDFFF {
					// Valid pair: emit both codepoints.
					b.WriteString(s[i : i+size+nextSize])
					i += size + nextSize
					continue
				}
			}
			// Unpaired surrogate: drop.
			i += size
			continue
		}
		b.WriteString(s[i : i+size])
		i += size
	}
	return b.String()
}

func isSurrogate(r rune) bool {
	return r >= 0xD800 && r <= 0xDFFF
}

func hasUnpairedSurrogate(s string) bool {
	for _, r := range s {
		if r >= 0xD800 && r <= 0xDFFF {
			return true
		}
	}
	return false
}
