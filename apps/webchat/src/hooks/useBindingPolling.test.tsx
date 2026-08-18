// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { NotificationBinding } from "../api/types";
import { useBindingPolling } from "./useBindingPolling";

function binding(status: string): NotificationBinding {
  return {
    id: "binding-a",
    owner_id: "owner",
    channel: "weixin",
    provider: "openclaw-weixin-qr",
    status,
    default_for_channel: false,
    scopes: [],
    created_at: "2026-08-18T00:00:00Z",
    updated_at: "2026-08-18T00:00:00Z"
  };
}

describe("useBindingPolling", () => {
  let root: ReturnType<typeof createRoot> | undefined;

  beforeEach(() => vi.useFakeTimers());

  afterEach(() => {
    if (root) act(() => root?.unmount());
    root = undefined;
    vi.useRealTimers();
  });

  function render(refreshBinding: (id: string, signal?: AbortSignal) => Promise<NotificationBinding>, onError = vi.fn()) {
    const container = document.createElement("div");
    root = createRoot(container);
    function Harness() {
      useBindingPolling({
        pendingBindingKey: JSON.stringify(["binding-a"]),
        refreshBinding,
        fallbackError: "Binding failed",
        onError
      });
      return null;
    }
    act(() => root?.render(<Harness />));
    return onError;
  }

  it("continues after two seconds while pending and stops at a terminal state", async () => {
    const refresh = vi.fn()
      .mockResolvedValueOnce(binding("waiting_scan"))
      .mockResolvedValueOnce(binding("active"));
    render(refresh);

    await act(async () => vi.advanceTimersByTimeAsync(1000));
    expect(refresh).toHaveBeenCalledTimes(1);
    await act(async () => vi.advanceTimersByTimeAsync(1999));
    expect(refresh).toHaveBeenCalledTimes(1);
    await act(async () => vi.advanceTimersByTimeAsync(1));
    expect(refresh).toHaveBeenCalledTimes(2);
    await act(async () => vi.advanceTimersByTimeAsync(5000));
    expect(refresh).toHaveBeenCalledTimes(2);
  });

  it("reports a rejected refresh and retries after four seconds", async () => {
    const refresh = vi.fn()
      .mockRejectedValueOnce(new Error("provider unavailable"))
      .mockResolvedValueOnce(binding("active"));
    const onError = render(refresh);

    await act(async () => vi.advanceTimersByTimeAsync(1000));
    expect(onError).toHaveBeenCalledWith("provider unavailable");
    await act(async () => vi.advanceTimersByTimeAsync(3999));
    expect(refresh).toHaveBeenCalledTimes(1);
    await act(async () => vi.advanceTimersByTimeAsync(1));
    expect(refresh).toHaveBeenCalledTimes(2);
  });
});
