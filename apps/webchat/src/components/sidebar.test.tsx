// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { dictionaries } from "../i18n";
import { SessionSidebar } from "./sidebar";

describe("SessionSidebar navigation and conversations", () => {
  it("omits managed MCP conversations from recent sessions", () => {
    const markup = renderToStaticMarkup(
      <SessionSidebar
        text={dictionaries.en}
        language="en"
        page="schedules"
        ownerProfile={null}
        sessions={[{
          id: "s_mcp_binding_a",
          title: "AI · device-a",
          source: "mcp",
          created_at: "2026-08-18T00:00:00Z",
          updated_at: "2026-08-18T00:00:00Z"
        }]}
        activeSession="s_mcp_binding_a"
        editingSession=""
        sessionTitleDraft=""
        sessionActionId=""
        onSearch={() => {}}
        onCreateSession={() => {}}
        onSelectSession={() => {}}
        onStartRename={() => {}}
        onCancelRename={() => {}}
        onRenameSubmit={() => {}}
        onTitleDraftChange={() => {}}
        onDeleteSession={() => {}}
      />
    );

    expect(markup).not.toContain("AI · device-a");
    expect(markup).toContain('aria-current="page"');
    expect(markup).not.toContain(dictionaries.en.nav.renameSession);
    expect(markup).not.toContain(dictionaries.en.nav.deleteSession);
  });

  it("keeps the navigation focused on tasks with icon-only conversation creation", () => {
    const markup = renderToStaticMarkup(
      <SessionSidebar
        text={dictionaries.en}
        language="en"
        page="chat"
        ownerProfile={{
          id: "owner-one",
          display_name: "Ada Lovelace",
          email: "ada@example.com",
          created_at: "2026-09-20T00:00:00Z",
          updated_at: "2026-09-20T00:00:00Z"
        }}
        sessions={[{
          id: "session-one",
          title: "Review the launch plan",
          created_at: "2026-09-20T00:00:00Z",
          updated_at: "2026-09-20T00:00:00Z"
        }]}
        activeSession="session-one"
        editingSession=""
        sessionTitleDraft=""
        sessionActionId=""
        onSearch={() => {}}
        onCreateSession={() => {}}
        onSelectSession={() => {}}
        onStartRename={() => {}}
        onCancelRename={() => {}}
        onRenameSubmit={() => {}}
        onTitleDraftChange={() => {}}
        onDeleteSession={() => {}}
      />
    );

    expect(markup).toContain("Review the launch plan");
    expect(markup).toContain(">Schedule</span>");
    expect(markup.indexOf("sidebarScheduleLink")).toBeLessThan(markup.indexOf("conversationListHeader"));
    expect(markup).toContain("Recent tasks");
    expect(markup).toContain("newConversationButton");
    expect(markup).toContain(`aria-label="${dictionaries.en.nav.newSession}"`);
    expect(markup).not.toContain(`>${dictionaries.en.nav.newSession}<`);
    expect(markup).toContain('aria-label="Search tasks"');
    expect(markup).toContain(`aria-label="Toggle sidebar"`);
    expect(markup).toContain("lucide-panel-left");
    expect(markup).toContain("lucide-search");
    expect(markup).toContain("lucide-square-pen");
    expect(markup).toContain("Ada Lovelace");
    expect(markup).toContain("ada@example.com");
    expect(markup).not.toContain(">Workspace settings<");
    for (const removed of ["New task", "Search tasks", "Connections", "Memory", "Approvals"]) {
      expect(markup).not.toContain(`>${removed}<`);
    }
  });

  it("opens account settings from the account menu and triggers title search", async () => {
    const onNavigate = vi.fn();
    const onSearch = vi.fn();
    const host = document.createElement("div");
    const root = createRoot(host);

    try {
      await act(async () => root.render(
        <SessionSidebar
          text={dictionaries.en}
          language="en"
          page="chat"
          ownerProfile={{
            id: "owner-one",
            display_name: "Ada Lovelace",
            email: "ada@example.com",
            created_at: "2026-09-20T00:00:00Z",
            updated_at: "2026-09-20T00:00:00Z"
          }}
          sessions={[]}
          activeSession=""
          editingSession=""
          sessionTitleDraft=""
          sessionActionId=""
          onSearch={onSearch}
          onCreateSession={() => {}}
          onSelectSession={() => {}}
          onStartRename={() => {}}
          onCancelRename={() => {}}
          onRenameSubmit={() => {}}
          onTitleDraftChange={() => {}}
          onDeleteSession={() => {}}
          onNavigate={onNavigate}
        />
      ));

      await act(async () => (host.querySelector('[aria-label="Search tasks"]') as HTMLButtonElement).click());
      expect(onSearch).toHaveBeenCalledOnce();
      expect(host.querySelector('[role="menu"]')).toBeNull();

      await act(async () => (host.querySelector(".sidebarScheduleLink") as HTMLButtonElement).click());
      expect(onNavigate).toHaveBeenCalledWith("schedules");

      await act(async () => (host.querySelector(".sidebarAccountTrigger") as HTMLButtonElement).click());
      expect(host.querySelector('[role="menu"]')).not.toBeNull();
      expect(host.textContent).toContain("Workspace settings");

      await act(async () => (host.querySelector('[role="menuitem"]') as HTMLButtonElement).click());
      expect(onNavigate).toHaveBeenCalledWith("settings");
      expect(host.querySelector('[role="menu"]')).toBeNull();
    } finally {
      await act(async () => root.unmount());
    }
  });
});
