// Package api turns the store into the JSON the web app reads, and serves it with Prometheus metrics.
package api

import (
	"sort"
	"time"

	"github.com/manojrevenkar12-web/query-plan-insights/internal/config"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/detect"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/diagnose"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/store"
)

// SeriesPoint is one interval of a query or of the whole database.
type SeriesPoint struct {
	At          time.Time `json:"at"`
	Calls       int64     `json:"calls"`
	MeanMs      float64   `json:"meanMs"`
	RowsPerCall float64   `json:"rowsPerCall"`
	ReadPerCall float64   `json:"readPerCall"`
	TempPerCall float64   `json:"tempPerCall"`
	LockShare   float64   `json:"lockShare"`
}

// QueryRow is one line of the overview table.
type QueryRow struct {
	ID          int64     `json:"id"`
	Text        string    `json:"text"`
	CallsPerSec float64   `json:"callsPerSec"`
	MeanMs      float64   `json:"meanMs"`
	LoadMsPerS  float64   `json:"loadMsPerSec"` // database time per second of wall clock
	Plan        string    `json:"plan,omitempty"`
	Spark       []float64 `json:"spark"`
	InsightID   int       `json:"insightId,omitempty"`
	Status      string    `json:"status"` // OK, REGRESSED, NEW
}

// Overview is the landing view: database load, open insights, heaviest queries.
type Overview struct {
	StartedAt     time.Time     `json:"startedAt"`
	Now           time.Time     `json:"now"`
	Database      []SeriesPoint `json:"database"`
	OpenInsights  int           `json:"openInsights"`
	Resolved      int           `json:"resolvedInsights"`
	ImpactMsPerS  float64       `json:"impactMsPerSec"`
	Queries       []QueryRow    `json:"queries"`
	TrackedTotal  int           `json:"trackedQueries"`
	Events        []store.Event `json:"events"`
	WindowSeconds int           `json:"windowSeconds"`
}

// InsightView adds what the JSON of the stored insight leaves out.
type InsightView struct {
	store.Insight
	WorstMs float64 `json:"worstMs"`
}

// PlanView is one stored plan version, rendered.
type PlanView struct {
	At          time.Time `json:"at"`
	Fingerprint string    `json:"fingerprint"`
	Tree        string    `json:"tree"`
}

// QueryDetail is the drill-down for one query.
type QueryDetail struct {
	ID           int64         `json:"id"`
	Text         string        `json:"text"`
	FirstSeen    time.Time     `json:"firstSeen"`
	Series       []SeriesPoint `json:"series"`
	Plans        []PlanView    `json:"plans"`
	ExplainError string        `json:"explainError,omitempty"`
	Insights     []InsightView `json:"insights"`
}

// Snapshot is everything the web app shows, in one document: what the evaluation saves for each scenario.
type Snapshot struct {
	Overview Overview      `json:"overview"`
	Insights []InsightView `json:"insights"`
	Queries  []QueryDetail `json:"queries"`
}

// View builds the JSON views from the store.
type View struct {
	Cfg   config.Config
	Store *store.Store
	Now   func() time.Time
	// OverviewRows is how many queries the overview lists.
	OverviewRows int
	// WindowSeconds is how much history the overview and sparklines cover.
	WindowSeconds int
}

func series(points []store.Point) []SeriesPoint {
	out := make([]SeriesPoint, 0, len(points))
	for _, p := range points {
		w := detect.Window{Calls: p.Calls, Ms: p.ExecMs, Rows: p.Rows, Read: p.BlksRead, Temp: p.TempBlksWritten,
			Samples: p.Samples, Lock: p.LockSamples}
		out = append(out, SeriesPoint{At: p.At, Calls: p.Calls, MeanMs: w.Mean(), RowsPerCall: w.PerCall(p.Rows),
			ReadPerCall: w.PerCall(p.BlksRead), TempPerCall: w.PerCall(p.TempBlksWritten), LockShare: w.LockShare()})
	}
	return out
}

func since(points []store.Point, from time.Time) []store.Point {
	i := sort.Search(len(points), func(i int) bool { return !points[i].At.Before(from) })
	return points[i:]
}

