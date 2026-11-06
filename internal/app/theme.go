package app

import "github.com/egoist/mygo/ui"

// The design tokens: cool neutral chrome with a faint violet cast, and one
// violet accent for what is interactive or chosen (the primary action, the
// selected row, focus). Data keeps its own hues (sessionPalette), none of
// them violet, so the accent never reads as a series. Values are derived in
// OKLCH (neutrals at hue 286, accent at 292) and stored as hex; text pairs
// clear WCAG AA in both appearances. Each pair is light, dark.
var tokens = map[string][2]string{
	"bg":           {"#F8F8FA", "#0F0F14"},
	"surface":      {"#FFFFFF", "#15151C"},
	"sidebar":      {"#F2F2F6", "#111117"},
	"line":         {"#E3E3E9", "#26262F"},
	"line-strong":  {"#D2D2DA", "#363540"},
	"ink":          {"#16161F", "#F0F0F3"},
	"ink-2":        {"#4F4E5B", "#C3C3CC"},
	"ink-3":        {"#6D6D79", "#9D9DA7"},
	"hover":        {"#ECECF3", "#201F28"},
	"pressed":      {"#E2E2EB", "#292934"},
	"selected":     {"#EDEAFE", "#322853"},
	"raised":       {"#FFFFFF", "#3F3E4A"},
	"on-ink":       {"#FFFFFF", "#0F0F14"},
	"accent":       {"#703FD5", "#7D53E1"},
	"accent-hover": {"#6134BD", "#7046CF"},
	"accent-ink":   {"#6335BE", "#B9A9FE"},
	"accent-bg":    {"#F1EFFE", "#231E39"},
	"on-accent":    {"#FFFFFF", "#FFFFFF"},
	"ok":           {"#1C7F4C", "#65C98C"},
	"ok-bg":        {"#E4F5E9", "#122D1D"},
	"warn":         {"#A46603", "#EBAE51"},
	"warn-bg":      {"#FDF1D9", "#32230E"},
	"warn-ink":     {"#694413", "#F0D49B"},
	"warn-line":    {"#E6D2A8", "#5E4925"},
	"danger":       {"#C63A2A", "#F47C6B"},
	"danger-bg":    {"#FFEBE7", "#3B1C1A"},
	"diff-add":     {"#E5F7EB", "#152C1E"},
	"diff-del":     {"#FFECEA", "#3A1D1B"},
	"code-bg":      {"#15151E", "#07070C"},
	"code-ink":     {"#D7D7DE", "#D7D7DE"},
}

// token returns a design token for the theme's appearance.
func token(t *ui.Theme, name string) ui.Color {
	v, ok := tokens[name]
	if !ok {
		panic("unknown design token " + name)
	}
	if t.Dark {
		return ui.Hex(v[1])
	}
	return ui.Hex(v[0])
}

// monoFont sets what is typed rather than read: paths, code, commands and
// rules. Names and figures stay in the system face, as macOS sets them;
// figures get tabular digits (number) so columns still line up.
const monoFont = "SF Mono, Menlo, monospace"

// appTheme maps the design tokens onto the toolkit's theme. The accent is
// violet: primary buttons, checked controls and the focus ring; text
// selection is a wash of it.
func appTheme(base *ui.Theme) *ui.Theme {
	t := *base
	tk := func(name string) ui.Color { return token(&t, name) }
	t.Radius = 6
	t.Background = tk("bg")
	t.Surface = tk("surface")
	t.SurfaceHover = tk("hover")
	t.SurfacePressed = tk("pressed")
	t.Border = tk("line")
	t.Text = tk("ink")
	t.TextMuted = tk("ink-2")
	t.Accent = tk("accent")
	t.AccentHover = tk("accent-hover")
	t.AccentPressed = tk("accent-hover")
	t.AccentText = tk("on-accent")
	t.Success = tk("ok")
	t.Warning = tk("warn")
	t.Danger = tk("danger")
	t.Inverse = tk("ink")
	t.InverseText = tk("on-ink")
	t.Focus = fade(tk("accent"), 170)
	t.Selection = fade(tk("accent"), 72)
	return &t
}

// fade returns col see-through, for a tinted fill behind its own color.
func fade(col ui.Color, a uint8) ui.Color {
	col.A = a
	return col
}

// card is a grouped panel: surface fill, hairline border, no shadow, the
// corner radius of a macOS grouped list.
func card(c *ui.Context) ui.Element {
	t := c.Theme()
	return ui.Column(c).Radius(8).Background(t.Surface).Border(1, t.Border)
}

// sectionTitle heads a group of content: a heavy label with an optional
// muted note trailing it.
func sectionTitle(c *ui.Context, title, note string) {
	t := c.Theme()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, title).FontSize(15).FontWeight(600)
		if note != "" {
			ui.Text(c, note).FontSize(13).TextColor(t.TextMuted)
		}
	})
}

// pill is a status word in its own color. macOS lists show state as text,
// not as a tinted capsule.
func pill(c *ui.Context, s string, col ui.Color) {
	ui.Text(c, s).FontSize(11.5).TextColor(col).Shrink(0).SingleLine()
}

// tintedTile draws glyph in its color on a faint wash of the same color.
func tintedTile(c *ui.Context, g string, col ui.Color, size float32) {
	ui.Box(c).Size(size, size).Shrink(0).Radius(size * 0.3).Background(fade(col, 24)).
		AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		ui.Icon(c, glyph(g)).FontSize(size * 0.56).TextColor(col)
	})
}

// blend mixes k of a with 1-k of b, opaque: a tint of a data color that
// stays solid over any background.
func blend(a, b ui.Color, k float32) ui.Color {
	mix := func(x, y uint8) uint8 { return uint8(float32(x)*k + float32(y)*(1-k) + 0.5) }
	return ui.Color{R: mix(a.R, b.R), G: mix(a.G, b.G), B: mix(a.B, b.B), A: 255}
}
