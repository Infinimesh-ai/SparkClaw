// @vitest-environment jsdom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";
import { api, apiToken, bindAPITokenToDeployment, saveAPIToken } from "./api/client";
import { dictionaries } from "./i18n";

const localIdentity = { deployment_id: "deployment", owner_id: "owner", access_mode: "local", client_id: "", local_access_id: "host-access" };
const roots: Root[] = [];
let storage: Map<string, string>;

function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}

function resource(url: string) {
  if (url.endsWith("/stream")) return new Response(": heartbeat\n\n");
  if (url.endsWith("/identity")) return json(localIdentity);
  if (url === "/api/config") return json({ speech: { default_language: "auto" } });
  if (url === "/api/owner") return json({ display_name: "Owner", email: "" });
  if (url === "/readyz") return json({ ok: true });
  return json({ sessions: [], approvals: [], memory_candidates: [], memories: [], notifications: [], unread_count: 0 });
}

async function renderApp() {
  const host = document.createElement("div");
  const root = createRoot(host);
  roots.push(root);
  await act(async () => root.render(<App />));
  return host;
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  storage = new Map([["sparkclaw.language", "en"], ["sparkclaw.browser.draft", "preserved draft"]]);
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => storage.set(key, value),
    removeItem: (key: string) => storage.delete(key)
  });
});

afterEach(async () => {
  for (const root of roots.splice(0)) await act(async () => root.unmount());
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
});

describe("WebChat access lifecycle", () => {
  it("waits for identity, then loads and subscribes without a token; reconnect only reads", async () => {
    let finishIdentity!: (response: Response) => void;
    const pending = new Promise<Response>((resolve) => { finishIdentity = resolve; });
    const fetcher = vi.fn(async (url: string) => url.endsWith("/identity") ? pending : resource(url));
    vi.stubGlobal("fetch", fetcher);
    const host = await renderApp();
    expect(fetcher.mock.calls.map(([url]) => url)).toEqual(["/api/workbench/identity"]);
    expect(host.textContent).toContain(dictionaries.en.auth.checkingAccess);
    await act(async () => finishIdentity(json(localIdentity)));
    expect(apiToken()).toBe("");
    expect(host.textContent).toContain(dictionaries.en.auth.localAccess);
    const routes = fetcher.mock.calls.map(([url]) => url);
    expect(routes).toContain("/api/sessions");
    expect(routes).toContain("/api/workbench/events/stream");
    expect(routes).toContain("/api/notifications/events/stream");
    await act(async () => { await vi.advanceTimersByTimeAsync(5100); });
    expect(vi.mocked(fetch).mock.calls.every(([, init]) => !init?.method || init.method === "GET")).toBe(true);
  });

  it("keeps a revoked token rejected until explicit recovery, then preserves other browser data", async () => {
    saveAPIToken("other-deployment-token");
    bindAPITokenToDeployment("other-deployment");
    saveAPIToken("revoked-token");
    bindAPITokenToDeployment("deployment");
    const fetcher = vi.fn(async (url: string, init?: RequestInit) => new Headers(init?.headers).has("Authorization")
      ? json({ error: "credential revoked" }, 401) : resource(url));
    vi.stubGlobal("fetch", fetcher);
    const host = await renderApp();
    expect(fetcher).toHaveBeenCalledOnce();
    expect(apiToken()).toBe("revoked-token");
    await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
    expect(fetcher).toHaveBeenCalledOnce();
    const recovery = [...host.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent === dictionaries.en.auth.useLocalAccess)!;
    expect(recovery).toBeDefined();
    await act(async () => recovery.click());
    expect(apiToken()).toBe("");
    expect(host.textContent).toContain(dictionaries.en.auth.localAccess);
    expect(storage.get("sparkclaw.browser.draft")).toBe("preserved draft");
    expect([...storage.values()]).toContain("other-deployment-token");
    expect([...storage.values()]).not.toContain("revoked-token");
  });

  it("stops all subscriptions on unauthorized access and never replays writes", async () => {
    let rejected = false;
    const fetcher = vi.fn(async (url: string) => rejected ? json({ error: "access revoked" }, 401) : resource(url));
    vi.stubGlobal("fetch", fetcher);
    const host = await renderApp();
    expect(host.textContent).toContain(dictionaries.en.auth.localAccess);
    rejected = true;
    await act(async () => { await api.createSession("attempt").catch(() => undefined); });
    const requestCount = fetcher.mock.calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
    expect(fetcher).toHaveBeenCalledTimes(requestCount);
    expect(host.textContent).toContain(dictionaries.en.auth.useLocalAccess);
    expect(vi.mocked(fetch).mock.calls.filter(([, init]) => init?.method === "POST")).toHaveLength(1);
  });

  it("explains fixed deployment tokens without offering ineffective recovery", async () => {
    vi.stubEnv("VITE_SPARKCLAW_API_TOKEN", "fixed-token");
    const fetcher = vi.fn(async () => json({ error: "credential revoked" }, 401));
    vi.stubGlobal("fetch", fetcher);
    const host = await renderApp();
    expect(host.textContent).toContain(dictionaries.en.auth.configuredToken);
    expect(host.textContent).not.toContain(dictionaries.en.auth.useLocalAccess);
    expect(host.querySelector('input[type="password"]')).toBeNull();
    expect(apiToken()).toBe("fixed-token");
  });
});
