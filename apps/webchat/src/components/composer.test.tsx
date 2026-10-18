// @vitest-environment jsdom
import { act, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { ComposerSurface } from "./composer";
import { dictionaries } from "../i18n";
import type { VoiceInputModel } from "../hooks/useVoiceInput";

vi.mock("./emailPopup", () => ({ EmailPopupEntry: () => <button type="button">Mail</button> }));
afterEach(() => { document.body.replaceChildren(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });
it("guards IME/voice/busy submits, grows to six lines and retains input and good attachments after upload failure", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
  let height = 28;
  vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockImplementation(() => height);
  const send = vi.fn(), upload = vi.fn().mockRejectedValueOnce(new Error("Upload failed")).mockResolvedValue(undefined);
  const voice = { state: "idle", active: false, toggle: vi.fn() } as unknown as VoiceInputModel;
  function Fixture({ busy = false }: { busy?: boolean }) {
    const [value,setValue] = useState("draft"); const ref = useRef<HTMLTextAreaElement>(null);
    return <ComposerSurface text={dictionaries.en} language="en" activeSession="a" activeInput={value} activeAttachments={[{name:"good.txt",rel_path:"good.txt"}]}
      busy={busy} voice={voice} composerInputRef={ref} onInputChange={setValue} onSend={send} onUploadDocument={upload}
      onChooseDocument={vi.fn()} onOpenAttachment={vi.fn()} onRemoveAttachment={vi.fn()} />;
  }
  const style = document.createElement("style"); style.textContent = "textarea { line-height:28px; padding:0; }"; document.head.append(style);
  const host=document.createElement("div");document.body.append(host);const root=createRoot(host);
  const key = async (options: KeyboardEventInit) => act(async () => host.querySelector("textarea")!.dispatchEvent(new KeyboardEvent("keydown", { key:"Enter",bubbles:true,cancelable:true,...options })));
  try {
    await act(async () => root.render(<Fixture />));
    const input = host.querySelector("textarea")!;
    await act(async () => input.dispatchEvent(new CompositionEvent("compositionstart", {bubbles:true})));
    await key({isComposing:true});expect(send).not.toHaveBeenCalled();
    await act(async () => input.dispatchEvent(new CompositionEvent("compositionend", {bubbles:true})));
    await key({});expect(send).not.toHaveBeenCalled();
    await new Promise(r => setTimeout(r,90));
    await key({shiftKey:true});expect(send).not.toHaveBeenCalled();
    await key({});expect(send).toHaveBeenCalledTimes(1);expect(document.activeElement).toBe(input);
    height=840;
    await act(async () => { Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,"value")!.set!.call(input,"long\n".repeat(30)); input.dispatchEvent(new Event("input",{bubbles:true})); });
    expect(parseFloat(input.style.height)).toBe(168);expect(input.style.overflowY).toBe("auto");
    const fileInput=host.querySelector<HTMLInputElement>('input[type="file"]')!;
    Object.defineProperty(fileInput,"files",{configurable:true,value:[new File(["fixture"],"failed.txt")]});
    await act(async () => fileInput.dispatchEvent(new Event("change",{bubbles:true})));
    expect(host.textContent).toContain("failed.txt: Upload failed");expect(host.textContent).toContain("good.txt");expect(input.value).toBe("long\n".repeat(30));
    await act(async () => host.querySelector<HTMLButtonElement>(".composerUploadFailure button")!.click());
    expect(upload).toHaveBeenCalledTimes(2);expect(host.querySelector(".composerUploadFailure")).toBeNull();
    await act(async () => root.render(<Fixture busy />));await key({});expect(send).toHaveBeenCalledTimes(1);
  } finally { await act(async () => root.unmount());style.remove(); }
});
