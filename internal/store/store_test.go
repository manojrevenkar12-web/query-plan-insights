package store

import (
	"errors"
	"testing"
	"time"

	"github.com/manojrevenkar12-web/query-plan-insights/internal/plan"
)

var t0 = time.Unix(1_000_000, 0)

func at(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }

func TestPointsAreKeptForTheRetention(t *testing.T) {
	s := New(10*time.Second, t0)
	for i := 0; i < 30; i++ {
		s.AddPoint(1, "SELECT 1", Point{At: at(i), Calls: 1, ExecMs: 2})
	}
	q, ok := s.Query(1)
	if !ok || len(q.Points) != 11 || !q.Points[0].At.Equal(at(19)) {
		t.Fatalf("kept %d points from %v", len(q.Points), q.Points[0].At)
	}
	if q.FirstSeen != at(0) {
		t.Fatalf("first seen %v", q.FirstSeen)
	}
}

func TestQueriesThatStopRunningAreForgotten(t *testing.T) {
	s := New(10*time.Second, t0)
	s.AddPoint(1, "SELECT 1", Point{At: at(0), Calls: 1})
	s.AddPoint(2, "SELECT 2", Point{At: at(0), Calls: 1})
	s.OpenInsight(&Insight{QueryID: 2})
	s.AddEvent(Event{At: at(0), Kind: "INDEX_DROPPED"})
	s.AddDBPoint(Point{At: at(20)})
	if _, ok := s.Query(1); ok {
		t.Fatal("query 1 has not run for longer than the retention")
	}
	if _, ok := s.Query(2); !ok {
		t.Fatal("query 2 has an open insight and must be kept")
	}
	if len(s.Events(time.Time{})) != 0 {
		t.Fatal("old events must be dropped")
	}
}

func TestCopiesDoNotAlias(t *testing.T) {
	s := New(time.Minute, t0)
	s.AddPoint(1, "SELECT 1", Point{At: at(0), Calls: 1})
	q, _ := s.Query(1)
	q.Points[0].Calls = 99
	again, _ := s.Query(1)
	if again.Points[0].Calls != 1 {
		t.Fatal("Query must return a copy")
	}
}

func TestPlansAreStoredOnlyWhenTheShapeChanges(t *testing.T) {
	s := New(time.Minute, t0)
	s.AddPoint(1, "SELECT 1", Point{At: at(0), Calls: 1})
	idx := &plan.Node{Type: "Index Scan", Relation: "orders", Index: "orders_customer_idx", Cost: 8}
	idx2 := &plan.Node{Type: "Index Scan", Relation: "orders", Index: "orders_customer_idx", Cost: 9000}
	seq := &plan.Node{Type: "Seq Scan", Relation: "orders"}
	if s.AddPlan(1, at(1), idx) {
		t.Fatal("the first plan is not a change")
	}
	if s.AddPlan(1, at(2), idx2) {
		t.Fatal("a different cost is not a different plan")
	}
	if !s.AddPlan(1, at(3), seq) {
		t.Fatal("a sequential scan replacing an index scan is a change")
	}
	q, _ := s.Query(1)
	if len(q.Plans) != 2 || q.Plans[1].Fingerprint != seq.Fingerprint() {
		t.Fatalf("plans %+v", q.Plans)
	}
	if s.AddPlan(42, at(3), seq) {
		t.Fatal("plans of unknown queries are ignored")
	}
}

func TestExplainScheduling(t *testing.T) {
	s := New(time.Minute, t0)
	s.AddPoint(1, "SELECT 1", Point{At: at(0), Calls: 1})
	if !s.NeedsExplain(1, at(0), 10*time.Second) {
		t.Fatal("a query never explained needs a plan")
	}
	s.ExplainFailed(1, at(1), errors.New("permission denied"))
	if s.NeedsExplain(1, at(5), 10*time.Second) {
		t.Fatal("a failed explain waits like a successful one")
	}
	if q, _ := s.Query(1); q.ExplainError != "permission denied" {
		t.Fatalf("error %q", q.ExplainError)
	}
	if !s.NeedsExplain(1, at(11), 10*time.Second) {
		t.Fatal("explain again after the interval")
	}
}

func TestTopBlockerIsTheMostFrequent(t *testing.T) {
	s := New(time.Minute, t0)
	s.NoteBlocker(1, Blocker{PID: 10, Query: "a"})
	s.NoteBlocker(1, Blocker{PID: 20, Query: "b"})
	s.NoteBlocker(1, Blocker{PID: 20, Query: "b", XactSeconds: 3})
	b, ok := s.TopBlocker(1)
	if !ok || b.PID != 20 || b.Seen != 2 || b.XactSeconds != 3 {
		t.Fatalf("top blocker %+v", b)
	}
	if _, ok := s.TopBlocker(2); ok {
		t.Fatal("no blocker recorded for query 2")
	}
}

func TestInsightLifecycle(t *testing.T) {
	s := New(time.Minute, t0)
	in := s.OpenInsight(&Insight{QueryID: 7, CurrentMs: 5})
	if in.ID != 1 || in.Status != "OPEN" || s.OpenFor(7) != in {
		t.Fatalf("opened %+v", in)
	}
	s.Update(in, func(x *Insight) { x.CurrentMs = 12 })
	s.Update(in, func(x *Insight) { x.CurrentMs = 3 })
	if in.Worst() != 12 {
		t.Fatalf("worst %v", in.Worst())
	}
	if in.ResolveTick(true) != 1 || in.ResolveTick(false) != 0 || in.ResolveTick(true) != 1 {
		t.Fatal("healthy ticks must be consecutive")
	}
	s.Update(in, func(x *Insight) { x.Status = "RESOLVED" })
	if s.OpenFor(7) != nil {
		t.Fatal("resolved insights are not open")
	}
	s.OpenInsight(&Insight{QueryID: 8})
	list := s.Insights()
	if len(list) != 2 || list[0].QueryID != 8 {
		t.Fatalf("insights must be newest first: %+v", list)
	}
}
