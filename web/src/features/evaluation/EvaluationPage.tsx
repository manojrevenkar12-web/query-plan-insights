import { motion, useReducedMotion } from "framer-motion";
import type { Outcome, Results, SetReport } from "../../shared/api/types";
import { SkeletonRows } from "../../shared/ui/Skeleton";
import { EmptyState, ErrorState } from "../../shared/ui/States";
import { Panel } from "../../shared/ui/Panel";
import { CauseTag } from "../../shared/ui/CauseTag";
import { fraction, seconds } from "../../shared/format";
import { color, motion as m } from "../../theme/tokens";
import { navigate } from "../../app/router";
import type { Resource } from "../../shared/hooks/useResource";

function Meter({ n, d, tone }: { n: number; d: number; tone: string }) {
  const reduce = useReducedMotion();
  const share = d ? n / d : 0;
  return (
    <div className="meter" aria-hidden="true">
      <motion.div
        className="meter__fill"
        style={{ background: tone }}
        initial={reduce ? false : { width: 0 }}
        animate={{ width: `${share * 100}%` }}
        transition={{ duration: m.draw, ease: m.ease }}
      />
    </div>
  );
}

function Scoreboard({ report, title, note }: { report?: SetReport; title: string; note: string }) {
  if (!report) {
    return (
      <Panel title={title} note={note}>
        <EmptyState title="Not run yet">Run qpi-eval for this set, then qpi-eval -report.</EmptyState>
      </Panel>
    );
  }
  const s = report.summary;
  const rows: Array<[string, number, number, string]> = [
    ["Faults detected", s.detected, s.faults, color.ink],
    ["Cause named right when the insight opened", s.causeCorrect, s.faults, color.cobalt],
    ["Fix suggested", s.suggestionCorrect, s.faults, color.cobalt],
    ["Fix confirmed by the data", s.resolved, s.faults, color.green],
  ];
  return (
    <Panel title={title} note={note}>
      <dl className="score">
        {rows.map(([label, n, d, tone]) => (
          <div key={label} style={{ display: "contents" }}>
            <dt>{label}</dt>
            <dd>{fraction(n, d)}</dd>
            <Meter n={n} d={d} tone={tone} />
          </div>
        ))}
        <dt>False alarms, {s.controls} control run(s) included</dt>
        <dd style={{ color: s.falseAlarms ? color.crimson : color.green }}>{s.falseAlarms}</dd>
        <dt>Median time from fault to insight</dt>
        <dd>{seconds(s.medianDetectSeconds)}</dd>
        <dt>Median time from fix to confirmation</dt>
        <dd>{seconds(s.medianResolveSeconds)}</dd>
      </dl>
    </Panel>
  );
}

function mark(ok: boolean, yes = "yes", no = "no") {
  return <span className={ok ? "mark-yes" : "mark-no"}>{ok ? yes : no}</span>;
}

function ScenarioTable({ outcomes, onOpen }: { outcomes: Outcome[]; onOpen: (o: Outcome) => void }) {
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>
            <th>Scenario</th>
            <th>Target query</th>
            <th className="num">Detected</th>
            <th>Cause at open</th>
            <th>Fix suggested</th>
            <th className="num">Fix confirmed</th>
            <th>False alarms</th>
          </tr>
        </thead>
        <tbody>
          {outcomes.map((o) => (
            <tr key={o.set + o.scenario} className="row-link" onClick={() => onOpen(o)}>
              <td>
                <button className="button--quiet" style={{ border: 0, padding: 0, fontWeight: 700, textAlign: "left" }} onClick={() => onOpen(o)}>
                  {o.scenario.replaceAll("_", " ")}
                </button>
                <div className="panel__note">{o.about}</div>
              </td>
              <td className="cell-sql" style={{ whiteSpace: "nowrap" }}>{o.target ?? "none (control)"}</td>
              <td className="num">{o.control ? "–" : o.detected ? `after ${seconds(o.detectSeconds)}` : mark(false, "", "missed")}</td>
              <td>
                {o.control ? "–" : o.causeAtOpen ? (
                  <>
                    <CauseTag code={o.causeAtOpen} /> {mark(o.causeCorrect, "right", `expected ${o.wantCause}`)}
                  </>
                ) : (
                  "–"
                )}
              </td>
              <td>{o.control ? "–" : mark(o.suggestionCorrect)}</td>
              <td className="num">{o.control ? "–" : o.resolved ? `after ${seconds(o.resolveSeconds)}` : mark(false, "", "no")}</td>
              <td>{(o.falseAlarms ?? []).length ? <span className="mark-no">{(o.falseAlarms ?? []).join(", ")}</span> : "none"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function EvaluationPage({ results }: { results: Resource<Results> & { retry: () => void } }) {
  if (results.state === "loading") return <SkeletonRows label="Loading the evaluation" />;
  if (results.state === "error") return <ErrorState error={results.error} onRetry={results.retry} what="the evaluation" />;
  const r = results.data;
  const open = (o: Outcome) => {
    navigate("/", `${o.set}-${o.scenario}`);
  };
  const all = [...(r.dev?.outcomes ?? []), ...(r.heldout?.outcomes ?? [])];
  return (
    <>
      <header className="page-head">
        <h1>Faults injected into a real PostgreSQL, and what the collector made of them</h1>
        <p className="page-head__lead">
          Each scenario seeds a fresh schema, runs an application workload, breaks something for {""}
          a while and then fixes it. The development set was used to tune the detector; the held-out set, a different
          application with different faults, was pinned before tuning and run {r.heldoutRuns === 1 ? "once" : `${r.heldoutRuns} times`}.
        </p>
      </header>
      <div className="scoreboard">
        <Scoreboard report={r.dev} title="Development set" note="shop app, tuned on" />
        <Scoreboard report={r.heldout} title="Held-out set" note={`billing app, pin ${r.heldoutPin.slice(0, 12)}`} />
      </div>
      {r.overhead && (
        <Panel title="Cost of watching" note={`${r.overhead.rounds} rounds of ${r.overhead.secondsPerPhase} s each way`}>
          <p style={{ maxWidth: "72ch" }}>
            A closed loop of primary-key lookups ran {Math.round(r.overhead.offMeanQps).toLocaleString("en")} queries a second
            with the collector off and {Math.round(r.overhead.onMeanQps).toLocaleString("en")} with it on: {r.overhead.overheadPct.toFixed(1)}% fewer.
            One collection takes {r.overhead.tickMeanMs.toFixed(1)} ms of the one-second interval.
          </p>
        </Panel>
      )}
      <Panel title="Every scenario" note="Select one to replay what the collector saw">
        {all.length ? <ScenarioTable outcomes={all} onOpen={open} /> : <EmptyState title="No scenario has run yet" />}
      </Panel>
    </>
  );
}
