// Session sidebar: the schedule entry, task conversation list, and direct actions.
import { useEffect, useRef, useState, type ReactNode } from "react";
import { CalendarDays, LogOut, MoreHorizontal, PanelLeft, Pencil, Save, Search, Settings, SquarePen, Trash2, X } from "lucide-react";
import { workbenchCopy, type WorkspacePage } from "./workbench";
import workbenchMark from "../../../desktop/src/assets/icon.png";
import type { Copy, Language } from "../i18n";
import type { OwnerProfile, Session } from "../api/types";
import { shortId } from "../lib/format";

type SidebarConversation = Pick<Session, "id" | "title"> & { source?: string };

type SessionSidebarProps<T extends SidebarConversation> = {
  text: Copy;
  language: Language;
  page: WorkspacePage;
  ownerProfile: OwnerProfile | null;
  sessions: T[];
  activeSession: string;
  editingSession?: string;
  sessionTitleDraft?: string;
  sessionActionId?: string;
  onCreateSession: () => void;
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
  page,
  ownerProfile,
  sessions,
  activeSession,
  editingSession = "",
  sessionTitleDraft = "",
  sessionActionId = "",
  onCreateSession,
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
  const visibleSessions = sessions.filter((session) => session.source !== "mcp");
  const [accountMenuOpen, setAccountMenuOpen] = useState(false);
  const accountMenuRef = useRef<HTMLDivElement>(null);
  const accountName = ownerProfile?.display_name.trim() || text.app.name;
  const accountEmail = ownerProfile?.email?.trim();
  const accountInitial = (accountName.match(/[A-Z\p{L}]/gu) ?? Array.from(accountName))
    .slice(0, 2)
    .join("")
    .toLocaleUpperCase() || "SC";
  const accountContext = accountEmail || copy.local;

  useEffect(() => {
    if (!accountMenuOpen) return;

    function dismissAccountMenu(event: PointerEvent) {
      if (!accountMenuRef.current?.contains(event.target as Node)) setAccountMenuOpen(false);
    }

    function dismissAccountMenuOnEscape(event: KeyboardEvent) {
      if (event.key === "Escape") setAccountMenuOpen(false);
    }

    document.addEventListener("pointerdown", dismissAccountMenu);
    document.addEventListener("keydown", dismissAccountMenuOnEscape);
    return () => {
      document.removeEventListener("pointerdown", dismissAccountMenu);
      document.removeEventListener("keydown", dismissAccountMenuOnEscape);
    };
  }, [accountMenuOpen]);

  return (
    <aside className="sidebar">
      <div className="brandRow">
        <button className="brand" type="button" onClick={() => onNavigate?.("chat")} title={text.app.tagline}>
          <img className="workbenchBrandMark" src={workbenchMark} alt="" aria-hidden="true" />
          <span className="workbenchBrandName">Spark<span>Claw</span></span>
        </button>
        <button className="iconButton subtle" onClick={onToggleSidebar} title={copy.toggleNav} aria-label={copy.toggleNav}>
          <PanelLeft size={17} />
        </button>
      </div>

      <button
        className="sidebarScheduleLink"
        type="button"
        aria-current={page === "schedules" ? "page" : undefined}
        onClick={() => onNavigate?.("schedules")}
      >
        <CalendarDays size={17} strokeWidth={1.7} aria-hidden="true" />
        <span>{copy.schedules}</span>
      </button>

      <div className="conversationListHeader">
        <span>{copy.recent}</span>
        <div className="conversationListActions">
          <button className="conversationHeaderButton" onClick={onSearch} title={copy.search} aria-label={copy.search}>
            <Search size={15.5} strokeWidth={1.7} />
          </button>
          <button className="conversationHeaderButton newConversationButton" disabled={busy} onClick={onCreateSession} title={text.nav.newSession} aria-label={text.nav.newSession}>
            <SquarePen size={16} strokeWidth={1.6} />
          </button>
        </div>
      </div>
      <nav className="sessionList" aria-label={text.nav.sessions}>
        {listNotice}
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
                <button className="sessionSelect" disabled={busy} aria-current={page === "chat" && session.id === activeSession ? "page" : undefined} onClick={() => onSelectSession(session)}>
                  <span>{session.title}</span>
                  <small>{shortId(session.id)}</small>
                </button>
                {(onStartRename || onDeleteSession) && <div className="sessionActions">
                  {onStartRename && <button className="miniIconButton" onClick={() => onStartRename?.(session)} disabled={sessionActionId === session.id} title={text.nav.renameSession}>
                    <Pencil size={13} />
                  </button>}
                  {onDeleteSession && <button className="miniIconButton dangerIcon" onClick={() => onDeleteSession?.(session.id)} disabled={sessionActionId === session.id} title={text.nav.deleteSession}>
                    <Trash2 size={13} />
                  </button>}
                </div>}
              </>
            )}
          </div>
        ))}
      </nav>
      <div className="sidebarFooter">
        <div className="sidebarAccount" ref={accountMenuRef}>
          {accountMenuOpen && <div className="sidebarAccountMenu" role="menu">
            <div className="sidebarAccountSummary">
              <span className="workspaceAvatar" aria-hidden="true">{accountInitial}</span>
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
                onNavigate?.("settings");
              }}
            >
              <Settings size={16} />
              <span>{copy.settings}</span>
            </button>
            {onLogout && <button className="sidebarAccountMenuItem" type="button" role="menuitem" onClick={() => { setAccountMenuOpen(false); onLogout(); }}><LogOut size={16} /><span>{language === "zh" ? "退出登录" : "Sign out"}</span></button>}
          </div>}
          <button
            className="sidebarAccountTrigger"
            type="button"
            aria-haspopup="menu"
            aria-expanded={accountMenuOpen}
            onClick={() => setAccountMenuOpen((current) => !current)}
          >
            <span className="workspaceAvatar" aria-hidden="true">{accountInitial}</span>
            <span className="sidebarAccountIdentity">
              <strong>{accountName}</strong>
              <small>{accountContext}</small>
            </span>
            <MoreHorizontal size={16} aria-hidden="true" />
          </button>
        </div>
      </div>
    </aside>
  );
}
