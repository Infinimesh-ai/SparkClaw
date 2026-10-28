// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { dictionaries } from "../i18n";
import { SessionSidebar } from "./sidebar";
import { ConfirmationProvider, useConfirm } from "./confirmation";

describe("SessionSidebar navigation and conversations", () => {
  it("keeps Schedule in the footer with its focus and task list when entering and leaving schedules", async () => {
    const host = document.createElement("div"); document.body.append(host);
    const root = createRoot(host), navigate = vi.fn();
    const render = (home: boolean, page: "chat" | "schedules") => root.render(<SessionSidebar
      text={dictionaries.en} language="en" home={home} page={page} ownerProfile={null}
      sessions={[{ id: "one", title: "Existing task" }]} activeSession="one"
      onCreateSession={() => {}} onSearch={() => {}} onSelectSession={() => {}} onNavigate={navigate} />);
    try {
      await act(async () => render(true, "chat"));
      const schedule = host.querySelector<HTMLButtonElement>(".sidebarScheduleLink")!;
      expect(schedule.parentElement).toHaveProperty("className", "sidebarFooter");
      schedule.focus();
      await act(async () => schedule.click());
      expect(navigate).toHaveBeenCalledExactlyOnceWith("schedules");
      await act(async () => render(false, "schedules"));
      expect(host.querySelector(".sidebarScheduleLink")).toBe(schedule);
      expect(document.activeElement).toBe(schedule);
      expect(schedule.getAttribute("aria-current")).toBe("page");
      expect(host.querySelector(".sessionTitle")?.textContent).toBe("Existing task");
      await act(async () => render(true, "chat"));
      expect(host.querySelector(".sidebarFooter .sidebarScheduleLink")).toBe(schedule);
      expect(schedule.hasAttribute("aria-current")).toBe(false);
    } finally { await act(async () => root.unmount()); host.remove(); }
  });

  it("moves through the account menu with arrows and restores its trigger after Escape", async () => {
    const host = document.createElement("div"); document.body.append(host);
    const root = createRoot(host), navigate = vi.fn(), logout = vi.fn();
    try {
      await act(async () => root.render(<SessionSidebar text={dictionaries.en} language="en" page="chat" ownerProfile={null}
        sessions={[]} activeSession="" onCreateSession={() => {}} onSearch={() => {}} onSelectSession={() => {}}
        onNavigate={navigate} onLogout={logout} />));
      const trigger = host.querySelector<HTMLButtonElement>(".sidebarAccountTrigger")!; trigger.focus();
      await act(async () => trigger.click());
      const items = host.querySelectorAll<HTMLButtonElement>('[role="menuitem"]');
      expect(document.activeElement).toBe(items[0]);
      await act(async () => items[0].dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true, cancelable: true })));
      expect(document.activeElement).toBe(items[1]);
      const event = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
      await act(async () => items[1].dispatchEvent(event));
      expect(event.defaultPrevented).toBe(true);
      expect(host.querySelector('[role="menu"]')).toBeNull();
      expect(document.activeElement).toBe(trigger);
      expect(navigate).not.toHaveBeenCalled(); expect(logout).not.toHaveBeenCalled();
    } finally { await act(async () => root.unmount()); host.remove(); }
  });

  it("shows each conversation's activity on the right while preserving selection and rename actions", async () => {
    const select = vi.fn(), rename = vi.fn();
    const host = document.createElement("div"), root = createRoot(host);
    const render = (running: "running" | "completed") => root.render(<SessionSidebar text={dictionaries.zh} language="zh"
      page="chat" ownerProfile={null} sessions={[{ id: "one", title: "正在处理的长标题" }, { id: "two", title: "空对话" }]} activeSession="two"
      activityBySession={{ one: running }} onCreateSession={() => {}} onSearch={() => {}} onSelectSession={select} onStartRename={rename} />);
    try {
      await act(async () => render("running"));
      const button = host.querySelector<HTMLButtonElement>(".sessionSelect")!;
      expect(button.lastElementChild?.className).toBe("sessionActivity running");
      expect(button.querySelector('[aria-label="任务正在进行中…"] svg')).not.toBeNull();
      expect(host.querySelectorAll(".sessionActivity")).toHaveLength(1);
      await act(async () => button.click()); expect(select).toHaveBeenCalledWith(expect.objectContaining({ id: "one" }));
      await act(async () => host.querySelector<HTMLButtonElement>(".sessionMenuTrigger")!.click());
      await act(async () => document.querySelector<HTMLButtonElement>(".sessionMenu [role=menuitem]")!.click());
      expect(rename).toHaveBeenCalledWith(expect.objectContaining({ id: "one" }));
      await act(async () => render("completed"));
      expect(button.querySelector(".sessionActivity svg")).toBeNull();
      expect(button.querySelector('[aria-label="任务已完成"] i')).not.toBeNull();
    } finally { await act(async () => root.unmount()); }
  });
  it("deletes through the requested conversation's menu without selecting it and disables actions during deletion", async () => {
    const onDelete = vi.fn(), onSelect = vi.fn();
    const host = document.createElement("div"), root = createRoot(host);
    const render = (busy = false, sessionActionId = "") => root.render(<SessionSidebar
      text={dictionaries.en} language="en" page="chat" ownerProfile={null}
      sessions={[{ id: "one", title: "First" }, { id: "two", title: "Second" }]} activeSession="one"
      onCreateSession={() => {}} onSearch={() => {}} onSelectSession={onSelect} onDeleteSession={onDelete}
      busy={busy} sessionActionId={sessionActionId} />);
    try {
      await act(async () => render());
      expect(document.querySelector('[aria-label="Delete session: Second"]')).toBeNull();
      await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Conversation actions: Second"]')!.click());
      await act(async () => document.querySelector<HTMLButtonElement>('[aria-label="Delete session: Second"]')!.click());
      expect(onDelete).toHaveBeenCalledExactlyOnceWith("two"); expect(onSelect).not.toHaveBeenCalled();
      expect(document.querySelector(".sessionMenu")).toBeNull();
      for (const [busy, actionID] of [[true, ""], [false, "one"]] as const) {
        await act(async () => render(busy, actionID));
        for (const button of host.querySelectorAll<HTMLButtonElement>(".sessionMenuTrigger")) expect(button.disabled).toBe(true);
      }
    } finally { await act(async () => root.unmount()); }
  });

  it("exposes one ellipsis per conversation and keeps rename save and cancel inside the inline editor", async () => {
    const host = document.createElement("div"); document.body.append(host);
    const root = createRoot(host), rename = vi.fn(), submit = vi.fn(), cancel = vi.fn(), select = vi.fn();
    const render = (editingSession = "", sessionActionId = "") => root.render(<SessionSidebar
      text={dictionaries.en} language="en" page="chat" ownerProfile={null}
      sessions={[{ id: "one", title: "First" }]} activeSession="one"
      editingSession={editingSession} sessionTitleDraft="New title" sessionActionId={sessionActionId}
      onCreateSession={() => {}} onSearch={() => {}} onSelectSession={select}
      onStartRename={rename} onRenameSubmit={submit} onCancelRename={cancel} onDeleteSession={() => {}} />);
    try {
      await act(async () => render());
      expect(host.querySelectorAll(".sessionActions > button.sessionArchiveTrigger")).toHaveLength(1);
      const actions = host.querySelector(".sessionActionMenu")!;
      expect(actions.querySelectorAll("button")).toHaveLength(1);
      expect(actions.querySelector(".lucide-ellipsis")).not.toBeNull();
      expect(actions.querySelector(".lucide-pencil, .lucide-trash-2")).toBeNull();
      const trigger = actions.querySelector<HTMLButtonElement>("button")!;
      expect(trigger.getAttribute("aria-expanded")).toBe("false");
      await act(async () => trigger.click());
      const menu = document.querySelector<HTMLElement>(".sessionMenu")!;
      expect(menu.parentElement).toBe(document.body);
      expect(trigger.getAttribute("aria-controls")).toBe(menu.id);
      expect(trigger.getAttribute("aria-expanded")).toBe("true");
      const items = menu.querySelectorAll<HTMLButtonElement>('[role="menuitem"]');
      expect(items).toHaveLength(2);
      expect(document.activeElement).toBe(items[0]);
      await act(async () => items[0].click());
      expect(rename).toHaveBeenCalledExactlyOnceWith({ id: "one", title: "First" });
      expect(select).not.toHaveBeenCalled();
      expect(document.querySelector(".sessionMenu")).toBeNull();
      await act(async () => render("one"));
      expect(document.activeElement).toBe(host.querySelector(".sessionRenameForm input"));
      expect(host.querySelector(".sessionMenuTrigger")).toBeNull();
      await act(async () => host.querySelector<HTMLFormElement>(".sessionRenameForm")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
      expect(submit).toHaveBeenCalledExactlyOnceWith("one");
      await act(async () => host.querySelector<HTMLButtonElement>(`[title="${dictionaries.en.common.cancel}"]`)!.click());
      expect(cancel).toHaveBeenCalledOnce();
      await act(async () => render("one", "one"));
      expect(host.querySelector<HTMLInputElement>(".sessionRenameForm input")!.disabled).toBe(true);
      for (const button of host.querySelectorAll<HTMLButtonElement>(".sessionRenameForm button")) expect(button.disabled).toBe(true);
    } finally { await act(async () => root.unmount()); host.remove(); }
  });

  it("supports keyboard navigation and Escape, trigger-toggle and outside dismissal without changing conversations", async () => {
    const host = document.createElement("div"); document.body.append(host);
    const root = createRoot(host), rename = vi.fn(), remove = vi.fn(), select = vi.fn();
    try {
      await act(async () => root.render(<SessionSidebar text={dictionaries.en} language="en" page="chat" ownerProfile={null}
        sessions={[{ id: "one", title: "First" }]} activeSession="one" onCreateSession={() => {}} onSearch={() => {}}
        onSelectSession={select} onStartRename={rename} onDeleteSession={remove} />));
      const trigger = host.querySelector<HTMLButtonElement>(".sessionMenuTrigger")!;
      trigger.focus();
      await act(async () => trigger.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true, cancelable: true })));
      const items = document.querySelectorAll<HTMLButtonElement>('.sessionMenu [role="menuitem"]');
      expect(document.activeElement).toBe(items[0]);
      await act(async () => items[0].dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true, cancelable: true })));
      expect(document.activeElement).toBe(items[1]);
      await act(async () => items[1].dispatchEvent(new KeyboardEvent("keydown", { key: "Home", bubbles: true, cancelable: true })));
      expect(document.activeElement).toBe(items[0]);
      await act(async () => items[0].dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true, cancelable: true })));
      expect(document.activeElement).toBe(items[1]);
      const escape = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
      await act(async () => items[1].dispatchEvent(escape));
      expect(escape.defaultPrevented).toBe(true);
      expect(document.querySelector(".sessionMenu")).toBeNull();
      expect(document.activeElement).toBe(trigger);
      await act(async () => trigger.click());
      await act(async () => trigger.click());
      expect(document.querySelector(".sessionMenu")).toBeNull();
      await act(async () => trigger.click());
      await act(async () => document.body.dispatchEvent(new Event("pointerdown", { bubbles: true })));
      expect(document.querySelector(".sessionMenu")).toBeNull();
      expect(select).not.toHaveBeenCalled(); expect(rename).not.toHaveBeenCalled(); expect(remove).not.toHaveBeenCalled();
    } finally { await act(async () => root.unmount()); host.remove(); }
  });

  it("keeps other conversations' rename action available while deleting is blocked and closes on list scroll", async () => {
    const host = document.createElement("div"); document.body.append(host);
    const root = createRoot(host);
    try {
      await act(async () => root.render(<SessionSidebar text={dictionaries.en} language="en" page="chat" ownerProfile={null}
        sessions={[{ id: "one", title: "First" }, { id: "two", title: "Second" }]} activeSession="one" sessionActionId="one"
        onCreateSession={() => {}} onSearch={() => {}} onSelectSession={() => {}} onStartRename={() => {}} onDeleteSession={() => {}} />));
      const triggers = host.querySelectorAll<HTMLButtonElement>(".sessionMenuTrigger");
      expect(triggers[0].disabled).toBe(true); expect(triggers[1].disabled).toBe(false);
      await act(async () => triggers[1].click());
      const items = document.querySelectorAll<HTMLButtonElement>('.sessionMenu [role="menuitem"]');
      expect(items[0].disabled).toBe(false); expect(items[1].disabled).toBe(true);
      expect(document.activeElement).toBe(items[0]);
      await act(async () => host.querySelector(".sessionList")!.dispatchEvent(new Event("scroll")));
      expect(document.querySelector(".sessionMenu")).toBeNull();
    } finally { await act(async () => root.unmount()); host.remove(); }
  });

  it("restores the ellipsis after cancelling a delete confirmation opened from its menu", async () => {
    const host = document.createElement("div"); document.body.append(host);
    const root = createRoot(host);
    function Harness() {
      const confirm = useConfirm(dictionaries.en);
      return <SessionSidebar text={dictionaries.en} language="en" page="chat" ownerProfile={null}
        sessions={[{ id: "one", title: "First" }]} activeSession="one" onCreateSession={() => {}} onSearch={() => {}}
        onSelectSession={() => {}} onDeleteSession={() => { void confirm("Delete this conversation?", { danger: true }); }} />;
    }
    try {
      await act(async () => root.render(<ConfirmationProvider><Harness /></ConfirmationProvider>));
      const trigger = host.querySelector<HTMLButtonElement>(".sessionMenuTrigger")!;
      await act(async () => trigger.click());
      await act(async () => document.querySelector<HTMLButtonElement>(".sessionMenuItem")!.click());
      expect(document.querySelector(".sessionMenu")).toBeNull();
      const cancel = document.querySelector<HTMLButtonElement>(".confirmationCancel")!;
      expect(document.activeElement).toBe(cancel);
      await act(async () => cancel.click());
      expect(document.querySelector(".confirmationDialog")).toBeNull();
      expect(document.activeElement).toBe(trigger);
    } finally { await act(async () => root.unmount()); host.remove(); }
  });

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

  it("keeps the navigation focused on tasks with a labelled and an icon-only conversation entry", () => {
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
    expect(markup).toContain('<span class="workbenchBrandName">Spark <span>X</span></span>');
    expect(markup).not.toContain('<span class="workbenchBrandName">Spark<span>Claw</span></span>');
    expect(markup).toContain(">Schedule</span>");
    expect(markup.indexOf("sidebarScheduleLink")).toBeGreaterThan(markup.indexOf("sessionList"));
    expect(markup).toContain("Recent tasks");
    expect(markup).toContain("newConversationButton");
    expect(markup).toContain(`aria-label="${dictionaries.en.nav.newSession}"`);
    expect(markup.indexOf("sidebarNewConversation")).toBeLessThan(markup.indexOf("conversationListHeader"));
    expect(markup).toContain(`<span>${dictionaries.en.nav.newSession}</span>`);
    expect(markup).toContain('aria-label="Search tasks"');
    expect(markup).toContain(`aria-label="Toggle sidebar"`);
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

      const items = [...host.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')];
      expect(items.map(item => item.textContent)).toEqual(["Archived conversations", "Workspace settings"]);
      await act(async () => items[1].click());
      expect(onNavigate).toHaveBeenCalledWith("settings");
      expect(host.querySelector('[role="menu"]')).toBeNull();
    } finally {
      await act(async () => root.unmount());
    }
  });

  it("archives conversations on this device and restores them from the archived list", async () => {
    const values = new Map<string, string>();
    vi.stubGlobal("localStorage", { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => values.set(key, value) });
    vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => { callback(0); return 0; });
    const host = document.createElement("div"); document.body.append(host);
    const root = createRoot(host), archiveSelection = vi.fn(), create = vi.fn();
    const owner = { id: "owner-one", display_name: "Ada", created_at: "", updated_at: "" };
    const titles = () => [...host.querySelectorAll(".sessionTitle")].map(title => title.textContent);
    try {
      await act(async () => root.render(<SessionSidebar text={dictionaries.zh} language="zh" page="chat" ownerProfile={owner}
        sessions={[{ id: "one", title: "First" }, { id: "two", title: "Second" }]} activeSession="one"
        onCreateSession={create} onArchiveSelection={archiveSelection} onSearch={() => {}} onSelectSession={() => {}} />));
      await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="归档对话: First"]')!.click());
      expect(titles()).toEqual(["Second"]);
      expect(JSON.parse(values.get("sparkx.archived-conversations.v1.owner-one")!)).toEqual(["one"]);
      expect(archiveSelection).toHaveBeenCalledExactlyOnceWith({ id: "two", title: "Second" });
      expect(document.activeElement).toBe(host.querySelector('[aria-label="搜索任务"]'));

      await act(async () => host.querySelector<HTMLButtonElement>(".sidebarAccountTrigger")!.click());
      const archivedEntry = host.querySelector<HTMLButtonElement>('[role="menuitem"]')!;
      expect(archivedEntry.textContent).toBe("已归档对话1");
      await act(async () => archivedEntry.click());
      expect(host.querySelector(".conversationListHeader > span")!.textContent).toBe("已归档对话");
      expect(host.querySelector(".newConversationButton")).toBeNull();
      expect(titles()).toEqual(["First"]);

      await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="取消归档: First"]')!.click());
      expect(titles()).toEqual([]);
      expect(host.querySelector(".sidebarArchiveEmpty")!.textContent).toBe("暂无归档对话");
      await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="返回最近对话"]')!.click());
      expect(titles()).toEqual(["First", "Second"]);
      expect(archiveSelection).toHaveBeenCalledOnce();

      await act(async () => host.querySelector<HTMLButtonElement>(".sidebarNewConversation")!.click());
      expect(create).toHaveBeenCalledOnce();
    } finally { await act(async () => root.unmount()); host.remove(); vi.unstubAllGlobals(); }
  });

  it("keeps the archive unchanged and explains when browser storage rejects it", async () => {
    vi.stubGlobal("localStorage", { getItem: () => null, setItem: () => { throw new Error("quota"); } });
    const host = document.createElement("div"); document.body.append(host);
    const root = createRoot(host), archiveSelection = vi.fn();
    try {
      await act(async () => root.render(<SessionSidebar text={dictionaries.en} language="en" page="chat" ownerProfile={null}
        sessions={[{ id: "one", title: "First" }]} activeSession="one"
        onCreateSession={() => {}} onArchiveSelection={archiveSelection} onSearch={() => {}} onSelectSession={() => {}} />));
      await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Archive conversation: First"]')!.click());
      expect(host.querySelector(".sessionTitle")!.textContent).toBe("First");
      expect(host.querySelector('[role="alert"]')!.textContent).toBe("Could not save the archive state. Check browser storage and try again.");
      expect(archiveSelection).not.toHaveBeenCalled();
    } finally { await act(async () => root.unmount()); host.remove(); vi.unstubAllGlobals(); }
  });
});
