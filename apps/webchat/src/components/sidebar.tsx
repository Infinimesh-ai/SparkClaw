// Session sidebar: the task conversation list and its direct actions. Workspace
// tools live in Settings so the navigation stays focused on conversations.
import { useEffect, useRef, useState } from "react";
import { MoreHorizontal, PanelLeft, Pencil, Save, Search, Settings, SquarePen, Trash2, X } from "lucide-react";
import { workbenchCopy, type WorkspacePage } from "./workbench";
import { workbenchWordmark } from "./workbenchBrand";
import type { Copy, Language } from "../i18n";
import type { OwnerProfile, Session } from "../api/types";
import { shortId } from "../lib/format";

type SessionSidebarProps = {
  text: Copy;
  language: Language;
  ownerProfile: OwnerProfile | null;
  sessions: Session[];
  activeSession: string;
  editingSession: string;
  sessionTitleDraft: string;
  sessionActionId: string;
  onCreateSession: () => void;
  onSelectSession: (session: Session) => void;
  onStartRename: (session: Session) => void;
  onCancelRename: () => void;
  onRenameSubmit: (sessionId: string) => void;
  onTitleDraftChange: (value: string) => void;
  onDeleteSession: (sessionId: string) => void;
  onNavigate?: (page: WorkspacePage) => void;
  onSearch: () => void;
  onToggleSidebar?: () => void;
};

export function SessionSidebar({
  text,
  language,
  ownerProfile,
  sessions,
  activeSession,
  editingSession,
  sessionTitleDraft,
  sessionActionId,
  onCreateSession,
  onSelectSession,
  onStartRename,
  onCancelRename,
  onRenameSubmit,
  onTitleDraftChange,
  onDeleteSession,
  onNavigate,
  onSearch,
  onToggleSidebar
}: SessionSidebarProps) {
  const copy = workbenchCopy[language];
  const visibleSessions = sessions.filter((session) => session.source !== "mcp");
  const [accountMenuOpen, setAccountMenuOpen] = useState(false);
  const accountMenuRef = useRef<HTMLDivElement>(null);
  const accountName = ownerProfile?.display_name.trim() || text.app.name;
  const accountEmail = ownerProfile?.email?.trim();
  const accountInitial = Array.from(accountName)[0]?.toLocaleUpperCase() || "S";

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
        <div className="brand">
          <img className="workbenchWordmark" src={workbenchWordmark} alt={text.app.name} title={text.app.tagline} />
        </div>
        <button className="iconButton subtle" onClick={onToggleSidebar} title={copy.toggleNav} aria-label={copy.toggleNav}>
          <PanelLeft size={17} />
        </button>
      </div>

      <div className="conversationListHeader">
        <span>{copy.recent}</span>
        <div className="conversationListActions">
          <button className="conversationHeaderButton" onClick={onSearch} title={copy.search} aria-label={copy.search}>
            <Search size={15.5} strokeWidth={1.7} />
          </button>
          <button className="conversationHeaderButton newConversationButton" onClick={onCreateSession} title={text.nav.newSession} aria-label={text.nav.newSession}>
            <SquarePen size={16} strokeWidth={1.6} />
          </button>
        </div>
      </div>
      <div className="sessionList" aria-label={text.nav.sessions}>
        {visibleSessions.map((session) => (
          <div className={`sessionItem ${session.id === activeSession ? "active" : ""}`} key={session.id}>
            {editingSession === session.id ? (
              <form
                className="sessionRenameForm"
                onSubmit={(event) => {
                  event.preventDefault();
                  onRenameSubmit(session.id);
                }}
              >
                <input
                  aria-label={text.nav.renameSession}
                  value={sessionTitleDraft}
                  onChange={(event) => onTitleDraftChange(event.target.value)}
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
                <button className="sessionSelect" onClick={() => onSelectSession(session)}>
                  <span>{session.title}</span>
                  <small>{shortId(session.id)}</small>
                </button>
                <div className="sessionActions">
                  <button className="miniIconButton" onClick={() => onStartRename(session)} disabled={sessionActionId === session.id} title={text.nav.renameSession}>
                    <Pencil size={13} />
                  </button>
                  <button className="miniIconButton dangerIcon" onClick={() => onDeleteSession(session.id)} disabled={sessionActionId === session.id} title={text.nav.deleteSession}>
                    <Trash2 size={13} />
                  </button>
                </div>
              </>
            )}
          </div>
        ))}
      </div>
      <div className="sidebarFooter">
        <div className="sidebarAccount" ref={accountMenuRef}>
          {accountMenuOpen && <div className="sidebarAccountMenu" role="menu">
            <div className="sidebarAccountSummary">
              <span className="workspaceAvatar" aria-hidden="true">{accountInitial}</span>
              <span className="sidebarAccountIdentity">
                <strong>{accountName}</strong>
                {accountEmail && <small>{accountEmail}</small>}
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
              {accountEmail && <small>{accountEmail}</small>}
            </span>
            <MoreHorizontal size={16} aria-hidden="true" />
          </button>
        </div>
      </div>
    </aside>
  );
}
