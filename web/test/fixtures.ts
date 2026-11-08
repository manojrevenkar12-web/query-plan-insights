import type { Insight, Overview, QueryDetail, Results, SeriesPoint } from "../src/shared/api/types";
import type { Source } from "../src/features/source/source";

const t = (s: number) => new Date(Date.UTC(2026, 10, 8, 12, 0, s)).toISOString();

export const series: SeriesPoint[] = Array.from({ length: 40 }, (_, i) => ({
  at: t(i),
  calls: 25,
  meanMs: i >= 20 && i < 32 ? 20 : 0.08,
  rowsPerCall: 10,
  readPerCall: 0,
  tempPerCall: 0,
  lockShare: 0,
}));

export const insight: Insight = {
  id: 1,
  kind: "LATENCY_REGRESSION",
  signal: "latency",
  queryId: "-5603298469950051314",
  query: "SELECT id, total, status FROM shop.orders WHERE customer_id = $1 ORDER BY created_at DESC LIMIT $2",
  status: "OPEN",
  openedAt: t(23),
  suspectedAt: t(20),
  baselineMs: 0.08,
  currentMs: 19.3,
  worstMs: 21.5,
  callsPerSec: 25,
  impactMsPerSec: 480.5,
  cause: { code: "MISSING_INDEX", confidence: 0.95, summary: "index orders_customer_idx, which the plan used, was dropped" },
  causeAtOpen: "MISSING_INDEX",
  evidence: ["mean time 0.08 ms -> 19.3 ms (241x) over 75 calls", "index orders_customer_idx on shop.orders dropped at 12:00:19"],
  planChanges: [{ kind: "access", relation: "shop.orders", before: "Bitmap Heap Scan", after: "Seq Scan" }],
  planBefore:
    "Limit  (rows=1 cost=27.7)\n  Sort  (rows=6 cost=27.7)\n    Bitmap Heap Scan on orders  (rows=6 cost=27.6)\n      Bitmap Index Scan using orders_customer_idx  (rows=6 cost=4.5)\n",
  planAfter: "Limit  (rows=1 cost=5497.1)\n  Gather Merge  (rows=4 cost=5497.4)\n    Sort  (rows=4 cost=4496.9)\n      Seq Scan on orders  (rows=4 cost=4496.9)\n",
  suggestion: { text: "Recreate the index without blocking writes.", sql: "CREATE INDEX CONCURRENTLY orders_customer_idx ON shop.orders USING btree (customer_id);" },
};

export const resolved: Insight = {
  ...insight,
  id: 2,
  queryId: "42",
  query: "SELECT name FROM shop.products WHERE id = $1",
  status: "RESOLVED",
  cause: { code: "LOCK_CONTENTION", confidence: 0.9, summary: "80% of the time the query waits for a lock" },
  resolvedAt: t(38),
  resolvedMs: 0.09,
  impactMsPerSec: 0,
  verification: "back to 0.09 ms from a worst of 21.50 ms (baseline 0.08 ms)",
};

export const overview: Overview = {
  startedAt: t(0),
  now: t(39),
  database: series,
  openInsights: 1,
  resolvedInsights: 1,
  impactMsPerSec: 480.5,
  trackedQueries: 2,
  windowSeconds: 300,
  events: [{ at: t(19), kind: "INDEX_DROPPED", relation: "shop.orders", name: "orders_customer_idx" }],
  queries: [
    { id: insight.queryId, text: insight.query, callsPerSec: 25, meanMs: 19.3, loadMsPerSec: 150, spark: series.map((p) => p.meanMs), insightId: 1, status: "REGRESSED" },
    { id: "42", text: resolved.query, callsPerSec: 80, meanMs: 0.02, loadMsPerSec: 1.6, spark: [0.02, 0.02], status: "OK" },
  ],
};

export const detail: QueryDetail = {
  id: insight.queryId,
  text: insight.query,
  firstSeen: t(0),
  series,
  plans: [{ at: t(1), fingerprint: "7db9a5975dbf", tree: insight.planBefore! }],
  insights: [insight],
};

export function fakeSource(over: Partial<Source> = {}): Source {
  return {
    id: "dev-drop_index",
    label: "drop index",
    live: false,
    names: { [insight.queryId]: "customer_orders" },
    timeline: { start: t(0), faultAt: t(19), fixAt: t(32), end: t(39) },
    overview: async () => overview,
    insights: async () => [insight, resolved],
    query: async () => detail,
    ...over,
  };
}

export const results: Results = {
  generatedAt: t(0),
  heldoutPin: "79f650126851d9dacff5493b570c8cd922d97d41b9a90fc0b7758f6f649c469e",
  heldoutRuns: 1,
  overhead: { rounds: 3, secondsPerPhase: 8, workers: 2, offMeanQps: 9000, onMeanQps: 8900, overheadPct: 1.1, tickMeanMs: 6.5 },
  dev: {
    set: "dev",
    generatedAt: t(0),
    summary: { scenarios: 2, faults: 1, controls: 1, detected: 1, causeCorrect: 1, finalCauseCorrect: 1, suggestionCorrect: 1, resolved: 1, falseAlarms: 0, medianDetectSeconds: 3, maxDetectSeconds: 3, medianResolveSeconds: 6, errors: 0 },
    outcomes: [
      { set: "dev", scenario: "drop_index", about: "A migration drops an index.", app: "shop", control: false, target: "customer_orders", wantCause: "MISSING_INDEX", detected: true, detectSeconds: 3, causeAtOpen: "MISSING_INDEX", causeCorrect: true, suggestionCorrect: true, resolved: true, resolveSeconds: 6, falseAlarms: [] },
      { set: "dev", scenario: "control", about: "Nothing changes.", app: "shop", control: true, detected: false, causeCorrect: false, suggestionCorrect: false, resolved: false, falseAlarms: [] },
    ],
  },
  heldout: {
    set: "heldout",
    generatedAt: t(0),
    summary: { scenarios: 1, faults: 1, controls: 0, detected: 0, causeCorrect: 0, finalCauseCorrect: 0, suggestionCorrect: 0, resolved: 0, falseAlarms: 1, medianDetectSeconds: 0, maxDetectSeconds: 0, medianResolveSeconds: 0, errors: 0 },
    outcomes: [
      { set: "heldout", scenario: "role_work_mem", about: "work_mem lowered for one role.", app: "billing", control: false, target: "usage_rollup", wantCause: "SPILL_TO_DISK", detected: false, causeCorrect: false, suggestionCorrect: false, resolved: false, falseAlarms: ["account"] },
    ],
  },
};
