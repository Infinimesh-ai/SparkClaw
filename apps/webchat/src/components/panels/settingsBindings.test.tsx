// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Mock, MockInstance } from "vitest";
import { api } from "../../api/client";
import type { ConnectorStatus, NotificationBinding } from "../../api/types";
import { dictionaries } from "../../i18n";
import { ConnectorBindingSettings } from "./settingsBindings";

const text = dictionaries.en;
const url = "https://liteapp.weixin.qq.com/q/synthetic-qr";
const connector: ConnectorStatus = {
  channel: "weixin", provider: "openclaw-weixin-qr", setup_kind: "qr", available: true, enabled: true,
  running: false, state: "setup_required", binding_status: "unbound", binding_startable: true, supports_multiple_bindings: true, version: 1,
};
function binding(status = "waiting_scan", id = "binding-1"): NotificationBinding {
  return { id, owner_id: "owner", channel: "weixin", provider: connector.provider, status, qr_code_url: url,
    expires_at: new Date(Date.now() + 60_000).toISOString(), default_for_channel: false, scopes: [],
    created_at: new Date().toISOString(), updated_at: new Date().toISOString() };
}

describe("desktop Weixin binding dialog", () => {
  let container: HTMLDivElement;
  let root: ReturnType<typeof createRoot>;
  let update: (bindings: NotificationBinding[]) => void;
  let disabled: (notice: string) => void;
  let openBrowser: Mock<(id: string) => Promise<void>>;
  let refresh: Mock<(id: string, signal?: AbortSignal) => Promise<NotificationBinding>>;
  let start: Mock<() => Promise<NotificationBinding>>;
  let qr: MockInstance<typeof api.notificationBindingQR>;

  beforeEach(() => {
    vi.useFakeTimers();
    Object.defineProperty(HTMLDialogElement.prototype, "showModal", { configurable: true, value: function(this: HTMLDialogElement) { this.open = true; } });
    Object.defineProperty(HTMLDialogElement.prototype, "close", { configurable: true, value: function(this: HTMLDialogElement) { this.open = false; } });
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1, createPersonal: vi.fn(), presentPersonal: vi.fn() } as never;
    container = document.createElement("div"); document.body.append(container); root = createRoot(container);
    openBrowser = vi.fn(async () => {}); refresh = vi.fn(async () => binding()); start = vi.fn(async () => binding());
    qr = vi.spyOn(api, "notificationBindingQR").mockResolvedValue({ url, expires_at: binding().expires_at! });
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove(); delete window.sparkclawDesktop;
    Reflect.deleteProperty(HTMLDialogElement.prototype, "showModal");
    Reflect.deleteProperty(HTMLDialogElement.prototype, "close");
    vi.restoreAllMocks(); vi.useRealTimers();
  });
  async function render(initial: NotificationBinding[] = []) {
    function Harness() {
      const [bindings, setBindings] = useState(initial);
      const [unavailable, setUnavailable] = useState("");
      update = setBindings; disabled = setUnavailable;
      return <ConnectorBindingSettings connectors={[connector]} notificationBindings={bindings} bindingsUnavailable={unavailable}
        text={text} language="en" onStartNotificationBinding={async () => {
          const next = await start(); setBindings(current => [next, ...current.filter(item => item.id !== next.id)]); return next;
        }} onRefreshNotificationBinding={async (id, signal) => {
          const next = await refresh(id, signal); setBindings(current => current.map(item => item.id === next.id ? next : item)); return next;
        }} onOpenNotificationBindingBrowser={openBrowser} onRevokeNotificationBinding={async () => {}} onUpdateConnector={async () => connector} />;
    }
    await act(async () => root.render(<Harness />));
  }
  const dialog = () => container.querySelector<HTMLDialogElement>("dialog");
  async function click(label: string) {
    const button = [...container.querySelectorAll("button")].find(item => item.textContent === label || item.getAttribute("aria-label") === label);
    expect(button, label).toBeDefined();
    await act(async () => button!.click());
  }

  it("opens on start, hides QR after scanning, and closes only after the backend confirms active", async () => {
    await render();
    refresh.mockResolvedValueOnce(binding("waiting_confirm")).mockResolvedValueOnce(binding("active"));
    await click(text.settings.bindWeixin);
    expect(dialog()?.open).toBe(true);
    expect(dialog()?.querySelector('svg[role="img"]')).not.toBeNull();
    expect(qr).toHaveBeenCalledWith("binding-1", expect.any(AbortSignal));
    expect(openBrowser).not.toHaveBeenCalled();
    expect(window.sparkclawDesktop!.createPersonal).not.toHaveBeenCalled();
    await act(async () => vi.advanceTimersByTimeAsync(1000));
    expect(dialog()?.textContent).toContain(text.settings.scannedWeixin);
    expect(dialog()?.querySelector('svg[role="img"]')).toBeNull();
    await act(async () => vi.advanceTimersByTimeAsync(2000));
    expect(dialog()).toBeNull();
    expect(container.textContent).toContain(text.settings.bound);
    await act(async () => vi.advanceTimersByTimeAsync(5000));
    expect(refresh).toHaveBeenCalledTimes(2);
  });

  it("can be dismissed and reopened without creating a second binding or stopping progress", async () => {
    await render();
    await click(text.settings.bindWeixin);
    await click(text.common.close);
    expect(dialog()).toBeNull();
    await act(async () => vi.advanceTimersByTimeAsync(1000));
    expect(refresh).toHaveBeenCalledOnce();
    expect(dialog()).toBeNull();
    await click(text.settings.showWeixinQr);
    expect(dialog()?.open).toBe(true);
    expect(start).toHaveBeenCalledOnce();
    expect(qr).toHaveBeenCalledTimes(2);
  });

  it("removes the expired image locally even when polling is unavailable, and can request a new QR", async () => {
    await render();
    refresh.mockRejectedValue(new Error("Disconnected"));
    await click(text.settings.bindWeixin);
    await act(async () => vi.advanceTimersByTimeAsync(60_000));
    expect(dialog()?.querySelector('svg[role="img"]')).toBeNull();
    expect(dialog()?.textContent).toContain(text.settings.weixinQrExpired);
    const next = binding("waiting_scan", "binding-2");
    start.mockResolvedValueOnce(next);
    qr.mockResolvedValueOnce({ url, expires_at: next.expires_at! });
    await click(text.settings.weixinQrRestart);
    expect(start).toHaveBeenCalledTimes(2);
    expect(dialog()?.querySelector('svg[role="img"]')).not.toBeNull();
  });

  it("retries a failed QR read without issuing another binding", async () => {
    qr.mockRejectedValueOnce(new Error("Disconnected"));
    await render();
    await click(text.settings.bindWeixin);
    expect(dialog()?.textContent).toContain(text.settings.bindingQrUnavailable);
    expect(dialog()?.querySelector('svg[role="img"]')).toBeNull();
    await click(text.common.refresh);
    expect(dialog()?.querySelector('svg[role="img"]')).not.toBeNull();
    expect(start).toHaveBeenCalledOnce();
  });

  it("does not recreate a dismissed dialog when its pending QR response arrives", async () => {
    let resolve!: (value: {url: string; expires_at: string}) => void;
    qr.mockReturnValueOnce(new Promise(value => { resolve = value; }));
    await render();
    await click(text.settings.bindWeixin);
    const signal = qr.mock.calls[0][1] as AbortSignal;
    await click(text.common.close);
    expect(signal.aborted).toBe(true);
    await act(async () => resolve({ url, expires_at: binding().expires_at! }));
    expect(dialog()).toBeNull();
  });

  it.each(["active", "revoked", "removed", "unavailable"])("hides the QR after %s", async state => {
    await render([binding()]);
    await click(text.settings.showWeixinQr);
    await act(async () => state === "unavailable" ? disabled("Reconnect") : update(state === "removed" ? [] : [binding(state)]));
    expect(dialog()).toBeNull();
    expect(container.querySelector('svg[role="img"]')).toBeNull();
  });

  it("keeps the existing backend WebChat browser action", async () => {
    delete window.sparkclawDesktop;
    await render();
    await click(text.settings.bindWeixin);
    expect(dialog()).toBeNull();
    await click(text.settings.openWeixinLogin);
    expect(openBrowser).toHaveBeenCalledWith("binding-1");
    expect(qr).not.toHaveBeenCalled();
  });
});
