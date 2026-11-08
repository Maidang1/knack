package app

import (
	"embed"
	"hash/fnv"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"knack/internal/skill"
)

const svgHead = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">`

// glyphs are 24×24 outlines drawn in the color of the text. The first group
// marks places and actions of the window; the second marks agents.
var glyphs = map[string]string{
	"disk": `<rect x="3" y="3" width="18" height="18" rx="3"/><circle cx="12" cy="11" r="5"/><circle cx="12" cy="11" r="1"/><path d="m15 14 3 3M6 18h2"/>`,
	// window
	"home":          `<path d="M15 21v-8a1 1 0 0 0-1-1h-4a1 1 0 0 0-1 1v8"/><path d="M3 10a2 2 0 0 1 .7-1.5l7-6a2 2 0 0 1 2.6 0l7 6A2 2 0 0 1 21 10v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>`,
	"inbox":         `<path d="M22 12h-6l-2 3h-4l-2-3H2"/><path d="M5.5 5.1 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.5-6.9A2 2 0 0 0 16.8 4H7.2a2 2 0 0 0-1.7 1.1z"/>`,
	"messages":      `<path d="M14 9a2 2 0 0 1-2 2H6l-4 4V4a2 2 0 0 1 2-2h8a2 2 0 0 1 2 2z"/><path d="M18 9h2a2 2 0 0 1 2 2v11l-4-4h-6a2 2 0 0 1-2-2v-1"/>`,
	"chart":         `<path d="M3 3v16a2 2 0 0 0 2 2h16"/><path d="M18 17V9M13 17V5M8 17v-3"/>`,
	"search":        `<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>`,
	"user":          `<circle cx="12" cy="8" r="4"/><path d="M4.5 20.5a7.5 7.5 0 0 1 15 0"/>`,
	"file":          `<path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5"/><path d="M9 13h6M9 17h4"/>`,
	"file-edit":     `<path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h4"/><path d="M14 3v5h5"/><path d="M19.4 12.6a1.6 1.6 0 0 1 2.3 2.3L16 20.6l-3 .8.8-3z"/>`,
	"pen":           `<path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z"/>`,
	"sparkle":       `<path d="M11 3l1.8 4.9 4.9 1.8-4.9 1.8L11 16.4l-1.8-4.9-4.9-1.8 4.9-1.8z"/><path d="M18.5 14.5l.8 1.9 1.9.8-1.9.8-.8 1.9-.8-1.9-1.9-.8 1.9-.8z"/>`,
	"hourglass":     `<path d="M6 3h12M6 21h12"/><path d="M7 3c0 5 5 6 5 9s-5 4-5 9"/><path d="M17 3c0 5-5 6-5 9s5 4 5 9"/>`,
	"expand":        `<path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5"/>`,
	"file-question": `<path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5"/><path d="M10.2 11.2a1.9 1.9 0 1 1 2.6 1.8c-.5.2-.8.6-.8 1.1v.4M12 17.2v.1"/>`,
	"tick":          `<path d="m5 12.5 4.5 4.5L19 7.5"/>`,
	"info":          `<circle cx="12" cy="12" r="9"/><path d="M12 11v5.5M12 7.8v.1"/>`,
	"skip":          `<circle cx="12" cy="12" r="9"/><path d="M5.6 18.4 18.4 5.6"/>`,
	"dashed":        `<circle cx="12" cy="12" r="9" stroke-dasharray="3 3"/>`,
	"lock":          `<rect x="4.5" y="10.5" width="15" height="10" rx="2.5"/><path d="M8 10.5V7a4 4 0 0 1 8 0v3.5"/>`,
	"hand":          `<path d="M18 11V6a2 2 0 0 0-4 0v5"/><path d="M14 10V4a2 2 0 0 0-4 0v6"/><path d="M10 10.5V6a2 2 0 0 0-4 0v8"/><path d="M18 8a2 2 0 1 1 4 0v6a8 8 0 0 1-8 8h-2c-2.8 0-4.5-.9-6-2.3l-3.6-3.6a2 2 0 0 1 2.8-2.8L7 15"/>`,
	"chevron-right": `<path d="m9 6 6 6-6 6"/>`,
	"chevron-down":  `<path d="m6 9 6 6 6-6"/>`,
	"chevron-up":    `<path d="m6 15 6-6 6 6"/>`,
	"arrow-left":    `<path d="M19 12H5"/><path d="m11 6-6 6 6 6"/>`,
	"arrow-right":   `<path d="M5 12h14"/><path d="m13 6 6 6-6 6"/>`,
	"sessions":      `<path d="M4 4h16v12H9l-5 4V4z"/><path d="M8 8h8M8 12h5"/>`,
	"effective":     `<circle cx="6" cy="19" r="3"/><path d="M9 19h8.5a3.5 3.5 0 0 0 0-7h-11a3.5 3.5 0 0 1 0-7H15"/><circle cx="18" cy="5" r="3"/>`,
	"overview":      `<rect x="3.5" y="3.5" width="7" height="7" rx="2"/><rect x="13.5" y="3.5" width="7" height="7" rx="2"/><rect x="3.5" y="13.5" width="7" height="7" rx="2"/><rect x="13.5" y="13.5" width="7" height="7" rx="2"/>`,
	"pool":          `<path d="M12 3 3 8l9 5 9-5-9-5z"/><path d="m3 12.5 9 5 9-5"/><path d="m3 17 9 5 9-5"/>`,
	"issues":        `<path d="M12 3.5 2.8 19.5h18.4L12 3.5z"/><path d="M12 10v4.5"/><path d="M12 17.2v.1"/>`,
	"graph":         `<circle cx="5" cy="12" r="2.5"/><circle cx="19" cy="5.5" r="2.5"/><circle cx="19" cy="18.5" r="2.5"/><path d="M7.3 10.9 16.7 6.6"/><path d="M7.3 13.1l9.4 4.3"/>`,
	"list":          `<path d="M8.5 6h12M8.5 12h12M8.5 18h12"/><path d="M3.8 6h.1M3.8 12h.1M3.8 18h.1"/>`,
	"sidebar":       `<rect x="3" y="4.5" width="18" height="15" rx="3"/><path d="M9.5 4.5v15"/>`,
	"folder":        `<path d="M3 7.5A2.5 2.5 0 0 1 5.5 5H9l2 2.5h7.5A2.5 2.5 0 0 1 21 10v7.5a2.5 2.5 0 0 1-2.5 2.5h-13A2.5 2.5 0 0 1 3 17.5v-10z"/>`,
	"package":       `<path d="M12 3 3.5 7.5v9L12 21l8.5-4.5v-9L12 3z"/><path d="m3.5 7.5 8.5 4.5 8.5-4.5M12 12v9"/>`,
	"wand":          `<path d="M4 20 15 9"/><path d="m14 7 3 3"/><path d="M18.5 3.5v3M17 5h3"/><path d="M20 12v2.5M18.8 13.2h2.4"/><path d="M8.5 3v2M7.5 4h2"/>`,
	"gear":          `<circle cx="12" cy="12" r="3"/><path d="M9.8 5.2 10.2 2.7h3.6l.4 2.5 1.1.4 2-1.5 2.6 2.6-1.5 2 .4 1.1 2.5.4v3.6l-2.5.4-.4 1.1 1.5 2-2.6 2.6-2-1.5-1.1.4-.4 2.5h-3.6l-.4-2.5-1.1-.4-2 1.5-2.6-2.6 1.5-2-.4-1.1-2.5-.4v-3.6l2.5-.4.4-1.1-1.5-2 2.6-2.6 2 1.5z"/>`,
	"plus":          `<path d="M12 5v14M5 12h14"/>`,
	"more":          `<g fill="currentColor" stroke="none"><circle cx="5" cy="12" r="1.5"/><circle cx="12" cy="12" r="1.5"/><circle cx="19" cy="12" r="1.5"/></g>`,
	"sort":          `<path d="m3 16 4 4 4-4"/><path d="M7 20V4"/><path d="m21 8-4-4-4 4"/><path d="M17 4v16"/>`,
	"refresh":       `<path d="M20 11a8 8 0 0 0-14.3-4.5L4 8.5"/><path d="M4 4v4.5h4.5"/><path d="M4 13a8 8 0 0 0 14.3 4.5l1.7-2"/><path d="M20 20v-4.5h-4.5"/>`,
	"reveal":        `<path d="M3 7.5A2.5 2.5 0 0 1 5.5 5H9l2 2.5h7.5A2.5 2.5 0 0 1 21 10v7.5a2.5 2.5 0 0 1-2.5 2.5h-13A2.5 2.5 0 0 1 3 17.5v-10z"/><path d="m10 14 4-3"/>`,
	"copy":          `<rect x="8.5" y="8.5" width="11.5" height="11.5" rx="2.5"/><path d="M15.5 8.5V6.5A2.5 2.5 0 0 0 13 4H6.5A2.5 2.5 0 0 0 4 6.5V13a2.5 2.5 0 0 0 2.5 2.5h2"/>`,
	// how an entry sits against the pool
	"link":     `<path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1"/><path d="M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1"/>`,
	"unlink":   `<path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1"/><path d="M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1"/><path d="M4 4l16 16"/>`,
	"alert":    `<circle cx="12" cy="12" r="9"/><path d="M12 7.5v5"/><path d="M12 16.2v.1"/>`,
	"check":    `<circle cx="12" cy="12" r="9"/><path d="m8 12.3 2.8 2.7 5.2-5.6"/>`,
	"empty":    `<path stroke-dasharray="3 3" d="M3 7.5A2.5 2.5 0 0 1 5.5 5H9l2 2.5h7.5A2.5 2.5 0 0 1 21 10v7.5a2.5 2.5 0 0 1-2.5 2.5h-13A2.5 2.5 0 0 1 3 17.5v-10z"/>`,
	"external": `<path d="M14 4h6v6"/><path d="M20 4 11 13"/><path d="M18 14v4a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4"/>`,
	"nested":   `<rect x="3.5" y="3.5" width="17" height="17" rx="3"/><rect x="8" y="8" width="8" height="8" rx="1.8"/>`,
	"missing":  `<circle cx="12" cy="12" r="9" stroke-dasharray="3 3"/><path d="M9.5 9.5l5 5M14.5 9.5l-5 5"/>`,
	// agents
	"burst":    `<path d="M12 3v18M4.2 7.5l15.6 9M19.8 7.5 4.2 16.5"/>`,
	"terminal": `<rect x="2.5" y="4.5" width="19" height="15" rx="3.5"/><path d="m7 9.5 3 2.5-3 2.5"/><path d="M12.5 15h4.5"/>`,
	"cursor":   `<path d="M5 3.5 19.5 10l-6.3 2.2L11 18.7 5 3.5z"/>`,
	"star4":    `<path d="M12 3c.8 5.2 3.8 8.2 9 9-5.2.8-8.2 3.8-9 9-.8-5.2-3.8-8.2-9-9 5.2-.8 8.2-3.8 9-9z"/>`,
	"bot":      `<rect x="4" y="8.5" width="16" height="11.5" rx="3.5"/><path d="M12 8.5V5"/><circle cx="12" cy="3.8" r="1.2"/><path d="M9 14v1.2M15 14v1.2"/>`,
	"braces":   `<path d="M9 4c-2.4 0-3 1.4-3 3.4v2c0 1.4-.9 2.6-2.5 2.6 1.6 0 2.5 1.2 2.5 2.6v2c0 2 .6 3.4 3 3.4"/><path d="M15 4c2.4 0 3 1.4 3 3.4v2c0 1.4.9 2.6 2.5 2.6-1.6 0-2.5 1.2-2.5 2.6v2c0 2-.6 3.4-3 3.4"/>`,
	"leaf":     `<path d="M5 19C5 10 10 5 20 4c0 10-5 15-15 15z"/><path d="M5 19 13 11"/>`,
	"ghost":    `<path d="M5 20.5V11a7 7 0 0 1 14 0v9.5l-3.5-2.4L12 20.5l-3.5-2.4L5 20.5z"/><path d="M9.5 10.5v1.8M14.5 10.5v1.8"/>`,
	"rocket":   `<path d="M12 3c4 2 6 6 6 10l-3 3H9l-3-3c0-4 2-8 6-10z"/><circle cx="12" cy="10" r="1.7"/><path d="M9 16l-2 4.5M15 16l2 4.5"/>`,
	"hexagon":  `<path d="M12 3l8 4.5v9L12 21l-8-4.5v-9L12 3z"/><circle cx="12" cy="12" r="2.6"/>`,
	"zap":      `<path d="M13.5 3 5 14h6l-1 7 8.5-11h-6l1-7z"/>`,
	"compass":  `<circle cx="12" cy="12" r="9"/><path d="m15.8 8.2-2.1 5.5-5.5 2.1 2.1-5.5 5.5-2.1z"/>`,
	"code":     `<path d="m8 8-4 4 4 4M16 8l4 4-4 4M13.5 5l-3 14"/>`,
	"flame":    `<path d="M12 3c1 4.2 5.2 5.6 5.2 10.2a5.2 5.2 0 0 1-10.4 0c0-2.2 1-3.4 2.2-4.4.2 1.8 1 2.8 2 3 .2-3-1-5.4 1-8.8z"/>`,
	"atom":     `<circle cx="12" cy="12" r="1.6"/><ellipse cx="12" cy="12" rx="9" ry="3.6"/><ellipse cx="12" cy="12" rx="9" ry="3.6" transform="rotate(60 12 12)"/><ellipse cx="12" cy="12" rx="9" ry="3.6" transform="rotate(120 12 12)"/>`,
	"cloud":    `<path d="M7.5 18.5a4.3 4.3 0 0 1-.6-8.5 5.7 5.7 0 0 1 10.9-1.2 4.9 4.9 0 0 1-.3 9.7H7.5z"/>`,
	"book":     `<path d="M5 4.5A1.5 1.5 0 0 1 6.5 3H19v15H6.5A1.5 1.5 0 0 0 5 19.5V4.5z"/><path d="M5 19.5A1.5 1.5 0 0 0 6.5 21H19v-3"/>`,
	"wave":     `<path d="M3 12c2-4 4-4 6 0s4 4 6 0 4-4 6 0"/>`,
	"diamond":  `<path d="M12 3l8 9-8 9-8-9 8-9z"/>`,
	"bolt":     `<circle cx="12" cy="12" r="9"/><path d="M12.5 6.5 8.5 12.5H12l-.5 5 4-6H12l.5-5z"/>`,
}

