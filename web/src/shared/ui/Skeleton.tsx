import { space } from "../../theme/tokens";

export function Skeleton({ width = "100%", height = space[4] }: { width?: number | string; height?: number }) {
  return <div className="skeleton" style={{ width, height }} aria-hidden="true" />;
}

/** A placeholder in the shape of a list of rows, announced as loading. */
export function SkeletonRows({ rows = 4, label }: { rows?: number; label: string }) {
  return (
    <div role="status" aria-live="polite" style={{ display: "flex", flexDirection: "column", gap: space[4] }}>
      <span className="visually-hidden">{label}</span>
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} style={{ display: "flex", flexDirection: "column", gap: space[2] }}>
          <Skeleton width={`${70 - i * 8}%`} height={space[5]} />
          <Skeleton width="40%" height={space[3]} />
        </div>
      ))}
    </div>
  );
}
