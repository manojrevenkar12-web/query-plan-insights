import type { Results } from "../../shared/api/types";
import { useSource } from "./SourceContext";

/** Choose between the live collector and one of the evaluation's recorded scenarios. */
export function SourcePicker({ results, about }: { results?: Results; about?: string }) {
  const { source, choose } = useSource();
  const sets = [
    { key: "dev", name: "Development scenarios", report: results?.dev },
    { key: "heldout", name: "Held-out scenarios", report: results?.heldout },
  ];
  return (
    <div className="source">
      <label htmlFor="source">Showing</label>
      <select id="source" value={source.id} onChange={(e) => choose(e.target.value)}>
        <option value="live">Live database</option>
        {sets.map(
          (s) =>
            s.report && (
              <optgroup key={s.key} label={s.name}>
                {s.report.outcomes.map((o) => (
                  <option key={o.scenario} value={`${s.key}-${o.scenario}`}>
                    Recording: {o.scenario.replaceAll("_", " ")}
                  </option>
                ))}
              </optgroup>
            ),
        )}
      </select>
      {about && <p className="source__about">{about}</p>}
    </div>
  );
}
