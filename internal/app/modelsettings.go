package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/choice"
	"github.com/lobehub/lobe-mygo/pkg/feedback"
	lobe "github.com/lobehub/lobe-mygo/pkg/theme"

	"knack/internal/evals"
	"knack/internal/llm"
)

// The model connection is what skill evaluation sends session content to.
// It is off until the person switches it on, and every run says what it is
// about to send before sending it.

const (
	settingsPluginTab = 3
	settingsModelTab  = 4
)

type modelState struct {
	config  llm.Config // as saved
	draft   llm.Config // as edited in settings
	loadErr string
	testing bool
	status  string // the last test, in words
	failed  bool
	// clearing is set between the first and the confirming click.
	clearing bool
	// connect makes the client; tests put a scripted one here.
	connect func(llm.Config) (llm.Client, error)
}

func (a *App) modelPath() string { return filepath.Join(a.configDir, "llm.json") }

func (a *App) evalsDir() string { return filepath.Join(a.configDir, "evals") }

func (a *App) loadModel() {
	cfg, err := llm.Load(a.modelPath())
	a.model = modelState{config: cfg, draft: cfg, connect: llm.New}
	if err != nil {
		a.model.loadErr = err.Error()
	}
}

// saveModel persists the edited connection. A change of service or model
// needs a fresh test.
func (a *App) saveModel() bool {
	m := &a.model
	m.draft.BaseURL, m.draft.Model, m.draft.APIKey = strings.TrimSpace(m.draft.BaseURL), strings.TrimSpace(m.draft.Model), strings.TrimSpace(m.draft.APIKey)
	if err := llm.Save(a.modelPath(), m.draft); err != nil {
		m.status, m.failed = "保存失败："+err.Error(), true
		return false
	}
	m.config, m.loadErr = m.draft, ""
	return true
}

func (a *App) testModel() {
	m := &a.model
	if m.testing || !a.saveModel() {
		return
	}
	client, err := m.connect(m.config)
	if err != nil {
		m.status, m.failed = err.Error(), true
		return
	}
	m.testing, m.status, m.failed = true, "", false
	run := func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		err := llm.Test(ctx, client)
		a.post(func() {
			m.testing = false
			if err != nil {
				m.status, m.failed = err.Error(), true
				return
			}
			m.status = "连接正常，" + m.config.Model + " 有回复。"
		})
	}
	if a.win == nil {
		run()
		return
	}
	go run()
}

// clearEvals removes every skill's evaluation: evaluator, verdicts, causes and
// trigger set.
func (a *App) clearEvals() {
	a.stopJudging()
	a.stopTriggers()
	a.insights.judge, a.insights.trigger = nil, nil
	entries, _ := os.ReadDir(a.evalsDir())
	var failed error
	for _, e := range entries {
		if e.IsDir() {
			if err := (evals.Store{Dir: filepath.Join(a.evalsDir(), e.Name())}).Clear(); err != nil {
				failed = err
			}
		}
	}
	if failed != nil {
		a.model.status, a.model.failed = "清除失败："+failed.Error(), true
		return
	}
	a.model.status, a.model.failed = "已清除所有技能的评测数据。", false
}

func (a *App) settingsModel(c *ui.Context) {
	t := c.Theme()
	m := &a.model
	field := func(label string, body func()) {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(13).TextColor(t.TextMuted).Width(56).Shrink(0)
			body()
		})
	}
	ui.Column(c).Gap(8).Children(func() {
		if m.loadErr != "" {
			callout(c, feedback.Warning, m.loadErr)
		}
		field("接口", func() {
			proto := 0
			if m.draft.Protocol == llm.OpenAI {
				proto = 1
			}
			if segmented(c, "接口类型", &proto, "Anthropic", "OpenAI 兼容") {
				m.draft.Protocol = []string{llm.Anthropic, llm.OpenAI}[proto]
				if strings.TrimSpace(m.draft.Model) == llm.DefaultModel && proto == 1 {
					m.draft.Model = ""
				}
				m.status = ""
			}
		})
		field("地址", func() {
			hint := "留空使用 api.anthropic.com"
			if m.draft.Protocol == llm.OpenAI {
				hint = "例如 https://api.openai.com/v1，留空即此地址"
			}
			textField(c, &m.draft.BaseURL, "模型服务地址", hint)
		})
		field("模型", func() {
			textField(c, &m.draft.Model, "模型名称", "模型的完整名称")
		})
		field("密钥", func() {
			hint := "密钥，或 $环境变量名"
			if m.draft.Protocol != llm.OpenAI {
				hint += "；留空时使用环境里已有的 Anthropic 凭据"
			}
			passwordField(c, &m.draft.APIKey, "密钥", hint)
		})
		ui.Row(c).Gap(12).AlignItems(ui.Start).Padding(10, 12).Radius(8).Border(1, t.Border).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Text(c, "允许把会话内容发给这个服务").FontSize(13).FontWeight(500)
				ui.Text(c, "开启后，起草标准和评审时会发送 SKILL.md 和所选那一轮的提示词、回复、工具输入输出；发送前先显示数量和估算的 Token，像密钥的内容会先隐去。结论和引文保存在本机 "+shortPath(a.home, a.evalsDir())+"。").
					FontSize(12).LineHeight(1.5).TextColor(token(t, "ink-3"))
			})
			on := m.draft.Enabled
			if choice.Switch(c, &on, choice.SwitchProps{Size: lobe.Small}).Label("允许发送会话内容").Changed() {
				m.draft.Enabled = on
				a.saveModel()
			}
		})
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			label := "保存并测试"
			if m.testing {
				label = "正在测试…"
			}
			if primaryButton(c, "model-test", "", label).Disabled(m.testing || strings.TrimSpace(m.draft.Model) == "").Clicked() {
				a.testModel()
			}
			tone := t.TextMuted
			if m.failed {
				tone = t.Warning
			}
			withTip(c, ui.Text(c, m.status).FontSize(12.5).TextColor(tone).Grow(1).MinWidth(0).MaxLines(2), m.status)
			clear := "清除评测数据"
			if m.clearing {
				clear = "确认清除"
			}
			if outlineButton(c, "model-clear", clear).Clicked() {
				if m.clearing {
					a.clearEvals()
				}
				m.clearing = !m.clearing
			}
		})
	})
}
