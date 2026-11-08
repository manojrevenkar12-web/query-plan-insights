import { createContext, useContext, type ReactNode } from "react";
import type { Source } from "./source";

const Ctx = createContext<{ source: Source; choose: (id: string) => void } | null>(null);

export function SourceProvider({ source, choose, children }: { source: Source; choose: (id: string) => void; children: ReactNode }) {
  return <Ctx.Provider value={{ source, choose }}>{children}</Ctx.Provider>;
}

export function useSource() {
  const v = useContext(Ctx);
  if (!v) throw new Error("useSource outside SourceProvider");
  return v;
}

/** The workload's name for a query when the source knows it (recordings do), otherwise its first words. */
export function queryName(source: Source, id: string, text: string): string {
  return source.names[id] ?? text.replace(/\s+/g, " ").slice(0, 48);
}
