export interface Scale {
  (v: number): number;
  ticks: number[];
}

const floor = 1e-3;

/** A log scale for latencies, which span four orders of magnitude between a healthy and a broken query. */
export function logScale(min: number, max: number, r0: number, r1: number): Scale {
  const lo = Math.log10(Math.max(min, floor));
  let hi = Math.log10(Math.max(max, floor));
  if (hi - lo < 1) hi = lo + 1;
  const a = Math.floor(lo);
  const b = Math.ceil(hi);
  const f = ((v: number) => {
    const t = (Math.log10(Math.max(v, floor)) - a) / (b - a);
    return r0 + t * (r1 - r0);
  }) as Scale;
  f.ticks = [];
  for (let e = a; e <= b; e++) f.ticks.push(10 ** e);
  return f;
}

export function linearScale(d0: number, d1: number, r0: number, r1: number) {
  const span = d1 - d0 || 1;
  return (v: number) => r0 + ((v - d0) / span) * (r1 - r0);
}

/** An SVG path through the points, broken where a value is missing. */
export function path(points: Array<[number, number] | null>): string {
  let d = "";
  let pen = false;
  for (const p of points) {
    if (!p) {
      pen = false;
      continue;
    }
    d += `${pen ? "L" : "M"}${p[0].toFixed(1)},${p[1].toFixed(1)}`;
    pen = true;
  }
  return d;
}

export function tickLabel(v: number): string {
  if (v >= 1000) return `${v / 1000} s`;
  if (v >= 1) return `${v} ms`;
  return `${v} ms`;
}
