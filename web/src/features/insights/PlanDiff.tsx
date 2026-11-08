import { motion, useReducedMotion } from "framer-motion";
import type { PlanChange } from "../../shared/api/types";
import { motion as m } from "../../theme/tokens";
import { planLines } from "./planLines";

function Tree({ tree, changes, side }: { tree?: string; changes?: PlanChange[] | null; side: "before" | "after" }) {
  const reduce = useReducedMotion();
  const lines = planLines(tree, changes, side);
  if (lines.length === 0) return <p className="panel__note">No plan was captured {side === "before" ? "before the change" : "after the change"}.</p>;
  return (
    <ol className="plan" aria-label={`Plan ${side}`}>
      {lines.map((l, i) => (
        <motion.li
          key={i}
          className={`plan__line${l.changed ? ` plan__line--${side}` : ""}`}
          initial={l.changed && !reduce ? { opacity: 0.35 } : false}
          animate={{ opacity: 1 }}
          transition={{ duration: m.settle, delay: m.draw * 0.6, ease: m.ease }}
        >
          <span className="plan__gutter" aria-hidden="true">
            {l.changed ? (side === "before" ? "−" : "+") : ""}
          </span>
          <span>
            {"  ".repeat(l.depth)}
            {l.node}
            {l.changed && <span className="visually-hidden"> (changed)</span>}
            {"  "}
            <span className="plan__detail">{l.detail}</span>
          </span>
        </motion.li>
      ))}
    </ol>
  );
}

export function PlanDiff({ before, after, changes }: { before?: string; after?: string; changes?: PlanChange[] | null }) {
  return (
    <div className="plans">
      <div>
        <div className="plan__head">
          <span>Before</span>
        </div>
        <Tree tree={before} changes={changes} side="before" />
      </div>
      <div>
        <div className="plan__head">
          <span>After</span>
        </div>
        <Tree tree={after} changes={changes} side="after" />
      </div>
    </div>
  );
}
