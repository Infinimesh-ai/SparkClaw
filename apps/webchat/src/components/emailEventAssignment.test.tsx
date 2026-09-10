// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { EmailMessage } from "../api/email";
import { dictionaries } from "../i18n";
import { EmailEventAssignment, EmailEventRename } from "./emailEventAssignment";
const text = dictionaries.en;
const mail = { id: "m1", version: 8, conversation_id: "old" } as EmailMessage;
describe("manual event correction", () => {
  let container: HTMLElement;
  let root: ReturnType<typeof createRoot>;
  const saved = vi.fn(async () => {});
  const error = vi.fn();
  beforeEach(() => {
    container = document.createElement("div"); document.body.append(container); root = createRoot(container);
    vi.spyOn(api, "emailConversations").mockResolvedValue({ version: 1, conversations: [{ id: "target", version: 3, title: "Confirm delivery", participants: [], concerns: [], unseen_count: 0 }] });
    vi.spyOn(api, "assignEmailEvent").mockResolvedValue({ conversation_id: "target" });
    vi.spyOn(api, "renameEmailEvent").mockResolvedValue({ conversation: { id: "old", title: "Arrange meeting", version: 5, participants: [], concerns: [], unseen_count: 0 } });
  });
  afterEach(() => { act(() => root.unmount()); container.remove(); vi.restoreAllMocks(); vi.clearAllMocks(); });
  async function click(label: string) { await act(async () => [...container.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent === label)!.click()); }
  async function edit(input: HTMLInputElement, value: string) {
    await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, value); input.dispatchEvent(new Event("input", { bubbles: true })); });
  }
  it("moves a mail using its version and searches all event categories", async () => {
    await act(async () => root.render(<EmailEventAssignment mail={mail} text={text} onSaved={saved} onError={error} />));
    await click(text.email.changeEvent);
    expect(api.emailConversations).toHaveBeenCalledWith({ q: "", cursor: "", limit: 30 }, expect.any(AbortSignal));
    const select = container.querySelector("select")!;
    await act(async () => { select.value = "target"; select.dispatchEvent(new Event("change", { bubbles: true })); });
    await click(text.email.saveEvent);
    expect(api.assignEmailEvent).toHaveBeenCalledWith("m1", { conversation_id: "target", title: undefined, expected_version: 8, command_key: expect.any(String) });
    expect(saved).toHaveBeenCalledWith("target");
  });
  it("creates a purpose-named event and leaves failed corrections editable", async () => {
    vi.mocked(api.assignEmailEvent).mockRejectedValue(new Error("version conflict"));
    await act(async () => root.render(<EmailEventAssignment mail={mail} text={text} onSaved={saved} onError={error} />));
    await click(text.email.changeEvent);
    await act(async () => container.querySelector<HTMLInputElement>("input[type=checkbox]")!.click());
    await edit(container.querySelector<HTMLInputElement>("input:not([type=checkbox])")!, " Arrange meeting ");
    await click(text.email.saveEvent);
    expect(api.assignEmailEvent).toHaveBeenCalledWith("m1", { conversation_id: undefined, title: "Arrange meeting", expected_version: 8, command_key: expect.any(String) });
    expect(error).toHaveBeenCalled(); expect(saved).not.toHaveBeenCalled(); expect(container.querySelector("form")).not.toBeNull();
  });
  it("renames the event with a conversation version fence", async () => {
    await act(async () => root.render(<EmailEventRename conversation={{ id: "old", title: "Old name", version: 4, concerns: [], participants: [], unseen_count: 0 }} text={text} onSaved={saved} onError={error} />));
    await click(text.email.renameEvent);
    await edit(container.querySelector("input")!, "Arrange meeting");
    await click(text.email.saveEvent);
    expect(api.renameEmailEvent).toHaveBeenCalledWith("old", { title: "Arrange meeting", expected_version: 4, command_key: expect.any(String) });
    expect(saved).toHaveBeenCalled();
  });
});
