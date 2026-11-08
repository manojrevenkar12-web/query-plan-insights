// Package collector polls PostgreSQL, feeds the store, and opens, diagnoses and resolves insights.
package collector

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/manojrevenkar12-web/query-plan-insights/internal/config"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/detect"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/diagnose"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/pgstat"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/plan"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/store"
)

type waits struct{ samples, lock, io, cpu int }

// Collector is one database's poller.
type Collector struct {
	cfg          config.Config
	reader       pgstat.Reader
	st           *store.Store
	workloadRole string
	log          io.Writer
	now          func() time.Time

	prev         map[int64]pgstat.Statement
	prevIndexes  map[string]pgstat.Index
	prevSettings map[string]string
	suspect      map[int64]int
	suspectedAt  map[int64]time.Time
	newSeen      map[int64]bool

	mu  sync.Mutex
	acc map[int64]*waits

	ticks     atomic.Int64
	collectNs atomic.Int64
}

// Stats returns how many intervals ran and the time spent collecting.
func (c *Collector) Stats() (int, time.Duration) {
	return int(c.ticks.Load()), time.Duration(c.collectNs.Load())
}

// New creates a collector. workloadRole lets it see per-role setting overrides (ALTER ROLE ... SET).
func New(cfg config.Config, r pgstat.Reader, st *store.Store, workloadRole string, log io.Writer) *Collector {
	return &Collector{cfg: cfg, reader: r, st: st, workloadRole: workloadRole, log: log, now: time.Now,
		prev: map[int64]pgstat.Statement{}, suspect: map[int64]int{}, suspectedAt: map[int64]time.Time{},
		newSeen: map[int64]bool{}, acc: map[int64]*waits{}}
}

// Run polls until the context ends.
func (c *Collector) Run(ctx context.Context) error {
	interval := c.cfg.Collector.Interval()
	samples := c.cfg.Collector.ActivitySamplesPerInterval
	go func() {
		t := time.NewTicker(interval / time.Duration(samples))
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = c.SampleActivity(ctx)
			}
		}
	}()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := c.Tick(ctx); err != nil && ctx.Err() == nil {
				fmt.Fprintf(c.log, "level=error msg=%q\n", err.Error())
			}
		}
	}
}

