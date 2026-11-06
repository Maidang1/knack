package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
)

// AgentConfig declares a user-defined Agent whose logs are JSON Lines, one
// record per line. Fields are dotted paths into a record ("message.role").
// Mapping only selects fields; it runs no code, and logs stay read-only.
type AgentConfig struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Roots    []string `json:"roots"`    // below the home directory, "~/" optional
	Suffixes []string `json:"suffixes"` // default .jsonl; .jsonl.zst is decompressed
	Fields   Fields   `json:"fields"`
	Roles    Roles    `json:"roles"`
}

type Fields struct {
	Time      string `json:"time"` // RFC 3339 text, or Unix seconds or milliseconds
	Role      string `json:"role"`
	Text      string `json:"text"` // a string or a list of {type, text} blocks
	ID        string `json:"id"`   // record ID; repeated records count once
	Request   string `json:"request"`
	Model     string `json:"model"`
	Thinking  string `json:"thinking"`
	SessionID string `json:"sessionId"`
	Title     string `json:"title"`
	CWD       string `json:"cwd"`
	Branch    string `json:"branch"`

	Usage       Usage       `json:"usage"`
	ToolCalls   ToolCalls   `json:"toolCalls"`
	ToolResults ToolResults `json:"toolResults"`
}

type Usage struct {
	Input      string `json:"input"`
	Output     string `json:"output"`
	CacheRead  string `json:"cacheRead"`
	CacheWrite string `json:"cacheWrite"`
	// InputIncludesCache is set when input counts cached tokens too (OpenAI).
	InputIncludesCache bool `json:"inputIncludesCache"`
}

// ToolCalls are items of the list at Path, optionally only those whose
// fields equal Match. ID, Name and Input are relative to an item.
type ToolCalls struct {
	Path  string            `json:"path"`
	Match map[string]string `json:"match"`
	ID    string            `json:"id"`
	Name  string            `json:"name"`
	Input string            `json:"input"`
}

// ToolResults are like ToolCalls. An empty Path makes the whole record of
// the tool role one result.
type ToolResults struct {
	Path   string            `json:"path"`
	Match  map[string]string `json:"match"`
	ID     string            `json:"id"`
	Output string            `json:"output"`
	Error  string            `json:"error"`
}

// Roles lists the role values of each kind; defaults cover common logs.
type Roles struct {
	User      []string `json:"user"`
	Assistant []string `json:"assistant"`
	Tool      []string `json:"tool"`
}

type agentsFile struct {
	Agents []AgentConfig `json:"agents"`
}

const maxAgentsFile = 1 << 20

var agentID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// LoadAgentConfig registers the Agents defined in path, replacing any loaded
// before. A missing file defines none. Invalid Agents are skipped; the returned
// problems are also listed in every snapshot's warnings.
func LoadAgentConfig(file string) []string {
	var custom []format
	var problems []string
	b, err := readConfig(file)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		problems = append(problems, "agents.json："+err.Error())
	default:
		var cfg agentsFile
		if err := json.Unmarshal(b, &cfg); err != nil {
			problems = append(problems, "agents.json：无法解析，自定义 Agent 未加载")
			break
		}
		// Definitions replace the previous ones, so only built-in Agents and
		// earlier entries of this file are taken.
		taken := slices.Clone(unsupportedAgents)
		var roots []string
		for _, f := range builtinFormats {
			taken = append(taken, f.Info())
			roots = append(roots, f.layout().roots...)
		}
		for i, c := range cfg.Agents {
			f, err := newCustomFormat(c, taken, roots)
			if err != nil {
				label := c.ID
				if label == "" {
					label = fmt.Sprintf("第 %d 个 Agent", i+1)
				}
				problems = append(problems, fmt.Sprintf("agents.json：%s：%s", label, err))
				continue
			}
			custom = append(custom, f)
			taken = append(taken, f.Info())
			roots = append(roots, f.cfg.Roots...)
		}
	}
	registry.Lock()
	registry.custom, registry.warnings = custom, problems
	registry.Unlock()
	return problems
}

func readConfig(file string) ([]byte, error) {
	f, err := os.Open(file)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("无法读取")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxAgentsFile+1))
	if err != nil {
		return nil, fmt.Errorf("无法读取")
	}
	if len(b) > maxAgentsFile {
		return nil, fmt.Errorf("超出 1 MiB")
	}
	return b, nil
}

func configWarnings() []string {
	registry.RLock()
	defer registry.RUnlock()
	return append([]string(nil), registry.warnings...)
}

type customFormat struct {
	cfg  AgentConfig
	hash string
}

