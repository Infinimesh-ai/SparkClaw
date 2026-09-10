// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { api } from "../api/client";
import { useEmailLoginAlert } from "./useEmailLoginAlert";

let root: ReturnType<typeof createRoot>, container: HTMLElement;
function Alert() { return <span>{String(useEmailLoginAlert())}</span>; }
beforeEach(() => { vi.useFakeTimers(); container = document.createElement("div"); document.body.append(container); root = createRoot(container); });
afterEach(() => { act(() => root.unmount()); container.remove(); vi.restoreAllMocks(); vi.useRealTimers(); });
it("polls without an open popup, retains expiry through failed reads, and clears only after recovery", async () => {
  const box = { id: "box", version: 1, provider: "gmail" as const, address: "a@example.test", active_binding: true, intake_enabled: true, state: "login_required" };
  const fetch = vi.spyOn(api, "emailSyncStatus").mockResolvedValue({ version: 1, mailboxes: [box], backlog: 0, pending_count: 0 });
  await act(async () => root.render(<Alert />));
  expect(container.textContent).toBe("true");
  fetch.mockRejectedValueOnce(new Error("offline"));
  await act(async () => vi.advanceTimersByTimeAsync(5000));
  expect(container.textContent).toBe("true");
  fetch.mockResolvedValue({ version: 2, mailboxes: [{ ...box, state: "active" }, { ...box, id: "other" }], backlog: 0, pending_count: 0 });
  await act(async () => vi.advanceTimersByTimeAsync(5000));
  expect(container.textContent).toBe("true");
  fetch.mockResolvedValue({ version: 3, mailboxes: [{ ...box, state: "active" }, { ...box, id: "other", intake_enabled: false }], backlog: 0, pending_count: 0 });
  await act(async () => vi.advanceTimersByTimeAsync(5000));
  expect(container.textContent).toBe("false");
});
