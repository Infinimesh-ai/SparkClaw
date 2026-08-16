// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { PassiveNotification } from "../api/types";
import { usePassiveNotifications } from "./usePassiveNotifications";

function notification(id: string, readAt?: string): PassiveNotification {
  return {
    id,
    notification_id: `delivery-${id}`,
    source: "localmind",
    kind: "comment_mention",
    deep_link: "https://localmind.example/doc",
    occurred_at: "2026-08-06T09:00:00Z",
    read_at: readAt,
    created_at: "2026-08-06T09:00:01Z",
    updated_at: "2026-08-06T09:00:01Z"
  };
}

let latest: ReturnType<typeof usePassiveNotifications>;
function Probe() {
  latest = usePassiveNotifications();
  return null;
}

describe("usePassiveNotifications", () => {
  afterEach(() => vi.restoreAllMocks());

  it("derives the unread count from the list and surfaces markRead failures", async () => {
    vi.spyOn(api, "notifications").mockResolvedValue({
      notifications: [notification("n1"), notification("n2", "2026-08-06T10:00:00Z")],
      // A drifting server-side counter must not leak into the UI.
      unread_count: 99
    });
    vi.spyOn(api, "markNotificationRead").mockRejectedValue(new Error("mark failed"));

    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => root.render(<Probe />));
    await act(async () => { await latest.refresh(); });

    expect(latest.notifications).toHaveLength(2);
    expect(latest.unreadCount).toBe(1);

    await act(async () => { await latest.markRead("n1"); });
    expect(latest.error).toBe("mark failed");
    // The list is untouched on failure, so nothing is optimistically read.
    expect(latest.unreadCount).toBe(1);

    await act(async () => root.unmount());
  });
});
