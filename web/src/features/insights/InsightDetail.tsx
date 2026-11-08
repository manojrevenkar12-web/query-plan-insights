import { useResource } from "../../shared/hooks/useResource";
import { SkeletonRows } from "../../shared/ui/Skeleton";
import { EmptyState, ErrorState } from "../../shared/ui/States";
import { Panel, Stat } from "../../shared/ui/Panel";
import { CauseTag } from "../../shared/ui/CauseTag";
import { CodeBlock } from "../../shared/ui/CodeBlock";
import { LatencyChart, type Marker } from "../../shared/charts/LatencyChart";
import { between, clock, ms, oneLine, rate, seconds } from "../../shared/format";
import { color, net } from "../../theme/tokens";
import { href } from "../../app/router";
import { queryName, useSource } from "../source/SourceContext";
import { confidence, headline } from "./sentence";
import { PlanDiff } from "./PlanDiff";
import type { DbEvent, Insight, QueryDetail } from "../../shared/api/types";

function markers(insight: Insight, events: DbEvent[], timeline?: { faultAt: string; fixAt: string }): Marker[] {
  const out: Marker[] = [];
  if (timeline) {
    out.push({ at: timeline.faultAt, label: "fault", tone: color.crimson });
    out.push({ at: timeline.fixAt, label: "fix", tone: color.green });
  } else {
    for (const e of events) out.push({ at: e.at, label: e.kind === "SETTING_CHANGED" ? e.name : e.kind.toLowerCase().replace("_", " "), tone: color.cobalt });
  }
  out.push({ at: insight.openedAt, label: "opened", tone: color.muted });
  if (insight.resolvedAt) out.push({ at: insight.resolvedAt, label: "confirmed", tone: color.green });
  return out;
}

export function InsightDetail({ id }: { id: number }) {
  const { source } = useSource();
  const res = useResource(
    async (s) => {
      const [insights, overview] = await Promise.all([source.insights(s), source.overview(s)]);
      const insight = insights.find((i) => i.id === id);
      const query: QueryDetail | null = insight ? await source.query(insight.queryId, s) : null;
      return { insight, query, events: overview.events ?? [] };
    },
    `${source.id}/${id}`,
    source.live ? net.liveRefreshMs : undefined,
  );
  const back = (
    <a className="back" href={href("/", source.id)}>
      ← All insights
    </a>
  );

  if (res.state === "loading") return <SkeletonRows rows={3} label="Loading the insight" />;
  if (res.state === "error") return <ErrorState error={res.error} onRetry={res.retry} what="this insight" />;
  const { insight, query, events } = res.data;
  if (!insight || !query) {
    return (
      <>
        {back}
        <EmptyState title={`There is no insight ${id} here`} action={<a href={href("/", source.id)}>See all insights</a>}>
          It may belong to another recording, or the collector restarted and forgot it.
        </EmptyState>
      </>
    );
  }
  const name = queryName(source, insight.queryId, insight.query);
  const open = insight.status === "OPEN";
  const detectedIn = source.timeline ? between(source.timeline.faultAt, insight.openedAt) : undefined;

  return (
    <>
      {back}
      <header className="finding">
        <h1>{headline(name, insight)}</h1>
        <p className="finding__cause">
          <CauseTag code={insight.cause.code} /> {insight.cause.summary}, {confidence(insight.cause.confidence)}.
        </p>
        <code className="finding__sql">{oneLine(insight.query, 240)}</code>
        <div className="stats">
          <Stat value={open ? "Open" : "Fixed"} label={open ? `since ${clock(insight.openedAt)}` : `confirmed ${clock(insight.resolvedAt!)}`} />
          <Stat value={rate(insight.callsPerSec)} label="calls" />
          {open ? (
            <Stat value={ms(insight.impactMsPerSec)} label="extra database time per second" />
          ) : (
            <Stat value={ms(insight.resolvedMs)} label={`per call now, from a worst of ${ms(insight.worstMs)}`} />
          )}
          {detectedIn != null && <Stat value={seconds(detectedIn)} label="from fault to insight" />}
        </div>
      </header>

      <Panel title="Latency" note={`${(query.series ?? []).length} seconds recorded`} id="latency">
        <LatencyChart
          title={`Mean time per call of ${name}`}
          series={query.series ?? []}
          baselineMs={insight.kind === "LATENCY_REGRESSION" ? insight.baselineMs : undefined}
          markers={markers(insight, events, source.timeline)}
        />
      </Panel>

      {(insight.planBefore || insight.planAfter) && (
        <Panel title="What the planner changed" note={insight.planChanges?.length ? `${insight.planChanges.length} change(s)` : undefined} id="plans">
          <PlanDiff before={insight.planBefore} after={insight.planAfter} changes={insight.planChanges} />
        </Panel>
      )}

      <div className="grid-2">
        <Panel title="Evidence" id="evidence">
          <ul className="evidence">
            {(insight.evidence ?? []).map((e) => (
              <li key={e}>{e}</li>
            ))}
          </ul>
        </Panel>
        <Panel title="Fix" id="fix">
          <div className="fix">
            <p>{insight.suggestion.text}</p>
            {insight.suggestion.sql && <CodeBlock code={insight.suggestion.sql} label="Suggested SQL" />}
            {insight.verification ? (
              <p className="verified">
                <strong>Confirmed by the data:</strong> {insight.verification}.
              </p>
            ) : (
              <p className="verified verified--waiting">The fix is confirmed once the query runs at its baseline speed for three seconds.</p>
            )}
          </div>
        </Panel>
      </div>
    </>
  );
}