// SampleActivity counts what active queries are waiting on, and who blocks them.
func (c *Collector) SampleActivity(ctx context.Context) error {
	acts, err := c.reader.Activity(ctx)
	if err != nil {
		return err
	}
	byPID := map[int]pgstat.Activity{}
	for _, a := range acts {
		byPID[a.PID] = a
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range acts {
		if a.State != "active" || a.QueryID == 0 {
			continue
		}
		w := c.acc[a.QueryID]
		if w == nil {
			w = &waits{}
			c.acc[a.QueryID] = w
		}
		w.samples++
		switch a.WaitEventType {
		case "Lock":
			w.lock++
		case "IO":
			w.io++
		case "":
			w.cpu++
		}
		for _, pid := range a.BlockedBy {
			if b, ok := byPID[pid]; ok {
				c.st.NoteBlocker(a.QueryID, store.Blocker{PID: pid, Query: b.Query, XactSeconds: b.XactSeconds})
			}
		}
	}
	return nil
}

// Tick runs one collection interval.
func (c *Collector) Tick(ctx context.Context) error {
	start := time.Now()
	now := c.now()
	defer func() {
		c.ticks.Add(1)
		c.collectNs.Add(int64(time.Since(start)))
	}()
	stmts, err := c.reader.Statements(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	acc := c.acc
	c.acc = map[int64]*waits{}
	c.mu.Unlock()
	var db store.Point
	db.At = now
	for _, s := range stmts {
		p, ok := c.prev[s.QueryID]
		c.prev[s.QueryID] = s
		if !ok {
			continue
		}
		d := store.Point{At: now, Calls: s.Calls - p.Calls, ExecMs: s.TotalExecMs - p.TotalExecMs, Rows: s.Rows - p.Rows,
			BlksRead: s.SharedBlksRead - p.SharedBlksRead, BlksHit: s.SharedBlksHit - p.SharedBlksHit,
			TempBlksWritten: s.TempBlksWritten - p.TempBlksWritten}
		if d.Calls < 0 { // counters were reset or the entry evicted
			d = store.Point{At: now, Calls: s.Calls, ExecMs: s.TotalExecMs, Rows: s.Rows, BlksRead: s.SharedBlksRead,
				BlksHit: s.SharedBlksHit, TempBlksWritten: s.TempBlksWritten}
		}
		if w := acc[s.QueryID]; w != nil {
			d.Samples, d.LockSamples, d.IOSamples, d.CPUSamples = w.samples, w.lock, w.io, w.cpu
		}
		if d.Calls < int64(c.cfg.Collector.MinCallsToTrack) && d.Samples == 0 {
			if _, known := c.st.Query(s.QueryID); !known {
				continue
			}
		}
		c.st.AddPoint(s.QueryID, s.Query, d)
		db.Calls += d.Calls
		db.ExecMs += d.ExecMs
	}
	c.st.AddDBPoint(db)
	if err := c.schema(ctx, now); err != nil {
		return err
	}
	if err := c.settings(ctx, now); err != nil {
		return err
	}
	if tables, err := c.reader.Tables(ctx); err == nil {
		for _, t := range tables {
			c.st.SetTable(t.Relation, store.TableInfo{LiveTuples: t.LiveTuples, ModSinceAnalyze: t.ModSinceAnalyze})
		}
	}
	c.explain(ctx, now)
	c.detect(now)
	return nil
}

func (c *Collector) schema(ctx context.Context, now time.Time) error {
	idx, err := c.reader.Indexes(ctx)
	if err != nil {
		return err
	}
	cur := map[string]pgstat.Index{}
	for _, i := range idx {
		cur[i.Schema+"."+i.Name] = i
	}
	if c.prevIndexes != nil {
		for k, i := range c.prevIndexes {
			if _, ok := cur[k]; !ok {
				c.emit(store.Event{At: now, Kind: "INDEX_DROPPED", Relation: i.Schema + "." + i.Table, Name: i.Name, Before: i.Def})
			}
		}
		for k, i := range cur {
			if _, ok := c.prevIndexes[k]; !ok {
				c.emit(store.Event{At: now, Kind: "INDEX_CREATED", Relation: i.Schema + "." + i.Table, Name: i.Name, After: i.Def})
			}
		}
	}
	c.prevIndexes = cur
	return nil
}

func (c *Collector) settings(ctx context.Context, now time.Time) error {
	cur, err := c.reader.Settings(ctx, c.workloadRole)
	if err != nil {
		return err
	}
	if c.prevSettings != nil {
		keys := make([]string, 0, len(cur))
		for k := range cur {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if c.prevSettings[k] != cur[k] {
				c.emit(store.Event{At: now, Kind: "SETTING_CHANGED", Name: k, Before: c.prevSettings[k], After: cur[k]})
			}
		}
	}
	c.prevSettings = cur
	return nil
}

func (c *Collector) emit(e store.Event) {
	c.st.AddEvent(e)
	fmt.Fprintf(c.log, "level=info msg=%q kind=%s relation=%q name=%q before=%q after=%q\n", "database change", e.Kind,
		e.Relation, e.Name, e.Before, e.After)
}

// explain refreshes the plans of the heaviest queries, and immediately for any query that just started to look slower.
func (c *Collector) explain(ctx context.Context, now time.Time) {
	type load struct {
		id int64
		ms float64
	}
	window := now.Add(-time.Duration(c.cfg.Detector.RecentSeconds+c.cfg.Detector.BaselineSeconds) * time.Second)
	var loads []load
	for _, id := range c.st.QueryIDs() {
		q, _ := c.st.Query(id)
		if !pgstat.Explainable(q.Text) {
			continue
		}
		w := detect.Sum(q.Points, window, now.Add(time.Nanosecond))
		loads = append(loads, load{id, w.Ms})
	}
	sort.Slice(loads, func(i, j int) bool {
		return loads[i].ms > loads[j].ms || (loads[i].ms == loads[j].ms && loads[i].id < loads[j].id)
	})
	every := time.Duration(c.cfg.Collector.ExplainEverySeconds) * time.Second
	for i, l := range loads {
		urgent := c.suspect[l.id] == 1
		if !urgent && (i >= c.cfg.Collector.ExplainTopN || !c.st.NeedsExplain(l.id, now, every)) {
			continue
		}
		q, _ := c.st.Query(l.id)
		raw, err := c.reader.Explain(ctx, q.Text, time.Duration(c.cfg.Collector.ExplainTimeoutMillis)*time.Millisecond)
		if err != nil {
			c.st.ExplainFailed(l.id, now, err)
			continue
		}
		root, err := plan.Parse(raw)
		if err != nil {
			c.st.ExplainFailed(l.id, now, err)
			continue
		}
		if c.st.AddPlan(l.id, now, root) {
			fmt.Fprintf(c.log, "level=info msg=%q query_id=%d fingerprint=%s\n", "plan changed", l.id, root.Fingerprint())
		}
	}
}

func (c *Collector) detect(now time.Time) {
	d := c.cfg.Detector
	warm := now.Sub(c.st.Started()) >= time.Duration(d.WarmupSeconds)*time.Second
	dbRecent := detect.Sum(c.st.DBPoints(now.Add(-time.Duration(d.RecentSeconds)*time.Second)), time.Time{}, now.Add(time.Nanosecond))
	for _, id := range c.st.QueryIDs() {
		q, _ := c.st.Query(id)
		base, recent := detect.Windows(d, q.Points, now)
		if open := c.st.OpenFor(id); open != nil {
			c.follow(open, q, base, recent, now)
			continue
		}
		if detect.Slower(d, base, recent) {
			c.suspect[id]++
			if c.suspect[id] == 1 {
				c.suspectedAt[id] = now
			}
			if c.suspect[id] >= d.PersistTicks && warm {
				c.open(q, base, recent, now)
				c.suspect[id] = 0
			}
			continue
		}
		c.suspect[id] = 0
		if !warm || c.newSeen[id] || q.FirstSeen.Before(c.st.Started().Add(time.Duration(d.WarmupSeconds)*time.Second)) {
			continue
		}
		if base.Calls < d.MinCalls && recent.CallsPerSec() >= d.NewQueryMinCallsPerSecond && dbRecent.Calls > 0 &&
			float64(recent.Calls)/float64(dbRecent.Calls) >= d.NewQueryShareOfCalls {
			c.newSeen[id] = true
			res := diagnose.NewQuery(recent, float64(recent.Calls)/float64(dbRecent.Calls))
			in := c.st.OpenInsight(&store.Insight{Kind: "NEW_HEAVY_QUERY", QueryID: id, Query: q.Text, OpenedAt: now,
				SuspectedAt: q.FirstSeen, CurrentMs: recent.Mean(), CallsPerSec: recent.CallsPerSec(),
				ImpactMsPerS: recent.Ms / recent.Seconds, Cause: res.Cause, CauseAtOpen: res.Cause.Code, Evidence: res.Evidence,
				Suggestion: res.Suggestion})
			c.logInsight("new heavy query", in)
		}
	}
}

func (c *Collector) open(q store.Query, base, recent detect.Window, now time.Time) {
	res := c.diagnose(q, base, recent, c.suspectedAt[q.ID])
	in := c.st.OpenInsight(&store.Insight{Kind: "LATENCY_REGRESSION", QueryID: q.ID, Query: q.Text, OpenedAt: now,
		SuspectedAt: c.suspectedAt[q.ID], BaselineMs: base.Mean(), CurrentMs: recent.Mean(), CallsPerSec: recent.CallsPerSec(),
		ImpactMsPerS: (recent.Mean() - base.Mean()) * recent.CallsPerSec(), Cause: res.Cause, CauseAtOpen: res.Cause.Code,
		Evidence: res.Evidence, PlanChanges: res.Changes, PlanBefore: res.PlanBefore, PlanAfter: res.PlanAfter,
		Suggestion: res.Suggestion})
	c.logInsight("query regression", in)
}

func (c *Collector) diagnose(q store.Query, base, recent detect.Window, suspected time.Time) diagnose.Result {
	lookback := time.Duration(c.cfg.Diagnosis.EventLookbackSeconds) * time.Second
	var blocker *store.Blocker
	if b, ok := c.st.TopBlocker(q.ID); ok {
		blocker = &b
	}
	return diagnose.Regression(c.cfg.Diagnosis, diagnose.Input{Query: q, SuspectedAt: suspected, Base: base, Recent: recent,
		Events: c.st.Events(suspected.Add(-lookback)), Tables: c.st.Table, Blocker: blocker})
}

// follow keeps an open insight current: impact, a diagnosis that improves as evidence arrives, and resolution.
func (c *Collector) follow(in *store.Insight, q store.Query, _ detect.Window, recent detect.Window, now time.Time) {
	d := c.cfg.Detector
	healthy := detect.Recovered(d, in.BaselineMs, recent)
	if in.Kind == "NEW_HEAVY_QUERY" {
		healthy = recent.CallsPerSec() < d.NewQueryMinCallsPerSecond/2
	}
	var res diagnose.Result
	if !healthy && recent.Calls >= d.MinCalls && in.Kind == "LATENCY_REGRESSION" {
		base := detect.Sum(q.Points, in.SuspectedAt.Add(-time.Duration(d.BaselineSeconds)*time.Second), in.SuspectedAt)
		res = c.diagnose(q, base, recent, in.SuspectedAt)
	}
	resolved := false
	c.st.Update(in, func(x *store.Insight) {
		if recent.Calls > 0 {
			x.CurrentMs = recent.Mean()
			x.CallsPerSec = recent.CallsPerSec()
			if x.Kind == "LATENCY_REGRESSION" {
				x.ImpactMsPerS = (recent.Mean() - x.BaselineMs) * recent.CallsPerSec()
			}
		}
		if res.Cause.Code != "" {
			x.Cause, x.Evidence, x.Suggestion = res.Cause, res.Evidence, res.Suggestion
			if len(res.Changes) > 0 {
				x.PlanChanges, x.PlanBefore, x.PlanAfter = res.Changes, res.PlanBefore, res.PlanAfter
			}
		}
		if x.ResolveTick(healthy) >= d.PersistTicks {
			t := now
			x.Status = "RESOLVED"
			x.ResolvedAt = &t
			x.ResolvedMs = recent.Mean()
			x.ImpactMsPerS = 0
			x.Verification = c.verification(x, q, now)
			resolved = true
		}
	})
	if resolved {
		c.logInsight("query recovered", in)
	}
}

// verification says what the recovery coincided with, so a fix is confirmed by data rather than assumed.
func (c *Collector) verification(in *store.Insight, q store.Query, now time.Time) string {
	parts := []string{fmt.Sprintf("back to %.2f ms from a worst of %.2f ms (baseline %.2f ms)", in.ResolvedMs, in.Worst(), in.BaselineMs)}
	for _, e := range c.st.Events(in.OpenedAt) {
		switch e.Kind {
		case "INDEX_CREATED":
			parts = append(parts, "after index "+e.Name+" was created")
		case "SETTING_CHANGED":
			parts = append(parts, fmt.Sprintf("after %s changed to %s", e.Name, e.After))
		}
	}
	if n := len(q.Plans); n >= 2 && q.Plans[n-1].At.After(in.OpenedAt) {
		parts = append(parts, "the plan changed back to "+q.Plans[n-1].Fingerprint)
	}
	return strings.Join(parts, "; ")
}

func (c *Collector) logInsight(msg string, in *store.Insight) {
	fmt.Fprintf(c.log, "level=warn msg=%q insight=%d kind=%s query_id=%d cause=%s baseline_ms=%.3f current_ms=%.3f impact_ms_per_s=%.1f\n",
		msg, in.ID, in.Kind, in.QueryID, in.Cause.Code, in.BaselineMs, in.CurrentMs, in.ImpactMsPerS)
}
