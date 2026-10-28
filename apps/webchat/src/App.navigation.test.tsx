// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";
import { api } from "./api/client";
import { LANGUAGE_STORAGE_KEY } from "./i18n";
import type { WorkbenchDraft } from "./lib/workbenchDraft";

vi.mock("./api/client", async importOriginal => {
  const actual = await importOriginal<typeof import("./api/client")>();
  const owner = { id: "owner", display_name: "Test", created_at: "", updated_at: "" };
  const responses: Record<string, unknown> = {
    config: { speech: { default_language: "auto" } }, ready: { ok: true }, owner, updateLanguage: owner,
    sessions: { sessions: [{ id: "existing-conversation", title: "Keep this conversation", source: "webchat", created_at: "", updated_at: "" }] },
    messages: { messages: [{ id: "existing-message", role: "user", content: "Keep this message", created_at: "" }] },
    workbenchDraft: { content: "", attachment_ids: [], revision: 0 }, workbenchRequests: [],
    approvals: { approvals: [] }, memoryCandidates: { memory_candidates: [] },
  };
  const mocked: Record<string, ReturnType<typeof vi.fn>> = Object.fromEntries(Object.keys(actual.api).map(name => [name, vi.fn(async () => responses[name] ?? {})]));
  mocked.saveWorkbenchDraft = vi.fn(async (_sessionId: string, value: WorkbenchDraft) => ({ ...value, revision: value.revision + 1 }));
  return { ...actual, api: mocked,
    streamPassiveNotifications: vi.fn((_cursor: string, options: { signal: AbortSignal }) => new Promise<void>(resolve => options.signal.addEventListener("abort", () => resolve(), { once: true }))),
  };
});

beforeEach(() => {
  const values = new Map<string, string>();
  Object.defineProperty(window, "localStorage", { configurable: true, value: {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value), removeItem: (key: string) => values.delete(key),
    clear: () => values.clear(),
  } });
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
  Object.defineProperty(window, "matchMedia", { configurable: true, value: vi.fn(() => ({ matches: false, addEventListener() {}, removeEventListener() {} })) });
});

afterEach(() => { vi.unstubAllGlobals(); vi.clearAllMocks(); });

describe("host schedule navigation", () => {
  it.each(["en", "zh"] as const)("returns to the original conversation and keeps the draft without a session mutation in %s", async language => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, language);
    const host = document.createElement("div"), root = createRoot(host);
    try {
      await act(async () => root.render(<App />));
      expect(host.querySelector(".messageList")?.textContent).toContain("Keep this message");
      const input = host.querySelector<HTMLTextAreaElement>("form.composer textarea")!;
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, "Unsent original draft");
        input.dispatchEvent(new Event("input", { bubbles: true }));
      });
      await act(async () => host.querySelector<HTMLButtonElement>(".sidebarScheduleLink")!.click());
      const back = host.querySelector<HTMLButtonElement>(".topbar .workspaceBackButton")!;
      expect(back.getAttribute("aria-label")).toBe(language === "zh" ? "返回首页" : "Back to home");
      await act(async () => back.click());
      expect(host.querySelector(".schedulePage")).toBeNull();
      expect(host.querySelector(".messageList")?.textContent).toContain("Keep this message");
      expect(host.querySelector(".sessionItem.active .sessionSelect")?.textContent).toContain("Keep this conversation");
      expect(host.querySelector<HTMLTextAreaElement>("form.composer textarea")!.value).toBe("Unsent original draft");
      expect(api.createSession).not.toHaveBeenCalled(); expect(api.deleteSession).not.toHaveBeenCalled();
      expect(api.sendMessageStream).not.toHaveBeenCalled();
    } finally { await act(async () => root.unmount()); }
  });
});

describe("host archive navigation", () => {
  it("opens a new conversation once when the open one is archived", async () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, "en");
    vi.mocked(api.createSession).mockResolvedValueOnce({ id: "new-conversation", title: "New conversation", source: "webchat", created_at: "", updated_at: "" });
    const host = document.createElement("div"), root = createRoot(host);
    try {
      await act(async () => root.render(<App />));
      expect(host.querySelector(".sessionItem.active .sessionSelect")?.textContent).toContain("Keep this conversation");
      await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Archive conversation: Keep this conversation"]')!.click());
      expect(JSON.parse(window.localStorage.getItem("sparkx.archived-conversations.v1.owner")!)).toEqual(["existing-conversation"]);
      expect(api.createSession).toHaveBeenCalledOnce();
      expect([...host.querySelectorAll(".sessionTitle")].map(title => title.textContent)).toEqual(["New conversation"]);
      expect(host.querySelector(".sessionItem.active .sessionSelect")?.textContent).toContain("New conversation");
      expect(host.querySelector(".messageList")?.textContent).not.toContain("Keep this message");
    } finally { await act(async () => root.unmount()); }
  });
});
