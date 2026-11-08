import { useResource } from "../../shared/hooks/useResource";
import { SkeletonRows } from "../../shared/ui/Skeleton";
import { EmptyState, ErrorState } from "../../shared/ui/States";
import { Panel } from "../../shared/ui/Panel";
import { LatencyChart } from "../../shared/charts/LatencyChart";
import { clock } from "../../shared/format";
import { color, net } from "../../theme/tokens";
import { href } from "../../app/router";
import { queryName, useSource } from "../source/SourceContext";
import { InsightRow } from "../insights/InsightRow";

export function QueryDetailPage({ id }: { id: string }) {
  const { source } = useSource();
  const res = useResource((s) => source.query(id, s), `${source.id}/q${id}`, source.live ? net.liveRefreshMs : undefined);
  const back = (
    <a className="back" href={href("/queries", source.id)}>
      ← All queries
    </a>
  );
  if (res.state === "loading") return <SkeletonRows rows={3} label="Loading the query" />;
  if (res.state === "error")
    return (
      <>
        {back}
        <ErrorState error={res.error} onRetry={res.retry} what="this query" />
      </>
    );
  const q = res.data;
  const name = queryName(source, q.id, q.text);
  const plans = q.plans ?? [];
  const markers = source.timeline
    ? [
        { at: source.timeline.faultAt, label: "fault", tone: color.crimson },
        { at: source.timeline.fixAt, label: "fix", tone: color.green },
      ]
    : [];
  return (
    <>
      {back}
      <header className="finding">
        <h1>{name}</h1>
        <pre className="codeblock" style={{ paddingRight: 16 }}>
          <code>{q.text}</code>
        </pre>
      </header>
      <Panel title="Latency" id="q-latency">
        <LatencyChart title={`Mean time per call of ${name}`} series={q.series ?? []} markers={markers} />
      </Panel>
      <Panel title="Plans" note={q.explainError ? `EXPLAIN failed: ${q.explainError}` : `${plans.length} distinct plan(s)`} id="q-plans">
        {plans.length === 0 ? (
          <EmptyState title="No plan captured">Only SELECT and WITH statements are explained: the monitoring role cannot run writes.</EmptyState>
        ) : (
          <div className="grid-2">
            {plans.map((p) => (
              <div key={p.fingerprint + p.at}>
                <div className="plan__head">
                  <span>from {clock(p.at)}</span>
                  <code>{p.fingerprint}</code>
                </div>
                <pre className="plan">{p.tree}</pre>
              </div>
            ))}
          </div>
        )}
      </Panel>
      <Panel title="Insights on this query" id="q-insights">
        {q.insights.length === 0 ? (
          <p className="panel__note">None. It has run at its usual speed.</p>
        ) : (
          <ul className="insights">
            {q.insights.map((i) => (
              <InsightRow key={i.id} insight={i} name={name} link={href(`/insights/${i.id}`, source.id)} />
            ))}
          </ul>
        )}
      </Panel>
    </>
  );
}
