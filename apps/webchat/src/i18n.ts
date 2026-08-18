import { en } from "./i18n/en";
import { zh } from "./i18n/zh";

export type Language = "en" | "zh";
export type Copy = typeof en;

export const LANGUAGE_STORAGE_KEY = "sparkclaw.language";

export const dictionaries = { en, zh } satisfies Record<Language, Copy>;

export function initialLanguage(): Language {
  const stored = window.localStorage.getItem(LANGUAGE_STORAGE_KEY);
  if (stored === "en" || stored === "zh") return stored;
  return window.navigator.language.toLowerCase().startsWith("zh") ? "zh" : "en";
}
