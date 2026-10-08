// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DesktopLoginGate } from "./DesktopLoginGate";
import type { DesktopConnectionStatus, SparkClawDesktop } from "./types";

const locked: DesktopConnectionStatus = { schema_version: 1, state: "locked", backend: {
  schema_version: 2, origin: "https://sparkclaw.example:9443", deployment_id: "deployment", owner_id: "owner", tls_certificate_sha256: "a".repeat(64),
} };

describe("desktop secure login gate", () => {
  beforeEach(() => {
    const values = new Map<string, string>();
    Object.defineProperty(window, "localStorage", { configurable: true, value: {
      getItem: (key: string) => values.get(key) || null,
      setItem: vi.fn((key: string, value: string) => values.set(key, value)),
      clear: () => values.clear(),
    } });
    vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  });
  afterEach(() => { delete window.sparkclawDesktop; window.localStorage.clear(); vi.unstubAllGlobals(); });

  it("blocks the protected child until one-step enrollment and clears the credential without renderer storage", async () => {
    window.localStorage.setItem("sparkclaw.language", "en");
    let listener: (status: DesktopConnectionStatus) => void = () => {};
    const desktop = {
      runtimeKind: "electron", capabilityVersion: 1,
      localConnection: async () => locked,
      onLocalConnection: (callback: typeof listener) => { listener = callback; return () => {}; },
      enroll: vi.fn(async () => ({ ...locked, state: "invalid_authentication" as const })),
      retryLocalConnection: vi.fn(async () => locked),
    } as unknown as SparkClawDesktop;
    window.sparkclawDesktop = desktop;
    const host = document.createElement("div");
    const root = createRoot(host);
    try {
      await act(async () => root.render(<DesktopLoginGate><div data-protected="true">Local workbench</div></DesktopLoginGate>));
      expect(host.querySelector("[data-protected]")).toBeNull();
      expect(host.textContent).toContain(locked.backend!.origin);
      const input = host.querySelector<HTMLInputElement>("#desktopCredential")!;
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "sparkclaw-connect-v1.test-user-credential");
        input.dispatchEvent(new Event("input", { bubbles: true }));
      });
      await act(async () => {
        input.closest("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
      });
      expect(desktop.enroll).toHaveBeenCalledWith("sparkclaw-connect-v1.test-user-credential");
      expect(input.value).toBe("");
      expect(host.textContent).toContain("rejected or revoked");
      expect(window.localStorage.setItem).toHaveBeenCalledTimes(1);
      await act(async () => listener({ ...locked, state: "connected", client_id: "device" }));
      expect(host.querySelector("[data-protected]")).not.toBeNull();
      await act(async () => listener({ ...locked, state: "invalid_authentication" }));
      expect(host.querySelector("[data-protected]")).toBeNull();
    } finally { await act(async () => root.unmount()); }
  });

  it("explains ISCP installation conflicts without reporting an HTTPS certificate error", async () => {
    window.localStorage.setItem("sparkclaw.language", "en");
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1,
      localConnection: async () => ({ schema_version: 1, state: "identity_conflict", test_mode: true,
        backend: { schema_version: 3, transport: "iscp", origin: "https://iscp.invalid", deployment_id: "deployment" } }),
      enroll: vi.fn(), onLocalConnection: () => () => {},
    } as unknown as SparkClawDesktop;
    const host = document.createElement("div"), root = createRoot(host);
    try {
      await act(async () => root.render(<DesktopLoginGate><div data-protected="true">Local</div></DesktopLoginGate>));
      expect(host.querySelector(".desktopLoginStatus")?.textContent).toContain("Client installation binding");
      expect(host.querySelector(".desktopLoginStatus")?.textContent).not.toContain("certificate");
      expect(host.querySelector("[data-protected]")).toBeNull();
    } finally { await act(async () => root.unmount()); }
  });

  it("keeps saved device's local workbench available during an outage and hides it after logout", async () => {
    let listener: (status: DesktopConnectionStatus) => void = () => {};
    window.sparkclawDesktop = {
      runtimeKind: "electron", capabilityVersion: 1,
      localConnection: async () => ({ ...locked, state: "service_unavailable", client_id: "device" }),
      enroll: vi.fn(), onLocalConnection: (callback: typeof listener) => { listener = callback; return () => {}; },
    } as unknown as SparkClawDesktop;
    const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<DesktopLoginGate><div data-protected="true">Local</div></DesktopLoginGate>));
      expect(host.querySelector("[data-protected]")).not.toBeNull();
      await act(async () => listener(locked));
      expect(host.querySelector("[data-protected]")).toBeNull();
    } finally { await act(async () => root.unmount()); }
  });
});
