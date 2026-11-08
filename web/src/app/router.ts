import { useEffect, useState } from "react";

export interface Route {
  path: string[];
  params: URLSearchParams;
}

export function parse(hash: string): Route {
  const raw = hash.replace(/^#/, "") || "/";
  const [p = "/", q = ""] = raw.split("?");
  return { path: p.split("/").filter(Boolean), params: new URLSearchParams(q) };
}

/** Builds a link that keeps the chosen data source. */
export function href(path: string, src: string): string {
  return `#${path}${src && src !== "live" ? `?src=${encodeURIComponent(src)}` : ""}`;
}

export function useRoute(): Route {
  const [route, setRoute] = useState(() => parse(window.location.hash));
  useEffect(() => {
    const on = () => setRoute(parse(window.location.hash));
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return route;
}

export function navigate(path: string, src: string) {
  window.location.hash = href(path, src).slice(1);
}
