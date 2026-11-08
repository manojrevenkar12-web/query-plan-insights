package eval

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"

	"github.com/manojrevenkar12-web/query-plan-insights/internal/api"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/collector"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/config"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/pgstat"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/store"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/workload"
)

// DSNs are the three roles: the admin injects faults, the application runs the workload, the monitor collects.
type DSNs struct {
	Admin, App, Monitor  string
	AppRole, MonitorRole string
}

// Env is what every scenario shares.
type Env struct {
	Cfg config.Config
	DSN DSNs
	Log io.Writer // the collector's logfmt stream
}

// Timeline marks when the fault and the fix went in.
type Timeline struct {
	Start   time.Time `json:"start"`
	FaultAt time.Time `json:"faultAt"`
	FixAt   time.Time `json:"fixAt"`
	End     time.Time `json:"end"`
}

// Recording is everything one scenario run produced.
type Recording struct {
	Scenario  Scenario
	Timeline  Timeline
	Names     map[int64]string // query id -> workload query name
	Insights  []store.Insight
	HolderPID int
	Workload  WorkloadStats
	Snapshot  api.Snapshot
}

// WorkloadStats is what the application saw.
type WorkloadStats struct {
	Issued  int64 `json:"issued"`
	Errors  int64 `json:"errors"`
	Dropped int64 `json:"dropped"`
}

// App returns the workload application by name.
func App(name string) workload.App {
	if name == "billing" {
		return workload.Billing()
	}
	return workload.Shop()
}

func open(dsn string, maxConns int) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	return db, db.Ping()
}

