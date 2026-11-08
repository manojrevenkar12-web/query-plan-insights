package eval

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manojrevenkar12-web/query-plan-insights/internal/config"
)

func dsn(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// TestDropIndexEndToEnd runs one real scenario on PostgreSQL: the collector must notice the dropped index, blame it,
// suggest recreating it, and confirm the fix. It needs the database from scripts/setup-db.sql and is skipped by -short.
func TestDropIndexEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("needs PostgreSQL with pg_stat_statements; run without -short")
	}
	env := Env{DSN: DSNs{
		Admin:   dsn("QPI_ADMIN_DSN", "postgres://qpi:qpi@localhost:5432/qpi?sslmode=disable"),
		App:     dsn("QPI_APP_DSN", "postgres://qpi_app:qpi_app@localhost:5432/qpi?sslmode=disable"),
		Monitor: dsn("QPI_MONITOR_DSN", "postgres://qpi_monitor:qpi_monitor@localhost:5432/qpi?sslmode=disable"),
		AppRole: "qpi_app", MonitorRole: "qpi_monitor",
	}, Log: io.Discard}
	if db, err := sql.Open("postgres", env.DSN.Admin); err != nil || db.Ping() != nil {
		t.Skip("no PostgreSQL at QPI_ADMIN_DSN")
	}
	root, _ := filepath.Abs("../..")
	cfg, err := config.Load(filepath.Join(root, "config", "qpi.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Eval.BaselineSeconds, cfg.Eval.FaultSeconds, cfg.Eval.FixSeconds = 18, 12, 12
	env.Cfg = cfg
	spec, err := LoadSpec(root)
	if err != nil {
		t.Fatal(err)
	}
	var sc Scenario
	for _, s := range spec.Dev {
		if s.Name == "drop_index" {
			sc = s
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	rec, err := Run(ctx, env, sc)
	if err != nil {
		t.Fatal(err)
	}
	o := Score("dev", rec)
	if !o.Detected || !o.CauseCorrect || !o.SuggestionCorrect || !o.Resolved {
		t.Fatalf("outcome %+v", o)
	}
	if o.DetectSeconds > 8 {
		t.Fatalf("took %.1f s to notice", o.DetectSeconds)
	}
	if len(rec.Snapshot.Queries) == 0 || len(rec.Snapshot.Overview.Events) < 2 {
		t.Fatalf("the snapshot lacks queries or the drop and create events: %+v", rec.Snapshot.Overview.Events)
	}
}
