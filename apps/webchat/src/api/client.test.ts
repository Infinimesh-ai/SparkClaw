import { describe, expect, it } from "vitest";
import { documentFileURL, messageStreamRequestBody } from "./client";

describe("documentFileURL", () => {
  it("keeps the workspace path scoped to its session", () => {
    const url = documentFileURL("media/20260720/weather card.png", "session-weather");
    expect(url).toContain("/api/documents/file?");
    expect(url).toContain("path=media%2F20260720%2Fweather+card.png");
    expect(url).toContain("session_id=session-weather");
  });
});

describe("messageStreamRequestBody", () => {
  it("changes only the final target when an endpoint is selected", () => {
    const attachments = [{ artifact_id: "artifact-file", name: "report.txt", rel_path: "uploads/report.txt" }];
    expect(messageStreamRequestBody("Summarize this report", attachments, "endpoint-selected")).toEqual({
      content: "Summarize this report",
      attachments,
      target_endpoint_id: "endpoint-selected"
    });
    expect(messageStreamRequestBody("Summarize this report", attachments)).toEqual({
      content: "Summarize this report",
      attachments
    });

    expect(messageStreamRequestBody("", attachments, "endpoint-selected")).toEqual({
      content: "",
      attachments,
      target_endpoint_id: "endpoint-selected"
    });
  });
});
