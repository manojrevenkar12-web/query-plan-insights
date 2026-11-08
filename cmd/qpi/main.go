// Command qpi watches one PostgreSQL database and serves what it finds.
//
//	qpi -dsn postgres://qpi_monitor:...@db:5432/app -role app_user -web web/dist
//
// It logs in logfmt (for Loki), exposes /metrics (for Prometheus) and serves the JSON API and the web app. The
// evaluation's recordings in reports/ are served under /api/evaluation and /api/scenarios, so the web app can replay
// them without a database.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"github.com/manojrevenkar12-web/query-plan-insights/internal/api"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/collector"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/config"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/pgstat"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/store"
)

var scenarioName = regexp.MustCompile(`^(dev|heldout)-[a-z0-9_]+$`)

func main() {
	root, _ := config.Root()
	cfgPath := flag.String("config", filepath.Join(root, "config", "qpi.json"), "settings file")
	dsn := flag.String("dsn", os.Getenv("QPI_MONITOR_DSN"), "monitoring connection string (read-only role with pg_monitor)")
	role := flag.String("role", os.Getenv("QPI_APP_ROLE"), "application role, to see its ALTER ROLE ... SET overrides")
	web := flag.String("web", filepath.Join(root, "web", "dist"), "built web app to serve (empty to serve none)")
	reports := flag.String("reports", filepath.Join(root, "reports"), "evaluation reports to serve")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	fail(err)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/evaluation", func(w http.ResponseWriter, r *http.Request) {
		serveFile(w, r, filepath.Join(*reports, "results.json"))
	})
	mux.HandleFunc("GET /api/scenarios/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !scenarioName.MatchString(name) {
			http.Error(w, "unknown scenario", http.StatusNotFound)
			return
		}
		serveFile(w, r, filepath.Join(*reports, "snapshots", name+".json"))
	})
	var static http.Handler
	if *web != "" {
		if _, err := os.Stat(filepath.Join(*web, "index.html")); err == nil {
			static = spa(*web)
		}
	}

	stats := func() (int, time.Duration) { return 0, 0 }
	var view api.View
	if *dsn != "" {
		db, err := sql.Open("postgres", *dsn)
		fail(err)
		db.SetMaxOpenConns(3)
		fail(db.PingContext(ctx))
		st := store.New(time.Duration(cfg.Collector.RetentionSeconds)*time.Second, time.Now())
		col := collector.New(cfg, pgstat.Reader{DB: db}, st, *role, os.Stdout)
		go func() { _ = col.Run(ctx) }()
		stats = col.Stats
		view = api.View{Cfg: cfg, Store: st, Now: time.Now, OverviewRows: cfg.API.OverviewRows, WindowSeconds: cfg.API.WindowSeconds}
	} else {
		fmt.Println(`level=info msg="no -dsn: serving the evaluation recordings only"`)
		view = api.View{Cfg: cfg, Store: store.New(time.Minute, time.Now()), Now: time.Now, OverviewRows: cfg.API.OverviewRows,
			WindowSeconds: cfg.API.WindowSeconds}
	}
	mux.Handle("/", api.Handler(view, stats, static))

	srv := &http.Server{Addr: cfg.API.Listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	fmt.Printf("level=info msg=\"listening\" addr=%s\n", cfg.API.Listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fail(err)
	}
}

func serveFile(w http.ResponseWriter, r *http.Request, path string) {
	if _, err := os.Stat(path); err != nil {
		http.Error(w, "not recorded yet: run qpi-eval", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	http.ServeFile(w, r, path)
}

// spa serves the built app, and index.html for client-side routes.
func spa(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := os.Stat(filepath.Join(dir, filepath.Clean("/"+r.URL.Path))); err != nil {
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		files.ServeHTTP(w, r)
	})
}

func fail(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "level=error msg=%q\n", err.Error())
		os.Exit(1)
	}
}
