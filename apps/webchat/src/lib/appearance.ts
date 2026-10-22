export type Theme = "system" | "light" | "dark";
export type TextSize = "small" | "default" | "large";
export type ReadingWidth = "standard" | "wide";
export type Transparency = "system" | "reduced";
export type Motion = "system" | "reduced";

export const APPEARANCE_CHANGE_EVENT = "sparkclaw:appearance-change";

const THEME_KEY = "sparkclaw.appearance.theme";
const TEXT_SIZE_KEY = "sparkclaw.appearance.text-size";
const READING_WIDTH_KEY = "sparkclaw.appearance.reading-width";
const TRANSPARENCY_KEY = "sparkclaw.appearance.transparency";
const MOTION_KEY = "sparkclaw.appearance.motion";
const THEME_QUERY = "(prefers-color-scheme: dark)";
const TRANSPARENCY_QUERY = "(prefers-reduced-transparency: reduce)";
const MOTION_QUERY = "(prefers-reduced-motion: reduce)";

function storedPreference(key: string) {
  return typeof window === "undefined" ? null : window.localStorage?.getItem(key);
}

function mediaMatches(query: string) {
  return window.matchMedia?.(query).matches ?? false;
}

export function currentTheme(): Theme {
  const value = storedPreference(THEME_KEY);
  return value === "light" || value === "dark" ? value : "system";
}

export function currentTextSize(): TextSize {
  const value = storedPreference(TEXT_SIZE_KEY);
  return value === "small" || value === "large" ? value : "default";
}

export function currentReadingWidth(): ReadingWidth {
  return storedPreference(READING_WIDTH_KEY) === "wide" ? "wide" : "standard";
}

export function currentTransparency(): Transparency {
  return storedPreference(TRANSPARENCY_KEY) === "reduced" ? "reduced" : "system";
}

export function currentMotion(): Motion {
  return storedPreference(MOTION_KEY) === "reduced" ? "reduced" : "system";
}

export function setTheme(value: Theme) {
  setPreference(THEME_KEY, value);
}

export function setTextSize(value: TextSize) {
  setPreference(TEXT_SIZE_KEY, value);
}

export function setReadingWidth(value: ReadingWidth) {
  setPreference(READING_WIDTH_KEY, value);
}

export function setTransparency(value: Transparency) {
  setPreference(TRANSPARENCY_KEY, value);
}

export function setMotion(value: Motion) {
  setPreference(MOTION_KEY, value);
}

function setPreference(key: string, value: string) {
  window.localStorage?.setItem(key, value);
  resolveAppearance();
}

export function applyAppearance() {
  resolveAppearance();
  const media = [THEME_QUERY, TRANSPARENCY_QUERY, MOTION_QUERY].map((query) => window.matchMedia?.(query));
  for (const preference of media) preference?.addEventListener("change", resolveAppearance);
  return () => {
    for (const preference of media) preference?.removeEventListener("change", resolveAppearance);
  };
}

function resolveAppearance() {
  const theme = currentTheme();
  const transparency = currentTransparency();
  const motion = currentMotion();
  const dataset = document.documentElement.dataset;
  dataset.theme = theme;
  dataset.textSize = currentTextSize();
  dataset.readingWidth = currentReadingWidth();
  dataset.transparency = transparency;
  dataset.motion = motion;
  dataset.resolvedTheme = theme === "system"
    ? mediaMatches(THEME_QUERY) ? "dark" : "light"
    : theme;
  dataset.resolvedTransparency = transparency === "reduced" || mediaMatches(TRANSPARENCY_QUERY) ? "reduced" : "full";
  dataset.resolvedMotion = motion === "reduced" || mediaMatches(MOTION_QUERY) ? "reduced" : "full";
  window.dispatchEvent(new Event(APPEARANCE_CHANGE_EVENT));
}
