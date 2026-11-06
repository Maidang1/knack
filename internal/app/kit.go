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
				label := ui.Text(c, text).FontSize(13).SingleLine()
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

// selectedRow is a list item, filled when chosen with a pale wash of the
// accent under ink text rather than an inverted block, so the row reads the
// same chosen or not.
func selectedRow(c *ui.Context, selected bool) ui.Element {
	r := ui.Row(c).Radius(6)
	if selected {
		r.Background(token(c.Theme(), "selected"))
	}
	return r
}

// quietList is the theme of a list whose rows draw their own choice
// (selectedRow): the list's accent fill is clear, and its text stays ink.
func quietList(t *ui.Theme) *ui.Theme {
	lt := *t
	lt.Accent, lt.AccentText = ui.Transparent, t.Text
	return &lt
}

// outlineButton is a small secondary action: white face, strong hairline.
func outlineButton(c *ui.Context, key any, text string) ui.Element {
	t := c.Theme()
	b := ui.Button(c.Key(key), text).Height(26).Padding(0, 10).Radius(6).FontSize(13).FontWeight(400).
		Background(t.Surface).Border(1, token(t, "line-strong")).Shrink(0)
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	return b
}

// selectStyle gives a Select the design's dropdown face.
func selectStyle(c *ui.Context, e ui.Element) ui.Element {
	t := c.Theme()
	return e.Height(28).Radius(7).Background(t.Surface).Border(1, token(t, "line-strong")).FontSize(13)
}

// sectionHead titles a block of a page, with a quiet trailing link.
func sectionHead(c *ui.Context, title, link string, onLink func()) {
	t := c.Theme()
	ui.Row(c).Padding(0, 0, 8, 0).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, title).FontSize(15).FontWeight(600).Grow(1).MinWidth(0).SingleLine()
		if link != "" {
			b := ui.ButtonBase(c.Key("link:"+title)).Label(link).Padding(2, 0)
			b.Children(func() {
				text := ui.Text(c, link).FontSize(13).TextColor(token(t, "accent-ink")).SingleLine()
				if b.Hovered() {
					text.Underline()
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
		ui.Text(c, name).FontSize(13).TextColor(c.Theme().TextMuted).SingleLine()
	})
}

// primaryButton is the page's one primary action: accent face, white label.
func primaryButton(c *ui.Context, key any, icon, text string) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).Height(28).Padding(0, 12).Gap(6).Radius(7).AlignItems(ui.Center).Background(t.Accent).Label(text).Shrink(0)
	if b.Hovered() {
		b.Background(t.AccentHover)
	}
	b.Children(func() {
		if icon != "" {
			ui.Icon(c, glyph(icon)).FontSize(15).TextColor(t.AccentText)
		}
		ui.Text(c, text).FontSize(13).FontWeight(600).TextColor(t.AccentText).SingleLine()
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
			ui.Text(c, body).FontSize(13).LineHeight(1.6).TextColor(t.TextMuted).MaxWidth(380).TextAlign(ui.Center)
		}
		if actions != nil {
			ui.Row(c).Gap(8).Margin(6, 0, 0, 0).Children(actions)
		}
	})
}

// chipTabs is a segmented control of filters, each with its count, as a
// macOS scope bar is. It reports a change.
func chipTabs(c *ui.Context, label string, selected *int, labels []string, counts []int) bool {
	shown := make([]string, len(counts))
	for i, n := range counts {
		shown[i] = fmt.Sprint(n)
	}
	return countedSegments(c, "chips:"+label, label, "", selected, labels, shown)
}

// underlineTabs switches the views of a page: a segmented control, each
// segment with its count, labelled prefix + name. It reports a change.
func underlineTabs(c *ui.Context, key, label, prefix string, selected *int, names, counts []string) bool {
	return countedSegments(c, key, label, prefix, selected, names, counts)
}

// countedSegments is the segmented track of segmented, with a quiet count
// after each name. A chosen segment is raised; its count stays gray, so
// the name carries the choice.
func countedSegments(c *ui.Context, key, label, prefix string, selected *int, names, counts []string) bool {
	t := c.Theme()
	tabs := ui.TabsBase(c.Key(key), selected, len(names))
	tabs.List.Gap(2).Padding(2).Radius(7).Background(t.SurfaceHover).Label(label).Shrink(0).AlignSelf(ui.Start).Wrap()
	tabs.List.Children(func() {
		for i, name := range names {
			on := i == *selected
			tab := tabs.Tab(i).Height(24).Padding(0, 10).Gap(5).Radius(5).AlignItems(ui.Center).Label(prefix + name)
			if on {
				tab.Background(token(t, "raised")).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.06))
			} else if tab.Hovered() {
				tab.Background(token(t, "pressed"))
			}
			tab.Children(func() {
				text := ui.Text(c, name).FontSize(13).SingleLine()
				if on {
					text.FontWeight(600)
				} else {
					text.TextColor(t.TextMuted)
				}
				if i < len(counts) && counts[i] != "" {
					number(c, counts[i]).FontSize(11.5).TextColor(token(t, "ink-3"))
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
