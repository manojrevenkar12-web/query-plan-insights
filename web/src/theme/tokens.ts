// The single source of the visual system. CSS reads these as custom properties (applyTokens); charts read them here.

export const color = {
  paper: "#EDF0F3",
  panel: "#FFFFFF",
  ink: "#18202B",
  muted: "#5A6472",
  faint: "#8B95A3",
  rule: "#CBD3DC",
  ruleStrong: "#A9B4C1",
  cobalt: "#2C4FD6",
  cobaltWash: "#E3E9FC",
  crimson: "#B3264A",
  crimsonWash: "#F8E4EA",
  green: "#24785A",
  greenWash: "#E0F1EA",
  amber: "#8C5B00",
  amberWash: "#F6ECD8",
  violet: "#6A3FB5",
  violetWash: "#ECE5F8",
} as const;

export const font = {
  ui: '"Atkinson Hyperlegible", "Segoe UI", system-ui, sans-serif',
  code: '"JetBrains Mono", ui-monospace, "SFMono-Regular", Menlo, monospace',
} as const;

// A 1.25 scale from a 15px body.
export const size = {
  small: 13,
  body: 15,
  lead: 18,
  title: 24,
  display: 32,
} as const;

export const weight = { regular: 400, bold: 700 } as const;

export const leading = { tight: 1.2, body: 1.5, code: 1.55 } as const;

export const space = { 1: 4, 2: 8, 3: 12, 4: 16, 5: 24, 6: 32, 7: 48, 8: 64 } as const;

export const radius = { control: 4, panel: 6 } as const;

export const layout = {
  railWidth: 232,
  contentMax: 1240,
  measure: 72, // characters per line for prose
  breakpoint: 860,
} as const;

export const chart = {
  height: 168,
  sparkWidth: 120,
  sparkHeight: 28,
  stroke: 1.75,
  padding: { top: 12, right: 12, bottom: 24, left: 52 },
  ticks: 4,
} as const;

export const motion = {
  quick: 0.16,
  settle: 0.32,
  draw: 0.9,
  ease: [0.22, 1, 0.36, 1] as [number, number, number, number],
} as const;

export const net = {
  timeoutMs: 8000,
  liveRefreshMs: 2000,
} as const;

// Cause codes the collector reports, with the words and colour the interface uses for them.
export const causes: Record<string, { label: string; tone: Tone }> = {
  MISSING_INDEX: { label: "Index dropped", tone: "cobalt" },
  SETTING_CHANGE: { label: "Setting changed", tone: "cobalt" },
  PLAN_CHANGE: { label: "Plan changed", tone: "cobalt" },
  STALE_STATISTICS: { label: "Stale statistics", tone: "cobalt" },
  LOCK_CONTENTION: { label: "Lock waits", tone: "violet" },
  SPILL_TO_DISK: { label: "Spills to disk", tone: "amber" },
  CACHE_MISSES: { label: "Cache misses", tone: "amber" },
  DATA_GROWTH: { label: "More rows", tone: "amber" },
  N_PLUS_ONE: { label: "N+1 queries", tone: "crimson" },
  HEAVY_NEW_QUERY: { label: "New heavy query", tone: "crimson" },
  RESOURCE_CONTENTION: { label: "Database busier", tone: "violet" },
  UNKNOWN: { label: "Cause unknown", tone: "muted" },
};

export type Tone = "cobalt" | "crimson" | "green" | "amber" | "violet" | "muted";

export const toneColor: Record<Tone, { fg: string; wash: string }> = {
  cobalt: { fg: color.cobalt, wash: color.cobaltWash },
  crimson: { fg: color.crimson, wash: color.crimsonWash },
  green: { fg: color.green, wash: color.greenWash },
  amber: { fg: color.amber, wash: color.amberWash },
  violet: { fg: color.violet, wash: color.violetWash },
  muted: { fg: color.muted, wash: color.paper },
};

function kebab(s: string) {
  return s.replace(/[A-Z]/g, (c) => "-" + c.toLowerCase());
}

/** Publishes the tokens as CSS custom properties on the root element. */
export function applyTokens(root: HTMLElement = document.documentElement) {
  for (const [k, v] of Object.entries(color)) root.style.setProperty(`--c-${kebab(k)}`, v);
  for (const [k, v] of Object.entries(font)) root.style.setProperty(`--f-${k}`, v);
  for (const [k, v] of Object.entries(size)) root.style.setProperty(`--s-${k}`, `${v}px`);
  for (const [k, v] of Object.entries(space)) root.style.setProperty(`--sp-${k}`, `${v}px`);
  for (const [k, v] of Object.entries(radius)) root.style.setProperty(`--r-${k}`, `${v}px`);
  for (const [k, v] of Object.entries(leading)) root.style.setProperty(`--lh-${k}`, String(v));
  root.style.setProperty("--rail", `${layout.railWidth}px`);
  root.style.setProperty("--content-max", `${layout.contentMax}px`);
  root.style.setProperty("--measure", `${layout.measure}ch`);
}
