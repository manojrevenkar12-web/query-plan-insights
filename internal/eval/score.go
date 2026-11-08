package eval

import (
	"fmt"
	"sort"
	"strings"

	"github.com/manojrevenkar12-web/query-plan-insights/internal/store"
)

// Outcome is the score of one scenario.
type Outcome struct {
	Set              string `json:"set"`
	Scenario         string `json:"scenario"`
	About            string `json:"about"`
	App              string `json:"app"`
	Control          bool   `json:"control"`
	Target           string `json:"target,omitempty"`
	WantKind         string `json:"wantKind,omitempty"`
	WantCause        string `json:"wantCause,omitempty"`
	WantInSuggestion string `json:"wantInSuggestion,omitempty"`

	Detected          bool    `json:"detected"`
	DetectSeconds     float64 `json:"detectSeconds,omitempty"`
	CauseAtOpen       string  `json:"causeAtOpen,omitempty"`
	CauseCorrect      bool    `json:"causeCorrect"`
	FinalCause        string  `json:"finalCause,omitempty"`
	FinalCauseCorrect bool    `json:"finalCauseCorrect"`
	Suggestion        string  `json:"suggestion,omitempty"`
	SuggestionCorrect bool    `json:"suggestionCorrect"`
	BaselineMs        float64 `json:"baselineMs,omitempty"`
	WorstMs           float64 `json:"worstMs,omitempty"`
	Resolved          bool    `json:"resolved"`
	ResolveSeconds    float64 `json:"resolveSeconds,omitempty"`
	Verification      string  `json:"verification,omitempty"`

	CollateralFound []string      `json:"collateralFound,omitempty"` // expected side effects that were also flagged
	FalseAlarms     []string      `json:"falseAlarms"`               // insights on queries that were fine
	Insights        int           `json:"insights"`
	Workload        WorkloadStats `json:"workload"`
	Error           string        `json:"error,omitempty"`
}

func label(rec Recording, in store.Insight) string {
	if n, ok := rec.Names[in.QueryID]; ok {
		return n
	}
	return fmt.Sprintf("query %d", in.QueryID)
}

// Score compares a recording with what its scenario expects.
func Score(set string, rec Recording) Outcome {
	sc := rec.Scenario
	o := Outcome{Set: set, Scenario: sc.Name, About: sc.About, App: sc.App, Control: sc.Control(), Target: sc.Target,
		WantKind: sc.Kind, WantCause: sc.Cause, WantInSuggestion: sc.Suggestion, Insights: len(rec.Insights),
		Workload: rec.Workload, FalseAlarms: []string{}}
	token := strings.ReplaceAll(sc.Suggestion, "$HOLDER_PID", fmt.Sprint(rec.HolderPID))
	allowed := map[string]bool{}
	for _, n := range sc.AlsoAffected {
		allowed[n] = true
	}
	ins := append([]store.Insight{}, rec.Insights...)
	sort.Slice(ins, func(i, j int) bool { return ins[i].OpenedAt.Before(ins[j].OpenedAt) })
	t0, t1 := rec.Timeline.FaultAt, rec.Timeline.FixAt
	var hit *store.Insight
	for i := range ins {
		in := &ins[i]
		name := label(rec, *in)
		afterFault := !in.OpenedAt.Before(t0)
		switch {
		case sc.Control() || !afterFault:
			o.FalseAlarms = append(o.FalseAlarms, name)
		case name == sc.Target && hit == nil && in.Kind == sc.Kind && in.OpenedAt.Before(t1):
			hit = in
		case name == sc.Target:
			// a second insight on the target, or one that opened after the fix: neither a hit nor a false alarm
		case allowed[name]:
			o.CollateralFound = append(o.CollateralFound, name)
		default:
			o.FalseAlarms = append(o.FalseAlarms, name)
		}
	}
	if hit == nil {
		return o
	}
	o.Detected = true
	o.DetectSeconds = hit.OpenedAt.Sub(t0).Seconds()
	o.CauseAtOpen = hit.CauseAtOpen
	o.CauseCorrect = hit.CauseAtOpen == sc.Cause
	o.FinalCause = hit.Cause.Code
	o.FinalCauseCorrect = hit.Cause.Code == sc.Cause
	o.Suggestion = strings.TrimSpace(hit.Suggestion.Text + " " + hit.Suggestion.SQL)
	o.SuggestionCorrect = strings.Contains(hit.Suggestion.SQL+" "+hit.Suggestion.Text, token)
	o.BaselineMs = hit.BaselineMs
	o.WorstMs = hit.Worst()
	if hit.Status == "RESOLVED" && hit.ResolvedAt != nil && !hit.ResolvedAt.Before(t1) {
		o.Resolved = true
		o.ResolveSeconds = hit.ResolvedAt.Sub(t1).Seconds()
		o.Verification = hit.Verification
	}
	return o
}

// Summary aggregates a set of outcomes.
type Summary struct {
	Scenarios         int     `json:"scenarios"`
	Faults            int     `json:"faults"`
	Controls          int     `json:"controls"`
	Detected          int     `json:"detected"`
	CauseCorrect      int     `json:"causeCorrect"`
	FinalCauseCorrect int     `json:"finalCauseCorrect"`
	SuggestionCorrect int     `json:"suggestionCorrect"`
	Resolved          int     `json:"resolved"`
	FalseAlarms       int     `json:"falseAlarms"`
	MedianDetectS     float64 `json:"medianDetectSeconds"`
	MaxDetectS        float64 `json:"maxDetectSeconds"`
	MedianResolveS    float64 `json:"medianResolveSeconds"`
	Errors            int     `json:"errors"`
}

// Summarise counts the outcomes.
func Summarise(out []Outcome) Summary {
	var s Summary
	var det, res []float64
	for _, o := range out {
		s.Scenarios++
		s.FalseAlarms += len(o.FalseAlarms)
		if o.Error != "" {
			s.Errors++
		}
		if o.Control {
			s.Controls++
			continue
		}
		s.Faults++
		if o.Detected {
			s.Detected++
			det = append(det, o.DetectSeconds)
		}
		if o.CauseCorrect {
			s.CauseCorrect++
		}
		if o.FinalCauseCorrect {
			s.FinalCauseCorrect++
		}
		if o.SuggestionCorrect {
			s.SuggestionCorrect++
		}
		if o.Resolved {
			s.Resolved++
			res = append(res, o.ResolveSeconds)
		}
	}
	s.MedianDetectS = median(det)
	s.MedianResolveS = median(res)
	for _, d := range det {
		if d > s.MaxDetectS {
			s.MaxDetectS = d
		}
	}
	return s
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	v = append([]float64{}, v...)
	sort.Float64s(v)
	if len(v)%2 == 1 {
		return v[len(v)/2]
	}
	return (v[len(v)/2-1] + v[len(v)/2]) / 2
}
