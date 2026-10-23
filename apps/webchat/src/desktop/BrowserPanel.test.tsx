// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { BrowserPanel, resolveBrowserInput } from "./BrowserPanel";
import type { DesktopPage, DesktopState, SparkClawDesktop } from "./types";

const page = (id: number, url = "about:blank", role: "personal" | "task" = "personal"): DesktopPage => ({
  page_ref: `page_${String(id).padStart(32, "0")}`, url, role, title: url, task_id: "", presented: false,
  loading: false, crashed: false, can_go_back: false, can_go_forward: false,
});
const empty: DesktopState = { schema_version: 1, capability_version: 1, runtime_kind: "electron", runtime_generation: "a".repeat(32),
  revision: 1, pages: [], downloads: [], permissions: [], presentation: {
    panel_bounds: { x: 680, y: 70, width: 640, height: 720 }, insufficient_space: false, presented_page_ref: "", layout_revision: 0,
  } };
let observers: ResizeObserverCallback[];
beforeEach(() => {
  observers = [];
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("ResizeObserver", class { constructor(callback: ResizeObserverCallback) { observers.push(callback); } observe() {} disconnect() {} });
});
afterEach(() => { delete window.sparkclawDesktop; document.body.replaceChildren(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

async function fixture(pages: DesktopPage[] = []) {
  let state = { ...empty, pages };
  let listener = (_: DesktopState) => {};
  const push = (next = state) => { state = next; listener(next); };
  const desktop = {
    runtimeKind: "electron", capabilityVersion: 1, state: vi.fn(async () => state), onState: (fn: typeof listener) => { listener = fn; return () => {}; },
    createPersonal: vi.fn(async (url?: string) => { const created = page(state.pages.length + 10, url); push({ ...state, pages: [...state.pages, created] }); return { page_ref: created.page_ref }; }),
    hideBrowser: vi.fn(async () => ({ completed: true })),
    setBounds: vi.fn(async () => ({ completed: true })),
    presentPersonal: vi.fn(async () => ({ completed: true })), observeTask: vi.fn(async () => ({ completed: true })),
    navigatePersonal: vi.fn(async () => ({ completed: true })),
    closePersonal: vi.fn(async (ref: string) => { push({ ...state, pages: state.pages.filter(p => p.page_ref !== ref) }); return { completed: true }; }),
  } as unknown as SparkClawDesktop;
  window.sparkclawDesktop = desktop;
  const host = document.createElement("div"); host.className = "workspace"; document.body.append(host);
  const root = createRoot(host), collapse = vi.fn();
  const render = async (open = true, visible = open, conversation?: string) => {
    await act(async () => { root.render(<BrowserPanel language="en" open={open} visible={visible} localConversationID={conversation} onCollapse={collapse} />); });
  };
  await render();
  const input = () => host.querySelector<HTMLInputElement>(".desktopAddressBar input")!;
  const type = async (value: string) => act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input(), value);
    input().dispatchEvent(new Event("input", { bubbles: true }));
  });
  return { host, desktop, render, input, type, collapse, push, state: () => state, cleanup: () => act(async () => root.unmount()) };
}

