package app

import (
	"embed"
	"hash/fnv"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/lucide"

	"knack/internal/skill"
)

const svgHead = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">`

// uiGlyphs mark the places and actions of the window, as Lobe UI marks
// them: Lucide's icons.
var uiGlyphs = map[string]func() *ui.SVG{
	"disk":          lucide.HardDrive,
	"home":          lucide.House,
	"inbox":         lucide.Inbox,
	"messages":      lucide.MessagesSquare,
	"chart":         lucide.ChartColumn,
	"search":        lucide.Search,
	"user":          lucide.User,
	"file":          lucide.FileText,
	"file-edit":     lucide.FilePen,
	"pen":           lucide.PenLine,
	"sparkle":       lucide.Sparkles,
	"hourglass":     lucide.Hourglass,
	"expand":        lucide.Expand,
	"file-question": lucide.FileQuestionMark,
	"tick":          lucide.Check,
	"info":          lucide.Info,
	"skip":          lucide.Ban,
	"dashed":        lucide.CircleDashed,
	"lock":          lucide.Lock,
	"hand":          lucide.Hand,
	"chevron-right": lucide.ChevronRight,
	"chevron-down":  lucide.ChevronDown,
	"chevron-up":    lucide.ChevronUp,
	"arrow-left":    lucide.ArrowLeft,
	"arrow-right":   lucide.ArrowRight,
	"sessions":      lucide.MessageSquareText,
	"effective":     lucide.Route,
	"overview":      lucide.LayoutGrid,
	"pool":          lucide.Layers,
	"issues":        lucide.TriangleAlert,
	"graph":         lucide.Share2,
	"list":          lucide.List,
	"sidebar":       lucide.PanelLeft,
	"folder":        lucide.Folder,
	"package":       lucide.Package,
	"wand":          lucide.WandSparkles,
	"gear":          lucide.Settings,
	"plus":          lucide.Plus,
	"minus":         lucide.Minus,
	"more":          lucide.Ellipsis,
	"sort":          lucide.ArrowUpDown,
	"refresh":       lucide.RefreshCw,
	"reveal":        lucide.FolderOpen,
	"copy":          lucide.Copy,
	"link":          lucide.Link,
	"unlink":        lucide.Unlink,
	"alert":         lucide.CircleAlert,
	"check":         lucide.CircleCheck,
	"external":      lucide.ExternalLink,
	"missing":       lucide.CircleX,
	"empty":         lucide.FolderDot,
	"nested":        lucide.SquareDashed,
}

// glyphs are 24×24 outlines drawn in the color of the text, marking agents.
var glyphs = map[string]string{
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
	out := make(map[string]*ui.SVG, len(glyphs)+len(uiGlyphs))
	for name, body := range glyphs {
		out[name] = ui.MustParseSVG([]byte(svgHead + body + `</svg>`))
	}
	for name, icon := range uiGlyphs {
		out[name] = icon()
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
