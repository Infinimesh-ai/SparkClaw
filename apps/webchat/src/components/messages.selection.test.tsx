// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { RenderedMessageText } from "./messages";

afterEach(() => { document.getSelection()?.removeAllRanges(); document.body.replaceChildren(); vi.unstubAllGlobals(); });

it.each(["\n", "\n\n"])("retains exact line/paragraph start boundaries with separator %j", async separator => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const mount = document.createElement("div"); document.body.append(mount);
  const root = createRoot(mount);
  const render = async (content: string) => act(async () => root.render(<RenderedMessageText content={content} />));
  try {
    await render(`First${separator}Second line`);
    const paragraphs = mount.querySelectorAll("p");
    const node = paragraphs[paragraphs.length - 1].lastChild!;
    const selection = document.getSelection()!;
    selection.setBaseAndExtent(node, 0, node, 6);
    await render(`First${separator}Second line grows`);
    expect(selection.toString()).toBe("Second");
    expect(selection.anchorNode).toBe(node); expect(selection.anchorOffset).toBe(0);
    const fragment = selection.getRangeAt(0).cloneContents();
    expect(fragment.querySelector("br, p")).toBeNull();
  } finally { await act(async () => root.unmount()); }
});

it.each([false, true])("keeps the native selection in growing text, backwards=%s", async backwards => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const mount = document.createElement("div"); document.body.append(mount);
  const root = createRoot(mount);
  const render = async (content: string) => act(async () => root.render(<RenderedMessageText content={content} />));
  try {
    await render("甲乙丙丁戊己");
    const node = mount.querySelector("p")!.firstChild!;
    const selection = document.getSelection()!;
    selection.setBaseAndExtent(node, backwards ? 4 : 1, node, backwards ? 1 : 4);
    await render("甲乙丙丁戊己庚辛");
    expect(selection.toString()).toBe("乙丙丁");
    expect(selection.anchorOffset).toBe(backwards ? 4 : 1);
    expect(selection.focusOffset).toBe(backwards ? 1 : 4);
    expect(mount.querySelector("p")!.firstChild).toBe(node);
    await render("甲乙丙丁戊己庚辛壬癸");
    expect(selection.toString()).toBe("乙丙丁");
    // A real replacement of the selected prefix must not restore stale text.
    await render("新的最终正文");
    expect(selection.toString()).not.toBe("乙丙丁");
  } finally { await act(async () => root.unmount()); }
});

it("preserves selection across inline nodes and leaves another answer's selection alone", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const mount = document.createElement("div"); document.body.append(mount);
  const root = createRoot(mount);
  const render = async (content: string) => act(async () => root.render(<><RenderedMessageText content={content} /><RenderedMessageText content="Other answer" /></>));
  try {
    await render("Before **bold** tail");
    const paragraph = mount.querySelector("p")!;
    const selection = document.getSelection()!;
    selection.setBaseAndExtent(paragraph.firstChild!, 3, paragraph.lastChild!, 3);
    const selected = selection.toString();
    await render("Before **bold** tail grows");
    expect(selection.toString()).toBe(selected);
    const other = mount.querySelectorAll("p")[1].firstChild!;
    selection.setBaseAndExtent(other, 0, other, 5);
    await render("Before **bold** tail grows again");
    expect(selection.toString()).toBe("Other");
    expect(selection.anchorNode).toBe(other);
  } finally { await act(async () => root.unmount()); }
});
