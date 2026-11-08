import { useResource } from "../../shared/hooks/useResource";
import { SkeletonRows } from "../../shared/ui/Skeleton";
import { EmptyState, ErrorState } from "../../shared/ui/States";
import { Sparkline } from "../../shared/charts/Sparkline";
import { ms, oneLine, rate } from "../../shared/format";
import { net } from "../../theme/tokens";
import { href, navigate } from "../../app/router";
import { queryName, useSource } from "../source/SourceContext";

const statusWord = { OK: "", REGRESSED: "Regressed", NEW: "New" } as const;

export function QueriesPage() {
  const { source } = useSource();
  const res = useResource((s) => source.overview(s), source.id, source.live ? net.liveRefreshMs : undefined);
  if (res.state === "loading") return <SkeletonRows label="Loading queries" />;
  if (res.state === "error") return <ErrorState error={res.error} onRetry={res.retry} what="queries" />;
  const o = res.data;
  const rows = o.queries ?? [];
  return (
    <>
      <header className="page-head">
        <h1>Queries by database time</h1>
        <p className="page-head__lead">
          The {rows.length} heaviest of {o.trackedQueries} statements seen in pg_stat_statements, over the last {Math.round(o.windowSeconds / 60) || 1} minutes.
          Regressed queries come first.
        </p>
      </header>
      {rows.length === 0 ? (
        <EmptyState title="No statements yet">The first numbers arrive one collection interval after the collector starts.</EmptyState>
      ) : (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Query</th>
                <th className="num">Calls</th>
                <th className="num">Mean</th>
                <th className="num">Database time</th>
                <th>Mean per second</th>
                <th>Status</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((q) => (
                <tr key={q.id} className="row-link" onClick={() => navigate(`/queries/${q.id}`, source.id)}>
                  <td>
                    <a href={href(`/queries/${q.id}`, source.id)} onClick={(e) => e.stopPropagation()}>
                      {queryName(source, q.id, q.text)}
                    </a>
                    <div className="cell-sql">{oneLine(q.text, 80)}</div>
                  </td>
                  <td className="num">{rate(q.callsPerSec)}</td>
                  <td className="num">{ms(q.meanMs)}</td>
                  <td className="num">{ms(q.loadMsPerSec)}/s</td>
                  <td>
                    <Sparkline values={q.spark ?? []} alert={q.status !== "OK"} />
                  </td>
                  <td>
                    {q.status !== "OK" && q.insightId ? (
                      <a className="status status--open" href={href(`/insights/${q.insightId}`, source.id)} onClick={(e) => e.stopPropagation()}>
                        {statusWord[q.status]}
                      </a>
                    ) : null}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}
