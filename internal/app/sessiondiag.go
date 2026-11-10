package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"knack/internal/insight"
)

// The session's diagnosis sits above its turns: a bar per model call for
// the context it carried, and the findings worth a look, each opening the
// turn and step it is about.

const (
	contextBars       = 240 // more calls than this share bars
	contextHeight     = 64
	sessionFindingCap = 5
)

func (d *sessionDetailData) diagnosis() *insight.SessionDiagnosis {
	if d.diag == nil {
		diag := insight.DiagnoseSession(d.session)
		d.diag = &diag
	}
	return d.diag
}

func (a *App) sessionDiagnosis(c *ui.Context, d *sessionDetailData, index int) {
	diag := d.diagnosis()
	if len(diag.Points) == 0 && len(diag.Findings) == 0 {
		return
	}
	t := c.Theme()
	muted := token(t, "ink-3")
	read, write, fresh := sessionColor("phase-think", t.Dark), sessionColor("phase-gen", t.Dark), sessionColor("phase-tool", t.Dark)
	ui.Column(c).Gap(10).Label("上下文与诊断").Children(func() {
		ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "每次模型调用的上下文").FontSize(13).FontWeight(600).Shrink(0)
			parts := []string{fmt.Sprintf("%d 次调用", len(diag.Points)), "峰值 " + tokenNumber(diag.Peak)}
			if diag.RebuildTokens > 0 {
				parts = append(parts, "重建缓存 "+tokenNumber(diag.RebuildTokens))
			}
			number(c, strings.Join(parts, " · ")).FontSize(12).TextColor(muted).Grow(1).MinWidth(0).SingleLine()
			legendItem(c, read, "缓存读取")
			legendItem(c, write, "缓存写入")
			legendItem(c, fresh, "未缓存")
			legendItem(c, t.Warning, "重建")
		})
		if len(diag.Points) > 0 {
			a.contextCurve(c, d, diag, index, read, write, fresh)
		}
		a.sessionFindings(c, d, diag)
	})
}

// contextCurve draws a bar per call, stacked by how its input was served;
// a call that rebuilt the cache shows its writes in the warning color and a
// compaction a gap before it. Choosing a bar opens its turn.
func (a *App) contextCurve(c *ui.Context, d *sessionDetailData, diag *insight.SessionDiagnosis, index int, read, write, fresh ui.Color) {
	t := c.Theme()
	points := diag.Points
	per := max(1, (len(points)+contextBars-1)/contextBars)
	peak := float32(max(diag.Peak, 1))
	ui.Row(c.Key("context-curve")).Height(contextHeight).Gap(1).AlignItems(ui.End).Label("上下文曲线").Children(func() {
		for start := 0; start < len(points); start += per {
			// A shared bar shows its largest call.
			p := points[start]
			for _, q := range points[start:min(start+per, len(points))] {
				if q.Tokens.InputTotal() > p.Tokens.InputTotal() {
					p = q
				}
			}
			tip := contextTip(p)
			on := p.Turn == index
			bar := withTip(c, ui.ButtonBase(c.Key(fmt.Sprintf("ctx:%d", start))).Grow(1).MinWidth(1).Height(contextHeight).Label(tip), tip)
			bar.Draw(func(pa *ui.Painter, r ui.Rect) {
				if !p.UsageKnown {
					pa.Fill(ui.Rect{X: r.X, Y: r.Y + r.H - 2, W: r.W, H: 2}, t.Border, 0)
					return
				}
				alpha := uint8(255)
				if !on {
					alpha = 150
				}
				y := r.Y + r.H
				seg := func(n int64, color ui.Color) {
					h := float32(n) / peak * r.H
					if h <= 0 {
						return
					}
					y -= h
					pa.Fill(ui.Rect{X: r.X, Y: y, W: r.W, H: h}, fade(color, alpha), 0)
				}
				seg(p.Tokens.CacheRead, read)
				w := write
				if p.Rebuild != "" {
					w = t.Warning
				}
				seg(p.Tokens.CacheWrite, w)
				seg(p.Tokens.Input, fresh)
				if p.Compacted {
					pa.Fill(ui.Rect{X: r.X, Y: r.Y, W: 1, H: r.H}, t.TextMuted, 0)
				}
			})
			if bar.Clicked() {
				a.chooseSessionTurn(d, p.Turn)
				a.openStep(d, p.Turn, "", p.RequestID)
			}
		}
	})
}

