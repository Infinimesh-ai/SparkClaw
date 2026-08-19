// @vitest-environment jsdom

import { afterEach, describe, expect, it } from "vitest";
import { isUnavailableMicrophoneError, loadPreferredMicrophone, savePreferredMicrophone } from "./microphones";

describe("microphone preferences", () => {
  const values = new Map<string, string>();
  Object.defineProperty(window, "localStorage", {
    configurable: true,
    value: {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
      removeItem: (key: string) => values.delete(key),
      clear: () => values.clear()
    }
  });

  afterEach(() => values.clear());

  it("stores only an explicit browser-local device preference", () => {
    savePreferredMicrophone("device-a");
    expect(loadPreferredMicrophone()).toBe("device-a");
    savePreferredMicrophone("");
    expect(loadPreferredMicrophone()).toBe("");
  });

  it("falls back only for a missing constrained device", () => {
    expect(isUnavailableMicrophoneError(new DOMException("missing", "NotFoundError"))).toBe(true);
    expect(isUnavailableMicrophoneError(new DOMException("denied", "NotAllowedError"))).toBe(false);
  });
});
