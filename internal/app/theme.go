package app

import (
	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/layout"
	lobe "github.com/lobehub/lobe-mygo/pkg/theme"
)

// The design tokens, named for what they do here and drawn from Lobe UI's
// tokens (lobe-mygo's theme package), so the pages and the Lobe components
// they hold share one palette: neutral chrome, a near-black primary, and
// see-through fills for hover, press and choice. Data keeps its own hues
// (sessionPalette).
var tokens = map[string]func(*lobe.Tokens) ui.Color{
	"bg":           func(k *lobe.Tokens) ui.Color { return k.BgLayout },
	"surface":      func(k *lobe.Tokens) ui.Color { return k.BgContainer },
	"sidebar":      func(k *lobe.Tokens) ui.Color { return flatten(k.FillTertiary, k.BgLayout) },
	"line":         func(k *lobe.Tokens) ui.Color { return k.BorderSecondary },
	"line-strong":  func(k *lobe.Tokens) ui.Color { return k.Border },
	"ink":          func(k *lobe.Tokens) ui.Color { return k.Text },
	"ink-2":        func(k *lobe.Tokens) ui.Color { return k.TextSecondary },
	"ink-3":        func(k *lobe.Tokens) ui.Color { return k.TextTertiary },
	"hover":        func(k *lobe.Tokens) ui.Color { return k.FillTertiary },
	"pressed":      func(k *lobe.Tokens) ui.Color { return k.FillSecondary },
	"selected":     func(k *lobe.Tokens) ui.Color { return k.FillSecondary },
	"raised":       func(k *lobe.Tokens) ui.Color { return k.BgElevated },
	"on-ink":       func(k *lobe.Tokens) ui.Color { return k.BgLayout },
	"accent":       func(k *lobe.Tokens) ui.Color { return k.Primary },
	"accent-hover": func(k *lobe.Tokens) ui.Color { return k.PrimaryHover },
	"accent-ink":   func(k *lobe.Tokens) ui.Color { return k.Link },
	"accent-bg":    func(k *lobe.Tokens) ui.Color { return k.PrimaryBg },
	"on-accent":    func(k *lobe.Tokens) ui.Color { return k.BgLayout },
	"ok":           func(k *lobe.Tokens) ui.Color { return k.SuccessText },
	"ok-bg":        func(k *lobe.Tokens) ui.Color { return k.SuccessBg },
	"warn":         func(k *lobe.Tokens) ui.Color { return k.WarningText },
	"warn-bg":      func(k *lobe.Tokens) ui.Color { return k.WarningBg },
	"warn-ink":     func(k *lobe.Tokens) ui.Color { return k.WarningTextActive },
	"warn-line":    func(k *lobe.Tokens) ui.Color { return k.WarningBorder },
	"danger":       func(k *lobe.Tokens) ui.Color { return k.ErrorText },
	"danger-bg":    func(k *lobe.Tokens) ui.Color { return k.ErrorBg },
	"diff-add":     func(k *lobe.Tokens) ui.Color { return k.SuccessBg },
	"diff-del":     func(k *lobe.Tokens) ui.Color { return k.ErrorBg },
	"code-bg":      func(k *lobe.Tokens) ui.Color { return flatten(k.FillTertiary, k.BgContainer) },
	"code-ink":     func(k *lobe.Tokens) ui.Color { return k.Text },
}

var lightTokens, darkTokens = lobe.Light(), lobe.Dark()

// lobeTokens returns Lobe UI's tokens for the theme's appearance.
func lobeTokens(t *ui.Theme) *lobe.Tokens {
	if t.Dark {
		return darkTokens
	}
	return lightTokens
}

// token returns a design token for the theme's appearance.
func token(t *ui.Theme, name string) ui.Color {
	v, ok := tokens[name]
	if !ok {
		panic("unknown design token " + name)
	}
	return v(lobeTokens(t))
}

// flatten is col drawn over an opaque under, as one opaque color.
func flatten(col, under ui.Color) ui.Color {
	return blend(ui.Color{R: col.R, G: col.G, B: col.B, A: 255}, under, float32(col.A)/255)
}

// monoFont sets what is typed rather than read: paths, code, commands and
// rules. Names and figures stay in the system face, as macOS sets them;
// figures get tabular digits (number) so columns still line up.
const monoFont = "SF Mono, Menlo, monospace"

// appTheme is Lobe UI's theme for the appearance, keeping what the window
// set that Lobe has no say in (vibrancy, fonts).
func appTheme(base *ui.Theme) *ui.Theme {
	t := *base
	lt := lobeTokens(base).Theme()
	t.Radius = lt.Radius
	t.Background = lt.Background
	t.Surface = token(&t, "surface")
	t.SurfaceHover = lt.SurfaceHover
	t.SurfacePressed = lt.SurfacePressed
	t.Border = token(&t, "line")
	t.Text = lt.Text
	t.TextMuted = lt.TextMuted
	t.Accent = lt.Accent
	t.AccentHover = lt.AccentHover
	t.AccentPressed = lt.AccentPressed
	t.AccentText = lt.AccentText
	t.Success = token(&t, "ok")
	t.Warning = token(&t, "warn")
	t.Danger = token(&t, "danger")
	t.Inverse = lt.Text
	t.InverseText = token(&t, "on-ink")
	t.Focus = lt.Focus
	t.FocusRingWidth, t.FocusRingOffset = lt.FocusRingWidth, lt.FocusRingOffset
	t.Selection = fade(lobeTokens(base).Info, 64)
	t.Scrollbar, t.ScrollbarWidth = lt.Scrollbar, lt.ScrollbarWidth
	return &t
}

// fade returns col see-through, for a tinted fill behind its own color.
func fade(col ui.Color, a uint8) ui.Color {
	col.A = a
	return col
}

// card is a grouped panel: Lobe UI's outlined block.
func card(c *ui.Context) ui.Element {
	return layout.Block(c, layout.BlockProps{Variant: layout.BlockOutlined}).Column()
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
