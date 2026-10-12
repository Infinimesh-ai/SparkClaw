import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { dictionaries } from "../i18n";
import { MessageAttachments, MessageBubble, TaskProgressMessage } from "./messages";

describe("task progress in the answer", () => {
  it.each(["en", "zh"] as const)("shows an accessible immediate answer placeholder in %s", language => {
    const markup = renderToStaticMarkup(<TaskProgressMessage id="request" sessionId="session" createdAt="" text={dictionaries[language]} language={language} />);
    expect(markup).toContain('role="status"');
    expect(markup).toContain(dictionaries[language].chat.waiting);
    expect(markup).toContain("lucide-loader-circle");
    expect(markup).toContain('class="message assistant"');
    expect(markup).not.toContain("feedbackBar");
  });
  it("replaces the placeholder with streaming content and retains tool activity", () => {
    const render = (content: string) => renderToStaticMarkup(<MessageBubble
      message={{ id: "answer", session_id: "session", role: "assistant", content, created_at: "" }} inProgress
      streamStatuses={[{ id: "waiting", type: "waiting", text: dictionaries.en.chat.waiting }, { id: "tool", type: "tool_started", text: "Reading file" }]}
      text={dictionaries.en} language="en" onFeedback={async () => {}} />);
    expect(render("").split(dictionaries.en.chat.waiting)).toHaveLength(2);
    const markup = render("First answer text");
    expect(markup).not.toContain("taskProgress");
    expect(markup).not.toContain(dictionaries.en.chat.waiting);
    expect(markup).toContain("First answer text");
    expect(markup).toContain("Reading file");
  });
});

describe("MessageAttachments", () => {
  it("renders an assistant weather card as inline media", () => {
    const markup = renderToStaticMarkup(
      <MessageAttachments
        attachments={[
          {
            artifact_id: "weather-card",
            name: "weather_card.png",
            rel_path: "media/20260729/weather_card.png",
            content_type: "image/png",
            source: "workflow_result",
            caption: "杭州天气卡片"
          }
        ]}
        sessionId="session-weather"
        text={dictionaries.en}
        inlineOutputs
      />
    );

    expect(markup).toContain('class="messageContent mediaOnly"');
    expect(markup).toContain('class="messageMediaImageButton"');
    expect(markup).toContain("杭州天气卡片");
    expect(markup).not.toContain('class="messageAttachments"');
    expect(markup).not.toContain('class="messageDocumentResult"');
  });
});

describe("MessageBubble external AI requirements", () => {
  it("shows MCP media locators as unverified requirements instead of attachments", () => {
    const markup = renderToStaticMarkup(
      <MessageBubble
        message={{
          id: "message-mcp",
          session_id: "session-mcp",
          role: "user",
          content: "",
          created_at: "2026-08-18T00:00:00Z",
          requested_media: [{ query: "quarterly report", caption: "Latest report" }]
        }}
        streamStatuses={[]}
        text={dictionaries.en}
        language="en"
        sessionSource="mcp"
        onFeedback={async () => {}}
      />
    );

    expect(markup).toContain("Requirement");
    expect(markup).toContain("Requested media (not yet verified)");
    expect(markup).toContain("Latest report");
    expect(markup).toContain("quarterly report");
    expect(markup).not.toContain("Open attachment");
    expect(markup).not.toContain(">You<");
  });
});
