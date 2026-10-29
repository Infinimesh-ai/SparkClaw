// @vitest-environment jsdom
import { act, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { LocalFileTree } from './LocalFileTree';
import type { LocalFile, LocalFilesBridge, LocalReadingState } from './localFiles';

const dir = (name: string): LocalFile => ({ ref: name, locator: `/${name}`, name, kind: 'directory', size: 0, version: 'v1', displayPath: `/${name}` });
const child = (parent: LocalFile, name: string, kind: LocalFile['kind'] = 'file'): LocalFile => ({ ...parent, ref: name, name, kind, locator: `${parent.locator}/${name}`, displayPath: `${parent.locator}/${name}` });
beforeEach(() => { vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true); vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} }); });
afterEach(() => { vi.unstubAllGlobals(); document.body.replaceChildren(); });
async function fixture(roots: LocalFile[], overrides: Partial<LocalFilesBridge>) {
  const files = { watch: vi.fn(async () => ({ watching: true })), unwatch: vi.fn(async () => ({})), release: vi.fn(async () => ({})), onChange: () => () => {}, ...overrides } as LocalFilesBridge;
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host);
  function Harness() {
    const [state, setState] = useState<LocalReadingState>({ tabs: [], active: 'files', expanded: [], scrollTop: 0, showHidden: false, filter: '' });
    return <LocalFileTree roots={roots} files={files} visible zh={false} state={state} onState={changes => setState(previous => {
      const next = { ...previous, ...changes }; return JSON.stringify(next) === JSON.stringify(previous) ? previous : next;
    })} onOpen={() => {}} />;
  }
  await act(async () => root.render(<Harness />));
  const click = (text: string) => act(async () => [...host.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent === text)!.click());
  return { files, host, click, close: () => act(async () => root.unmount()) };
}

it('revisits existing pages before continuing enumeration and terminates a short final page', async () => {
  const parent = dir('root'), entries = Array.from({ length: 5 }, (_, i) => child(parent, `file${i}`));
  const list = vi.fn(async (options: Parameters<LocalFilesBridge['list']>[0]) => {
    const page = options.page ?? 0;
    return { entries: entries.slice(page * 2, page * 2 + 2), page, pageSize: 2, totalLoaded: 5, complete: Boolean(options.cursor), cursor: options.cursor ? null : 'remaining', listing: 'a' };
  });
  const f = await fixture([parent], { list });
  try {
    await f.click('root'); await f.click('Continue / next page');
    expect(list).toHaveBeenLastCalledWith({ ref: 'root', page: 1, showHidden: false, filter: '' });
    await f.click('Continue / next page'); expect(list).toHaveBeenLastCalledWith({ ref: 'root', page: 2, showHidden: false, filter: '' });
    await f.click('Continue / next page'); expect(list).toHaveBeenLastCalledWith({ ref: 'root', page: 2, cursor: 'remaining', showHidden: false, filter: '' });
    expect(f.host.textContent).not.toContain('Continue / next page'); expect(f.host.textContent).toContain('file4');
    await f.click('Previous page'); expect(f.host.textContent).toContain('file2');
  } finally { await f.close(); }
});

it('following a link checks only its actual ancestors and collapsing releases descendants', async () => {
  const a = dir('a'), b = dir('b'), link = child(a, 'link-to-b', 'link');
  const followLink = vi.fn(async () => ({ ...link, kind: 'directory' as const, ref: 'followed' }));
  const list = vi.fn(async ({ ref }: { ref: string }) => ({ entries: ref === 'a' ? [link] : [], page: 0, pageSize: 128, totalLoaded: ref === 'a' ? 1 : 0, complete: true, listing: ref }));
  const f = await fixture([a, b], { list, followLink });
  try {
    await f.click('a'); await f.click('b'); await f.click('link-to-b');
    expect(followLink).toHaveBeenCalledWith({ ref: 'link-to-b', ancestors: ['/a'] });
    await f.click('a'); expect(f.files.release).toHaveBeenCalledWith('a'); expect(f.files.release).toHaveBeenCalledWith('followed'); expect(f.files.release).not.toHaveBeenCalledWith('b');
  } finally { await f.close(); }
});

it('filtering one selected directory preserves another directory continuation conditions', async () => {
  const a = dir('a'), b = dir('b'), leaf = child(a, 'leaf');
  const list = vi.fn(async ({ ref, filter }: Parameters<LocalFilesBridge['list']>[0]) => ({ entries: ref === 'a' && !filter ? [leaf] : [], page: 0, pageSize: 128, totalLoaded: ref === 'a' && !filter ? 1 : 0, complete: Boolean(filter), cursor: filter ? null : `${ref}-cursor`, listing: ref }));
  const f = await fixture([a, b], { list });
  try {
    await f.click('a'); await f.click('b'); await f.click('leaf');
    const input = f.host.querySelector<HTMLInputElement>('.localFilesFilter input')!;
    await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'needle'); input.dispatchEvent(new Event('input', { bubbles: true })); });
    expect(list).toHaveBeenLastCalledWith({ ref: 'a', showHidden: false, filter: 'needle', refresh: true });
    await f.click('Continue / next page'); expect(list).toHaveBeenLastCalledWith({ ref: 'b', showHidden: false, filter: '', cursor: 'b-cursor', page: 0 });
  } finally { await f.close(); }
});
