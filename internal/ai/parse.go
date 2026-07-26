package ai

import (
	"encoding/json"
	"strings"
)

type modelReply struct {
	SQL         string `json:"sql"`
	Explanation string `json:"explanation"`
}

// parseResponse extracts {sql, explanation} from a model reply, tolerating
// markdown fences and surrounding prose.
func parseResponse(raw string) (sqlText, explanation string) {
	text := strings.TrimSpace(raw)

	// direct JSON, or JSON inside a fence / prose
	if candidate := extractJSON(text); candidate != "" {
		var r modelReply
		if err := json.Unmarshal([]byte(candidate), &r); err == nil && strings.TrimSpace(r.SQL) != "" {
			return strings.TrimSpace(r.SQL), strings.TrimSpace(r.Explanation)
		}
	}

	// ```sql fenced block
	if i := strings.Index(text, "```sql"); i >= 0 {
		rest := text[i+len("```sql"):]
		if j := strings.Index(rest, "```"); j >= 0 {
			return strings.TrimSpace(rest[:j]), ""
		}
	}
	// any fenced block
	if i := strings.Index(text, "```"); i >= 0 {
		rest := text[i+3:]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:]
		}
		if j := strings.Index(rest, "```"); j >= 0 {
			return strings.TrimSpace(rest[:j]), ""
		}
	}

	// last resort: if it plausibly starts with SQL, take it verbatim
	lower := strings.ToLower(text)
	for _, kw := range []string{"select", "with", "insert", "update", "delete", "create"} {
		if strings.HasPrefix(lower, kw) {
			return text, ""
		}
	}
	return "", ""
}

// extractJSON returns the first balanced {...} object in the text.
func extractJSON(text string) string {
	start := strings.IndexByte(text, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		c := text[i]
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start : i+1]
			}
		}
	}
	return ""
}
