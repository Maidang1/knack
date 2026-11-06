package session

import (
	"fmt"
	"testing"
	"time"
)

func TestAggregate(t *testing.T) {
	t.Run("normalized usage, percentiles, models and anomalies", func(t *testing.T) {
		at := time.Date(2026, 11, 1, 17, 0, 0, 0, time.UTC)
		turn := Turn{ID: "turn", Start: at}
		for i := 1; i <= 10; i++ {
			turn.Requests = append(turn.Requests, Request{ID: fmt.Sprint(i), Model: "test-model", End: at, UsageKnown: true, Tokens: Tokens{Input: 10, Output: int64(i), CacheRead: 30, CacheWrite: 10}, Generation: estimated(time.Second), Elapsed: estimated(2 * time.Minute)})
		}
		turn.Tools = []ToolCall{{ID: "slow-tool", End: at, Elapsed: estimated(2 * time.Minute)}}
		turn.Waits = []Wait{{Kind: "permission", ToolID: "slow-tool", End: at, Elapsed: estimated(2 * time.Minute)}}
		turn.Events = []Event{{Kind: "timeout", At: at}, {Kind: "away_summary", At: at}}
		turn.Skills = []SkillCall{{Name: "build", At: at}, {Name: "build", At: at}}
		snap := &Snapshot{Sessions: []Session{{ID: "one", Agent: Claude, Start: at, End: at, Turns: []Turn{turn}}}}
		s := Aggregate(snap, Filter{Location: time.FixedZone("UTC+8", 8*3600)})
		if s.Sessions != 1 || s.Requests != 10 || s.Tokens.Total() != 555 || s.Tokens.Output != 55 || s.CacheHit != .6 || !s.CacheHitKnown {
			t.Fatalf("usage %+v", s)
		}
		if s.OutputRate.Median != 5.5 || s.OutputRate.P90 != 9 || s.OutputRate.Samples != 10 || !s.OutputRate.Estimated {
			t.Fatalf("percentiles %+v", s.OutputRate)
		}
		if len(s.Daily) != 1 || s.Daily[0].Date != "2026-11-02" || s.Daily[0].Tokens.Total() != 555 {
			t.Fatalf("daily %+v", s.Daily)
		}
		if len(s.ByModel) != 1 || s.ByModel[0].Sessions != 1 || s.ByModel[0].OutputRate.P90 != 9 || s.ByAgent[0].Sessions != 1 {
			t.Fatal("group summaries")
		}
		if len(s.Skills) != 1 || s.Skills[0].Calls != 2 || s.Skills[0].Sessions != 1 || len(s.Anomalies) != 13 {
			t.Fatalf("skills/anomalies %+v", s)
		}
		for _, filter := range []Filter{{From: at.Add(time.Nanosecond)}, {To: at}, {Agent: Codex}} {
			out := Aggregate(snap, filter)
			if out.Sessions != 0 || out.Requests != 0 || out.OutputRate.Samples != 0 || len(out.ByAgent) != 0 {
				t.Fatalf("range exclusion %+v", out)
			}
		}
		if included := Aggregate(snap, Filter{From: at, To: at.Add(time.Second)}); included.Requests != 10 {
			t.Fatal("half-open range start")
		}
	})
	t.Run("missing usage never becomes a measured zero", func(t *testing.T) {
		s := fixtureState(t, Factory, "factory.jsonl")
		sum := Aggregate(&Snapshot{Sessions: []Session{s.Session}}, Filter{})
		if sum.UnmeteredRequests != 3 || !sum.Incomplete || sum.CacheHitKnown || sum.OutputRate.Samples != 0 {
			t.Fatalf("unknown usage %+v", sum)
		}
		if empty := Aggregate(nil, Filter{}); empty.CacheHitKnown || empty.Sessions != 0 {
			t.Fatal("empty snapshot")
		}
	})
}

func TestFormatDuration(t *testing.T) {
	for _, tc := range []struct {
		duration time.Duration
		want     string
	}{
		{-time.Second, "0s"}, {0, "0s"}, {42 * time.Minute, "42m"}, {372 * time.Second, "6m12s"}, {4320 * time.Second, "1h12m"}, {time.Hour, "1h"}, {6*time.Minute + time.Second, "6m01s"}, {1500 * time.Millisecond, "1s"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			if got := FormatDuration(tc.duration); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestFoldLongStep(t *testing.T) {
	for _, tc := range []struct {
		step, turn time.Duration
		want       bool
	}{
		{time.Minute, 100 * time.Second, false}, {61 * time.Second, 122 * time.Second, false}, {61 * time.Second, 121 * time.Second, true}, {time.Hour, 0, false}, {0, time.Hour, false},
	} {
		t.Run(fmt.Sprintf("%v/%v", tc.step, tc.turn), func(t *testing.T) {
			if got := FoldLongStep(tc.step, tc.turn); got != tc.want {
				t.Fatalf("fold=%v want %v", got, tc.want)
			}
		})
	}
}
