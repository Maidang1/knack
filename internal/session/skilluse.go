package session

import (
	"bytes"
	"cmp"
	"encoding/json"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// A skill's life in a turn: how it was loaded, what loading it put into
// context, and whether the work after it reached into the skill's own
// files. Only names, paths, sizes and counts are kept, never content.

// EstimateTokens approximates a tokenizer: CJK characters about one token
// each, other text about four characters per token.
func EstimateTokens(text string) int {
	cjk, other := 0, 0
	for _, r := range text {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
			cjk++
		} else {
			other++
		}
	}
	return cjk + (other+3)/4
}

const baseDirPrefix = "Base directory for this skill:"

// skillBaseDir is the directory Claude Code names on the first line of a
// loaded skill.
func skillBaseDir(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if dir, ok := strings.CutPrefix(line, baseDirPrefix); ok {
		return strings.TrimSpace(dir)
	}
	return ""
}

var commandName = regexp.MustCompile(`<command-name>/?([^<\s]+)</command-name>`)

// slashCommand is the command a prompt invoked with /name, if any.
func slashCommand(text string) string {
	if m := commandName.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

// skillRead finds a command that prints a SKILL.md, which is how Codex
// loads a skill: cat, sed -n, head and the like on <dir>/<name>/SKILL.md.
var skillRead = regexp.MustCompile("(?:^|[\\s;&|(\"'`{])(?:cat|head|tail|sed|nl|less|more|bat|Get-Content)\\b[^;&|\\n]*?((?:[^\\s'\"`;&|]*/)?([A-Za-z0-9_.@+-]+))/SKILL\\.md")

func skillReads(raw json.RawMessage) [][2]string {
	text := string(raw)
	var s string
	if json.Unmarshal(raw, &s) == nil {
		text = s
	}
	// Commands nested in a JSON string arrive with escaped quotes.
	text = strings.ReplaceAll(text, `\"`, `"`)
	var out [][2]string
	for _, m := range skillRead.FindAllStringSubmatch(text, -1) {
		out = append(out, [2]string{m[2], m[1]})
	}
	return out
}

// correctionCues open a prompt that pushes back on the previous turn.
var correctionCues = []string{
	"不对", "不是这样", "不是这个", "错了", "搞错", "不行", "没用", "没有用", "还是不", "还是有问题", "还是报错", "还是没",
	"依然", "仍然", "重来", "撤销", "回滚", "别这样", "不要这样", "停下", "停止",
	"no,", "no.", "no ", "nope", "wrong", "that's wrong", "that's not", "thats not", "not what", "stop", "undo", "revert",
	"still ", "it still", "didn't work", "doesn't work", "does not work", "did not work",
}

func correction(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "no" || t == "不" {
		return true
	}
	for _, cue := range correctionCues {
		if strings.HasPrefix(t, cue) {
			return true
		}
	}
	return false
}

// addSkill records a loaded skill on the current turn.
func (p *parser) addSkill(sk SkillCall) {
	t := p.turn(sk.At)
	t.Skills = append(t.Skills, sk)
}

// skillContent fills in what loading a skill put into context: the text
// Claude Code injects after a Skill call (naming the call) or after a
// /command (as the prompt's companion).
func (p *parser) skillContent(toolID string, companion bool, at time.Time, text string) {
	if len(p.s.Session.Turns) == 0 {
		return
	}
	t := &p.s.Session.Turns[len(p.s.Session.Turns)-1]
	fill := func(sk *SkillCall) {
		sk.Dir = cmp.Or(sk.Dir, skillBaseDir(text))
		sk.Tokens += int64(EstimateTokens(text))
	}
	if toolID == "" {
		if companion && p.s.Command != "" {
			t.Skills = append(t.Skills, SkillCall{Name: p.s.Command, At: at, Source: SkillByCommand})
			fill(&t.Skills[len(t.Skills)-1])
			p.s.Command = ""
		}
		return
	}
	for i := len(t.Skills) - 1; i >= 0; i-- {
		if t.Skills[i].ToolID == toolID {
			fill(&t.Skills[i])
			return
		}
	}
}

// touchSkills counts a tool call as using each skill loaded earlier in the
// turn when its input names a file in the skill's directory other than the
// SKILL.md that loaded it.
func touchSkills(t *Turn, raw json.RawMessage) {
	if len(t.Skills) == 0 || len(raw) == 0 {
		return
	}
	for i := range t.Skills {
		sk := &t.Skills[i]
		name := sk.Name
		if _, after, ok := strings.Cut(name, ":"); ok {
			name = after
		}
		needles := [][]byte{[]byte("/" + name + "/")}
		if sk.Dir != "" {
			needles = append(needles, []byte(strings.TrimSuffix(sk.Dir, "/")+"/"))
		}
		if reaches(raw, needles) {
			sk.Uses++
		}
	}
}

func reaches(raw []byte, needles [][]byte) bool {
	for _, n := range needles {
		rest := raw
		for {
			i := bytes.Index(rest, n)
			if i < 0 {
				break
			}
			rest = rest[i+len(n):]
			if !bytes.HasPrefix(rest, []byte("SKILL.md")) {
				return true
			}
		}
	}
	return false
}

// codexSkills records the skills a Codex tool call loads by printing their
// SKILL.md.
func (p *parser) codexSkills(id string, at time.Time, raw json.RawMessage) {
	for _, r := range skillReads(raw) {
		name, dir := r[0], r[1]
		if name == "." || name == ".." {
			continue
		}
		t := p.turn(at)
		known := false
		for _, sk := range t.Skills {
			known = known || sk.Name == name
		}
		if !known {
			p.addSkill(SkillCall{Name: name, ToolID: id, At: at, Source: SkillByRead, Dir: path.Clean(dir)})
		}
	}
}

// output records the size of what a tool returned. A Codex skill loaded by
// printing its SKILL.md costs what the print returned.
func (p *parser) output(id, text string) {
	pos, ok := p.tools[id]
	if !ok {
		return
	}
	t := &p.s.Session.Turns[pos.turn]
	t.Tools[pos.item].OutputBytes = int64(len(text))
	for i := range t.Skills {
		if sk := &t.Skills[i]; sk.ToolID == id && sk.Source == SkillByRead {
			sk.Tokens = int64(EstimateTokens(text))
		}
	}
}
