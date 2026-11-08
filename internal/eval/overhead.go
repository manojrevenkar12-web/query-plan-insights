package eval

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/manojrevenkar12-web/query-plan-insights/internal/collector"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/pgstat"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/store"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/workload"
)

// Overhead is the cost of running the collector: closed-loop throughput of a point lookup with it off and on,
// alternating, so drift on the machine affects both sides alike.
type Overhead struct {
	Rounds      int       `json:"rounds"`
	Seconds     int       `json:"secondsPerPhase"`
	Workers     int       `json:"workers"`
	OffQPS      []float64 `json:"offQps"`
	OnQPS       []float64 `json:"onQps"`
	OffMean     float64   `json:"offMeanQps"`
	OnMean      float64   `json:"onMeanQps"`
	OverheadPct float64   `json:"overheadPct"`
	Ticks       int       `json:"ticks"`
	TickMeanMs  float64   `json:"tickMeanMs"`
}

func closedLoop(ctx context.Context, db *sql.DB, workers int, d time.Duration, seed uint64) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	var n atomic.Int64
	var firstErr atomic.Value
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			r := workload.NewRand(seed + uint64(w))
			for ctx.Err() == nil {
				var name string
				var price float64
				var stock int
				err := db.QueryRowContext(ctx, `SELECT name, price, stock FROM shop.products WHERE id = $1`, 1+r.Intn(2000)).
					Scan(&name, &price, &stock)
				if err != nil {
					if ctx.Err() == nil {
						firstErr.CompareAndSwap(nil, err)
					}
					return
				}
				n.Add(1)
			}
		}(w)
	}
	wg.Wait()
	if e, ok := firstErr.Load().(error); ok {
		return 0, e
	}
	return float64(n.Load()) / d.Seconds(), nil
}

// MeasureOverhead runs the closed loop with the collector off and on, Rounds times each.
func MeasureOverhead(ctx context.Context, env Env) (Overhead, error) {
	cfg := env.Cfg
	o := Overhead{Rounds: cfg.Eval.OverheadRounds, Seconds: cfg.Eval.OverheadSeconds, Workers: cfg.Eval.OverheadWorkers}
	admin, err := open(env.DSN.Admin, 2)
	if err != nil {
		return o, err
	}
	defer admin.Close()
	if err := Reset(ctx, admin, cfg.Eval, env.DSN.AppRole); err != nil {
		return o, err
	}
	if err := workload.Setup(ctx, admin, workload.Shop(), env.DSN.AppRole, env.DSN.MonitorRole); err != nil {
		return o, err
	}
	appDB, err := open(env.DSN.App, o.Workers)
	if err != nil {
		return o, err
	}
	defer appDB.Close()
	mon, err := open(env.DSN.Monitor, 3)
	if err != nil {
		return o, err
	}
	defer mon.Close()
	phase := time.Duration(o.Seconds) * time.Second
	if _, err := closedLoop(ctx, appDB, o.Workers, phase/2, cfg.Eval.Seed); err != nil { // warm the cache and the pool
		return o, err
	}
	var collect time.Duration
	for round := 0; round < o.Rounds; round++ {
		off, err := closedLoop(ctx, appDB, o.Workers, phase, cfg.Eval.Seed+uint64(round))
		if err != nil {
			return o, err
		}
		cctx, cancel := context.WithCancel(ctx)
		col := collector.New(cfg, pgstat.Reader{DB: mon}, store.New(phase*2, time.Now()), env.DSN.AppRole, nopWriter{})
		done := make(chan struct{})
		go func() { _ = col.Run(cctx); close(done) }()
		on, err := closedLoop(ctx, appDB, o.Workers, phase, cfg.Eval.Seed+uint64(round))
		cancel()
		<-done
		if err != nil {
			return o, err
		}
		ticks, t := col.Stats()
		o.Ticks += ticks
		collect += t
		o.OffQPS = append(o.OffQPS, off)
		o.OnQPS = append(o.OnQPS, on)
	}
	for i := range o.OffQPS {
		o.OffMean += o.OffQPS[i] / float64(len(o.OffQPS))
		o.OnMean += o.OnQPS[i] / float64(len(o.OnQPS))
	}
	if o.OffMean == 0 {
		return o, fmt.Errorf("no throughput with the collector off")
	}
	o.OverheadPct = (o.OffMean - o.OnMean) / o.OffMean * 100
	if o.Ticks > 0 {
		o.TickMeanMs = float64(collect.Microseconds()) / 1000 / float64(o.Ticks)
	}
	return o, nil
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
