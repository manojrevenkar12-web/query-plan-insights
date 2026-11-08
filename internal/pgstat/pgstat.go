// Package pgstat reads PostgreSQL's statistics views: the same sources Grafana Alloy's database_observability.postgres
// component uses (pg_stat_statements, pg_stat_activity, the catalog), plus settings and table statistics.
package pgstat

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Statement is one row of pg_stat_statements: cumulative counters for a normalised query.
type Statement struct {
	QueryID         int64
	Query           string
	Calls           int64
	TotalExecMs     float64
	Rows            int64
	SharedBlksHit   int64
	SharedBlksRead  int64
	TempBlksWritten int64
}

// Activity is one sampled backend from pg_stat_activity.
type Activity struct {
	PID           int
	QueryID       int64
	State         string
	WaitEventType string
	WaitEvent     string
	BlockedBy     []int
	Query         string
	XactSeconds   float64
}

// Index is one index definition from pg_indexes.
type Index struct {
	Schema, Table, Name, Def string
}

// TableStats is the part of pg_stat_user_tables that tells how stale planner statistics are.
type TableStats struct {
	Relation          string
	LiveTuples        int64
	ModSinceAnalyze   int64
	SeqScan, IdxScan  int64
	LastAnalyzeSecond float64
}

// Reader reads the views through one connection pool, as the monitoring user.
type Reader struct {
	DB *sql.DB
}

// Statements returns every statement not issued by the monitoring user itself.
func (r Reader) Statements(ctx context.Context) ([]Statement, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT queryid, query, calls, total_exec_time, rows, shared_blks_hit,
		shared_blks_read, temp_blks_written FROM pg_stat_statements s
		WHERE s.userid <> (SELECT oid FROM pg_roles WHERE rolname = current_user) AND s.dbid = (SELECT oid FROM pg_database
		WHERE datname = current_database()) AND queryid IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("pg_stat_statements: %w", err)
	}
	defer rows.Close()
	var out []Statement
	for rows.Next() {
		var s Statement
		if err := rows.Scan(&s.QueryID, &s.Query, &s.Calls, &s.TotalExecMs, &s.Rows, &s.SharedBlksHit, &s.SharedBlksRead,
			&s.TempBlksWritten); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Activity samples active backends of this database, with who blocks them.
func (r Reader) Activity(ctx context.Context) ([]Activity, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT pid, coalesce(query_id, 0), coalesce(state, ''), coalesce(wait_event_type, ''),
		coalesce(wait_event, ''), coalesce(array_to_string(pg_blocking_pids(pid), ','), ''), left(coalesce(query, ''), 300),
		coalesce(extract(epoch FROM clock_timestamp() - xact_start), 0)
		FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid() AND backend_type = 'client backend'
		AND usename <> current_user`)
	if err != nil {
		return nil, fmt.Errorf("pg_stat_activity: %w", err)
	}
	defer rows.Close()
	var out []Activity
	for rows.Next() {
		var a Activity
		var blockers string
		if err := rows.Scan(&a.PID, &a.QueryID, &a.State, &a.WaitEventType, &a.WaitEvent, &blockers, &a.Query, &a.XactSeconds); err != nil {
			return nil, err
		}
		for _, p := range strings.Split(blockers, ",") {
			var pid int
			if _, err := fmt.Sscan(p, &pid); err == nil {
				a.BlockedBy = append(a.BlockedBy, pid)
			}
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Indexes returns every user index.
func (r Reader) Indexes(ctx context.Context) ([]Index, error) {
	// indexdef is NULL for an index dropped between the catalog scan and pg_get_indexdef
	rows, err := r.DB.QueryContext(ctx, `SELECT schemaname, tablename, indexname, indexdef FROM pg_indexes
		WHERE schemaname NOT IN ('pg_catalog', 'information_schema') AND indexdef IS NOT NULL ORDER BY 1, 2, 3`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Index
	for rows.Next() {
		var i Index
		if err := rows.Scan(&i.Schema, &i.Table, &i.Name, &i.Def); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// PlannerSettings are the settings that change plans or execution memory.
var PlannerSettings = []string{"work_mem", "random_page_cost", "seq_page_cost", "effective_cache_size", "enable_indexscan",
	"enable_bitmapscan", "enable_seqscan", "enable_hashjoin", "enable_nestloop", "enable_mergejoin", "jit", "default_statistics_target"}

// Settings returns the planner settings as the database sees them now (including role and database overrides of the
// workload's role, when given).
func (r Reader) Settings(ctx context.Context, role string) (map[string]string, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT name, setting FROM pg_settings WHERE name = ANY($1)`, pq.Array(PlannerSettings))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if role != "" {
		// per-role and per-database overrides (ALTER ROLE ... SET, ALTER DATABASE ... SET)
		ov, err := r.DB.QueryContext(ctx, `SELECT unnest(setconfig) FROM pg_db_role_setting s
			LEFT JOIN pg_roles r ON r.oid = s.setrole LEFT JOIN pg_database d ON d.oid = s.setdatabase
			WHERE (r.rolname = $1 OR s.setrole = 0) AND (d.datname = current_database() OR s.setdatabase = 0)
			ORDER BY s.setrole, s.setdatabase`, role)
		if err != nil {
			return nil, err
		}
		defer ov.Close()
		for ov.Next() {
			var kv string
			if err := ov.Scan(&kv); err != nil {
				return nil, err
			}
			if k, v, ok := strings.Cut(kv, "="); ok {
				for _, s := range PlannerSettings {
					if s == k {
						out[k] = v
					}
				}
			}
		}
		if err := ov.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Tables returns planner-statistics freshness per user table.
func (r Reader) Tables(ctx context.Context) ([]TableStats, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT schemaname || '.' || relname, n_live_tup, n_mod_since_analyze, seq_scan,
		coalesce(idx_scan, 0), coalesce(extract(epoch FROM clock_timestamp() - greatest(last_analyze, last_autoanalyze)), -1)
		FROM pg_stat_user_tables`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TableStats
	for rows.Next() {
		var t TableStats
		if err := rows.Scan(&t.Relation, &t.LiveTuples, &t.ModSinceAnalyze, &t.SeqScan, &t.IdxScan, &t.LastAnalyzeSecond); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Explain returns the generic plan (PostgreSQL 16+) of a normalised statement, as JSON. EXPLAIN without ANALYZE never
// executes the statement, so it is safe for writes too; it runs in a read-only transaction anyway.
func (r Reader) Explain(ctx context.Context, query string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tx, err := r.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // read-only, nothing to keep
	var out string
	err = tx.QueryRowContext(ctx, "EXPLAIN (GENERIC_PLAN, VERBOSE, FORMAT JSON) "+query).Scan(&out)
	if err != nil {
		return nil, err
	}
	return []byte(out), nil
}

// Explainable is true for reads. The monitoring role holds pg_read_all_data, not write privileges, and EXPLAIN checks
// the same privileges as running the statement.
func Explainable(q string) bool {
	s := strings.ToUpper(strings.TrimSpace(q))
	for _, p := range []string{"SELECT", "WITH"} {
		if strings.HasPrefix(s, p) {
			return !strings.Contains(s, "PG_STAT_") && !strings.Contains(s, "PG_CATALOG")
		}
	}
	return false
}
