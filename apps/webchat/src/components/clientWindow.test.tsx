// @vitest-environment jsdom
import { act, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ClientTitlebarBrowser, ClientWindow, useClientTitlebar, WorkspaceTitlebar } from "./clientWindow";
import { usePageHistory } from "../hooks/usePageHistory";
import type { SparkClawDesktop } from "../desktop/types";

type Entry = { page: string; sessionID: string };

function Workspace({ sessions = ["one", "two"] }: { sessions?: string[] }) {
  const [entry, setEntry] = useState<Entry>({ page: "chat", sessionID: "one" });
  const history = usePageHistory(entry, {
    scope: "scope",
    valid: (value) => !value.sessionID || sessions.includes(value.sessionID),
    restore: (value) => setEntry(value),
  }, useClientTitlebar());
  return <main>
    <WorkspaceTitlebar language="en" navigation={history}
      sidebar={<button className="iconButton sidebarToggle" type="button" aria-label="Toggle sidebar" />}
      back={<button className="iconButton workspaceBackButton" type="button" aria-label="Back to home" />}
      actions={<button className="iconButton notificationBell" type="button" aria-label="Notifications" />} />
    <output>{entry.page}:{entry.sessionID}</output>
    <button className="visit" type="button" onClick={() => setEntry({ page: "chat", sessionID: "two" })} />
    <button className="schedules" type="button" onClick={() => setEntry({ page: "schedules", sessionID: "two" })} />
  </main>;
}

afterEach(() => { delete window.sparkclawDesktop; vi.unstubAllGlobals(); });

describe("client title bar", () => {
  it("portals workspace controls into the window title bar and walks page history", async () => {
    vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
    const host = document.createElement("div"), root = createRoot(host);
    const current = () => host.querySelector("output")!.textContent;
    const control = (label: string) => host.querySelector<HTMLButtonElement>(`.clientTitlebar [aria-label="${label}"]`)!;
    try {
      await act(async () => root.render(<ClientWindow><Workspace /></ClientWindow>));
      const titlebar = host.querySelector(".clientTitlebar")!;
      expect(host.querySelector(".clientContent .topbar")).toBeNull();
      expect(titlebar.querySelector(".clientSidebarControl .sidebarToggle")).not.toBeNull();
      expect(titlebar.querySelector(".clientTitlebarActions .notificationBell")).not.toBeNull();
      // History replaces the in-page back control, and native controls are not drawn.
      expect(titlebar.querySelector(".workspaceBackButton")).toBeNull();
      expect(titlebar.querySelector(".clientWindowControls")).toBeNull();
      expect(control("Back").disabled).toBe(true);
      expect(control("Forward").disabled).toBe(true);

      await act(async () => host.querySelector<HTMLButtonElement>(".visit")!.click());
      await act(async () => host.querySelector<HTMLButtonElement>(".schedules")!.click());
      await act(async () => control("Back").click());
      expect(current()).toBe("chat:two");
      await act(async () => control("Back").click());
      expect(current()).toBe("chat:one");
      expect(control("Back").disabled).toBe(true);
      await act(async () => control("Forward").click());
      await act(async () => control("Forward").click());
      expect(current()).toBe("schedules:two");
      expect(control("Forward").disabled).toBe(true);

      // A new page after going back discards the forward entries.
      await act(async () => control("Back").click());
      await act(async () => host.querySelector<HTMLButtonElement>(".schedules")!.click());
      expect(control("Forward").disabled).toBe(true);
    } finally { await act(async () => root.unmount()); }
  });

  it("skips entries for removed conversations", async () => {
    vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
    const host = document.createElement("div"), root = createRoot(host);
    try {
      await act(async () => root.render(<ClientWindow><Workspace /></ClientWindow>));
      await act(async () => host.querySelector<HTMLButtonElement>(".visit")!.click());
      await act(async () => root.render(<ClientWindow><Workspace sessions={["two"]} /></ClientWindow>));
      expect(host.querySelector<HTMLButtonElement>('.clientTitlebar [aria-label="Back"]')!.disabled).toBe(true);
    } finally { await act(async () => root.unmount()); }
  });

  it("reserves room for inset system window controls on macOS", async () => {
    vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
    window.sparkclawDesktop = { runtimeKind: "electron", capabilityVersion: 1, windowChrome: "inset" } as unknown as SparkClawDesktop;
    const host = document.createElement("div"), root = createRoot(host);
    try {
      await act(async () => root.render(<ClientWindow><Workspace /></ClientWindow>));
      const spacer = host.querySelector(".clientTitlebarLeading > .clientWindowControls")!;
      expect(spacer.getAttribute("aria-hidden")).toBe("true");
      expect(spacer.childElementCount).toBe(0);
    } finally { await act(async () => root.unmount()); }
  });

  it("keeps the in-page topbar without history on the web", async () => {
    vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
    const host = document.createElement("div"), root = createRoot(host);
    try {
      await act(async () => root.render(<Workspace />));
      const topbar = host.querySelector("header.topbar")!;
      expect([...topbar.querySelectorAll("button")].map(button => button.getAttribute("aria-label"))).toEqual(["Toggle sidebar", "Back to home", "Notifications"]);
      expect(host.querySelector('[aria-label="Back"]')).toBeNull();
    } finally { await act(async () => root.unmount()); }
  });

  it("shows the open conversation's title over the workspace", async () => {
    vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
    const host = document.createElement("div"), root = createRoot(host);
    const render = (title?: string) => root.render(<ClientWindow><WorkspaceTitlebar language="en" title={title} actions={null} /></ClientWindow>);
    try {
      await act(async () => render("Quarterly report"));
      const title = host.querySelector(".clientTitlebarMain .clientTitlebarTitle")!;
      expect([title.textContent, title.getAttribute("title")]).toEqual(["Quarterly report", "Quarterly report"]);
      await act(async () => render());
      expect(host.querySelector(".clientTitlebarTitle")).toBeNull();
    } finally { await act(async () => root.unmount()); }
  });

  it("moves the open browser's tabs into the title bar above the panel", async () => {
    vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
    vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
    function Panel({ active }: { active: boolean }) {
      const panel = useRef<HTMLElement | null>(null);
      return <aside ref={panel} className="desktopBrowserPanel"><ClientTitlebarBrowser active={active} panel={panel}><div className="tabs" /></ClientTitlebarBrowser></aside>;
    }
    const host = document.createElement("div"), root = createRoot(host);
    const render = (active: boolean, client = true) => root.render(client
      ? <ClientWindow><WorkspaceTitlebar language="en" actions={null} /><Panel active={active} /></ClientWindow> : <Panel active={active} />);
    try {
      await act(async () => render(true));
      const titlebar = host.querySelector<HTMLElement>(".clientTitlebar")!;
      expect(titlebar.querySelector(".clientTitlebarTrailing .clientTitlebarBrowser .tabs")).not.toBeNull();
      expect(host.querySelector(".desktopBrowserPanel .tabs")).toBeNull();
      expect(titlebar.hasAttribute("data-browser-open")).toBe(true);
      await act(async () => render(false));
      expect(host.querySelector(".tabs")).toBeNull();
      expect(titlebar.hasAttribute("data-browser-open")).toBe(false);
      await act(async () => render(true, false));
      expect(host.querySelector(".desktopBrowserPanel .tabs")).not.toBeNull();
    } finally { await act(async () => root.unmount()); }
  });
});
