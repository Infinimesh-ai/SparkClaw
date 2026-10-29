import { useCallback, useEffect, useRef, useState } from "react";
import { Folder, Maximize2, Minimize2, PanelRightClose, RefreshCw, X } from "lucide-react";
import { ClientTitlebarBrowser } from "../components/clientWindow";
import type { Language } from "../i18n";
import { LocalFileTree } from "./LocalFileTree";
import { LocalDocumentPreview } from "./LocalDocumentPreview";
import { localFileError, type LocalFile, type LocalFilesBridge, type LocalReadingState, type ReadingPosition } from "./localFiles";
import "./local-files.css";

const emptyState = (): LocalReadingState => ({ tabs: [], active: "files", expanded: [], scrollTop: 0, showHidden: false, filter: "" });
type Tab = { file: LocalFile; changed?: boolean; unavailable?: string };
type Props = { files: LocalFilesBridge; open: boolean; visible: boolean; language: Language; wide: boolean; onWideChange: (wide: boolean) => void; onCollapse: () => void; conversationInFront?: boolean; onShowFiles: () => void };
export function LocalFilesPanel({ files, open, visible, language, wide, onWideChange, onCollapse, conversationInFront, onShowFiles }: Props) {
  const zh = language === "zh";
  const [roots, setRoots] = useState<LocalFile[]>([]);
  const [tabs, setTabs] = useState<Tab[]>([]);
  const [state, setState] = useState<LocalReadingState>(emptyState);
  const [generation, setGeneration] = useState("");
  const [error, setError] = useState("");
  const [loaded, setLoaded] = useState(false);
  const panelRef = useRef<HTMLElement>(null);
  const alive = useRef(true);
  const epoch = useRef(0), generationRef = useRef("");
  const pendingOpen = useRef(new Map<string, number>()), operation = useRef(0);
  const tabRef = useRef(tabs); tabRef.current = tabs;
  const updateState = useCallback((changes: Partial<LocalReadingState>) => setState(previous => {
    if (Object.keys(changes).every(key => JSON.stringify(previous[key as keyof LocalReadingState]) === JSON.stringify(changes[key as keyof LocalReadingState]))) return previous;
    return { ...previous, ...changes };
  }), []);
  useEffect(() => {
    alive.current = true; let cancelled = false; const started = epoch.current;
    void Promise.all([files.roots(), files.loadState()]).then(async ([catalog, saved]) => {
      if (cancelled || started !== epoch.current) return;
      if (catalog.generation !== saved.generation) throw Object.assign(new Error("Reading session changed"), { code: "revoked" });
      setRoots(catalog.roots); setGeneration(catalog.generation); generationRef.current = catalog.generation;
      const restored = saved.state || emptyState(); const next: Tab[] = [];
      for (const record of restored.tabs.slice(0, 12)) {
        try { const file = await files.resolve(record.locator); if (cancelled || started !== epoch.current) return; if (file.kind === "file" && !file.unavailable) { next.push({ file }); await files.watch(file.ref).catch(() => {}); } } catch { /* Do not revive unavailable old content. */ }
      }
      if (cancelled || started !== epoch.current) return;
      setTabs(next); setState({ ...emptyState(), ...restored, tabs: restored.tabs.filter(record => next.some(tab => tab.file.locator === record.locator)),
        active: restored.active === "files" || next.some(tab => tab.file.locator === restored.active) ? restored.active : "files" });
      setLoaded(true);
    }).catch(reason => { if (!cancelled && started === epoch.current) setError(localFileError(reason, zh)); });
    return () => { cancelled = true; alive.current = false; epoch.current++; for (const tab of tabRef.current) { void files.unwatch(tab.file.ref).catch(() => {}); void files.release(tab.file.ref).catch(() => {}); } };
  // Language changes translate the existing view; they must not reload saved tabs.
  }, [files]);
  useEffect(() => { void files.setVisible(visible).catch(reason => { if (visible && alive.current) setError(localFileError(reason, zh)); }); return () => { void files.setVisible(false).catch(() => {}); }; }, [files, visible, zh]);
  useEffect(() => {
    if (!loaded) return;
    const timer = window.setTimeout(() => { void files.saveState(state).catch(reason => { if (alive.current) setError(localFileError(reason, zh)); }); }, 300);
    return () => window.clearTimeout(timer);
  }, [state, loaded, files, zh]);
  const revalidate = useCallback(async (tab: Tab) => {
    const started = epoch.current;
    try {
      const fresh = await files.stat(tab.file.ref);
      if (!alive.current || started !== epoch.current) return;
      setTabs(previous => previous.map(value => value.file.ref === tab.file.ref && value.file.version === tab.file.version ? { ...value, changed: fresh.version !== tab.file.version } : value));
    } catch (reason) {
      if (alive.current && started === epoch.current) setTabs(previous => previous.map(value => value.file.ref === tab.file.ref && value.file.version === tab.file.version ? { ...value, unavailable: localFileError(reason, zh) } : value));
    }
  }, [files, zh]);
  useEffect(() => files.onChange(event => {
    if (event.kind === "revoked") { epoch.current++; pendingOpen.current.clear(); generationRef.current = event.generation; tabRef.current = []; setGeneration(event.generation); setRoots([]); setTabs([]); setState(emptyState()); setLoaded(false); return; }
    if (event.generation !== generationRef.current) return;
    for (const tab of tabRef.current) if (event.refs?.includes(tab.file.ref)) void revalidate(tab);
  }), [files, revalidate]);
  useEffect(() => { if (visible) for (const tab of tabRef.current) void revalidate(tab); }, [visible, revalidate]);
  const openFile = useCallback(async (entry: LocalFile) => {
    const started = epoch.current, request = ++operation.current;
    try {
      if (entry.kind !== "file") throw new Error(zh ? "此项目不支持内容预览。" : "Content preview is unsupported for this entry.");
      const existing = tabRef.current.find(tab => tab.file.locator === entry.locator);
      if (existing) { updateState({ active: entry.locator }); return; }
      if (pendingOpen.current.has(entry.locator)) return;
      if (tabRef.current.length + pendingOpen.current.size >= 12) throw new Error(zh ? "请先关闭一个文件标签页。" : "Close a document tab before opening another.");
      pendingOpen.current.set(entry.locator, request);
      const file = await files.stat(entry.ref); if (!alive.current || started !== epoch.current) return;
      setTabs(previous => previous.some(tab => tab.file.locator === file.locator) ? previous : [...previous, { file }]);
      setState(previous => ({ ...previous, active: file.locator, tabs: previous.tabs.some(tab => tab.locator === file.locator) ? previous.tabs : [...previous.tabs, { locator: file.locator }] }));
      await files.watch(file.ref).catch(() => {}); if (alive.current && started === epoch.current) setError("");
    } catch (reason) { if (alive.current && started === epoch.current) setError(localFileError(reason, zh)); }
    finally { if (pendingOpen.current.get(entry.locator) === request) pendingOpen.current.delete(entry.locator); }
  }, [files, zh, updateState]);
  const closeTab = (tab: Tab) => {
    setTabs(previous => previous.filter(value => value.file.locator !== tab.file.locator));
    setState(previous => ({ ...previous, tabs: previous.tabs.filter(value => value.locator !== tab.file.locator), active: previous.active === tab.file.locator ? "files" : previous.active }));
    void files.unwatch(tab.file.ref).catch(() => {}); void files.release(tab.file.ref).catch(() => {});
  };
  const reopen = async (tab: Tab) => {
    const started = epoch.current;
    try {
      const file = await files.resolve(tab.file.locator);
      if (!alive.current || started !== epoch.current || !tabRef.current.some(value => value.file.ref === tab.file.ref)) return;
      if (file.kind !== "file") throw new Error(zh ? "此位置已不再是普通文件。" : "This location is no longer a regular file.");
      setTabs(previous => previous.map(value => value.file.ref === tab.file.ref ? { file } : value));
      if (file.ref !== tab.file.ref) { await files.unwatch(tab.file.ref).catch(() => {}); await files.release(tab.file.ref).catch(() => {}); }
      await files.watch(file.ref).catch(() => {});
    } catch (reason) { if (alive.current && started === epoch.current) setError(localFileError(reason, zh)); }
  };
  const clear = async () => {
    epoch.current++; pendingOpen.current.clear(); tabRef.current = []; setTabs([]); setState(emptyState()); setLoaded(false); setRoots([]);
    try { await files.clear(); const started = epoch.current, catalog = await files.roots(); if (!alive.current || started !== epoch.current) return; generationRef.current = catalog.generation; setRoots(catalog.roots); setGeneration(catalog.generation); setLoaded(true); setError(""); }
    catch (reason) { if (alive.current) setError(localFileError(reason, zh)); }
  };
  const active = tabs.find(tab => tab.file.locator === state.active);
  const position = useCallback((source: LocalFile, value: ReadingPosition) => {
    if (!tabRef.current.some(tab => tab.file.ref === source.ref && tab.file.version === source.version)) return;
    setState(previous => ({ ...previous, tabs: previous.tabs.map(tab => tab.locator === source.locator ? { ...tab, position: value } : tab) }));
  }, []);
  const tabBar = <div className="desktopBrowserTabBar localFilesTabBar">
    <div className="desktopTabStrip" role="tablist" aria-label={zh ? "本地文件" : "Local files"} onKeyDown={event => {
      if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
      const buttons = [...event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="tab"]')], index = buttons.indexOf(document.activeElement as HTMLButtonElement);
      if (index < 0) return; event.preventDefault();
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : (index + (event.key === 'ArrowRight' ? 1 : -1) + buttons.length) % buttons.length;
      buttons[next].focus(); buttons[next].click();
    }}>
      <div className={`desktopTab ${state.active === "files" ? "selected" : ""}`}><button className="desktopTabSelect" type="button" role="tab" aria-selected={state.active === "files"} onClick={() => { updateState({ active: "files" }); onShowFiles(); }}><Folder size={14} /><span>{zh ? "文件" : "Files"}</span></button></div>
      {tabs.map(tab => <div className={`desktopTab ${state.active === tab.file.locator ? "selected" : ""}`} key={tab.file.locator} title={tab.file.displayPath}>
        <button type="button" className="desktopTabSelect" role="tab" aria-selected={state.active === tab.file.locator} onClick={() => { updateState({ active: tab.file.locator }); onShowFiles(); }}
          onKeyDown={event => { if (event.key === "Delete") { event.preventDefault(); closeTab(tab); } }}><span>{tab.file.name}</span></button>
        <button type="button" className="desktopTabClose" aria-label={`${zh ? "关闭" : "Close"} ${tab.file.name}`} onClick={() => closeTab(tab)}><X size={13} /></button>
      </div>)}
    </div>
    <div className="desktopBrowserWindowActions"><button className="iconButton" type="button" title={zh ? "调整阅读宽度" : "Resize reading area"} aria-label={zh ? "调整阅读宽度" : "Resize reading area"} onClick={() => onWideChange(!wide)}>{wide ? <Minimize2 size={15} /> : <Maximize2 size={15} />}</button>
      <button type="button" className="iconButton" title={zh ? "收起文件" : "Collapse files"} aria-label={zh ? "收起文件" : "Collapse files"} onClick={onCollapse}><PanelRightClose size={15} /></button></div>
  </div>;
  return <>
    {open && conversationInFront !== undefined && <ClientTitlebarBrowser active panel={panelRef}>{tabBar}</ClientTitlebarBrowser>}
    <aside ref={panelRef} className={`taskInspector desktopBrowserPanel localFilesPanel ${wide ? "wide" : ""}`} hidden={!visible} aria-label={zh ? "文件" : "Files"}>
      {conversationInFront === undefined && tabBar}
      {error && <p role="alert" className="localFilesError">{error}</p>}
      {loaded && <LocalFileTree key={generation} files={files} roots={roots} visible={visible && state.active === "files"} zh={zh} state={state} onState={updateState} onOpen={entry => void openFile(entry)} />}
      {state.active === "files" && <button type="button" className="localFilesClear" onClick={() => void clear()}>{zh ? "清除本地预览缓存与阅读记录" : "Clear local previews and reading history"}</button>}
      {visible && active && <>
        {(active.changed || active.unavailable) && <div className="localFilesError" role="status"><p>{active.unavailable || (zh ? "文件已更改，当前预览已暂停。" : "The file changed. Preview is paused.")}</p><button type="button" onClick={() => void reopen(active)}><RefreshCw size={14} />{zh ? "重新打开" : "Reopen"}</button></div>}
        {!active.unavailable && !active.changed && <LocalDocumentPreview key={`${active.file.ref}:${active.file.version}`} files={files} file={active.file} loginGeneration={generation} zh={zh}
          position={state.tabs.find(tab => tab.locator === active.file.locator)?.position} onPosition={value => position(active.file, value)} onInvalid={() => void revalidate(active)} onEscape={onCollapse} />}
      </>}
    </aside>
  </>;
}
