import { useCallback, useEffect, useRef, useState } from "react";

export type Resource<T> =
  | { state: "loading"; data?: undefined; error?: undefined }
  | { state: "error"; data?: undefined; error: Error }
  | { state: "ready"; data: T; error?: undefined };

/**
 * Loads data with a function of an abort signal. `key` re-runs it; `refreshMs` polls without flashing the loading
 * state. `retry` starts over.
 */
export function useResource<T>(
  load: (signal: AbortSignal) => Promise<T>,
  key: string,
  refreshMs?: number,
): Resource<T> & { retry: () => void } {
  const [res, setRes] = useState<Resource<T>>({ state: "loading" });
  const [attempt, setAttempt] = useState(0);
  const loadRef = useRef(load);
  loadRef.current = load;

  useEffect(() => {
    const ctrl = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    setRes({ state: "loading" });
    const run = (first: boolean) => {
      loadRef
        .current(ctrl.signal)
        .then((data) => {
          if (ctrl.signal.aborted) return;
          setRes({ state: "ready", data });
          if (refreshMs) timer = setTimeout(() => run(false), refreshMs);
        })
        .catch((error: Error) => {
          if (ctrl.signal.aborted) return;
          // a failed refresh keeps the last good data on screen
          if (first) setRes({ state: "error", error });
          else if (refreshMs) timer = setTimeout(() => run(false), refreshMs);
        });
    };
    run(true);
    return () => {
      ctrl.abort();
      if (timer) clearTimeout(timer);
    };
  }, [key, attempt, refreshMs]);

  const retry = useCallback(() => setAttempt((a) => a + 1), []);
  return { ...res, retry };
}