// logoFS holds each agent's own mark, one single-color file named by the
// agent's key (see agentKey). They draw with currentColor, so a tile shows
// them in white on the agent's color like any other glyph.
//
//go:embed logos/*.svg logos/color/*.svg
var logoFS embed.FS

var svgs = func() map[string]*ui.SVG {
	out := make(map[string]*ui.SVG, len(glyphs))
	for name, body := range glyphs {
		out[name] = ui.MustParseSVG([]byte(svgHead + body + `</svg>`))
	}
	for prefix, dir := range map[string]string{"logo:": "logos", "color:": "logos/color"} {
		files, _ := logoFS.ReadDir(dir)
		for _, f := range files {
			data, err := logoFS.ReadFile(dir + "/" + f.Name())
			if err != nil {
				continue
			}
			if svg, err := ui.ParseSVG(data); err == nil {
				out[prefix+strings.TrimSuffix(f.Name(), ".svg")] = svg
			}
		}
	}
	return out
}()

func glyph(name string) *ui.SVG { return svgs[name] }

// look is how an agent shows up: a glyph and the color it sits on.
type look struct {
	glyph string
	color ui.Color
}

var knownLooks = map[string]look{
	"claude":      {"burst", ui.Hex("#d97757")},
	"codex":       {"terminal", ui.Hex("#10a37f")},
	"cursor":      {"cursor", ui.Hex("#52525b")},
	"gemini":      {"star4", ui.Hex("#4285f4")},
	"antigravity": {"compass", ui.Hex("#7c3aed")},
	"cline":       {"bot", ui.Hex("#8b5cf6")},
	"opencode":    {"braces", ui.Hex("#0ea5e9")},
	"goose":       {"leaf", ui.Hex("#f59e0b")},
	"kiro":        {"ghost", ui.Hex("#9046ff")},
	"trae":        {"rocket", ui.Hex("#16a34a")},
	"grok":        {"zap", ui.Hex("#3f3f46")},
	"qwen":        {"hexagon", ui.Hex("#615ced")},
	"continue":    {"code", ui.Hex("#e11d48")},
	"crush":       {"flame", ui.Hex("#ec4899")},
	"factory":     {"atom", ui.Hex("#ea580c")},
	"snowflake":   {"cloud", ui.Hex("#0891b2")},
	"hermes":      {"wave", ui.Hex("#0d9488")},
	"mcpjam":      {"diamond", ui.Hex("#be123c")},
	"junie":       {"bolt", ui.Hex("#db2777")},
	"roo":         {"bot", ui.Hex("#2563eb")},
	"kilocode":    {"code", ui.Hex("#ca8a04")},
	"openhands":   {"hexagon", ui.Hex("#475569")},
	"augment":     {"star4", ui.Hex("#059669")},
	"agents":      {"layers", ui.Hex("#2563eb")},
	"deepseek":    {"code", ui.Hex("#4d6bfe")},
	"minimax":     {"package", ui.Hex("#f23f5d")},
}

