import { describe, expect, it } from "vitest";
import { en } from "../i18n/en";
import { formatDateTime, formatTime, profileLabel } from "./format";

describe("model profile formatting", () => {
  it("distinguishes physical context, admitted input, and output allowances", () => {
    expect(profileLabel({
      name: "fast",
      model: "sparkclaw-fast",
      base_url: "",
      context_tokens: 262144,
      max_input_tokens: 65536,
      max_tokens: 1024,
      mtp: false,
    }, en)).toBe("fast · sparkclaw-fast · 262,144 ctx · 65,536 input · 1,024 output");
  });
});

describe("time formatting", () => {
  it("uses the supplied client timezone across shared displays", () => {
    const instant = "2026-01-01T00:30:00Z";
    const newYork = formatDateTime(instant, "en", "America/New_York");
    const tokyo = formatDateTime(instant, "en", "Asia/Tokyo");

    expect(newYork).toContain("Dec");
    expect(newYork).toContain("31");
    expect(tokyo).toContain("Jan");
    expect(tokyo).toContain("1");
    expect(formatTime(instant, "en", "America/New_York")).toContain("07:30");
  });
});
