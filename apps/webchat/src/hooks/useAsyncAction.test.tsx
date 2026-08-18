// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useAsyncAction } from "./useAsyncAction";

type HarnessValue = ReturnType<typeof useAsyncAction>;

describe("useAsyncAction", () => {
  const roots: ReturnType<typeof createRoot>[] = [];

  afterEach(() => {
    for (const root of roots.splice(0)) act(() => root.unmount());
  });

  function renderHook(options: Parameters<typeof useAsyncAction>[0] = {}) {
    const container = document.createElement("div");
    const root = createRoot(container);
    roots.push(root);
    let value: HarnessValue | undefined;
    function Harness() {
      value = useAsyncAction(options);
      return null;
    }
    act(() => root.render(<Harness />));
    return { root, value: () => value as HarnessValue };
  }

  it("tracks the action token and clears it after success", async () => {
    let resolveTask: (() => void) | undefined;
    const task = new Promise<void>((resolve) => { resolveTask = resolve; });
    const hook = renderHook();

    let completion: Promise<void | undefined> | undefined;
    act(() => { completion = hook.value().run("record-a", () => task); });
    expect(hook.value().busy).toBe("record-a");
    await act(async () => { resolveTask?.(); await completion; });
    expect(hook.value().busy).toBe("");
  });

  it("maps failures and clears configured errors before running", async () => {
    const clearError = vi.fn();
    const onError = vi.fn();
    const hook = renderHook({ clearError, onError });
    const failure = new Error("failed");

    await act(async () => { await hook.value().run("save", async () => { throw failure; }); });

    expect(clearError).toHaveBeenCalledOnce();
    expect(onError).toHaveBeenCalledWith(failure);
    expect(hook.value().busy).toBe("");
  });

  it("ignores a second action while the current render is busy", async () => {
    let resolveTask: (() => void) | undefined;
    const firstTask = new Promise<void>((resolve) => { resolveTask = resolve; });
    const second = vi.fn(async () => {});
    const hook = renderHook();

    let completion: Promise<void | undefined> | undefined;
    act(() => { completion = hook.value().run("first", () => firstTask); });
    await act(async () => { await hook.value().run("second", second); });
    expect(second).not.toHaveBeenCalled();
    await act(async () => { resolveTask?.(); await completion; });
  });

  it("does not write state or report errors after unmount", async () => {
    let rejectTask: ((error: Error) => void) | undefined;
    const task = new Promise<void>((_resolve, reject) => { rejectTask = reject; });
    const onError = vi.fn();
    const hook = renderHook({ onError });

    let completion: Promise<void | undefined> | undefined;
    act(() => { completion = hook.value().run("save", () => task); });
    act(() => hook.root.unmount());
    roots.splice(roots.indexOf(hook.root), 1);
    await act(async () => { rejectTask?.(new Error("late")); await completion; });
    expect(onError).not.toHaveBeenCalled();
  });
});
