import { useCallback, useEffect, useMemo, useRef, useState, type SetStateAction } from "react";
import { api, apiToken } from "../api/client";
import type { MessageAttachment } from "../api/types";
import { WorkbenchDrafts, type WorkbenchDraft, type DraftStatus } from "../lib/workbenchDraft";

// Only presentation references are saved here. Sending/opening an attachment
// still resolves and authorizes it through the host repository.
function attachments(value: WorkbenchDraft): MessageAttachment[] {
  return value.attachment_ids.map((reference) => {
    const item: unknown = JSON.parse(reference);
    if (!item || typeof item !== "object" || !("rel_path" in item) || !("name" in item) ||
        typeof item.rel_path !== "string" || typeof item.name !== "string") throw new Error("Saved attachment is invalid");
    return item as MessageAttachment;
  });
}

export function useHostWorkbenchDrafts(authEpoch: number, onError: (error: unknown) => void) {
  const [content, setContent] = useState<Record<string, string>>({});
  const [files, setFiles] = useState<Record<string, MessageAttachment[]>>({});
  const [status, setStatus] = useState<Record<string, DraftStatus>>({});
  const [loaded, setLoaded] = useState<Record<string, boolean>>({});
  const [revisions, setRevisions] = useState<Record<string, number>>({});
  const contentRef = useRef(content); const filesRef = useRef(files);
  const statusRef = useRef(status); statusRef.current = status;
  const loadedRef = useRef(loaded); loadedRef.current = loaded;
  const errorHandler = useRef(onError); errorHandler.current = onError;
  const current = useRef<symbol | undefined>(undefined);
  const credential = apiToken();
  const repository = useMemo(() => {
    const token = Symbol("host-drafts");
    const pending = new Map<string, Promise<void>>();
    const isCurrent = () => apiToken() === credential && current.current === token;
    const checkIdentity = () => {
      if (!isCurrent()) throw new Error("Draft login changed");
    };
    const load = async (id: string) => { checkIdentity(); const value = await api.workbenchDraft(id); checkIdentity(); return value; };
    const drafts = new WorkbenchDrafts({ load, save: async (id, value) => {
      checkIdentity(); const saved = await api.saveWorkbenchDraft(id, value); checkIdentity(); return saved;
    } }, {
      onChange: (id, value, state) => {
        if (current.current !== token) return;
        const refs = attachments(value);
        contentRef.current = { ...contentRef.current, [id]: value.content };
        filesRef.current = { ...filesRef.current, [id]: refs };
        setContent(contentRef.current); setFiles(filesRef.current);
        setStatus((previous) => ({ ...previous, [id]: state }));
        setLoaded((previous) => ({ ...previous, [id]: true }));
        setRevisions((previous) => ({ ...previous, [id]: value.revision }));
      },
      onError: (error) => { if (current.current === token) errorHandler.current(error); }
    });
    const edit = (id: string, value: string, refs: MessageAttachment[]) => {
      const write = (pending.get(id) ?? Promise.resolve()).catch(() => undefined).then(async () => {
        await drafts.load(id); checkIdentity();
        drafts.edit(id, { content: value, attachment_ids: refs.map((ref) => JSON.stringify(ref)) });
      });
      pending.set(id, write);
      void write.catch((error) => { if (current.current === token) errorHandler.current(error); });
    };
    const loadDraft = async (id: string) => {
      try { return await drafts.load(id); }
      catch (error) { if (isCurrent()) setStatus((previous) => ({ ...previous, [id]: "error" })); throw error; }
    };
    return { token, drafts, edit, isCurrent, load: loadDraft,
      flush: async (id: string) => { await pending.get(id); return drafts.flush(id); },
      keepAndSave: async (id: string) => { await pending.get(id); return drafts.saveCurrentOverLatest(id); },
      reload: async (id: string) => { await pending.get(id); const value = await load(id); drafts.accept(id, value); return value; },
      clear: async (id: string, revision: number) => { checkIdentity(); const value = await api.saveWorkbenchDraft(id, { content: "", attachment_ids: [], revision }); checkIdentity(); drafts.accept(id, value); return value; },
      accept: (id: string, value: WorkbenchDraft) => drafts.accept(id, value),
    };
  }, [credential, authEpoch]);
  current.current = repository.token;
  useEffect(() => {
    contentRef.current = {}; filesRef.current = {}; setContent({}); setFiles({}); setStatus({}); setLoaded({}); setRevisions({});
    return () => repository.drafts.dispose();
  }, [repository]);
  useEffect(() => {
    const pendingIDs = () => Object.keys(statusRef.current).filter((id) => loadedRef.current[id] && statusRef.current[id] !== "saved");
    const flush = () => { for (const id of pendingIDs()) void repository.flush(id).catch((error) => { if (repository.isCurrent()) errorHandler.current(error); }); };
    const beforeUnload = (event: BeforeUnloadEvent) => {
      if (!pendingIDs().length) return;
      flush(); event.preventDefault(); event.returnValue = "";
    };
    const hidden = () => { if (document.visibilityState === "hidden") flush(); };
    window.addEventListener("beforeunload", beforeUnload);
    window.addEventListener("pagehide", flush);
    document.addEventListener("visibilitychange", hidden);
    return () => { window.removeEventListener("beforeunload", beforeUnload); window.removeEventListener("pagehide", flush); document.removeEventListener("visibilitychange", hidden); };
  }, [repository]);
  const setDraftsBySession = useCallback((action: SetStateAction<Record<string, string>>) => {
    if (!repository.isCurrent()) return;
    const previous = contentRef.current;
    const next = typeof action === "function" ? action(previous) : action;
    contentRef.current = next; setContent(next);
    for (const [id, value] of Object.entries(next)) if (previous[id] !== value) repository.edit(id, value, filesRef.current[id] ?? []);
  }, [repository]);
  const setAttachmentsBySession = useCallback((action: SetStateAction<Record<string, MessageAttachment[]>>) => {
    if (!repository.isCurrent()) return;
    const previous = filesRef.current;
    const next = typeof action === "function" ? action(previous) : action;
    filesRef.current = next; setFiles(next);
    for (const [id, refs] of Object.entries(next)) if (previous[id] !== refs) repository.edit(id, contentRef.current[id] ?? "", refs);
  }, [repository]);
  return { content, files, status, loaded, revisions, setDraftsBySession, setAttachmentsBySession, repository };
}
