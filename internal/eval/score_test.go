package eval

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manojrevenkar12-web/query-plan-insights/internal/store"
)

var t0 = time.Unix(3_000_000, 0)

func at(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }

func recording(sc Scenario, ins ...store.Insight) Recording {
	return Recording{Scenario: sc, Timeline: Timeline{Start: at(0), FaultAt: at(20), FixAt: at(45), End: at(65)},
		Names: map[int64]string{1: "customer_orders", 2: "order_items", 3: "product"}, Insights: ins, HolderPID: 777}
}

var dropIndex = Scenario{Name: "drop_index", App: "shop", Target: "customer_orders", AlsoAffected: []string{"order_items"},
	Kind: "LATENCY_REGRESSION", Cause: "MISSING_INDEX", Suggestion: "CREATE INDEX CONCURRENTLY orders_customer_idx"}

func resolvedAt(s int) *time.Time { t := at(s); return &t }

func TestScoreAHit(t *testing.T) {
	o := Score("dev", recording(dropIndex,
		store.Insight{QueryID: 1, Kind: "LATENCY_REGRESSION", OpenedAt: at(23), CauseAtOpen: "MISSING_INDEX",
			Cause:      store.Cause{Code: "MISSING_INDEX"},
			Suggestion: store.Suggestion{SQL: "CREATE INDEX CONCURRENTLY orders_customer_idx ON shop.orders (customer_id);"},
			Status:     "RESOLVED", ResolvedAt: resolvedAt(51), Verification: "back to 0.08 ms"},
		store.Insight{QueryID: 2, Kind: "LATENCY_REGRESSION", OpenedAt: at(24)}))
	if !o.Detected || o.DetectSeconds != 3 || !o.CauseCorrect || !o.FinalCauseCorrect || !o.SuggestionCorrect {
		t.Fatalf("outcome %+v", o)
	}
	if !o.Resolved || o.ResolveSeconds != 6 || o.Verification == "" {
		t.Fatalf("resolution %+v", o)
	}
	if len(o.FalseAlarms) != 0 || len(o.CollateralFound) != 1 {
		t.Fatalf("an expected side effect is not a false alarm: %+v", o)
	}
}

func TestScoreFalseAlarms(t *testing.T) {
	o := Score("dev", recording(dropIndex,
		store.Insight{QueryID: 1, Kind: "LATENCY_REGRESSION", OpenedAt: at(10)}, // before the fault
		store.Insight{QueryID: 3, Kind: "LATENCY_REGRESSION", OpenedAt: at(25)}, // a query the fault does not touch
	))
	if o.Detected {
		t.Fatal("an insight before the fault is not a detection")
	}
	if len(o.FalseAlarms) != 2 {
		t.Fatalf("false alarms %v", o.FalseAlarms)
	}
}

func TestScoreWrongCauseAndLateDetection(t *testing.T) {
	o := Score("dev", recording(dropIndex,
		store.Insight{QueryID: 1, Kind: "LATENCY_REGRESSION", OpenedAt: at(30), CauseAtOpen: "PLAN_CHANGE",
			Cause: store.Cause{Code: "MISSING_INDEX"}, Status: "OPEN"}))
	if !o.Detected || o.CauseCorrect || !o.FinalCauseCorrect || o.Resolved {
		t.Fatalf("outcome %+v", o)
	}
	late := Score("dev", recording(dropIndex, store.Insight{QueryID: 1, Kind: "LATENCY_REGRESSION", OpenedAt: at(50)}))
	if late.Detected || len(late.FalseAlarms) != 0 {
		t.Fatalf("an insight after the fix is neither a detection nor a false alarm: %+v", late)
	}
}

func TestScoreSubstitutesTheHolderPID(t *testing.T) {
	sc := Scenario{Name: "locks", App: "shop", Target: "product", Kind: "LATENCY_REGRESSION", Cause: "LOCK_CONTENTION",
		Suggestion: "$HOLDER_PID"}
	o := Score("dev", recording(sc, store.Insight{QueryID: 3, Kind: "LATENCY_REGRESSION", OpenedAt: at(22),
		Suggestion: store.Suggestion{SQL: "SELECT pid, xact_start, state, query FROM pg_stat_activity WHERE pid = 777;"}}))
	if !o.SuggestionCorrect {
		t.Fatalf("outcome %+v", o)
	}
}

