// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import { useEmailViewing } from "./useEmailViewing";

const observers: { callback: IntersectionObserverCallback; nodes: Element[] }[] = [];
class Observer {
  record: typeof observers[number];
  constructor(callback: IntersectionObserverCallback) { this.record = { callback, nodes: [] }; observers.push(this.record); }
  observe(node: Element) { this.record.nodes.push(node); }
  disconnect() { this.record.nodes = []; }
}
function present(ids: string[]) {
  const observer = observers.at(-1)!;
  const entries = observer.nodes.filter((node) => ids.includes((node as HTMLElement).dataset.emailMailId!)).map((target) => ({ target, isIntersecting: true, intersectionRatio: 1 }));
  observer.callback(entries as IntersectionObserverEntry[], {} as IntersectionObserver);
}

describe("individual email viewing receipts", () => {
  let root: ReturnType<typeof createRoot>;
  let container: HTMLElement;
  let onError = vi.fn<(reason: unknown) => void>();
  beforeEach(() => {
    vi.useFakeTimers(); observers.length = 0; onError = vi.fn();
    vi.stubGlobal("IntersectionObserver", Observer);
    vi.spyOn(api, "markEmailViewed").mockImplementation(async (ids) => ({ version: 2, mail_ids: ids }));
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
    container = document.createElement("div"); root = createRoot(container);
  });
  afterEach(() => { act(() => root.unmount()); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers(); });
  function Harness({ ids, hiddenCopy = false }: { ids: string[]; hiddenCopy?: boolean }) {
    const [viewport, setViewport] = useState<HTMLDivElement | null>(null);
    const viewed = useEmailViewing(viewport, ids.join(","), () => {}, onError);
    return <div ref={setViewport}>{ids.map((id) => <article key={id} data-email-mail-id={id}>{id}:{String(viewed(id))}</article>)}{hiddenCopy && <details><summary>Other source</summary><article data-email-mail-id="copy-1">Folded source</article></details>}</div>;
  }

  it("leaves mail 101 unseen when only 102 enters the viewport; fetching and folded copies acknowledge nothing", async () => {
    await act(async () => root.render(<Harness ids={["102"]} hiddenCopy />));
    await act(async () => vi.advanceTimersByTimeAsync(500));
    expect(api.markEmailViewed).not.toHaveBeenCalled();
    act(() => present(["102"]));
    await act(async () => vi.advanceTimersByTimeAsync(300));
    expect(api.markEmailViewed).toHaveBeenCalledWith(["102"], expect.any(AbortSignal));
    await act(async () => root.render(<Harness ids={["102", "101"]} />));
    await act(async () => vi.advanceTimersByTimeAsync(600));
    expect(api.markEmailViewed).toHaveBeenCalledTimes(1);
    expect(container.textContent).toContain("101:false");
  });

  it("defers hidden-tab cards, batches at most 100 explicit IDs and accepts idempotent pending-mail replays", async () => {
    const ids = Array.from({ length: 105 }, (_, i) => `pending-${i}`);
    await act(async () => root.render(<Harness ids={ids} />));
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" });
    act(() => present(ids));
    await act(async () => vi.advanceTimersByTimeAsync(600));
    expect(api.markEmailViewed).not.toHaveBeenCalled();
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    await act(async () => vi.advanceTimersByTimeAsync(600));
    expect(vi.mocked(api.markEmailViewed).mock.calls.map(([batch]) => batch.length)).toEqual([100, 5]);
    act(() => present(ids));
    await act(async () => vi.advanceTimersByTimeAsync(600));
    expect(api.markEmailViewed).toHaveBeenCalledTimes(2);
  });

  it("retries a failed receipt without treating the failed attempt as viewed, and cancels on close", async () => {
    vi.mocked(api.markEmailViewed).mockRejectedValueOnce(new Error("offline"));
    await act(async () => root.render(<Harness ids={["pending"]} />));
    act(() => present(["pending"]));
    await act(async () => vi.advanceTimersByTimeAsync(300));
    expect(container.textContent).toContain("pending:false");
    expect(onError).toHaveBeenCalled();
    await act(async () => vi.advanceTimersByTimeAsync(5400));
    expect(container.textContent).toContain("pending:true");
    const signal = vi.mocked(api.markEmailViewed).mock.calls.at(-1)![1];
    act(() => root.render(null));
    expect(signal?.aborted).toBe(true);
  });
});
