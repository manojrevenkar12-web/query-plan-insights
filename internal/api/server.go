package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/manojrevenkar12-web/query-plan-insights/internal/store"
)

// Stats is what the collector reports about itself.
type Stats func() (ticks int, collect time.Duration)

// Handler serves the JSON API, /metrics and, if static is set, the built web app.
func Handler(v View, stats Stats, static http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/overview", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, v.Overview()) })
	mux.HandleFunc("GET /api/insights", func(w http.ResponseWriter, r *http.Request) {
		out := v.Insights()
		if s := r.URL.Query().Get("status"); s != "" {
			kept := out[:0]
			for _, in := range out {
				if strings.EqualFold(in.Status, s) {
					kept = append(kept, in)
				}
			}
			out = kept
		}
		if out == nil {
			out = []InsightView{}
		}
		writeJSON(w, out)
	})
	mux.HandleFunc("GET /api/insights/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			http.Error(w, "insight id must be a number", http.StatusBadRequest)
			return
		}
		for _, in := range v.Insights() {
			if in.ID == id {
				writeJSON(w, in)
				return
			}
		}
		http.Error(w, "no such insight", http.StatusNotFound)
	})
	mux.HandleFunc("GET /api/queries/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "query id must be a number", http.StatusBadRequest)
			return
		}
		d, ok := v.Query(id)
		if !ok {
			http.Error(w, "no such query", http.StatusNotFound)
			return
		}
		writeJSON(w, d)
	})
	mux.HandleFunc("GET /api/snapshot", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, v.Snapshot()) })
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		ticks, collect := stats()
		WriteMetrics(w, v.Store.Insights(), ticks, collect)
	})
	if static != nil {
		mux.Handle("/", static)
	}
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

// WriteMetrics writes the Prometheus text exposition for insights and the collector.
func WriteMetrics(w interface{ Write([]byte) (int, error) }, insights []store.Insight, ticks int, collect time.Duration) {
	type key struct{ kind, cause, status string }
	counts := map[key]int{}
	impact := 0.0
	for _, in := range insights {
		counts[key{in.Kind, in.CauseAtOpen, in.Status}]++
		if in.Status == "OPEN" {
			impact += in.ImpactMsPerS
		}
	}
	keys := make([]key, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		return a.kind+a.cause+a.status < b.kind+b.cause+b.status
	})
	fmt.Fprintln(w, "# HELP qpi_insights Insights by kind, cause at open and status.")
	fmt.Fprintln(w, "# TYPE qpi_insights gauge")
	for _, k := range keys {
		fmt.Fprintf(w, "qpi_insights{kind=%q,cause=%q,status=%q} %d\n", k.kind, k.cause, k.status, counts[k])
	}
	fmt.Fprintln(w, "# HELP qpi_open_impact_ms_per_second Extra database time per second caused by open regressions.")
	fmt.Fprintln(w, "# TYPE qpi_open_impact_ms_per_second gauge")
	fmt.Fprintf(w, "qpi_open_impact_ms_per_second %.3f\n", impact)
	fmt.Fprintln(w, "# HELP qpi_collector_ticks_total Collection intervals run.")
	fmt.Fprintln(w, "# TYPE qpi_collector_ticks_total counter")
	fmt.Fprintf(w, "qpi_collector_ticks_total %d\n", ticks)
	fmt.Fprintln(w, "# HELP qpi_collector_tick_seconds_total Time spent collecting.")
	fmt.Fprintln(w, "# TYPE qpi_collector_tick_seconds_total counter")
	fmt.Fprintf(w, "qpi_collector_tick_seconds_total %.6f\n", collect.Seconds())
}
