package app

import "github.com/egoist/mygo/ui"

// appTheme restyles the stock theme as a macOS app: white content, system
// grays, one system-blue accent, and three status colors used only as text.
// The accent is the darker blue (#0A5FD1) so white text on a filled button
// and blue text on white both clear 4.5:1; #007AFF is for graphics only.
// The system still chooses light or dark.
func appTheme(base *ui.Theme) *ui.Theme {
	t := *base
	t.Radius = 6
	if t.Dark {
		t.Background = ui.Hex("#1e1e20")
		t.Surface = ui.Hex("#2a2a2d")
		t.SurfaceHover = ui.Hex("#343438")
		t.SurfacePressed = ui.Hex("#3e3e43")
		t.Border = ui.Hex("#3a3a3f")
		t.Text = ui.Hex("#f5f5f7")
		t.TextMuted = ui.Hex("#a1a1a6")
		t.Accent = ui.Hex("#0a74e6")
		t.AccentHover = ui.Hex("#2b8af0")
		t.AccentPressed = ui.Hex("#0961c4")
		t.AccentText = ui.Hex("#ffffff")
		t.Success = ui.Hex("#5bc08a")
		t.Warning = ui.Hex("#e0a04a")
		t.Danger = ui.Hex("#ff7a7f")
		t.Selection = ui.RGBA(10, 132, 255, 0.4)
		t.Focus = ui.RGBA(10, 132, 255, 0.6)
	} else {
		t.Background = ui.Hex("#ffffff")
		t.Surface = ui.Hex("#ffffff")
		t.SurfaceHover = ui.Hex("#f2f2f4")
		t.SurfacePressed = ui.Hex("#e8e8eb")
		t.Border = ui.Hex("#e1e1e4")
		t.Text = ui.Hex("#1d1d1f")
		t.TextMuted = ui.Hex("#6e6e73")
		t.Accent = ui.Hex("#0a5fd1")
		t.AccentHover = ui.Hex("#0953b8")
		t.AccentPressed = ui.Hex("#0a4a9f")
		t.AccentText = ui.Hex("#ffffff")
		t.Success = ui.Hex("#2b7650")
		t.Warning = ui.Hex("#996019")
		t.Danger = ui.Hex("#bd4045")
		t.Selection = ui.RGBA(0, 122, 255, 0.25)
		t.Focus = ui.RGBA(0, 122, 255, 0.6)
	}
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
