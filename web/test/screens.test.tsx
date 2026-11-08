import { describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SourceProvider } from "../src/features/source/SourceContext";
import { InsightsPage } from "../src/features/insights/InsightsPage";
import { InsightDetail } from "../src/features/insights/InsightDetail";
import { QueriesPage } from "../src/features/queries/QueriesPage";
import { EvaluationPage } from "../src/features/evaluation/EvaluationPage";
import { ApiError } from "../src/shared/api/client";
import type { Source } from "../src/features/source/source";
import { fakeSource, results } from "./fixtures";

function withSource(source: Source, ui: React.ReactNode) {
  return render(
    <SourceProvider source={source} choose={() => {}}>
      {ui}
    </SourceProvider>,
  );
}

describe("insights page", () => {
  it("shows a skeleton, then leads with the worst open regression", async () => {
    withSource(fakeSource(), <InsightsPage />);
    expect(screen.getByRole("status")).toHaveTextContent("Loading insights");
    expect(await screen.findByRole("heading", { level: 1 })).toHaveTextContent("customer_orders became 269× slower");
    expect(screen.getAllByRole("link")).toHaveLength(2);
  });

  it("filters to fixed insights and says when a filter is empty", async () => {
    const user = userEvent.setup();
    withSource(fakeSource(), <InsightsPage />);
    await screen.findByRole("heading", { level: 1 });
    await user.click(screen.getByRole("button", { name: "Fixed" }));
    const list = screen.getByRole("list");
    expect(within(list).getAllByRole("listitem")).toHaveLength(1);
    expect(within(list).getByText("Fixed")).toBeInTheDocument();
  });

  it("explains an empty recording", async () => {
    withSource(fakeSource({ insights: async () => [] }), <InsightsPage />);
    expect(await screen.findByText("Nothing to look at yet")).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("No query has regressed");
  });

  it("offers a retry when loading fails, and recovers", async () => {
    const user = userEvent.setup();
    const insights = vi
      .fn()
      .mockRejectedValueOnce(new ApiError("Could not reach the server. Is qpi running?", null, true))
      .mockResolvedValue([]);
    withSource(fakeSource({ insights }), <InsightsPage />);
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not reach the server");
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("Nothing to look at yet")).toBeInTheDocument();
    expect(insights).toHaveBeenCalledTimes(2);
  });

  it("does not offer a retry that cannot help", async () => {
    withSource(fakeSource({ insights: async () => Promise.reject(new ApiError("Not found.", 404, false)) }), <InsightsPage />);
    await screen.findByRole("alert");
    expect(screen.queryByRole("button", { name: "Try again" })).not.toBeInTheDocument();
  });
});

describe("insight detail", () => {
  it("shows the cause, the plan change, the evidence and the fix", async () => {
    withSource(fakeSource(), <InsightDetail id={1} />);
    expect(await screen.findByRole("heading", { level: 1 })).toHaveTextContent("customer_orders");
    expect(screen.getByText("Index dropped")).toBeInTheDocument();
    expect(screen.getByText(/strong evidence/)).toBeInTheDocument();
    const before = screen.getByRole("list", { name: "Plan before" });
    const after = screen.getByRole("list", { name: "Plan after" });
    expect(within(before).getByText(/Bitmap Heap Scan on orders/).closest("li")).toHaveClass("plan__line--before");
    expect(within(after).getByText(/Seq Scan on orders/).closest("li")).toHaveClass("plan__line--after");
    expect(screen.getByText(/CREATE INDEX CONCURRENTLY orders_customer_idx/)).toBeInTheDocument();
    expect(screen.getByText("4 s")).toBeInTheDocument(); // fault at :19, insight opened at :23
  });

  it("copies the suggested SQL", async () => {
    const user = userEvent.setup();
    const writeText = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue();
    withSource(fakeSource(), <InsightDetail id={1} />);
    await user.click(await screen.findByRole("button", { name: "Copy SQL" }));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("CREATE INDEX CONCURRENTLY"));
    expect(await screen.findByRole("button", { name: "Copied" })).toBeInTheDocument();
  });

  it("says when an insight does not exist", async () => {
    withSource(fakeSource(), <InsightDetail id={99} />);
    expect(await screen.findByText("There is no insight 99 here")).toBeInTheDocument();
  });
});

describe("queries page", () => {
  it("names queries the workload knows and links regressions to their insight", async () => {
    withSource(fakeSource(), <QueriesPage />);
    const table = await screen.findByRole("table");
    expect(within(table).getByRole("link", { name: "customer_orders" })).toHaveAttribute("href", expect.stringContaining("/queries/-5603298469950051314"));
    expect(within(table).getByRole("link", { name: "Regressed" })).toHaveAttribute("href", expect.stringContaining("/insights/1"));
  });
});

describe("evaluation page", () => {
  it("shows both sets, the overhead and every scenario", () => {
    render(<EvaluationPage results={{ state: "ready", data: results, retry: () => {} }} />);
    expect(screen.getByRole("heading", { name: "Development set" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Held-out set" })).toBeInTheDocument();
    expect(screen.getAllByText("1 of 1").length).toBeGreaterThan(0);
    expect(screen.getByText(/1.1% fewer/)).toBeInTheDocument();
    const rows = screen.getAllByRole("row");
    expect(rows).toHaveLength(4); // header + 3 scenarios
    expect(within(rows[3]!).getByText("account")).toBeInTheDocument();
  });

  it("shows loading and error states", async () => {
    const user = userEvent.setup();
    const retry = vi.fn();
    const { rerender } = render(<EvaluationPage results={{ state: "loading", retry }} />);
    expect(screen.getByRole("status")).toHaveTextContent("Loading the evaluation");
    rerender(<EvaluationPage results={{ state: "error", error: new ApiError("not recorded yet: run qpi-eval", 404, false), retry }} />);
    expect(screen.getByRole("alert")).toHaveTextContent("not recorded yet");
    rerender(<EvaluationPage results={{ state: "error", error: new Error("boom"), retry }} />);
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(retry).toHaveBeenCalled();
  });
});
