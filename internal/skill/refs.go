package skill

import (
	"path"
	"regexp"
	"strings"
)

const maxRefs = 64

// requireKeys are the front matter fields that list other skills a skill
// needs, written either inline or as a block list.
var requireKeys = []string{"dependencies", "requires", "depends_on", "depends-on", "depends"}

var (
	backtickRe = regexp.MustCompile("`([^`\\s]{2,80})`")
	linkRe     = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	callRe     = regexp.MustCompile(`(?i)\bskill\(\s*["']?([A-Za-z0-9][A-Za-z0-9_.-]*)`)
	phraseRe   = regexp.MustCompile("(?i)\\b(?:use|using|invoke|invokes|load|run|call|see|read|follow|via|with)\\s+(?:the\\s+)?[`\"']?([a-z0-9][a-z0-9_-]{2,})[`\"']?\\s+skill\\b")
	slashRe    = regexp.MustCompile(`(?:^|[\s(])/([a-z0-9][a-z0-9_-]{2,})(?:[\s.,;:)]|$)`)
)

// ParseRefs reads which other skills a SKILL.md points at. requires are the
// names its front matter lists as dependencies. mentions are names the body
// writes in a way that reads as a skill: in backticks, as a link to another
// SKILL.md, as Skill(name), as "use the name skill" or as /name. A mention
// is only a candidate; BuildGraph keeps those that name a skill in scope.
func ParseRefs(md string) (requires, mentions []string) {
	md = strings.TrimPrefix(md, "\uFEFF")
	md = strings.ReplaceAll(md, "\r\n", "\n")
	body := md
	if strings.HasPrefix(md, "---\n") {
		rest := md[len("---\n"):]
		if end := strings.Index(rest, "\n---"); end >= 0 {
			fm := rest[:end]
			body = rest[end+len("\n---"):]
			for _, key := range requireKeys {
				requires = append(requires, frontList(fm, key)...)
			}
		}
	}
	return dedupe(requires), dedupe(bodyMentions(body))
}

func bodyMentions(body string) []string {
	var out []string
	for _, m := range backtickRe.FindAllStringSubmatch(body, -1) {
		out = append(out, tokenName(m[1]))
	}
	for _, m := range linkRe.FindAllStringSubmatch(body, -1) {
		target := m[1]
		if i := strings.IndexAny(target, "#?"); i >= 0 {
			target = target[:i]
		}
		if strings.EqualFold(path.Base(target), "SKILL.md") {
			out = append(out, path.Base(path.Dir(target)))
		}
	}
	for _, m := range callRe.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	for _, m := range phraseRe.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	for _, m := range slashRe.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

// tokenName turns an inline code token into a skill name: a path to a
// SKILL.md gives the folder's name, a leading slash or "skill:" is dropped.
func tokenName(tok string) string {
	tok = strings.TrimPrefix(tok, "/")
	tok = strings.TrimPrefix(strings.ToLower(tok), "skill:")
	if strings.EqualFold(path.Base(tok), "SKILL.md") {
		return path.Base(path.Dir(tok))
	}
	if strings.ContainsAny(tok, "/\\.") {
		return ""
	}
	return tok
}

func frontList(fm, key string) []string {
	lines := strings.Split(fm, "\n")
	prefix := key + ":"
	for i, line := range lines {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		val := strings.TrimSpace(line[len(prefix):])
		if val != "" {
			val = strings.TrimSuffix(strings.TrimPrefix(val, "["), "]")
			var out []string
			for _, part := range strings.Split(val, ",") {
				if n := cleanName(part); n != "" {
					out = append(out, n)
				}
			}
			return out
		}
		var out []string
		for _, next := range lines[i+1:] {
			trimmed := strings.TrimSpace(next)
			if trimmed == "" {
				continue
			}
			if !strings.HasPrefix(next, " ") && !strings.HasPrefix(next, "\t") && !strings.HasPrefix(trimmed, "-") {
				break
			}
			if item, ok := strings.CutPrefix(trimmed, "-"); ok {
				if n := cleanName(item); n != "" {
					out = append(out, n)
				}
			}
		}
		return out
	}
	return nil
}

func cleanName(s string) string {
	s = strings.TrimSpace(s)
	s = unquote(s)
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "/")
	if strings.ContainsAny(s, " \t:") {
		return ""
	}
	return s
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		key := strings.ToLower(s)
		if s == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
		if len(out) >= maxRefs {
			break
		}
	}
	return out
}
