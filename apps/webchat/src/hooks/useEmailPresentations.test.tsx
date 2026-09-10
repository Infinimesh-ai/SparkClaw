// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { EmailPresentation } from "../api/email";
import { useEmailPresentations } from "./useEmailPresentations";

function Probe({ language }: { language: "en" | "zh" }) {
  const p = useEmailPresentations("mail", ["m"], language);
  return <><p>{p.items.m?.summary}</p><button onClick={() => void p.retry()}>retry</button></>;
}
afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers(); });
describe("language presentation identity", () => {
  it("ignores a late English response after switching to Chinese", async () => {
    vi.useFakeTimers();
    let finish!: (v: { items: EmailPresentation[] }) => void;
    vi.spyOn(api, "emailPresentations").mockImplementation(async (_kind, _ids, language) => language === "en" ? new Promise((resolve) => { finish = resolve; }) : { items: [{ target_kind: "mail", target_id: "m", presentation_language: "zh", state: "ready", revision: 1, analysis_revision: "r", summary: "中文摘要" }] });
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<Probe language="en" />));
      await act(async () => root.render(<Probe language="zh" />));
      await act(async () => finish({ items: [{ target_kind: "mail", target_id: "m", presentation_language: "en", state: "ready", revision: 99, analysis_revision: "r", summary: "Late English" }] }));
      expect(host.textContent).toContain("中文摘要"); expect(host.textContent).not.toContain("Late English");
    } finally { act(() => root.unmount()); }
  });
  it("does not silently rearm failed generation during polling", async () => {
    vi.useFakeTimers();
    vi.spyOn(api, "emailPresentations").mockResolvedValue({ items: [{ target_kind: "mail", target_id: "m", presentation_language: "zh", state: "failed", revision: 1, analysis_revision: "r" }] });
    const ensure = vi.spyOn(api, "ensureEmailPresentations").mockResolvedValue({ items: [] });
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<Probe language="zh" />));
      await act(async () => vi.advanceTimersByTimeAsync(10000));
      expect(ensure).not.toHaveBeenCalled();
      await act(async () => host.querySelector("button")!.click());
      expect(ensure).toHaveBeenCalledWith("mail", ["m"], "zh", true, expect.any(AbortSignal));
    } finally { act(() => root.unmount()); }
  });
  it("removes stale text when newer source content needs a fresh presentation", async () => {
    vi.useFakeTimers();
    const read = vi.spyOn(api, "emailPresentations").mockResolvedValue({ items: [{ target_kind: "mail", target_id: "m", presentation_language: "zh", state: "ready", revision: 99, analysis_revision: "old-source", summary: "旧摘要" }] });
    vi.spyOn(api, "ensureEmailPresentations").mockResolvedValue({ items: [{ target_kind: "mail", target_id: "m", presentation_language: "zh", state: "queued", revision: 1, analysis_revision: "new-source" }] });
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<Probe language="zh" />));
      expect(host.textContent).toContain("旧摘要");
      read.mockResolvedValue({ items: [{ target_kind: "mail", target_id: "m", presentation_language: "zh", state: "missing", revision: 0, analysis_revision: "new-source" }] });
      await act(async () => vi.advanceTimersByTimeAsync(5000));
      expect(host.textContent).not.toContain("旧摘要");
    } finally { act(() => root.unmount()); }
  });

});
