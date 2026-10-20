// @vitest-environment jsdom
import { act, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { ComposerSurface, type ComposerSurfaceProps } from "./composer";
import { dictionaries } from "../i18n";
import type { VoiceInputModel } from "../hooks/useVoiceInput";

vi.mock("./emailPopup", () => ({ EmailPopupEntry: ({ enabled }: { enabled: boolean }) => <button type="button" disabled={!enabled}>Mail</button> }));
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

it("covers and locks all composer entry points until approvals resolve, then restores the existing draft and attachments", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
  const send = vi.fn(), upload = vi.fn(), choose = vi.fn(), remove = vi.fn(), change = vi.fn();
  const voice = { state: "recording", active: true, toggle: vi.fn(), cancel: vi.fn(), stopPreview: vi.fn() } as unknown as VoiceInputModel;
  function Fixture({ pending }: { pending: boolean }) {
    const ref = useRef<HTMLTextAreaElement>(null);
    return <ComposerSurface text={dictionaries.en} language="en" activeSession="a" activeInput="retained draft"
      activeAttachments={[{ name: "good.txt", rel_path: "good.txt" }]} busy={false} voice={voice}
      composerInputRef={ref} approvalPending={pending} approvals={<button type="button">Approval choice</button>}
      onInputChange={change} onSend={send} onUploadDocument={upload} onChooseDocument={choose}
      onOpenAttachment={vi.fn()} onRemoveAttachment={remove} />;
  }
  const host = document.createElement("div"); document.body.append(host); const root = createRoot(host);
  try {
    await act(async () => root.render(<Fixture pending={false} />));
    const input = host.querySelector("textarea")!;
    input.focus();
    await act(async () => root.render(<Fixture pending />));
    expect(host.querySelector<HTMLElement>(".composerInputSurface")!.hidden).toBe(true);
    expect(input.disabled).toBe(true);
    expect(document.activeElement).toBe(host.querySelector(".composerApprovalOverlay"));
    expect(voice.cancel).toHaveBeenCalledTimes(1);
    expect(voice.stopPreview).toHaveBeenCalledTimes(1);
    for (const control of host.querySelectorAll<HTMLButtonElement>(".uploadButton, .attachmentRemove, .voiceButton, .voiceDeviceButton, .sendButton")) {
      expect(control.disabled).toBe(true);
      await act(async () => control.click());
    }
    expect([...host.querySelectorAll<HTMLButtonElement>(".composer button")].find(button => button.textContent === "Mail")!.disabled).toBe(true);
    const fileInput = host.querySelector<HTMLInputElement>('input[type="file"]')!;
    Object.defineProperty(fileInput, "files", { configurable: true, value: [new File(["fixture"], "late.txt")] });
    await act(async () => {
      fileInput.dispatchEvent(new Event("change", { bubbles: true }));
      input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
      host.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, "new requirement");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    for (const callback of [send, upload, choose, remove, change, voice.toggle]) expect(callback).not.toHaveBeenCalled();
    voice.active = false;
    await act(async () => root.render(<Fixture pending={false} />));
    expect(host.querySelector(".composerApprovalOverlay")).toBeNull();
    expect(host.querySelector("textarea")).toBe(input);
    expect(input.disabled).toBe(false);
    expect(input.value).toBe("retained draft");
    expect(host.querySelector(".attachmentTray")!.textContent).toContain("good.txt");
    await act(async () => host.querySelector<HTMLButtonElement>(".sendButton")!.click());
    expect(send).toHaveBeenCalledTimes(1);
  } finally { await act(async () => root.unmount()); }
});

type UploadAvailability = Partial<Pick<ComposerSurfaceProps, "busy" | "uploadingDocument" | "canCompose" | "filesEnabled">>;
it.each<[string, UploadAvailability]>([
  ["file capability revoked", { filesEnabled: false }],
  ["conversation no longer editable", { canCompose: false }],
  ["another document uploading", { uploadingDocument: true }],
  ["workbench busy", { busy: true }],
])("keeps failed-upload retry disabled while %s and resumes without losing draft or attachments", async (_reason, unavailable) => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
  const upload = vi.fn().mockRejectedValueOnce(new Error("Upload failed")).mockResolvedValue(undefined);
  const file = new File(["fixture"], "failed.txt");
  const voice = { state: "idle", active: false, toggle: vi.fn() } as unknown as VoiceInputModel;
  function Fixture(props: UploadAvailability) {
    const [value, setValue] = useState("retained draft");
    const ref = useRef<HTMLTextAreaElement>(null);
    return <ComposerSurface text={dictionaries.en} language="en" activeSession="a" activeInput={value}
      activeAttachments={[{ name: "good.txt", rel_path: "good.txt" }]} busy={false} {...props}
      voice={voice} composerInputRef={ref} onInputChange={setValue} onSend={vi.fn()} onUploadDocument={upload}
      onChooseDocument={vi.fn()} onOpenAttachment={vi.fn()} onRemoveAttachment={vi.fn()} />;
  }
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host);
  const render = (props: UploadAvailability = {}) => act(async () => root.render(<Fixture {...props} />));
  const retained = () => {
    expect(host.querySelector<HTMLTextAreaElement>("textarea")!.value).toBe("retained draft");
    expect(host.querySelector(".attachmentTray")?.textContent).toContain("good.txt");
  };
  try {
    await render();
    const fileInput = host.querySelector<HTMLInputElement>('input[type="file"]')!;
    Object.defineProperty(fileInput, "files", { configurable: true, value: [file] });
    await act(async () => fileInput.dispatchEvent(new Event("change", { bubbles: true })));
    expect(upload).toHaveBeenCalledTimes(1);
    expect(host.textContent).toContain("failed.txt: Upload failed"); retained();

    await render(unavailable);
    const retry = host.querySelector<HTMLButtonElement>(".composerUploadFailure button")!;
    expect(retry.disabled).toBe(true);
    expect(host.querySelector<HTMLButtonElement>(".composer > .uploadButton")!.disabled).toBe(true);
    await act(async () => retry.click());
    // A file picker opened before capability loss can still deliver a change.
    await act(async () => fileInput.dispatchEvent(new Event("change", { bubbles: true })));
    expect(upload).toHaveBeenCalledTimes(1);
    expect(host.textContent).toContain("failed.txt: Upload failed"); retained();

    await render();
    expect(retry.disabled).toBe(false);
    expect(host.querySelector<HTMLButtonElement>(".composer > .uploadButton")!.disabled).toBe(false);
    await act(async () => retry.click());
    expect(upload).toHaveBeenCalledTimes(2);
    expect(upload).toHaveBeenLastCalledWith(file);
    expect(host.querySelector(".composerUploadFailure")).toBeNull(); retained();
  } finally { await act(async () => root.unmount()); }
});
