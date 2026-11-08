package collector

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/manojrevenkar12-web/query-plan-insights/internal/config"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/pgstat"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/store"
)

// fake is a database whose statistics the test scripts tick by tick.
type fake struct {
	tick     int
	stmts    map[int64]*pgstat.Statement
	indexed  bool
	workMem  string
	acts     []pgstat.Activity
	explains int
}

func (f *fake) Statements(context.Context) ([]pgstat.Statement, error) {
	var out []pgstat.Statement
	for _, s := range f.stmts {
		out = append(out, *s)
	}
	return out, nil
}
func (f *fake) Activity(context.Context) ([]pgstat.Activity, error) { return f.acts, nil }
func (f *fake) Indexes(context.Context) ([]pgstat.Index, error) {
	if !f.indexed {
		return nil, nil
	}
	return []pgstat.Index{{Schema: "shop", Table: "orders", Name: "orders_customer_idx",
		Def: "CREATE INDEX orders_customer_idx ON shop.orders USING btree (customer_id)"}}, nil
}
func (f *fake) Settings(context.Context, string) (map[string]string, error) {
	return map[string]string{"work_mem": f.workMem, "random_page_cost": "4"}, nil
}
func (f *fake) Tables(context.Context) ([]pgstat.TableStats, error) { return nil, nil }
func (f *fake) Explain(context.Context, string, time.Duration) ([]byte, error) {
	f.explains++
	name := "seq_scan.json"
	if f.indexed {
		name = "index_scan.json"
	}
	return os.ReadFile("../plan/testdata/" + name)
}

// run advances the statement counters by calls at meanMs each.
func (f *fake) run(id int64, text string, calls int64, meanMs float64, temp int64) {
	s := f.stmts[id]
	if s == nil {
		s = &pgstat.Statement{QueryID: id, Query: text}
		f.stmts[id] = s
	}
	s.Calls += calls
	s.TotalExecMs += float64(calls) * meanMs
	s.Rows += calls
	s.TempBlksWritten += temp * calls
}

const orders = "SELECT id, total FROM shop.orders WHERE customer_id = $1"

func setup(t *testing.T) (*Collector, *fake, *store.Store, *time.Time) {
	t.Helper()
	cfg, err := config.Load("../../config/qpi.json")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Unix(5_000_000, 0)
	now := start
	f := &fake{stmts: map[int64]*pgstat.Statement{}, indexed: true, workMem: "65536"}
	st := store.New(time.Hour, start)
	c := New(cfg, f, st, "app", io.Discard)
	c.now = func() time.Time { return now }
	return c, f, st, &now
}

func step(t *testing.T, c *Collector, now *time.Time) {
	t.Helper()
	*now = now.Add(time.Second)
	if err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDroppedIndexIsFoundBlamedAndConfirmedFixed(t *testing.T) {
	c, f, st, now := setup(t)
	for i := 0; i < 70; i++ {
		switch i {
		case 30:
			f.indexed = false
		case 50:
			f.indexed = true
		}
		mean := 0.08
		if !f.indexed {
			mean = 20
		}
		f.run(1, orders, 25, mean, 0)
		f.run(2, "SELECT name FROM shop.products WHERE id = $1", 80, 0.02, 0)
		step(t, c, now)
		if i == 29 && len(st.Insights()) != 0 {
			t.Fatalf("insight before the fault: %+v", st.Insights())
		}
	}
	ins := st.Insights()
	if len(ins) != 1 {
		t.Fatalf("want one insight, got %+v", ins)
	}
	in := ins[0]
	if in.QueryID != 1 || in.CauseAtOpen != "MISSING_INDEX" || !strings.Contains(in.Suggestion.SQL, "CREATE INDEX CONCURRENTLY orders_customer_idx") {
		t.Fatalf("insight %+v", in)
	}
	opened := in.OpenedAt.Sub(st.Started()).Seconds()
	if opened < 32 || opened > 35 {
		t.Fatalf("opened %v s after start, the index went at 31", opened)
	}
	if in.Status != "RESOLVED" || !strings.Contains(in.Verification, "after index orders_customer_idx was created") {
		t.Fatalf("resolution %q %q", in.Status, in.Verification)
	}
	if ticks, spent := c.Stats(); ticks != 70 || spent <= 0 {
		t.Fatalf("stats %d %v", ticks, spent)
	}
}

func TestNewQueryFloodIsAnNPlusOne(t *testing.T) {
	c, f, st, now := setup(t)
	for i := 0; i < 40; i++ {
		f.run(2, "SELECT name FROM shop.products WHERE id = ANY($1)", 6, 0.3, 0)
		f.run(3, "SELECT * FROM shop.customers WHERE id = $1", 30, 0.05, 0)
		if i >= 25 && i < 34 {
			f.run(4, "SELECT name FROM shop.products WHERE id = $1 AND price > $2", 120, 0.03, 0)
		}
		step(t, c, now)
	}
	ins := st.Insights()
	if len(ins) != 1 || ins[0].Kind != "NEW_HEAVY_QUERY" || ins[0].CauseAtOpen != "N_PLUS_ONE" {
		t.Fatalf("insights %+v", ins)
	}
	if ins[0].Status != "RESOLVED" {
		t.Fatal("the flood stopped at 34, so the insight must be resolved")
	}
}

func TestSpillAndBlockerEvidence(t *testing.T) {
	c, f, st, now := setup(t)
	for i := 0; i < 45; i++ {
		if i == 25 {
			f.workMem = "64"
		}
		mean, temp := 70.0, int64(0)
		if i >= 25 {
			mean, temp = 90, 400
		}
		f.run(5, "SELECT id, rank() OVER (ORDER BY total) FROM shop.orders", 2, mean, temp)
		step(t, c, now)
	}
	ins := st.Insights()
	if len(ins) != 1 || ins[0].Signal != "spill" || ins[0].CauseAtOpen != "SPILL_TO_DISK" {
		t.Fatalf("insights %+v", ins)
	}
	if !strings.Contains(ins[0].Suggestion.SQL, "work_mem = '64MB'") {
		t.Fatalf("suggestion %q", ins[0].Suggestion.SQL)
	}
	if ins[0].Status != "OPEN" {
		t.Fatal("still spilling, so still open")
	}
}

func TestBlockersAreSampled(t *testing.T) {
	c, f, st, _ := setup(t)
	f.acts = []pgstat.Activity{
		{PID: 10, QueryID: 7, State: "active", WaitEventType: "Lock", BlockedBy: []int{99}},
		{PID: 11, QueryID: 7, State: "active", WaitEventType: "Lock", BlockedBy: []int{99}},
		{PID: 99, State: "idle in transaction", Query: "UPDATE shop.products SET stock = stock WHERE id <= 40", XactSeconds: 0.7},
	}
	if err := c.SampleActivity(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, ok := st.TopBlocker(7)
	if !ok || b.PID != 99 || b.Seen != 2 || !strings.HasPrefix(b.Query, "UPDATE") {
		t.Fatalf("blocker %+v", b)
	}
	if w := c.acc[7]; w == nil || w.samples != 2 || w.lock != 2 {
		t.Fatalf("waits %+v", w)
	}
}
