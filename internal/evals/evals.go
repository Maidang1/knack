// Package evals judges a skill by criteria. An evaluator is a list of
// criteria for one skill: rules Knack checks itself from what the log
// records, and statements a model judges by reading the turn. A verdict is
// the model's judgement of one load of the skill, with the evidence it
// quoted and what the person later said of it.
package evals

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"knack/internal/insight"
)

const (
	KindLLM  = "llm"
	KindRule = "rule"
)

// The facts a rule criterion can read from an episode.
const (
	RuleNoFailures     = "no_failed_tools"
	RuleNotInterrupted = "not_interrupted"
	RuleNotCorrected   = "not_corrected"
	RuleUsedFiles      = "used_skill_files"
)

const (
	Pass = "pass"
	Fail = "fail"
	NA   = "na" // the turn's task did not call for this criterion
)

// Criterion is one thing to check about a load of the skill.
type Criterion struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Kind string `json:"kind"`
	Rule string `json:"rule,omitempty"`
}

// Evaluator is a skill's criteria.
type Evaluator struct {
	Skill    string      `json:"skill"`
	Created  time.Time   `json:"created"`
	Model    string      `json:"model,omitempty"` // the model that drafted it
	Criteria []Criterion `json:"criteria"`
}

// Judged are the criteria a model has to read the turn for.
func (e Evaluator) Judged() []Criterion {
	var out []Criterion
	for _, c := range e.Criteria {
		if c.Kind == KindLLM && strings.TrimSpace(c.Text) != "" {
			out = append(out, c)
		}
	}
	return out
}

