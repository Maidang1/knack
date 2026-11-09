package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// usageState reconciles an authoritative request stream with cumulative-keyed
// fallback reports. Only numbers, identifiers and timings are checkpointed.
// Fallback requests stay provisional: they must not consume the authoritative
// stream's timing anchor, create turns, or otherwise change its statistics.
type usageState struct {
	Totals          map[string]Tokens
	Identity, Scope string
	Epoch           int64
	Seen            map[string]bool
	Primary         map[string]int
	Queues          map[string][]string
	Pending         map[string]pendingUsage
	At              time.Time
	Think           time.Duration
}

type pendingUsage struct {
	Request  Request
	Turn     int
	Sequence int64
	Owner    bool // the report belongs to this file's first session identity
}

func (p *parser) usage() *usageState {
	if p.s.Usage == nil {
		p.s.Usage = &usageState{Seen: map[string]bool{}, Primary: map[string]int{}, Queues: map[string][]string{}, Pending: map[string]pendingUsage{}, Totals: map[string]Tokens{}}
	}
	return p.s.Usage
}

func (u *usageState) matchKey(tokens Tokens) string {
	// An input boundary separates independent requests with identical usage.
	// Within one boundary, reports of each cost pair in occurrence order.
	return u.Scope + "\x00" + fmt.Sprintf("%d:%d:%d:%d:%d", u.Epoch, tokens.Input, tokens.Output, tokens.CacheRead, tokens.CacheWrite)
}

func tokenKey(tokens Tokens) string {
	return fmt.Sprintf("%d:%d:%d:%d", tokens.Input, tokens.Output, tokens.CacheRead, tokens.CacheWrite)
}

func (u *usageState) cumulativeKey(tokens Tokens) string { return u.Scope + "\x00" + tokenKey(tokens) }

func (p *parser) primaryUsage(tokens Tokens) {
	u := p.usage()
	match := u.matchKey(tokens)
	if queue := u.Queues[match]; len(queue) > 0 {
		delete(u.Pending, queue[0])
		if len(queue) == 1 {
			delete(u.Queues, match)
		} else {
			u.Queues[match] = queue[1:]
		}
		// Its cumulative position was already reserved by the fallback report.
		return
	}
	total := u.Totals[u.Scope]
	total.add(tokens)
	u.Totals[u.Scope] = total
	u.Primary[u.cumulativeKey(total)]++
}

func (p *parser) fallbackUsage(a object, at time.Time) {
	u := p.usage()
	scope := first(u.Scope, p.s.Session.Path, p.s.Session.ID)
	sum := sha256.Sum256([]byte(scope + "\x00" + str(a, "key")))
	key := hex.EncodeToString(sum[:])
	if u.Seen[key] {
		return
	}
	u.Seen[key] = true
	var tokens Tokens
	_ = json.Unmarshal(a["tokens"], &tokens)
	if tokens.Total() == 0 {
		return
	}
	var total Tokens
	_ = json.Unmarshal(a["total"], &total)
	cumulative := u.cumulativeKey(total)
	if u.Primary[cumulative] > 0 {
		u.Primary[cumulative]--
		if u.Primary[cumulative] == 0 {
			delete(u.Primary, cumulative)
		}
		return
	}
	match := u.matchKey(tokens)
	// Reserve missing legacy requests in the cumulative stream. A later primary
	// in a new input epoch continues after them instead of matching equal costs.
	current := u.Totals[u.Scope]
	u.Totals[u.Scope] = Tokens{max(current.Input, total.Input), max(current.Output, total.Output), max(current.CacheRead, total.CacheRead), max(current.CacheWrite, total.CacheWrite)}
	r := Request{ID: "usage-" + key, Model: p.s.Model, End: at, Tokens: tokens, UsageKnown: flag(a, "known")}
	input, think := p.s.InputAt, p.s.Think
	if !u.At.IsZero() && !input.After(u.At) {
		input, think = u.At, max(0, think-u.Think)
	}
	setInputTiming(&r, input, at, think)
	u.At, u.Think = at, p.s.Think
	u.Pending[key] = pendingUsage{Request: r, Turn: len(p.s.Session.Turns) - 1, Sequence: p.s.Sequence, Owner: scope == u.Identity}
	u.Queues[match] = append(u.Queues[match], key)
}

func setInputTiming(r *Request, input, at time.Time, think time.Duration) {
	r.Start, r.Elapsed, r.Thinking, r.Generation = time.Time{}, Duration{}, Duration{}, Duration{}
	if !input.IsZero() && !at.Before(input) {
		r.Start, r.Elapsed = input, estimated(at.Sub(input))
		if think > 0 {
			r.Thinking = Duration{Value: min(think, r.Elapsed.Value), Known: true}
		}
		if r.Elapsed.Value > r.Thinking.Value {
			r.Generation = estimated(r.Elapsed.Value - r.Thinking.Value)
		}
	}
}

// fallbackOwners selects one occurrence of replayed history across files.
// Prefer its original owner, then the earliest report, then stable path order.
// Selection is rebuilt from all checkpoints, including after cache-only startup.
func (r *Reader) fallbackOwners(logs []logFile) map[string]string {
	owners := map[string]string{}
	records := map[string]pendingUsage{}
	for _, file := range logs {
		st := r.files[file.path]
		if st == nil || st.State.Usage == nil {
			continue
		}
		for key, candidate := range st.State.Usage.Pending {
			old, exists := records[key]
			prefer := !exists || candidate.Owner && !old.Owner
			if exists && candidate.Owner == old.Owner {
				order := candidate.Request.End.Compare(old.Request.End)
				prefer = order < 0 || order == 0 && file.path < owners[key]
			}
			if !prefer {
				continue
			}
			records[key], owners[key] = candidate, file.path
		}
	}
	return owners
}

func appendFallbacks(s *Session, state *usageState, path string, owners map[string]string) {
	if state == nil {
		return
	}
	var pending []pendingUsage
	early := false
	for key, item := range state.Pending {
		if owners[key] != path {
			continue
		}
		pending = append(pending, item)
		early = early || item.Turn < 0
	}
	slices.SortFunc(pending, func(a, b pendingUsage) int {
		if a.Sequence < b.Sequence {
			return -1
		}
		if a.Sequence > b.Sequence {
			return 1
		}
		return 0
	})
	if len(pending) == 0 {
		return
	}
	if early {
		s.Turns = append([]Turn{{ID: "usage-history"}}, s.Turns...)
	}
	for _, item := range pending {
		index := item.Turn
		if early {
			index++
		}
		t := &s.Turns[index]
		req := item.Request
		t.Requests = append(t.Requests, req)
		if t.Start.IsZero() {
			t.Start = req.End
		}
		if req.End.After(t.End) {
			t.End = req.End
		}
	}
	// Native and fallback requests can coexist in one turn. Keep display order
	// chronological without changing the mutable checkpoint's request indices.
	for i := range s.Turns {
		slices.SortStableFunc(s.Turns[i].Requests, func(a, b Request) int { return a.End.Compare(b.End) })
	}
}
