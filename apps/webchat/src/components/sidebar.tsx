// Session sidebar: brand row, gateway status, session list with rename and
// delete affordances. Extracted from App.tsx so the root component stays
// below the size baseline; all state stays in the parent so behavior is
// unchanged.
import { Languages, Pencil, Plus, Save, Terminal, Trash2, X } from "lucide-react";
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
  onDeleteSession
}: SessionSidebarProps) {
  const languageLabel = language === "zh" ? "中" : "EN";
  const nextLanguage: Language = language === "zh" ? "en" : "zh";

  return (
    <aside className="sidebar">
      <div className="brandRow">
        <div className="brand">
          <div className="brandMark">
            <Terminal size={18} />
          </div>
          <div>
            <strong>{text.app.name}</strong>
            <span>{text.app.tagline}</span>
          </div>
        </div>
        <button className="iconButton subtle" onClick={() => onLanguageChange(nextLanguage)} title={text.nav.language}>
          <Languages size={17} />
          <span>{languageLabel}</span>
        </button>
      </div>

      <div className="navStatus">
        <div className={`statusDot ${ready?.ok ? "ready" : "offline"}`} />
        <div>
          <strong>{ready?.ok ? text.nav.ready : text.nav.offline}</strong>
          <span>{ready ? ready.model_mode : text.topbar.connecting}</span>
        </div>
      </div>

      <button className="primaryButton" onClick={onCreateSession} title={text.nav.newSession}>
        <Plus size={17} />
        <span>{text.nav.newSession}</span>
      </button>

      <dl className="navMetrics">
        <dt>{text.nav.sessions}</dt>
        <dd>{sessions.length}</dd>
        <dt>{text.nav.approvals}</dt>
        <dd>{pendingApprovalCount}</dd>
        <dt>{text.nav.memories}</dt>
        <dd>{pendingCandidateCount}</dd>
      </dl>

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
    </aside>
  );
}
