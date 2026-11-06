package app

import "github.com/egoist/mygo/ui"

// The design tokens of the Pencil file (knack.pen): neutral chrome in warm
// grays with ink for emphasis and selection; color is kept for data and for
// states that need attention. Each pair is light, dark.
var tokens = map[string][2]string{
	"bg":          {"#F6F6F4", "#161618"},
	"surface":     {"#FFFFFF", "#1E1E21"},
	"sidebar":     {"#EEEEEB", "#19191B"},
	"line":        {"#E4E4E0", "#2C2C30"},
	"line-strong": {"#D2D2CC", "#3C3C42"},
	"ink":         {"#1A1A1C", "#EDEDEF"},
	"ink-2":       {"#55555B", "#B4B4BA"},
	"ink-3":       {"#8A8A90", "#85858C"},
	"hover":       {"#E6E6E2", "#2A2A2E"},
	"pressed":     {"#DCDCD7", "#34343A"},
	"raised":      {"#FFFFFF", "#2E2E33"},
	"on-ink":      {"#FFFFFF", "#161618"},
	"focus":       {"#2F6BFF", "#5B8CFF"},
	"ok":          {"#2F8F5B", "#5BC48A"},
	"ok-bg":       {"#E7F3EC", "#1B3325"},
	"warn":        {"#B7791F", "#E0A84A"},
	"warn-bg":     {"#FBF1DE", "#33281A"},
	"warn-ink":    {"#6B4A12", "#F2D49A"},
	"warn-line":   {"#E6CF9F", "#5C4A22"},
	"danger":      {"#C2412D", "#F07A63"},
	"danger-bg":   {"#FBE9E5", "#3A201B"},
	"diff-add":    {"#EAF5EE", "#1C3124"},
	"diff-del":    {"#FBECEA", "#3A2220"},
	"code-bg":     {"#1C1C1F", "#0E0E10"},
	"code-ink":    {"#D4D4D8", "#D4D4D8"},
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

// monoFont sets identifiers, paths and figures, as the design does.
const monoFont = "SF Mono, Menlo, monospace"

// appTheme maps the design tokens onto the toolkit's theme. Ink is the
// accent: primary buttons and checked controls are ink, links are text.
// The focus ring stays blue so keyboard focus never reads as selection.
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
	t.Accent = tk("ink")
	t.AccentHover = tk("ink-2")
	t.AccentPressed = tk("ink")
	t.AccentText = tk("on-ink")
	t.Success = tk("ok")
	t.Warning = tk("warn")
	t.Danger = tk("danger")
	t.Inverse = tk("ink")
	t.InverseText = tk("on-ink")
	focus := tk("focus")
	t.Focus = fade(focus, 150)
	t.Selection = fade(focus, 64)
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
		ui.Text(c, title).FontSize(14).FontWeight(600)
		if note != "" {
			ui.Text(c, note).FontSize(12).TextColor(t.TextMuted)
		}
	})
}

// pill is a status word in its own color. macOS lists show state as text,
// not as a tinted capsule.
func pill(c *ui.Context, s string, col ui.Color) {
	ui.Text(c, s).FontSize(11).TextColor(col).Shrink(0).SingleLine()
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
