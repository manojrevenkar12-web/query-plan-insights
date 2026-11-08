// Command qpi-eval runs the fault-injection evaluation against a real PostgreSQL and writes the reports.
//
//	qpi-eval -set dev                 # development scenarios (tuned on)
//	qpi-eval -set dev -only drop_index
//	qpi-eval -pin                     # pin the held-out scenarios, once, before any tuning
//	qpi-eval -set heldout             # the held-out scenarios, run once
//	qpi-eval -overhead                # collector overhead
//	qpi-eval -report                  # reports/results.json, reports/RESULTS.md and the README block
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/manojrevenkar12-web/query-plan-insights/internal/config"
	"github.com/manojrevenkar12-web/query-plan-insights/internal/eval"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	set := flag.String("set", "", "scenario set to run: dev or heldout")
	only := flag.String("only", "", "run only this scenario")
	pin := flag.Bool("pin", false, "write config/heldout.sha256 from the held-out scenarios")
	again := flag.Bool("again", false, "allow another held-out run (it is recorded)")
	overhead := flag.Bool("overhead", false, "measure collector overhead")
	report := flag.Bool("report", false, "write reports/results.json, reports/RESULTS.md and the README results block")
	flag.Parse()

	root, err := config.Root()
	fail(err)
	cfg, err := config.Load(filepath.Join(root, "config", "qpi.json"))
	fail(err)
	spec, err := eval.LoadSpec(root)
	fail(err)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	e := eval.Env{Cfg: cfg, DSN: eval.DSNs{
		Admin:       env("QPI_ADMIN_DSN", "postgres://qpi:qpi@localhost:5432/qpi?sslmode=disable"),
		App:         env("QPI_APP_DSN", "postgres://qpi_app:qpi_app@localhost:5432/qpi?sslmode=disable"),
		Monitor:     env("QPI_MONITOR_DSN", "postgres://qpi_monitor:qpi_monitor@localhost:5432/qpi?sslmode=disable"),
		AppRole:     env("QPI_APP_ROLE", "qpi_app"),
		MonitorRole: env("QPI_MONITOR_ROLE", "qpi_monitor"),
	}}
	reports := filepath.Join(root, "reports")
	fail(os.MkdirAll(filepath.Join(reports, "snapshots"), 0o755))

	switch {
	case *pin:
		path := filepath.Join(root, "config", "heldout.sha256")
		if _, err := os.Stat(path); err == nil {
			fail(fmt.Errorf("%s exists: the held-out set is pinned once", path))
		}
		fail(os.WriteFile(path, []byte(spec.Pin()+"\n"), 0o644))
		fmt.Println("pinned", spec.Pin())
	case *overhead:
		o, err := eval.MeasureOverhead(ctx, e)
		fail(err)
		fail(writeJSON(filepath.Join(reports, "overhead.json"), o))
		fmt.Printf("overhead %.2f%% (off %.0f qps, on %.0f qps), tick %.2f ms\n", o.OverheadPct, o.OffMean, o.OnMean, o.TickMeanMs)
	case *report:
		fail(writeReport(root))
	case *set == "dev" || *set == "heldout":
		scenarios := spec.Dev
		if *set == "heldout" {
			fail(eval.CheckPin(root, spec))
			if *only != "" {
				fail(fmt.Errorf("the held-out set runs whole"))
			}
			runs := filepath.Join(reports, "heldout-runs.log")
			if b, err := os.ReadFile(runs); err == nil && strings.TrimSpace(string(b)) != "" && !*again {
				fail(fmt.Errorf("the held-out set has already run (%s); pass -again to run it again, which is recorded", runs))
			}
			f, err := os.OpenFile(runs, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fail(err)
			fmt.Fprintf(f, "%s pin=%s\n", time.Now().UTC().Format(time.RFC3339), spec.Pin())
			f.Close()
			scenarios = spec.Heldout
		}
		fail(runSet(ctx, e, *set, scenarios, *only, reports))
	default:
		flag.Usage()
		os.Exit(2)
	}
}

// SetReport is one set's results file.
type SetReport struct {
	Set         string          `json:"set"`
	GeneratedAt time.Time       `json:"generatedAt"`
	HeldoutPin  string          `json:"heldoutPin,omitempty"`
	Detector    config.Detector `json:"detector"`
	Eval        config.Eval     `json:"eval"`
	Summary     eval.Summary    `json:"summary"`
	Outcomes    []eval.Outcome  `json:"outcomes"`
}

func runSet(ctx context.Context, e eval.Env, set string, scenarios []eval.Scenario, only, reports string) error {
	path := filepath.Join(reports, "eval-"+set+".json")
	var prev SetReport
	if only != "" {
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &prev)
		}
	}
	var outcomes []eval.Outcome
	ran := false
	for _, sc := range scenarios {
		if only != "" && sc.Name != only {
			for _, o := range prev.Outcomes {
				if o.Scenario == sc.Name {
					outcomes = append(outcomes, o)
				}
			}
			continue
		}
		ran = true
		fmt.Printf("%s/%s: %s\n", set, sc.Name, sc.About)
		logPath := filepath.Join(reports, "snapshots", set+"-"+sc.Name+".logfmt")
		lf, err := os.Create(logPath)
		if err != nil {
			return err
		}
		e.Log = io.MultiWriter(lf, prefixed{"  | "})
		rec, err := eval.Run(ctx, e, sc)
		lf.Close()
		o := eval.Score(set, rec)
		if err != nil {
			o.Error = err.Error()
			fmt.Println("  error:", err)
			if ctx.Err() != nil {
				return err
			}
		}
		outcomes = append(outcomes, o)
		fmt.Printf("  detected=%v in %.1fs cause=%s (%v) suggestion=%v resolved=%v in %.1fs false_alarms=%v collateral=%v\n",
			o.Detected, o.DetectSeconds, o.CauseAtOpen, o.CauseCorrect, o.SuggestionCorrect, o.Resolved, o.ResolveSeconds,
			o.FalseAlarms, o.CollateralFound)
		if err := writeJSON(filepath.Join(reports, "snapshots", set+"-"+sc.Name+".json"), map[string]any{
			"set": set, "scenario": sc, "timeline": rec.Timeline, "outcome": o, "names": rec.Names, "holderPid": rec.HolderPID,
			"snapshot": rec.Snapshot,
		}); err != nil {
			return err
		}
	}
	if !ran {
		return fmt.Errorf("no scenario named %q in %s", only, set)
	}
	r := SetReport{Set: set, GeneratedAt: time.Now().UTC(), Detector: e.Cfg.Detector, Eval: e.Cfg.Eval,
		Summary: eval.Summarise(outcomes), Outcomes: outcomes}
	if set == "heldout" {
		spec, err := eval.LoadSpec(filepath.Dir(reports))
		if err == nil {
			r.HeldoutPin = spec.Pin()
		}
	}
	s := r.Summary
	fmt.Printf("%s: detected %d/%d, cause %d/%d, suggestion %d/%d, resolved %d/%d, false alarms %d, median detect %.1fs\n",
		set, s.Detected, s.Faults, s.CauseCorrect, s.Faults, s.SuggestionCorrect, s.Faults, s.Resolved, s.Faults, s.FalseAlarms,
		s.MedianDetectS)
	return writeJSON(path, r)
}

type prefixed struct{ p string }

func (w prefixed) Write(b []byte) (int, error) {
	for _, line := range strings.SplitAfter(string(b), "\n") {
		if line != "" {
			fmt.Print(w.p + line)
		}
	}
	return len(b), nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "qpi-eval:", err)
		os.Exit(1)
	}
}