describe("desktop browser entry", () => {
  it("opens a blank tab directly, retains existing personal/task pages and reuses an unused blank", async () => {
    const f = await fixture([page(1, "https://example.test"), page(2, "https://task.test", "task"), page(3)]);
    try {
      expect(f.host.querySelector(".desktopToolLauncher")).toBeNull();
      expect(f.host.textContent).not.toContain("Back to tools");
      expect(f.host.querySelectorAll(".desktopTab")).toHaveLength(3);
      expect(f.desktop.createPersonal).not.toHaveBeenCalled();
      expect(f.desktop.presentPersonal).not.toHaveBeenCalled();
      expect(f.input().value).toBe("");
      await act(async () => { await new Promise(r => requestAnimationFrame(r)); });
      expect(document.activeElement).toBe(f.input());
      await f.render(false); expect(f.host.querySelector("aside")?.hidden).toBe(true);
      await f.render(); expect(f.desktop.createPersonal).not.toHaveBeenCalled();
      await act(async () => { await new Promise(r => requestAnimationFrame(r)); });
      const control = document.createElement("button"); document.body.append(control); control.focus();
      await f.render(true, false); await f.render(true, true);
      await act(async () => { await new Promise(r => requestAnimationFrame(r)); });
      expect(document.activeElement).toBe(control); // Automatic split recovery is not an open action.
    } finally { await f.cleanup(); }
  });

  it("retains typed addresses across pushes/collapse and does not reuse a dirty blank", async () => {
    const f = await fixture();
    try {
      expect(f.desktop.createPersonal).toHaveBeenCalledTimes(1);
      const first = f.state().pages[0];
      await f.type("not submitted yet");
      await act(async () => f.push({ ...f.state(), revision: 90 }));
      expect(f.input().value).toBe("not submitted yet");
      await f.render(false); await f.render();
      expect(f.desktop.createPersonal).toHaveBeenCalledTimes(2);
      await act(async () => f.host.querySelectorAll<HTMLButtonElement>(".desktopTabSelect")[0].click());
      expect(f.input().value).toBe("not submitted yet");
      await act(async () => f.host.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
      expect(f.desktop.navigatePersonal).toHaveBeenCalledWith(first.page_ref, "https://www.google.com/search?q=not+submitted+yet");
    } finally { await f.cleanup(); }
  });

  it("coalesces rapid reopen while blank creation is pending and preserves the address-ready surface on last close", async () => {
    const f = await fixture([page(1)]);
    try {
      let complete!: (value: { page_ref: string }) => void;
      vi.mocked(f.desktop.createPersonal).mockImplementation(() => new Promise(resolve => { complete = resolve; }));
      await f.type("dirty"); await f.render(false); await f.render(); await f.render(false); await f.render();
      expect(f.desktop.createPersonal).toHaveBeenCalledTimes(1);
      await act(async () => { const added = page(2); f.push({ ...f.state(), pages: [added] }); complete({ page_ref: added.page_ref }); });
      await act(async () => f.host.querySelector<HTMLButtonElement>(".desktopTabClose")!.click());
      expect(f.host.querySelector(".desktopBrowserHost")).not.toBeNull();
      expect(f.host.textContent).toContain("New tab");
      expect(f.collapse).not.toHaveBeenCalled();
      expect(f.input().value).toBe("");
      await act(async () => f.host.querySelector<HTMLButtonElement>('[aria-label="Collapse browser"]')!.click());
      expect(f.collapse).toHaveBeenCalledOnce();
    } finally { await f.cleanup(); }
  });


  it("creates a personal page after a task is filtered away and selects a neighbor on close", async () => {
    const task = { ...page(2, "https://task.test", "task"), local_conversation_id: "a" };
    const f = await fixture([page(1, "https://personal.test"), task, page(3)]);
    try {
      await f.render(true, true, "a");
      await act(async () => f.host.querySelectorAll<HTMLButtonElement>(".desktopTabSelect")[1].click());
      await f.render(true, true, "b");
      await f.type("https://new.test/");
      await act(async () => f.host.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
      expect(f.desktop.navigatePersonal).not.toHaveBeenCalled();
      expect(f.desktop.createPersonal).toHaveBeenCalledWith("https://new.test/");
      await act(async () => f.host.querySelectorAll<HTMLButtonElement>(".desktopTabSelect")[0].click());
      await act(async () => f.host.querySelector<HTMLButtonElement>(".desktopTab.selected .desktopTabClose")!.click());
      expect(f.host.querySelector(".desktopTabPlaceholder")).toBeNull();
      expect(f.host.querySelector(".desktopTab.selected .desktopTabSelect")?.textContent).toContain("New tab");
    } finally { await f.cleanup(); }
  });

  it("rounds bounds inward, tracks layout revisions, and hides native pages under app modals", async () => {
    const f = await fixture([page(1, "https://example.test"), page(2)]);
    try {
      const browser = f.host.querySelector<HTMLElement>(".desktopBrowserHost")!;
      vi.spyOn(browser, "getBoundingClientRect").mockReturnValue({ x: 800.5, y: 224.5, width: 639.5, height: 611.5 } as DOMRect);
      await act(async () => observers.at(-1)!([], {} as ResizeObserver));
      expect(f.desktop.setBounds).toHaveBeenLastCalledWith({ x: 801, y: 225, width: 639, height: 611 }, 1);
      await act(async () => f.host.querySelectorAll<HTMLButtonElement>(".desktopTabSelect")[0].click());
      expect(f.desktop.presentPersonal).toHaveBeenCalledWith(page(1).page_ref);
      const modal = document.createElement("div"); modal.setAttribute("aria-modal", "true");
      await act(async () => { document.body.append(modal); });
      expect(f.desktop.hideBrowser).toHaveBeenCalled();
      const calls = vi.mocked(f.desktop.presentPersonal).mock.calls.length;
      await act(async () => f.push({ ...f.state(), revision: 99 }));
      expect(f.desktop.presentPersonal).toHaveBeenCalledTimes(calls);
      await act(async () => modal.remove());
      expect(f.desktop.presentPersonal).toHaveBeenCalledTimes(calls + 1);
    } finally { await f.cleanup(); }
  });

  it("recovers a rejected resize and ignores a late failure from an older measurement", async () => {
    const f = await fixture([page(1, "https://task.test", "task"), page(2)]);
    try {
      const browser = f.host.querySelector<HTMLElement>(".desktopBrowserHost")!;
      vi.spyOn(browser, "getBoundingClientRect").mockReturnValue({ x: 800, y: 224, width: 480, height: 420 } as DOMRect);
      vi.mocked(f.desktop.setBounds).mockRejectedValueOnce(new Error("Desktop browser bounds are outside the workbench"));
      await act(async () => f.host.querySelectorAll<HTMLButtonElement>(".desktopTabSelect")[0].click());
      expect(f.host.querySelector(".desktopBrowserError")?.textContent).toContain("outside the workbench");
      await act(async () => observers.at(-1)!([], {} as ResizeObserver));
      expect(f.host.querySelector(".desktopBrowserError")).toBeNull();
      expect(f.desktop.observeTask).toHaveBeenCalledWith(page(1).page_ref);
      let rejectOld!: (error: Error) => void;
      vi.mocked(f.desktop.setBounds).mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectOld = reject; }));
      await act(async () => observers.at(-1)!([], {} as ResizeObserver));
      await act(async () => observers.at(-1)!([], {} as ResizeObserver));
      await act(async () => rejectOld(new Error("Desktop browser bounds are outside the workbench")));
      expect(f.host.querySelector(".desktopBrowserError")).toBeNull();
    } finally { await f.cleanup(); }
  });
});
it("resolves addresses and searches without permitting credentials", () => {
  expect(resolveBrowserInput("http://example.com/docs")).toBe("https://example.com/docs");
  expect(resolveBrowserInput("example.com/docs")).toBe("https://example.com/docs");
  expect(resolveBrowserInput("React 19.2")).toBe("https://www.google.com/search?q=React+19.2");
});
