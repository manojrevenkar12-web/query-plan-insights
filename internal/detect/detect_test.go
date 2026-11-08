package detect

import (
	"testing"
	"time"

	"github.com/manojrevenkar12-web/query-plan-insights/internal/config"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/store"
)

var cfg = config.Detector{BaselineSeconds: 12, RecentSeconds: 3, MinCalls: 5, LatencyRatio: 2.5, MinDeltaMillis: 1,
	ResolveTolerance: 0.5}

// points makes one point per second ending at now, with the given mean for each second.
func points(now time.Time, calls int64, means ...float64) []store.Point {
	var out []store.Point
	for i, m := range means {
		at := now.Add(-time.Duration(len(means)-1-i) * time.Second)
		out = append(out, store.Point{At: at, Calls: calls, ExecMs: m * float64(calls)})
	}
	return out
}

func repeat(v float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestWindowsSplitBaselineAndRecent(t *testing.T) {
	now := time.Unix(1000, 0)
	pts := points(now, 10, append(repeat(1, 12), repeat(10, 3)...)...)
	base, recent := Windows(cfg, pts, now)
	if base.Calls != 120 || recent.Calls != 30 {
		t.Fatalf("calls base=%d recent=%d", base.Calls, recent.Calls)
	}
	if base.Mean() != 1 || recent.Mean() != 10 {
		t.Fatalf("means base=%v recent=%v", base.Mean(), recent.Mean())
	}
	if got := recent.CallsPerSec(); got != 10 {
		t.Fatalf("calls per second %v", got)
	}
}

func TestSlowerNeedsRatioAndMargin(t *testing.T) {
	cases := []struct {
		name       string
		base, rec  float64
		calls      int64
		wantSlower bool
	}{
		{"ten times slower", 1, 10, 10, true},
		{"ratio met but under a millisecond", 0.05, 0.5, 10, false},
		{"margin met but under the ratio", 10, 20, 10, false},
		{"too few calls", 1, 10, 1, false},
		{"faster", 10, 1, 10, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := Window{Calls: c.calls * 12, Ms: c.base * float64(c.calls*12)}
			r := Window{Calls: c.calls * 3, Ms: c.rec * float64(c.calls*3)}
			if c.calls == 1 {
				r.Calls, r.Ms = 3, c.rec*3
			}
			if got := Slower(cfg, b, r); got != c.wantSlower {
				t.Fatalf("Slower = %v", got)
			}
		})
	}
}

func TestRecoveredUsesTheFrozenBaseline(t *testing.T) {
	if !Recovered(cfg, 2, Window{Calls: 10, Ms: 25}) {
		t.Fatal("2.5 ms is within 50% of 2 ms")
	}
	if Recovered(cfg, 2, Window{Calls: 10, Ms: 31}) {
		t.Fatal("3.1 ms is not within 50% of 2 ms")
	}
	if Recovered(cfg, 2, Window{Calls: 2, Ms: 2}) {
		t.Fatal("two calls are not enough to call it recovered")
	}
}

func TestLockShare(t *testing.T) {
	if (Window{}).LockShare() != 0 {
		t.Fatal("no samples means no lock share")
	}
	if got := (Window{Samples: 8, Lock: 6}).LockShare(); got != 0.75 {
		t.Fatalf("lock share %v", got)
	}
}

func TestSpillingNeedsNewTempBlocksAndSomeSlowdown(t *testing.T) {
	c := cfg
	c.SpillTempBlocksPerCall, c.SpillLatencyRatio = 1, 1.2
	base := Window{Calls: 24, Ms: 24 * 70}
	spill := Window{Calls: 6, Ms: 6 * 95, Temp: 6 * 375}
	if ok, signal := Regressed(c, base, spill); !ok || signal != "spill" {
		t.Fatalf("a new 1.36x spill is a regression (%v %q)", ok, signal)
	}
	already := base
	already.Temp = 24 * 300
	if Spilling(c, already, spill) {
		t.Fatal("a query that already spilled has not started spilling")
	}
	noSlower := spill
	noSlower.Ms = 6 * 72
	if Spilling(c, base, noSlower) {
		t.Fatal("a spill that costs nothing is not a regression")
	}
	if ok, signal := Regressed(c, base, Window{Calls: 6, Ms: 6 * 200}); !ok || signal != "latency" {
		t.Fatalf("slower without temp blocks is the latency rule (%v %q)", ok, signal)
	}
}
