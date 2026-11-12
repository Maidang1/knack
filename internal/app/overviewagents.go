package app

import (
	"fmt"
	"strings"
	"time"

	"knack/internal/session"
	"knack/internal/skill"

	"github.com/egoist/mygo/ui"
)

// agentColumns are the design's fixed widths of the Agents table; the name
// takes the rest. The home page keeps only what tells the Agents apart; the
// Agent's own page has the rest.
var agentColumns = []struct {
	title string
	width float32
}{{"会话", 64}, {"Token", 92}, {"最近活跃", 96}}

func (a *App) agentUsage(c *ui.Context, d *overviewData) {
	t := c.Theme()
	muted := token(t, "ink-3")
	link := ""
	if n := len(a.sidebarAgents()); n > 0 {
		link = fmt.Sprintf("全部 %d 个 →", n)
	}
	sectionHead(c, "Agents", link, func() { a.page = agentsPage })
	row := func(key string, height float32, fn func()) {
		ui.Row(c.Key(key)).Height(height).Gap(8).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(fn)
	}
	// open is a row that goes to its Agent's page, when it has one.
	open := func(id session.Agent, fn func()) {
		root, ok := a.agentRoot(id)
		b := listRow(c, string(id), sessionAgentName(id)).Height(40).Padding(0).Gap(8).Disabled(!ok)
		b.Children(fn)
		if b.Clicked() {
			a.page = "root:" + root.ID
		}
	}
	cell := func(i int, value string, supported bool, tip string) {
		e := number(c, value).Width(agentColumns[i].width).Shrink(0).TextAlign(ui.End).FontSize(13).TextColor(t.TextMuted)
		if !supported {
			e.TextColor(muted)
		}
		if tip != "" {
			withTip(c, e, tip)
		}
	}
	row("head", 26, func() {
		ui.Text(c, "Agent").Grow(1).MinWidth(0).FontSize(11.5).TextColor(muted)
		for _, col := range agentColumns {
			ui.Text(c, col.title).Width(col.width).Shrink(0).TextAlign(ui.End).FontSize(11.5).TextColor(muted)
		}
	})
	name := func(id session.Agent) {
		ui.Row(c).Grow(1).MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(8, 8).Radius(4).Shrink(0).Background(agentColor(id, t.Dark))
			ui.Text(c, sessionAgentName(id)).FontSize(13).SingleLine()
		})
	}
	now := time.Now()
	total := d.summary.Tokens.Total()
	for _, agent := range d.agents {
		sum := agent.summary
		open(agent.id, func() {
			name(agent.id)
			cell(0, fmt.Sprint(sum.Sessions), true, "主会话数，不含子 Agent")
			tokens, tokensOK := tokenNumber(sum.Tokens.Total()), true
			if sum.UnmeteredRequests > 0 && sum.Tokens.Total() == 0 {
				tokens, tokensOK = "暂不支持", false
			}
			tip := ""
			if total > 0 && tokensOK {
				tip = fmt.Sprintf("占 %.0f%%", 100*float64(sum.Tokens.Total())/float64(total))
			}
			if len(sum.ByModel) > 0 {
				tip = strings.TrimPrefix(tip+" · 主力模型 "+sum.ByModel[0].Name, " · ")
			}
			cell(1, tokens, tokensOK, tip)
			active := "—"
			if !agent.last.IsZero() {
				active = ago(now, agent.last)
			}
			cell(2, active, true, "")
		})
	}
	if a.hasUntrackedSessionAgents() {
		ui.Text(c, "其他 Agent 尚未接入本地会话格式，暂不支持；可用 agents.json 或 JS 插件接入").FontSize(11.5).TextColor(muted).Padding(8, 0, 0, 0)
	}
}

// agentRoot is the Agent's skills directory, which its page belongs to.
func (a *App) agentRoot(id session.Agent) (skill.Root, bool) {
	for _, r := range a.sidebarAgents() {
		if agentKey(a.home, r.Path) == string(id) {
			return r, true
		}
	}
	return skill.Root{}, false
}

func (a *App) hasUntrackedSessionAgents() bool {
	for _, r := range a.roots(skill.RoleAgent) {
		key := agentKey(a.home, r.Path)
		if key != "claude" && key != "codex" && key != "factory" && key != "cursor" {
			return true
		}
	}
	return false
}
