// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  APPEARANCE_CHANGE_EVENT,
  applyAppearance,
  currentMotion,
  currentReadingWidth,
  currentTextSize,
  currentTransparency,
  setMotion,
  setReadingWidth,
  setTextSize,
  setTransparency
} from "./appearance";

const textSizeKey = "sparkclaw.appearance.text-size";
const preferenceKeys = ["theme", "text-size", "reading-width", "transparency", "motion"].map((key) => `sparkclaw.appearance.${key}`);
let storedPreferences: (string | null)[];

function mockMediaPreferences() {
  const values = new Map<string, { matches: boolean; listeners: Set<() => void>; addEventListener: ReturnType<typeof vi.fn>; removeEventListener: ReturnType<typeof vi.fn> }>();
  const media = (query: string) => {
    if (!values.has(query)) {
      const listeners = new Set<() => void>();
      values.set(query, {
        matches: false,
        listeners,
        addEventListener: vi.fn((_type: string, listener: () => void) => listeners.add(listener)),
        removeEventListener: vi.fn((_type: string, listener: () => void) => listeners.delete(listener))
      });
    }
    return values.get(query)!;
  };
  vi.stubGlobal("matchMedia", vi.fn(media));
  return {
    media,
    change(query: string, matches: boolean) {
      const preference = media(query);
      preference.matches = matches;
      for (const listener of preference.listeners) listener();
    }
  };
}

beforeEach(() => {
  // Node 26 shadows jsdom storage; keep preferences in an isolated memory store.
  const values = new Map<string, string>();
  const storage = { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => void values.set(key, String(value)),
    removeItem: (key: string) => void values.delete(key), clear: () => values.clear() };
  Object.defineProperty(window, "localStorage", { configurable: true, value: storage });
  Object.defineProperty(globalThis, "localStorage", { configurable: true, value: storage });
  storedPreferences = preferenceKeys.map((key) => window.localStorage.getItem(key));
  for (const key of preferenceKeys) window.localStorage.removeItem(key);
});

