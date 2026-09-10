// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { mergeEmailPages, useEmailPages, type EmailPage } from "./useEmailPages";

type Item = { id: string; version: number; title: string };
function page(version: number, id: string, next_cursor?: string): EmailPage<Item> {
  return { version, items: [{ id, version, title: `v${version}` }], next_cursor };
}

describe("versioned email pages", () => {
  let root: ReturnType<typeof createRoot>;
  let container: HTMLElement;
  let current: ReturnType<typeof useEmailPages<Item>>;
  beforeEach(() => {
    vi.useFakeTimers();
    container = document.createElement("div");
    root = createRoot(container);
  });
  afterEach(() => { act(() => root.unmount()); vi.useRealTimers(); });
  function Harness({ load, filter = "all" }: { load: (cursor: string, signal: AbortSignal) => Promise<EmailPage<Item>>; filter?: string }) {
    current = useEmailPages(filter, load);
    return <>{current.items.map((item) => <span key={item.id}>{item.id}:{item.title}</span>)}</>;
  }

  it("retains loaded history on head refresh and ignores an older page response", async () => {
    const load = vi.fn(async (cursor: string) => cursor ? page(2, "history") : page(2, "latest", "older"));
    await act(async () => root.render(<Harness load={load} />));
    await act(async () => current.loadMore());
    expect(container.textContent).toBe("latest:v2history:v2");
    load.mockImplementation(async (cursor) => cursor ? page(2, "history") : page(1, "latest", "older"));
    await act(async () => current.refresh());
    expect(container.textContent).toBe("latest:v2history:v2");
    load.mockImplementation(async (cursor) => cursor ? page(3, "history") : page(3, "new", "older"));
    await act(async () => vi.advanceTimersByTimeAsync(5000));
    expect(container.textContent).toBe("new:v3history:v3");
  });

  it("discards a late response from the previous source filter", async () => {
    let resolve!: (page: EmailPage<Item>) => void;
    const oldLoad = vi.fn(() => new Promise<EmailPage<Item>>((done) => { resolve = done; }));
    const newLoad = vi.fn(async () => page(1, "other-account"));
    await act(async () => root.render(<Harness load={oldLoad} />));
    await act(async () => root.render(<Harness load={newLoad} filter="other" />));
    await act(async () => resolve(page(100, "wrong-account")));
    expect(container.textContent).toBe("other-account:v1");
    expect(oldLoad.mock.calls[0]).toBeDefined();
  });

  it("uses the newest row across overlapping pages without deduplicating different mail IDs", () => {
    const pages = new Map([
      ["", { version: 4, items: [{ id: "102", version: 4, title: "same subject" }] }],
      ["older", { version: 2, items: [{ id: "102", version: 2, title: "stale" }, { id: "101", version: 2, title: "same subject" }] }]
    ]);
    expect(mergeEmailPages(pages)).toEqual([{ id: "102", version: 4, title: "same subject" }, { id: "101", version: 2, title: "same subject" }]);
  });
});
