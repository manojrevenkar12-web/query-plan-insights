package diagnose

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/manojrevenkar12-web/query-plan-insights/internal/config"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/detect"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/plan"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/store"
)

var cfg = config.Diagnosis{EventLookbackSeconds: 60, LockShareMin: 0.3, TempBlocksPerCallMin: 1, ReadRatioMin: 3,
	RowsRatioMin: 2, ModSinceAnalyzeShareMin: 0.2, EstimateErrorMin: 10}

var t0 = time.Unix(2_000_000, 0)

func at(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }

func fixture(t *testing.T, name string) *plan.Node {
	t.Helper()
	b, err := os.ReadFile("../plan/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	n, err := plan.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func query(plans ...*plan.Node) store.Query {
	q := store.Query{ID: 1, Text: "SELECT id, total FROM shop.orders WHERE customer_id = $1"}
	for i, p := range plans {
		q.Plans = append(q.Plans, store.PlanVersion{At: at(i * 20), Fingerprint: p.Fingerprint(), Root: p})
	}
	return q
}

var base = detect.Window{Calls: 300, Ms: 30, Rows: 3000, Read: 0, Hit: 900, Samples: 40}
var slow = detect.Window{Calls: 75, Ms: 1500, Rows: 750, Read: 0, Hit: 60000, Samples: 40}

func noTables(string) (store.TableInfo, bool) { return store.TableInfo{}, false }

func TestDroppedIndexThatThePlanUsed(t *testing.T) {
	r := Regression(cfg, Input{Query: query(fixture(t, "index_scan.json"), fixture(t, "seq_scan.json")), SuspectedAt: at(21),
		Base: base, Recent: slow, Tables: noTables, Events: []store.Event{{At: at(19), Kind: "INDEX_DROPPED",
			Relation: "shop.orders", Name: "orders_customer_idx",
			Before: "CREATE INDEX orders_customer_idx ON shop.orders USING btree (customer_id)"}}})
	if r.Cause.Code != "MISSING_INDEX" || r.Cause.Confidence < 0.9 {
		t.Fatalf("cause %+v", r.Cause)
	}
	if r.Suggestion.SQL != "CREATE INDEX CONCURRENTLY orders_customer_idx ON shop.orders USING btree (customer_id);" {
		t.Fatalf("suggestion %q", r.Suggestion.SQL)
	}
	if len(r.Changes) != 1 || r.PlanBefore == "" || r.PlanAfter == "" {
		t.Fatalf("plan evidence %+v", r.Changes)
	}
	if r.Ranked[0].Code != "MISSING_INDEX" || r.Ranked[len(r.Ranked)-1].Code != "PLAN_CHANGE" {
		t.Fatalf("ranking %+v", r.Ranked)
	}
}

func TestDroppedIndexOnAnotherTableIsNotTheCause(t *testing.T) {
	r := Regression(cfg, Input{Query: query(fixture(t, "index_scan.json"), fixture(t, "seq_scan.json")), SuspectedAt: at(21),
		Base: base, Recent: slow, Tables: noTables, Events: []store.Event{{At: at(19), Kind: "INDEX_DROPPED",
			Relation: "shop.customers", Name: "customers_country_idx"}}})
	if r.Cause.Code != "PLAN_CHANGE" {
		t.Fatalf("cause %+v", r.Cause)
	}
	if !strings.Contains(r.Suggestion.SQL, "shop.orders (customer_id)") {
		t.Fatalf("suggestion %q", r.Suggestion.SQL)
	}
}

func TestPlannerSettingWithAPlanChange(t *testing.T) {
	r := Regression(cfg, Input{Query: query(fixture(t, "index_scan.json"), fixture(t, "seq_scan.json")), SuspectedAt: at(21),
		Base: base, Recent: slow, Tables: noTables, Events: []store.Event{{At: at(18), Kind: "SETTING_CHANGED",
			Name: "random_page_cost", Before: "4", After: "4000"}}})
	if r.Cause.Code != "SETTING_CHANGE" || !strings.Contains(r.Suggestion.SQL, "ALTER SYSTEM RESET random_page_cost") {
		t.Fatalf("cause %+v suggestion %+v", r.Cause, r.Suggestion)
	}
}

func TestPlannerSettingWithoutAPlanChangeIsNotBlamed(t *testing.T) {
	r := Regression(cfg, Input{Query: query(fixture(t, "index_scan.json")), SuspectedAt: at(21), Base: base, Recent: slow,
		Tables: noTables, Events: []store.Event{{At: at(18), Kind: "SETTING_CHANGED", Name: "random_page_cost", Before: "4",
			After: "4000"}}})
	if r.Cause.Code == "SETTING_CHANGE" {
		t.Fatalf("a setting that did not change the plan is not the cause: %+v", r.Cause)
	}
}

func TestWorkMemSpill(t *testing.T) {
	spilling := slow
	spilling.Temp = 75 * 400
	r := Regression(cfg, Input{Query: query(fixture(t, "hash_join.json")), SuspectedAt: at(21), Base: base, Recent: spilling,
		Tables: noTables, Events: []store.Event{{At: at(18), Kind: "SETTING_CHANGED", Name: "work_mem", Before: "65536", After: "64"}}})
	if r.Cause.Code != "SPILL_TO_DISK" || r.Cause.Confidence < 0.9 {
		t.Fatalf("cause %+v", r.Cause)
	}
	if !strings.Contains(r.Suggestion.SQL, "work_mem = '64MB'") {
		t.Fatalf("the fix restores the old value: %q", r.Suggestion.SQL)
	}
}

func TestSpillWithoutASettingChangeSizesWorkMem(t *testing.T) {
	spilling := slow
	spilling.Temp = 75 * 1280 // 10 MB per call
	r := Regression(cfg, Input{Query: query(fixture(t, "hash_join.json")), SuspectedAt: at(21), Base: base, Recent: spilling,
		Tables: noTables})
	if r.Cause.Code != "SPILL_TO_DISK" || r.Suggestion.SQL != "SET work_mem = '21MB'; -- for this role or session" {
		t.Fatalf("cause %+v suggestion %q", r.Cause, r.Suggestion.SQL)
	}
}

func TestLockContentionNamesTheBlocker(t *testing.T) {
	locked := slow
	locked.Lock = 30
	r := Regression(cfg, Input{Query: query(fixture(t, "index_scan.json")), SuspectedAt: at(21), Base: base, Recent: locked,
		Tables: noTables, Blocker: &store.Blocker{PID: 4242, Query: "UPDATE shop.products SET stock = stock WHERE id <= 40",
			XactSeconds: 0.8}})
	if r.Cause.Code != "LOCK_CONTENTION" || !strings.Contains(r.Cause.Summary, "pid 4242") {
		t.Fatalf("cause %+v", r.Cause)
	}
	if !strings.Contains(r.Suggestion.SQL, "pid = 4242") {
		t.Fatalf("suggestion %q", r.Suggestion.SQL)
	}
}

func TestLockWaitsThatWereAlreadyThereAreNotNew(t *testing.T) {
	b, rec := base, slow
	b.Lock, rec.Lock = 30, 30
	r := Regression(cfg, Input{Query: query(fixture(t, "index_scan.json")), SuspectedAt: at(21), Base: b, Recent: rec,
		Tables: noTables})
	if r.Cause.Code == "LOCK_CONTENTION" {
		t.Fatal("lock waits at baseline are not a new cause")
	}
}

func TestStaleStatistics(t *testing.T) {
	tables := func(rel string) (store.TableInfo, bool) {
		return store.TableInfo{LiveTuples: 300000, ModSinceAnalyze: 150000}, rel == "shop.orders"
	}
	r := Regression(cfg, Input{Query: query(fixture(t, "index_scan.json"), fixture(t, "seq_scan.json")), SuspectedAt: at(21),
		Base: base, Recent: slow, Tables: tables})
	if r.Cause.Code != "STALE_STATISTICS" || r.Suggestion.SQL != "ANALYZE shop.orders;" {
		t.Fatalf("cause %+v suggestion %q", r.Cause, r.Suggestion.SQL)
	}
}

func TestMoreRowsAndColdCache(t *testing.T) {
	grown := slow
	grown.Rows = 75 * 40
	if r := Regression(cfg, Input{Query: query(), SuspectedAt: at(21), Base: base, Recent: grown, Tables: noTables}); r.Cause.Code != "DATA_GROWTH" {
		t.Fatalf("cause %+v", r.Cause)
	}
	cold := slow
	cold.Read = 75 * 50
	if r := Regression(cfg, Input{Query: query(), SuspectedAt: at(21), Base: base, Recent: cold, Tables: noTables}); r.Cause.Code != "CACHE_MISSES" {
		t.Fatalf("cause %+v", r.Cause)
	}
}

func TestNothingToGoOn(t *testing.T) {
	r := Regression(cfg, Input{Query: query(), SuspectedAt: at(21), Base: base, Recent: slow, Tables: noTables})
	if r.Cause.Code != "UNKNOWN" || len(r.Evidence) != 1 {
		t.Fatalf("cause %+v evidence %v", r.Cause, r.Evidence)
	}
}

func TestOldPlansAreNotCompared(t *testing.T) {
	q := query(fixture(t, "index_scan.json"), fixture(t, "seq_scan.json"))
	r := Regression(cfg, Input{Query: q, SuspectedAt: at(500), Base: base, Recent: slow, Tables: noTables})
	if len(r.Changes) != 0 || r.PlanBefore != "" {
		t.Fatalf("a plan change long before the regression is not evidence: %+v", r.Changes)
	}
}

func TestNewQuery(t *testing.T) {
	perItem := detect.Window{Calls: 360, Ms: 36, Rows: 360, Seconds: 3}
	if r := NewQuery(perItem, 0.4); r.Cause.Code != "N_PLUS_ONE" || !strings.Contains(r.Suggestion.SQL, "ANY($1)") {
		t.Fatalf("cause %+v", r.Cause)
	}
	heavy := detect.Window{Calls: 300, Ms: 3000, Rows: 30000, Seconds: 3}
	if r := NewQuery(heavy, 0.4); r.Cause.Code != "HEAVY_NEW_QUERY" {
		t.Fatalf("cause %+v", r.Cause)
	}
}

func TestRender(t *testing.T) {
	got := Render(fixture(t, "index_scan.json"))
	want := "Limit  (rows=6 cost=27.7)\n  Sort  (rows=6 cost=27.7)\n    Bitmap Heap Scan on orders  (rows=6 cost=27.6)\n" +
		"      Bitmap Index Scan using orders_customer_idx  (rows=6 cost=4.5)\n"
	if got != want {
		t.Fatalf("render\n%s", got)
	}
}

func TestWorkMemThatChangedThePlanWithoutSpilling(t *testing.T) {
	r := Regression(cfg, Input{Query: query(fixture(t, "index_scan.json"), fixture(t, "seq_scan.json")), SuspectedAt: at(21),
		Base: base, Recent: slow, Tables: noTables, Events: []store.Event{{At: at(18), Kind: "SETTING_CHANGED", Name: "work_mem",
			Before: "65536", After: "64"}}})
	if r.Cause.Code != "SETTING_CHANGE" || !strings.Contains(r.Cause.Summary, "64MB to 64kB") ||
		!strings.Contains(r.Suggestion.SQL, "work_mem = '64MB'") {
		t.Fatalf("cause %+v suggestion %q", r.Cause, r.Suggestion.SQL)
	}
}

func TestResourceContentionPointsAtTheBusyQuery(t *testing.T) {
	c := cfg
	c.ContentionRatioMin = 2
	r := Regression(c, Input{Query: query(fixture(t, "hash_join.json")), SuspectedAt: at(21), Base: base, Recent: slow,
		Tables: noTables, DBBase: detect.Window{Ms: 1200, Seconds: 12}, DBRecent: detect.Window{Ms: 2400, Seconds: 3},
		TopConsumer: "SELECT product_id, qty FROM shop.order_items WHERE order_id = $1"})
	if r.Cause.Code != "RESOURCE_CONTENTION" || !strings.Contains(r.Cause.Summary, "8.0x busier") {
		t.Fatalf("cause %+v", r.Cause)
	}
	if !strings.Contains(r.Suggestion.Text, "shop.order_items") {
		t.Fatalf("suggestion %q", r.Suggestion.Text)
	}
	quiet := Regression(c, Input{Query: query(fixture(t, "hash_join.json")), SuspectedAt: at(21), Base: base, Recent: slow,
		Tables: noTables, DBBase: detect.Window{Ms: 1200, Seconds: 12}, DBRecent: detect.Window{Ms: 400, Seconds: 3}})
	if quiet.Cause.Code == "RESOURCE_CONTENTION" {
		t.Fatal("a database that is not busier is not contention")
	}
}

func TestLockWaitsAreNotMistakenForABusierDatabase(t *testing.T) {
	c := cfg
	c.ContentionRatioMin = 2
	locked := slow
	locked.Lock = 12
	r := Regression(c, Input{Query: query(fixture(t, "index_scan.json")), SuspectedAt: at(21), Base: base, Recent: locked,
		Tables: noTables, DBBase: detect.Window{Ms: 2600, Seconds: 12}, DBRecent: detect.Window{Ms: 7260, Seconds: 3}})
	for _, c := range r.Ranked {
		if c.Code == "RESOURCE_CONTENTION" {
			t.Fatalf("blocked calls inflate database time; that is lock contention: %+v", r.Ranked)
		}
	}
}
