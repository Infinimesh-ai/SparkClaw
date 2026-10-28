import { useConfirm } from "../components/confirmation";
// Session create/rename/delete plus the rename-form state, which route
// through the gateway session endpoints and reset the per-session UI state
// owned by the parent. Extracted from App.tsx so the root component stays
// below the size baseline.
import { useEffect, useRef, useState } from "react";
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
  surfaceError: (err: unknown, fallback: string) => void;
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
  surfaceError,
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
  const confirm = useConfirm(text);
  const [editingSession, setEditingSession] = useState("");
  const [sessionTitleDraft, setSessionTitleDraft] = useState("");
  const [sessionActionId, setSessionActionId] = useState("");
  const sessionActionRef = useRef("");
  const mountedRef = useRef(true);
  useEffect(() => {
    mountedRef.current = true;
    return () => { mountedRef.current = false; };
  }, []);

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
      return session;
    } catch (err) {
      surfaceError(err, text.errors.createSession);
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
    if (!title || sessionActionRef.current || !mountedRef.current) return;
    try {
      sessionActionRef.current = id;
      setSessionActionId(id);
      setError("");
      const updated = await api.updateSession(id, title);
      setSessions((current) => current.map((session) => (session.id === id ? updated : session)));
      cancelRenameSession();
    } catch (err) {
      surfaceError(err, text.errors.renameSession);
    } finally {
      sessionActionRef.current = "";
      setSessionActionId("");
    }
  }

  function deleteSession(id: string) {
    return deleteSessions([id], text.nav.confirmDeleteSession);
  }

  // One confirmation covers every id; a failure stops the batch, and the
  // sessions already deleted are still removed from the UI before it surfaces.
  async function deleteSessions(ids: string[], confirmation: string) {
    if (!ids.length || sessionActionRef.current || !(await confirm(confirmation, { danger: true }))) return;
    if (sessionActionRef.current || !mountedRef.current) return;
    try {
      sessionActionRef.current = ids[0];
      setSessionActionId(ids[0]);
      setError("");
      const deleted: string[] = [];
      let failure: unknown;
      for (const id of ids) {
        try {
          await api.deleteSession(id);
          deleted.push(id);
        } catch (err) {
          failure = err;
          break;
        }
      }
      if (deleted.length) {
        const sessionList = await api.sessions();
        let next = deleted.includes(activeSession) ? sessionList.sessions[0] : sessionList.sessions.find((session) => session.id === activeSession);
        if (!next) next = await api.createSession();
        setDraftsBySession((current) => {
          const nextDrafts = { ...current };
          for (const id of deleted) delete nextDrafts[id];
          return nextDrafts;
        });
        setAttachmentsBySession((current) => deleted.reduce(omitSession, current));
        for (const id of deleted) clearSessionTarget(id);
        setSessions(next ? [next, ...sessionList.sessions.filter((session) => session.id !== next.id)] : sessionList.sessions);
        setActiveSession(next.id);
        cancelRenameSession();
        setTraceRun(null);
        setTab("timeline");
        await Promise.all([refreshSession(next.id), refreshGlobal()]);
      }
      if (failure) throw failure;
    } catch (err) {
      surfaceError(err, text.errors.deleteSession);
    } finally {
      sessionActionRef.current = "";
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
    deleteSession,
    deleteSessions
  };
}

function omitSession<T>(current: Record<string, T>, sessionId: string) {
  const next = { ...current };
  delete next[sessionId];
  return next;
}
