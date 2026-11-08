import type { PlanChange } from "../../shared/api/types";

export interface PlanLine {
  depth: number;
  node: string;
  detail: string;
  changed: boolean;
}

/**
 * Splits a rendered plan tree into lines and marks the nodes a plan change touched: the access method on a relation,
 * a join type, or an aggregation strategy.
 */
export function planLines(tree: string | undefined, changes: PlanChange[] | null | undefined, side: "before" | "after"): PlanLine[] {
  if (!tree) return [];
  const marks = (changes ?? []).map((c) => ({ kind: c.kind, relation: c.relation, method: side === "before" ? c.before : c.after }));
  return tree
    .split("\n")
    .filter((l) => l.trim() !== "")
    .map((l) => {
      const depth = (l.length - l.trimStart().length) / 2;
      const body = l.trim();
      const cut = body.indexOf("  (");
      const node = cut >= 0 ? body.slice(0, cut) : body;
      const detail = cut >= 0 ? body.slice(cut + 2) : "";
      const changed = marks.some((m) => {
        if (m.kind === "access" && m.relation) {
          const rel = m.relation.split(".").pop() ?? m.relation;
          const method = m.method.split(" using ")[0] ?? m.method;
          return node.includes(` on ${rel}`) && node.startsWith(method);
        }
        if (m.kind === "join") return /Join|Nested Loop/.test(node) && m.method.includes(node.split(" on ")[0] ?? node);
        if (m.kind === "aggregation") return /Aggregate|Sort|Hash$/.test(node);
        return false;
      });
      return { depth, node, detail, changed };
    });
}
