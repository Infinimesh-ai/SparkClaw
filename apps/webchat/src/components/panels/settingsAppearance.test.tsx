// @vitest-environment jsdom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../../api/client";
import { dictionaries, type Language } from "../../i18n";
import { applyAppearance } from "../../lib/appearance";
import { SettingsPanel } from "./settings";

const preferenceKeys = ["theme", "text-size", "reading-width", "transparency", "motion"].map((key) => `sparkclaw.appearance.${key}`);
const datasetKeys = ["theme", "resolvedTheme", "textSize", "readingWidth", "transparency", "resolvedTransparency", "motion", "resolvedMotion"];
let savedStorage: (string | null)[];
let savedDataset: (string | undefined)[];
const mountedRoots = new Set<Root>();

beforeEach(() => {
  // Node 26 shadows jsdom storage; keep preferences in an isolated memory store.
  const values = new Map<string, string>();
  const storage = { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => void values.set(key, String(value)),
    removeItem: (key: string) => void values.delete(key), clear: () => values.clear() };
  Object.defineProperty(window, "localStorage", { configurable: true, value: storage });
  Object.defineProperty(globalThis, "localStorage", { configurable: true, value: storage });
  savedStorage = preferenceKeys.map((key) => localStorage.getItem(key));
  savedDataset = datasetKeys.map((key) => document.documentElement.dataset[key]);
  for (const key of preferenceKeys) localStorage.removeItem(key);
  for (const key of datasetKeys) delete document.documentElement.dataset[key];
  vi.stubGlobal("matchMedia", vi.fn(() => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() })));
});

