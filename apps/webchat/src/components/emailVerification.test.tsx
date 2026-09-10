// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { api } from "../api/client";
import { dictionaries } from "../i18n";
import { EmailVerification } from "./emailVerification";
afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers(); });
it("does not fetch a code until explicit reveal and expires using server time", async () => {
  vi.useFakeTimers();
  const value = { purpose: "login", state: "not_expired" as const, server_now: "2026-09-09T00:00:00Z", expires_at: "2026-09-09T00:00:02Z", can_reveal: true };
  const reveal = vi.spyOn(api, "emailVerification").mockResolvedValue({ ...value, code: "123456" });
  const host = document.createElement("div"); const root = createRoot(host); const text = dictionaries.zh;
  try {
    await act(async () => root.render(<EmailVerification mailId="m" value={value} text={text} language="zh" />));
    expect(reveal).not.toHaveBeenCalled(); expect(host.textContent).not.toContain("123456");
    await act(async () => host.querySelector("button")!.click());
    expect(reveal).toHaveBeenCalledWith("m"); expect(host.textContent).toContain("123456");
    await act(async () => vi.advanceTimersByTimeAsync(3000));
    expect(host.textContent).toContain(text.email.expired);
  } finally { act(() => root.unmount()); }
});

it("recalibrates expiry on focus without revealing the code", async () => {
  const value = { purpose: "login", state: "not_expired" as const, server_now: "2026-09-09T00:00:00Z", expires_at: "2026-09-09T00:01:00Z", can_reveal: true };
  const reveal = vi.spyOn(api, "emailVerification");
  const detail = vi.spyOn(api, "emailMessage").mockResolvedValue({ id: "m", version: 1, mailbox_id: "b", receiving_address: "a@example.com", direction: "inbound", from: "b@example.com", to: [], cc: [], subject: "code", arrived_at: value.server_now, viewed: true, attachments: [], verification: { ...value, server_now: "2026-09-09T00:02:00Z", state: "expired" } });
  const host = document.createElement("div"); const root = createRoot(host); const text = dictionaries.zh;
  try {
    await act(async () => root.render(<EmailVerification mailId="m" value={value} text={text} language="zh" />));
    await act(async () => window.dispatchEvent(new Event("focus")));
    expect(detail).toHaveBeenCalledWith("m", expect.any(AbortSignal));
    expect(reveal).not.toHaveBeenCalled(); expect(host.textContent).toContain(text.email.expired);
  } finally { act(() => root.unmount()); }
});
