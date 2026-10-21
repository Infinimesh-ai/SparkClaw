// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MessageStreamDeliveryError } from "../lib/messageStream";
import { api, APIError, apiToken, bindAPITokenToDeployment, clearAPIToken, saveAPIToken, documentFileURL, messageStreamRequestBody, scheduleActionRequestBody, scheduleCreateRequestBody, streamWorkbenchInvalidations } from "./client";

describe("desktop Weixin QR binding", () => {
  afterEach(() => { delete window.sparkclawDesktop; vi.unstubAllGlobals(); });
  function setup(result: { url: string; expires_at?: string }) {
    const createPersonal = vi.fn(async () => ({ page_ref: "qr-page" }));
    const presentPersonal = vi.fn(async () => ({ completed: true }));
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1, createPersonal, presentPersonal } as unknown as NonNullable<typeof window.sparkclawDesktop>;
    const fetch = vi.fn(async () => new Response(JSON.stringify({ expires_at: new Date(Date.now() + 60_000).toISOString(), ...result })));
    vi.stubGlobal("fetch", fetch);
    return { createPersonal, presentPersonal, fetch };
  }
  it("resolves QR content without opening a local or backend browser", async () => {
    const url = "https://liteapp.weixin.qq.com/q/pending";
    const f = setup({ url, expires_at: new Date(Date.now() + 60_000).toISOString() });
    expect(await api.notificationBindingQR("binding-1")).toEqual({ url, expires_at: expect.any(String) });
    expect(f.fetch).toHaveBeenCalledExactlyOnceWith(expect.stringContaining("/api/notification-bindings/binding-1/login-url"), expect.anything());
    expect(f.createPersonal).not.toHaveBeenCalled();
    expect(f.presentPersonal).not.toHaveBeenCalled();
  });
  it.each([
    { url: "not a URL" },
    { url: "https://liteapp.weixin.qq.com/q", expires_at: undefined },
    { url: "https://liteapp.weixin.qq.com/q/" + "x".repeat(2048), expires_at: new Date(Date.now() + 60_000).toISOString() },
    { url: "https://liteapp.weixin.qq.com/q/" + "码".repeat(1000) },
    { url: "https://other.example/q" },
    { url: "http://liteapp.weixin.qq.com/q" },
    { url: "https://user:secret@liteapp.weixin.qq.com/q" },
    { url: "https://liteapp.weixin.qq.com:8080/q" },
    { url: "https://liteapp.weixin.qq.com/q", expires_at: new Date(0).toISOString() },
    { url: "https://liteapp.weixin.qq.com/q", expires_at: "invalid" },
  ])("rejects invalid or expired QR content: $url", async value => {
    const f = setup(value);
    await expect(api.notificationBindingQR("binding-1")).rejects.toThrow("unavailable");
    expect(f.createPersonal).not.toHaveBeenCalled();
  });
});

describe("email refresh request identity", () => {
  let values: Map<string, string>;
  beforeEach(() => {
    values = new Map();
    vi.stubGlobal("localStorage", { getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value), removeItem: (key: string) => values.delete(key) });
  });
  afterEach(() => vi.unstubAllGlobals());
  it("preserves the backend request identity and selected mailbox without retrying POST", async () => {
    const result = { scheduled: true, refresh_requests: [{ mailbox_id: "box", refresh_request_id: "refresh-1" }] };
    const fetch = vi.fn(async () => ({ ok: true, json: async () => result })); vi.stubGlobal("fetch", fetch);
    expect(await api.syncEmail("box")).toEqual(result);
    expect(fetch).toHaveBeenCalledOnce();
    expect(fetch.mock.calls[0]).toEqual([expect.stringContaining("/api/email/sync"), expect.objectContaining({ method: "POST", body: JSON.stringify({ mailbox_id: "box" }) })]);
  });
  it("clears mailbox guards when authentication is cleared or changed", () => {
    const key = "sparkclaw.email.refresh.v1";
    values.set(key, "old-user-guard"); saveAPIToken("new-token"); expect(values.has(key)).toBe(false);
    values.set(key, "current-user-guard"); saveAPIToken("new-token"); expect(values.has(key)).toBe(true);
    clearAPIToken(); expect(values.has(key)).toBe(false);
  });
  it("binds a staged token to the authenticated deployment namespace", () => {
    saveAPIToken("deployment-a-token");
    bindAPITokenToDeployment("deployment-a");
    expect(apiToken()).toBe("deployment-a-token");
    expect([...values.entries()]).toEqual(expect.arrayContaining([
      [expect.stringContaining("sparkclaw.deployment_id."), "deployment-a"],
      [expect.stringContaining(".deployment-a"), "deployment-a-token"]
    ]));
    expect([...values.keys()].filter((key) => key.startsWith("sparkclaw.api_token.") && !key.endsWith(".deployment-a"))).toHaveLength(0);

    saveAPIToken("deployment-b-token");
    expect(apiToken()).toBe("deployment-b-token");
    bindAPITokenToDeployment("deployment-b");
    expect(apiToken()).toBe("deployment-b-token");
    expect([...values.values()]).toContain("deployment-a-token");
    expect([...values.values()]).toContain("deployment-b-token");
  });
});

