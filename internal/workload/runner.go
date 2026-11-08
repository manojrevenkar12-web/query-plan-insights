package workload

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Rand is a goroutine-local deterministic source.
type Rand struct{ r *rand.Rand }

func NewRand(seed uint64) *Rand { return &Rand{rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))} }

func (r *Rand) Intn(n int) int { return r.r.IntN(n) }

// IDArray returns a Postgres int[] literal of n ids in [1, max].
func (r *Rand) IDArray(n, max int) string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprint(1 + r.Intn(max))
	}
	return "{" + strings.Join(ids, ",") + "}"
}

// Stats counts what the workload did.
type Stats struct {
	Issued, Errors, Dropped atomic.Int64
}

// Runner drives an app's mix. Each query has its own open-loop schedule: if the database slows down, work queues up and
// is dropped past a bound, as an application with timeouts would.
type Runner struct {
	DB      *sql.DB
	App     App
	Seed    uint64
	Stats   Stats
	perItem atomic.Bool
}

// SetNPlusOne switches the batched lookup to one query per item (a release that introduced an N+1 loop).
func (w *Runner) SetNPlusOne(on bool) { w.perItem.Store(on) }

// Run issues the mix until the context ends.
func (w *Runner) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i, q := range append(append([]Query{}, w.App.Mix...), w.App.Batch, w.App.PerItem) {
		wg.Add(1)
		go func(i int, q Query) {
			defer wg.Done()
			w.drive(ctx, q, w.Seed+uint64(i)*1000)
		}(i, q)
	}
	wg.Wait()
}

func (w *Runner) active(q Query) bool {
	switch q.Name {
	case w.App.Batch.Name:
		return !w.perItem.Load()
	case w.App.PerItem.Name:
		return w.perItem.Load()
	}
	return true
}

func (w *Runner) drive(ctx context.Context, q Query, seed uint64) {
	jobs := make(chan []any, q.Workers*4)
	var wg sync.WaitGroup
	for k := 0; k < q.Workers; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for args := range jobs {
				qctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				rows, err := w.DB.QueryContext(qctx, q.SQL, args...)
				if err == nil {
					for rows.Next() {
					}
					err = rows.Err()
					rows.Close()
				}
				cancel()
				w.Stats.Issued.Add(1)
				if err != nil && ctx.Err() == nil {
					w.Stats.Errors.Add(1)
				}
			}
		}()
	}
	r := NewRand(seed)
	t := time.NewTicker(time.Duration(float64(time.Second) / q.PerSec))
	defer func() {
		t.Stop()
		close(jobs)
		wg.Wait()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !w.active(q) {
				continue
			}
			select {
			case jobs <- q.Args(r):
			default:
				w.Stats.Dropped.Add(1)
			}
		}
	}
}

// Setup creates the schema, seeds it, lets the application read and write it, and lets the monitor read it (EXPLAIN
// plans a query, so it needs SELECT; the monitor gets nothing more).
func Setup(ctx context.Context, admin *sql.DB, app App, appRole, monitorRole string) error {
	for _, s := range append(append([]string{}, app.Schema...), app.Seed...) {
		if _, err := admin.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("%s: %w", firstLine(s), err)
		}
	}
	grants := []string{
		fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s", app.Name, appRole),
		fmt.Sprintf("GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA %s TO %s", app.Name, appRole),
		fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s", app.Name, monitorRole),
		fmt.Sprintf("GRANT SELECT ON ALL TABLES IN SCHEMA %s TO %s", app.Name, monitorRole),
	}
	for _, g := range grants {
		if _, err := admin.ExecContext(ctx, g); err != nil {
			return err
		}
	}
	return nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	return s
}
