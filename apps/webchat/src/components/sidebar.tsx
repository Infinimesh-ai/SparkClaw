// Session sidebar: brand row, gateway status, session list with rename and
// delete affordances. Extracted from App.tsx so the root component stays
// below the size baseline; all state stays in the parent so behavior is
// unchanged.
import { Clock3, Grid2X2, Languages, Link, MemoryStick, Monitor, PanelLeft, Pencil, Plus, Save, Search, Settings, ShieldCheck, Trash2, X } from "lucide-react";
import { workbenchCopy, type WorkspacePage } from "./workbench";
import { workbenchWordmark } from "./workbenchBrand";
import type { Copy, Language } from "../i18n";
import type { ReadyStatus, Session } from "../api/types";
import { shortId } from "../lib/format";

type SessionSidebarProps = {
  text: Copy;
  language: Language;
  ready: ReadyStatus | null;
  sessions: Session[];
  activeSession: string;
  pendingApprovalCount: number;
  pendingCandidateCount: number;
  editingSession: string;
  sessionTitleDraft: string;
  sessionActionId: string;
  onLanguageChange: (language: Language) => void;
  onCreateSession: () => void;
  onSelectSession: (session: Session) => void;
  onStartRename: (session: Session) => void;
  onCancelRename: () => void;
  onRenameSubmit: (sessionId: string) => void;
  onTitleDraftChange: (value: string) => void;
  onDeleteSession: (sessionId: string) => void;
  page?: WorkspacePage;
  onNavigate?: (page: WorkspacePage) => void;
  onSearch?: () => void;
  onToggleSidebar?: () => void;
};

export function SessionSidebar({
  text,
  language,
  ready,
  sessions,
  activeSession,
  pendingApprovalCount,
  pendingCandidateCount,
  editingSession,
  sessionTitleDraft,
  sessionActionId,
  onLanguageChange,
  onCreateSession,
  onSelectSession,
  onStartRename,
  onCancelRename,
  onRenameSubmit,
  onTitleDraftChange,
  onDeleteSession,
  page = "chat",
  onNavigate,
  onSearch,
  onToggleSidebar
}: SessionSidebarProps) {
  const languageLabel = language === "zh" ? "中" : "EN";
  const nextLanguage: Language = language === "zh" ? "en" : "zh";
  const copy = workbenchCopy[language];
  const navigation = [
    ["chat", copy.home, Grid2X2], ["schedules", copy.schedules, Clock3],
    ["channels", copy.channels, Link], ["memory", copy.memory, MemoryStick],
    ["approvals", copy.approvals, ShieldCheck]
  ] as const;

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

      <button className="primaryButton" onClick={onCreateSession} title={text.nav.newSession}>
        <Plus size={17} />
        <span>{copy.newTask}</span><kbd>⌘ N</kbd>
      </button>
      <nav className="workbenchNav" aria-label={copy.workspace} title={`${text.nav.approvals}: ${pendingApprovalCount} · ${text.nav.memories}: ${pendingCandidateCount}`}>
        <button onClick={onSearch}><Search size={18} /><span>{copy.search}</span><kbd>⌘ K</kbd></button>
        {navigation.map(([target, label, Icon]) => <button key={target} className={page === target ? "selected" : ""} aria-current={page === target ? "page" : undefined} onClick={() => onNavigate?.(target)}><Icon size={18} /><span>{label}</span>{target === "approvals" && pendingApprovalCount > 0 && <small>{pendingApprovalCount}</small>}{target === "memory" && pendingCandidateCount > 0 && <small>{pendingCandidateCount}</small>}</button>)}
      </nav>
      <div className="historyLabel">{copy.recent}<span>{sessions.length}</span></div>
      <div className="sessionList" aria-label={text.nav.sessions}>
        {sessions.map((session) => (
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
                {session.source !== "mcp" && (
                  <div className="sessionActions">
                    <button className="miniIconButton" onClick={() => onStartRename(session)} disabled={sessionActionId === session.id} title={text.nav.renameSession}>
                      <Pencil size={13} />
                    </button>
                    <button className="miniIconButton dangerIcon" onClick={() => onDeleteSession(session.id)} disabled={sessionActionId === session.id} title={text.nav.deleteSession}>
                      <Trash2 size={13} />
                    </button>
                  </div>
                )}
              </>
            )}
          </div>
        ))}
      </div>
      <div className="sidebarFooter">
        <button className="navStatus" onClick={() => onNavigate?.("settings")}>
          <Monitor size={18} /><div><strong>{copy.local}</strong><span>{ready?.ok ? text.nav.ready : text.nav.offline}{ready ? ` · ${ready.model_mode}` : ""}</span></div><i className={`statusDot ${ready?.ok ? "ready" : "offline"}`} />
        </button>
        <div className="workspaceProfile"><button onClick={() => onNavigate?.("settings")}><span className="workspaceAvatar">S</span>{copy.workspace}<Settings size={16} /></button><button className="iconButton subtle" onClick={() => onLanguageChange(nextLanguage)} title={text.nav.language}><Languages size={15} /><span>{languageLabel}</span></button></div>
      </div>
    </aside>
  );
}
