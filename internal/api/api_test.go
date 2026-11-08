package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manojrevenkar12-web/query-plan-insights/internal/config"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/plan"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/store"
)

var t0 = time.Unix(4_000_000, 0)

func at(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }

func fixture() (View, *store.Store) {
	st := store.New(time.Hour, at(0))
	for i := 1; i <= 30; i++ {
		ms := 0.1
		if i > 20 {
			ms = 20
		}
		st.AddPoint(11, "SELECT id FROM shop.orders WHERE customer_id = $1", store.Point{At: at(i), Calls: 25, ExecMs: ms * 25, Rows: 250})
		st.AddPoint(22, "SELECT name FROM shop.products WHERE id = $1", store.Point{At: at(i), Calls: 80, ExecMs: 0.02 * 80, Rows: 80})
		st.AddDBPoint(store.Point{At: at(i), Calls: 105, ExecMs: ms*25 + 1.6})
	}
	st.AddPlan(11, at(5), &plan.Node{Type: "Index Scan", Relation: "orders", Index: "orders_customer_idx"})
	st.AddPlan(11, at(21), &plan.Node{Type: "Seq Scan", Relation: "orders"})
	st.AddEvent(store.Event{At: at(20), Kind: "INDEX_DROPPED", Relation: "shop.orders", Name: "orders_customer_idx"})
	in := st.OpenInsight(&store.Insight{Kind: "LATENCY_REGRESSION", QueryID: 11, OpenedAt: at(23), BaselineMs: 0.1,
		CurrentMs: 20, ImpactMsPerS: 497.5, Cause: store.Cause{Code: "MISSING_INDEX"}, CauseAtOpen: "MISSING_INDEX"})
	st.Update(in, func(x *store.Insight) { x.CurrentMs = 21 })
	cfg := config.Config{Detector: config.Detector{RecentSeconds: 3}}
	return View{Cfg: cfg, Store: st, Now: func() time.Time { return at(30) }, OverviewRows: 10, WindowSeconds: 300}, st
}

func get(t *testing.T, h http.Handler, path string, into any) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if into != nil && rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	return rec
}

func handler() http.Handler {
	v, _ := fixture()
	return Handler(v, func() (int, time.Duration) { return 30, 150 * time.Millisecond }, nil)
}

func TestOverviewPutsRegressionsFirst(t *testing.T) {
	var o Overview
	get(t, handler(), "/api/overview", &o)
	if o.OpenInsights != 1 || o.ImpactMsPerS != 497.5 || o.TrackedTotal != 2 || len(o.Events) != 1 {
		t.Fatalf("overview %+v", o)
	}
	first := o.Queries[0]
	if first.ID != 11 || first.Status != "REGRESSED" || first.InsightID != 1 || first.MeanMs != 20 {
		t.Fatalf("first row %+v", first)
	}
	if first.CallsPerSec != 25 || len(first.Spark) != 30 {
		t.Fatalf("rates %+v", first)
	}
	if o.Queries[1].Status != "OK" {
		t.Fatalf("second row %+v", o.Queries[1])
	}
}

func TestInsightsAndFilters(t *testing.T) {
	h := handler()
	var all []InsightView
	get(t, h, "/api/insights", &all)
	if len(all) != 1 || all[0].WorstMs != 21 {
		t.Fatalf("insights %+v", all)
	}
	var resolved []InsightView
	get(t, h, "/api/insights?status=resolved", &resolved)
	if resolved == nil || len(resolved) != 0 {
		t.Fatalf("an empty filter is an empty list, not null: %v", resolved)
	}
	var one InsightView
	get(t, h, "/api/insights/1", &one)
	if one.QueryID != 11 {
		t.Fatalf("insight %+v", one)
	}
	if rec := get(t, h, "/api/insights/9", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
	if rec := get(t, h, "/api/insights/abc", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestQueryDetail(t *testing.T) {
	h := handler()
	var d QueryDetail
	get(t, h, "/api/queries/11", &d)
	if len(d.Series) != 30 || len(d.Plans) != 2 || len(d.Insights) != 1 {
		t.Fatalf("detail %+v", d)
	}
	if !strings.HasPrefix(d.Plans[1].Tree, "Seq Scan on orders") {
		t.Fatalf("tree %q", d.Plans[1].Tree)
	}
	if d.Series[29].RowsPerCall != 10 {
		t.Fatalf("rows per call %v", d.Series[29].RowsPerCall)
	}
	if rec := get(t, h, "/api/queries/5", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestSnapshotHoldsEveryQueryItShows(t *testing.T) {
	v, _ := fixture()
	s := v.Snapshot()
	if len(s.Queries) != 2 || len(s.Insights) != 1 || s.Queries[0].ID != 11 {
		t.Fatalf("snapshot queries %d insights %d", len(s.Queries), len(s.Insights))
	}
}

func TestMetrics(t *testing.T) {
	rec := get(t, handler(), "/metrics", nil)
	body := rec.Body.String()
	for _, want := range []string{
		`qpi_insights{kind="LATENCY_REGRESSION",cause="MISSING_INDEX",status="OPEN"} 1`,
		"qpi_open_impact_ms_per_second 497.500",
		"qpi_collector_ticks_total 30",
		"qpi_collector_tick_seconds_total 0.150000",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %q:\n%s", want, body)
		}
	}
}
