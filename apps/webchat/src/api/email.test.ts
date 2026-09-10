// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, emailFileURL, openEmailFile } from "./client";

describe("email owner API", () => {
  beforeEach(() => vi.stubGlobal("localStorage", { getItem: () => "owner-token" }));
  afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers(); });

  it("searches admitted backend data and submits only explicit viewing IDs", async () => {
    const fetcher = vi.fn(async () => ({ ok: true, json: async () => ({ version: 4, conversations: [], mail_ids: ["102"] }) }));
    vi.stubGlobal("fetch", fetcher);
    await api.emailConversations({ mailbox_id: "mailbox+one", q: "alice & contract", cursor: "opaque+/=" });
    const [url, init] = fetcher.mock.calls[0] as unknown as [string, RequestInit];
    const params = new URL(url, "http://localhost").searchParams;
    expect(params.get("q")).toBe("alice & contract");
    expect(params.get("mailbox_id")).toBe("mailbox+one");
    expect(params.get("cursor")).toBe("opaque+/=");
    expect(init.headers).toMatchObject({ Authorization: "Bearer owner-token" });
    await api.markEmailViewed(["102"]);
    expect(fetcher.mock.calls[1]).toEqual(["/api/email/messages/viewed", expect.objectContaining({ method: "POST", body: '{"mail_ids":["102"]}' })]);
    expect(() => api.markEmailViewed(Array.from({ length: 101 }, (_, i) => String(i)))).toThrow();
  });

  it("downloads untrusted files by mail/part identity without host paths, URL credentials or executable blobs", async () => {
    vi.useFakeTimers();
    expect(emailFileURL("mail/id", "part &1")).toBe("/api/email/messages/mail%2Fid/file?part_id=part%20%261");
    const fetcher = vi.fn(async () => ({ ok: true, blob: async () => new Blob(["<script>bad()</script>"], { type: "text/html" }) }));
    vi.stubGlobal("fetch", fetcher);
    const create = vi.fn((_blob: Blob) => "blob:mail-download");
    const revoke = vi.fn();
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: create, revokeObjectURL: revoke }));
    const clicks: HTMLAnchorElement[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) { clicks.push(this); });
    await openEmailFile("mail-1", "part-2", "untrusted.html");
    expect(create.mock.calls[0][0]).toMatchObject({ type: "application/octet-stream" });
    expect(clicks[0].download).toBe("untrusted.html");
    expect(clicks[0].href).toBe("blob:mail-download");
    expect(clicks[0].target).toBe("");
    expect(fetcher.mock.calls[0]).toEqual(["/api/email/messages/mail-1/file?part_id=part-2", expect.objectContaining({ headers: { Authorization: "Bearer owner-token" } })]);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(revoke).toHaveBeenCalledWith("blob:mail-download");
  });
});
