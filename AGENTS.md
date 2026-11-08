# Working on query-plan-insights

## Fast feedback

| What | Command | Time |
|---|---|---|
| Format, vet, types, lint | `make check` | ~10 s |
| Unit tests, Go and web, no database | `make test` | ~15 s |
| One real scenario on PostgreSQL | `make test-db` | ~1 min |
| Development scenarios | `make eval` | ~8 min |

Run `make check test` before every commit. `make test-db` needs the database from `make db`.

## Invariants

1. **The monitor only reads.** The collector connects as a role with `pg_monitor` and `SELECT`, runs `EXPLAIN`
   (never `EXPLAIN ANALYZE`) with `GENERIC_PLAN` inside a `READ ONLY` transaction with a statement timeout, and only
   for `SELECT`/`WITH` statements. Do not add a code path that executes application SQL.
2. **Every insight has a reason.** An insight carries a cause code, a confidence, the evidence lines the cause was
   chosen from and, where one exists, the SQL that fixes it. A new cause needs a test in `internal/diagnose` that
   shows the evidence that selects it and the evidence that does not.
3. **Settings in config.** Every threshold, window and rate lives in `config/qpi.json` and is loaded strictly
   (unknown keys fail). The web app's sizes, colours, timings and breakpoints live in `web/src/theme/tokens.ts`.
4. **Generated reports.** `reports/results.json`, `reports/RESULTS.md` and the README block between the
   `results:start` and `results:end` markers are written by `qpi-eval -report`. Never edit them by hand.
5. **Query ids are strings in JSON.** `queryid` is a 64-bit hash; JavaScript numbers lose its last digits.
6. **Every screen has loading, error-with-retry and empty states**, and respects reduced motion.

## The held-out rule

`config/scenarios.json` has a development set (the `shop` app) and a held-out set (the `billing` app: other tables,
other faults). The held-out set was pinned in `config/heldout.sha256` before any tuning, and `qpi-eval -set heldout`
refuses to run if it changed. It runs once; a second run needs `-again` and is logged in `reports/heldout-runs.log`,
which the README reports. Tune detector and diagnosis settings on the development set only. If a held-out scenario
fails, say so in the results; do not change the held-out set or tune until it passes.

## Layout

- `internal/pgstat`: reads pg_stat_statements, pg_stat_activity, catalog, settings, EXPLAIN.
- `internal/collector`: one tick per second: deltas, events, plans, detection, diagnosis, resolution.
- `internal/detect`: windows and the regression tracker. `internal/diagnose`: ranked causes and fixes.
- `internal/plan`: plan parsing, fingerprints and diffs. `internal/store`: in-memory state with retention.
- `internal/api`: JSON views and `/metrics`. `internal/workload`, `internal/eval`: the fault-injection evaluation.
- `cmd/qpi`: the server. `cmd/qpi-eval`: the evaluation and the reports. `web/`: React app, feature-first.
