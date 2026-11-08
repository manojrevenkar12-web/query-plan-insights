// Package store keeps what the collector has seen: per-query time series, plan versions, schema and settings changes,
// and insights. It is in memory and bounded by the retention window; everything it holds can be exported as JSON.
package store

import (
	"sort"
	"sync"
	"time"

	"github.com/manojrevenkar12-web/query-plan-insights/internal/plan"
)

// Point is one collection interval for one query: counter deltas plus wait samples.
type Point struct {
	At              time.Time `json:"at"`
	Calls           int64     `json:"calls"`
	ExecMs          float64   `json:"execMs"`
	Rows            int64     `json:"rows"`
	BlksRead        int64     `json:"blksRead"`
	BlksHit         int64     `json:"blksHit"`
	TempBlksWritten int64     `json:"tempBlksWritten"`
	Samples         int       `json:"samples"`
	LockSamples     int       `json:"lockSamples"`
	IOSamples       int       `json:"ioSamples"`
	CPUSamples      int       `json:"cpuSamples"`
}

// MeanMs is the mean execution time in the interval.
func (p Point) MeanMs() float64 {
	if p.Calls == 0 {
		return 0
	}
	return p.ExecMs / float64(p.Calls)
}

// PlanVersion is a plan seen at a time. A new version is stored only when the shape changes.
type PlanVersion struct {
	At          time.Time  `json:"at"`
	Fingerprint string     `json:"fingerprint"`
	Root        *plan.Node `json:"root"`
}

// Query is everything known about one normalised statement.
type Query struct {
	ID           int64         `json:"id"`
	Text         string        `json:"text"`
	FirstSeen    time.Time     `json:"firstSeen"`
	Points       []Point       `json:"points"`
	Plans        []PlanVersion `json:"plans"`
	LastExplain  time.Time     `json:"-"`
	ExplainError string        `json:"explainError,omitempty"`
}

// Event is a change outside the query: an index created or dropped, a setting changed.
type Event struct {
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"` // INDEX_CREATED, INDEX_DROPPED, SETTING_CHANGED
	Relation string    `json:"relation,omitempty"`
	Name     string    `json:"name"`
	Before   string    `json:"before,omitempty"`
	After    string    `json:"after,omitempty"`
}

// Blocker is a session that held up a query.
type Blocker struct {
	PID         int     `json:"pid"`
	Query       string  `json:"query"`
	XactSeconds float64 `json:"xactSeconds"`
	Seen        int     `json:"seen"`
}

// Store is safe for concurrent use.
type Store struct {
	mu        sync.RWMutex
	retention time.Duration
	started   time.Time
	queries   map[int64]*Query
	events    []Event
	insights  []*Insight
	tables    map[string]TableInfo
	blockers  map[int64]map[int]*Blocker
	dbMs      []Point // whole-database totals per interval
}

// TableInfo is the latest planner-statistics state of a table.
type TableInfo struct {
	LiveTuples      int64 `json:"liveTuples"`
	ModSinceAnalyze int64 `json:"modSinceAnalyze"`
}

func New(retention time.Duration, started time.Time) *Store {
	return &Store{retention: retention, started: started, queries: map[int64]*Query{}, tables: map[string]TableInfo{},
		blockers: map[int64]map[int]*Blocker{}}
}

func (s *Store) Started() time.Time { return s.started }

// AddPoint appends an interval for a query, creating the query on first sight.
func (s *Store) AddPoint(id int64, text string, p Point) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queries[id]
	if q == nil {
		q = &Query{ID: id, Text: text, FirstSeen: p.At}
		s.queries[id] = q
	}
	q.Points = append(q.Points, p)
	cut := p.At.Add(-s.retention)
	i := 0
	for i < len(q.Points) && q.Points[i].At.Before(cut) {
		i++
	}
	q.Points = q.Points[i:]
}

// AddDBPoint records whole-database totals for an interval.
func (s *Store) AddDBPoint(p Point) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dbMs = append(s.dbMs, p)
}

// DBPoints returns whole-database totals since a time.
func (s *Store) DBPoints(since time.Time) []Point {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Point
	for _, p := range s.dbMs {
		if !p.At.Before(since) {
			out = append(out, p)
		}
	}
	return out
}

// AddPlan stores a plan if its shape differs from the latest one. It returns true when the shape changed.
func (s *Store) AddPlan(id int64, at time.Time, root *plan.Node) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queries[id]
	if q == nil {
		return false
	}
	q.LastExplain = at
	q.ExplainError = ""
	fp := root.Fingerprint()
	if n := len(q.Plans); n > 0 && q.Plans[n-1].Fingerprint == fp {
		return false
	}
	q.Plans = append(q.Plans, PlanVersion{At: at, Fingerprint: fp, Root: root})
	return len(q.Plans) > 1
}

func (s *Store) ExplainFailed(id int64, at time.Time, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if q := s.queries[id]; q != nil {
		q.LastExplain = at
		q.ExplainError = err.Error()
	}
}

func (s *Store) AddEvent(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

// Events returns the changes since a time.
func (s *Store) Events(since time.Time) []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Event
	for _, e := range s.events {
		if !e.At.Before(since) {
			out = append(out, e)
		}
	}
	return out
}

func (s *Store) SetTable(rel string, t TableInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tables[rel] = t
}

func (s *Store) Table(rel string) (TableInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tables[rel]
	return t, ok
}

// NoteBlocker records that a session blocked a query in this interval.
func (s *Store) NoteBlocker(id int64, b Blocker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.blockers[id]
	if m == nil {
		m = map[int]*Blocker{}
		s.blockers[id] = m
	}
	if old := m[b.PID]; old != nil {
		old.Seen++
		old.XactSeconds = b.XactSeconds
		return
	}
	b.Seen = 1
	m[b.PID] = &b
}

// TopBlocker is the session that blocked a query most often.
func (s *Store) TopBlocker(id int64) (Blocker, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var best *Blocker
	for _, b := range s.blockers[id] {
		if best == nil || b.Seen > best.Seen || (b.Seen == best.Seen && b.PID < best.PID) {
			best = b
		}
	}
	if best == nil {
		return Blocker{}, false
	}
	return *best, true
}

// Query returns a copy of one query's state.
func (s *Store) Query(id int64) (Query, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	q := s.queries[id]
	if q == nil {
		return Query{}, false
	}
	c := *q
	c.Points = append([]Point(nil), q.Points...)
	c.Plans = append([]PlanVersion(nil), q.Plans...)
	return c, true
}

// QueryIDs returns all query ids, sorted.
func (s *Store) QueryIDs() []int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]int64, 0, len(s.queries))
	for id := range s.queries {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// NeedsExplain reports whether a query's plan is older than the given age.
func (s *Store) NeedsExplain(id int64, now time.Time, every time.Duration) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	q := s.queries[id]
	return q != nil && now.Sub(q.LastExplain) >= every
}
