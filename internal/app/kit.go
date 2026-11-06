package app

import (
	"fmt"
	"github.com/egoist/mygo/ui"
)

// Controls shared by the pages, drawn as the design's: quiet chrome in the
// neutral tokens, raised white for what is chosen.

// segmented is the design's segmented control: a gray track whose chosen
// segment is raised. It is a tab list, so arrow keys move the choice.
func segmented(c *ui.Context, label string, selected *int, labels ...string) bool {
	return segmentedTrack(c, label, selected, false, labels)
}

// segmentedFill is a segmented control as wide as its container, its
// segments sharing the width and wrapping when they run out of it.
func segmentedFill(c *ui.Context, label string, selected *int, labels ...string) bool {
	return segmentedTrack(c, label, selected, true, labels)
}

func segmentedTrack(c *ui.Context, label string, selected *int, fill bool, labels []string) bool {
	t := c.Theme()
	tabs := ui.TabsBase(c.Key("seg:"+label), selected, len(labels))
	tabs.List.Gap(2).Padding(2).Radius(7).Background(t.SurfaceHover).Label(label).Shrink(0)
	if fill {
		tabs.List.Grow(1).MinWidth(0).Wrap()
	}
	tabs.List.Children(func() {
		for i, text := range labels {
			on := i == *selected
			tab := tabs.Tab(i).Padding(4, 10).Radius(5).Label(text)
			if fill {
				tab.Grow(1).Padding(4, 6).AlignItems(ui.Center).Justify(ui.Center)
			}
			if on {
				tab.Background(token(t, "raised")).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.06))
			}
			tab.Children(func() {
				label := ui.Text(c, text).FontSize(12).SingleLine()
				if on {
					label.FontWeight(600)
				} else {
					label.TextColor(t.TextMuted)
				}
			})
		}
	})
	changed := tabs.List.Changed()
	if changed {
		c.Invalidate()
	}
	return changed
}

// outlineButton is a small secondary action: white face, strong hairline.
func outlineButton(c *ui.Context, key any, text string) ui.Element {
	t := c.Theme()
	b := ui.Button(c.Key(key), text).Height(26).Padding(0, 10).Radius(6).FontSize(12).FontWeight(400).
		Background(t.Surface).Border(1, token(t, "line-strong")).Shrink(0)
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	return b
}

// selectStyle gives a Select the design's dropdown face.
func selectStyle(c *ui.Context, e ui.Element) ui.Element {
	t := c.Theme()
	return e.Height(28).Radius(7).Background(t.Surface).Border(1, token(t, "line-strong")).FontSize(12)
}

// sectionHead titles a block of a page, with a quiet trailing link.
func sectionHead(c *ui.Context, title, link string, onLink func()) {
	t := c.Theme()
	ui.Row(c).Padding(0, 0, 8, 0).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, title).FontSize(14).FontWeight(600).Grow(1).MinWidth(0).SingleLine()
		if link != "" {
			b := ui.ButtonBase(c.Key("link:"+title)).Label(link).Padding(2, 0)
			b.Children(func() {
				text := ui.Text(c, link).FontSize(12).TextColor(t.TextMuted).SingleLine()
				if b.Hovered() {
					text.TextColor(t.Text)
				}
			})
			if b.Clicked() {
				onLink()
				c.Invalidate()
			}
		}
	})
}

// legendItem is a colored square and its name.
func legendItem(c *ui.Context, color ui.Color, name string) {
	ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Box(c).Size(8, 8).Radius(2).Background(color)
		ui.Text(c, name).FontSize(12).TextColor(c.Theme().TextMuted).SingleLine()
	})
}

// inkButton is the page's one primary action: ink face, on-ink label.
func inkButton(c *ui.Context, key any, icon, text string) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).Height(28).Padding(0, 12).Gap(6).Radius(7).AlignItems(ui.Center).Background(t.Text).Label(text).Shrink(0)
	if b.Hovered() {
		b.Background(t.TextMuted)
	}
	b.Children(func() {
		if icon != "" {
			ui.Icon(c, glyph(icon)).FontSize(14).TextColor(token(t, "on-ink"))
		}
		ui.Text(c, text).FontSize(12.5).FontWeight(600).TextColor(token(t, "on-ink")).SingleLine()
	})
	return b
}

