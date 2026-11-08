import { motion } from "framer-motion";
import type { Insight } from "../../shared/api/types";
import { CauseTag, causeTone } from "../../shared/ui/CauseTag";
import { clock, ms } from "../../shared/format";
import { headline } from "./sentence";
import { motion as m } from "../../theme/tokens";

export function InsightRow({ insight, name, link }: { insight: Insight; name: string; link: string }) {
  const tone = causeTone(insight.cause.code);
  const open = insight.status === "OPEN";
  return (
    <motion.li className="insight-row" layout="position" transition={{ duration: m.settle, ease: m.ease }}>
      <a href={link}>
        <span className="insight-row__bar" style={{ background: open ? tone.fg : "transparent", border: open ? 0 : `1px solid ${tone.fg}` }} />
        <div>
          <div className="insight-row__title">{headline(name, insight)}</div>
          <div className="insight-row__meta">
            <CauseTag code={insight.cause.code} />
            <span className={`status ${open ? "status--open" : "status--resolved"}`}>{open ? "Open" : "Fixed"}</span>
            <span>opened {clock(insight.openedAt)}</span>
            {insight.resolvedAt && <span>fixed {clock(insight.resolvedAt)}</span>}
          </div>
        </div>
        <div className="insight-row__impact">
          {open ? (
            <>
              <strong>{ms(insight.impactMsPerSec)}</strong>extra per second
            </>
          ) : (
            <>
              <strong>{ms(insight.resolvedMs)}</strong>now per call
            </>
          )}
        </div>
      </a>
    </motion.li>
  );
}
