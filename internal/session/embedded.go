package session

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

//go:embed plugins/*.js
var embeddedScripts embed.FS

// streamingFormat is the trusted, bundled form of the JS plugin protocol.
// The host owns discovery, byte checkpoints and decompression. JS owns all
// source-format decisions and emits source-independent statistics/replay ops.
// Unlike user scripts it has no filesystem API and never reads a whole log.
type streamingFormat struct {
	mu                   sync.Mutex
	vm                   *goja.Runtime
	def                  *goja.Object
	transform, stringify goja.Callable
	info                 AgentInfo
	paths                layout
	hash                 string
}

func embeddedFormat(name string) *streamingFormat {
	common, err := embeddedScripts.ReadFile("plugins/common.js")
	if err != nil {
		panic(err)
	}
	source, err := embeddedScripts.ReadFile("plugins/" + name + ".js")
	if err != nil {
		panic(err)
	}
	source = append(append(common, '\n'), source...)
	f := &streamingFormat{vm: goja.New()}
	k := f.vm.NewObject()
	_ = k.Set("registerAgent", func(v *goja.Object) { f.def = v })
	_ = f.vm.Set("knack", k)
	if _, err := f.vm.RunScript(name+".js", string(source)); err != nil {
		panic(err)
	}
	f.stringify, _ = goja.AssertFunction(f.vm.Get("JSON").ToObject(f.vm).Get("stringify"))
	f.transform, _ = goja.AssertFunction(f.def.Get("readRecord"))
	f.export(f.def.Get("info"), &f.info)
	f.export(f.def.Get("roots"), &f.paths.roots)
	f.export(f.def.Get("suffixes"), &f.paths.suffixes)
	sum := sha256.Sum256(source)
	f.hash = hex.EncodeToString(sum[:])
	return f
}

func (f *streamingFormat) export(v goja.Value, out any) {
	b, err := f.stringify(goja.Undefined(), v)
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal([]byte(b.String()), out); err != nil {
		panic(err)
	}
}
func (f *streamingFormat) Info() AgentInfo { return f.info }
func (f *streamingFormat) version() string { return "stream/1/" + f.hash }
func (f *streamingFormat) layout() layout  { return f.paths }
func (f *streamingFormat) invoke(name string, out any, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn, ok := goja.AssertFunction(f.def.Get(name))
	if !ok {
		panic("missing streaming plugin function: " + name)
	}
	values := make([]goja.Value, len(args))
	for i, v := range args {
		values[i] = f.vm.ToValue(v)
	}
	v, err := fn(f.def, values...)
	if err != nil {
		panic(err)
	}
	f.export(v, out)
}
func (f *streamingFormat) visit(parts []string, dir bool) (string, string, bool) {
	var v struct {
		Parent, Child string
		OK            bool
	}
	f.invoke("visit", &v, parts, dir)
	return v.Parent, v.Child, v.OK
}
func (f *streamingFormat) parentLog(child logFile) string {
	var path string
	f.invoke("parent", &path, filepath.ToSlash(child.path), child.parent)
	return filepath.FromSlash(path)
}
func (f *streamingFormat) header(head []byte) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn, ok := goja.AssertFunction(f.def.Get("readHeader"))
	if !ok {
		return nil
	}
	v, err := fn(f.def, f.vm.ToValue(string(head[:min(len(head), largeHeadBytes)])))
	if err != nil || goja.IsNull(v) || goja.IsUndefined(v) {
		return nil
	}
	v, err = f.stringify(goja.Undefined(), v)
	if err != nil {
		return nil
	}
	return []byte(v.String())
}