// Overview lists the heaviest queries over the window.
func (v View) Overview() Overview {
	now := v.Now()
	from := now.Add(-time.Duration(v.WindowSeconds) * time.Second)
	recentFrom := now.Add(-time.Duration(v.Cfg.Detector.RecentSeconds) * time.Second)
	o := Overview{StartedAt: v.Store.Started(), Now: now, Database: series(v.Store.DBPoints(from)),
		Events: v.Store.Events(from), WindowSeconds: v.WindowSeconds}
	open := map[int64]store.Insight{}
	for _, in := range v.Store.Insights() {
		if in.Status == "OPEN" {
			o.OpenInsights++
			o.ImpactMsPerS += in.ImpactMsPerS
			if _, seen := open[in.QueryID]; !seen {
				open[in.QueryID] = in
			}
		} else {
			o.Resolved++
		}
	}
	ids := v.Store.QueryIDs()
	o.TrackedTotal = len(ids)
	for _, id := range ids {
		q, _ := v.Store.Query(id)
		pts := since(q.Points, from)
		recent := detect.Sum(pts, recentFrom, now.Add(time.Nanosecond))
		all := detect.Sum(pts, from, now.Add(time.Nanosecond))
		row := QueryRow{ID: id, Text: q.Text, CallsPerSec: recent.CallsPerSec(), MeanMs: recent.Mean(),
			LoadMsPerS: all.Ms / all.Seconds, Status: "OK"}
		for _, p := range pts {
			row.Spark = append(row.Spark, p.MeanMs())
		}
		if n := len(q.Plans); n > 0 {
			row.Plan = q.Plans[n-1].Root.Shape()
		}
		if in, ok := open[id]; ok {
			row.InsightID = in.ID
			row.Status = "REGRESSED"
			if in.Kind == "NEW_HEAVY_QUERY" {
				row.Status = "NEW"
			}
		}
		o.Queries = append(o.Queries, row)
	}
	sort.SliceStable(o.Queries, func(i, j int) bool {
		a, b := o.Queries[i], o.Queries[j]
		if (a.InsightID != 0) != (b.InsightID != 0) {
			return a.InsightID != 0
		}
		return a.LoadMsPerS > b.LoadMsPerS || (a.LoadMsPerS == b.LoadMsPerS && a.ID < b.ID)
	})
	if len(o.Queries) > v.OverviewRows {
		o.Queries = o.Queries[:v.OverviewRows]
	}
	return o
}

// Insights lists every insight, newest first.
func (v View) Insights() []InsightView {
	var out []InsightView
	for _, in := range v.Store.Insights() {
		out = append(out, InsightView{Insight: in, WorstMs: in.Worst()})
	}
	return out
}

// Query is the drill-down for one query, or false if it is unknown.
func (v View) Query(id int64) (QueryDetail, bool) {
	q, ok := v.Store.Query(id)
	if !ok {
		return QueryDetail{}, false
	}
	d := QueryDetail{ID: id, Text: q.Text, FirstSeen: q.FirstSeen, Series: series(q.Points), ExplainError: q.ExplainError,
		Insights: []InsightView{}}
	for _, p := range q.Plans {
		d.Plans = append(d.Plans, PlanView{At: p.At, Fingerprint: p.Fingerprint, Tree: diagnose.Render(p.Root)})
	}
	for _, in := range v.Insights() {
		if in.QueryID == id {
			d.Insights = append(d.Insights, in)
		}
	}
	return d, true
}

// Snapshot captures the overview, every insight and the detail of every query on the overview or with an insight.
func (v View) Snapshot() Snapshot {
	s := Snapshot{Overview: v.Overview(), Insights: v.Insights()}
	want := map[int64]bool{}
	for _, r := range s.Overview.Queries {
		want[r.ID] = true
	}
	for _, in := range s.Insights {
		want[in.QueryID] = true
	}
	ids := make([]int64, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		if d, ok := v.Query(id); ok {
			s.Queries = append(s.Queries, d)
		}
	}
	return s
}
