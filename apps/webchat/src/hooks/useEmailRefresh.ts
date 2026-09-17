import { useEffect, useSyncExternalStore } from "react";
import { api, APIError } from "../api/client";
import type { EmailSyncSchedule, EmailSyncStatus } from "../api/email";

// This is a mailbox-scoped submission guard, not a lock on model/parse backlog.
// Server-side atomic coalescing remains authoritative across browser processes.
const storageKey = "sparkclaw.email.refresh.v1";
type Pending = { baseline: string; request?: string; submitted_at?: number; generation?: string };
type PendingMap = Record<string, Pending>;
const listeners = new Set<() => void>();
const submitting = new Set<string>();
let revision = 0;
let generationSequence = 0;
function nextGeneration() {
  // UI ordering only, not an authentication token. getRandomValues also works
  // on the supported LAN HTTP deployment where randomUUID is unavailable.
  const random = typeof crypto !== "undefined" && typeof crypto.getRandomValues === "function"
    ? [...crypto.getRandomValues(new Uint32Array(2))].join("-") : Math.random().toString(36).slice(2);
  return `${Date.now()}-${++generationSequence}-${random}`;
}
function read(): PendingMap | null {
  try {
    const value = JSON.parse(window.localStorage.getItem(storageKey) || "{}");
    if (!value || Array.isArray(value) || typeof value !== "object") return {};
    return Object.fromEntries(Object.entries(value).filter((entry): entry is [string, Pending] => Boolean(entry[1] && typeof entry[1] === "object" && typeof (entry[1] as Pending).baseline === "string")));
  } catch { return null; }
}
let pending = read() ?? {};
function notify() { revision++; listeners.forEach((listener) => listener()); }
function write() {
  try { window.localStorage.setItem(storageKey, JSON.stringify(pending)); } catch { /* In-memory guard still works. */ }
  notify();
}
if (typeof window !== "undefined") window.addEventListener("storage", (event) => {
  if (event.key === storageKey || event.key === null) { pending = read() ?? pending; notify(); }
});
function subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; }
type StatusFence = Record<string, { generation?: string; settled: boolean }>;
function statusFence(): StatusFence {
  return Object.fromEntries(Object.entries(pending).map(([id, local]) => [id, {
    generation: local.generation,
    settled: !submitting.has(id) && (!local.submitted_at || Date.now() - local.submitted_at > 60_000),
  }]));
}
function reconcile(status: EmailSyncStatus, fence?: StatusFence) {
  pending = read() ?? pending;
  let changed = false;
  for (const mailbox of status.mailboxes) {
    const local = pending[mailbox.id];
    if (!local) continue;
    // Only a GET started after this exact submission settled is authoritative
    // for a replacement/cleared ID. Old props and pre-submission GETs are not.
    const fresh = fence?.[mailbox.id]?.settled && fence[mailbox.id].generation === local.generation;
    const observed = mailbox.refresh_request_id || "";
    if (fresh && mailbox.refresh_pending && observed && local.request !== observed) {
      local.request = observed; changed = true;
    }
    if (mailbox.refresh_pending === false && (fresh || (observed && local.request === observed))) {
      delete pending[mailbox.id]; changed = true;
    }
  }
  if (changed) write();
}

export function useEmailRefresh(status: EmailSyncStatus | null, mailboxId: string) {
  useSyncExternalStore(subscribe, () => revision, () => 0);
  const selected = (status?.mailboxes ?? []).filter((mailbox) => mailboxId ? mailbox.id === mailboxId : mailbox.active_binding && mailbox.intake_enabled);
  const isPending = selected.some((mailbox) => mailbox.refresh_pending === true || Boolean(pending[mailbox.id]));
  useEffect(() => { if (status) reconcile(status); }, [status]);
  useEffect(() => {
    if (!isPending) return;
    const controller = new AbortController();
    const check = async () => {
      const fence = statusFence();
      try { reconcile(await api.emailSyncStatus(controller.signal), fence); } catch { /* Unknown means keep the guard, never resend. */ }
    };
    void check();
    const timer = setInterval(() => void check(), 5000);
    return () => { controller.abort(); clearInterval(timer); };
  }, [isPending]);
  async function refresh(targetMailboxId = mailboxId): Promise<EmailSyncSchedule | null> {
    const selected = (status?.mailboxes ?? []).filter((mailbox) => targetMailboxId ? mailbox.id === targetMailboxId : mailbox.active_binding && mailbox.intake_enabled);
    const ids = selected.map((mailbox) => mailbox.id);
    // Synchronous guard is needed because two clicks may precede React's render.
    pending = read() ?? pending;
    if (!status || !selected.length || selected.some((mailbox) => mailbox.refresh_pending || pending[mailbox.id])) return null;
    const generation = nextGeneration();
    for (const mailbox of selected) { pending[mailbox.id] = { baseline: mailbox.refresh_request_id || "", submitted_at: Date.now(), generation }; submitting.add(mailbox.id); }
    write();
    try {
      const result = await api.syncEmail(targetMailboxId);
      pending = read() ?? pending;
      ids.forEach((id) => { submitting.delete(id); if (pending[id]?.generation === generation) delete pending[id].submitted_at; });
      for (const request of result.refresh_requests ?? []) {
        if (pending[request.mailbox_id]?.generation === generation) pending[request.mailbox_id].request = request.refresh_request_id;
      }
      if (!result.scheduled) for (const id of ids) if (pending[id]?.generation === generation) delete pending[id];
      write();
      // Read after submission instead of accepting an already in-flight false.
      const fence = statusFence();
      try { reconcile(await api.emailSyncStatus(), fence); } catch { /* Status polling will resolve the request. */ }
      return result;
    } catch (reason) {
      pending = read() ?? pending;
      ids.forEach((id) => { submitting.delete(id); if (pending[id]?.generation === generation) delete pending[id].submitted_at; });
      write();
      // Only a definitive client rejection proves no request was accepted.
      if (reason instanceof APIError && reason.status >= 400 && reason.status < 500 && reason.status !== 408) {
        for (const id of ids) if (pending[id]?.generation === generation) delete pending[id]; write();
      } else {
        const fence = statusFence();
        try { reconcile(await api.emailSyncStatus(), fence); } catch { /* Preserve uncertain submission across remounts. */ }
      }
      throw reason;
    }
  }
  return { refresh, pending: isPending };
}
