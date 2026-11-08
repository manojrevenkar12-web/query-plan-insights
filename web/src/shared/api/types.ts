// Mirrors of the JSON the Go server writes (internal/api, internal/store, cmd/qpi-eval).

export interface SeriesPoint {
  at: string;
  calls: number;
  meanMs: number;
  rowsPerCall: number;
  readPerCall: number;
  tempPerCall: number;
  lockShare: number;
}

export interface DbEvent {
  at: string;
  kind: "INDEX_CREATED" | "INDEX_DROPPED" | "SETTING_CHANGED";
  relation?: string;
  name: string;
  before?: string;
  after?: string;
}

export interface QueryRow {
  /** pg_stat_statements queryid: a 64-bit integer, sent as a string so no digit is lost */
  id: string;
  text: string;
  callsPerSec: number;
  meanMs: number;
  loadMsPerSec: number;
  plan?: string;
  spark: number[] | null;
  insightId?: number;
  status: "OK" | "REGRESSED" | "NEW";
}

export interface Overview {
  startedAt: string;
  now: string;
  database: SeriesPoint[] | null;
  openInsights: number;
  resolvedInsights: number;
  impactMsPerSec: number;
  queries: QueryRow[] | null;
  trackedQueries: number;
  events: DbEvent[] | null;
  windowSeconds: number;
}

export interface PlanChange {
  kind: "access" | "join" | "aggregation";
  relation?: string;
  before: string;
  after: string;
}

export interface Insight {
  id: number;
  kind: "LATENCY_REGRESSION" | "NEW_HEAVY_QUERY";
  signal?: "latency" | "spill";
  queryId: string;
  query: string;
  status: "OPEN" | "RESOLVED";
  openedAt: string;
  suspectedAt: string;
  baselineMs: number;
  currentMs: number;
  worstMs: number;
  callsPerSec: number;
  impactMsPerSec: number;
  cause: { code: string; confidence: number; summary: string };
  causeAtOpen: string;
  evidence: string[] | null;
  planChanges?: PlanChange[] | null;
  planBefore?: string;
  planAfter?: string;
  suggestion: { text: string; sql?: string };
  resolvedAt?: string;
  resolvedMs?: number;
  verification?: string;
}

export interface PlanView {
  at: string;
  fingerprint: string;
  tree: string;
}

export interface QueryDetail {
  id: string;
  text: string;
  firstSeen: string;
  series: SeriesPoint[] | null;
  plans: PlanView[] | null;
  explainError?: string;
  insights: Insight[];
}

export interface Snapshot {
  overview: Overview;
  insights: Insight[] | null;
  queries: QueryDetail[] | null;
}

export interface Timeline {
  start: string;
  faultAt: string;
  fixAt: string;
  end: string;
}

export interface Outcome {
  set: "dev" | "heldout";
  scenario: string;
  about: string;
  app: string;
  control: boolean;
  target?: string;
  wantKind?: string;
  wantCause?: string;
  detected: boolean;
  detectSeconds?: number;
  causeAtOpen?: string;
  causeCorrect: boolean;
  suggestionCorrect: boolean;
  resolved: boolean;
  resolveSeconds?: number;
  verification?: string;
  collateralFound?: string[] | null;
  falseAlarms: string[] | null;
  baselineMs?: number;
  worstMs?: number;
  error?: string;
}

export interface Summary {
  scenarios: number;
  faults: number;
  controls: number;
  detected: number;
  causeCorrect: number;
  finalCauseCorrect: number;
  suggestionCorrect: number;
  resolved: number;
  falseAlarms: number;
  medianDetectSeconds: number;
  maxDetectSeconds: number;
  medianResolveSeconds: number;
  errors: number;
}

export interface SetReport {
  set: "dev" | "heldout";
  generatedAt: string;
  summary: Summary;
  outcomes: Outcome[];
}

export interface Overhead {
  rounds: number;
  secondsPerPhase: number;
  workers: number;
  offMeanQps: number;
  onMeanQps: number;
  overheadPct: number;
  tickMeanMs: number;
}

export interface Results {
  generatedAt: string;
  heldoutPin: string;
  heldoutRuns: number;
  dev?: SetReport;
  heldout?: SetReport;
  overhead?: Overhead;
}

export interface Recording {
  set: "dev" | "heldout";
  scenario: { name: string; about: string; app: string; target?: string };
  timeline: Timeline;
  outcome: Outcome;
  names: Record<string, string>;
  snapshot: Snapshot;
}
