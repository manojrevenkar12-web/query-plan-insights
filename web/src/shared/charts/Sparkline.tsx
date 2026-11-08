import { chart, color } from "../../theme/tokens";
import { logScale, linearScale, path } from "./scale";

export function Sparkline({ values, alert }: { values: number[]; alert?: boolean }) {
  const w = chart.sparkWidth;
  const h = chart.sparkHeight;
  const live = values.filter((v) => v > 0);
  if (live.length < 2) return <svg width={w} height={h} aria-hidden="true" />;
  const y = logScale(Math.min(...live), Math.max(...live), h - 2, 2);
  const x = linearScale(0, values.length - 1, 1, w - 1);
  const d = path(values.map((v, i) => (v > 0 ? [x(i), y(v)] : null)));
  return (
    <svg width={w} height={h} aria-hidden="true">
      <path d={d} fill="none" stroke={alert ? color.crimson : color.muted} strokeWidth={1.25} />
    </svg>
  );
}