// Hash identifies the judged criteria, so verdicts made against an earlier
// wording are known to be stale.
func (e Evaluator) Hash() string {
	h := sha256.New()
	for _, c := range e.Judged() {
		h.Write([]byte(c.ID + "\x00" + strings.TrimSpace(c.Text) + "\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Rules are the criteria Knack can check for a skill without a model:
// tool failures only where the log records them, the skill's own files only
// where it has some.
func Rules(outcomes, files bool) []Criterion {
	out := []Criterion{
		{ID: RuleNotInterrupted, Kind: KindRule, Rule: RuleNotInterrupted, Text: "这一轮没有被用户中断"},
		{ID: RuleNotCorrected, Kind: KindRule, Rule: RuleNotCorrected, Text: "下一轮开头不像在纠正这一轮"},
	}
	if outcomes {
		out = append(out, Criterion{ID: RuleNoFailures, Kind: KindRule, Rule: RuleNoFailures, Text: "加载之后的工具调用没有失败"})
	}
	if files {
		out = append(out, Criterion{ID: RuleUsedFiles, Kind: KindRule, Rule: RuleUsedFiles, Text: "用到了技能自带的脚本或参考文件"})
	}
	return out
}

// RuleVerdict checks a rule criterion against what the log recorded.
func RuleVerdict(c Criterion, ep insight.Episode) string {
	ok := false
	switch c.Rule {
	case RuleNoFailures:
		ok = ep.Failed == 0
	case RuleNotInterrupted:
		ok = !ep.Interrupted
	case RuleNotCorrected:
		ok = !ep.Corrected
	case RuleUsedFiles:
		ok = ep.Uses > 0
	default:
		return NA
	}
	if ok {
		return Pass
	}
	return Fail
}

// The person's word on a verdict.
const (
	ReviewOK    = "ok"
	ReviewWrong = "wrong"
)

// Result is the model's judgement of one criterion.
type Result struct {
	ID      string `json:"id"`
	Verdict string `json:"verdict"`
	Step    int    `json:"step,omitempty"`  // the excerpt step the quote is from
	Quote   string `json:"quote,omitempty"` // copied from the turn, kept short
	Reason  string `json:"reason,omitempty"`
	// Verified says the quote really is in the turn. A judgement resting
	// on words that are not there should not be trusted.
	Verified bool   `json:"verified,omitempty"`
	Review   string `json:"review,omitempty"`
}

// Final is the verdict after the person's correction.
func (r Result) Final() string {
	if r.Review != ReviewWrong {
		return r.Verdict
	}
	switch r.Verdict {
	case Pass:
		return Fail
	case Fail:
		return Pass
	}
	return r.Verdict
}

// Verdict is the judgement of one load.
type Verdict struct {
	Evaluator string    `json:"evaluator"` // Evaluator.Hash it was judged against
	Model     string    `json:"model"`
	At        time.Time `json:"at"`
	Results   []Result  `json:"results"`
	// Truncated says the turn was too long to send whole.
	Truncated     bool  `json:"truncated,omitempty"`
	Input, Output int64 `json:",omitempty"`
}

// Key names a load across runs: the turn that loaded the skill.
func Key(ep insight.Episode) string {
	return string(ep.Agent) + ":" + ep.SessionID + ":" + ep.TurnID
}

// Tally sums verdicts.
type Tally struct {
	Pass, Fail, NA int
	Reviewed       int // verdicts the person looked at
	Agreed         int // and found right
}

func (t *Tally) add(r Result) {
	switch r.Final() {
	case Pass:
		t.Pass++
	case Fail:
		t.Fail++
	default:
		t.NA++
	}
	switch r.Review {
	case ReviewOK:
		t.Reviewed++
		t.Agreed++
	case ReviewWrong:
		t.Reviewed++
	}
}

// Count tallies a verdict's results.
func (v Verdict) Count() Tally {
	var t Tally
	for _, r := range v.Results {
		t.add(r)
	}
	return t
}

// Store keeps one skill's evaluator and verdicts in a directory.
type Store struct{ Dir string }

// SkillDir is where a skill's evaluation lives beneath the evals directory.
func SkillDir(root, skill string) string {
	return filepath.Join(root, strings.NewReplacer("/", "-", ":", "-", "\\", "-").Replace(skill))
}

// Evaluator reads the skill's evaluator; nil when there is none yet.
func (s Store) Evaluator() (*Evaluator, error) {
	var e Evaluator
	if ok, err := readJSON(filepath.Join(s.Dir, "evaluator.json"), &e); !ok || err != nil {
		return nil, err
	}
	return &e, nil
}

func (s Store) SaveEvaluator(e Evaluator) error {
	return writeJSON(filepath.Join(s.Dir, "evaluator.json"), e)
}

// Verdicts reads the verdicts by load.
func (s Store) Verdicts() (map[string]Verdict, error) {
	out := map[string]Verdict{}
	if _, err := readJSON(filepath.Join(s.Dir, "verdicts.json"), &out); err != nil {
		return map[string]Verdict{}, err
	}
	return out, nil
}

func (s Store) SaveVerdicts(v map[string]Verdict) error {
	return writeJSON(filepath.Join(s.Dir, "verdicts.json"), v)
}

// Clear removes the evaluator, verdicts, causes and trigger set, and the
// directory if nothing else is in it.
func (s Store) Clear() error {
	var failed error
	for _, name := range []string{"evaluator.json", "verdicts.json", "causes.json", "triggers.json", "eval_set.json"} {
		if err := os.Remove(filepath.Join(s.Dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			failed = errors.Join(failed, err)
		}
	}
	_ = os.Remove(s.Dir)
	return failed
}

func readJSON(path string, v any) (bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(b, v)
}

// Verdicts quote the person's sessions, so they are theirs alone to read.
func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Corrections are the verdicts the person marked wrong, newest first, for
// showing the judge where it erred before.
func Corrections(verdicts map[string]Verdict, limit int) []Result {
	type dated struct {
		r  Result
		at time.Time
	}
	var all []dated
	for _, v := range verdicts {
		for _, r := range v.Results {
			if r.Review == ReviewWrong {
				all = append(all, dated{r, v.At})
			}
		}
	}
	slices.SortStableFunc(all, func(a, b dated) int { return b.at.Compare(a.at) })
	var out []Result
	for _, d := range all[:min(len(all), limit)] {
		out = append(out, d.r)
	}
	return out
}
