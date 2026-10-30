import { useEffect, useRef, useState } from 'react';
import type { Schedule } from '../api/types';
import type { ClientStoreAPI, LocalConversationContent, SchedulePresentation, SchedulePresentationReceipt } from './clientStore';

type Anchor = { scope: string; conversation: string; expectedScope: string };
type Props = Anchor & {
  store: ClientStoreAPI; actions: LocalConversationContent['client_actions']; visible: boolean; active: boolean;
  present: (action: SchedulePresentation, rows: Schedule[]) => void;
  onError: (error: unknown) => void;
};

// Only a submission made in this live conversation may navigate. Loading old
// history, polling, remounting, or replaying a delivery cannot arm an action.
export function useSchedulePresentation(props: Props) {
  const current = useRef(props); current.current = props;
  const armed = useRef(new Map<string, Anchor>());
  const processing = useRef(new Set<string>());
  const claiming = useRef(false);
  const [claimRevision, setClaimRevision] = useState(0);
  const releaseClaim = () => { claiming.current = false; setClaimRevision(value => value + 1); };
  const [view, setView] = useState<{ scope: string; expectedScope: string; id: string } | null>(null);
  const [displaying, setDisplaying] = useState<(Anchor & { requestID: string; viewID: string }) | null>(null);
  const same = (anchor: Anchor) => current.current.scope === anchor.scope && current.current.conversation === anchor.conversation;

  useEffect(() => {
    if (!props.store.beginSchedulePresentation || !props.expectedScope) return;
    let active = true;
    setView(null);
    void props.store.beginSchedulePresentation(props.expectedScope).then(value => {
      if (active) setView({ scope: props.scope, expectedScope: props.expectedScope, id: value.view_id });
    }).catch(error => { if (active) current.current.onError(error); });
    return () => { active = false; };
  }, [props.store, props.scope, props.expectedScope]);

  useEffect(() => {
    for (const [id, anchor] of armed.current) if (!same(anchor) || !props.active) armed.current.delete(id);
  }, [props.scope, props.conversation, props.active]);

  useEffect(() => {
    const { store, actions, expectedScope } = props;
    if (!store.claimSchedulePresentation || !store.finishSchedulePresentation || !view || view.scope !== props.scope || view.expectedScope !== expectedScope) return;
    for (const action of actions ?? []) {
      const key = `${props.scope}:${action.request_id}`;
      if (action.state !== 'pending' || processing.current.has(key)) continue;
      const anchor = armed.current.get(action.request_id);
      if (anchor && same(anchor) && props.active && claiming.current) continue;
      processing.current.add(key);
      const receipt = (status: SchedulePresentationReceipt) => store.finishSchedulePresentation!(action.request_id, status, expectedScope, view.id)
        .catch(error => { processing.current.delete(key); throw error; });
      if (!anchor || !same(anchor) || !props.active) {
        void receipt('skipped').catch(error => { if (current.current.scope === props.scope) current.current.onError(error); });
        continue;
      }
      claiming.current = true;
      void (async () => {
        let presented = false;
        try {
          const claimed = await store.claimSchedulePresentation!(action.request_id, anchor.conversation, anchor.expectedScope, view.id);
          if (!claimed) return;
          if (!same(anchor) || !current.current.active || !armed.current.has(action.request_id)) { await receipt('skipped'); return; }
          current.current.present(claimed.action, claimed.schedules);
          setDisplaying({ ...anchor, requestID: action.request_id, viewID: view.id });
          presented = true;
        } catch (error) {
          // The main process records local read failures; a failed renderer
          // transition is recorded here as well. An identical receipt is safe.
          await receipt('failed').catch(() => undefined);
          if (same(anchor)) current.current.onError(error);
        } finally { armed.current.delete(action.request_id); if (!presented) releaseClaim(); }
      })();
    }
  }, [props.actions, props.store, props.scope, props.conversation, props.expectedScope, props.active, view, claimRevision]);

  useEffect(() => {
    if (!displaying) return;
    // Effects run after the existing schedule page and its local data commit.
    const shown = same(displaying) && props.visible;
    setDisplaying(null);
    void props.store.finishSchedulePresentation?.(displaying.requestID, shown ? 'shown' : 'skipped', displaying.expectedScope, displaying.viewID)
      .catch(error => { if (same(displaying)) current.current.onError(error); }).finally(releaseClaim);
  }, [displaying, props.visible, props.store, props.scope, props.conversation]);

  return {
    arm: (requestID: string, conversation: string, expectedScope: string) => {
      if (props.store.claimSchedulePresentation && props.store.finishSchedulePresentation) {
        armed.current.set(requestID, { scope: current.current.scope, conversation, expectedScope });
      }
    },
    dismiss: () => armed.current.clear(),
  };
}
