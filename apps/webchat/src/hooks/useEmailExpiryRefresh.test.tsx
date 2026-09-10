// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import type { EmailMessage } from "../api/email";
import { useEmailExpiryRefresh } from "./useEmailExpiryRefresh";
afterEach(() => vi.useRealTimers());
it.each([2000, 30 * 24 * 60 * 60 * 1000])("resets a frozen validity page once after %i ms without losing the selection", async (lifetime) => {
  vi.useFakeTimers();
  const refresh = vi.fn();
  const messages = [{ id: "code", verification: { expires_at: new Date(Date.parse("2026-09-09T00:00:00Z") + lifetime).toISOString() } }] as EmailMessage[];
  function Harness() { useEmailExpiryRefresh(true, messages, "2026-09-09T00:00:00Z", refresh); return <span>selected mail</span>; }
  const host = document.createElement("div"); const root = createRoot(host);
  try {
    await act(async () => root.render(<Harness />));
    await act(async () => vi.advanceTimersByTimeAsync(lifetime - 1000)); expect(refresh).not.toHaveBeenCalled();
    await act(async () => vi.advanceTimersByTimeAsync(1100)); expect(refresh).toHaveBeenCalledTimes(1);
    await act(async () => window.dispatchEvent(new Event("focus"))); expect(refresh).toHaveBeenCalledTimes(1);
    expect(host.textContent).toBe("selected mail");
  } finally { act(() => root.unmount()); }
});
