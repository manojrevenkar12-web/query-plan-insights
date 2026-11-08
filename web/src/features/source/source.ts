import { fetchJSON } from "../../shared/api/client";
import type { Insight, Overview, QueryDetail, Recording, Results, Timeline } from "../../shared/api/types";

/** Where the screens get their data: the live collector, or one recorded evaluation scenario. */
export interface Source {
  id: string;
  label: string;
  live: boolean;
  timeline?: Timeline;
  names: Record<string, string>;
  overview(signal: AbortSignal): Promise<Overview>;
  insights(signal: AbortSignal): Promise<Insight[]>;
  query(id: string, signal: AbortSignal): Promise<QueryDetail>;
}

export const liveSource: Source = {
  id: "live",
  label: "Live database",
  live: true,
  names: {},
  overview: (s) => fetchJSON<Overview>("/api/overview", s),
  insights: async (s) => (await fetchJSON<Insight[] | null>("/api/insights", s)) ?? [],
  query: (id, s) => fetchJSON<QueryDetail>(`/api/queries/${encodeURIComponent(id)}`, s),
};

export function recordingSource(rec: Recording): Source {
  const queries = new Map((rec.snapshot.queries ?? []).map((q) => [q.id, q]));
  return {
    id: `${rec.set}-${rec.scenario.name}`,
    label: rec.scenario.name.replaceAll("_", " "),
    live: false,
    timeline: rec.timeline,
    names: rec.names ?? {},
    overview: async () => rec.snapshot.overview,
    insights: async () => rec.snapshot.insights ?? [],
    query: async (id) => {
      const q = queries.get(id);
      if (!q) throw new Error("This recording has no detail for that query.");
      return q;
    },
  };
}

export const loadResults = (s: AbortSignal) => fetchJSON<Results>("/api/evaluation", s);

export const loadRecording = (id: string, s: AbortSignal) => fetchJSON<Recording>(`/api/scenarios/${id}`, s);
