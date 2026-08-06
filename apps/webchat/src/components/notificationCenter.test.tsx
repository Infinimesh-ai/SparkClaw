import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { PassiveNotification } from "../api/types";
import { dictionaries } from "../i18n";
import { NotificationCenter } from "./notificationCenter";

const notification: PassiveNotification = {
  id: "notification-1",
  notification_id: "delivery-1",
  source: "localmind",
  kind: "comment_mention",
  deep_link: "https://localmind.example/workspace/document",
  occurred_at: "2026-08-06T09:00:00Z",
  created_at: "2026-08-06T09:00:01Z",
  updated_at: "2026-08-06T09:00:01Z"
};

describe("NotificationCenter", () => {
  it("renders an unread badge and an explicit safe LocalMind link without session UI", () => {
    const markup = renderToStaticMarkup(
      <NotificationCenter
        notifications={[notification]}
        unreadCount={1}
        open
        toast={notification}
        language="en"
        text={dictionaries.en}
        onToggle={() => {}}
        onDismissToast={() => {}}
        onRead={async () => {}}
        onReadAll={async () => {}}
      />
    );

    expect(markup).toContain('class="notificationBadge"');
    expect(markup).toContain(dictionaries.en.notifications.commentMention);
    expect(markup).toContain('href="https://localmind.example/workspace/document"');
    expect(markup).toContain('target="_blank"');
    expect(markup).toContain('rel="noopener noreferrer"');
    expect(markup).not.toContain("session");
  });
});
