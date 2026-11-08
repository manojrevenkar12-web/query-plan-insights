/** Milliseconds the way a developer reads them: 0.08 ms, 19.3 ms, 1.24 s. */
export function ms(v: number | undefined | null): string {
  if (v == null || !Number.isFinite(v)) return "–";
  if (v >= 1000) return `${(v / 1000).toFixed(2)} s`;
  if (v >= 100) return `${v.toFixed(0)} ms`;
  if (v >= 10) return `${v.toFixed(1)} ms`;
  return `${v.toFixed(2)} ms`;
}

export function ratio(after: number, before: number): string {
  if (!before || !Number.isFinite(after / before)) return "–";
  const r = after / before;
  return r >= 10 ? `${Math.round(r)}×` : `${r.toFixed(1)}×`;
}

export function rate(perSec: number): string {
  if (!Number.isFinite(perSec)) return "–";
  return perSec >= 10 ? `${Math.round(perSec)}/s` : `${perSec.toFixed(1)}/s`;
}

export function seconds(s: number | undefined): string {
  if (s == null || !Number.isFinite(s)) return "–";
  return s >= 60 ? `${Math.floor(s / 60)} min ${Math.round(s % 60)} s` : `${Math.round(s)} s`;
}

export function clock(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

/** Seconds between two timestamps. */
export function between(a: string, b: string): number {
  return (new Date(b).getTime() - new Date(a).getTime()) / 1000;
}

export function fraction(n: number, d: number): string {
  return d ? `${n} of ${d}` : "–";
}

/** One line of SQL, shortened. */
export function oneLine(sql: string, max = 96): string {
  const s = sql.replace(/\s+/g, " ").trim();
  return s.length > max ? s.slice(0, max - 1) + "…" : s;
}