var spare = []string{"bot", "book", "diamond", "hexagon", "wave", "code", "star4", "atom", "bolt", "flame"}

var palette = []ui.Color{
	ui.Hex("#2563eb"), ui.Hex("#dc2626"), ui.Hex("#16a34a"), ui.Hex("#d97706"),
	ui.Hex("#7c3aed"), ui.Hex("#0891b2"), ui.Hex("#db2777"), ui.Hex("#4f46e5"),
	ui.Hex("#0d9488"), ui.Hex("#ea580c"),
}

// agentKey names the agent a skills directory belongs to by the first
// folder under home that is not a dot folder of configuration: ~/.claude
// gives "claude", ~/.config/opencode gives "opencode".
func agentKey(home, path string) string {
	rel, err := filepath.Rel(home, path)
	if err != nil {
		return ""
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if parts[0] == ".gemini" && len(parts) > 1 && strings.HasPrefix(parts[1], "antigravity") {
		return "antigravity"
	}
	if parts[0] == ".config" && len(parts) > 1 {
		return strings.ToLower(strings.TrimPrefix(parts[1], "."))
	}
	return strings.ToLower(strings.TrimPrefix(parts[0], "."))
}

// lookOf picks the look of a root: the pool and repositories have their
// own, an agent keeps the one it is known by, and any other gets a stable
// glyph and color made from its name.
func lookOf(home string, r skill.Root) look {
	switch r.Role {
	case skill.RolePool:
		return look{"pool", ui.Hex("#2563eb")}
	case skill.RoleRepo:
		return look{"folder", ui.Hex("#64748b")}
	}
	key := agentKey(home, r.Path)
	logo := "logo:" + key
	if l, ok := knownLooks[key]; ok {
		if l.glyph == "layers" {
			l.glyph = "pool"
		}
		if svgs[logo] != nil {
			l.glyph = logo
		}
		return l
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	sum := h.Sum32()
	l := look{spare[int(sum)%len(spare)], palette[int(sum>>8)%len(palette)]}
	if svgs[logo] != nil {
		l.glyph = logo
	} else if r.Role == skill.RoleBuiltin {
		l.glyph = "package"
	}
	return l
}

// tile draws a look as a rounded square in its color, with its glyph in white.
func tile(c *ui.Context, l look, size float32) {
	ui.Box(c).Size(size, size).Shrink(0).Radius(size * 0.28).Background(l.color).
		AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		ui.Icon(c, glyph(l.glyph)).FontSize(size * 0.64).TextColor(ui.RGB(255, 255, 255))
	})
}