func newCustomFormat(c AgentConfig, taken []AgentInfo, roots []string) (customFormat, error) {
	if !agentID.MatchString(c.ID) {
		return customFormat{}, fmt.Errorf("id 只能包含小写字母、数字、- 和 _，最长 32 个字符")
	}
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" || len([]rune(c.Name)) > 40 {
		return customFormat{}, fmt.Errorf("name 不能为空，最长 40 个字符")
	}
	for _, t := range taken {
		if string(t.ID) == c.ID || strings.EqualFold(t.Name, c.Name) {
			return customFormat{}, fmt.Errorf("id 或 name 与已有 Agent 重复")
		}
	}
	if len(c.Roots) == 0 {
		return customFormat{}, fmt.Errorf("缺少 roots")
	}
	for i, r := range c.Roots {
		r = path.Clean(strings.TrimPrefix(strings.ReplaceAll(r, `\`, "/"), "~/"))
		if r == "." || path.IsAbs(r) || r == ".." || strings.HasPrefix(r, "../") || strings.Contains(r, ":") {
			return customFormat{}, fmt.Errorf("roots 必须是主目录下的子目录")
		}
		for _, other := range roots {
			if r == other || strings.HasPrefix(r, other+"/") || strings.HasPrefix(other, r+"/") {
				return customFormat{}, fmt.Errorf("roots 与其他 Agent 的日志目录重叠：%s", other)
			}
		}
		c.Roots[i] = r
	}
	if len(c.Suffixes) == 0 {
		c.Suffixes = []string{".jsonl"}
	}
	for _, s := range c.Suffixes {
		if s != ".jsonl" && s != ".jsonl.zst" {
			return customFormat{}, fmt.Errorf("suffixes 只支持 .jsonl 和 .jsonl.zst")
		}
	}
	if c.Fields.Time == "" || c.Fields.Role == "" || c.Fields.Text == "" {
		return customFormat{}, fmt.Errorf("fields 必须包含 time、role 和 text")
	}
	if c.Fields.ToolCalls.Path != "" && c.Fields.ToolCalls.ID == "" || c.Fields.ToolResults.ID == "" && (c.Fields.ToolResults.Path != "" || c.Fields.ToolResults.Output != "") {
		return customFormat{}, fmt.Errorf("toolCalls 和 toolResults 需要 id 才能关联")
	}
	if len(c.Roles.User) == 0 {
		c.Roles.User = []string{"user", "human"}
	}
	if len(c.Roles.Assistant) == 0 {
		c.Roles.Assistant = []string{"assistant", "ai", "model"}
	}
	if len(c.Roles.Tool) == 0 {
		c.Roles.Tool = []string{"tool"}
	}
	// The version follows the whole definition, so editing a mapping rebuilds
	// that Agent's cached statistics.
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return customFormat{cfg: c, hash: hex.EncodeToString(sum[:6])}, nil
}

func (f customFormat) Info() AgentInfo {
	// Request time is estimated from the previous record, as for Claude.
	return AgentInfo{ID: Agent(f.cfg.ID), Name: f.cfg.Name, OutputRate: true,
		SkillNote: f.cfg.Name + " 是自定义 Agent，Skill 调用暂不支持"}
}
func (f customFormat) version() string { return "custom/2/" + f.hash }
func (f customFormat) layout() layout {
	return layout{roots: f.cfg.Roots, suffixes: f.cfg.Suffixes}
}
func (customFormat) visit([]string, bool) (string, string, bool) { return "", "", true }
func (customFormat) parentLog(logFile) string                    { return "" }
func (f customFormat) recordID(o object) string                  { return text(lookup(o, f.cfg.Fields.ID)) }

// lookup follows a dotted path through nested objects.
func lookup(o object, p string) json.RawMessage {
	if p == "" {
		return nil
	}
	keys := strings.Split(p, ".")
	for _, k := range keys[:len(keys)-1] {
		o = obj(o, k)
		if o == nil {
			return nil
		}
	}
	return o[keys[len(keys)-1]]
}

// text reads a string, or a number written as JSON.
func text(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

// Records of other roles (system, metadata) still carry session fields.
func (f customFormat) record(o object) (time.Time, bool) {
	at := f.time(o)
	return at, !at.IsZero() || f.role(o) != ""
}

func (f customFormat) time(o object) time.Time {
	raw := lookup(o, f.cfg.Fields.Time)
	var s string
	if json.Unmarshal(raw, &s) == nil {
		t, _ := time.Parse(time.RFC3339Nano, s)
		return t
	}
	var n float64
	if json.Unmarshal(raw, &n) != nil || n <= 0 || n > 1e15 {
		return time.Time{}
	}
	if n < 1e11 { // seconds; milliseconds are past 1973 at 1e11
		return time.UnixMilli(int64(n * 1000))
	}
	return time.UnixMilli(int64(n))
}

func (f customFormat) role(o object) string {
	r := text(lookup(o, f.cfg.Fields.Role))
	if r == "" {
		return ""
	}
	match := func(values []string) bool {
		return slices.ContainsFunc(values, func(v string) bool { return strings.EqualFold(r, v) })
	}
	switch {
	case match(f.cfg.Roles.User):
		return "user"
	case match(f.cfg.Roles.Assistant):
		return "assistant"
	case match(f.cfg.Roles.Tool):
		return "tool"
	}
	return ""
}

// items returns the objects at p matching every field of match; the record
// itself when p is empty.
func items(o object, p string, match map[string]string) []object {
	var out []object
	if p == "" {
		out = []object{o}
	} else if raw := lookup(o, p); len(raw) > 0 {
		if out = objects(raw); out == nil {
			if one := decode(raw); one != nil {
				out = []object{one}
			}
		}
	}
	kept := out[:0]
	for _, it := range out {
		ok := true
		for k, v := range match {
			ok = ok && text(lookup(it, k)) == v
		}
		if ok {
			kept = append(kept, it)
		}
	}
	return kept
}

type customCall struct {
	id, name string
	input    json.RawMessage
}
type customResult struct {
	id, output string
	failed     bool
}

func (f customFormat) calls(o object) []customCall {
	c := f.cfg.Fields.ToolCalls
	if c.Path == "" {
		return nil
	}
	var out []customCall
	for _, it := range items(o, c.Path, c.Match) {
		if id := text(lookup(it, c.ID)); id != "" {
			out = append(out, customCall{id, text(lookup(it, c.Name)), lookup(it, c.Input)})
		}
	}
	return out
}

func (f customFormat) results(o object, role string) []customResult {
	c := f.cfg.Fields.ToolResults
	if c.ID == "" || c.Path == "" && role != "tool" {
		return nil
	}
	var out []customResult
	for _, it := range items(o, c.Path, c.Match) {
		id := text(lookup(it, c.ID))
		if id == "" {
			continue
		}
		raw := lookup(it, c.Output)
		output := contentText(raw)
		if output == "" {
			output = jsonText(raw)
		}
		var failed bool
		_ = json.Unmarshal(lookup(it, c.Error), &failed)
		out = append(out, customResult{id, output, failed})
	}
	return out
}

func (f customFormat) usage(o object) (Tokens, bool) {
	u := f.cfg.Fields.Usage
	n := func(p string) (int64, bool) {
		var v int64
		raw := lookup(o, p)
		ok := len(raw) > 0 && json.Unmarshal(raw, &v) == nil
		return max(v, 0), ok
	}
	in, okIn := n(u.Input)
	out, okOut := n(u.Output)
	read, _ := n(u.CacheRead)
	write, _ := n(u.CacheWrite)
	if u.InputIncludesCache {
		in = max(0, in-read-write)
	}
	return Tokens{in, out, read, write}, okIn && okOut
}

func (f customFormat) parse(p *parser, o object, at time.Time) {
	m := f.cfg.Fields
	s := p.s
	if id := text(lookup(o, m.SessionID)); id != "" && s.Session.ParentID == "" {
		s.Session.ID = id
	}
	s.Session.Title = first(text(lookup(o, m.Title)), s.Session.Title)
	s.Session.CWD = first(text(lookup(o, m.CWD)), s.Session.CWD)
	s.Session.GitBranch = first(text(lookup(o, m.Branch)), s.Session.GitBranch)
	role := f.role(o)
	if role == "" {
		return
	}
	s.FormatKnown = true
	if results := f.results(o, role); len(results) > 0 {
		for _, r := range results {
			p.result(r.id, at, nil, object{"is_error": json.RawMessage(fmt.Sprint(r.failed))})
		}
		return
	}
	body := strings.TrimSpace(contentText(lookup(o, m.Text)))
	switch role {
	case "user":
		if body != "" {
			p.begin(f.recordID(o), at)
		}
	case "assistant":
		tokens, known := f.usage(o)
		p.request(first(text(lookup(o, m.Request)), f.recordID(o)), text(lookup(o, m.Model)), at, tokens, known, 0, false)
		calls := f.calls(o)
		for _, c := range calls {
			p.use(c.id, c.name, at, c.input)
		}
		if len(calls) == 0 && body != "" && !p.pendingTools() {
			s.LastReply = at
		}
	}
}

func (f customFormat) replay(tr *transcriptReader, o object, at time.Time) {
	m := f.cfg.Fields
	role := f.role(o)
	if results := f.results(o, role); len(results) > 0 {
		for _, r := range results {
			tr.result(r.id, at, r.output, r.failed)
		}
		return
	}
	body := contentText(lookup(o, m.Text))
	switch role {
	case "user":
		if strings.TrimSpace(body) != "" {
			tr.userEntry(userKind(body), at, body)
		}
	case "assistant":
		rid := first(text(lookup(o, m.Request)), f.recordID(o))
		model := text(lookup(o, m.Model))
		if thinking := contentText(lookup(o, m.Thinking)); strings.TrimSpace(thinking) != "" {
			tr.add(Entry{Kind: EntryThinking, Start: at, End: at, RequestID: rid, Name: model, Text: thinking})
		}
		if strings.TrimSpace(body) != "" {
			tr.add(Entry{Kind: EntryAssistant, Start: at, End: at, RequestID: rid, Name: model, Text: body})
		}
		for _, c := range f.calls(o) {
			if _, ok := tr.tools[c.id]; !ok {
				tr.tools[c.id] = tr.add(Entry{Kind: EntryTool, Start: at, RequestID: rid, ToolID: c.id, Name: c.name, Input: jsonText(c.input)})
			}
		}
	}
}