afterEach(() => {
  for (const [index, key] of preferenceKeys.entries()) {
    const value = storedPreferences[index];
    if (value === null) window.localStorage.removeItem(key);
    else window.localStorage.setItem(key, value);
  }
  for (const key of ["textSize", "theme", "resolvedTheme", "readingWidth", "transparency", "resolvedTransparency", "motion", "resolvedMotion"]) delete document.documentElement.dataset[key];
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it.each(["small", "default", "large"] as const)("persists %s text and restores it when appearance initializes again", (size) => {
  setTextSize(size);
  expect(window.localStorage.getItem(textSizeKey)).toBe(size);
  expect(document.documentElement.dataset.textSize).toBe(size);

  delete document.documentElement.dataset.textSize;
  const dispose = applyAppearance();
  try {
    expect(currentTextSize()).toBe(size);
    expect(document.documentElement.dataset.textSize).toBe(size);
  } finally {
    dispose();
  }
});

it("restores a saved Small preference during startup without resetting it", () => {
  window.localStorage.setItem(textSizeKey, "small");
  const dispose = applyAppearance();
  try {
    expect(document.documentElement.dataset.textSize).toBe("small");
    expect(window.localStorage.getItem(textSizeKey)).toBe("small");
  } finally {
    dispose();
  }
});

it("uses the default size when a saved text preference is absent or unsupported", () => {
  expect(currentTextSize()).toBe("default");
  window.localStorage.setItem(textSizeKey, "unsupported-size");
  const dispose = applyAppearance();
  try {
    expect(currentTextSize()).toBe("default");
    expect(document.documentElement.dataset.textSize).toBe("default");
  } finally {
    dispose();
  }
});

it("persists reading and material preferences and restores their resolved startup appearance", () => {
  setReadingWidth("wide");
  setTransparency("reduced");
  setMotion("reduced");
  expect(window.localStorage.getItem("sparkclaw.appearance.reading-width")).toBe("wide");
  expect(window.localStorage.getItem("sparkclaw.appearance.transparency")).toBe("reduced");
  expect(window.localStorage.getItem("sparkclaw.appearance.motion")).toBe("reduced");

  for (const key of ["readingWidth", "transparency", "resolvedTransparency", "motion", "resolvedMotion"]) delete document.documentElement.dataset[key];
  const dispose = applyAppearance();
  try {
    expect(currentReadingWidth()).toBe("wide");
    expect(currentTransparency()).toBe("reduced");
    expect(currentMotion()).toBe("reduced");
    expect(document.documentElement.dataset).toMatchObject({ readingWidth: "wide", transparency: "reduced", resolvedTransparency: "reduced", motion: "reduced", resolvedMotion: "reduced" });
  } finally { dispose(); }
});

it("falls back to standard reading and system material preferences for absent or unsupported values", () => {
  expect([currentReadingWidth(), currentTransparency(), currentMotion()]).toEqual(["standard", "system", "system"]);
  for (const key of ["reading-width", "transparency", "motion"]) window.localStorage.setItem(`sparkclaw.appearance.${key}`, "unsupported");
  const dispose = applyAppearance();
  try {
    expect([currentReadingWidth(), currentTransparency(), currentMotion()]).toEqual(["standard", "system", "system"]);
    expect(document.documentElement.dataset).toMatchObject({ readingWidth: "standard", transparency: "system", resolvedTransparency: "full", motion: "system", resolvedMotion: "full" });
  } finally { dispose(); }
});

it("resolves system media changes live, notifies subscribers, and removes every media listener on cleanup", () => {
  const { media, change } = mockMediaPreferences();
  const changed = vi.fn();
  window.addEventListener(APPEARANCE_CHANGE_EVENT, changed);
  const queries = ["(prefers-color-scheme: dark)", "(prefers-reduced-transparency: reduce)", "(prefers-reduced-motion: reduce)"];
  const dispose = applyAppearance();
  try {
    expect(changed).toHaveBeenCalledOnce();
    for (const query of queries) change(query, true);
    expect(document.documentElement.dataset).toMatchObject({ resolvedTheme: "dark", resolvedTransparency: "reduced", resolvedMotion: "reduced" });
    expect(changed).toHaveBeenCalledTimes(4);
    for (const query of queries) change(query, false);
    expect(document.documentElement.dataset).toMatchObject({ resolvedTheme: "light", resolvedTransparency: "full", resolvedMotion: "full" });
    expect(changed).toHaveBeenCalledTimes(7);
    dispose();
    for (const query of queries) {
      expect(media(query).listeners.size).toBe(0);
      expect(media(query).removeEventListener).toHaveBeenCalledOnce();
      change(query, true);
    }
    expect(changed).toHaveBeenCalledTimes(7);
    expect(document.documentElement.dataset.resolvedMotion).toBe("full");
  } finally {
    dispose();
    window.removeEventListener(APPEARANCE_CHANGE_EVENT, changed);
  }
});

it("keeps explicit reductions when the system prefers full effects and notifies on each setting change", () => {
  mockMediaPreferences();
  const changed = vi.fn();
  window.addEventListener(APPEARANCE_CHANGE_EVENT, changed);
  try {
    setReadingWidth("wide");
    setTransparency("reduced");
    setMotion("reduced");
    expect(changed).toHaveBeenCalledTimes(3);
    expect(document.documentElement.dataset).toMatchObject({ readingWidth: "wide", resolvedTransparency: "reduced", resolvedMotion: "reduced" });
    setTransparency("system");
    setMotion("system");
    expect(document.documentElement.dataset).toMatchObject({ resolvedTransparency: "full", resolvedMotion: "full" });
    expect(changed).toHaveBeenCalledTimes(5);
  } finally { window.removeEventListener(APPEARANCE_CHANGE_EVENT, changed); }
});
