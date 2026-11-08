package detect

import (
	"encoding/json"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/manojrevenkar12-web/query-plan-insights/internal/config"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/store"
)

// Recordings of real evaluation runs (scripts/extract-series.py): per-second points for every workload query.
type recording struct {
	FaultAt time.Time                `json:"faultAt"`
	FixAt   time.Time                `json:"fixAt"`
	Queries map[string][]store.Point `json:"queries"`
}

func loadRecording(t *testing.T, name string) recording {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var r recording
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func shipped(t *testing.T) config.Detector {
	t.Helper()
	c, err := config.Load("../../config/qpi.json")
	if err != nil {
		t.Fatal(err)
	}
	return c.Detector
}

type opened struct {
	at     time.Time
	signal string
}

// replay steps a tracker over the points as the collector would, one tick per point, after the warm-up.
func replay(cfg config.Detector, points []store.Point) []opened {
	var tr Tracker
	var out []opened
	if len(points) == 0 {
		return nil
	}
	warm := points[0].At.Add(time.Duration(cfg.WarmupSeconds) * time.Second)
	for i := range points {
		now := points[i].At
		if v := tr.Step(cfg, points[:i+1], now); v.Open && !now.Before(warm) {
			out = append(out, opened{now, v.Signal})
		}
	}
	return out
}

func names(r recording) []string {
	var out []string
	for n := range r.Queries {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func TestReplayControlRaisesNothing(t *testing.T) {
	cfg := shipped(t)
	r := loadRecording(t, "control.json")
	for _, n := range names(r) {
		if got := replay(cfg, r.Queries[n]); len(got) != 0 {
			t.Errorf("%s: false alarm at %v", n, got)
		}
	}
}

func TestReplaySpillIsCaughtByTheSpillRule(t *testing.T) {
	cfg := shipped(t)
	r := loadRecording(t, "work_mem_spill.json")
	got := replay(cfg, r.Queries["rank_orders"])
	if len(got) == 0 || got[0].signal != "spill" || got[0].at.Before(r.FaultAt) || got[0].at.After(r.FixAt) {
		t.Fatalf("rank_orders: %v (fault %v, fix %v)", got, r.FaultAt, r.FixAt)
	}
	for _, n := range names(r) {
		if n == "rank_orders" || n == "top_customers" {
			continue
		}
		if got := replay(cfg, r.Queries[n]); len(got) != 0 {
			t.Errorf("%s: false alarm at %v", n, got)
		}
	}
}

func TestReplayLostIndexScanIsCaughtByTheLatencyRule(t *testing.T) {
	cfg := shipped(t)
	r := loadRecording(t, "planner_setting.json")
	got := replay(cfg, r.Queries["customer_orders"])
	if len(got) == 0 || got[0].signal != "latency" || got[0].at.Sub(r.FaultAt) > 6*time.Second {
		t.Fatalf("customer_orders: %v (fault %v)", got, r.FaultAt)
	}
}

func TestTrackerFreezesTheBaselineAtTheOnset(t *testing.T) {
	cfg := shipped(t)
	now := time.Unix(1000, 0)
	pts := points(now, 10, append(repeat(1, 20), repeat(10, 10)...)...)
	var tr Tracker
	var v Verdict
	for i := 20; i < 30; i++ {
		v = tr.Step(cfg, pts[:i+1], pts[i].At)
		if v.Open {
			break
		}
	}
	if !v.Open || v.Base.Mean() != 1 {
		t.Fatalf("baseline %v must stay at the pre-regression mean", v.Base.Mean())
	}
	if v.Recent.Mean() <= 5 {
		t.Fatalf("recent %v", v.Recent.Mean())
	}
}

func TestReplayOneSlowSecondIsNotARegression(t *testing.T) {
	cfg := shipped(t)
	r := loadRecording(t, "drop_index.json")
	if got := replay(cfg, r.Queries["reserve_stock"]); len(got) != 0 {
		t.Fatalf("reserve_stock had one 8 ms second during the fault and nothing else: %v", got)
	}
	got := replay(cfg, r.Queries["customer_orders"])
	if len(got) == 0 || got[0].at.Sub(r.FaultAt) > 5*time.Second {
		t.Fatalf("customer_orders lost its index: %v (fault %v)", got, r.FaultAt)
	}
}
