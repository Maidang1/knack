package insight

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"knack/internal/session"
	"knack/internal/skill"
)

func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens("abcdefgh"); got != 2 {
		t.Fatalf("latin %d", got)
	}
	if got := EstimateTokens("用于生成图片"); got != 6 {
		t.Fatalf("cjk %d", got)
	}
}

func TestAnalyzeSkills(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	root := skill.Root{ID: "claude", Name: "Claude", Path: "/h/.claude/skills", Role: skill.RoleAgent}
	pool := skill.Root{ID: "pool", Name: "共享池", Path: "/h/.agents/skills", Role: skill.RolePool}
	entry := func(name, desc string) skill.Entry {
		return skill.Entry{RootID: root.ID, Name: name, Kind: skill.KindSkill, HasDocument: true, FrontName: name, Description: desc}
	}
	pdf := "Extract text and tables from PDF files, fill PDF forms, merge and split PDF documents. Use when working with PDF files."
	pdf2 := "Extract text and tables from PDF files, merge and split PDF documents, fill forms. Use when the user has PDF files."
	skills := &skill.Snapshot{
		Roots: []skill.Root{root, pool},
		Entries: []skill.Entry{
			entry("pdf", pdf),
			entry("pdf-tools", pdf2),
			entry("deploy", "Deploys the app to production servers with rollback support, health checks and notifications for the team."),
			{RootID: root.ID, Name: "empty-desc", Kind: skill.KindSkill, HasDocument: true},
			{RootID: root.ID, Name: "gone", Kind: skill.KindBroken},
			{RootID: root.ID, Name: "named", Kind: skill.KindSkill, HasDocument: true, FrontName: "other", Description: "短描述"},
			{RootID: pool.ID, Name: "pooled", Kind: skill.KindSkill, HasDocument: true, Description: "not loaded directly"},
		},
	}
	sessions := &session.Snapshot{Sessions: []session.Session{{Agent: session.Claude, Turns: []session.Turn{{
		Skills: []session.SkillCall{
			{Name: "pdf", At: now.AddDate(0, 0, -2)},
			{Name: "pdf", At: now.AddDate(0, 0, -45)},
			{Name: "plugin:deploy", At: now.AddDate(0, 0, -200)},
		},
	}}}}}
	r := AnalyzeSkills(skills, sessions, func(skill.Root) session.Agent { return session.Claude }, func(session.Agent) bool { return true }, now)
	by := map[string]SkillHealth{}
	for _, h := range r.Skills {
		by[h.Name] = h
	}
	if _, ok := by["pooled"]; ok {
		t.Fatal("pool entries are not loaded by an Agent")
	}
	if h := by["pdf"]; h.Calls30 != 1 || h.Calls90 != 2 || h.Unused() || h.DescTokens == 0 {
		t.Fatalf("pdf %+v", h)
	}
	if h := by["deploy"]; !h.Unused() || h.LastUsed.IsZero() {
		t.Fatalf("plugin-qualified calls count, but not within 90 days: %+v", h)
	}
	if h := by["empty-desc"]; len(h.Findings) == 0 || h.Findings[0].Severity != 2 {
		t.Fatalf("missing description %+v", h.Findings)
	}
	if h := by["gone"]; len(h.Findings) == 0 || !strings.Contains(h.Findings[0].Text, "软链") {
		t.Fatalf("broken %+v", h.Findings)
	}
	if h := by["named"]; len(h.Findings) < 2 {
		t.Fatalf("short description and name mismatch %+v", h.Findings)
	}
	if len(r.Overlaps) != 1 || r.Overlaps[0].A != "pdf" || r.Overlaps[0].B != "pdf-tools" {
		t.Fatalf("overlaps %+v", r.Overlaps)
	}
	if len(by["pdf"].OverlapsWith) != 1 {
		t.Fatalf("overlap not attached %+v", by["pdf"])
	}
	if len(r.Roots) != 1 || r.Roots[0].Skills != 6 || r.Roots[0].Unused != 4 || r.Roots[0].StandingTokens == 0 {
		t.Fatalf("roots %+v", r.Roots)
	}
	r = AnalyzeSkills(skills, sessions, func(skill.Root) session.Agent { return session.Codex }, func(session.Agent) bool { return false }, now)
	if r.Roots[0].Unused != 0 {
		t.Fatal("unknown usage is not reported as unused")
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadConfig(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "code", "app")
	dirJSON, err := json.Marshal(dir)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(home, ".claude", "settings.json"), `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"\"/usr/local/bin/guard\" --token SECRET"}]}]},"permissions":{"allow":["Read","Bash(go test:*)"],"deny":["WebFetch"]},"env":{"API_KEY":"SECRET"}}`)
	write(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"github":{"type":"stdio","command":"npx","args":["-y","SECRET"],"env":{"T":"SECRET"}},"docs.site":{"type":"http","url":"https://mcp.example.com/path?key=SECRET"}},"projects":{`+string(dirJSON)+`:{"mcpServers":{"local-db":{"command":"db-mcp"}},"disabledMcpServers":["github"]}}}`)
	write(t, filepath.Join(home, ".claude", "CLAUDE.md"), "@RTK.md and @~/shared/rules.md, not `@code.md` or user@example.com\n```\n@fenced.md\n```\n")
	write(t, filepath.Join(home, ".claude", "RTK.md"), strings.Repeat("word ", 100)+"\n@CLAUDE.md loops back")
	write(t, filepath.Join(home, "shared", "rules.md"), "rules")
	write(t, filepath.Join(home, ".claude", "code.md"), "x")
	write(t, filepath.Join(home, ".claude", "fenced.md"), "x")
	write(t, filepath.Join(home, "code", "CLAUDE.md"), "outer")
	write(t, filepath.Join(dir, "CLAUDE.md"), "inner")
	write(t, filepath.Join(dir, ".mcp.json"), `{"mcpServers":{"proj":{"command":"node server.js"}}}`)
	write(t, filepath.Join(dir, ".claude", "settings.local.json"), `not json`)
	write(t, filepath.Join(home, ".codex", "config.toml"), `