func (f *streamingFormat) normalize(b []byte, mode string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// JSON.parse inside the VM avoids exposing host objects to plugin code.
	parse, _ := goja.AssertFunction(f.vm.Get("JSON").ToObject(f.vm).Get("parse"))
	o, err := parse(goja.Undefined(), f.vm.ToValue(string(b)))
	if err != nil {
		return nil, fmt.Errorf("无效 JSON 记录")
	}
	v, err := f.transform(f.def, o, f.vm.ToValue(mode))
	if err != nil {
		return nil, fmt.Errorf("插件转换失败：%w", err)
	}
	v, err = f.stringify(goja.Undefined(), v)
	if err != nil {
		return nil, err
	}
	return []byte(v.String()), nil
}
func (*streamingFormat) record(o object) (time.Time, bool) { return stamp(o), str(o, "type") != "" }
func (*streamingFormat) recordID(o object) string          { return str(o, "id") }

// These operations describe normalized model actions, never native log fields.
func (*streamingFormat) parse(p *parser, o object, at time.Time)             { applyStats(p, o, at) }
func (*streamingFormat) replay(tr *transcriptReader, o object, at time.Time) { applyReplay(tr, o, at) }

func applyStats(p *parser, o object, at time.Time) {
	s := p.s
	for _, a := range objects(o["stats"]) {
		id := str(a, "id")
		duration := Duration{Value: millis(num(a, "duration")), Known: a["duration"] != nil}
		switch str(a, "op") {
		case "known":
			s.FormatKnown = true
		case "usageScope":
			p.usage().Scope = str(a, "scope")
		case "fallbackUsage":
			p.fallbackUsage(a, at)
		case "metadata":
			if flag(a, "identityOnce") && id != "" {
				u := p.usage()
				if u.Identity != "" && u.Identity != id {
					continue
				}
				u.Identity = id
			}
			if id != "" && (!flag(a, "preserveChild") || s.Session.ParentID == "") {
				s.Session.ID = id
			}
			s.Session.CWD = first(str(a, "cwd"), s.Session.CWD)
			s.Session.GitBranch = first(str(a, "branch"), s.Session.GitBranch)
			s.Session.Title = first(str(a, "title"), s.Session.Title)
			if flag(a, "replace") {
				s.Session.CWD, s.Session.GitBranch = str(a, "cwd"), str(a, "branch")
			}
			if parent := str(a, "parent"); parent != "" {
				s.Session.ParentID, s.Session.AgentID = parent, s.Session.ID
			}
		case "model":
			s.Model = str(a, "model")
		case "input":
			if s.Usage != nil {
				s.Usage.Epoch++
				s.Usage.At, s.Usage.Think = time.Time{}, 0
			}
			s.InputAt, s.Think = at, 0
			if flag(a, "resetPrompt") {
				s.Prompted = false
			}
		case "think":
			s.Think += duration.Value
		case "prompt":
			body := strings.TrimSpace(str(a, "text"))
			if !s.Prompted && body != "" && len(s.Session.Turns) > 0 {
				s.Prompted = true
				if len(s.Session.Turns) > 1 {
					s.Session.Turns[len(s.Session.Turns)-1].Corrects = correction(body)
				}
			}
		case "begin":
			if flag(a, "interruptNotice") && len(s.Session.Turns) > 0 {
				t := p.turn(at)
				if !t.Interrupted {
					t.Interrupted = true
					p.event("interrupted", at, Duration{})
				}
				s.LastReply = at
				continue
			}
			exists := false
			if flag(a, "unique") {
				for _, t := range s.Session.Turns {
					exists = exists || (id != "" && t.ID == id)
				}
			}
			if exists {
				continue
			}
			p.begin(id, at)
			if flag(a, "command") {
				body := str(a, "text")
				s.Command = slashCommand(body)
				if s.Command == "" && len(s.Session.Turns) > 1 {
					s.Session.Turns[len(s.Session.Turns)-1].Corrects = correction(body)
				}
			}
		case "request":
			if flag(a, "unique") {
				if _, ok := p.requests[id]; ok || id == "" {
					continue
				}
			}
			var tokens Tokens
			_ = json.Unmarshal(a["tokens"], &tokens)
			if flag(a, "correlateUsage") {
				p.primaryUsage(tokens)
			}
			model := str(a, "model")
			if flag(a, "inputTiming") {
				model = s.Model
			}
			p.request(id, model, at, tokens, flag(a, "known"), millis(num(a, "think")), flag(a, "error"))
			if flag(a, "inputTiming") {
				pos := p.requests[id]
				r := &s.Session.Turns[pos.turn].Requests[pos.item]
				setInputTiming(r, s.InputAt, at, s.Think)
				s.InputAt, s.Think = at, 0
			}
		case "use":
			p.use(id, str(a, "name"), at, a["input"])
			if flag(a, "detectSkills") {
				p.readSkills(id, at, a["input"])
			}
		case "result":
			// result() is shared with Factory; adapt the normalized outcome to it.
			p.result(id, at, object{"toolUseResult": a["result"], "permissionDecision": a["permission"]}, object{"is_error": a["error"]})
			p.output(id, str(a, "output"))
		case "skillContent":
			p.skillContent(id, flag(a, "companion"), at, str(a, "text"))
		case "reply":
			if flag(a, "clear") {
				s.LastReply = time.Time{}
			} else if flag(a, "force") || (flag(a, "content") && !p.pendingTools()) {
				s.LastReply = at
			}
		case "duration":
			p.turn(at).Elapsed = duration
		case "away":
			s.Away = true
		case "event":
			p.event(str(a, "kind"), at, duration)
		case "interrupt":
			if flag(a, "onlyExisting") && len(s.Session.Turns) == 0 {
				continue
			}
			t := p.turn(at)
			if !t.Interrupted {
				t.Interrupted = true
				p.event("interrupted", at, Duration{})
			}
			if flag(a, "reply") {
				s.LastReply = at
			}
		case "finish":
			for i := len(s.Session.Turns) - 1; i >= 0; i-- {
				t := &s.Session.Turns[i]
				if id != "" && t.ID != id {
					continue
				}
				t.End, t.Elapsed = at, duration
				if flag(a, "interrupted") {
					t.Interrupted = true
					t.Events = append(t.Events, Event{Kind: "interrupted", At: at})
				} else if i == len(s.Session.Turns)-1 && s.LastReply.IsZero() {
					s.LastReply = at
				}
				break
			}
		}
	}
}
func applyReplay(tr *transcriptReader, o object, at time.Time) {
	for _, a := range objects(o["replay"]) {
		id := str(a, "id")
		switch str(a, "op") {
		case "model":
			tr.model = str(a, "model")
		case "associate":
			for _, i := range tr.pending {
				tr.out.Entries[i].RequestID = str(a, "rid")
			}
			tr.pending = tr.pending[:0]
		case "timing":
			if tr.timing == nil {
				tr.timing = map[string][2]time.Time{}
			}
			tr.timing[str(a, "kind")] = [2]time.Time{time.UnixMilli(num(a, "from")), time.UnixMilli(num(a, "to"))}
		case "result":
			tr.result(id, at, str(a, "text"), flag(a, "error"))
		case "entry":
			if flag(a, "once") {
				if tr.system {
					continue
				}
				tr.system = true
			}
			e := Entry{Kind: EntryKind(str(a, "kind")), Start: at, End: at, RequestID: str(a, "rid"), Name: str(a, "name"), Text: str(a, "text"), Error: flag(a, "error")}
			if flag(a, "currentModel") {
				e.Name = tr.model
			}
			if kind := str(a, "measured"); kind != "" {
				if span, ok := tr.timing[kind]; ok {
					e.Start, e.End = span[0], span[1]
					delete(tr.timing, kind)
				}
			}
			if e.Kind == EntryTool {
				if _, ok := tr.tools[id]; ok || id == "" {
					continue
				}
				e.ToolID, e.End, e.Input = id, time.Time{}, jsonText(a["input"])
			}
			i := tr.add(e)
			if e.Kind == EntryTool {
				tr.tools[id] = i
			}
			if flag(a, "pending") {
				tr.pending = append(tr.pending, i)
			}
		}
	}
}

func (p *parser) pendingTools() bool {
	for _, t := range p.s.Session.Turns {
		for _, tool := range t.Tools {
			if tool.End.IsZero() {
				return true
			}
		}
	}
	return false
}
