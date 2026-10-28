// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ClientWindow, useClientTitlebar, WorkspaceTitlebar } from "./clientWindow";
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
});