describe("documentFileURL", () => {
  it("keeps the workspace path scoped to its session", () => {
    const url = documentFileURL("media/20260720/weather card.png", "session-weather");
    expect(url).toContain("/api/documents/file?");
    expect(url).toContain("path=media%2F20260720%2Fweather+card.png");
    expect(url).toContain("session_id=session-weather");
  });
});

describe("identity read deadline by native transport", () => {
  beforeEach(() => {
    vi.stubGlobal("localStorage", { getItem: () => null, setItem: vi.fn(), removeItem: vi.fn() });
    delete window.sparkclawDesktop;
  });
  afterEach(() => { delete window.sparkclawDesktop; vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

  it("accepts a public ISCP identity roundtrip beyond five seconds using the native thirty-second budget", async () => {
    vi.useFakeTimers();
    const timeout = vi.spyOn(AbortSignal, "timeout").mockImplementation(milliseconds => {
      const controller = new AbortController(); setTimeout(() => controller.abort(), milliseconds); return controller.signal;
    });
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1,
      localConnection: vi.fn(async () => ({ schema_version: 1, state: "connected", backend: { transport: "iscp" } })) } as unknown as NonNullable<typeof window.sparkclawDesktop>;
    const identity = { deployment_id: "deployment", owner_id: "owner", client_id: "client" };
    const fetch = vi.fn((_url: string, init: RequestInit) => new Promise<Response>((resolve, reject) => {
      const timer = setTimeout(() => resolve(new Response(JSON.stringify(identity))), 6000);
      init.signal?.addEventListener("abort", () => { clearTimeout(timer); reject(new Error("identity deadline")); }, { once: true });
    }));
    vi.stubGlobal("fetch", fetch);
    const pending = api.workbenchIdentity();
    await vi.advanceTimersByTimeAsync(6000);
    expect(await pending).toEqual(identity); expect(timeout).toHaveBeenCalledExactlyOnceWith(30000); expect(fetch).toHaveBeenCalledOnce();
  });

  it.each(["LAN", "browser", "unavailable native status"])("preserves the five-second %s identity deadline", async kind => {
    const timeout = vi.spyOn(AbortSignal, "timeout").mockReturnValue(new AbortController().signal);
    if (kind !== "browser") window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1,
      localConnection: kind === "LAN" ? vi.fn(async () => ({ schema_version: 1, state: "connected", backend: { transport: "lan" } })) : vi.fn(async () => { throw new Error("native status unavailable"); }) } as unknown as NonNullable<typeof window.sparkclawDesktop>;
    const fetch = vi.fn(async () => new Response(JSON.stringify({ deployment_id: "deployment", owner_id: "owner", client_id: "client" })));
    vi.stubGlobal("fetch", fetch);
    await api.workbenchIdentity(); expect(timeout).toHaveBeenCalledExactlyOnceWith(5000); expect(fetch).toHaveBeenCalledOnce();
  });
});

