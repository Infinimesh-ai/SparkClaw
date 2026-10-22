// @vitest-environment jsdom
import { act, StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { dictionaries } from "../i18n";
import { useDismissibleLayer } from "../hooks/useDismissibleLayer";
import { ConfirmationProvider, useConfirm } from "./confirmation";

describe("shared confirmation", () => {
  let host: HTMLDivElement;
  let root: ReturnType<typeof createRoot>;
  let confirm: ReturnType<typeof useConfirm>;
  const outcomes: boolean[] = [];

  function Consumer() {
    confirm = useConfirm(dictionaries.en);
    return <button className="confirmationTrigger" onClick={() => void confirm("Remove this saved credential?").then(result => outcomes.push(result))}>Remove credential</button>;
  }

  function render(consumer = true) {
    root.render(<StrictMode><ConfirmationProvider>{consumer && <Consumer />}</ConfirmationProvider></StrictMode>);
  }

  beforeEach(async () => {
    vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
    outcomes.length = 0;
    host = document.createElement("div"); document.body.append(host);
    root = createRoot(host);
    await act(async () => render());
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
    vi.unstubAllGlobals();
  });

  async function open() {
    const trigger = host.querySelector<HTMLButtonElement>("button")!;
    trigger.focus();
    await act(async () => trigger.click());
    return trigger;
  }

  it("keeps the request pending until explicit continuation and restores focus", async () => {
    const trigger = await open();
    expect(outcomes).toEqual([]);
    expect(document.activeElement).toBe(document.querySelector(".confirmationCancel"));
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain("Remove this saved credential?");
    await act(async () => document.querySelector<HTMLButtonElement>(".confirmationAccept")!.click());
    expect(outcomes).toEqual([true]);
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it.each(["cancel", "escape", "backdrop"] as const)("resolves false for %s and restores the trigger", async (method) => {
    const trigger = await open();
    await act(async () => {
      if (method === "cancel") document.querySelector<HTMLButtonElement>(".confirmationCancel")!.click();
      else if (method === "escape") document.activeElement!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
      else document.querySelector(".confirmationOverlay")!.dispatchEvent(new MouseEvent("mousedown", { bubbles: true }));
    });
    expect(outcomes).toEqual([false]);
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it("contains keyboard focus while the modal is open", async () => {
    const trigger = await open();
    const cancel = document.querySelector<HTMLButtonElement>(".confirmationCancel")!;
    const accept = document.querySelector<HTMLButtonElement>(".confirmationAccept")!;
    await act(async () => {
      cancel.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", shiftKey: true, bubbles: true, cancelable: true }));
    });
    expect(document.activeElement).toBe(accept);
    await act(async () => trigger.focus());
    expect(document.activeElement).toBe(cancel);
  });

  it("keeps destructive styling explicit and dismisses only the top dialog", async () => {
    const dismissOuter = vi.fn();
    function NestedConsumer() {
      const request = useConfirm(dictionaries.en);
      const ref = useDismissibleLayer<HTMLDivElement>({ open: true, modal: true, onDismiss: dismissOuter });
      return <div ref={ref} role="dialog" tabIndex={-1}><button className="nestedTrigger" onClick={() => void request("Delete this item?", { danger: true }).then(result => outcomes.push(result))}>Delete</button></div>;
    }
    await act(async () => root.render(<StrictMode><ConfirmationProvider><NestedConsumer /></ConfirmationProvider></StrictMode>));
    const trigger = host.querySelector<HTMLButtonElement>(".nestedTrigger")!;
    await act(async () => trigger.click());
    expect(document.querySelector(".confirmationAccept")?.classList.contains("dangerButton")).toBe(true);
    await act(async () => document.activeElement!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true })));
    expect(outcomes).toEqual([false]);
    expect(dismissOuter).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(trigger);
    await act(async () => trigger.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true })));
    expect(dismissOuter).toHaveBeenCalledOnce();
  });

  it("shows queued requests in order and cancels all pending work when the caller unmounts", async () => {
    let first!: Promise<boolean>, second!: Promise<boolean>, third!: Promise<boolean>;
    await act(async () => {
      first = confirm("First request");
      second = confirm("Second request");
      third = confirm("Third request");
    });
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain("First request");
    await act(async () => document.querySelector<HTMLButtonElement>(".confirmationAccept")!.click());
    await expect(first).resolves.toBe(true);
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain("Second request");
    await act(async () => render(false));
    await expect(second).resolves.toBe(false);
    await expect(third).resolves.toBe(false);
    expect(document.querySelector('[role="dialog"]')).toBeNull();
  });

  it("settles active and queued requests when the provider unmounts", async () => {
    let first!: Promise<boolean>, second!: Promise<boolean>;
    await act(async () => { first = confirm("First request"); second = confirm("Second request"); });
    await act(async () => root.render(null));
    await expect(first).resolves.toBe(false);
    await expect(second).resolves.toBe(false);
    expect(document.querySelector('[role="dialog"]')).toBeNull();
  });
});
