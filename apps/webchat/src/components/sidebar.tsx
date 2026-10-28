// Session sidebar: the schedule entry, task conversation list, archive, and action menus.
import { useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Archive, ArchiveRestore, ArrowLeft, LoaderCircle, LogOut, MoreHorizontal, Pencil, Save, Trash2, X } from "lucide-react";
import { workbenchCopy, type WorkspacePage } from "./workbench";
import workbenchMark from "../assets/sparkx-mark.png";
import type { Copy, Language } from "../i18n";
import type { OwnerProfile, Session } from "../api/types";
import { useDismissibleLayer } from "../hooks/useDismissibleLayer";
import { shortId } from "../lib/format";
import type { ConversationActivity } from "../lib/conversationActivity";
import { ActionIcon } from "./ActionIcon";

type SidebarConversation = Pick<Session, "id" | "title"> & { source?: string };

// Archiving only hides a conversation from the recent list on this device.
const archiveStorageKey = (ownerID?: string) => `sparkx.archived-conversations.v1.${ownerID ?? "local"}`;

function readArchived(key: string) {
  try {
    const value: unknown = JSON.parse(window.localStorage.getItem(key) ?? "[]");
    return Array.isArray(value) ? value.filter((id): id is string => typeof id === "string") : [];
  } catch {
    return [];
  }
}

function moveMenuFocus(event: KeyboardEvent<HTMLDivElement>) {
  if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
  const items = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="menuitem"]:not(:disabled)'));
  if (!items.length) return;
  event.preventDefault();
  const current = items.indexOf(document.activeElement as HTMLButtonElement);
  const index = event.key === "Home" ? 0 : event.key === "End" ? items.length - 1 : (current + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length;
  items[index].focus();
}

