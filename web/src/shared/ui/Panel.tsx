import type { ReactNode } from "react";

export function Panel({ title, note, children, id }: { title: string; note?: ReactNode; children: ReactNode; id?: string }) {
  return (
    <section className="panel" aria-labelledby={id}>
      <div className="panel__title">
        <h3 id={id}>{title}</h3>
        {note && <span className="panel__note">{note}</span>}
      </div>
      {children}
    </section>
  );
}

export function Stat({ value, label }: { value: ReactNode; label: string }) {
  return (
    <div>
      <div className="stat__value">{value}</div>
      <div className="stat__label">{label}</div>
    </div>
  );
}