// emptyState is a centered explanation of why a pane has nothing to show:
// an icon in a soft disc, a title, a sentence, and room for actions.
func emptyState(c *ui.Context, label, icon string, tone ui.Color, title, body string, actions func()) {
	t := c.Theme()
	ui.Column(c).Grow(1).Padding(48, 32).Gap(10).AlignItems(ui.Center).Justify(ui.Center).Label(label).Children(func() {
		ui.Box(c).Size(44, 44).Radius(22).Background(fade(tone, 30)).AlignItems(ui.Center).Justify(ui.Center).Margin(0, 0, 4, 0).Children(func() {
			ui.Icon(c, glyph(icon)).FontSize(20).TextColor(tone)
		})
		ui.Text(c, title).FontSize(15).FontWeight(600).TextAlign(ui.Center)
		if body != "" {
			ui.Text(c, body).FontSize(12.5).LineHeight(1.6).TextColor(t.TextMuted).MaxWidth(380).TextAlign(ui.Center)
		}
		if actions != nil {
			ui.Row(c).Gap(8).Margin(6, 0, 0, 0).Children(actions)
		}
	})
}

// chipTabs is a row of rounded filters, each with its count; the chosen one
// is ink. It reports a change.
func chipTabs(c *ui.Context, label string, selected *int, labels []string, counts []int) bool {
	t := c.Theme()
	tabs := ui.TabsBase(c.Key("chips:"+label), selected, len(labels))
	tabs.List.Gap(6).Label(label).Shrink(0).Wrap().Children(func() {
		for i, text := range labels {
			on := i == *selected
			tab := tabs.Tab(i).Height(26).Padding(0, 10).Gap(6).Radius(13).AlignItems(ui.Center).Label(text)
			if on {
				tab.Background(t.Text)
			} else {
				tab.Border(1, token(t, "line-strong"))
				if tab.Hovered() {
					tab.Background(t.SurfaceHover)
				}
			}
			tab.Children(func() {
				name := ui.Text(c, text).FontSize(12).SingleLine()
				count := number(c, fmt.Sprint(counts[i])).FontSize(10.5)
				if on {
					name.TextColor(token(t, "on-ink")).FontWeight(600)
					count.TextColor(token(t, "on-ink"))
				} else {
					count.TextColor(token(t, "ink-3"))
				}
			})
		}
	})
	changed := tabs.List.Changed()
	if changed {
		c.Invalidate()
	}
	return changed
}

// underlineTabs is a row of tabs marked by an ink rule under the chosen
// one, each with its count. Tabs are labelled prefix + name. It reports a
// change.
func underlineTabs(c *ui.Context, key, label, prefix string, selected *int, names, counts []string) bool {
	t := c.Theme()
	tabs := ui.TabsBase(c.Key(key), selected, len(names))
	tabs.List.Gap(20).Label(label).Children(func() {
		for i, name := range names {
			on := *selected == i
			tab := tabs.Tab(i).Padding(9, 0, 8, 0).Gap(5).AlignItems(ui.Center).Label(prefix + name)
			tab.Draw(func(p *ui.Painter, r ui.Rect) {
				if on {
					p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H - 2, W: r.W, H: 2}, t.Text, 1)
				}
			})
			tab.Children(func() {
				text := ui.Text(c, name).FontSize(13).SingleLine()
				if on {
					text.FontWeight(600)
				} else {
					text.TextColor(t.TextMuted)
				}
				if i < len(counts) && counts[i] != "" {
					number(c, counts[i]).FontSize(11).TextColor(token(t, "ink-3"))
				}
			})
		}
	})
	changed := tabs.List.Changed()
	if changed {
		c.Invalidate()
	}
	return changed
}
