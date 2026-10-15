// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useConversationActivity } from "./useConversationActivity";

beforeEach(() => {
  vi.useFakeTimers(); vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  // Node 26 exposes its own file-less localStorage, so keep viewer state in memory.
  const stored = new Map<string, string>();
  Object.defineProperty(window, "localStorage", { configurable: true, value: {
    getItem: (key: string) => stored.get(key) ?? null,
    setItem: (key: string, value: string) => stored.set(key, value),
    clear: () => stored.clear(),
  } });
});
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });

describe("background conversation activity", () => {
  it("refreshes inactive conversations, preserves status on read failure and coalesces change events", async () => {
    let state = "running", unavailable = false;
    const read = vi.fn(async () => { if (unavailable) throw new Error("Disconnected"); return [state]; });
    let changed = () => {};
    const unsubscribe = vi.fn();
    const subscribe = (listener: () => void) => { changed = listener; return unsubscribe; };
    function Harness() { const activity = useConversationActivity(["background"], read, "owner", subscribe); return <span>{activity.background ?? "idle"}</span>; }
    const host = document.createElement("div"), root = createRoot(host);
    try {
      await act(async () => root.render(<Harness />)); expect(host.textContent).toBe("running");
      unavailable = true;
      await act(async () => { changed(); changed(); changed(); await vi.advanceTimersByTimeAsync(80); });
      expect(read).toHaveBeenCalledTimes(2); expect(host.textContent).toBe("running");
      unavailable = false; state = "delivered";
      await act(async () => { changed(); await vi.advanceTimersByTimeAsync(80); });
      expect(host.textContent).toBe("completed");
      state = "unknown";
      await act(async () => vi.advanceTimersByTimeAsync(3000)); expect(host.textContent).toBe("idle");
    } finally { await act(async () => root.unmount()); }
    expect(unsubscribe).toHaveBeenCalledOnce();
  });

  it("bounds reads to four and discards late results from the previous identity", async () => {
    const finishes: Array<(states: string[]) => void> = [];
    const oldRead = vi.fn(() => new Promise<string[]>(resolve => { finishes.push(resolve); }));
    const newRead = vi.fn(async () => ["failed"]);
    function Harness({ scope, read }: { scope: string; read: (id: string) => Promise<string[]> }) {
      const activity = useConversationActivity(["a", "b", "c", "d", "e"], read, scope);
      return <span>{activity.a ?? "idle"}</span>;
    }
    const host = document.createElement("div"), root = createRoot(host);
    try {
      await act(async () => root.render(<Harness scope="old" read={oldRead} />));
      expect(oldRead).toHaveBeenCalledTimes(4);
      await act(async () => root.render(<Harness scope="new" read={newRead} />));
      expect(host.textContent).toBe("idle"); expect(newRead).toHaveBeenCalledTimes(5);
      await act(async () => finishes.forEach(finish => finish(["delivered"])));
      expect(host.textContent).toBe("idle"); expect(oldRead).toHaveBeenCalledTimes(4);
    } finally { await act(async () => root.unmount()); }
  });

  it("clears a completion once its conversation is viewed until a newer execution completes", async () => {
    let states = ["delivered"];
    const read = vi.fn(async () => states);
    function Harness({ viewing }: { viewing: string }) {
      const activity = useConversationActivity(["a"], read, "owner", undefined, viewing);
      return <span>{activity.a ?? "idle"}</span>;
    }
    const host = document.createElement("div"), root = createRoot(host);
    try {
      await act(async () => root.render(<Harness viewing="" />)); expect(host.textContent).toBe("completed");
      await act(async () => root.render(<Harness viewing="a" />)); expect(host.textContent).toBe("idle");
      await act(async () => root.render(<Harness viewing="" />)); expect(host.textContent).toBe("idle");
      states = ["running", "delivered"];
      await act(async () => vi.advanceTimersByTimeAsync(3100)); expect(host.textContent).toBe("running");
      states = ["delivered", "delivered"];
      await act(async () => vi.advanceTimersByTimeAsync(3100)); expect(host.textContent).toBe("completed");
    } finally { await act(async () => root.unmount()); }
    const remounted = createRoot(host);
    states = ["delivered"];
    try {
      await act(async () => remounted.render(<Harness viewing="" />)); expect(host.textContent).toBe("idle");
    } finally { await act(async () => remounted.unmount()); }
  });
});
