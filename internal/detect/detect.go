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

// Sum aggregates the points with from <= At < to.
func Sum(points []store.Point, from, to time.Time) Window {
	var w Window
	for _, p := range points {
		if p.At.Before(from) || !p.At.Before(to) {
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
	return Sum(points, baseFrom, recentFrom), Sum(points, recentFrom, now.Add(time.Nanosecond))
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

// Recovered is true when the recent mean is back within the tolerance of the frozen baseline.
func Recovered(cfg config.Detector, baselineMs float64, recent Window) bool {
	return recent.Calls >= cfg.MinCalls && recent.Mean() <= baselineMs*(1+cfg.ResolveTolerance)
}
