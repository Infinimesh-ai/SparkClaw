// @vitest-environment jsdom
import { act, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ClientStoreAPI, LocalConversationContent, SchedulePresentation } from './clientStore';
import { useSchedulePresentation } from './useSchedulePresentation';

const action: SchedulePresentation = { schema_version: 1, type: 'schedule.show', action_id: 'request', request_id: 'request',
  local_conversation_id: 'conversation', date_start: '', date_end: '', timezone: 'Asia/Shanghai', expires_at: '2099-01-01T00:00:00Z' };
let host: HTMLDivElement, root: Root;
beforeEach(() => { (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true; host = document.createElement('div'); document.body.append(host); root = createRoot(host); });
afterEach(async () => { await act(async () => root.unmount()); host.remove(); });

function fixture() {
  const claim = vi.fn(async () => ({ action, schedules: [] }));
  const finish = vi.fn(async () => ({ recorded: true as const }));
  const present = vi.fn(); const error = vi.fn();
  const store = { beginSchedulePresentation: vi.fn(async () => ({ view_id: 'view' })), claimSchedulePresentation: claim, finishSchedulePresentation: finish } as unknown as ClientStoreAPI;
  let controller: ReturnType<typeof useSchedulePresentation>;
  function UI({ actions = [], conversation = 'conversation', scope = 'owner' }: {
    actions?: LocalConversationContent['client_actions']; conversation?: string; scope?: string;
  }) {
    const [visible, setVisible] = useState(false);
    controller = useSchedulePresentation({ store, actions, conversation, scope, expectedScope: 'bound-scope', visible, active: !visible,
      present: (value, rows) => { present(value, rows); setVisible(true); }, onError: error });
    return visible ? <section className="schedulePage">Original schedules</section> : <div className="chatColumn">Original chat</div>;
  }
  return { claim, finish, present, error, UI, arm: (id = 'request') => controller.arm(id, 'conversation', 'bound-scope'), dismiss: () => controller.dismiss() };
}

describe('backend-directed local presentation', () => {
  it('opens only after native data is read and sends shown after the original page renders', async () => {
    const f = fixture();
    f.finish.mockImplementation(async () => { expect(host.querySelector('.schedulePage')).not.toBeNull(); return { recorded: true }; });
    await act(async () => root.render(<f.UI />)); f.arm();
    await act(async () => root.render(<f.UI actions={[{ ...action, state: 'pending' }]} />));
    expect(f.claim).toHaveBeenCalledWith('request', 'conversation', 'bound-scope', 'view');
    expect(f.present).toHaveBeenCalledOnce();
    expect(f.finish).toHaveBeenCalledWith('request', 'shown', 'bound-scope', 'view');
    await act(async () => root.render(<f.UI actions={[{ ...action, state: 'pending' }]} />));
    expect(f.present).toHaveBeenCalledOnce();
  });

  it('history loading and explicit navigation cannot arm or repeat a presentation', async () => {
    const f = fixture();
    await act(async () => root.render(<f.UI actions={[{ ...action, state: 'pending' }]} />));
    expect(f.claim).not.toHaveBeenCalled(); expect(f.present).not.toHaveBeenCalled();
    expect(f.finish).toHaveBeenCalledWith('request', 'skipped', 'bound-scope', 'view');
    const g = fixture(); await act(async () => root.render(<g.UI />)); g.arm(); g.dismiss();
    await act(async () => root.render(<g.UI actions={[{ ...action, state: 'pending' }]} />));
    expect(g.claim).not.toHaveBeenCalled(); expect(g.present).not.toHaveBeenCalled();
  });

  it('a delayed local read cannot navigate after the user changes conversation', async () => {
    const f = fixture(); let resolve: (value: { action: SchedulePresentation; schedules: [] }) => void = () => {};
    f.claim.mockImplementation(() => new Promise(done => { resolve = done; }));
    await act(async () => root.render(<f.UI />)); f.arm();
    await act(async () => root.render(<f.UI actions={[{ ...action, state: 'pending' }]} />));
    await act(async () => root.render(<f.UI conversation="other" />));
    await act(async () => resolve({ action, schedules: [] }));
    expect(f.present).not.toHaveBeenCalled();
    expect(f.finish).toHaveBeenCalledWith('request', 'skipped', 'bound-scope', 'view');
  });

  it('a failed local read reports failed and retains chat', async () => {
    const f = fixture(); f.claim.mockRejectedValue(new Error('local read unavailable'));
    await act(async () => root.render(<f.UI />)); f.arm();
    await act(async () => root.render(<f.UI actions={[{ ...action, state: 'pending' }]} />));
    expect(f.present).not.toHaveBeenCalled(); expect(f.error).toHaveBeenCalledOnce();
    expect(f.finish).toHaveBeenCalledWith('request', 'failed', 'bound-scope', 'view');
    expect(host.querySelector('.chatColumn')).not.toBeNull();
  });
  it('two pending actions cannot overwrite an unreceipted claim in one render batch', async () => {
    const f = fixture();
    await act(async () => root.render(<f.UI />)); f.arm(); f.arm('second');
    await act(async () => root.render(<f.UI actions={[{ ...action, state: 'pending' }, { ...action, action_id: 'second', request_id: 'second', state: 'pending' }]} />));
    expect(f.claim).toHaveBeenCalledTimes(1);
    expect(f.present).toHaveBeenCalledTimes(1);
    expect(f.finish).toHaveBeenCalledWith('request', 'shown', 'bound-scope', 'view');
    expect(f.finish).toHaveBeenCalledWith('second', 'skipped', 'bound-scope', 'view');
  });

});
