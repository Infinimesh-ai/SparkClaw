// @vitest-environment jsdom

import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
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
  it("dismisses the inbox with Escape, restores the bell, and leaves unread state alone", async () => {
    const host = document.createElement("div"); document.body.append(host);
    const root = createRoot(host), read = vi.fn(async () => {}), readAll = vi.fn(async () => {});
    function Harness() {
      const [open, setOpen] = useState(false);
      return <NotificationCenter notifications={[notification]} unreadCount={1} open={open} toast={null} language="en" text={dictionaries.en}
        onToggle={() => setOpen(value => !value)} onDismissToast={() => {}} onRead={read} onReadAll={readAll} />;
    }
    try {
      await act(async () => root.render(<Harness />));
      const bell = host.querySelector<HTMLButtonElement>(".notificationBell")!; bell.focus();
      await act(async () => bell.click());
      const link = host.querySelector<HTMLAnchorElement>(".notificationOpen")!; link.focus();
      const event = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
      await act(async () => link.dispatchEvent(event));
      expect(event.defaultPrevented).toBe(true);
      expect(host.querySelector(".notificationInbox")).toBeNull();
      expect(document.activeElement).toBe(bell);
      expect(read).not.toHaveBeenCalled(); expect(readAll).not.toHaveBeenCalled();
    } finally { await act(async () => root.unmount()); host.remove(); }
  });

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

  it("renders the notification's own source and an explicit label for unknown kinds", () => {
    const foreign: PassiveNotification = {
      ...notification,
      id: "notification-2",
      source: "teamdocs",
      kind: "task_assigned"
    };
    const markup = renderToStaticMarkup(
      <NotificationCenter
        notifications={[foreign]}
        unreadCount={1}
        open
        toast={foreign}
        language="en"
        text={dictionaries.en}
        onToggle={() => {}}
        onDismissToast={() => {}}
        onRead={async () => {}}
        onReadAll={async () => {}}
      />
    );

    expect(markup).toContain("<strong>teamdocs</strong>");
    expect(markup).not.toContain("<strong>LocalMind</strong>");
    expect(markup).toContain(dictionaries.en.notifications.activity);
    expect(markup).not.toContain(dictionaries.en.notifications.documentMention);
  });

  it("shows a sync error inside the inbox", () => {
    const markup = renderToStaticMarkup(
      <NotificationCenter
        notifications={[notification]}
        unreadCount={0}
        open
        toast={null}
        error="mark all read failed"
        language="en"
        text={dictionaries.en}
        onToggle={() => {}}
        onDismissToast={() => {}}
        onRead={async () => {}}
        onReadAll={async () => {}}
      />
    );

    expect(markup).toContain('class="notificationError"');
    expect(markup).toContain("mark all read failed");
  });
});
