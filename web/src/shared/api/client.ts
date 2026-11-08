import { net } from "../../theme/tokens";

/** An error the interface can explain: what failed and whether trying again can help. */
export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number | null,
    readonly retryable: boolean,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export async function fetchJSON<T>(path: string, signal?: AbortSignal, fetcher: typeof fetch = fetch): Promise<T> {
  const timeout = new AbortController();
  const timer = setTimeout(() => timeout.abort(), net.timeoutMs);
  const onAbort = () => timeout.abort();
  signal?.addEventListener("abort", onAbort);
  try {
    const res = await fetcher(path, { signal: timeout.signal, headers: { Accept: "application/json" } });
    if (!res.ok) {
      const body = (await res.text()).trim();
      if (res.status === 404) throw new ApiError(body || "Not found.", 404, false);
      throw new ApiError(body || `The server answered ${res.status}.`, res.status, res.status >= 500);
    }
    return (await res.json()) as T;
  } catch (e) {
    if (e instanceof ApiError) throw e;
    if (signal?.aborted) throw e;
    if (timeout.signal.aborted) throw new ApiError("The server took too long to answer.", null, true);
    throw new ApiError("Could not reach the server. Is qpi running?", null, true);
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener("abort", onAbort);
  }
}
