import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { dictionaries } from "../i18n";
import { MessageAttachments } from "./messages";

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
