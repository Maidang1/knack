package app

import (
	"context"
	"os"
	"slices"
	"time"

	"knack/internal/session"
)

// Replay content is read from the log when a session's turns are viewed and
// held in memory for a few recent sessions only. It is never cached to disk.
const replayKeep = 4

type replayState struct {
	path    string
	agent   session.Agent
	size    int64
	modTime time.Time
	loading bool
	err     string
	tr      *session.Transcript
	index   *replayIndex
	indexOf *sessionDetailData
}

type replayIndex struct {
	prompts   [][]session.Entry // per turn: what the person typed
	context   []int             // per turn: injected context entries
	events    [][]session.Entry // per turn: interruptions, compaction, errors
	byRequest map[string][]session.Entry
	calls     map[string][]session.Entry // tool calls each request made
	byTool    map[string]session.Entry
}

// sessionReplay returns the replay content of d's log, starting a background
// read when it is missing or the log changed. The result may still be loading.
func (a *App) sessionReplay(d *sessionDetailData) *replayState {
	path := d.session.Path
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	var r *replayState
	for i, x := range a.sessions.replay {
		if x.path == path {
			r = x
			// Most recently used first.
			a.sessions.replay = append([]*replayState{r}, slices.Delete(a.sessions.replay, i, i+1)...)
			break
		}
	}
	if r != nil && (r.loading || err != nil || info.Size() == r.size && info.ModTime().Equal(r.modTime)) {
		if r.tr != nil && r.indexOf != d {
			r.index, r.indexOf = buildReplayIndex(d, r.tr), d
		}
		return r
	}
	if err != nil {
		return &replayState{path: path, err: "无法读取日志，回放内容暂不可用"}
	}
	if r == nil {
		r = &replayState{path: path, agent: d.session.Agent}
		a.sessions.replay = append([]*replayState{r}, a.sessions.replay...)
		if len(a.sessions.replay) > replayKeep {
			a.sessions.replay = a.sessions.replay[:replayKeep]
		}
	}
	r.loading, r.size, r.modTime = true, info.Size(), info.ModTime()
	run := func() {
		tr, err := session.ReadTranscript(context.Background(), path, d.session.Agent)
		a.post(func() {
			r.loading = false
			r.err = ""
			if err != nil {
				r.err = err.Error()
			}
			r.tr, r.index, r.indexOf = tr, nil, nil
		})
	}
	if a.win == nil {
		run()
		if r.tr != nil {
			r.index, r.indexOf = buildReplayIndex(d, r.tr), d
		}
	} else {
		go run()
	}
	return r
}

// Entries join turns by time and requests/tools by their log identifiers.
func buildReplayIndex(d *sessionDetailData, tr *session.Transcript) *replayIndex {
	x := &replayIndex{
		prompts:   make([][]session.Entry, len(d.turns)),
		context:   make([]int, len(d.turns)),
		events:    make([][]session.Entry, len(d.turns)),
		byRequest: map[string][]session.Entry{},
		calls:     map[string][]session.Entry{},
		byTool:    map[string]session.Entry{},
	}
	turnAt := func(at time.Time) int {
		i, _ := slices.BinarySearchFunc(d.turns, at, func(t sessionTurnView, at time.Time) int {
			return t.turn.Start.Compare(at)
		})
		// The turn starting at or before at; entries before the first turn
		// (session preamble) belong to the first.
		if i < len(d.turns) && d.turns[i].turn.Start.Equal(at) {
			return i
		}
		return max(0, i-1)
	}
	for _, e := range tr.Entries {
		if e.ToolID != "" {
			x.byTool[e.ToolID] = e
			if e.RequestID != "" {
				x.calls[e.RequestID] = append(x.calls[e.RequestID], e)
			}
		}
		if e.RequestID != "" && e.Kind != session.EntryTool {
			x.byRequest[e.RequestID] = append(x.byRequest[e.RequestID], e)
		}
		if len(d.turns) == 0 {
			continue
		}
		i := turnAt(e.Start)
		switch e.Kind {
		case session.EntryUser:
			x.prompts[i] = append(x.prompts[i], e)
		case session.EntryContext:
			x.context[i]++
		case session.EntryEvent:
			if e.RequestID == "" {
				x.events[i] = append(x.events[i], e)
			}
		}
	}
	return x
}

func (r *replayState) ready() *replayIndex {
	if r == nil {
		return nil
	}
	return r.index
}