afterEach(async () => {
  for (const root of mountedRoots) await act(async () => root.unmount());
  mountedRoots.clear();
  for (const [index, key] of preferenceKeys.entries()) {
    const value = savedStorage[index];
    if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, value);
  }
  for (const [index, key] of datasetKeys.entries()) {
    const value = savedDataset[index];
    if (value === undefined) delete document.documentElement.dataset[key];
    else document.documentElement.dataset[key] = value;
  }
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("Appearance settings", () => {
  it("applies local controls immediately to the interface and preview without loading credentials", async () => {
    const integrations = vi.spyOn(api, "integrations");
    const dispose = applyAppearance();
    try {
      const { container } = await renderAppearance();
      expect(container.querySelector(".appearancePreview")).not.toBeNull();
      expect(integrations).not.toHaveBeenCalled();

      await selectOption(container, "Reading width", "wide");
      expect(localStorage.getItem("sparkclaw.appearance.reading-width")).toBe("wide");
      expect(document.documentElement.dataset.readingWidth).toBe("wide");
      expect(previewCanvas(container).dataset.readingWidth).toBe("wide");

      await selectOption(container, "Transparency", "reduced");
      expect(localStorage.getItem("sparkclaw.appearance.transparency")).toBe("reduced");
      expect(document.documentElement.dataset).toMatchObject({ transparency: "reduced", resolvedTransparency: "reduced" });

      await selectOption(container, "Motion", "reduced");
      expect(localStorage.getItem("sparkclaw.appearance.motion")).toBe("reduced");
      expect(document.documentElement.dataset).toMatchObject({ motion: "reduced", resolvedMotion: "reduced" });

      await selectOption(container, "Text size", "small");
      await clickTheme(container, "Dark");
      expect(document.documentElement.dataset).toMatchObject({ theme: "dark", resolvedTheme: "dark", textSize: "small" });
      expect(container.querySelector('button[aria-pressed="true"]')?.textContent).toBe("Dark");
      expect(previewCanvas(container).dataset.readingWidth).toBe("wide");
    } finally { dispose(); }
  });

  it("restores all preferences and the preview after leaving and reopening Appearance", async () => {
    const dispose = applyAppearance();
    try {
      const first = await renderAppearance();
      await selectOption(first.container, "Reading width", "wide");
      await selectOption(first.container, "Transparency", "reduced");
      await selectOption(first.container, "Motion", "reduced");
      await selectOption(first.container, "Text size", "small");
      await clickTheme(first.container, "Dark");
      await act(async () => first.root.unmount());
      mountedRoots.delete(first.root);

      const reopened = await renderAppearance();
      for (const [label, value] of [["Reading width", "wide"], ["Transparency", "reduced"], ["Motion", "reduced"], ["Text size", "small"]]) {
        expect(select(reopened.container, label).value).toBe(value);
      }
      expect(reopened.container.querySelector('button[aria-pressed="true"]')?.textContent).toBe("Dark");
      expect(previewCanvas(reopened.container).dataset.readingWidth).toBe("wide");

      await selectOption(reopened.container, "Reading width", "standard");
      await selectOption(reopened.container, "Transparency", "system");
      await selectOption(reopened.container, "Motion", "system");
      expect(document.documentElement.dataset).toMatchObject({ readingWidth: "standard", transparency: "system", resolvedTransparency: "full", motion: "system", resolvedMotion: "full", textSize: "small", theme: "dark" });
      expect(previewCanvas(reopened.container).dataset.readingWidth).toBe("standard");
    } finally { dispose(); }
  });

  it.each([
    ["en", "Reading width", "Transparency", "Motion", "Text size"],
    ["zh", "阅读宽度", "透明效果", "动态效果", "文字大小"]
  ] as const)("keeps %s appearance controls labeled with complete choices", async (language, widthLabel, transparencyLabel, motionLabel, textLabel) => {
    const { container } = await renderAppearance(language);
    expect(Array.from(select(container, widthLabel).options, (option) => option.value)).toEqual(["standard", "wide"]);
    expect(Array.from(select(container, transparencyLabel).options, (option) => option.value)).toEqual(["system", "reduced"]);
    expect(Array.from(select(container, motionLabel).options, (option) => option.value)).toEqual(["system", "reduced"]);
    expect(Array.from(select(container, textLabel).options, (option) => option.value)).toEqual(["small", "default", "large"]);
    expect(preview(container).textContent?.trim()).not.toBe("");
  });
});

async function renderAppearance(language: Language = "en") {
  const container = document.createElement("div");
  const root = createRoot(container);
  mountedRoots.add(root);
  await act(async () => root.render(<SettingsPanel
    section="appearance" runtimeConfig={null} ownerProfile={null} clients={[]} connectors={[]} notificationBindings={[]}
    text={dictionaries[language]} language={language} onUpdateOwner={async () => {}} onRevokeClient={async () => {}}
    onStartNotificationBinding={async () => ({}) as never} onRefreshNotificationBinding={async () => ({}) as never}
    onOpenNotificationBindingBrowser={async () => {}} onRevokeNotificationBinding={async () => {}}
    onUpdateConnector={async () => ({}) as never} onUpdatePolicy={async () => {}}
  />));
  return { container, root };
}

function select(container: HTMLElement, label: string) {
  const control = Array.from(container.querySelectorAll("select")).find((element) => element.getAttribute("aria-label") === label);
  expect(control, `${label} select`).toBeDefined();
  return control!;
}

async function selectOption(container: HTMLElement, label: string, value: string) {
  const control = select(container, label);
  await act(async () => {
    control.value = value;
    control.dispatchEvent(new Event("change", { bubbles: true }));
  });
}

async function clickTheme(container: HTMLElement, label: string) {
  const button = Array.from(container.querySelectorAll('button[aria-pressed]')).find((element) => element.textContent === label) as HTMLButtonElement | undefined;
  expect(button, `${label} theme button`).toBeDefined();
  await act(async () => button!.click());
}

function preview(container: HTMLElement) {
  const element = container.querySelector<HTMLElement>(".appearancePreview");
  expect(element).not.toBeNull();
  return element!;
}

function previewCanvas(container: HTMLElement) {
  const element = preview(container).querySelector<HTMLElement>(".appearancePreviewCanvas");
  expect(element).not.toBeNull();
  return element!;
}
