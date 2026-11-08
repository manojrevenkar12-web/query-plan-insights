package store

import (
	"time"

	"github.com/manojrevenkar12-web/query-plan-insights/internal/plan"
)

// Insight is what a developer acts on: what got slower, by how much, why, and what to do.
type Insight struct {
	ID           int           `json:"id"`
	Kind         string        `json:"kind"` // LATENCY_REGRESSION, NEW_HEAVY_QUERY
	QueryID      int64         `json:"queryId"`
	Query        string        `json:"query"`
	Status       string        `json:"status"` // OPEN, RESOLVED
	OpenedAt     time.Time     `json:"openedAt"`
	SuspectedAt  time.Time     `json:"suspectedAt"`
	BaselineMs   float64       `json:"baselineMs"`
	CurrentMs    float64       `json:"currentMs"`
	CallsPerSec  float64       `json:"callsPerSec"`
	ImpactMsPerS float64       `json:"impactMsPerSec"` // extra database time per second of wall clock
	Cause        Cause         `json:"cause"`
	CauseAtOpen  string        `json:"causeAtOpen"`
	Evidence     []string      `json:"evidence"`
	PlanChanges  []plan.Change `json:"planChanges,omitempty"`
	PlanBefore   string        `json:"planBefore,omitempty"`
	PlanAfter    string        `json:"planAfter,omitempty"`
	Suggestion   Suggestion    `json:"suggestion"`
	ResolvedAt   *time.Time    `json:"resolvedAt,omitempty"`
	ResolvedMs   float64       `json:"resolvedMs,omitempty"`
	Verification string        `json:"verification,omitempty"`
	worstMs      float64
	resolveTicks int
}

// Cause is the ranked explanation.
type Cause struct {
	Code       string  `json:"code"`
	Confidence float64 `json:"confidence"`
	Summary    string  `json:"summary"`
}

// Suggestion is the fix to try, with SQL when there is one.
type Suggestion struct {
	Text string `json:"text"`
	SQL  string `json:"sql,omitempty"`
}

// OpenInsight registers a new insight and returns it.
func (s *Store) OpenInsight(in *Insight) *Insight {
	s.mu.Lock()
	defer s.mu.Unlock()
	in.ID = len(s.insights) + 1
	in.Status = "OPEN"
	in.worstMs = in.CurrentMs
	s.insights = append(s.insights, in)
	return in
}

// OpenFor returns the open insight of a query, if any.
func (s *Store) OpenFor(id int64) *Insight {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, in := range s.insights {
		if in.QueryID == id && in.Status == "OPEN" {
			return in
		}
	}
	return nil
}

// Update applies a change to an insight under the store's lock.
func (s *Store) Update(in *Insight, f func(*Insight)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(in)
	if in.CurrentMs > in.worstMs {
		in.worstMs = in.CurrentMs
	}
}

// Worst is the slowest mean seen while the insight was open.
func (in *Insight) Worst() float64 { return in.worstMs }

// ResolveTick counts consecutive healthy evaluations; it returns the new count.
func (in *Insight) ResolveTick(healthy bool) int {
	if healthy {
		in.resolveTicks++
	} else {
		in.resolveTicks = 0
	}
	return in.resolveTicks
}

// Insights returns copies, newest first.
func (s *Store) Insights() []Insight {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Insight, 0, len(s.insights))
	for i := len(s.insights) - 1; i >= 0; i-- {
		out = append(out, *s.insights[i])
	}
	return out
}
