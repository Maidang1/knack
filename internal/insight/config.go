package insight

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"knack/internal/session"
)

// Config scopes, from the widest to the narrowest.
const (
	ScopeUser    = "全局"
	ScopeProject = "项目"
	ScopeLocal   = "本地"
)

// ConfigFile is one file an Agent reads at startup.
type ConfigFile struct {
	Agent  session.Agent
	Scope  string
	Kind   string // settings, mcp, instructions
	Path   string
	Exists bool
	Size   int64
	Tokens int // instructions only: estimated tokens loaded into every session
	Err    string
	// ImportedBy is the instruction file whose @path line loaded this one.
	ImportedBy string
}

// MCPServer never carries arguments, environment or full URLs: they often
// hold credentials. Launch is the command's base name or the URL's host.
type MCPServer struct {
	Agent    session.Agent
	Name     string
	Scope    string
	Source   string
	Launch   string
	Disabled bool
	Calls    int // in the analyzed period, when the Agent's logs name MCP tools
}

type Hook struct {
	Agent          session.Agent
	Scope, Source  string
	Event, Matcher string
	Command        string // first word of the command only
}

type PermissionRule struct {
	Agent      session.Agent
	Scope      string
	Source     string
	Kind, Rule string // allow, ask, deny
}

type ConfigReport struct {
	Dir               string
	Files             []ConfigFile
	MCP               []MCPServer
	Hooks             []Hook
	Permissions       []PermissionRule
	InstructionTokens map[session.Agent]int
	Warnings          []string
}

// Files larger than this are not read; ~/.claude.json grows with history.
const maxConfigBytes = 32 << 20

// ReadConfig reads the Claude Code and Codex configuration that applies to
// an Agent started in dir: the user's own files, then the project's. An
// empty dir reads the user files only. Nothing is written.
func ReadConfig(home, dir string) ConfigReport {
	out := ConfigReport{Dir: dir, InstructionTokens: map[session.Agent]int{}}
	home = filepath.Clean(home)
	if dir != "" {
		dir = filepath.Clean(dir)
	}
	c := &configReader{home: home, out: &out}

	// Claude Code
	c.settings(session.Claude, ScopeUser, filepath.Join(home, ".claude", "settings.json"))
	c.claudeJSON(filepath.Join(home, ".claude.json"), dir)
	c.instructions(session.Claude, ScopeUser, filepath.Join(home, ".claude", "CLAUDE.md"))
	if dir != "" {
		c.settings(session.Claude, ScopeProject, filepath.Join(dir, ".claude", "settings.json"))
		c.settings(session.Claude, ScopeLocal, filepath.Join(dir, ".claude", "settings.local.json"))
		c.mcpJSON(session.Claude, ScopeProject, filepath.Join(dir, ".mcp.json"))
		for _, d := range ancestors(home, dir) {
			c.instructions(session.Claude, ScopeProject, filepath.Join(d, "CLAUDE.md"))
			c.instructions(session.Claude, ScopeProject, filepath.Join(d, ".claude", "CLAUDE.md"))
		}
		c.instructions(session.Claude, ScopeLocal, filepath.Join(dir, "CLAUDE.local.md"))
	}

	// Codex
	c.codexTOML(filepath.Join(home, ".codex", "config.toml"))
	c.instructions(session.Codex, ScopeUser, filepath.Join(home, ".codex", "AGENTS.md"))
	if dir != "" {
		for _, d := range ancestors(home, dir) {
			c.instructions(session.Codex, ScopeProject, filepath.Join(d, "AGENTS.md"))
		}
	}
	slices.SortStableFunc(out.MCP, func(a, b MCPServer) int {
		return cmp.Or(strings.Compare(string(a.Agent), string(b.Agent)), strings.Compare(a.Name, b.Name))
	})
	return out
}

// ancestors are dir and its parents below home, outermost first. A dir
// outside home is read alone.
func ancestors(home, dir string) []string {
	var out []string
	rel, err := filepath.Rel(home, dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return []string{dir}
	}
	for d := dir; ; d = filepath.Dir(d) {
		out = append(out, d)
		if filepath.Dir(d) == home || filepath.Dir(d) == d {
			break
		}
	}
	slices.Reverse(out)
	return out
}

type configReader struct {
	home string
	out  *ConfigReport
}

func (c *configReader) read(agent session.Agent, scope, kind, path string) ([]byte, bool) {
	return c.readFrom(agent, scope, kind, path, "")
}

func (c *configReader) readFrom(agent session.Agent, scope, kind, path, importedBy string) ([]byte, bool) {
	f := ConfigFile{Agent: agent, Scope: scope, Kind: kind, Path: path, ImportedBy: importedBy}
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, false
	case err != nil:
		f.Err = "无法访问"
	case !info.Mode().IsRegular():
		f.Err = "不是普通文件"
	case info.Size() > maxConfigBytes:
		f.Exists, f.Size, f.Err = true, info.Size(), "文件过大，未读取"
	}
	if f.Err != "" {
		c.out.Files = append(c.out.Files, f)
		return nil, false
	}
	b, err := readLimited(path)
	f.Exists, f.Size = true, info.Size()
	if err != nil {
		f.Err = "读取失败"
		c.out.Files = append(c.out.Files, f)
		return nil, false
	}
	if kind == "instructions" {
		f.Tokens = EstimateTokens(string(b))
		c.out.InstructionTokens[agent] += f.Tokens
	}
	c.out.Files = append(c.out.Files, f)
	return b, true
}

