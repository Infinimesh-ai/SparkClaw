// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { LocalFilesPanel } from "./LocalFilesPanel";
import { LocalDocumentPreview } from "./LocalDocumentPreview";
import { localFilesCapability, type LocalFile, type LocalFileChange, type LocalFilesBridge } from "./localFiles";
import { validDocument, type PreviewDocument } from '../local-preview/channel';

const directory: LocalFile = { ref: "root", locator: "/fixtures", name: "Fixtures", kind: "directory", size: 0, version: "v1", displayPath: "/fixtures" };
const documentFile = (id: string): LocalFile => ({ ref: id, locator: `/fixtures/${id}/report.txt`, name: "report.txt", kind: "file", size: 4, version: "v1", displayPath: `/fixtures/${id}/report.txt` });
beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
});
afterEach(() => { delete window.sparkclawLocalFiles; vi.unstubAllGlobals(); document.body.replaceChildren(); });
function bridge() {
  const listeners = new Set<(event: LocalFileChange) => void>();
  const first = documentFile("one"), second = documentFile("two");
  const files = {
    roots: vi.fn(async () => ({ roots: [directory], generation: "login-1" })), loadState: vi.fn(async () => ({ state: null, generation: "login-1" })),
    resolve: vi.fn(async (locator: string) => [directory, first, second].find(file => file.locator === locator)!),
    stat: vi.fn(async (ref: string) => [directory, first, second].find(file => file.ref === ref)!),
    list: vi.fn(async () => ({ entries: [first, second], page: 0, complete: true, listing: "list-1", totalLoaded: 2 })),
    setVisible: vi.fn(async () => ({})), saveState: vi.fn(async () => ({})), clear: vi.fn(async () => ({})),
    watch: vi.fn(async () => ({})), unwatch: vi.fn(async () => ({})), release: vi.fn(async () => ({})),
    cancel: vi.fn(async () => ({})), cancelPreview: vi.fn(async () => ({})), releasePreview: vi.fn(async () => ({})),
    readBytes: vi.fn(async () => ({ bytes: new Uint8Array([116, 101, 115, 116]), offset: 0, total: 4, version: "v1" })),
    onChange: (listener: (event: LocalFileChange) => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
  } as unknown as LocalFilesBridge;
  return { files, first, second, emit: (event: LocalFileChange) => listeners.forEach(listener => listener(event)) };
}
async function fixture(configure?: (mock: ReturnType<typeof bridge>) => void) {
  const mock = bridge(), host = document.createElement("div"); document.body.append(host);
  configure?.(mock);
  const root = createRoot(host);
  const render = (visible = true) => act(async () => root.render(<LocalFilesPanel files={mock.files} open={visible} visible={visible} language="en" wide={false} onWideChange={() => {}} onCollapse={() => {}} onShowFiles={() => {}} />));
  await render();
  const click = async (text: string) => act(async () => [...host.querySelectorAll<HTMLButtonElement>("button")].find(button => button.textContent === text)!.click());
  return { ...mock, host, root, render, click, close: () => act(async () => root.unmount()) };
}

it("retains independent same-name tabs, tree selection and lightweight state while hidden", async () => {
  const f = await fixture();
  try {
    await f.click("Fixtures");
    const rows = [...f.host.querySelectorAll<HTMLButtonElement>('[role="treeitem"]')].filter(row => row.textContent === "report.txt");
    await act(async () => rows[0].click());
    expect(f.host.querySelectorAll('iframe')).toHaveLength(1);
    await f.click("Files"); await act(async () => rows[1].click());
    expect(f.host.querySelectorAll('[role="tab"]')).toHaveLength(3);
    await f.click("Files"); await act(async () => rows[0].click());
    expect(f.host.querySelectorAll('[role="tab"]')).toHaveLength(3);
    await f.render(false); expect(f.host.querySelectorAll('iframe')).toHaveLength(0);
    expect(f.host.querySelectorAll('[role="tab"]')).toHaveLength(3);
    expect(f.files.cancelPreview).toHaveBeenCalled();
    await f.render(); expect(f.host.querySelectorAll('iframe')).toHaveLength(1);
    await act(async () => f.host.querySelector<HTMLButtonElement>('[aria-label="Close report.txt"]')!.click());
    expect(f.host.querySelectorAll('[role="tab"]')).toHaveLength(2);
    expect(f.files.release).toHaveBeenCalledWith("one");
  } finally { await f.close(); }
});

it("clears document frames and readable state on permission loss and login revocation", async () => {
  const f = await fixture();
  try {
    await f.click("Fixtures"); await f.click("report.txt");
    vi.mocked(f.files.stat).mockRejectedValue(Object.assign(new Error("Access denied"), { code: "permission_denied" }));
    await act(async () => f.emit({ kind: "changed", refs: ["one"], generation: "login-1" }));
    expect(f.host.querySelector('iframe')).toBeNull(); expect(f.host.textContent).toContain("Permission");
    await act(async () => f.emit({ kind: "revoked", generation: "login-2" }));
    expect(f.host.querySelectorAll('[role="tab"]')).toHaveLength(1);
    expect(f.host.textContent).not.toContain("report.txt");
  } finally { await f.close(); }
});

it("does not resurrect an in-flight open after login revocation", async () => {
  const f = await fixture();
  try {
    await f.click('Fixtures');
    let finish!: (file: LocalFile) => void;
    vi.mocked(f.files.stat).mockImplementation(() => new Promise(resolve => { finish = resolve; }));
    await f.click('report.txt'); await act(async () => f.emit({ kind: 'revoked', generation: 'login-2' }));
    await act(async () => finish(f.first));
    expect(f.host.querySelector('iframe')).toBeNull(); expect(f.host.querySelectorAll('[role="tab"]')).toHaveLength(1);
  } finally { await f.close(); }
});

it("restored tabs watch their source and clear on deletion before any further read", async () => {
  const f = await fixture(mock => vi.mocked(mock.files.loadState).mockResolvedValue({ generation: 'login-1', state: { tabs: [{ locator: mock.first.locator }], active: mock.first.locator, expanded: [], scrollTop: 0, showHidden: false, filter: '' } }));
  try {
    expect(f.files.watch).toHaveBeenCalledWith('one'); expect(f.host.querySelector('iframe')).not.toBeNull();
    vi.mocked(f.files.stat).mockRejectedValue(Object.assign(new Error('Deleted'), { code: 'unavailable' }));
    await act(async () => f.emit({ kind: 'changed', generation: 'login-1', refs: ['one'] }));
    expect(f.host.querySelector('iframe')).toBeNull();
  } finally { await f.close(); }
});

it("bounds rendered tree rows while retaining continuation and rejects collapsed late responses", async () => {
  const f = await fixture();
  try {
    const entries = Array.from({ length: 128 }, (_, index) => documentFile(String(index)));
    vi.mocked(f.files.list).mockResolvedValue({ entries, cursor: "cursor-1", page: 0, complete: false, listing: "list-1", totalLoaded: 128 });
    await f.click("Fixtures");
    expect(f.host.querySelectorAll('[role="treeitem"]').length).toBeLessThan(40);
    let resolve!: (value: Awaited<ReturnType<LocalFilesBridge['list']>>) => void;
    vi.mocked(f.files.list).mockImplementation(() => new Promise(done => { resolve = done; }));
    const refresh = f.host.querySelector<HTMLButtonElement>('[aria-label="Refresh current directory"]')!;
    await act(async () => refresh.click());
    await f.click("Fixtures");
    await act(async () => resolve({ entries, page: 0, complete: true, listing: "late" }));
    expect(f.host.querySelectorAll('[role="treeitem"]')).toHaveLength(1);
  } finally { await f.close(); }
});

it("preserves typed IPC failure codes outside contextBridge", async () => {
  window.sparkclawLocalFiles = { envelopeVersion: 1, onChange: () => () => {}, stat: vi.fn(async () => ({ ok: false, error: { code: "source_changed", message: "Changed" } })) } as unknown as LocalFilesBridge;
  await expect(localFilesCapability()!.stat("ref")).rejects.toMatchObject({ code: "source_changed" });
  expect(localFilesCapability()).toBe(localFilesCapability());
});

it("accepts only its isolated frame handshake and refuses requests for another document", async () => {
  const mock = bridge(), host = document.createElement("div"); document.body.append(host); const root = createRoot(host);
  const outgoing: unknown[] = [];
  const port = { start: vi.fn(), close: vi.fn(), postMessage: (value: unknown) => outgoing.push(value), onmessage: null as null | ((event: { data: unknown }) => Promise<void>) };
  vi.stubGlobal("MessageChannel", class { port1 = port; port2 = {}; });
  try {
    const escape = vi.fn(), position = vi.fn();
    await act(async () => root.render(<LocalDocumentPreview files={mock.files} file={mock.first} loginGeneration="login-1" zh={false} onPosition={position} onInvalid={() => {}} onEscape={escape} />));
    const frame = host.querySelector("iframe")!, post = vi.spyOn(frame.contentWindow!, "postMessage").mockImplementation(() => {});
    await act(async () => window.dispatchEvent(new MessageEvent("message", { source: frame.contentWindow, origin: "https://other.test", data: { type: "sparkclaw-preview:ready", protocol: 1 } })));
    expect(post).not.toHaveBeenCalled();
    await act(async () => window.dispatchEvent(new MessageEvent("message", { source: frame.contentWindow, origin: "sparkclaw-preview://viewer", data: { type: "sparkclaw-preview:ready", protocol: 1 } })));
    const init = post.mock.calls[0][0] as { document: PreviewDocument };
    expect(validDocument(init.document)).toBe(true);
    expect(JSON.stringify(init)).not.toContain("/fixtures"); expect(JSON.stringify(init)).not.toContain('"ref"');
    await port.onmessage!({ data: { type: "request", id: 1, documentId: "another", generation: init.document.generation, loginGeneration: "login-1", operation: "read", offset: 0, length: 4 } });
    expect(mock.files.readBytes).not.toHaveBeenCalled();
    await port.onmessage!({ data: { type: "request", id: 2, documentId: init.document.id, generation: init.document.generation, loginGeneration: "login-1", operation: "read", offset: 0, length: 4 } });
    expect(mock.files.readBytes).toHaveBeenCalledTimes(1); expect(outgoing).toHaveLength(1);
    await port.onmessage!({ data: { type: 'escape', documentId: init.document.id, generation: init.document.generation, loginGeneration: 'login-1' } }); expect(escape).toHaveBeenCalledOnce();
    await port.onmessage!({ data: { type: 'position', documentId: init.document.id, generation: init.document.generation, loginGeneration: 'login-1', position: { offset: 4096 } } }); expect(position).toHaveBeenCalledWith({ offset: 4096 });
  } finally { await act(async () => root.unmount()); }
  expect(port.close).toHaveBeenCalled();
});
