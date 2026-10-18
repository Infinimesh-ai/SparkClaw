// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { TaskSearch } from "./workbench";

describe("TaskSearch keyboard navigation", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    Reflect.deleteProperty(HTMLDialogElement.prototype, "showModal");
    Reflect.deleteProperty(HTMLDialogElement.prototype, "close");
  });
  it("navigates matches, preserves IME input, and restores the trigger on a single Escape", async () => {
    Object.defineProperty(HTMLDialogElement.prototype, "showModal", { configurable: true, value: function (this: HTMLDialogElement) { this.open = true; } });
    Object.defineProperty(HTMLDialogElement.prototype, "close", { configurable: true, value: function (this: HTMLDialogElement) { this.open = false; } });
    const host = document.createElement("div"); document.body.append(host);
    const root = createRoot(host), select = vi.fn(), background = vi.fn();
    const sessions = [{ id: "a", title: "Alpha" }, { id: "b", title: "Beta" }, { id: "hidden", title: "Managed", source: "mcp" }];
    function Harness() {
      const [open, setOpen] = useState(false);
      return <><button onClick={() => setOpen(true)}>Search</button>{open && <TaskSearch language="en" sessions={sessions} onSelect={select} onClose={() => setOpen(false)} />}</>;
    }
    const press = (target: HTMLElement, key: string, isComposing = false) => target.dispatchEvent(new KeyboardEvent("keydown", { key, isComposing, bubbles: true, cancelable: true }));
    const globalShortcut = (event: KeyboardEvent) => {
      if (!event.defaultPrevented && !document.querySelector('dialog[open]')) background();
    };
    document.addEventListener("keydown", globalShortcut);
    try {
      await act(async () => root.render(<Harness />));
      const trigger = host.querySelector<HTMLButtonElement>("button")!; trigger.focus();
      await act(async () => trigger.click());
      const input = host.querySelector("input")!;
      expect(document.activeElement).toBe(input);
      expect(host.querySelectorAll('[role="option"]')).toHaveLength(2);
      await act(async () => press(input, "ArrowDown"));
      expect(host.querySelector('[aria-selected="true"]')?.textContent).toBe("Beta");
      expect(document.activeElement).toBe(input);
      expect(input.getAttribute("aria-activedescendant")).toBe(host.querySelector('[aria-selected="true"]')?.id);
      await act(async () => press(input, "Enter", true)); expect(select).not.toHaveBeenCalled();
      await act(async () => press(input, "Enter")); expect(select).toHaveBeenCalledExactlyOnceWith(sessions[1]);
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "Alpha");
        input.dispatchEvent(new Event("input", { bubbles: true }));
      });
      expect(host.querySelector('[aria-selected="true"]')?.textContent).toBe("Alpha");
      background.mockClear();
      await act(async () => press(input, "Escape"));
      expect(host.querySelector("dialog")).toBeNull(); expect(document.activeElement).toBe(trigger);
      expect(background).not.toHaveBeenCalled();
    } finally { document.removeEventListener("keydown", globalShortcut); await act(async () => root.unmount()); host.remove(); }
  });
});
