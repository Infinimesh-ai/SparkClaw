// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LocalWorkbench } from "./LocalWorkbench";
import type { SparkClawDesktop } from "./types";
import { LANGUAGE_STORAGE_KEY } from "../i18n";

beforeEach(() => {
  const values = new Map<string, string>();
  Object.defineProperty(window, "localStorage", { configurable: true, value: {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
    clear: () => values.clear(),
  } });
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
});

afterEach(() => {
  delete window.sparkclawClientStore; delete window.sparkclawDesktop;
  vi.unstubAllGlobals(); vi.restoreAllMocks(); window.localStorage.clear();
});

describe("R3 local workbench", () => {
  it("saves locally without loading shared history or submitting tasks, and preserves input on disk failure", async () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, "en");
    const row = { id: "conversation", title: "Local conversation", created_at: "", updated_at: "" };
    const fetch = vi.fn(() => { throw new Error("unexpected network request"); });
    vi.stubGlobal("fetch", fetch);
    const enqueue = vi.fn().mockRejectedValueOnce(new Error("Disk full"))
      .mockResolvedValue({ id: "task", request_id: "request", status: "awaiting_runtime", created_at: "" });
    window.sparkclawClientStore = { schemaVersion: 1, list: vi.fn(async () => [row]), create: vi.fn(async () => row),
      read: vi.fn(async () => ({ messages: [], tasks: [], files: [] })), enqueue,
      saveFile: vi.fn(), exportFile: vi.fn(), submit: vi.fn(), reconcile: vi.fn(), cancel: vi.fn(), scheduleCreate: vi.fn(), scheduleCheck: vi.fn(), scheduleCancel: vi.fn(), scheduleRunNow: vi.fn() };
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1,
      localConnection: vi.fn(async () => ({ schema_version: 1, state: "connected", client_id: "client" })) } as unknown as SparkClawDesktop;
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<LocalWorkbench />));
      const rowButton = [...host.querySelectorAll<HTMLButtonElement>("nav button")].find((button) => button.textContent === row.title)!;
      await act(async () => rowButton.click());
      const input = host.querySelector<HTMLTextAreaElement>("#localDraft")!;
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, "Keep this local input");
        input.dispatchEvent(new Event("input", { bubbles: true }));
      });
      await act(async () => host.querySelector("form.localComposer")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
      expect(host.textContent).toContain("Disk full");
      expect(input.value).toBe("Keep this local input");
      await act(async () => host.querySelector("form.localComposer")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
      expect(host.textContent).toContain("Execution has not been submitted");
      expect(enqueue).toHaveBeenLastCalledWith(row.id, "Keep this local input");
      expect(fetch).not.toHaveBeenCalled();
    } finally { await act(async () => root.unmount()); }
  });

  it("submits only by explicit action, refreshes durable state, and keeps unknown tasks from resend", async () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, "en");
    const row = { id: "conversation", title: "Local conversation", created_at: "", updated_at: "" };
    let task = { id: "task", request_id: "request", status: "awaiting_runtime", explicitly_submitted: 0, created_at: "" };
    const submit = vi.fn(async () => { task = { ...task, status: "unknown", explicitly_submitted: 1 }; throw new Error("Admission response lost"); });
    const reconcile = vi.fn(async () => task);
    let changed: () => void = () => {};
    const selectConversation = vi.fn(async () => ({ page_ref: "" }));
    window.sparkclawClientStore = { schemaVersion: 1, list: vi.fn(async () => [row]), create: vi.fn(async () => row),
      read: vi.fn(async () => ({ messages: [], tasks: [task], files: [] })), enqueue: vi.fn(),
      saveFile: vi.fn(), exportFile: vi.fn(), submit, reconcile, cancel: vi.fn(), scheduleCreate: vi.fn(), scheduleCheck: vi.fn(), scheduleCancel: vi.fn(), scheduleRunNow: vi.fn() };
    window.sparkclawClientStore.onChange = (listener) => { changed = listener; return () => {}; };
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1, selectConversation,
      localConnection: vi.fn(async () => ({ schema_version: 1, state: "connected", client_id: "client" })) } as unknown as SparkClawDesktop;
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<LocalWorkbench />));
      await act(async () => host.querySelector<HTMLButtonElement>("nav button")!.click());
      expect(submit).not.toHaveBeenCalled();
      expect(selectConversation).toHaveBeenCalledWith("conversation");
      const button = [...host.querySelectorAll<HTMLButtonElement>(".localTaskActions button")].find((item) => item.textContent === "Submit")!;
      await act(async () => button.click());
      expect(submit).toHaveBeenCalledWith("request");
      expect(host.textContent).toContain("Outcome uncertain");
      expect(host.textContent).toContain("Admission response lost");
      expect([...host.querySelectorAll(".localTaskActions button")].some((item) => item.textContent === "Submit")).toBe(false);
      await act(async () => host.querySelector<HTMLButtonElement>(".localTaskActions button")!.click());
      expect(reconcile).toHaveBeenCalledWith("request");
      expect(submit).toHaveBeenCalledTimes(1);
      task = { ...task, status: "delivered" };
      await act(async () => changed());
      expect(host.textContent).toContain("Saved and acknowledged");
    } finally { await act(async () => root.unmount()); }
  });

  it("keeps local input available offline and provides reachable reconnection", async () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, "en");
    const row = { id: "conversation", title: "Offline conversation", created_at: "", updated_at: "" };
    const retryLocalConnection = vi.fn(async () => ({ schema_version: 1, state: "connected", client_id: "client" }));
    window.sparkclawClientStore = { schemaVersion: 1, list: vi.fn(async () => [row]), create: vi.fn(async () => row),
      read: vi.fn(async () => ({ messages: [], tasks: [], files: [] })), enqueue: vi.fn(),
      saveFile: vi.fn(), exportFile: vi.fn(), submit: vi.fn(), reconcile: vi.fn(), cancel: vi.fn(), scheduleCreate: vi.fn(), scheduleCheck: vi.fn(), scheduleCancel: vi.fn(), scheduleRunNow: vi.fn() };
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1, retryLocalConnection,
      localConnection: vi.fn(async () => ({ schema_version: 1, state: "service_unavailable", client_id: "client" })) } as unknown as SparkClawDesktop;
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<LocalWorkbench />));
      await act(async () => host.querySelector<HTMLButtonElement>("nav button")!.click());
      expect(host.querySelector<HTMLTextAreaElement>("#localDraft")!.disabled).toBe(false);
      expect(host.textContent).toContain("Offline. Your device conversations");
      const reconnect = [...host.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent === "Reconnect")!;
      await act(async () => reconnect.click());
      expect(retryLocalConnection).toHaveBeenCalledTimes(1);
      expect(host.textContent).not.toContain("Offline. Your device conversations");
    } finally { await act(async () => root.unmount()); }
  });

  it("persists a single-run definition from the explicit schedule form and offers new-request recovery only when missed", async () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, "en");
    const row = { id: "conversation", title: "Scheduled conversation", created_at: "", updated_at: "" };
    let schedules: Array<{ request_id: string; due_at: string; state: string }> = [];
    const scheduleCreate = vi.fn(async (_id: string, _content: string, dueAt: string) => {
      const schedule = { request_id: "schedule-request", due_at: dueAt, state: "missed" };
      schedules = [schedule]; return schedule;
    });
    const scheduleRunNow = vi.fn(async () => { schedules = [{ ...schedules[0], state: "run_now" }]; return schedules[0]; });
    window.sparkclawClientStore = { schemaVersion: 1, list: vi.fn(async () => [row]), create: vi.fn(async () => row),
      read: vi.fn(async () => ({ messages: [], tasks: [], files: [], schedules })), enqueue: vi.fn(),
      saveFile: vi.fn(), exportFile: vi.fn(), submit: vi.fn(), reconcile: vi.fn(), cancel: vi.fn(),
      scheduleCreate, scheduleCheck: vi.fn(), scheduleCancel: vi.fn(), scheduleRunNow };
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1,
      localConnection: vi.fn(async () => ({ schema_version: 1, state: "connected", client_id: "client" })) } as unknown as SparkClawDesktop;
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<LocalWorkbench />));
      await act(async () => host.querySelector<HTMLButtonElement>("nav button")!.click());
      expect(scheduleCreate).not.toHaveBeenCalled();
      await act(async () => {
        const draft = host.querySelector<HTMLTextAreaElement>("#scheduleDraft")!;
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(draft, "Run this once");
        draft.dispatchEvent(new Event("input", { bubbles: true }));
        const date = host.querySelector<HTMLInputElement>("#scheduleDate")!;
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(date, "2026-10-03T20:00");
        date.dispatchEvent(new Event("input", { bubbles: true }));
      });
      await act(async () => host.querySelector<HTMLFormElement>(".localSchedules form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
      expect(scheduleCreate).toHaveBeenCalledWith("conversation", "Run this once", new Date("2026-10-03T20:00").toISOString());
      const recover = [...host.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent === "Run now as a new request")!;
      await act(async () => recover.click());
      expect(scheduleRunNow).toHaveBeenCalledWith("schedule-request");
      expect(host.textContent).toContain("Started as a new request");
      expect([...host.querySelectorAll<HTMLButtonElement>("button")].some((button) => button.textContent === "Run now as a new request")).toBe(false);
    } finally { await act(async () => root.unmount()); }
  });
});
