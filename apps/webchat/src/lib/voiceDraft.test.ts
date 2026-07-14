import { describe, expect, it } from "vitest";
import { insertVoiceTranscript } from "./voiceDraft";

describe("insertVoiceTranscript", () => {
  it("replaces the saved selection and preserves readable ASCII spacing", () => {
    const result = insertVoiceTranscript("Ask now please", "SparkClaw", 4, 7);
    expect(result).toEqual({ value: "Ask SparkClaw please", caret: 13 });
  });

  it("does not add spaces around Chinese text", () => {
    const result = insertVoiceTranscript("请总结", "这些文档", 1, 1);
    expect(result).toEqual({ value: "请这些文档总结", caret: 5 });
  });

  it("leaves the draft unchanged for an empty transcript", () => {
    expect(insertVoiceTranscript("draft", "  ", 2, 4)).toEqual({ value: "draft", caret: 2 });
  });
});
