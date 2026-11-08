import { causes } from "../../theme/tokens";
import type { Insight } from "../../shared/api/types";
import { ms, ratio } from "../../shared/format";

/** The headline of an insight, in one sentence a developer can act on. */
export function headline(name: string, in_: Insight): string {
  if (in_.kind === "NEW_HEAVY_QUERY") {
    return `${name} is new and runs ${Math.round(in_.callsPerSec)} times a second`;
  }
  const peak = Math.max(in_.worstMs ?? 0, in_.currentMs);
  return `${name} became ${ratio(peak, in_.baselineMs)} slower, ${ms(in_.baselineMs)} to ${ms(peak)}`;
}

export function causeLabel(code: string): string {
  return causes[code]?.label ?? code.toLowerCase().replaceAll("_", " ");
}

export function confidence(c: number): string {
  if (c >= 0.85) return "strong evidence";
  if (c >= 0.6) return "likely";
  return "weak evidence";
}
