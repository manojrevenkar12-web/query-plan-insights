// Package diagnose ranks the likely causes of a regression from evidence the collector already has: plan versions,
// schema and settings changes, wait samples, temp and cache counters, and planner-statistics freshness.
package diagnose

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/manojrevenkar12-web/query-plan-insights/internal/config"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/detect"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/plan"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/store"
)

// Input is what the diagnosis may look at.
type Input struct {
	Query       store.Query
	SuspectedAt time.Time
	Base        detect.Window
	Recent      detect.Window
	Events      []store.Event
	Tables      func(rel string) (store.TableInfo, bool)
	Blocker     *store.Blocker
}

// Result is the ranked diagnosis.
type Result struct {
	Cause      store.Cause
	Ranked     []store.Cause
	Evidence   []string
	Changes    []plan.Change
	PlanBefore string
	PlanAfter  string
	Suggestion store.Suggestion
}

type candidate struct {
	cause      store.Cause
	suggestion store.Suggestion
}

// Regression explains why a query got slower.
func Regression(cfg config.Diagnosis, in Input) Result {
	var r Result
	before, after := plansAround(in.Query, in.SuspectedAt, time.Duration(cfg.EventLookbackSeconds)*time.Second)
	var rels []string
	if after != nil {
		rels = after.Relations()
		r.PlanAfter = Render(after)
	}
	if before != nil {
		r.PlanBefore = Render(before)
		r.Changes = plan.Diff(before, after)
		rels = union(rels, before.Relations())
	}
	planChanged := before != nil && after != nil && before.Fingerprint() != after.Fingerprint()
	r.Evidence = append(r.Evidence, fmt.Sprintf("mean time %.2f ms -> %.2f ms (%.1fx) over %d calls", in.Base.Mean(),
		in.Recent.Mean(), ratio(in.Recent.Mean(), in.Base.Mean()), in.Recent.Calls))
	for _, c := range r.Changes {
		r.Evidence = append(r.Evidence, fmt.Sprintf("plan %s %s: %s -> %s", c.Kind, c.Relation, c.Before, c.After))
	}
	var cands []candidate

	usedBefore := map[string]bool{}
	if before != nil {
		for _, ix := range before.Indexes() {
			usedBefore[ix] = true
		}
	}
	for _, e := range in.Events {
		switch e.Kind {
		case "INDEX_DROPPED":
			if contains(rels, e.Relation) {
				r.Evidence = append(r.Evidence, fmt.Sprintf("index %s on %s dropped at %s", e.Name, e.Relation, e.At.Format(time.TimeOnly)))
				conf := 0.7
				if usedBefore[e.Name] {
					conf = 0.95
				}
				cands = append(cands, candidate{store.Cause{Code: "MISSING_INDEX", Confidence: conf,
					Summary: fmt.Sprintf("index %s, which the plan used, was dropped", e.Name)},
					store.Suggestion{Text: "Recreate the index without blocking writes.", SQL: concurrently(e.Before)}})
			}
		case "SETTING_CHANGED":
			r.Evidence = append(r.Evidence, fmt.Sprintf("setting %s changed %s -> %s at %s", e.Name, e.Before, e.After, e.At.Format(time.TimeOnly)))
			if e.Name == "work_mem" && in.Recent.PerCall(in.Recent.Temp) >= cfg.TempBlocksPerCallMin {
				cands = append(cands, candidate{store.Cause{Code: "SPILL_TO_DISK", Confidence: 0.95,
					Summary: fmt.Sprintf("work_mem dropped from %s to %s, so sorts and hashes spill to disk", e.Before, e.After)},
					store.Suggestion{Text: "Restore work_mem where it was changed.", SQL: fmt.Sprintf("-- where it was changed: SET work_mem = '%s'", humanKB(e.Before))}})
			} else if e.Name != "work_mem" && planChanged {
				cands = append(cands, candidate{store.Cause{Code: "SETTING_CHANGE", Confidence: 0.9,
					Summary: fmt.Sprintf("%s changed from %s to %s and the planner chose a different plan", e.Name, e.Before, e.After)},
					store.Suggestion{Text: fmt.Sprintf("Revert %s, or set it only for the sessions that need it.", e.Name),
						SQL: fmt.Sprintf("ALTER SYSTEM RESET %s; SELECT pg_reload_conf();", e.Name)}})
			}
		}
	}

	tempNow, tempBase := in.Recent.PerCall(in.Recent.Temp), in.Base.PerCall(in.Base.Temp)
	if tempNow >= cfg.TempBlocksPerCallMin && tempNow > 4*tempBase {
		r.Evidence = append(r.Evidence, fmt.Sprintf("temp blocks written per call %.1f -> %.1f", tempBase, tempNow))
		need := int(math.Ceil(tempNow*8/1024*2)) + 1
		cands = append(cands, candidate{store.Cause{Code: "SPILL_TO_DISK", Confidence: 0.8,
			Summary: fmt.Sprintf("the query now writes %.0f temp blocks per call: sorts or hashes no longer fit in work_mem", tempNow)},
			store.Suggestion{Text: "Give this workload more work_mem (about twice the spill) or reduce the rows it sorts.",
				SQL: fmt.Sprintf("SET work_mem = '%dMB'; -- for this role or session", need)}})
	}

	lockNow, lockBase := in.Recent.LockShare(), in.Base.LockShare()
	if lockNow >= cfg.LockShareMin && lockBase < cfg.LockShareMin/2 {
		r.Evidence = append(r.Evidence, fmt.Sprintf("%.0f%% of active samples wait on locks (baseline %.0f%%)", lockNow*100, lockBase*100))
		s := store.Suggestion{Text: "Find the transaction holding the lock and shorten it."}
		summary := fmt.Sprintf("%.0f%% of the time the query waits for a lock", lockNow*100)
		if in.Blocker != nil {
			r.Evidence = append(r.Evidence, fmt.Sprintf("blocked by pid %d (transaction open %.1f s): %s", in.Blocker.PID,
				in.Blocker.XactSeconds, oneLine(in.Blocker.Query)))
			summary += fmt.Sprintf(", mostly behind pid %d", in.Blocker.PID)
			s.SQL = fmt.Sprintf("SELECT pid, xact_start, state, query FROM pg_stat_activity WHERE pid = %d;", in.Blocker.PID)
		}
		cands = append(cands, candidate{store.Cause{Code: "LOCK_CONTENTION", Confidence: 0.9, Summary: summary}, s})
	}

	if planChanged {
		stale := ""
		for _, rel := range rels {
			if t, ok := in.Tables(rel); ok && t.LiveTuples > 0 &&
				float64(t.ModSinceAnalyze)/float64(t.LiveTuples) >= cfg.ModSinceAnalyzeShareMin {
				stale = rel
				r.Evidence = append(r.Evidence, fmt.Sprintf("%s: %d rows changed since the last ANALYZE (%d live)", rel,
					t.ModSinceAnalyze, t.LiveTuples))
			}
		}
		if stale != "" {
			cands = append(cands, candidate{store.Cause{Code: "STALE_STATISTICS", Confidence: 0.75,
				Summary: fmt.Sprintf("the plan changed while %s's statistics are stale", stale)},
				store.Suggestion{Text: "Refresh the statistics, then check the plan again.", SQL: "ANALYZE " + stale + ";"}})
		}
		cols := after.FilterColumns()
		for rel, cs := range cols {
			cands = append(cands, candidate{store.Cause{Code: "PLAN_CHANGE", Confidence: 0.5,
				Summary: fmt.Sprintf("the planner now scans %s sequentially", rel)},
				store.Suggestion{Text: "If no setting or schema change explains it, an index on the filtered columns lets the planner avoid the scan.",
					SQL: fmt.Sprintf("CREATE INDEX CONCURRENTLY ON %s (%s);", rel, strings.Join(cs, ", "))}})
		}
	}

	if rowsNow, rowsBase := in.Recent.PerCall(in.Recent.Rows), in.Base.PerCall(in.Base.Rows); rowsBase > 0 && rowsNow >= cfg.RowsRatioMin*rowsBase {
		r.Evidence = append(r.Evidence, fmt.Sprintf("rows per call %.1f -> %.1f", rowsBase, rowsNow))
		cands = append(cands, candidate{store.Cause{Code: "DATA_GROWTH", Confidence: 0.6,
			Summary: fmt.Sprintf("each call returns %.1fx more rows", rowsNow/rowsBase)},
			store.Suggestion{Text: "Check whether the caller needs every row; paginate or filter earlier."}})
	}
	readNow, readBase := in.Recent.PerCall(in.Recent.Read), in.Base.PerCall(in.Base.Read)
	if readNow > 0 && readNow >= cfg.ReadRatioMin*math.Max(readBase, 1) {
		r.Evidence = append(r.Evidence, fmt.Sprintf("blocks read from disk per call %.1f -> %.1f", readBase, readNow))
		cands = append(cands, candidate{store.Cause{Code: "CACHE_MISSES", Confidence: 0.45,
			Summary: "the query reads far more from disk than before"},
			store.Suggestion{Text: "Look for a plan that reads less, or for memory pressure on the server."}})
	}

	if len(cands) == 0 {
		cands = append(cands, candidate{store.Cause{Code: "UNKNOWN", Confidence: 0.2,
			Summary: "slower, with no plan, schema, settings, lock or spill change seen"},
			store.Suggestion{Text: "Compare query samples and server metrics for the window."}})
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].cause.Confidence > cands[j].cause.Confidence })
	r.Cause, r.Suggestion = cands[0].cause, cands[0].suggestion
	seen := map[string]bool{}
	for _, c := range cands {
		if !seen[c.cause.Code] {
			seen[c.cause.Code] = true
			r.Ranked = append(r.Ranked, c.cause)
		}
	}
	return r
}

