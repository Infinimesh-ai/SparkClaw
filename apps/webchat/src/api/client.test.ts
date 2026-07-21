import { describe, expect, it } from "vitest";
import { documentFileURL } from "./client";

describe("documentFileURL", () => {
  it("keeps the workspace path scoped to its session", () => {
    const url = documentFileURL("media/20260720/weather card.png", "session-weather");
    expect(url).toContain("/api/documents/file?");
    expect(url).toContain("path=media%2F20260720%2Fweather+card.png");
    expect(url).toContain("session_id=session-weather");
  });
});
