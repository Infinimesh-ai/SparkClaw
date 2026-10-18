// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { dictionaries } from "../i18n";
import { NotificationCenter } from "./notificationCenter";
import { ScheduleCreateDialog } from "./schedules";

it("closes the dialog above notifications one layer per Escape and restores each trigger", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host), read = vi.fn(async () => {}), create = vi.fn(async () => null), background = vi.fn();
  const globalShortcut = (event: KeyboardEvent) => {
    if (!event.defaultPrevented && !document.querySelector('dialog[open], [role="dialog"], [role="menu"], .notificationInbox')) background();
  };
  document.addEventListener("keydown", globalShortcut);
  function Harness() {
    const [notificationsOpen, setNotificationsOpen] = useState(false);
    const [dialogOpen, setDialogOpen] = useState(false);
    return <>
      <NotificationCenter notifications={[]} unreadCount={0} open={notificationsOpen} toast={null} language="en" text={dictionaries.en}
        onToggle={() => setNotificationsOpen(value => !value)} onDismissToast={() => {}} onRead={read} onReadAll={read} />
      <button className="openSchedule" onClick={() => setDialogOpen(true)}>New schedule</button>
      {dialogOpen && <ScheduleCreateDialog busy={false} text={dictionaries.en} onClose={() => setDialogOpen(false)} onCreate={create} />}
    </>;
  }
  try {
    await act(async () => root.render(<Harness />));
    const bell = host.querySelector<HTMLButtonElement>(".notificationBell")!;
    bell.focus(); await act(async () => bell.click());
    const scheduleTrigger = host.querySelector<HTMLButtonElement>(".openSchedule")!;
    scheduleTrigger.focus(); await act(async () => scheduleTrigger.click());
    expect(document.activeElement).toBe(host.querySelector("textarea"));
    const firstEscape = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
    await act(async () => document.activeElement!.dispatchEvent(firstEscape));
    expect(firstEscape.defaultPrevented).toBe(true);
    expect(host.querySelector('[role="dialog"]')).toBeNull();
    expect(host.querySelector(".notificationInbox")).not.toBeNull();
    expect(document.activeElement).toBe(scheduleTrigger);
    const secondEscape = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
    await act(async () => scheduleTrigger.dispatchEvent(secondEscape));
    expect(secondEscape.defaultPrevented).toBe(true);
    expect(host.querySelector(".notificationInbox")).toBeNull();
    expect(document.activeElement).toBe(bell);
    expect(background).not.toHaveBeenCalled(); expect(read).not.toHaveBeenCalled(); expect(create).not.toHaveBeenCalled();
  } finally { document.removeEventListener("keydown", globalShortcut); await act(async () => root.unmount()); host.remove(); vi.unstubAllGlobals(); }
});
