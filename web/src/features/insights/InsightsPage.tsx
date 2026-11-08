import { useState } from "react";
import { AnimatePresence } from "framer-motion";
import { useResource } from "../../shared/hooks/useResource";
import { SkeletonRows } from "../../shared/ui/Skeleton";
import { EmptyState, ErrorState } from "../../shared/ui/States";
import { Stat } from "../../shared/ui/Panel";
import { ms } from "../../shared/format";
import { net } from "../../theme/tokens";
import { href } from "../../app/router";
import { queryName, useSource } from "../source/SourceContext";
import { InsightRow } from "./InsightRow";
import { causeLabel, headline } from "./sentence";
import type { Insight } from "../../shared/api/types";

type Filter = "all" | "open" | "fixed";

const filters: Array<{ key: Filter; label: string }> = [
  { key: "all", label: "All" },
  { key: "open", label: "Open" },
  { key: "fixed", label: "Fixed" },
];

export function InsightsPage() {
  const { source } = useSource();
  const res = useResource((s) => source.insights(s), source.id, source.live ? net.liveRefreshMs : undefined);
  const [filter, setFilter] = useState<Filter>("all");

  if (res.state === "loading") return <SkeletonRows label="Loading insights" />;
  if (res.state === "error") return <ErrorState error={res.error} onRetry={res.retry} what="insights" />;

  const all = res.data;
  const open = all.filter((i) => i.status === "OPEN");
  const worst = [...open].sort((a, b) => b.impactMsPerSec - a.impactMsPerSec)[0];
  const shown = all.filter((i) => filter === "all" || (filter === "open" ? i.status === "OPEN" : i.status === "RESOLVED"));
  const name = (i: Insight) => queryName(source, i.queryId, i.query);

  return (
    <>
      <header className="page-head">
        <h1>{worst ? headline(name(worst), worst) : all.length ? "Every regression found here has been fixed" : "No query has regressed"}</h1>
        <p className="page-head__lead">
          {worst
            ? `${causeLabel(worst.cause.code)}: ${worst.cause.summary}.`
            : "Each query is compared with its own recent past every second. A regression appears here when it lasts three seconds."}
        </p>
        <div className="stats">
          <Stat value={open.length} label="open regressions" />
          <Stat value={all.length - open.length} label="fixed and confirmed" />
          <Stat value={ms(open.reduce((a, i) => a + i.impactMsPerSec, 0))} label="extra database time per second" />
        </div>
      </header>
      <section aria-label="Insights">
        <div className="panel__title">
          <h2>Insights</h2>
          <div className="filters" role="group" aria-label="Show">
            {filters.map((f) => (
              <button key={f.key} aria-pressed={filter === f.key} onClick={() => setFilter(f.key)}>
                {f.label}
              </button>
            ))}
          </div>
        </div>
        {shown.length === 0 ? (
          <EmptyState title={all.length === 0 ? "Nothing to look at yet" : `No ${filter} insights`}>
            {all.length === 0
              ? source.live
                ? "The collector is watching. Insights appear when a query gets slower or a new query floods the database."
                : "This recording ran without any regression, which is what a control scenario should show."
              : "Change the filter to see the others."}
          </EmptyState>
        ) : (
          <ul className="insights">
            <AnimatePresence initial={false}>
              {shown.map((i) => (
                <InsightRow key={i.id} insight={i} name={name(i)} link={href(`/insights/${i.id}`, source.id)} />
              ))}
            </AnimatePresence>
          </ul>
        )}
      </section>
    </>
  );
}