function SessionActionMenu({ title, language, text, renameDisabled, deleteDisabled, onRename, onDelete }: {
  title: string;
  language: Language;
  text: Copy;
  renameDisabled: boolean;
  deleteDisabled: boolean;
  onRename?: () => void;
  onDelete?: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState({ top: 0, left: 0 });
  const menuId = useId();
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useDismissibleLayer<HTMLDivElement>({
    open,
    onDismiss: () => setOpen(false),
    initialFocus: '[role="menuitem"]:not(:disabled)',
    restoreFocusRef: triggerRef
  });
  const label = `${language === "zh" ? "会话操作" : "Conversation actions"}: ${title}`;
  function runAction(action: () => void) {
    setOpen(false);
    // A confirmation opened by an action must restore a surviving control,
    // rather than the menu item that disappears as soon as it is chosen.
    triggerRef.current?.focus({ preventScroll: true });
    action();
  }

  useLayoutEffect(() => {
    if (!open || !triggerRef.current || !menuRef.current) return;
    // Render outside the scrolling list, with the same surface tokens as mail.
    // Align to the trigger and flip upward for rows near the viewport bottom.
    const trigger = triggerRef.current.getBoundingClientRect();
    const menu = menuRef.current.getBoundingClientRect();
    const margin = 12, gap = 6;
    const below = trigger.bottom + gap;
    setPosition({
      left: Math.max(margin, Math.min(trigger.right - menu.width, window.innerWidth - menu.width - margin)),
      top: Math.max(margin, Math.min(below + menu.height <= window.innerHeight - margin ? below : trigger.top - gap - menu.height, window.innerHeight - menu.height - margin))
    });
  }, [open, menuRef]);

  useEffect(() => {
    if (!open) return;
    function dismissOutside(event: Event) {
      if (event.target instanceof Node && !menuRef.current?.contains(event.target) && !triggerRef.current?.contains(event.target)) setOpen(false);
    }
    function dismissOnViewportChange(event: Event) {
      if (!(event.target instanceof Node) || !menuRef.current?.contains(event.target)) setOpen(false);
    }
    document.addEventListener("pointerdown", dismissOutside);
    document.addEventListener("focusin", dismissOutside);
    window.addEventListener("scroll", dismissOnViewportChange, true);
    window.addEventListener("resize", dismissOnViewportChange);
    return () => {
      document.removeEventListener("pointerdown", dismissOutside);
      document.removeEventListener("focusin", dismissOutside);
      window.removeEventListener("scroll", dismissOnViewportChange, true);
      window.removeEventListener("resize", dismissOnViewportChange);
    };
  }, [open, menuRef]);

  return <div className="sessionActionMenu">
    <button ref={triggerRef} className="sessionMenuTrigger" type="button"
      title={label} aria-label={label} aria-haspopup="menu" aria-expanded={open} aria-controls={open ? menuId : undefined}
      disabled={renameDisabled && deleteDisabled}
      onClick={() => setOpen(current => !current)}
      onKeyDown={event => {
        if (event.key === "ArrowDown" || event.key === "ArrowUp") {
          event.preventDefault();
          setOpen(true);
        }
      }}>
      <MoreHorizontal size={18} strokeWidth={1.75} aria-hidden="true" />
    </button>
    {open && createPortal(<div ref={menuRef} id={menuId} className="sessionMenu" role="menu" aria-label={label}
      tabIndex={-1} style={position} onKeyDown={moveMenuFocus}>
      {onRename && <button className="sessionMenuItem" type="button" role="menuitem" disabled={renameDisabled}
        onClick={() => runAction(onRename)}>
        <Pencil size={16} strokeWidth={1.75} aria-hidden="true" /><span>{text.nav.renameSession}</span>
      </button>}
      {onDelete && <button className="sessionMenuItem isDestructive" type="button" role="menuitem" disabled={deleteDisabled}
        aria-label={`${text.nav.deleteSession}: ${title}`} onClick={() => runAction(onDelete)}>
        <Trash2 size={16} strokeWidth={1.75} aria-hidden="true" /><span>{text.nav.deleteSession}</span>
      </button>}
    </div>, document.body)}
  </div>;
}

type SessionSidebarProps<T extends SidebarConversation> = {
  text: Copy;
  language: Language;
  home?: boolean;
  searchOpen?: boolean;
  page: WorkspacePage;
  ownerProfile: OwnerProfile | null;
  sessions: T[];
  activeSession: string;
  activityBySession?: Record<string, ConversationActivity | undefined>;
  editingSession?: string;
  sessionTitleDraft?: string;
  sessionActionId?: string;
  onCreateSession: () => void;
  // Called after the open conversation is archived, with the next recent one.
  onArchiveSelection?: (next: T | undefined) => void;
  onSelectSession: (session: T) => void;
  onStartRename?: (session: T) => void;
  onCancelRename?: () => void;
  onRenameSubmit?: (sessionId: string) => void;
  onTitleDraftChange?: (value: string) => void;
  onDeleteSession?: (sessionId: string) => void;
  onNavigate?: (page: WorkspacePage) => void;
  onSearch: () => void;
  onToggleSidebar?: () => void;
  onLogout?: () => void;
  busy?: boolean;
  listNotice?: ReactNode;
};

export function SessionSidebar<T extends SidebarConversation>({
  text,
  language,
  searchOpen = false,
  page,
  ownerProfile,
  sessions,
  activeSession,
  activityBySession = {},
  editingSession = "",
  sessionTitleDraft = "",
  sessionActionId = "",
  onCreateSession,
  onArchiveSelection,
  onSelectSession,
  onStartRename,
  onCancelRename,
  onRenameSubmit,
  onTitleDraftChange,
  onDeleteSession,
  onNavigate,
  onSearch,
  onToggleSidebar,
  onLogout,
  busy = false,
  listNotice
}: SessionSidebarProps<T>) {
  const copy = workbenchCopy[language];
  const zh = language === "zh";
  const archiveKey = archiveStorageKey(ownerProfile?.id);
  const [archived, setArchived] = useState(() => readArchived(archiveKey));
  const [showArchived, setShowArchived] = useState(false);
  const [archiveError, setArchiveError] = useState("");
  const searchRef = useRef<HTMLButtonElement>(null);
  const conversations = sessions.filter((session) => session.source !== "mcp");
  const visibleSessions = conversations.filter((session) => archived.includes(session.id) === showArchived);
  const archivedCount = conversations.filter((session) => archived.includes(session.id)).length;
  useEffect(() => {
    setArchived(readArchived(archiveKey));
    const changed = (event: StorageEvent) => { if (event.key === archiveKey) setArchived(readArchived(archiveKey)); };
    window.addEventListener("storage", changed);
    return () => window.removeEventListener("storage", changed);
  }, [archiveKey]);

  function toggleArchive(session: T) {
    const wasArchived = archived.includes(session.id);
    const next = wasArchived ? archived.filter((id) => id !== session.id) : [...archived, session.id];
    try {
      window.localStorage.setItem(archiveKey, JSON.stringify(next));
    } catch {
      setArchiveError(zh ? "无法保存归档状态，请检查浏览器存储后重试。" : "Could not save the archive state. Check browser storage and try again.");
      return;
    }
    setArchived(next);
    setArchiveError("");
    if (!wasArchived && activeSession === session.id) onArchiveSelection?.(conversations.find((item) => !next.includes(item.id)));
    // The row leaves this list; keep keyboard focus inside the sidebar.
    requestAnimationFrame(() => searchRef.current?.focus({ preventScroll: true }));
  }

  const archiveLabel = (title: string) =>
    `${zh ? showArchived ? "取消归档" : "归档对话" : showArchived ? "Unarchive conversation" : "Archive conversation"}: ${title}`;

  function createSession() {
    setShowArchived(false);
    onCreateSession();
  }

  const [accountMenuOpen, setAccountMenuOpen] = useState(false);
  const accountMenuRef = useRef<HTMLDivElement>(null);
  const accountTriggerRef = useRef<HTMLButtonElement>(null);
  const accountLayerRef = useDismissibleLayer<HTMLDivElement>({ open: accountMenuOpen, onDismiss: () => setAccountMenuOpen(false), initialFocus: '[role="menuitem"]', restoreFocusRef: accountTriggerRef });
  const accountName = ownerProfile?.display_name.trim() || text.app.name;
  const accountEmail = ownerProfile?.email?.trim();
  const accountContext = accountEmail || copy.local;
  const scheduleLink = <button
    className="sidebarScheduleLink"
    type="button"
    aria-current={page === "schedules" ? "page" : undefined}
    onClick={() => onNavigate?.("schedules")}
  >
    <ActionIcon name="calendar" active={page === "schedules"} />
    <span>{copy.schedules}</span>
  </button>;

  useEffect(() => {
    if (!accountMenuOpen) return;

    function dismissAccountMenu(event: PointerEvent) {
      if (!accountMenuRef.current?.contains(event.target as Node)) setAccountMenuOpen(false);
    }

    document.addEventListener("pointerdown", dismissAccountMenu);
    return () => {
      document.removeEventListener("pointerdown", dismissAccountMenu);
    };
  }, [accountMenuOpen]);

  return (
    <aside className="sidebar">
      <div className="brandRow">
        <button className="brand" type="button" onClick={() => onNavigate?.("chat")} title={text.app.tagline}>
          <img className="workbenchBrandMark" src={workbenchMark} alt="" aria-hidden="true" />
          <span className="workbenchBrandName">Spark <span>X</span></span>
        </button>
        <button className="iconButton subtle sidebarCollapseButton" onClick={onToggleSidebar} title={`${copy.toggleNav} (⌘/Ctrl+B)`} aria-keyshortcuts="Meta+B Control+B" aria-label={copy.toggleNav}>
          <ActionIcon name="panel-left" />
        </button>
      </div>

      <button className="sidebarNewConversation" type="button" disabled={busy} onClick={createSession}>
        <ActionIcon name="compose" />
        <span>{text.nav.newSession}</span>
      </button>

      <div className="conversationListHeader">
        <span>{showArchived ? (zh ? "已归档对话" : "Archived") : copy.recent}</span>
        <div className="conversationListActions">
          {showArchived && <button className="conversationHeaderButton" type="button" onClick={() => setShowArchived(false)}
            title={zh ? "返回最近对话" : "Back to recent conversations"} aria-label={zh ? "返回最近对话" : "Back to recent conversations"}>
            <ArrowLeft size={16} />
          </button>}
          <button ref={searchRef} className="conversationHeaderButton" onClick={onSearch} title={`${copy.search} (⌘/Ctrl+K)`} aria-keyshortcuts="Meta+K Control+K" aria-label={copy.search} aria-haspopup="dialog" aria-expanded={searchOpen}>
            <ActionIcon name="search" active={searchOpen} />
          </button>
          {!showArchived && <button className="conversationHeaderButton newConversationButton" disabled={busy} onClick={createSession} title={`${text.nav.newSession} (⌘/Ctrl+N)`} aria-keyshortcuts="Meta+N Control+N" aria-label={text.nav.newSession}>
            <ActionIcon name="compose" />
          </button>}
        </div>
      </div>
      <nav className="sessionList" aria-label={text.nav.sessions}>
        {listNotice}
        {archiveError && <p className="sidebarArchiveNotice" role="alert">{archiveError}</p>}
        {showArchived && !visibleSessions.length && <p className="sidebarArchiveEmpty">{zh ? "暂无归档对话" : "No archived conversations"}</p>}
        {visibleSessions.map((session) => (
          <div className={`sessionItem ${page === "chat" && session.id === activeSession ? "active" : ""}`} key={session.id}>
            {editingSession === session.id ? (
              <form
                className="sessionRenameForm"
                onSubmit={(event) => {
                  event.preventDefault();
                  onRenameSubmit?.(session.id);
                }}
              >
                <input
                  autoFocus
                  aria-label={text.nav.renameSession}
                  value={sessionTitleDraft}
                  onChange={(event) => onTitleDraftChange?.(event.target.value)}
                  disabled={sessionActionId === session.id}
                />
                <button className="miniIconButton" disabled={!sessionTitleDraft.trim() || sessionActionId === session.id} title={text.nav.saveSessionName}>
                  <Save size={13} />
                </button>
                <button className="miniIconButton" type="button" onClick={onCancelRename} disabled={sessionActionId === session.id} title={text.common.cancel}>
                  <X size={13} />
                </button>
              </form>
            ) : (
              <>
                <button className="sessionSelect" title={session.title} disabled={busy} aria-current={page === "chat" && session.id === activeSession ? "page" : undefined} onClick={() => onSelectSession(session)}>
                  <span className="sessionTitle">{session.title}</span>
                  <small>{shortId(session.id)}</small>
                  {activityBySession[session.id] && <span className={`sessionActivity ${activityBySession[session.id]}`}
                    role="img" aria-label={activityBySession[session.id] === "running" ? text.chat.waiting : text.chat.taskCompleted}
                    title={activityBySession[session.id] === "running" ? text.chat.waiting : text.chat.taskCompleted}>
                    {activityBySession[session.id] === "running" ? <LoaderCircle size={13} aria-hidden="true" /> : <i aria-hidden="true" />}
                  </span>}
                </button>
                <div className="sessionActions">
                  <button className="sessionArchiveTrigger" type="button" disabled={busy || Boolean(sessionActionId)}
                    title={archiveLabel(session.title)} aria-label={archiveLabel(session.title)} onClick={() => toggleArchive(session)}>
                    {showArchived ? <ArchiveRestore size={14.4} strokeWidth={1.75} aria-hidden="true" /> : <Archive size={14.4} strokeWidth={1.75} aria-hidden="true" />}
                  </button>
                  {(onStartRename || onDeleteSession) && <SessionActionMenu
                    title={session.title} language={language} text={text}
                    renameDisabled={!onStartRename || sessionActionId === session.id}
                    deleteDisabled={!onDeleteSession || busy || Boolean(sessionActionId)}
                    onRename={onStartRename ? () => onStartRename(session) : undefined}
                    onDelete={onDeleteSession ? () => onDeleteSession(session.id) : undefined}
                  />}
                </div>
              </>
            )}
          </div>
        ))}
      </nav>
      <div className="sidebarFooter">
        {scheduleLink}
        <div className="sidebarAccount" ref={accountMenuRef}>
          {accountMenuOpen && <div ref={accountLayerRef} className="sidebarAccountMenu" role="menu" aria-label={accountName} onKeyDown={moveMenuFocus}>
            <div className="sidebarAccountSummary">
              <span className="workspaceAvatar" aria-hidden="true"><ActionIcon name="account" active={accountMenuOpen} /></span>
              <span className="sidebarAccountIdentity">
                <strong>{accountName}</strong>
                <small>{accountContext}</small>
              </span>
            </div>
            <button
              className="sidebarAccountMenuItem"
              type="button"
              role="menuitem"
              onClick={() => {
                setAccountMenuOpen(false);
                setShowArchived((current) => !current);
              }}
            >
              <Archive size={18} strokeWidth={1.75} aria-hidden="true" />
              <span>{showArchived ? (zh ? "最近对话" : "Recent conversations") : (zh ? "已归档对话" : "Archived conversations")}</span>
              {!showArchived && archivedCount > 0 && <small className="sidebarArchiveCount">{archivedCount}</small>}
            </button>
            <button
              className="sidebarAccountMenuItem"
              type="button"
              role="menuitem"
              onClick={() => {
                setAccountMenuOpen(false);
                onNavigate?.("settings");
              }}
            >
              <ActionIcon name="settings" />
              <span title={`${copy.settings} (⌘/Ctrl+,)`}>{copy.settings}</span>
            </button>
            {onLogout && <button className="sidebarAccountMenuItem" type="button" role="menuitem" onClick={() => { setAccountMenuOpen(false); onLogout(); }}><LogOut size={18} strokeWidth={1.75} aria-hidden="true" /><span>{zh ? "退出登录" : "Sign out"}</span></button>}
          </div>}
          <button
            ref={accountTriggerRef}
            className="sidebarAccountTrigger"
            type="button"
            aria-haspopup="menu"
            aria-expanded={accountMenuOpen}
            onClick={() => setAccountMenuOpen((current) => !current)}
          >
            <span className="workspaceAvatar" aria-hidden="true"><ActionIcon name="account" active={accountMenuOpen} /></span>
            <span className="sidebarAccountIdentity">
              <strong>{accountName}</strong>
              <small>{accountContext}</small>
            </span>
            <MoreHorizontal size={16} strokeWidth={1.75} aria-hidden="true" />
          </button>
        </div>
      </div>
    </aside>
  );
}
