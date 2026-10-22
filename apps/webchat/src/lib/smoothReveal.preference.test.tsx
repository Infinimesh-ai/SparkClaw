// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { APPEARANCE_CHANGE_EVENT, setMotion } from "./appearance";
import { useSmoothReveal } from "./smoothReveal";

function Preview({ content }: { content: string }) {
  return <output>{useSmoothReveal(content)}</output>;
}

let host: HTMLDivElement;
let root: ReturnType<typeof createRoot>;
let previousMotion: string | null;
let frames: Map<number, FrameRequestCallback>;
let systemMotion: { matches: boolean; listeners: Set<() => void> };

beforeEach(() => {
  // Node 26 shadows jsdom storage; keep preferences in an isolated memory store.
  const values = new Map<string, string>();
  const storage = { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => void values.set(key, String(value)),
    removeItem: (key: string) => void values.delete(key), clear: () => values.clear() };
  Object.defineProperty(window, "localStorage", { configurable: true, value: storage });
  Object.defineProperty(globalThis, "localStorage", { configurable: true, value: storage });
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  previousMotion = window.localStorage.getItem("sparkclaw.appearance.motion");
  window.localStorage.removeItem("sparkclaw.appearance.motion");
  frames = new Map();
  let nextFrame = 1;
  vi.stubGlobal("requestAnimationFrame", vi.fn((callback: FrameRequestCallback) => { const id = nextFrame++; frames.set(id, callback); return id; }));
  vi.stubGlobal("cancelAnimationFrame", vi.fn((id: number) => frames.delete(id)));
  systemMotion = { matches: false, listeners: new Set() };
  vi.stubGlobal("matchMedia", vi.fn(() => ({
    get matches() { return systemMotion.matches; },
    addEventListener: (_type: string, listener: () => void) => systemMotion.listeners.add(listener),
    removeEventListener: (_type: string, listener: () => void) => systemMotion.listeners.delete(listener)
  })));
  host = document.createElement("div");
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  if (previousMotion === null) window.localStorage.removeItem("sparkclaw.appearance.motion");
  else window.localStorage.setItem("sparkclaw.appearance.motion", previousMotion);
  for (const key of ["theme", "textSize", "resolvedTheme", "readingWidth", "transparency", "resolvedTransparency", "motion", "resolvedMotion"]) delete document.documentElement.dataset[key];
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it("finishes an active reveal immediately when reduced motion is selected and resumes only for new content", async () => {
  await act(async () => root.render(<Preview content="" />));
  await act(async () => root.render(<Preview content="First complete snapshot" />));
  expect(host.textContent).toBe("");
  expect(frames.size).toBe(1);

  await act(async () => setMotion("reduced"));
  expect(host.textContent).toBe("First complete snapshot");
  expect(frames.size).toBe(0);

  await act(async () => root.render(<Preview content="First complete snapshot plus incoming content" />));
  expect(host.textContent).toBe("First complete snapshot plus incoming content");
  expect(frames.size).toBe(0);
  await act(async () => setMotion("system"));
  expect(host.textContent).toBe("First complete snapshot plus incoming content");
  expect(frames.size).toBe(0);
  await act(async () => root.render(<Preview content="First complete snapshot plus incoming content and a new suffix" />));
  expect(host.textContent).toBe("First complete snapshot plus incoming content");
  expect(frames.size).toBe(1);
});

it("honors a live system reduction and removes preference listeners on unmount", async () => {
  const removeListener = vi.spyOn(window, "removeEventListener");
  await act(async () => root.render(<Preview content="" />));
  await act(async () => root.render(<Preview content="An active preview" />));
  expect(frames.size).toBe(1);
  await act(async () => {
    systemMotion.matches = true;
    for (const listener of systemMotion.listeners) listener();
  });
  expect(host.textContent).toBe("An active preview");
  expect(frames.size).toBe(0);
  await act(async () => root.render(null));
  expect(systemMotion.listeners.size).toBe(0);
  expect(removeListener).toHaveBeenCalledWith(APPEARANCE_CHANGE_EVENT, expect.any(Function));
});
