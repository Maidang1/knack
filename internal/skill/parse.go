package skill

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// ParseFront reads the YAML-ish front matter of a SKILL.md. It returns the
// name and description written there. A file without front matter yields
// the first paragraph of the body as its description.
func ParseFront(md string) (name, description string) {
	md = strings.TrimPrefix(md, "\uFEFF")
	md = strings.ReplaceAll(md, "\r\n", "\n")
	if !strings.HasPrefix(md, "---\n") {
		return "", firstParagraph(md)
	}
	rest := md[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", firstParagraph(md)
	}
	fm := rest[:end]
	body := rest[end+len("\n---"):]
	name = frontField(fm, "name")
	description = frontField(fm, "description")
	if description == "" {
		description = firstParagraph(body)
	}
	return strings.TrimSpace(name), clip(strings.TrimSpace(description), 2000)
}

func frontField(fm, key string) string {
	lines := strings.Split(fm, "\n")
	prefix := key + ":"
	for i, line := range lines {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		val := strings.TrimSpace(line[len(prefix):])
		if _, ok := blockScalar(val); ok {
			folded := strings.Contains(val, ">")
			var parts []string
			for _, next := range lines[i+1:] {
				if next == "" {
					if len(parts) == 0 {
						continue
					}
					parts = append(parts, "")
					continue
				}
				if !strings.HasPrefix(next, " ") && !strings.HasPrefix(next, "\t") {
					break
				}
				parts = append(parts, strings.TrimSpace(next))
			}
			if folded {
				return strings.Join(parts, " ")
			}
			return strings.Join(parts, "\n")
		}
		return unquote(val)
	}
	return ""
}

func blockScalar(val string) (string, bool) {
	if val == "" {
		return "", false
	}
	if val[0] == '>' || val[0] == '|' {
		return val, true
	}
	return "", false
}

func unquote(s string) string {
	if len(s) < 2 {
		return s
	}
	if s[0] == '"' && s[len(s)-1] == '"' {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
		return s[1 : len(s)-1]
	}
	if s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	return s
}

func firstParagraph(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if b.Len() > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			if b.Len() > 0 {
				break
			}
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(line)
	}
	return b.String()
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}