describe("messageStreamRequestBody", () => {
  it("reads completed requests for conversation activity through the existing read-only list", async () => {
    vi.stubGlobal("localStorage", { getItem: () => null });
    const status = { schema_version: 1, request_id: "finished", input_digest: "a".repeat(64), state: "completed" };
    const fetch = vi.fn(async (_url: string, _init: RequestInit) => ({ ok: true, json: async () => ({ requests: [status] }) }));
    vi.stubGlobal("fetch", fetch);
    try {
      expect(await api.workbenchRequests("session", false)).toEqual([status]);
      expect(fetch.mock.calls[0][0]).not.toContain("attention=1");
      expect(fetch.mock.calls[0][1]).not.toHaveProperty("method", "POST");
    } finally { vi.unstubAllGlobals(); }
  });
  it("loads every page of unresolved request identities", async () => {
    vi.stubGlobal("localStorage", { getItem: () => null });
    const base = { schema_version: 1, input_digest: "a".repeat(64), state: "unknown" };
    const fetch = vi.fn().mockResolvedValueOnce({ ok: true, json: async () => ({ requests: [{ ...base, request_id: "newer" }], next_cursor: "newer" }) })
      .mockResolvedValueOnce({ ok: true, json: async () => ({ requests: [{ ...base, request_id: "older" }] }) });
    vi.stubGlobal("fetch", fetch);
    try {
      expect((await api.workbenchRequests("session")).map((row) => row.request_id)).toEqual(["newer", "older"]);
      expect(fetch.mock.calls[1][0]).toContain("attention=1&cursor=newer");
    } finally { vi.unstubAllGlobals(); }
  });
  it("preserves the explicit request identity across a lost-response lookup", async () => {
    vi.stubGlobal("localStorage", { getItem: () => null });
    const requestID = "11111111-1111-4111-8111-111111111111";
    const status = { schema_version: 1, request_id: requestID, input_digest: "a".repeat(64), state: "unknown" };
    const fetch = vi.fn(async () => ({ ok: true, json: async () => status }));
    vi.stubGlobal("fetch", fetch);
    try {
      expect(messageStreamRequestBody("hello", [], "", "", requestID).request_id).toBe(requestID);
      expect(await api.workbenchRequest("session", requestID)).toEqual(status);
      expect(fetch).toHaveBeenCalledOnce();
      expect(fetch.mock.calls[0]).toEqual([expect.stringContaining(`/api/sessions/session/requests/${requestID}`), expect.not.objectContaining({ method: "POST" })]);
    } finally { vi.unstubAllGlobals(); }
  });
  it("changes only the final target when an endpoint is selected", () => {
    const attachments = [{ artifact_id: "artifact-file", name: "report.txt", rel_path: "uploads/report.txt" }];
    expect(messageStreamRequestBody("Summarize this report", attachments, "endpoint-selected")).toEqual({
      request_id: expect.stringMatching(/^[a-f0-9-]{36}$/u),
      content: "Summarize this report",
      attachments,
      target_endpoint_id: "endpoint-selected"
    });
    expect(messageStreamRequestBody("Summarize this report", attachments)).toEqual({
      request_id: expect.stringMatching(/^[a-f0-9-]{36}$/u),
      content: "Summarize this report",
      attachments
    });

    expect(messageStreamRequestBody("", attachments, "endpoint-selected")).toEqual({
      request_id: expect.stringMatching(/^[a-f0-9-]{36}$/u),
      content: "",
      attachments,
      target_endpoint_id: "endpoint-selected"
    });

    expect(messageStreamRequestBody("hello", [], "", "America/New_York")).toEqual({
      request_id: expect.stringMatching(/^[a-f0-9-]{36}$/u),
      content: "hello",
      attachments: [],
      client_timezone: "America/New_York"
    });
  });
});

