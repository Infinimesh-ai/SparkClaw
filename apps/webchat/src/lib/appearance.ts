export type Theme = "system" | "light" | "dark";
export type TextSize = "default" | "large";

const THEME_KEY = "sparkclaw.appearance.theme";
const TEXT_SIZE_KEY = "sparkclaw.appearance.text-size";

export function currentTheme(): Theme {
  const value = typeof window === "undefined" ? null : window.localStorage?.getItem(THEME_KEY);
  return value === "light" || value === "dark" ? value : "system";
}

export function currentTextSize(): TextSize {
  return typeof window !== "undefined" && window.localStorage?.getItem(TEXT_SIZE_KEY) === "large" ? "large" : "default";
}

export function setTheme(value: Theme) {
  window.localStorage?.setItem(THEME_KEY, value);
  document.documentElement.dataset.theme = value;
  resolveTheme();
}

export function setTextSize(value: TextSize) {
  window.localStorage?.setItem(TEXT_SIZE_KEY, value);
  document.documentElement.dataset.textSize = value;
}

export function applyAppearance() {
  document.documentElement.dataset.theme = currentTheme();
  document.documentElement.dataset.textSize = currentTextSize();
  resolveTheme();
  const media = window.matchMedia?.("(prefers-color-scheme: dark)");
  media?.addEventListener("change", resolveTheme);
  return () => media?.removeEventListener("change", resolveTheme);
}

function resolveTheme() {
  const theme = currentTheme();
  document.documentElement.dataset.resolvedTheme = theme === "system"
    ? window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "dark" : "light"
    : theme;
}
