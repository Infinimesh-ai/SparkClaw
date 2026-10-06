import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { WorkbenchDrafts, type WorkbenchDraft, type WorkbenchDraftAdapter, type DraftStatus } from "../lib/workbenchDraft";

const empty: WorkbenchDraft = { content: "", attachment_ids: [], revision: 0 };
export function useWorkbenchDraft(adapter: WorkbenchDraftAdapter, onError: (error: unknown) => void) {
  const [draft, setDraft] = useState<WorkbenchDraft>(empty);
  const [loaded, setLoaded] = useState(false);
  const [status, setStatus] = useState<DraftStatus>("loading");
  const current = useRef("");
  const generation = useRef(0);
  const active = useRef(true);
  const latest = useRef(draft);
  const errorHandler = useRef(onError); errorHandler.current = onError;
  const controllerToken = useRef<symbol | null>(null);
  const drafts = useMemo(() => {
    const token = Symbol("draft-controller"); controllerToken.current = token;
    return new WorkbenchDrafts(adapter, {
    onChange: (id, value, state) => {
      if (active.current && controllerToken.current === token && current.current === id) { latest.current = value; setDraft(value); setStatus(state); setLoaded(true); }
    },
    onError: (error) => { if (active.current && controllerToken.current === token) errorHandler.current(error); },
    });
  }, [adapter]);
  const select = useCallback(async (id: string) => {
    const request = ++generation.current;
    await drafts.flush(current.current);
    if (request !== generation.current || !active.current) return;
    current.current = id; setStatus("loading"); setLoaded(false);
    const value = await drafts.load(id);
    if (request === generation.current && active.current) { latest.current = value; setDraft(value); setStatus("saved"); setLoaded(true); }
  }, [drafts]);
  useEffect(() => {
    active.current = true; current.current = ""; latest.current = empty;
    setDraft(empty); setLoaded(false); setStatus("loading");
    void select("").catch((error) => { setStatus("error"); errorHandler.current(error); });
    return () => { active.current = false; generation.current++; drafts.dispose(); };
  }, [drafts, select]);
  return {
    draft, status, ready: loaded,
    select,
    id: () => current.current,
    content: (content: string) => drafts.edit(current.current, { ...latest.current, content }),
    attachments: (attachment_ids: string[]) => drafts.edit(current.current, { ...latest.current, attachment_ids }),
    flush: () => drafts.flush(current.current),
    accept: (id: string, value: WorkbenchDraft) => drafts.accept(id, value),
  };
}
