import type { ReactNode } from "react";
import { motion } from "framer-motion";
import { motion as m } from "../theme/tokens";
import { href } from "./router";

export interface NavItem {
  key: string;
  label: string;
  path: string;
  count?: number;
  alert?: boolean;
}

export function Shell({ nav, active, src, children, top }: { nav: NavItem[]; active: string; src: string; children: ReactNode; top: ReactNode }) {
  return (
    <div className="shell">
      <aside className="rail">
        <div className="rail__name">
          Query Plan Insights
          <span>for PostgreSQL</span>
        </div>
        <nav className="rail__nav" aria-label="Sections">
          {nav.map((n) => (
            <a key={n.key} className="rail__link" href={href(n.path, src)} aria-current={active === n.key ? "page" : undefined}>
              {active === n.key && <motion.span className="rail__marker" layoutId="rail-marker" transition={{ duration: m.settle, ease: m.ease }} />}
              <span>{n.label}</span>
              {n.count != null && <span className={`rail__count${n.alert ? " rail__count--alert" : ""}`}>{n.count}</span>}
            </a>
          ))}
        </nav>
        <p className="rail__foot">Reads pg_stat_statements, pg_stat_activity and EXPLAIN plans once a second, as a read-only role.</p>
      </aside>
      <main className="main">
        <div className="main__inner">
          {top}
          {children}
        </div>
      </main>
    </div>
  );
}
