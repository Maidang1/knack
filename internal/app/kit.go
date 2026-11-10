package app

import (
	"fmt"
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/display"
	"github.com/lobehub/lobe-mygo/pkg/field"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
	"github.com/lobehub/lobe-mygo/pkg/overlay"
	lobe "github.com/lobehub/lobe-mygo/pkg/theme"
)

// Controls shared by the pages: Lobe UI's components (lobe-mygo), with the
// sizes and labels the pages share.

// withTip gives el Lobe UI's tooltip, shown once the pointer rests on it or
// the keyboard focus comes to it.
func withTip(c *ui.Context, el ui.Element, text string) ui.Element {
	overlay.Tooltip(c, el, text, overlay.TooltipProps{})
	return el
}

// segmented is Lobe UI's segmented control: a track whose chosen segment
// sits on a raised indicator. It is a radio group, so arrow keys move the
// choice.
func segmented(c *ui.Context, label string, selected *int, labels ...string) bool {
	return segmentedTrack(c, label, selected, false, labels)
}

// segmentedFill is a segmented control as wide as its container, its
// segments wrapping when they run out of it.
func segmentedFill(c *ui.Context, label string, selected *int, labels ...string) bool {
	return segmentedTrack(c, label, selected, true, labels)
}

func segmentedTrack(c *ui.Context, label string, selected *int, fill bool, labels []string) bool {
	track := action.Segmented(c.Key("seg:"+label), selected, labels, action.SegmentedProps{Size: lobe.Small}).
		Label(label).Shrink(0)
	if fill {
		track.Grow(1).MinWidth(0).Wrap()
	}
	changed := track.Changed()
	if changed {
		c.Invalidate()
	}
	return changed
}

// searchField is Lobe UI's search bar, labelled label: width wide, or at 0
// growing to fill its row. It returns the input, for Changed and Focus.
func searchField(c *ui.Context, query *string, label string, width float32, p field.SearchBarProps) ui.Element {
	var input ui.Element
	fieldBox(c, width).Children(func() {
		input = field.SearchBar(c, query, p).Label(label)
	})
	// Escape empties a search that holds words, as a search field does.
	return input.OnTextKey(func(k ui.TextKey) bool {
		if k.Key != ui.KeyEscape || k.Text() == "" {
			return false
		}
		k.SetText("")
		return true
	})
}

// textField is Lobe UI's outlined input, labelled label, growing to fill
// its row. It returns the input.
func textField(c *ui.Context, value *string, label, placeholder string) ui.Element {
	var input ui.Element
	fieldBox(c, 0).Children(func() {
		input = field.Input(c, value, field.InputProps{Placeholder: placeholder}).Label(label)
	})
	return input
}

// passwordField is textField hiding what it holds.
func passwordField(c *ui.Context, value *string, label, placeholder string) ui.Element {
	var input ui.Element
	fieldBox(c, 0).Children(func() {
		input = field.InputPassword(c, value, field.InputProps{Placeholder: placeholder}).Label(label)
	})
	return input
}

// textArea is Lobe UI's outlined text area, growing from min to max lines.
func textArea(c *ui.Context, value *string, label string, min, max int) ui.Element {
	var area ui.Element
	fieldBox(c, 0).Children(func() {
		area = field.TextArea(c, value, field.TextAreaProps{MinRows: min, MaxRows: max}).Label(label)
	})
	return area
}

// fieldBox holds a Lobe field, whose element is its input rather than its
// frame: the box gives the frame its width.
func fieldBox(c *ui.Context, width float32) ui.Element {
	box := ui.Column(c).MinWidth(0)
	if width > 0 {
		return box.Width(width).Shrink(1)
	}
	return box.Grow(1)
}

