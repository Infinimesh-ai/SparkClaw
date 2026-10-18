// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { useDismissibleLayer } from "./useDismissibleLayer";

function Layer({ close, blocked = false, children }: { close: () => void; blocked?: boolean; children?: React.ReactNode }) {
  const ref = useDismissibleLayer<HTMLDivElement>({ open: true, onDismiss: close, modal: true, dismissible: !blocked });
  return <div ref={ref} role="dialog" tabIndex={-1}><button>First</button>{children}<button>Last</button></div>;
}

function key(target: Element | Document, value: string, shiftKey = false) {
  const event = new KeyboardEvent("keydown", { key: value, shiftKey, bubbles: true, cancelable: true });
  target.dispatchEvent(event);
  return event;
}

describe("useDismissibleLayer", () => {
  it("contains modal Tab and programmatic focus, then returns focus to its trigger", async () => {
    const host = document.createElement("div"); document.body.append(host);
    const root = createRoot(host);
    function Harness() {
      const [open, setOpen] = useState(false);
      return <><button onClick={() => setOpen(true)}>Open</button>{open && <Layer close={() => setOpen(false)} />}</>;
    }
    try {
      await act(async () => root.render(<Harness />));
      const trigger = host.querySelector<HTMLButtonElement>("button")!; trigger.focus();
      await act(async () => trigger.click());
      const buttons = host.querySelectorAll<HTMLButtonElement>('[role="dialog"] button');
      expect(document.activeElement).toBe(buttons[0]);
      expect(key(buttons[0], "Tab", true).defaultPrevented).toBe(true);
      expect(document.activeElement).toBe(buttons[1]);
      key(buttons[1], "Tab"); expect(document.activeElement).toBe(buttons[0]);
      trigger.focus(); expect(document.activeElement).toBe(buttons[0]);
      await act(async () => key(buttons[0], "Escape"));
      expect(host.querySelector('[role="dialog"]')).toBeNull();
      expect(document.activeElement).toBe(trigger);
    } finally { await act(async () => root.unmount()); host.remove(); }
  });

  it("claims one Escape for the top layer and suppresses background shortcuts while busy", async () => {
    const host = document.createElement("div"); document.body.append(host);
    const root = createRoot(host), underneath = vi.fn(), close = vi.fn();
    const globalShortcut = (event: KeyboardEvent) => {
      if (!event.defaultPrevented && !document.querySelector('[role="dialog"]')) underneath();
    };
    document.addEventListener("keydown", globalShortcut);
    function Harness({ blocked = false }: { blocked?: boolean }) {
      const [top, setTop] = useState(false);
      return <Layer close={close}><button onClick={() => setTop(true)}>Another</button>{top && <Layer blocked={blocked} close={() => setTop(false)} />}</Layer>;
    }
    try {
      await act(async () => root.render(<Harness />));
      await act(async () => host.querySelectorAll<HTMLButtonElement>("button")[1].click());
      expect(host.querySelectorAll('[role="dialog"]')).toHaveLength(2);
      await act(async () => key(document.activeElement!, "Escape"));
      expect(host.querySelectorAll('[role="dialog"]')).toHaveLength(1);
      expect(close).not.toHaveBeenCalled(); expect(underneath).not.toHaveBeenCalled();
      await act(async () => host.querySelectorAll<HTMLButtonElement>("button")[1].click());
      await act(async () => root.render(<Harness blocked />));
      await act(async () => key(document.activeElement!, "Escape"));
      expect(host.querySelectorAll('[role="dialog"]')).toHaveLength(2);
      expect(close).not.toHaveBeenCalled(); expect(underneath).not.toHaveBeenCalled();
    } finally { document.removeEventListener("keydown", globalShortcut); await act(async () => root.unmount()); host.remove(); }
  });

  it("respects an Escape already handled by a focused editor", async () => {
    const host = document.createElement("div"); document.body.append(host);
    const root = createRoot(host), close = vi.fn();
    try {
      await act(async () => root.render(<Layer close={close}><input onKeyDown={event => event.preventDefault()} /></Layer>));
      const editor = host.querySelector("input")!; editor.focus();
      await act(async () => key(editor, "Escape"));
      expect(close).not.toHaveBeenCalled();
    } finally { await act(async () => root.unmount()); host.remove(); }
  });
});
