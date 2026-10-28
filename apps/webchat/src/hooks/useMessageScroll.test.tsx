// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { useMessageScroll } from "./useMessageScroll";
import { browserFocusRequired } from "./useBrowserLayout";

afterEach(() => { document.body.replaceChildren(); document.getSelection()?.removeAllRanges(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

it("uses measured split space with hysteresis", () => {
  expect(browserFocusRequired(1196, 640, false)).toBe(false);
  expect(browserFocusRequired(936, 640, false)).toBe(true);
  expect(browserFocusRequired(1170, 640, true)).toBe(true);
  expect(browserFocusRequired(1196, 640, true)).toBe(false);
});

it("narrows a fitted browser before it takes over the conversation", () => {
  expect(browserFocusRequired(1006, 640, false, true)).toBe(false);
  expect(browserFocusRequired(880, 640, false, true)).toBe(true);
  expect(browserFocusRequired(900, 640, true, true)).toBe(true);
  expect(browserFocusRequired(920, 640, true, true)).toBe(false);
});

it("retains reading anchors, selection and per-conversation positions across final identity changes", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
  let total = 1000, shift = 0;
  const tops = new WeakMap<HTMLElement, number>();
  vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockImplementation(function(this: HTMLElement) { return this.className === "messageList" ? 100 : 200; });
  vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockImplementation(() => total);
  vi.spyOn(HTMLElement.prototype, "scrollTop", "get").mockImplementation(function(this: HTMLElement) { return tops.get(this) ?? 0; });
  vi.spyOn(HTMLElement.prototype, "scrollTop", "set").mockImplementation(function(this: HTMLElement, value: number) { tops.set(this, Math.min(total - 100, Math.max(0, value))); });
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function(this: HTMLElement) {
    const list = this.parentElement;
    const top = this.className === "message" ? Number(this.dataset.index) * 200 + shift - (list?.scrollTop ?? 0) : 0;
    return { top, bottom: top + (this.className === "message" ? 200 : 100) } as DOMRect;
  });
  function Fixture({ session, revision, empty = false }: { session: string; revision: number; empty?: boolean }) {
    const scroll = useMessageScroll(session);
    return <><div ref={scroll.listRef} className="messageList">{!empty && [0,1,2,3,4].map(i => <article key={i} className="message" data-index={i} data-scroll-anchor={`execution:${i}`} data-message-id={`${revision}-${i}`}>Stable selected text {i}<span>{revision}</span></article>)}</div><button onClick={scroll.resume}>{scroll.away ? "latest" : "following"}</button></>;
  }
  const mount = document.createElement("div"); document.body.append(mount); const root = createRoot(mount);
  const render = async (session: string, revision: number, empty = false) => act(async () => root.render(<Fixture session={session} revision={revision} empty={empty} />));
  const settle = async () => act(async () => { await new Promise(resolve => setTimeout(resolve, 25)); });
  try {
    await render("a",0);
    const list = mount.querySelector<HTMLElement>(".messageList")!;
    expect(list.scrollTop).toBe(900);
    await act(async () => { list.scrollTop = 450; list.dispatchEvent(new Event("scroll")); });
    expect(mount.querySelector("button")?.textContent).toBe("latest");
    total += 100; shift = 100;
    await render("a",1); await settle();
    expect(list.scrollTop).toBe(550); // Same execution anchor, different persisted ID.
    await render("b",1); expect(list.scrollTop).toBe(1000);
    await render("a",1,true);
    await act(async () => { list.scrollTop = 0; list.dispatchEvent(new Event("scroll")); });
    await render("a",2); await settle();
    expect(list.scrollTop).toBe(550);
    await render("b",2);
    shift += 50; total += 50; // The retained chat returns at a different text/layout size.
    await render("a",2); expect(list.scrollTop).toBe(600);
    await act(async () => mount.querySelector("button")!.click()); expect(list.scrollTop).toBe(1050);
    const range = document.createRange(); range.selectNodeContents(mount.querySelector("article")!.firstChild!);
    await act(async () => { const selection = document.getSelection()!; selection.removeAllRanges(); selection.addRange(range); document.dispatchEvent(new Event("selectionchange")); });
    const selected = document.getSelection()!.toString();
    total += 400; await render("a",3); await settle();
    expect(list.scrollTop).toBe(1050); expect(document.getSelection()!.toString()).toBe(selected);
    await act(async () => { document.getSelection()!.removeAllRanges(); mount.querySelector("button")!.click(); });
    expect(list.scrollTop).toBe(1450);
  } finally { await act(async () => root.unmount()); }
});
