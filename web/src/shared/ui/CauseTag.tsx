import { causes, toneColor } from "../../theme/tokens";

export function CauseTag({ code }: { code: string }) {
  const c = causes[code] ?? causes.UNKNOWN!;
  const t = toneColor[c.tone];
  return (
    <span className="tag" style={{ color: t.fg, background: t.wash }}>
      {c.label}
    </span>
  );
}

export function causeTone(code: string) {
  return toneColor[(causes[code] ?? causes.UNKNOWN!).tone];
}