func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxConfigBytes))
}

func (c *configReader) fail(path, what string) {
	c.out.Warnings = append(c.out.Warnings, fmt.Sprintf("%s：%s", shortPath(c.home, path), what))
	for i := range c.out.Files {
		if c.out.Files[i].Path == path {
			c.out.Files[i].Err = what
		}
	}
}

func (c *configReader) instructions(agent session.Agent, scope, path string) {
	b, ok := c.read(agent, scope, "instructions", path)
	if ok && agent == session.Claude {
		c.imports(scope, path, b, map[string]bool{path: true}, 1)
	}
}

// Claude Code follows @path imports in CLAUDE.md, outside code, up to five
// levels deep, relative to the importing file.
const maxImportDepth = 5

var importPattern = regexp.MustCompile(`(?:^|\s)@((?:~/|\.{0,2}/)?[^\s@` + "`" + `]+)`)

func (c *configReader) imports(scope, from string, body []byte, seen map[string]bool, depth int) {
	if depth > maxImportDepth {
		return
	}
	fenced := false
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		for _, m := range importPattern.FindAllStringSubmatch(stripCodeSpans(line), -1) {
			target := strings.TrimRight(m[1], ".,;:)")
			switch {
			case strings.HasPrefix(target, "~/"):
				target = filepath.Join(c.home, target[2:])
			case !filepath.IsAbs(target):
				target = filepath.Join(filepath.Dir(from), target)
			}
			target = filepath.Clean(target)
			if seen[target] {
				continue
			}
			if info, err := os.Stat(target); err != nil || !info.Mode().IsRegular() {
				continue
			}
			seen[target] = true
			if b, ok := c.readFrom(session.Claude, scope, "instructions", target, from); ok {
				c.imports(scope, target, b, seen, depth+1)
			}
		}
	}
}

func stripCodeSpans(line string) string {
	parts := strings.Split(line, "`")
	for i := 1; i < len(parts); i += 2 {
		parts[i] = ""
	}
	return strings.Join(parts, " ")
}

type claudeSettings struct {
	Hooks       map[string][]hookGroup `json:"hooks"`
	Permissions struct {
		Allow []string `json:"allow"`
		Ask   []string `json:"ask"`
		Deny  []string `json:"deny"`
	} `json:"permissions"`
}

type hookGroup struct {
	Matcher string `json:"matcher"`
	Hooks   []struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	} `json:"hooks"`
}

func (c *configReader) settings(agent session.Agent, scope, path string) {
	b, ok := c.read(agent, scope, "settings", path)
	if !ok {
		return
	}
	var s claudeSettings
	if err := json.Unmarshal(b, &s); err != nil {
		c.fail(path, "JSON 格式无效")
		return
	}
	events := make([]string, 0, len(s.Hooks))
	for e := range s.Hooks {
		events = append(events, e)
	}
	slices.Sort(events)
	for _, event := range events {
		for _, g := range s.Hooks[event] {
			for _, h := range g.Hooks {
				c.out.Hooks = append(c.out.Hooks, Hook{Agent: agent, Scope: scope, Source: path, Event: event, Matcher: g.Matcher, Command: commandName(cmp.Or(h.Command, h.Type))})
			}
		}
	}
	for _, rule := range s.Permissions.Allow {
		c.out.Permissions = append(c.out.Permissions, PermissionRule{agent, scope, path, "allow", rule})
	}
	for _, rule := range s.Permissions.Ask {
		c.out.Permissions = append(c.out.Permissions, PermissionRule{agent, scope, path, "ask", rule})
	}
	for _, rule := range s.Permissions.Deny {
		c.out.Permissions = append(c.out.Permissions, PermissionRule{agent, scope, path, "deny", rule})
	}
}

type mcpEntry struct {
	Type     string `json:"type"`
	Command  string `json:"command"`
	URL      string `json:"url"`
	Disabled bool   `json:"disabled"`
}

func (c *configReader) addMCP(agent session.Agent, scope, source string, servers map[string]mcpEntry, disabled map[string]bool) {
	for name, s := range servers {
		launch := commandName(s.Command)
		if s.URL != "" {
			launch = urlHost(s.URL)
		}
		c.out.MCP = append(c.out.MCP, MCPServer{Agent: agent, Name: name, Scope: scope, Source: source, Launch: launch, Disabled: s.Disabled || disabled[name]})
	}
}