describe("scheduleActionRequestBody", () => {
  it("sends schedule creation to the dedicated resource without conversation fields", () => {
    expect(scheduleCreateRequestBody("Every weekday at 9 AM, summarize project progress", "Asia/Shanghai")).toEqual({
      content: "Every weekday at 9 AM, summarize project progress",
      client_timezone: "Asia/Shanghai"
    });
  });

  it("sends the browser timezone with schedule mutations", () => {
    const action = { operation: "delete" as const, schedule_id: "schedule-1", expected_updated_at: "2026-08-19T01:00:00Z" };
    expect(scheduleActionRequestBody("Delete schedule", action, "America/New_York")).toEqual({
      request_id: expect.stringMatching(/^[a-f0-9-]{36}$/u),
      content: "Delete schedule",
      schedule_action: action,
      client_timezone: "America/New_York"
    });
  });
});

function sseResponse(payload: string) {
  const encoder = new TextEncoder();
  let sent = false;
  return {
    ok: true,
    body: {
      getReader: () => ({
        read: async () => {
          if (sent) return { value: undefined, done: true };
          sent = true;
          return { value: encoder.encode(payload), done: false };
        }
      })
    }
  };
}

describe("sendMessageStream failure events", () => {
  beforeEach(() => {
    // jsdom serves the suite from an opaque origin without localStorage;
    // apiToken() only needs a null read.
    vi.stubGlobal("localStorage", { getItem: () => null });
  });
  afterEach(() => vi.unstubAllGlobals());

  it("raises a typed delivery error for the gateway's delivery_failed event", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => sseResponse(
      'event: message.stream.started\ndata: {"session_id":"s1"}\n\n' +
      'event: message.stream.delivery_failed\ndata: {"error":"provider temporarily unavailable","session_id":"s1"}\n\n'
    )));
    const errors: Error[] = [];
    await api.sendMessageStream("s1", "hello", [], { onError: (error) => errors.push(error) });
    expect(errors).toHaveLength(1);
    expect(errors[0]).toBeInstanceOf(MessageStreamDeliveryError);
    expect(errors[0].message).toBe("provider temporarily unavailable");
  });

  it("throws a typed APIError carrying the HTTP status from both request paths", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => ({
      ok: false,
      status: 401,
      json: async () => ({ error: "Token 认证失败" })
    })));
    // JSON path: the 401 must be detectable from the status, not from
    // sniffing localized display strings.
    const jsonError = await api.sessions().catch((error: unknown) => error);
    expect(jsonError).toBeInstanceOf(APIError);
    expect((jsonError as APIError).status).toBe(401);
    expect((jsonError as APIError).message).toBe("Token 认证失败");
    // Stream path rejects with the same typed error.
    const streamError = await api.sendMessageStream("s1", "hello").catch((error: unknown) => error);
    expect(streamError).toBeInstanceOf(APIError);
    expect((streamError as APIError).status).toBe(401);
  });

  it("keeps run failures as plain stream errors", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => sseResponse(
      'event: error\ndata: {"error":"model failed","session_id":"s1"}\n\n'
    )));
    const errors: Error[] = [];
    await api.sendMessageStream("s1", "hello", [], { onError: (error) => errors.push(error) });
    expect(errors).toHaveLength(1);
    expect(errors[0]).not.toBeInstanceOf(MessageStreamDeliveryError);
    expect(errors[0].message).toBe("model failed");
  });
});

describe("workbench invalidation stream", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("uses the Web bearer and accepts only valid invalidation events", async () => {
    vi.stubGlobal("localStorage", { getItem: () => "web-client-token" });
    const fetcher = vi.fn(async () => sseResponse(
      ': heartbeat\n\n' +
      'event: other\ndata: {"schema_version":1}\n\n' +
      'event: invalidation\ndata: not-json\n\n' +
      'event: invalidation\ndata: {"schema_version":1,"epoch":"epoch-a","sequence":7,"category":"sessions","resource_id":"session-a"}\n\n'
    ));
    vi.stubGlobal("fetch", fetcher);
    const received: unknown[] = [];
    await streamWorkbenchInvalidations(new AbortController().signal, (event) => received.push(event));
    expect(received).toEqual([expect.objectContaining({ epoch: "epoch-a", sequence: 7, category: "sessions" })]);
    expect(fetcher).toHaveBeenCalledWith(expect.stringContaining("/api/workbench/events/stream"), expect.objectContaining({
      method: "GET",
      headers: expect.objectContaining({ Authorization: "Bearer web-client-token", Accept: "text/event-stream" })
    }));
  });
});

