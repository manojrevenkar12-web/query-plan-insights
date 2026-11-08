import { describe, expect, it, vi } from "vitest";
import { between, ms, oneLine, rate, ratio, seconds } from "../src/shared/format";
import { linearScale, logScale, path } from "../src/shared/charts/scale";
import { planLines } from "../src/features/insights/planLines";
import { headline } from "../src/features/insights/sentence";
import { ApiError, fetchJSON } from "../src/shared/api/client";
import { insight } from "./fixtures";

describe("format", () => {
  it("prints latencies at a precision that suits their size", () => {
    expect(ms(0.0712)).toBe("0.07 ms");
    expect(ms(19.34)).toBe("19.3 ms");
    expect(ms(248.2)).toBe("248 ms");
    expect(ms(1240)).toBe("1.24 s");
    expect(ms(undefined)).toBe("–");
  });
  it("prints ratios, rates and durations", () => {
    expect(ratio(27.3, 0.07)).toBe("390×");
    expect(ratio(2.2, 1)).toBe("2.2×");
    expect(ratio(1, 0)).toBe("–");
    expect(rate(33.4)).toBe("33/s");
    expect(rate(2)).toBe("2.0/s");
    expect(seconds(3)).toBe("3 s");
    expect(seconds(75)).toBe("1 min 15 s");
    expect(between("2026-11-08T12:00:00Z", "2026-11-08T12:00:03.5Z")).toBe(3.5);
    expect(oneLine("SELECT  a\n\tFROM t", 10)).toBe("SELECT a …");
  });
});

describe("scales", () => {
  it("covers whole decades on a log scale", () => {
    const y = logScale(0.07, 27, 100, 0);
    expect(y.ticks).toEqual([0.01, 0.1, 1, 10, 100]);
    expect(y(0.01)).toBe(100);
    expect(y(100)).toBe(0);
  });
  it("maps linearly and breaks paths at gaps", () => {
    expect(linearScale(0, 10, 0, 100)(5)).toBe(50);
    expect(path([[0, 1], [1, 2], null, [3, 4]])).toBe("M0.0,1.0L1.0,2.0M3.0,4.0");
  });
});

describe("plan lines", () => {
  it("marks the node whose access method changed, on both sides", () => {
    const before = planLines(insight.planBefore, insight.planChanges, "before");
    const after = planLines(insight.planAfter, insight.planChanges, "after");
    expect(before.filter((l) => l.changed).map((l) => l.node)).toEqual(["Bitmap Heap Scan on orders"]);
    expect(after.filter((l) => l.changed).map((l) => l.node)).toEqual(["Seq Scan on orders"]);
    expect(after[3]).toMatchObject({ depth: 3, detail: "(rows=4 cost=4496.9)" });
  });
  it("handles a missing plan", () => {
    expect(planLines(undefined, null, "before")).toEqual([]);
  });
});

describe("headline", () => {
  it("states the slowdown with the peak", () => {
    expect(headline("customer_orders", insight)).toBe("customer_orders became 269× slower, 0.08 ms to 21.5 ms");
  });
  it("states a new query by its rate", () => {
    expect(headline("basket_product_each", { ...insight, kind: "NEW_HEAVY_QUERY", callsPerSec: 120.4 })).toBe(
      "basket_product_each is new and runs 120 times a second",
    );
  });
});

describe("fetchJSON", () => {
  const respond = (status: number, body: string) =>
    vi.fn(async () => new Response(body, { status, headers: { "Content-Type": "application/json" } })) as unknown as typeof fetch;

  it("returns the parsed body", async () => {
    await expect(fetchJSON<{ a: number }>("/x", undefined, respond(200, '{"a":1}'))).resolves.toEqual({ a: 1 });
  });
  it("says a missing thing cannot be retried", async () => {
    const err = await fetchJSON("/x", undefined, respond(404, "no such query")).catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 404, retryable: false, message: "no such query" });
  });
  it("says a server error can be retried", async () => {
    const err = await fetchJSON("/x", undefined, respond(503, "")).catch((e) => e);
    expect(err).toMatchObject({ status: 503, retryable: true, message: "The server answered 503." });
  });
  it("explains an unreachable server", async () => {
    const down = vi.fn(async () => {
      throw new TypeError("Failed to fetch");
    }) as unknown as typeof fetch;
    const err = await fetchJSON("/x", undefined, down).catch((e) => e);
    expect(err).toMatchObject({ retryable: true, message: "Could not reach the server. Is qpi running?" });
  });
});