// claudeJSON reads the user-scope MCP servers of ~/.claude.json and the
// local-scope ones it keeps for dir.
func (c *configReader) claudeJSON(path, dir string) {
	b, ok := c.read(session.Claude, ScopeUser, "mcp", path)
	if !ok {
		return
	}
	var s struct {
		MCPServers map[string]mcpEntry `json:"mcpServers"`
		Projects   map[string]struct {
			MCPServers             map[string]mcpEntry `json:"mcpServers"`
			DisabledMcpjsonServers []string            `json:"disabledMcpjsonServers"`
			DisabledMcpServers     []string            `json:"disabledMcpServers"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		c.fail(path, "JSON 格式无效")
		return
	}
	disabled := map[string]bool{}
	if p, ok := s.Projects[dir]; ok && dir != "" {
		for _, n := range p.DisabledMcpServers {
			disabled[n] = true
		}
		for _, n := range p.DisabledMcpjsonServers {
			disabled[n] = true
		}
		c.addMCP(session.Claude, ScopeLocal, path, p.MCPServers, disabled)
	}
	c.addMCP(session.Claude, ScopeUser, path, s.MCPServers, disabled)
}

func (c *configReader) mcpJSON(agent session.Agent, scope, path string) {
	b, ok := c.read(agent, scope, "mcp", path)
	if !ok {
		return
	}
	var s struct {
		MCPServers map[string]mcpEntry `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		c.fail(path, "JSON 格式无效")
		return
	}
	c.addMCP(agent, scope, path, s.MCPServers, nil)
}

// codexTOML reads the [mcp_servers.<name>] tables of Codex's config.toml.
// It understands only the keys it needs: command, url and enabled.
func (c *configReader) codexTOML(path string) {
	b, ok := c.read(session.Codex, ScopeUser, "settings", path)
	if !ok {
		return
	}
	servers := map[string]mcpEntry{}
	current := ""
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			current = ""
			header := strings.Trim(strings.TrimSpace(strings.SplitN(line, "#", 2)[0]), "[]")
			if name, ok := tomlServer(header); ok {
				current = name
				servers[name] = servers[name]
			}
			continue
		}
		if current == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.Trim(strings.TrimSpace(strings.SplitN(value, " #", 2)[0]), `"'`)
		s := servers[current]
		switch key {
		case "command":
			s.Command = value
		case "url":
			s.URL = value
		case "enabled":
			s.Disabled = value == "false"
		}
		servers[current] = s
	}
	c.addMCP(session.Codex, ScopeUser, path, servers, nil)
}

// tomlServer names the server of an [mcp_servers.<name>] header; its
// sub-tables such as [mcp_servers.<name>.env] are not servers.
func tomlServer(header string) (string, bool) {
	rest, ok := strings.CutPrefix(header, "mcp_servers.")
	if !ok || rest == "" {
		return "", false
	}
	if q := rest[0]; q == '"' || q == '\'' {
		end := strings.IndexByte(rest[1:], q)
		if end < 0 || end+2 != len(rest) {
			return "", false
		}
		return rest[1 : end+1], true
	}
	return rest, !strings.Contains(rest, ".")
}

func commandName(cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(strings.Trim(fields[0], `"'`))
}

func urlHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "URL"
	}
	return u.Host
}

func shortPath(home, path string) string {
	if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
		return "~/" + filepath.ToSlash(rel)
	}
	return path
}

// MatchMCPCalls fills each server's call count from the analyzed sessions,
// which name MCP tools mcp__<server>__<tool>.
func (r *ConfigReport) MatchMCPCalls(usage []MCPUsage) {
	type key struct {
		agent  session.Agent
		server string
	}
	calls := map[key]int{}
	for _, u := range usage {
		calls[key{u.Agent, normalizeServer(u.Server)}] += u.Calls
	}
	for i := range r.MCP {
		r.MCP[i].Calls = calls[key{r.MCP[i].Agent, normalizeServer(r.MCP[i].Name)}]
	}
}

// Agents replace characters outside [A-Za-z0-9_] in server names when they
// name tools: Claude Code keeps "-", Codex turns it into "_".
func normalizeServer(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return strings.ToLower(b.String())
}

// PermissionHint is a tool people kept confirming by hand, and whether any
// allow rule already names it.
type PermissionHint struct {
	Tool    string
	Waits   int
	Covered bool
}

// PermissionHints lists tools confirmed by hand at least min times, most
// first, so they can be considered for an allow rule.
func PermissionHints(tools []ToolFriction, rules []PermissionRule, min int) []PermissionHint {
	var out []PermissionHint
	for _, t := range tools {
		if t.PermissionWaits < min || InteractiveTools[t.Name] {
			continue
		}
		h := PermissionHint{Tool: t.Name, Waits: t.PermissionWaits}
		for _, r := range rules {
			if r.Kind == "allow" && (r.Rule == t.Name || strings.HasPrefix(r.Rule, t.Name+"(")) {
				h.Covered = true
			}
		}
		out = append(out, h)
	}
	slices.SortFunc(out, func(a, b PermissionHint) int { return cmp.Or(b.Waits-a.Waits, strings.Compare(a.Tool, b.Tool)) })
	return out
}