// selectField is Lobe UI's select over plain options, each its own label.
func selectField(c *ui.Context, selected *string, options []string) ui.Element {
	opts := make([]field.SelectOption, len(options))
	for i, o := range options {
		opts[i] = field.SelectOption{Value: o, Label: o}
	}
	return field.Select(c, selected, field.SelectProps{Options: opts, Size: lobe.Small}).Height(28).FontSize(13)
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

// outlineButton is a small secondary action: Lobe UI's default button.
func outlineButton(c *ui.Context, key any, text string) ui.Element {
	return action.Button(c.Key(key), text, action.ButtonProps{Size: lobe.Small}).Label(text).Shrink(0)
}

// sectionHead titles a block of a page, with a quiet trailing link.
func sectionHead(c *ui.Context, title, link string, onLink func()) {
	ui.Row(c).Padding(0, 0, 8, 0).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, title).FontSize(15).FontWeight(600).Grow(1).MinWidth(0).SingleLine()
		if link != "" {
			b := action.Button(c.Key("link:"+title), link, action.ButtonProps{Variant: action.Link, Size: lobe.Small}).Label(link)
			if b.Hovered() {
				b.TextColor(lobe.TokensOf(c).LinkHover)
			}
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

// menuButton gives a menu's trigger the look of Lobe UI's default button.
func menuButton(c *ui.Context, trigger ui.Element, text string) ui.Element {
	b := action.StyleButton(c, trigger, "", action.ButtonProps{Size: lobe.Small}).Label(text).Shrink(0)
	b.Children(func() {
		ui.Text(c, text).SingleLine()
		ui.Icon(c, lucide.ChevronDown()).FontSize(12).TextColor(lobe.TokensOf(c).TextTertiary)
	})
	return b
}

// menu lists a Lobe UI menu's items the way ui.Menu builds them, so a menu
// reads the same: if m.Item("复制路径").Chosen() { … }. The build runs once
// to list the items and, in the frame one is chosen, again to act on it.
type menu struct {
	items  []overlay.MenuItem
	chosen string
}

type menuEntry struct {
	m *menu
	i int
}

func (m *menu) Item(label string) menuEntry {
	m.items = append(m.items, overlay.MenuItem{Key: strconv.Itoa(len(m.items)), Label: label})
	return menuEntry{m, len(m.items) - 1}
}

// Separator is a line between groups of items.
func (m *menu) Separator() { m.items = append(m.items, overlay.MenuItem{Divider: true}) }

func (e menuEntry) Disabled(disabled bool) menuEntry {
	e.m.items[e.i].Disabled = disabled
	return e
}

// Danger colors an item that deletes something.
func (e menuEntry) Danger() menuEntry {
	e.m.items[e.i].Danger = true
	return e
}

// Checked marks the current choice of a list of options; the others keep
// its place, so the labels line up.
func (e menuEntry) Checked(on bool) menuEntry {
	if on {
		e.m.items[e.i].Icon = lucide.Check()
	} else {
		e.m.items[e.i].Icon = blankGlyph
	}
	return e
}

func (e menuEntry) Chosen() bool { return e.m.chosen != "" && e.m.items[e.i].Key == e.m.chosen }

var blankGlyph = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"></svg>`))

// runMenu lists build's items for show, a Lobe menu, and acts on the one
// it reports chosen.
func runMenu(build func(m *menu), show func(items []overlay.MenuItem) string) {
	m := &menu{}
	build(m)
	if len(m.items) == 0 {
		return
	}
	if key := show(m.items); key != "" {
		build(&menu{chosen: key})
	}
}

// dropdown is Lobe UI's dropdown menu under a trigger, which style gives
// its look and content.
func (a *App) dropdown(c *ui.Context, key string, build func(m *menu), style func(t ui.Element)) ui.Element {
	if a.menus == nil {
		a.menus = map[string]*bool{}
	}
	open := a.menus[key]
	if open == nil {
		open = new(bool)
		a.menus[key] = open
	}
	var trigger ui.Element
	runMenu(build, func(items []overlay.MenuItem) string {
		return overlay.DropdownMenu(c.Key(key), open, overlay.DropdownMenuProps{Items: items}, func(t ui.Element) {
			trigger = t
			style(t)
		})
	})
	return trigger
}

// contextMenu gives target Lobe UI's context menu.
func contextMenu(c *ui.Context, target ui.Element, build func(m *menu)) {
	runMenu(build, func(items []overlay.MenuItem) string {
		return overlay.ContextMenu(c, target, overlay.DropdownMenuProps{Items: items})
	})
}

// textButton is Lobe UI's text button, with no surface until hovered, and
// an icon before its label when icon is set.
func textButton(c *ui.Context, key any, icon, text string) ui.Element {
	b := action.StyleButton(c, ui.ButtonBase(c.Key(key)), "", action.ButtonProps{Variant: action.Text, Size: lobe.Small}).
		Label(text).Shrink(0)
	b.Children(func() {
		if icon != "" {
			ui.Icon(c, glyph(icon)).FontSize(14)
		}
		if text != "" {
			ui.Text(c, text).SingleLine()
		}
	})
	return b
}

// linkButton is Lobe UI's link button.
func linkButton(c *ui.Context, key any, text string) ui.Element {
	b := action.Button(c.Key(key), text, action.ButtonProps{Variant: action.Link, Size: lobe.Small}).Label(text).Shrink(0)
	if b.Hovered() {
		b.TextColor(lobe.TokensOf(c).LinkHover)
	}
	return b
}

// primaryButton is the page's one primary action: Lobe UI's primary
// button, with an icon before its label.
func primaryButton(c *ui.Context, key any, icon, text string) ui.Element {
	return stylePrimary(c, ui.ButtonBase(c.Key(key)), icon, text)
}

// stylePrimary gives b, a button of any kind, primaryButton's look.
func stylePrimary(c *ui.Context, b ui.Element, icon, text string) ui.Element {
	b = action.StyleButton(c, b, "", action.ButtonProps{Variant: action.Primary, Size: lobe.Small}).
		Height(28).AlignItems(ui.Center).Label(text).Shrink(0)
	b.Children(func() {
		if icon != "" {
			ui.Icon(c, glyph(icon)).FontSize(14)
		}
		ui.Text(c, text).SingleLine()
	})
	return b
}

// emptyState is Lobe UI's empty placeholder, centered in its pane: why the
// pane has nothing to show, and room for actions.
func emptyState(c *ui.Context, label, icon, title, body string, actions func()) {
	ui.Column(c).Grow(1).Padding(48, 32).AlignItems(ui.Center).Justify(ui.Center).Label(label).Children(func() {
		display.Empty(c, display.EmptyProps{Icon: glyph(icon), Title: title, Description: body, Action: actions}).
			MaxWidth(420)
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

// countedSegments is a tab list in the look of Lobe UI's segmented control,
// with a quiet count after each name. A chosen tab sits on the raised
// indicator; its count stays gray, so the name carries the choice.
func countedSegments(c *ui.Context, key, label, prefix string, selected *int, names, counts []string) bool {
	tk := lobe.TokensOf(c)
	tabs := ui.TabsBase(c.Key(key), selected, len(names))
	segmentTrack(c, tabs.List).Label(label).Shrink(0).AlignSelf(ui.Start).Wrap()
	tabs.List.Children(func() {
		for i, name := range names {
			segmentTab(c, tabs.Tab(i), i == *selected).Label(prefix + name).Children(func() {
				ui.Text(c, name).SingleLine()
				if i < len(counts) && counts[i] != "" {
					number(c, counts[i]).FontSize(11).TextColor(tk.TextTertiary)
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

// segmentTrack gives a tab list the track of Lobe UI's segmented control.
func segmentTrack(c *ui.Context, list ui.Element) ui.Element {
	tk := lobe.TokensOf(c)
	return list.Gap(4).Padding(3).Radius(tk.BorderRadiusLG).Background(tk.BgLayout).Border(1, tk.FillQuaternary)
}

// segmentTab gives a tab a segment's look: the chosen one sits on the
// raised indicator.
func segmentTab(c *ui.Context, tab ui.Element, on bool) ui.Element {
	tk := lobe.TokensOf(c)
	tab.Height(26).Padding(0, 10).Gap(5).Radius(tk.BorderRadius).AlignItems(ui.Center).
		FontSize(12).FontWeight(500).Cursor(ui.CursorPointer)
	fg := tk.TextSecondary
	switch {
	case on:
		fg = tk.Text
		tab.Background(tk.BgElevated).Shadow(0, 1, 2, 0, tk.Border)
	case tab.Hovered():
		fg = tk.Text
	}
	return tab.TextColor(fg)
}