// relationGlyph is the small icon that stands before an entry's row.
func relationGlyph(r skill.Relation) string {
	switch r {
	case skill.RelPool:
		return "pool"
	case skill.RelLinked:
		return "link"
	case skill.RelSame, skill.RelCopy:
		return "copy"
	case skill.RelStale:
		return "alert"
	case skill.RelLocal:
		return "check"
	case skill.RelExternal:
		return "external"
	case skill.RelBroken:
		return "unlink"
	case skill.RelEmpty:
		return "empty"
	case skill.RelSystem:
		return "package"
	default:
		return "nested"
	}
}

// issueGlyph is the small icon of an issue kind.
func issueGlyph(k skill.IssueKind) string {
	switch k {
	case skill.IssueBroken:
		return "unlink"
	case skill.IssueStale:
		return "alert"
	case skill.IssueDuplicate:
		return "copy"
	case skill.IssueEmpty:
		return "empty"
	case skill.IssueOrphan:
		return "pool"
	}
	return "alert"
}

// sideLogo is the mark an Agent has in a list: its full-color logo, else its
// single-color one, which an Image draws in the text color. It is nil when
// the Agent has no logo.
func sideLogo(home string, r skill.Root) *ui.SVG {
	key, ok := strings.CutPrefix(lookOf(home, r).glyph, "logo:")
	if !ok {
		return nil
	}
	if svg := svgs["color:"+key]; svg != nil {
		return svg
	}
	return svgs["logo:"+key]
}