// NewQuery explains a query that appeared after start-up and issues a large share of all calls.
func NewQuery(recent detect.Window, share float64) Result {
	var r Result
	rowsPerCall := recent.PerCall(recent.Rows)
	r.Evidence = []string{fmt.Sprintf("new query: %.0f calls/s, %.2f ms each, %.1f rows per call, %.0f%% of all calls",
		recent.CallsPerSec(), recent.Mean(), rowsPerCall, share*100)}
	if rowsPerCall <= 1.5 && recent.Mean() < 2 {
		r.Cause = store.Cause{Code: "N_PLUS_ONE", Confidence: 0.8,
			Summary: fmt.Sprintf("a new query runs %.0f times a second for about one row each: a loop issuing one query per item", recent.CallsPerSec())}
		r.Suggestion = store.Suggestion{Text: "Fetch the items in one query.", SQL: "-- e.g. WHERE id = ANY($1) instead of one call per id"}
	} else {
		r.Cause = store.Cause{Code: "HEAVY_NEW_QUERY", Confidence: 0.6, Summary: "a new query issues a large share of all calls"}
		r.Suggestion = store.Suggestion{Text: "Check the release that introduced it."}
	}
	r.Ranked = []store.Cause{r.Cause}
	return r
}

// plansAround returns the plan in force before the regression and the newest plan.
func plansAround(q store.Query, suspected time.Time, lookback time.Duration) (before, after *plan.Node) {
	if len(q.Plans) == 0 {
		return nil, nil
	}
	after = q.Plans[len(q.Plans)-1].Root
	for i := len(q.Plans) - 2; i >= 0; i-- {
		if q.Plans[i+1].At.After(suspected.Add(-lookback)) {
			return q.Plans[i].Root, after
		}
	}
	return nil, after
}