describe("revoked credential deletion through the desktop request boundary", () => {
  beforeEach(() => {
    vi.stubGlobal("localStorage", { getItem: () => null, setItem: () => {}, removeItem: () => {} });
  });
  afterEach(() => vi.unstubAllGlobals());
  it("sends an empty JSON object that survives Electron's non-GET arrayBuffer conversion", async () => {
    const requests: Request[] = [];
    vi.stubGlobal("fetch", vi.fn(async (url: string, init: RequestInit) => {
      const request = new Request(new URL(url, "https://iscp.invalid"), init);
      requests.push(request);
      expect(new TextDecoder().decode(await request.arrayBuffer())).toBe("{}");
      return new Response(JSON.stringify({ deleted: true }));
    }));
    expect(await api.deleteClient("revoked-device")).toEqual({ deleted: true });
    expect(requests).toHaveLength(1);
    expect(requests[0].method).toBe("DELETE");
    expect(new URL(requests[0].url).pathname).toBe("/api/clients/revoked-device");
  });
});

describe("desktop email connection check recovery", () => {
  beforeEach(async () => {
    const { webcrypto } = await vi.importActual<{ webcrypto: Crypto }>("node:crypto");
    vi.stubGlobal("crypto", webcrypto);
    const values = new Map<string, string>();
    vi.stubGlobal("localStorage", { getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value), removeItem: (key: string) => values.delete(key) });
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1 } as NonNullable<typeof window.sparkclawDesktop>;
  });
  afterEach(() => { delete window.sparkclawDesktop; vi.unstubAllGlobals(); });

  it.each(["gmail", "outlook", "qq_mail"] as const)("starts a new explicit %s check after a delivered provider failure", async provider => {
    const actions: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (_url: string, init: RequestInit) => {
      actions.push(new Headers(init.headers).get("x-sparkclaw-action-id")!);
      return new Response(JSON.stringify({ code: "email_provider_unavailable", error: "Unavailable" }), { status: 503 });
    }));
    await expect(api.checkEmailProvider(provider)).rejects.toMatchObject({ status: 503 });
    await expect(api.checkEmailProvider(provider)).rejects.toMatchObject({ status: 503 });
    expect(actions).toHaveLength(2);
    expect(actions[0]).toMatch(/^[a-f0-9-]{36}$/);
    expect(actions[1]).not.toBe(actions[0]);
  });

  it.each(["operation_outcome_unknown", "operation_receipt_unavailable", "operation_receipt_capacity"])("retains %s check identity for receipt reconciliation", async code => {
    const actions: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (_url: string, init: RequestInit) => {
      actions.push(new Headers(init.headers).get("x-sparkclaw-action-id")!);
      return new Response(JSON.stringify({ code, error: "Unavailable" }), { status: 503 });
    }));
    await expect(api.checkEmailProvider("gmail")).rejects.toMatchObject({ status: 503 });
    await expect(api.checkEmailProvider("gmail")).rejects.toMatchObject({ status: 503 });
    expect(actions[1]).toBe(actions[0]);
  });

  it("keeps a failed send's original identity instead of creating another send", async () => {
    const actions: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (_url: string, init: RequestInit) => {
      actions.push(new Headers(init.headers).get("x-sparkclaw-action-id")!);
      return new Response(JSON.stringify({ code: "email_provider_unavailable", error: "Unavailable" }), { status: 503 });
    }));
    await expect(api.sendEmailDraft("draft", 1, "original-send")).rejects.toMatchObject({ status: 503 });
    await expect(api.sendEmailDraft("draft", 1, "original-send")).rejects.toMatchObject({ status: 503 });
    expect(actions[1]).toBe(actions[0]);
  });
});
