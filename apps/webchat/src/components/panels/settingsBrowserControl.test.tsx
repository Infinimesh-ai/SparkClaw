// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api/client";
import type { BrowserExtensionStatus } from "../../api/types";
import { dictionaries } from "../../i18n";
import { BrowserControlSettings } from "./settingsBrowserControl";

const notConfigured: BrowserExtensionStatus = {
  configured: false,
  state: "not_configured",
  profile_id: "default",
  credential_generation: 0,
  versions: {}
};

const ready: BrowserExtensionStatus = {
  configured: true,
  state: "ready",
  profile_id: "default",
  credential_generation: 2,
  controller_generation: 11,
  session_generation: 3,
  page_generation: 1,
  last_validated_at: "2026-09-04T02:30:00Z",
  versions: {
    client: "playwright-mcp",
    client_version: "0.0.80",
    playwright_version: "1.63.0-alpha-2026-08-31",
    browser_channel: "chrome"
  }
};

describe("Browser control settings", () => {
  afterEach(() => vi.restoreAllMocks());

  it("never prefills the token and clears it after a failed validation", async () => {
    vi.spyOn(api, "browserExtension").mockResolvedValue(notConfigured);
    const save = vi.spyOn(api, "saveBrowserExtensionToken").mockRejectedValue(new Error("extension rejected the credential"));
    const { container, root } = await renderBrowserControl();
    await flushEffects();

    const input = container.querySelector('input[type="password"]') as HTMLInputElement;
    expect(input.value).toBe("");
    const secret = "browser-extension-secret-canary";
    await changeInput(input, secret);
    await act(async () => { (container.querySelector("form") as HTMLFormElement).requestSubmit(); });

    expect(save).toHaveBeenCalledWith(secret);
    expect(input.value).toBe("");
    expect(container.innerHTML).not.toContain(secret);
    expect(container.textContent).toContain("extension rejected the credential");
    expect(container.textContent).toContain(dictionaries.en.settings.browserControlDisposableProfileWarning);
    await act(async () => root.unmount());
  });

  it("supports save, fresh check, and confirmed removal with redacted status", async () => {
    vi.spyOn(api, "browserExtension").mockResolvedValue(notConfigured);
    const save = vi.spyOn(api, "saveBrowserExtensionToken").mockResolvedValue(ready);
    const check = vi.spyOn(api, "checkBrowserExtension").mockResolvedValue({ ...ready, session_generation: 4 });
    const remove = vi.spyOn(api, "removeBrowserExtensionToken").mockResolvedValue({
      ...notConfigured,
      credential_generation: 3
    });
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const { container, root } = await renderBrowserControl();
    await flushEffects();

    const input = container.querySelector('input[type="password"]') as HTMLInputElement;
    await changeInput(input, "qualification-token-value");
    await act(async () => { (container.querySelector("form") as HTMLFormElement).requestSubmit(); });
    expect(save).toHaveBeenCalledWith("qualification-token-value");
    expect(input.value).toBe("");
    expect(container.textContent).toContain("playwright-mcp 0.0.80");

    const checkButton = container.querySelector(`button[title="${dictionaries.en.settings.checkConnection}"]`) as HTMLButtonElement;
    await act(async () => checkButton.click());
    expect(check).toHaveBeenCalledTimes(1);

    const removeButton = container.querySelector(`button[title="${dictionaries.en.settings.browserControlRemove}"]`) as HTMLButtonElement;
    await act(async () => removeButton.click());
    expect(remove).toHaveBeenCalledTimes(1);
    expect(container.textContent).toContain(dictionaries.en.settings.browserControlRemovedDetail);
    expect(container.textContent).not.toContain("qualification-token-value");
    await act(async () => root.unmount());
  });
});

async function renderBrowserControl() {
  const container = document.createElement("div");
  const root = createRoot(container);
  await act(async () => root.render(<BrowserControlSettings text={dictionaries.en} language="en" />));
  return { container, root };
}

async function flushEffects() {
  await act(async () => { await Promise.resolve(); });
}

async function changeInput(input: HTMLInputElement, value: string) {
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
    setter?.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
