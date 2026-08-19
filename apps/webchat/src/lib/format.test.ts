import { describe, expect, it } from "vitest";
import { formatDateTime, formatTime } from "./format";

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