model = "x"
[mcp_servers.search]
command = "search-mcp" # comment
args = ["SECRET"]
[mcp_servers.search.env]
command = "SHOULD_NOT_OVERRIDE"
[mcp_servers."web.api"]
url = "https://api.example.com/mcp"
enabled = false
[projects."/x"]
command = "not a server"
`)
	write(t, filepath.Join(home, ".codex", "AGENTS.md"), "codex rules")

	r := ReadConfig(home, dir)
	all := ""
	for _, m := range r.MCP {
		all += m.Name + m.Launch + m.Source
	}
	for _, h := range r.Hooks {
		all += h.Command + h.Matcher
	}
	if strings.Contains(all, "SECRET") {
		t.Fatal("arguments, environment or URLs leaked")
	}
	mcp := map[string]MCPServer{}
	for _, m := range r.MCP {
		mcp[string(m.Agent)+"/"+m.Name] = m
	}
	if m := mcp["claude/github"]; m.Launch != "npx" || !m.Disabled || m.Scope != ScopeUser {
		t.Fatalf("github %+v", m)
	}
	if m := mcp["claude/docs.site"]; m.Launch != "mcp.example.com" {
		t.Fatalf("http server %+v", m)
	}
	if m := mcp["claude/local-db"]; m.Scope != ScopeLocal {
		t.Fatalf("local %+v", m)
	}
	if m := mcp["claude/proj"]; m.Scope != ScopeProject || m.Launch != "node" {
		t.Fatalf("project %+v", m)
	}
	if m := mcp["codex/search"]; m.Launch != "search-mcp" || m.Disabled {
		t.Fatalf("codex search %+v", m)
	}
	if m := mcp["codex/web.api"]; m.Launch != "api.example.com" || !m.Disabled {
		t.Fatalf("codex quoted %+v", m)
	}
	if len(r.MCP) != 6 {
		t.Fatalf("servers %+v", r.MCP)
	}
	if len(r.Hooks) != 1 || r.Hooks[0].Command != "guard" || r.Hooks[0].Event != "PreToolUse" {
		t.Fatalf("hooks %+v", r.Hooks)
	}
	if len(r.Permissions) != 3 {
		t.Fatalf("permissions %+v", r.Permissions)
	}
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "settings.local.json") {
		t.Fatalf("warnings %v", r.Warnings)
	}
	var claudeMD []string
	for _, f := range r.Files {
		if f.Kind == "instructions" && f.Agent == session.Claude {
			claudeMD = append(claudeMD, f.Path)
		}
	}
	if len(claudeMD) != 5 || claudeMD[1] != filepath.Join(home, ".claude", "RTK.md") || claudeMD[2] != filepath.Join(home, "shared", "rules.md") || claudeMD[3] != filepath.Join(home, "code", "CLAUDE.md") {
		t.Fatalf("instruction layers %v", claudeMD)
	}
	if r.InstructionTokens[session.Claude] < 100 || r.InstructionTokens[session.Codex] == 0 {
		t.Fatalf("tokens %v", r.InstructionTokens)
	}

	r.MatchMCPCalls([]MCPUsage{{Agent: session.Claude, Server: "docs_site", Calls: 4}, {Agent: session.Codex, Server: "search", Calls: 2}})
	for _, m := range r.MCP {
		if m.Name == "docs.site" && m.Calls != 4 || m.Agent == session.Codex && m.Name == "search" && m.Calls != 2 || m.Agent == session.Claude && m.Name == "github" && m.Calls != 0 {
			t.Fatalf("calls %+v", m)
		}
	}
	hints := PermissionHints([]ToolFriction{{Name: "Bash", PermissionWaits: 5}, {Name: "Edit", PermissionWaits: 3}, {Name: "Grep", PermissionWaits: 1}, {Name: "AskUserQuestion", PermissionWaits: 9}},
		r.Permissions, 2)
	if len(hints) != 2 || hints[0].Tool != "Bash" || !hints[0].Covered || hints[1].Covered {
		t.Fatalf("hints %+v", hints)
	}
}