// Render prints a plan as an indented tree.
func Render(n *plan.Node) string {
	var b strings.Builder
	var walk func(*plan.Node, int)
	walk = func(x *plan.Node, d int) {
		b.WriteString(strings.Repeat("  ", d))
		b.WriteString(x.Type)
		if x.Relation != "" {
			b.WriteString(" on " + x.Relation)
		}
		if x.Index != "" {
			b.WriteString(" using " + x.Index)
		}
		fmt.Fprintf(&b, "  (rows=%.0f cost=%.1f)\n", x.Rows, x.Cost)
		for _, c := range x.Children {
			walk(c, d+1)
		}
	}
	walk(n, 0)
	return b.String()
}

func concurrently(def string) string {
	if def == "" {
		return ""
	}
	out := strings.Replace(def, "CREATE INDEX ", "CREATE INDEX CONCURRENTLY ", 1)
	out = strings.Replace(out, "CREATE UNIQUE INDEX ", "CREATE UNIQUE INDEX CONCURRENTLY ", 1)
	return out + ";"
}

func humanKB(v string) string {
	var kb int
	if _, err := fmt.Sscan(v, &kb); err != nil {
		return v
	}
	if kb%1024 == 0 {
		return fmt.Sprintf("%dMB", kb/1024)
	}
	return fmt.Sprintf("%dkB", kb)
}

func ratio(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}

func union(a, b []string) []string {
	m := map[string]bool{}
	for _, x := range append(append([]string{}, a...), b...) {
		m[x] = true
	}
	out := make([]string, 0, len(m))
	for x := range m {
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}
