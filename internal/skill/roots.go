package skill

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// knownNames are the skill directories on this machine, named the way the
// user talks about them. Discovery checks only these fixed paths.
var knownNames = map[string]string{
	".agents/skills":                         "共享池",
	".adal/skills":                           "Adal",
	".aiden/skills":                          "Aiden",
	".augment/skills":                        "Augment",
	".bifrost/agent/skills":                  "Bifrost",
	".bob/skills":                            "Bob",
	".botmux/claude-plugin/skills":           "Botmux",
	".claude/skills":                         "Claude",
	".cline/skills":                          "Cline",
	".coco/skills":                           "Coco",
	".codebuddy/skills":                      "CodeBuddy",
	".codex/skills":                          "Codex",
	".codin/skills":                          "Codin",
	".commandcode/skills":                    "Command Code",
	".config/agents/skills":                  "agents 配置",
	".config/crush/skills":                   "Crush",
	".config/goose/skills":                   "Goose",
	".config/opencode/skills":                "OpenCode",
	".continue/skills":                       "Continue",
	".cursor/skills":                         "Cursor",
	".cursor/skills-cursor":                  "Cursor 自带",
	".deepseek/skills":                       "DeepSeek",
	".factory/skills":                        "Factory",
	".gemini/antigravity-cli/builtin/skills": "Antigravity CLI 自带",
	".gemini/antigravity-ide/builtin/skills": "Antigravity IDE 自带",
	".gemini/antigravity/builtin/skills":     "Antigravity 自带",
	".gemini/skills":                         "Gemini",
	".genius/skills":                         "Genius",
	".grok/bundled/skills":                   "Grok 自带",
	".hermes/skills":                         "Hermes",
	".iflow/skills":                          "iFlow",
	".junie/skills":                          "Junie",
	".kilocode/skills":                       "Kilo Code",
	".kimi_openclaw/workspace/skills":        "Kimi OpenClaw",
	".kiro/skills":                           "Kiro",
	".kode/skills":                           "Kode",
	".mcpjam/skills":                         "MCPJam",
	".minimax/.builtin-skills":               "MiniMax 自带",
	".mux/skills":                            "Mux",
	".nanobot/workspace/skills":              "Nanobot",
	".neeko/skills":                          "Neeko",
	".neovate/skills":                        "Neovate",
	".openhands/skills":                      "OpenHands",
	".pencil/skills":                         "Pencil",
	".pi/agent/skills":                       "Pi",
	".pochi/skills":                          "Pochi",
	".qoder/skills":                          "Qoder",
	".qwen/skills":                           "Qwen",
	".roo/skills":                            "Roo",
	".snowflake/cortex/skills":               "Snowflake",
	".trae/cli/memories/skills":              "Trae 记忆",
	".trae/skills":                           "Trae",
	".verdent/skills":                        "Verdent",
	".vibe/skills":                           "Vibe",
	".zencoder/skills":                       "Zencoder",
}

// Discover lists skill directories under home: the shared pool, each
// agent's skills folder, and the copies apps ship themselves. It does not
// hash anything.
func Discover(home string) []Root {
	home = filepath.Clean(home)
	seen := map[string]Root{}
	add := func(path string) {
		path = filepath.Clean(path)
		if _, ok := seen[path]; ok {
			return
		}
		st, err := os.Stat(path)
		if os.IsNotExist(err) || err == nil && !st.IsDir() {
			return
		}
		// Keep inaccessible fixed roots so Scan can report their error.
		seen[path] = rootAt(home, path)
	}
	for rel := range knownNames {
		add(filepath.Join(home, filepath.FromSlash(rel)))
	}
	roots := make([]Root, 0, len(seen))
	for _, r := range seen {
		roots = append(roots, r)
	}
	slices.SortFunc(roots, func(a, b Root) int {
		if c := roleRank(a.Role) - roleRank(b.Role); c != 0 {
			return c
		}
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.Path, b.Path)
	})
	return roots
}

func roleRank(r Role) int {
	switch r {
	case RolePool:
		return 0
	case RoleAgent:
		return 1
	case RoleBuiltin:
		return 2
	default:
		return 3
	}
}

func rootAt(home, path string) Root {
	rel, err := filepath.Rel(home, path)
	if err != nil {
		rel = path
	}
	rel = filepath.ToSlash(rel)
	role := RoleAgent
	switch {
	case rel == ".agents/skills":
		role = RolePool
	case isBuiltin(rel):
		role = RoleBuiltin
	}
	name := knownNames[rel]
	if name == "" {
		name = humanize(rel)
	}
	return Root{ID: rootID(rel, role), Name: name, Path: path, Role: role}
}

func rootID(rel string, role Role) string {
	if role == RolePool {
		return "pool"
	}
	s := strings.Trim(rel, ".")
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.Trim(s, "-")
	return s
}

func isBuiltin(rel string) bool {
	s := "/" + rel
	return strings.Contains(s, "/builtin/") ||
		strings.HasSuffix(s, "/skills-cursor") ||
		strings.Contains(s, "/.builtin-skills") ||
		strings.Contains(s, "/.grok/bundled/") ||
		strings.Contains(s, "/vendor_imports/")
}

func humanize(rel string) string {
	parts := strings.Split(rel, "/")
	var keep []string
	for _, p := range parts {
		switch p {
		case "skills", "skills-cursor", "builtin", ".builtin-skills", "agent", "cli", "memories", "workspace", "cortex":
			continue
		}
		p = strings.TrimPrefix(p, ".")
		if p == "" {
			continue
		}
		keep = append(keep, p)
	}
	if len(keep) == 0 {
		return rel
	}
	return strings.Join(keep, " / ")
}
