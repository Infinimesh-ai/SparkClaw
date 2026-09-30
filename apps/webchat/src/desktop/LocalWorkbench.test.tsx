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
      saveFile: vi.fn(), exportFile: vi.fn() };
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1,
      localConnection: vi.fn(async () => ({ schema_version: 1, state: "connected", client_id: "client" })) } as unknown as SparkClawDesktop;
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<LocalWorkbench />));
      const rowButton = [...host.querySelectorAll<HTMLButtonElement>("nav button")].find((button) => button.textContent === row.title)!;
      await act(async () => rowButton.click());
      const input = host.querySelector("textarea")!;
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, "Keep this local input");
        input.dispatchEvent(new Event("input", { bubbles: true }));
      });
      await act(async () => host.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
      expect(host.textContent).toContain("Disk full");
      expect(input.value).toBe("Keep this local input");
      await act(async () => host.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
      expect(host.textContent).toContain("Execution has not been submitted");
      expect(enqueue).toHaveBeenLastCalledWith(row.id, "Keep this local input");
      expect(fetch).not.toHaveBeenCalled();
    } finally { await act(async () => root.unmount()); }
  });
});
