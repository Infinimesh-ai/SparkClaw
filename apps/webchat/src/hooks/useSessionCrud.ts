// Session create/rename/delete plus the rename-form state, which route
// through the gateway session endpoints and reset the per-session UI state
// owned by the parent. Extracted from App.tsx so the root component stays
// below the size baseline.
import { useState } from "react";
import type { Dispatch, SetStateAction } from "react";
import { api } from "../api/client";
import type {
  AuditEvent,
  EpisodeSummary,
  Message,
  MessageAttachment,
  ModelCall,
  RunTrace,
  Session,
  ToolCall
} from "../api/types";
import type { Copy } from "../i18n";
import type { PanelTab } from "../components/inspector";

type Options = {
  activeSession: string;
  text: Copy;
  setError: (message: string) => void;
  setSessions: Dispatch<SetStateAction<Session[]>>;
  setActiveSession: (sessionId: string) => void;
  setMessages: (messages: Message[]) => void;
  setDraftsBySession: Dispatch<SetStateAction<Record<string, string>>>;
  setAttachmentsBySession: Dispatch<SetStateAction<Record<string, MessageAttachment[]>>>;
  setToolCalls: (calls: ToolCall[]) => void;
  setModelCalls: (calls: ModelCall[]) => void;
  setAuditEvents: (events: AuditEvent[]) => void;
  setEpisodes: (episodes: EpisodeSummary[]) => void;
  setTab: (tab: PanelTab) => void;
  setTraceRun: (trace: RunTrace | null) => void;
  clearSessionTarget: (sessionId: string) => void;
  refreshSession: (sessionId: string) => Promise<void>;
  refreshGlobal: () => Promise<void>;
};

export function useSessionCrud({
  activeSession,
  text,
  setError,
  setSessions,
  setActiveSession,
  setMessages,
  setDraftsBySession,
  setAttachmentsBySession,
  setToolCalls,
  setModelCalls,
  setAuditEvents,
  setEpisodes,
  setTab,
  setTraceRun,
  clearSessionTarget,
  refreshSession,
  refreshGlobal
}: Options) {
  const [editingSession, setEditingSession] = useState("");
  const [sessionTitleDraft, setSessionTitleDraft] = useState("");
  const [sessionActionId, setSessionActionId] = useState("");

  async function createSession() {
    try {
      setError("");
      const session = await api.createSession();
      setSessions((current) => [session, ...current]);
      setActiveSession(session.id);
      setMessages([]);
      setAttachmentsBySession((current) => ({ ...current, [session.id]: [] }));
      setToolCalls([]);
      setModelCalls([]);
      setAuditEvents([]);
      setEpisodes([]);
      setTab("timeline");
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.createSession);
    }
  }

  function startRenameSession(session: Session) {
    setEditingSession(session.id);
    setSessionTitleDraft(session.title);
  }

  function cancelRenameSession() {
    setEditingSession("");
    setSessionTitleDraft("");
  }

  async function renameSession(id: string) {
    const title = sessionTitleDraft.trim();
    if (!title || sessionActionId) return;
    try {
      setSessionActionId(id);
      setError("");
      const updated = await api.updateSession(id, title);
      setSessions((current) => current.map((session) => (session.id === id ? updated : session)));
      cancelRenameSession();
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.renameSession);
    } finally {
      setSessionActionId("");
    }
  }

  async function deleteSession(id: string) {
    if (sessionActionId || !window.confirm(text.nav.confirmDeleteSession)) return;
    try {
      setSessionActionId(id);
      setError("");
      await api.deleteSession(id);
      const sessionList = await api.sessions();
      let next = id === activeSession ? sessionList.sessions[0] : sessionList.sessions.find((session) => session.id === activeSession);
      if (!next) next = await api.createSession();
      setDraftsBySession((current) => {
        const nextDrafts = { ...current };
        delete nextDrafts[id];
        return nextDrafts;
      });
      setAttachmentsBySession((current) => omitSession(current, id));
      clearSessionTarget(id);
      setSessions(next ? [next, ...sessionList.sessions.filter((session) => session.id !== next.id)] : sessionList.sessions);
      setActiveSession(next.id);
      cancelRenameSession();
      setTraceRun(null);
      setTab("timeline");
      await Promise.all([refreshSession(next.id), refreshGlobal()]);
    } catch (err) {
      setError(err instanceof Error ? err.message : text.errors.deleteSession);
    } finally {
      setSessionActionId("");
    }
  }

  return {
    editingSession,
    sessionTitleDraft,
    setSessionTitleDraft,
    sessionActionId,
    createSession,
    startRenameSession,
    cancelRenameSession,
    renameSession,
    deleteSession
  };
}

function omitSession<T>(current: Record<string, T>, sessionId: string) {
  const next = { ...current };
  delete next[sessionId];
  return next;
}
