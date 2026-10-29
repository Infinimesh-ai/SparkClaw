import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { ChevronDown, ChevronRight, File, Folder, Link2, RefreshCw } from "lucide-react";
import { localFileError, type LocalFile, type LocalFilesBridge, type LocalListing, type LocalReadingState } from "./localFiles";

const ROW_HEIGHT = 30;
const MAX_EXPANDED = 16;
type Directory = { file: LocalFile; ancestors: string[]; conditions: { showHidden: boolean; filter: string }; listing?: LocalListing; busy?: boolean; error?: string; changed?: boolean; sequence: number; refreshRequested?: boolean };
type Row = { key: string; ancestors: string[]; depth: number; file?: LocalFile; parent?: LocalFile; action?: "previous" | "next" | "status"; text?: string; informational?: boolean };
type Props = {
  files: LocalFilesBridge; roots: LocalFile[]; visible: boolean; zh: boolean; state: LocalReadingState;
  onState: (changes: Partial<LocalReadingState>) => void; onOpen: (file: LocalFile) => void;
};
function selectedDirectory(entries: Map<string, Directory>, selected?: string) {
  return entries.get(selected || "") || [...entries.values()].find(node => node.listing?.entries.some(file => file.locator === selected)) || [...entries.values()].at(-1);
}

