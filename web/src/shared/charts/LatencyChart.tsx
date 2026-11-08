import { useId, useMemo, useRef, useState, useLayoutEffect } from "react";
import { motion, useReducedMotion } from "framer-motion";
import { chart, color, motion as m } from "../../theme/tokens";
import type { SeriesPoint } from "../api/types";
import { linearScale, logScale, path, tickLabel } from "./scale";
import { clock, ms } from "../format";

export interface Marker {
  at: string;
  label: string;
  tone: string;
}

/**
 * Mean latency per collection interval on a log scale, with the frozen baseline and what happened when: the fault,
 * the insight opening, the fix, the recovery.
 */
export function LatencyChart({ series, baselineMs, markers = [], title }: { series: SeriesPoint[]; baselineMs?: number; markers?: Marker[]; title: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(720);
  const reduce = useReducedMotion();
  const id = useId();
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(([e]) => e && setWidth(Math.max(280, e.contentRect.width)));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const pad = chart.padding;
  const h = chart.height;
  const model = useMemo(() => {
    const pts = series.filter((p) => p.calls > 0);
    const t = series.map((p) => new Date(p.at).getTime());
    const t0 = t[0] ?? 0;
    const t1 = t[t.length - 1] ?? 1;
    const values = pts.map((p) => p.meanMs);
    if (baselineMs) values.push(baselineMs);
    const y = logScale(Math.min(...values, 1), Math.max(...values, 1), h - pad.bottom, pad.top);
    const x = linearScale(t0, t1, pad.left, width - pad.right);
    const line = path(series.map((p) => (p.calls > 0 ? [x(new Date(p.at).getTime()), y(p.meanMs)] : null)));
    const peak = pts.reduce((a, p) => Math.max(a, p.meanMs), 0);
    return { x, y, line, t0, t1, peak };
  }, [series, baselineMs, width, h, pad.bottom, pad.top, pad.left, pad.right]);

  if (series.length < 2) return null;
  const { x, y, line, t0, t1, peak } = model;
  const inRange = markers.filter((mk) => {
    const t = new Date(mk.at).getTime();
    return t >= t0 && t <= t1;
  });

  // marker labels sit above the plot; one that would overlap its left neighbour moves up a row
  const placed: Array<{ mk: Marker; px: number; row: number }> = [];
  for (const mk of [...inRange].sort((a, b) => a.at.localeCompare(b.at))) {
    const px = x(new Date(mk.at).getTime());
    const prev = placed[placed.length - 1];
    const row = prev && px - prev.px < chart.labelGap && prev.row === 0 ? 1 : 0;
    placed.push({ mk, px, row });
  }
  return (
    <figure ref={ref} style={{ margin: 0 }}>
      <svg className="chart" width={width} height={h} role="img" aria-labelledby={id}>
        <title id={id}>{`${title}: peak ${ms(peak)}${baselineMs ? `, baseline ${ms(baselineMs)}` : ""}`}</title>
        {y.ticks.map((v) => (
          <g key={v}>
            <line x1={pad.left} x2={width - pad.right} y1={y(v)} y2={y(v)} stroke={color.rule} strokeWidth={1} />
            <text x={pad.left - 8} y={y(v) + 4} textAnchor="end">
              {tickLabel(v)}
            </text>
          </g>
        ))}
        {placed.map(({ mk, px, row }) => (
          <g key={mk.label + mk.at}>
            <line x1={px} x2={px} y1={pad.top - row * chart.labelRow - 8} y2={h - pad.bottom} stroke={mk.tone} strokeWidth={1} strokeDasharray="3 3" />
            <text x={px + 4} y={pad.top - row * chart.labelRow - 10} style={{ fill: mk.tone }}>
              {mk.label}
            </text>
          </g>
        ))}
        {baselineMs ? (
          <line x1={pad.left} x2={width - pad.right} y1={y(baselineMs)} y2={y(baselineMs)} stroke={color.green} strokeWidth={1.25} strokeDasharray="6 4" />
        ) : null}
        <motion.path
          d={line}
          fill="none"
          stroke={color.ink}
          strokeWidth={chart.stroke}
          strokeLinejoin="round"
          initial={reduce ? false : { pathLength: 0 }}
          animate={{ pathLength: 1 }}
          transition={{ duration: m.draw, ease: m.ease }}
        />
        <text x={pad.left} y={h - 6}>
          {clock(new Date(t0).toISOString())}
        </text>
        <text x={width - pad.right} y={h - 6} textAnchor="end">
          {clock(new Date(t1).toISOString())}
        </text>
      </svg>
      <figcaption className="chart__legend">
        <span>
          <span className="chart__key" style={{ borderColor: color.ink }} />
          mean time per call, each second
        </span>
        {baselineMs ? (
          <span>
            <span className="chart__key chart__key--dashed" style={{ borderColor: color.green }} />
            baseline {ms(baselineMs)}
          </span>
        ) : null}
      </figcaption>
    </figure>
  );
}