func TestScoreControl(t *testing.T) {
	o := Score("dev", recording(Scenario{Name: "control", App: "shop"}, store.Insight{QueryID: 2, OpenedAt: at(30)}))
	if !o.Control || len(o.FalseAlarms) != 1 {
		t.Fatalf("every insight in a control is a false alarm: %+v", o)
	}
}

func TestSummarise(t *testing.T) {
	s := Summarise([]Outcome{
		{Detected: true, DetectSeconds: 3, CauseCorrect: true, SuggestionCorrect: true, Resolved: true, ResolveSeconds: 6},
		{Detected: true, DetectSeconds: 5, Resolved: true, ResolveSeconds: 4, FalseAlarms: []string{"x"}},
		{Detected: false},
		{Control: true, FalseAlarms: []string{}},
	})
	if s.Faults != 3 || s.Controls != 1 || s.Detected != 2 || s.CauseCorrect != 1 || s.FalseAlarms != 1 {
		t.Fatalf("summary %+v", s)
	}
	if s.MedianDetectS != 4 || s.MaxDetectS != 5 || s.MedianResolveS != 5 {
		t.Fatalf("times %+v", s)
	}
}

func TestNormalMatchesPgStatStatementsText(t *testing.T) {
	cases := map[string]string{
		"SELECT id FROM shop.orders WHERE customer_id = $1 ORDER BY created_at DESC LIMIT 20": "SELECT id FROM shop.orders WHERE customer_id = $1 ORDER BY created_at DESC LIMIT $2",
		"UPDATE shop.products SET stock = stock - 1 WHERE id = $1":                            "UPDATE shop.products SET stock = stock - $2 WHERE id = $1",
		"SELECT a FROM t WHERE s = 'paid'\n\t\tAND x > 1.5":                                   "SELECT a FROM t WHERE s = $1 AND x > $2",
	}
	for app, pgss := range cases {
		if normal(app) != normal(pgss) {
			t.Errorf("%q != %q", normal(app), normal(pgss))
		}
	}
	if normal("SELECT a FROM t1") == normal("SELECT a FROM t2") {
		t.Error("digits inside identifiers are not constants")
	}
}

func TestTheShippedSpecIsValidAndPinned(t *testing.T) {
	root, _ := filepath.Abs("../..")
	spec, err := LoadSpec(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Dev) == 0 || len(spec.Heldout) == 0 {
		t.Fatal("both sets need scenarios")
	}
	if err := CheckPin(root, spec); err != nil {
		t.Fatal(err)
	}
	devApps, heldApps := map[string]bool{}, map[string]bool{}
	for _, s := range spec.Dev {
		devApps[s.App] = true
	}
	for _, s := range spec.Heldout {
		heldApps[s.App] = true
		if devApps[s.App] {
			t.Errorf("held-out scenario %s runs on the development app", s.Name)
		}
	}
}

func TestSpecValidation(t *testing.T) {
	bad := []Scenario{
		{Name: "x", App: "nope"},
		{Name: "x", App: "shop", Fault: &Action{}},
		{Name: "x", App: "shop", Target: "product"},
	}
	for _, s := range bad {
		if s.validate() == nil {
			t.Errorf("%+v accepted", s)
		}
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "config"), 0o755)
	os.WriteFile(filepath.Join(dir, "config", "heldout.sha256"), []byte("0000000000000000\n"), 0o644)
	if CheckPin(dir, Spec{}) == nil {
		t.Error("a changed held-out set must fail the pin")
	}
}

func TestScoreContentionDuringTheFaultIsCollateral(t *testing.T) {
	o := Score("dev", recording(dropIndex,
		store.Insight{QueryID: 3, Kind: "LATENCY_REGRESSION", OpenedAt: at(26), CauseAtOpen: "RESOURCE_CONTENTION"},
		store.Insight{QueryID: 3, Kind: "LATENCY_REGRESSION", OpenedAt: at(50), CauseAtOpen: "RESOURCE_CONTENTION"}))
	if len(o.CollateralFound) != 1 || o.CollateralFound[0] != "product (contention)" || len(o.FalseAlarms) != 1 {
		t.Fatalf("contention while the fault runs is collateral, after the fix it is a false alarm: %+v", o)
	}
	c := Score("dev", recording(Scenario{Name: "control", App: "shop"},
		store.Insight{QueryID: 3, OpenedAt: at(26), CauseAtOpen: "RESOURCE_CONTENTION"}))
	if len(c.FalseAlarms) != 1 {
		t.Fatalf("in a control every insight is a false alarm: %+v", c)
	}
}