// Each directory retains one bounded page. Other pages live in the main
// process's private page store, so scrolling cannot grow a renderer-wide array.
export function LocalFileTree({ files, roots, visible, zh, state, onState, onOpen }: Props) {
  const [directories, setDirectories] = useState<Map<string, Directory>>(new Map());
  const current = useRef(directories); current.current = directories;
  const alive = useRef(true);
  const sequence = useRef(0);
  const viewport = useRef<HTMLDivElement>(null);
  const [scroll, setScroll] = useState(state.scrollTop);
  const [height, setHeight] = useState(540);
  const [error, setError] = useState("");
  const selectedRef = useRef(state.selected); selectedRef.current = state.selected;
  const conditions = useRef({ showHidden: state.showHidden, filter: state.filter });
  conditions.current = { showHidden: state.showHidden, filter: state.filter };
  const expand = useCallback(async (file: LocalFile, options: { refresh?: boolean; page?: number; cursor?: string } = {}, ancestors?: string[]): Promise<void> => {
    const previous = current.current.get(file.locator);
    if (previous?.busy) {
      if (options.refresh) { current.current = new Map(current.current).set(file.locator, { ...previous, refreshRequested: true }); setDirectories(current.current); }
      return;
    }
    if (!previous && current.current.size >= MAX_EXPANDED) {
      setError(zh ? "请先收起一个目录，再展开其他目录。" : "Collapse a directory before expanding another."); return;
    }
    const request = ++sequence.current;
    const requestConditions = !previous || options.refresh ? { ...conditions.current } : previous.conditions;
    const next: Directory = { ...previous, file, ancestors: ancestors ?? previous?.ancestors ?? [], conditions: requestConditions, busy: true, error: undefined, changed: false, sequence: request, refreshRequested: false };
    current.current = new Map(current.current).set(file.locator, next); setDirectories(current.current);
    try {
      const listing = await files.list({ ref: file.ref, ...requestConditions, ...options });
      if (!alive.current || current.current.get(file.locator)?.sequence !== request) return;
      current.current = new Map(current.current).set(file.locator, { ...next, listing, refreshRequested: current.current.get(file.locator)?.refreshRequested, changed: listing.changed, busy: false }); setDirectories(current.current);
      const watching = await files.watch(file.ref).catch(() => ({ watching: false })) as { watching?: boolean };
      if (alive.current && current.current.get(file.locator)?.sequence === request && watching?.watching === false) setError(zh ? "目录监测不可用，可手动刷新。" : "Directory watching is unavailable. Refresh manually.");
    } catch (reason) {
      if (!alive.current || current.current.get(file.locator)?.sequence !== request) return;
      current.current = new Map(current.current).set(file.locator, { ...next, refreshRequested: current.current.get(file.locator)?.refreshRequested, busy: false, error: localFileError(reason, zh) }); setDirectories(current.current);
    } finally {
      if (alive.current && current.current.get(file.locator)?.sequence === request && current.current.get(file.locator)?.refreshRequested) void expand(file, { refresh: true }, ancestors);
    }
  }, [files, zh]);
  const collapse = useCallback((file: LocalFile) => {
    // Releasing a directory invalidates its enumeration and its continuation.
    const next = new Map(current.current);
    for (const [locator, entry] of next) if (locator === file.locator || entry.ancestors.includes(file.locator)) {
      next.delete(locator); void files.unwatch(entry.file.ref).catch(() => {}); void files.release(entry.file.ref).catch(() => {});
    }
    current.current = next; setDirectories(next);
  }, [files]);
  useEffect(() => {
    alive.current = true;
    return () => { alive.current = false; for (const entry of current.current.values()) { void files.unwatch(entry.file.ref).catch(() => {}); void files.release(entry.file.ref).catch(() => {}); } };
  }, [files]);
  const restored = useRef(false);
  useEffect(() => {
    if (restored.current || !roots.length || !visible) return;
    restored.current = true;
    void (async () => {
      const reachable = state.expanded.filter(locator => roots.some(root => locator === root.locator || locator.startsWith(root.locator === '/' ? '/' : `${root.locator}/`)));
      for (const locator of reachable.slice(0, MAX_EXPANDED)) {
        try { const file = await files.resolve(locator); if (!alive.current) return; await expand(file, {}, [...current.current.keys()].filter(parent => locator.startsWith(parent === "/" ? "/" : `${parent}/`))); } catch { /* Stale locations remain absent until explicitly reopened. */ }
      }
    })();
  }, [roots, files, expand, state.expanded, visible]);
  const lastConditions = useRef(`${state.showHidden}:${state.filter}`);
  useEffect(() => {
    const key = `${state.showHidden}:${state.filter}`;
    if (lastConditions.current === key) return;
    lastConditions.current = key;
    // Name filtering is scoped to the selected directory, never the disk.
    const target = selectedDirectory(current.current, selectedRef.current);
    if (target) void expand(target.file, { refresh: true });
  }, [state.showHidden, state.filter, expand]);
  useEffect(() => files.onChange((event) => {
    if (event.kind === "revoked") { sequence.current++; current.current = new Map(); setDirectories(current.current); return; }
    const refs = new Set(event.refs);
    setDirectories(previous => new Map([...previous].map(([key, value]) => [key, refs.has(value.file.ref) ? { ...value, changed: true } : value])));
  }), [files]);
  useEffect(() => { onState({ expanded: [...directories.keys()] }); }, [directories, onState]);
  useEffect(() => {
    const host = viewport.current; if (!host) return;
    const observer = new ResizeObserver(() => setHeight(host.clientHeight || 540)); observer.observe(host);
    host.scrollTop = state.scrollTop;
    return () => observer.disconnect();
  // Restoration occurs once; subsequent scroll is controlled by the user.
  }, []);
  const rows = useMemo(() => {
    const result: Row[] = [];
    const visit = (file: LocalFile, depth: number, ancestors: Set<string>) => {
      const rowKey = JSON.stringify([...ancestors, file.locator]);
      result.push({ key: rowKey, file, depth, ancestors: [...ancestors] });
      const node = directories.get(file.locator); if (!node || ancestors.has(file.locator)) return;
      const next = new Set(ancestors).add(file.locator);
      if ((node.listing?.page ?? 0) > 0) result.push({ key: `${rowKey}:previous`, ancestors: [...next], parent: file, action: "previous", depth: depth + 1 });
      for (const entry of node.listing?.entries || []) visit(entry, depth + 1, next);
      const partial = node.listing && !node.listing.complete;
      if (node.error || node.busy || node.changed || node.listing?.incomplete || partial) result.push({ key: `${rowKey}:status`, ancestors: [...next], parent: file, action: "status", depth: depth + 1,
        informational: node.busy || !node.error && !node.changed && !node.listing?.incomplete,
        text: node.error || (node.busy ? (zh ? "正在读取…" : "Loading…") : node.changed ? (zh ? "目录有变化，刷新查看" : "Directory changed · refresh") : node.listing?.incomplete ? localFileError({ code: node.listing.incomplete }, zh) : (zh ? "列表尚未读完" : "Listing is not complete")) });
      if (!node.busy && node.listing && (node.listing.cursor || ((node.listing.page + 1) * (node.listing.pageSize ?? 128) < (node.listing.totalLoaded ?? node.listing.entries.length)))) {
        result.push({ key: `${rowKey}:next`, ancestors: [...next], parent: file, action: "next", depth: depth + 1 });
      }
    };
    roots.forEach(file => visit(file, 1, new Set())); return result;
  }, [roots, directories, zh]);
  const previousRows = useRef<Row[]>([]);
  useLayoutEffect(() => {
    const host = viewport.current; if (!host) return;
    const old = previousRows.current, anchorIndex = Math.floor(host.scrollTop / ROW_HEIGHT), anchor = old[anchorIndex];
    const replacement = anchor ? rows.findIndex(row => row.key === anchor.key) : -1;
    const desired = replacement < 0 ? host.scrollTop : replacement * ROW_HEIGHT + host.scrollTop % ROW_HEIGHT;
    const clamped = Math.max(0, Math.min(desired, Math.max(0, rows.length * ROW_HEIGHT - height)));
    previousRows.current = rows;
    if (host.scrollTop !== clamped) { host.scrollTop = clamped; setScroll(clamped); onState({ scrollTop: clamped }); }
  }, [rows, height, onState]);
  const activate = async (row: Row) => {
    if (row.parent) {
      const node = current.current.get(row.parent.locator);
      if (row.action === "status") return row.informational ? undefined : expand(row.parent, { refresh: true });
      const listing = node?.listing; if (!listing) return;
      if (row.action === "previous") return expand(row.parent, { page: Math.max(0, listing.page - 1) });
      const nextPage = listing.page + 1, pageSize = listing.pageSize ?? 128, loaded = listing.totalLoaded ?? listing.entries.length;
      if (nextPage * pageSize < loaded) return expand(row.parent, { page: nextPage });
      if (listing.cursor) return expand(row.parent, { cursor: listing.cursor, page: Math.floor(loaded / pageSize) });
      return;
    }
    const file = row.file; if (!file) return;
    onState({ selected: file.locator }); setError("");
    if (file.unavailable) { setError(localFileError({ code: file.unavailable }, zh)); return; }
    if (file.kind === "directory" || file.kind === "bundle") {
      if (current.current.has(file.locator)) collapse(file); else await expand(file, {}, row.ancestors); return;
    }
    if (file.kind === "link") {
      try {
        if (current.current.has(file.locator)) { collapse(file); return; }
        const target = await files.followLink({ ref: file.ref, ancestors: row.ancestors });
        if (target.kind === "directory" || target.kind === "bundle") await expand({ ...target, locator: file.locator, name: file.name }, {}, row.ancestors); else onOpen(target);
      } catch (reason) { setError(localFileError(reason, zh)); }
      return;
    }
    onOpen(file);
  };
  const focusRow = (index: number) => {
    const row = rows[Math.max(0, Math.min(rows.length - 1, index))]; if (!row) return;
    onState({ selected: row.file?.locator ?? row.parent?.locator });
    const top = Math.max(0, index) * ROW_HEIGHT;
    if (viewport.current && (top < scroll || top > scroll + height - ROW_HEIGHT)) { viewport.current.scrollTop = top; setScroll(top); }
    requestAnimationFrame(() => viewport.current?.querySelector<HTMLButtonElement>(`[data-row-index="${Math.max(0, Math.min(rows.length - 1, index))}"]`)?.focus());
  };
  const keyDown = (event: KeyboardEvent, row: Row, index: number) => {
    if (event.nativeEvent.isComposing || event.altKey || event.ctrlKey || event.metaKey) return;
    if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
      event.preventDefault(); focusRow(event.key === "Home" ? 0 : event.key === "End" ? rows.length - 1 : index + (event.key === "ArrowDown" ? 1 : -1));
    } else if (event.key === "ArrowRight" && row.file && ["directory", "bundle"].includes(row.file.kind)) {
      event.preventDefault(); if (!directories.has(row.file.locator)) void expand(row.file, {}, row.ancestors); else focusRow(index + 1);
    } else if (event.key === "ArrowLeft") {
      event.preventDefault(); if (row.file && directories.has(row.file.locator)) collapse(row.file);
      else { let parentIndex = index - 1; while (parentIndex >= 0 && rows[parentIndex].depth >= row.depth) parentIndex--; if (parentIndex >= 0) focusRow(parentIndex); }
    }
  };
  const start = Math.max(0, Math.floor(scroll / ROW_HEIGHT) - 6), end = Math.min(rows.length, start + Math.min(48, Math.ceil(height / ROW_HEIGHT) + 12));
  const selectedFolder = selectedDirectory(directories, state.selected);
  return <section className="localFileTree" hidden={!visible}>
    <div className="localFilesTreeTools">
      <strong>{zh ? "此电脑" : "This Computer"}</strong>
      <button type="button" className="iconButton" title={zh ? "刷新当前目录" : "Refresh current directory"} aria-label={zh ? "刷新当前目录" : "Refresh current directory"}
        disabled={!selectedFolder || selectedFolder.busy} onClick={() => selectedFolder && void expand(selectedFolder.file, { refresh: true })}><RefreshCw size={15} /></button>
      <label><input type="checkbox" checked={state.showHidden} onChange={event => onState({ showHidden: event.target.checked })} />{zh ? "隐藏文件" : "Hidden files"}</label>
    </div>
    <label className="localFilesFilter">{zh ? "当前目录名称筛选" : "Filter names in current directory"}
      <input value={state.filter} maxLength={120} onChange={event => onState({ filter: event.target.value })} placeholder={selectedFolder?.file.name || (zh ? "先展开目录" : "Expand a directory")} />
    </label>
    {error && <p role="alert" className="localFilesError">{error}</p>}
    <div className="localFileTreeViewport" ref={viewport} role="tree" aria-label={zh ? "此电脑" : "This Computer"}
      onScroll={event => { setScroll(event.currentTarget.scrollTop); onState({ scrollTop: event.currentTarget.scrollTop }); }}>
      <div style={{ height: rows.length * ROW_HEIGHT, position: "relative" }}>
        {rows.slice(start, end).map((row, offset) => {
          const file = row.file, directory = file && ["directory", "bundle"].includes(file.kind), expanded = file && directories.has(file.locator);
          const selected = state.selected === (row.file?.locator ?? row.key);
          return <button type="button" role="treeitem" className={`localFileTreeRow ${selected ? "selected" : ""}`}
            key={row.key} data-row-index={start + offset} style={{ top: (start + offset) * ROW_HEIGHT, paddingLeft: row.depth * 16 }}
            aria-level={row.depth} aria-expanded={directory ? Boolean(expanded) : undefined} aria-selected={selected} aria-disabled={row.informational || undefined}
            tabIndex={selected || !state.selected && start + offset === 0 ? 0 : -1} title={file?.displayPath}
            onKeyDown={event => keyDown(event, row, start + offset)} onClick={() => void activate(row)}>
            {directory ? expanded ? <ChevronDown size={13} /> : <ChevronRight size={13} /> : <span className="localFileTreeIndent" />}
            {file && (directory ? <Folder size={15} /> : file.kind === "link" ? <Link2 size={15} /> : <File size={15} />)}
            <span>{file?.name || row.text || (row.action === "previous" ? (zh ? "上一页" : "Previous page") : (zh ? "继续读取 / 下一页" : "Continue / next page"))}</span>
            {file?.unavailable && <span aria-label={zh ? "不可用" : "Unavailable"}> · </span>}
          </button>;
        })}
      </div>
    </div>
  </section>;
}
