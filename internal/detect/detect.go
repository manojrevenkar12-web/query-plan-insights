// Package detect decides when a query has become slower than it was, using windows of per-interval deltas.
package detect

import (
	"time"

	"github.com/manojrevenkar12-web/query-plan-insights/internal/config"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/store"
)

// Window aggregates points over a time range.
type Window struct {
	Calls    int64   `json:"calls"`
	Ms       float64 `json:"ms"`
	Rows     int64   `json:"rows"`
	Read     int64   `json:"read"`
	Hit      int64   `json:"hit"`
	Temp     int64   `json:"temp"`
	Samples  int     `json:"samples"`
	Lock     int     `json:"lock"`
	Seconds  float64 `json:"seconds"`
	Interval int     `json:"intervals"`
}

// Sum aggregates the points with from < At <= to. A point stands for the interval that ends at At, so the window
// (now-3s, now] holds exactly three one-second intervals.
func Sum(points []store.Point, from, to time.Time) Window {
	var w Window
	for _, p := range points {
		if !p.At.After(from) || p.At.After(to) {
			continue
		}
		w.Calls += p.Calls
		w.Ms += p.ExecMs
		w.Rows += p.Rows
		w.Read += p.BlksRead
		w.Hit += p.BlksHit
		w.Temp += p.TempBlksWritten
		w.Samples += p.Samples
		w.Lock += p.LockSamples
		w.Interval++
	}
	w.Seconds = to.Sub(from).Seconds()
	return w
}

func (w Window) Mean() float64 { return per(w.Ms, w.Calls) }

func (w Window) PerCall(v int64) float64 { return per(float64(v), w.Calls) }

func (w Window) LockShare() float64 {
	if w.Samples == 0 {
		return 0
	}
	return float64(w.Lock) / float64(w.Samples)
}

func (w Window) CallsPerSec() float64 {
	if w.Seconds <= 0 {
		return 0
	}
	return float64(w.Calls) / w.Seconds
}

func per(v float64, calls int64) float64 {
	if calls == 0 {
		return 0
	}
	return v / float64(calls)
}

// Windows returns the baseline window and the recent window ending at now.
func Windows(cfg config.Detector, points []store.Point, now time.Time) (base, recent Window) {
	recentFrom := now.Add(-time.Duration(cfg.RecentSeconds) * time.Second)
	baseFrom := recentFrom.Add(-time.Duration(cfg.BaselineSeconds) * time.Second)
	return Sum(points, baseFrom, recentFrom), Sum(points, recentFrom, now)
}

// Slower is true when the recent window is slower than the baseline by both the ratio and the absolute margin, with
// enough calls on both sides to mean something.
func Slower(cfg config.Detector, base, recent Window) bool {
	if base.Calls < cfg.MinCalls || recent.Calls < cfg.MinCalls {
		return false
	}
	b, r := base.Mean(), recent.Mean()
	return r >= cfg.LatencyRatio*b && r-b >= cfg.MinDeltaMillis
}

// Spilling is true when a query that did not write temporary files now does, and is slower for it. A spill costs
// less on a fast disk than a lost index, so it needs a smaller slowdown to count; the temp blocks are the signal.
func Spilling(cfg config.Detector, base, recent Window) bool {
	if base.Calls < cfg.MinCalls || recent.Calls < cfg.MinCalls {
		return false
	}
	tempNow := recent.PerCall(recent.Temp)
	b, r := base.Mean(), recent.Mean()
	return tempNow >= cfg.SpillTempBlocksPerCall && base.PerCall(base.Temp) <= tempNow/10 &&
		r >= cfg.SpillLatencyRatio*b && r-b >= cfg.MinDeltaMillis
}

// Regressed is the detector: slower by the latency rule, or newly spilling. It names the signal that fired.
func Regressed(cfg config.Detector, base, recent Window) (bool, string) {
	if Slower(cfg, base, recent) {
		return true, "latency"
	}
	if Spilling(cfg, base, recent) {
		return true, "spill"
	}
	return false, ""
}

// Recovered is true when the recent mean is back within the tolerance of the frozen baseline.
func Recovered(cfg config.Detector, baselineMs float64, recent Window) bool {
	return recent.Calls >= cfg.MinCalls && recent.Mean() <= baselineMs*(1+cfg.ResolveTolerance)
}

// Tracker follows one query between ticks. Once a window looks bad, the baseline is frozen at the start of that window
// and the evidence grows from there: the baseline is not contaminated by the regression it is judging, and a slow
// query called twice a second is judged on all its slow calls, not on the last three seconds alone.
type Tracker struct {
	ticks int
	onset time.Time
}

// Verdict is what one tick concluded.
type Verdict struct {
	Open   bool      // the regression persisted: open an insight
	Signal string    // latency or spill
	Onset  time.Time // start of the first bad window
	Base   Window    // the frozen baseline
	Recent Window    // everything since the onset
}

// Step judges the query at now. It returns Open once the regression has held for PersistTicks ticks, then starts over.
func (t *Tracker) Step(cfg config.Detector, points []store.Point, now time.Time) Verdict {
	var base, recent Window
	if t.ticks == 0 {
		base, recent = Windows(cfg, points, now)
	} else {
		base = Sum(points, t.onset.Add(-time.Duration(cfg.BaselineSeconds)*time.Second), t.onset)
		recent = Sum(points, t.onset, now)
	}
	slow, signal := Regressed(cfg, base, recent)
	if slow && !latestAgrees(cfg, signal, base, points, now) {
		slow = false // one bad second is a spike, not a regression: every tick's newest interval must agree
	}
	if !slow {
		t.ticks = 0
		return Verdict{Base: base, Recent: recent}
	}
	if t.ticks == 0 {
		t.onset = now.Add(-time.Duration(cfg.RecentSeconds) * time.Second)
	}
	t.ticks++
	v := Verdict{Signal: signal, Onset: t.onset, Base: base, Recent: recent}
	if t.ticks >= cfg.PersistTicks {
		v.Open = true
		t.ticks = 0
	}
	return v
}

// Suspected reports whether the last tick looked bad.
func (t *Tracker) Suspected() bool { return t.ticks > 0 }

// Reset forgets any suspicion.
func (t *Tracker) Reset() { t.ticks = 0 }

// latestAgrees checks the newest interval on its own. An interval without calls says nothing either way.
func latestAgrees(cfg config.Detector, signal string, base Window, points []store.Point, now time.Time) bool {
	if len(points) == 0 {
		return false
	}
	p := points[len(points)-1]
	if p.At.After(now) || p.Calls == 0 {
		return true
	}
	if signal == "spill" {
		return float64(p.TempBlksWritten)/float64(p.Calls) >= cfg.SpillTempBlocksPerCall
	}
	m, b := p.MeanMs(), base.Mean()
	return m >= cfg.LatencyRatio*b && m-b >= cfg.MinDeltaMillis
}