// Reset puts the server back to the evaluation's starting settings, with no role overrides.
func Reset(ctx context.Context, admin *sql.DB, cfg config.Eval, appRole string) error {
	stmts := []string{"ALTER SYSTEM RESET ALL", "ALTER ROLE " + appRole + " RESET ALL"}
	for k, v := range cfg.ServerSettings {
		stmts = append(stmts, fmt.Sprintf("ALTER SYSTEM SET %s = '%s'", k, v))
	}
	stmts = append(stmts, "SELECT pg_reload_conf()")
	for _, s := range stmts {
		if _, err := admin.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// holder runs a lock-holding session until stopped.
type holder struct {
	pid  int
	stop chan struct{}
	done chan struct{}
	err  error
}

func startHolder(ctx context.Context, admin *sql.DB, h Holder) (*holder, error) {
	conn, err := admin.Conn(ctx)
	if err != nil {
		return nil, err
	}
	x := &holder{stop: make(chan struct{}), done: make(chan struct{})}
	if err := conn.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&x.pid); err != nil {
		conn.Close()
		return nil, err
	}
	go func() {
		defer close(x.done)
		defer conn.Close()
		wait := func(ms int) bool {
			select {
			case <-x.stop:
				return false
			case <-ctx.Done():
				return false
			case <-time.After(time.Duration(ms) * time.Millisecond):
				return true
			}
		}
		for {
			tx, err := conn.BeginTx(ctx, nil)
			if err != nil {
				x.err = err
				return
			}
			rows, err := tx.QueryContext(ctx, h.SQL)
			if err == nil {
				for rows.Next() {
				}
				rows.Close()
			}
			more := err == nil && wait(h.HoldMillis)
			if err := tx.Commit(); err != nil && x.err == nil && ctx.Err() == nil {
				x.err = err
			}
			if !more || !wait(h.PauseMillis) {
				return
			}
		}
	}()
	return x, nil
}

func (h *holder) Stop() error {
	close(h.stop)
	<-h.done
	return h.err
}

var (
	space    = regexp.MustCompile(`\s+`)
	constant = regexp.MustCompile(`\$\d+|'[^']*'|\b\d+(\.\d+)?\b`)
)

// normal compares statements the way pg_stat_statements does: constants and parameters are the same placeholder.
func normal(s string) string {
	return strings.TrimSpace(space.ReplaceAllString(constant.ReplaceAllString(s, "?"), " "))
}

// names maps the store's query ids to the workload's query names by their text.
func names(st *store.Store, app workload.App) map[int64]string {
	byText := map[string]string{}
	for _, q := range append(append([]workload.Query{}, app.Mix...), app.Batch, app.PerItem) {
		byText[normal(q.SQL)] = q.Name
	}
	out := map[int64]string{}
	for _, id := range st.QueryIDs() {
		q, _ := st.Query(id)
		if n, ok := byText[normal(q.Text)]; ok {
			out[id] = n
		}
	}
	return out
}

// Run plays one scenario: fresh schema and statistics, a baseline, the fault, the fix.
func Run(ctx context.Context, env Env, sc Scenario) (rec Recording, err error) {
	cfg := env.Cfg
	rec.Scenario = sc
	app := App(sc.App)
	admin, err := open(env.DSN.Admin, 4)
	if err != nil {
		return rec, fmt.Errorf("admin: %w", err)
	}
	defer admin.Close()
	if err := Reset(ctx, admin, cfg.Eval, env.DSN.AppRole); err != nil {
		return rec, err
	}
	defer func() {
		if rerr := Reset(context.Background(), admin, cfg.Eval, env.DSN.AppRole); rerr != nil && err == nil {
			err = rerr
		}
	}()
	if err := workload.Setup(ctx, admin, app, env.DSN.AppRole, env.DSN.MonitorRole); err != nil {
		return rec, fmt.Errorf("setup %s: %w", app.Name, err)
	}
	if _, err := admin.ExecContext(ctx, "SELECT pg_stat_statements_reset()"); err != nil {
		return rec, err
	}
	workers := 0
	for _, q := range append(append([]workload.Query{}, app.Mix...), app.Batch, app.PerItem) {
		workers += q.Workers
	}
	appDB, err := open(env.DSN.App, workers)
	if err != nil {
		return rec, fmt.Errorf("app: %w", err)
	}
	defer appDB.Close()
	appDB.SetConnMaxLifetime(time.Duration(cfg.Eval.ConnMaxLifetimeMillis) * time.Millisecond)
	mon, err := open(env.DSN.Monitor, 3)
	if err != nil {
		return rec, fmt.Errorf("monitor: %w", err)
	}
	defer mon.Close()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	start := time.Now()
	st := store.New(time.Duration(cfg.Collector.RetentionSeconds)*time.Second, start)
	col := collector.New(cfg, pgstat.Reader{DB: mon}, st, env.DSN.AppRole, env.Log)
	runner := &workload.Runner{DB: appDB, App: app, Seed: cfg.Eval.Seed}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); runner.Run(runCtx) }()
	go func() { defer wg.Done(); _ = col.Run(runCtx) }()
	stopAll := func() {
		cancel()
		wg.Wait()
	}

	rec.Timeline.Start = start
	if err := sleep(ctx, time.Duration(cfg.Eval.BaselineSeconds)*time.Second); err != nil {
		stopAll()
		return rec, err
	}
	var h *holder
	rec.Timeline.FaultAt = time.Now()
	if sc.Fault != nil {
		if h, err = apply(ctx, admin, runner, *sc.Fault, nil); err != nil {
			stopAll()
			return rec, fmt.Errorf("fault: %w", err)
		}
		if h != nil {
			rec.HolderPID = h.pid
		}
	}
	if err := sleep(ctx, time.Duration(cfg.Eval.FaultSeconds)*time.Second); err != nil {
		stopAll()
		return rec, err
	}
	rec.Timeline.FixAt = time.Now()
	if sc.Fix != nil {
		if _, err = apply(ctx, admin, runner, *sc.Fix, h); err != nil {
			stopAll()
			return rec, fmt.Errorf("fix: %w", err)
		}
	}
	if err := sleep(ctx, time.Duration(cfg.Eval.FixSeconds)*time.Second); err != nil {
		stopAll()
		return rec, err
	}
	stopAll()
	rec.Timeline.End = time.Now()
	rec.Names = names(st, app)
	rec.Insights = st.Insights()
	rec.Workload = WorkloadStats{runner.Stats.Issued.Load(), runner.Stats.Errors.Load(), runner.Stats.Dropped.Load()}
	end := rec.Timeline.End
	rec.Snapshot = api.View{Cfg: cfg, Store: st, Now: func() time.Time { return end }, OverviewRows: cfg.API.OverviewRows,
		WindowSeconds: int(end.Sub(start).Seconds()) + 1}.Snapshot()
	return rec, nil
}

func apply(ctx context.Context, admin *sql.DB, runner *workload.Runner, a Action, running *holder) (*holder, error) {
	for _, s := range a.SQL {
		if _, err := admin.ExecContext(ctx, s); err != nil {
			return nil, fmt.Errorf("%s: %w", s, err)
		}
	}
	if a.NPlusOne != nil {
		runner.SetNPlusOne(*a.NPlusOne)
	}
	if a.StopLockHolder && running != nil {
		if err := running.Stop(); err != nil {
			return nil, fmt.Errorf("lock holder: %w", err)
		}
	}
	if a.LockHolder != nil {
		return startHolder(ctx, admin, *a.LockHolder)
	}
	return nil, nil
}
