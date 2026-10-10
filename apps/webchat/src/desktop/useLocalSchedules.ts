import { useCallback, useEffect, useRef, useState } from 'react';
import type { Schedule } from '../api/types';
import type { ScheduleCreateResult, ScheduleEditDraft } from '../components/schedules';
import type { ClientStoreAPI } from './clientStore';
import { clientTimezone } from '../lib/timezone';

// Adapt the original schedule presentation to the local repository. Scope and
// version checks stay in the native capability, including edits and cancellation.
export function useLocalSchedules(store: ClientStoreAPI, scope: string, visible: boolean, onError: (error: unknown) => void) {
  const [schedules, setSchedules] = useState<Schedule[]>([]);
  const [loading, setLoading] = useState(false);
  const [busyID, setBusyID] = useState('');
  const generation = useRef(0);
  const scopeRef = useRef(scope); scopeRef.current = scope;
  const refresh = useCallback(async () => {
    const current = scopeRef.current, revision = ++generation.current;
    setLoading(true);
    try {
      const rows = await store.listSchedules();
      if (current === scopeRef.current && revision === generation.current) setSchedules(rows);
    } finally { if (current === scopeRef.current && revision === generation.current) setLoading(false); }
  }, [store]);
  useEffect(() => { ++generation.current; setSchedules([]); setBusyID(''); }, [scope]);
  useEffect(() => {
    if (!visible) return;
    void refresh().catch(onError);
    return store.onChange?.(() => { void refresh().catch(onError); });
  }, [store, scope, visible, refresh, onError]);
  const pending = useRef(false);
  async function mutate(id: string, operation: () => Promise<unknown>) {
    if (pending.current) throw new Error('A schedule operation is already in progress.');
    const current = scopeRef.current;
    pending.current = true; setBusyID(id);
    try { await operation(); if (current === scopeRef.current) await refresh(); }
    catch (error) { if (current === scopeRef.current) onError(error); throw error; }
    finally { pending.current = false; if (current === scopeRef.current) setBusyID(''); }
  }
  return { schedules, loading, busyID, refresh,
    create: async (request: string): Promise<ScheduleCreateResult> => {
      try { await mutate('create', () => store.createScheduleRequest(request, clientTimezone())); return { success: true, message: '' }; }
      catch (error) { return { success: false, message: error instanceof Error ? error.message : 'Unable to create the schedule.' }; }
    },
    edit: (schedule: Schedule, draft: ScheduleEditDraft) => mutate(schedule.id, () => store.editSchedule(schedule.id, schedule.updated_at, draft)),
    cancel: (schedule: Schedule) => mutate(schedule.id, () => store.scheduleCancel(schedule.id)),
    runNow: (schedule: Schedule) => mutate(schedule.id, () => store.scheduleRunNow(schedule.id)),
    check: () => mutate('refresh', async () => { for (const schedule of schedules) if (['sending', 'submitted'].includes(schedule.status)) await store.scheduleCheck(schedule.id); }),
  };
}
