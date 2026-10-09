// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { BrowserPanel, resolveBrowserInput } from "./BrowserPanel";
import type { DesktopState, SparkClawDesktop } from "./types";

const EMPTY_STATE: DesktopState = {
  schema_version: 1,
  capability_version: 1,
  runtime_kind: "electron",
  runtime_generation: "a".repeat(32),
  revision: 1,
  pages: [],
  downloads: [],
  permissions: [],
  presentation: {
    panel_bounds: { x: 680, y: 70, width: 640, height: 720 },
    insufficient_space: false,
    presented_page_ref: "",
  },
};

describe("desktop browser input", () => {
  it("opens HTTPS addresses and routes non-address input to Google Search", () => {
    expect(resolveBrowserInput("https://example.com/docs")).toBe("https://example.com/docs");
    expect(resolveBrowserInput("http://example.com/path")).toBe("https://example.com/path");
    expect(resolveBrowserInput("example.com/docs")).toBe("https://example.com/docs");
    expect(resolveBrowserInput("local agent release notes")).toBe("https://www.google.com/search?q=local+agent+release+notes");
    expect(resolveBrowserInput("React 19.2")).toBe("https://www.google.com/search?q=React+19.2");
  });
});

describe("BrowserPanel launcher", () => {
  let resizeCallback: ResizeObserverCallback;
  let observedElements: Element[];

  beforeEach(() => {
    observedElements = [];
    class ResizeObserverStub {
      constructor(callback: ResizeObserverCallback) { resizeCallback = callback; }
      observe(element: Element) { observedElements.push(element); }
      disconnect() {}
    }
    vi.stubGlobal("ResizeObserver", ResizeObserverStub);
  });

  afterEach(() => {
    delete window.sparkclawDesktop;
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("opens with tools, enters one browser surface, and searches from an empty state", async () => {
    const pageRef = `page_${"1".repeat(32)}`;
    const desktop: SparkClawDesktop = {
      runtimeKind: "electron",
      capabilityVersion: 1,
      gatewayBase: "sparkclaw-app://workbench/desktop-gateway",
      speechBase: "ws://127.0.0.1",
      localConnection: vi.fn(),
      retryLocalConnection: vi.fn(),
      loginStartup: vi.fn(async () => ({ supported: true, enabled: false })),
      onLocalConnection: vi.fn(() => () => undefined),
      state: vi.fn(async () => EMPTY_STATE),
      createPersonal: vi.fn(async () => ({ page_ref: pageRef })),
      navigatePersonal: vi.fn(async () => ({ completed: true as const })),
      personalNavigation: vi.fn(async () => ({ completed: true as const })),
      closePersonal: vi.fn(async () => ({ completed: true as const })),
      presentPersonal: vi.fn(async () => ({ completed: true as const })),
      observeTask: vi.fn(async () => ({ completed: true as const })),
      hideBrowser: vi.fn(async () => ({ completed: true as const })),
      setBounds: vi.fn(async () => ({ completed: true as const })),
      respondPermission: vi.fn(async () => ({ completed: true as const })),
      cancelDownload: vi.fn(async () => ({ completed: true as const })),
      showDownload: vi.fn(async () => ({ completed: true as const })),
      onState: vi.fn(() => () => undefined),
    };
    window.sparkclawDesktop = desktop;

    const host = document.createElement("div");
    const workspace = document.createElement("section");
    workspace.className = "workspace";
    workspace.append(host);
    const root = createRoot(host);
    try {
      await act(async () => root.render(<BrowserPanel language="en" />));
      expect(host.textContent).toContain("Browser");
      expect(host.textContent).not.toContain("Terminal");
      expect(host.querySelector(".desktopBrowserHost")).toBeNull();

      const browserButton = [...host.querySelectorAll<HTMLButtonElement>(".desktopToolLauncher button")]
        .find((button) => button.textContent?.includes("Browser"));
      await act(async () => browserButton?.click());

      expect(host.querySelector(".desktopBrowserHost")).not.toBeNull();
      expect(observedElements).toContain(workspace);
      expect(host.querySelector(".desktopBrowserTabBar")).not.toBeNull();
      expect(host.querySelector(".desktopOmnibox")).not.toBeNull();
      expect(host.textContent).toContain("Start browsing");
      expect(host.textContent).not.toContain("My browsing");
      expect(host.textContent).not.toContain("Task observation");

      const browserHost = host.querySelector(".desktopBrowserHost") as HTMLElement;
      let browserX = 800;
      vi.spyOn(browserHost, "getBoundingClientRect").mockImplementation(() => ({
        x: browserX, y: 118, width: 640, height: 500,
      }) as DOMRect);
      await act(async () => resizeCallback([], {} as ResizeObserver));
      expect(desktop.setBounds).toHaveBeenLastCalledWith({ x: 800, y: 118, width: 640, height: 500 }, 1);
      browserX = 560;
      await act(async () => resizeCallback([], {} as ResizeObserver));
      expect(desktop.setBounds).toHaveBeenLastCalledWith({ x: 560, y: 118, width: 640, height: 500 }, 2);

      vi.spyOn(browserHost, "getBoundingClientRect").mockImplementation(() => ({
        x: 1120, y: 118, width: 320, height: 180,
      }) as DOMRect);
      await act(async () => resizeCallback([], {} as ResizeObserver));
      expect(desktop.setBounds).toHaveBeenLastCalledWith({ x: 1120, y: 118, width: 320, height: 180 }, 3);

      // The authorization toolbar leaves half-pixel coordinates. Independent
      // rounding would extend this native view to y=837 outside an 836px window.
      vi.spyOn(browserHost, "getBoundingClientRect").mockImplementation(() => ({
        x: 800.5, y: 224.5, width: 639.5, height: 611.5,
      }) as DOMRect);
      await act(async () => resizeCallback([], {} as ResizeObserver));
      expect(desktop.setBounds).toHaveBeenLastCalledWith({ x: 801, y: 225, width: 639, height: 611 }, 4);

      const input = host.querySelector(".desktopAddressBar input") as HTMLInputElement;
      await act(async () => {
        const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
        setter?.call(input, "local agent release notes");
        input.dispatchEvent(new Event("input", { bubbles: true }));
      });
      await act(async () => {
        (host.querySelector(".desktopAddressBar") as HTMLFormElement).dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
        await Promise.resolve();
      });

      expect(desktop.createPersonal).toHaveBeenCalledWith("https://www.google.com/search?q=local+agent+release+notes");
      expect(desktop.presentPersonal).toHaveBeenCalledWith(pageRef);

      // A new local conversation has no task page. It must not silently
      // present the user's older personal browser merely because it exists.
      await act(async () => root.unmount());
      vi.mocked(desktop.state).mockResolvedValue({
        ...EMPTY_STATE,
        pages: [{ page_ref: pageRef, role: "personal", task_id: "", title: "Old personal page", url: "https://example.com/private", presented: true, loading: false, crashed: false, can_go_back: false, can_go_forward: false }],
        presentation: { ...EMPTY_STATE.presentation, layout_revision: 40, presented_page_ref: pageRef },
      });
      vi.mocked(desktop.presentPersonal).mockClear();
      const localRoot = createRoot(host);
      try {
        await act(async () => localRoot.render(<BrowserPanel language="en" localConversationID="new-conversation" />));
        expect(host.textContent).toContain("Start browsing");
        expect(desktop.presentPersonal).not.toHaveBeenCalled();
        const remountedHost = host.querySelector(".desktopBrowserHost")!;
        vi.spyOn(remountedHost, "getBoundingClientRect").mockReturnValue({ x: 801, y: 225, width: 639, height: 611 } as DOMRect);
        await act(async () => resizeCallback([], {} as ResizeObserver));
        expect(desktop.setBounds).toHaveBeenLastCalledWith({ x: 801, y: 225, width: 639, height: 611 }, 41);
      } finally {
        await act(async () => localRoot.unmount());
      }
    } finally {
      if (host.firstChild) await act(async () => root.unmount());
    }
  });
});
