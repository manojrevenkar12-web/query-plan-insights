import { useCallback, useEffect, useMemo, useState } from "react";
import { MotionConfig } from "framer-motion";
import { useResource } from "../shared/hooks/useResource";
import { SkeletonRows } from "../shared/ui/Skeleton";
import { ErrorState } from "../shared/ui/States";
import { fetchJSON } from "../shared/api/client";
import type { Overview } from "../shared/api/types";
import { net } from "../theme/tokens";
import { liveSource, loadRecording, loadResults, recordingSource } from "../features/source/source";
import { SourceProvider } from "../features/source/SourceContext";
import { SourcePicker } from "../features/source/SourcePicker";
import { InsightsPage } from "../features/insights/InsightsPage";
import { InsightDetail } from "../features/insights/InsightDetail";
import { QueriesPage } from "../features/queries/QueriesPage";
import { QueryDetailPage } from "../features/queries/QueryDetailPage";
import { EvaluationPage } from "../features/evaluation/EvaluationPage";
import { navigate, useRoute } from "./router";
import { Shell, type NavItem } from "./Shell";

/** Picks the live database when it has data, otherwise the first recorded scenario. */
async function defaultSource(signal: AbortSignal): Promise<string> {
  try {
    const o = await fetchJSON<Overview>("/api/overview", signal);
    if (o.trackedQueries > 0) return "live";
  } catch {
    /* no live collector: fall through to the recordings */
  }
  try {
    const r = await loadResults(signal);
    const first = r.dev?.outcomes[0];
    if (first) return `dev-${first.scenario}`;
  } catch {
    /* nothing recorded either */
  }
  return "live";
}

export function App() {
  const route = useRoute();
  const requested = route.params.get("src");
  const [fallback, setFallback] = useState<string | null>(null);
  useEffect(() => {
    if (requested) return;
    const ctrl = new AbortController();
    defaultSource(ctrl.signal).then((id) => !ctrl.signal.aborted && setFallback(id));
    return () => ctrl.abort();
  }, [requested]);
  const srcId = requested ?? fallback;

  const results = useResource(loadResults, "results");
  const recording = useResource(
    async (s) => (srcId && srcId !== "live" ? recordingSource(await loadRecording(srcId, s)) : liveSource),
    srcId ?? "pending",
  );
  const source = recording.state === "ready" ? recording.data : null;
  const insights = useResource(async (s) => (source ? source.insights(s) : []), source?.id ?? "none", source?.live ? net.liveRefreshMs : undefined);

  const section = route.path[0] === "queries" ? "queries" : route.path[0] === "evaluation" ? "evaluation" : "insights";
  // a new source keeps the section but not the item: insight 3 of one recording is not insight 3 of another
  const choose = useCallback((id: string) => navigate(section === "insights" ? "/" : `/${section}`, id), [section]);

  const open = insights.state === "ready" ? insights.data.filter((i) => i.status === "OPEN").length : undefined;
  const nav: NavItem[] = [
    { key: "insights", label: "Insights", path: "/", count: open, alert: !!open },
    { key: "queries", label: "Queries", path: "/queries" },
    { key: "evaluation", label: "Evaluation", path: "/evaluation" },
  ];
  const about = useMemo(() => {
    if (!source || source.live || results.state !== "ready") return undefined;
    const o = [...(results.data.dev?.outcomes ?? []), ...(results.data.heldout?.outcomes ?? [])].find((x) => `${x.set}-${x.scenario}` === source.id);
    return o?.about;
  }, [source, results]);

  let body;
  if (section === "evaluation") body = <EvaluationPage results={results} />;
  else if (!srcId || recording.state === "loading") body = <SkeletonRows label="Loading the data source" />;
  else if (recording.state === "error") body = <ErrorState error={recording.error} onRetry={recording.retry} what="this recording" />;
  else {
    const item = route.path[1];
    if (section === "queries") body = item ? <QueryDetailPage id={item} /> : <QueriesPage />;
    else body = route.path[0] === "insights" && item ? <InsightDetail id={Number(item)} /> : <InsightsPage />;
  }

  const content = (
    <Shell
      nav={nav}
      active={section}
      src={srcId ?? "live"}
      top={section !== "evaluation" && source ? <SourcePicker results={results.state === "ready" ? results.data : undefined} about={about} /> : null}
    >
      {body}
    </Shell>
  );
  return (
    <MotionConfig reducedMotion="user">
      {source ? (
        <SourceProvider source={source} choose={choose}>
          {content}
        </SourceProvider>
      ) : (
        <SourceProvider source={liveSource} choose={choose}>
          {content}
        </SourceProvider>
      )}
    </MotionConfig>
  );
}