func contextTip(p insight.ContextPoint) string {
	parts := []string{fmt.Sprintf("第 %d 轮", p.Turn+1)}
	if p.Model != "" {
		parts = append(parts, p.Model)
	}
	if !p.UsageKnown {
		return strings.Join(append(parts, "用量未记录"), " · ")
	}
	tk := p.Tokens
	parts = append(parts, fmt.Sprintf("输入 %s（缓存读取 %s · 写入 %s · 未缓存 %s）· 输出 %s",
		tokenNumber(tk.InputTotal()), tokenNumber(tk.CacheRead), tokenNumber(tk.CacheWrite), tokenNumber(tk.Input), tokenNumber(tk.Output)))
	if p.Compacted {
		parts = append(parts, "此前上下文已压缩")
	}
	if p.Rebuild != "" {
		parts = append(parts, "缓存重建："+p.Rebuild)
	}
	return strings.Join(parts, " · ")
}

func findingIcon(kind string) string {
	switch kind {
	case insight.FindingLoop:
		return "refresh"
	case insight.FindingFailures, insight.FindingAPIError:
		return "alert"
	case insight.FindingSkill:
		return "sparkle"
	case insight.FindingLargeOut:
		return "file"
	case insight.FindingRebuild:
		return "zap"
	case insight.FindingPermission:
		return "hand"
	case insight.FindingInterrupt:
		return "skip"
	}
	return "info"
}

func (a *App) sessionFindings(c *ui.Context, d *sessionDetailData, diag *insight.SessionDiagnosis) {
	t := c.Theme()
	muted := token(t, "ink-3")
	if len(diag.Findings) == 0 {
		ui.Text(c, "没有发现循环、连续失败、被纠正的轮次或大段工具结果").FontSize(12).TextColor(muted)
		return
	}
	list := diag.Findings
	all := a.sessions.allFindings == d.session.ID
	if !all {
		list = list[:min(len(list), sessionFindingCap)]
	}
	ui.Column(c).Gap(0).Label("会话诊断").Children(func() {
		for i, f := range list {
			label := fmt.Sprintf("第 %d 轮 · %s · %s", f.Turn+1, f.Title, f.Detail)
			row := ui.ButtonBase(c.Key(fmt.Sprintf("finding:%d", i))).Label(label).Padding(6, 0).Gap(10).AlignItems(ui.Center).Justify(ui.Start)
			if row.Hovered() {
				row.Background(fade(t.SurfaceHover, 120))
			}
			row.Children(func() {
				tone := t.TextMuted
				if f.Kind == insight.FindingLoop || f.Kind == insight.FindingFailures || f.Kind == insight.FindingSkill || f.Kind == insight.FindingCorrected {
					tone = t.Warning
				}
				ui.Icon(c, glyph(findingIcon(f.Kind))).FontSize(13).TextColor(tone).Shrink(0)
				number(c, fmt.Sprintf("第 %d 轮", f.Turn+1)).FontSize(12).TextColor(muted).Width(52).Shrink(0)
				ui.Text(c, f.Title).FontSize(13).FontWeight(600).Shrink(0).SingleLine()
				withTip(c, ui.Text(c, f.Detail).FontSize(13).TextColor(t.TextMuted).Grow(1).MinWidth(0).SingleLine(), f.Detail)
			})
			if row.Clicked() {
				a.chooseSessionTurn(d, f.Turn)
				a.openStep(d, f.Turn, f.ToolID, f.RequestID)
			}
		}
		if len(diag.Findings) > sessionFindingCap {
			text := fmt.Sprintf("全部 %d 条", len(diag.Findings))
			if all {
				text = "收起"
			}
			if linkButton(c, "findings-all", text).AlignSelf(ui.Start).Clicked() {
				if all {
					a.sessions.allFindings = ""
				} else {
					a.sessions.allFindings = d.session.ID
				}
			}
		}
	})
}

// openStep opens the step of a turn that a finding points at: a tool call,
// or a model request.
func (a *App) openStep(d *sessionDetailData, turn int, toolID, requestID string) {
	if turn < 0 || turn >= len(d.turns) || toolID == "" && requestID == "" {
		return
	}
	for i, s := range d.turns[turn].steps {
		if toolID != "" && s.tool != nil && s.tool.ID == toolID || toolID == "" && s.request != nil && s.request.ID == requestID {
			a.sessions.selectedStep, a.sessions.stepOpen = i, true
			return
		}
	}
}
