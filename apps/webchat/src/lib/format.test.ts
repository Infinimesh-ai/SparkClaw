import { describe, expect, it } from "vitest";
import { en } from "../i18n/en";
import { formatDateTime, formatTime, profileLabel } from "./format";

describe("model profile formatting", () => {
  it("renders the physical model window and output-class budgets", () => {
    expect(profileLabel({
      name: "fast",
      model: "sparkclaw-fast",
      base_url: "",
      capacity_physical_model: "hosted-fast",
      context_tokens: 262144,
      output_budgets: { answer: 8192, compact_structured: 2048 },
      mtp: false,
    }, en)).toBe("fast · sparkclaw-fast · hosted-fast · 262,144 ctx · output: answer=8,192, compact_structured=2,048");
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
