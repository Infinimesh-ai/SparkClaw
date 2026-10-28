import { FormEvent, useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ArrowLeft,
  ArrowRight,
  ArrowUpRight,
  Download,
  EllipsisVertical,
  Eye,
  FolderOpen,
  Globe2,
  LockKeyhole,
  Maximize2,
  Minimize2,
  PanelRightClose,
  Plus,
  RefreshCw,
  ShieldCheck,
  X,
} from "lucide-react";
import type { Language } from "../i18n";
import { desktopCapability } from "./capability";
import type { DesktopPage, DesktopState } from "./types";
import { useDismissibleLayer } from "../hooks/useDismissibleLayer";
import { ClientTitlebarBrowser } from "../components/clientWindow";

const copy = {
  en: {
    browser: "Browser", collapse: "Collapse browser", newTab: "New tab",
    address: "Search or enter an address", go: "Open", start: "Start browsing", startHint: "Enter a URL to open a page",
    taskPage: "Task page", readOnly: "Read-only task page",
    close: "Close tab", back: "Back", forward: "Forward", reload: "Reload", expand: "Widen browser", restore: "Restore browser width", more: "More browser actions",
    transfers: "Downloads & permissions", allow: "Allow", deny: "Deny", cancel: "Cancel", show: "Show in folder",
  },
  zh: {
    browser: "浏览器", collapse: "收起浏览器", newTab: "新标签页",
    address: "搜索或输入网址", go: "打开", start: "开始浏览", startHint: "输入 URL 以打开页面",
    taskPage: "任务页面", readOnly: "任务页面 · 只读",
    close: "关闭标签页", back: "后退", forward: "前进", reload: "刷新", expand: "加宽浏览器", restore: "恢复浏览器宽度", more: "更多浏览器操作",
    transfers: "下载与权限", allow: "允许", deny: "拒绝", cancel: "取消", show: "在文件夹中显示",
  },
} as const;

export type BrowserPanelProps = {
  language: Language;
  localConversationID?: string;
  open?: boolean;
  visible?: boolean;
  onCollapse?: () => void;
  wide?: boolean;
  onWideChange?: (wide: boolean) => void;
};

// Keep this component mounted when collapsed: address drafts belong to tabs,
// and native views must be hidden without destroying their browsing state.
export function BrowserPanel({ language, localConversationID, open = true,
  visible = open, onCollapse, wide: controlledWide, onWideChange }: BrowserPanelProps) {
  const desktop = desktopCapability();
  const text = copy[language];
  const hostRef = useRef<HTMLDivElement | null>(null);
  const panelRef = useRef<HTMLElement | null>(null);
  const addressRef = useRef<HTMLInputElement | null>(null);
  const boundsRevision = useRef(0);
  const boundsError = useRef("");
  const nativePresented = useRef("");
  const [state, setState] = useState<DesktopState | null>(null);
  const [selectedPageRef, setSelectedPageRef] = useState("");
  const [address, setAddress] = useState("");
  const drafts = useRef(new Map<string, string>());
  const [error, setError] = useState("");
  const [showTransfers, setShowTransfers] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useDismissibleLayer<HTMLDivElement>({ open: menuOpen, onDismiss: () => setMenuOpen(false) });
  const [localWide, setLocalWide] = useState(false);
  const wide = controlledWide ?? localWide;
  const setWide = (next: boolean) => { setLocalWide(next); onWideChange?.(next); };
  const [collapsed, setCollapsed] = useState(false);
  const shown = visible && open && !collapsed;
  const shownRef = useRef(shown); shownRef.current = shown;
  const creating = useRef<Promise<string> | null>(null);
  const [focusRequest, setFocusRequest] = useState(0);
  const focusedRequest = useRef(0);
  const opened = useRef(false);
  const [occluded, setOccluded] = useState(false);

  const run = useCallback((action: Promise<unknown>) => {
    void action.then(() => setError("")).catch((reason) => setError(message(reason)));
  }, []);

  useEffect(() => {
    if (!desktop) return;
    let active = true;
    const acceptState = (next: DesktopState) => {
      if (!active) return;
      boundsRevision.current = Math.max(boundsRevision.current, next.presentation.layout_revision ?? 0);
      nativePresented.current = next.presentation.presented_page_ref;
      setState(next);
    };
    void desktop.state().then(acceptState).catch((reason) => { if (active) setError(message(reason)); });
    const remove = desktop.onState(acceptState);
    return () => { active = false; remove(); void desktop.hideBrowser().catch(() => undefined); };
  }, [desktop]);

  const pages = useMemo(() => (state?.pages ?? []).filter((page) => localConversationID === undefined || page.role === "personal" ||
    (page as DesktopPage & { local_conversation_id?: string }).local_conversation_id === localConversationID), [state, localConversationID]);
  const activePage = pages.find((page) => page.page_ref === selectedPageRef);
  const blankPage = !activePage || activePage.url === "about:blank";
  useEffect(() => {
    if (selectedPageRef && !activePage && !creating.current) setSelectedPageRef("");
  }, [selectedPageRef, activePage, pages]);

  const create = useCallback((url?: string): Promise<string> => {
    if (!desktop) return Promise.resolve("");
    if (creating.current) return creating.current;
    const operation = desktop.hideBrowser().then(() => desktop.createPersonal(url)).then(async ({ page_ref }) => {
      setSelectedPageRef(page_ref);
      setAddress(url ?? "");
      if (!url) { await desktop.hideBrowser(); setFocusRequest(value => value + 1); }
      setError("");
      return page_ref;
    }).catch(reason => { setError(message(reason)); return ""; }).finally(() => { creating.current = null; });
    creating.current = operation;
    return operation;
  }, [desktop]);

  useEffect(() => {
    if (!open) { opened.current = false; setCollapsed(false); return; }
    if (!state || opened.current || !desktop) return;
    opened.current = true;
    setCollapsed(false);
    setMenuOpen(false);
    const blank = pages.find(page => page.role === "personal" && page.url === "about:blank" && !drafts.current.get(page.page_ref));
    run(desktop.hideBrowser());
    if (blank) { setSelectedPageRef(blank.page_ref); setAddress(""); setFocusRequest(value => value + 1); }
    else void create();
  }, [open, state, pages, desktop, create, run]);

  // Only an explicit open/new-tab action requests focus. State pushes and
  // unrelated task updates never overwrite a partially typed address.
  useEffect(() => {
    if (!focusRequest || focusedRequest.current === focusRequest || !shown) return;
    const frame = requestAnimationFrame(() => {
      if (shownRef.current) { addressRef.current?.focus(); focusedRequest.current = focusRequest; }
    });
    return () => cancelAnimationFrame(frame);
  }, [focusRequest, shown]);
  useEffect(() => {
    setAddress(drafts.current.get(selectedPageRef) ?? (activePage?.url.startsWith("https://") ? activePage.url : ""));
  }, [selectedPageRef, activePage?.url]);

  // A native WebContentsView paints above DOM overlays. Suspend presentation
  // while an app dialog/menu is visible, then restore the same page.
  useEffect(() => {
    const update = () => setOccluded(Boolean(document.querySelector('dialog[open], [aria-modal="true"], [role="menu"], .notificationInbox')));
    const observer = new MutationObserver(update);
    observer.observe(document.body, { subtree: true, childList: true, attributes: true, attributeFilter: ["open", "aria-modal"] });
    update();
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    if (!desktop || !state) return;
    if (!shown || occluded || blankPage) { nativePresented.current = ""; run(desktop.hideBrowser()); return; }
    const host = hostRef.current;
    if (!host || !activePage) return;
    let cancelled = false;
    const update = async () => {
      const bounds = host.getBoundingClientRect();
      const x = Math.ceil(bounds.x), y = Math.ceil(bounds.y);
      const width = Math.floor(bounds.x + bounds.width) - x, height = Math.floor(bounds.y + bounds.height) - y;
      if (width <= 0 || height <= 0) return;
      const revision = ++boundsRevision.current;
      try {
        await desktop.setBounds({ x, y, width, height }, revision);
        if (cancelled || !shownRef.current || revision !== boundsRevision.current) return;
        // Main can reject a measurement made before a window resize/zoom
        // reaches the renderer. A later accepted layout clears only that error.
        const recovered = boundsError.current;
        if (recovered) { setError(current => current === recovered ? "" : current); boundsError.current = ""; }
        if (nativePresented.current !== activePage.page_ref) {
          if (activePage.role === "personal") await desktop.presentPersonal(activePage.page_ref);
          else await desktop.observeTask(activePage.page_ref);
          nativePresented.current = activePage.page_ref;
        }
      } catch (reason) {
        if (!cancelled && revision === boundsRevision.current) { boundsError.current = message(reason); setError(boundsError.current); }
      }
    };
    const observer = new ResizeObserver(() => { void update(); });
    observer.observe(host);
    const workspace = host.closest(".workspace");
    if (workspace) observer.observe(workspace);
    window.addEventListener("resize", update);
    void update();
    return () => { cancelled = true; observer.disconnect(); window.removeEventListener("resize", update); };
  }, [desktop, Boolean(state), shown, occluded, blankPage, activePage?.page_ref, wide, run]);

  // Bounds also stay current on the new-tab surface, before the first navigation.
  useEffect(() => {
    if (!desktop || !state || !shown || !blankPage || !hostRef.current) return;
    const host = hostRef.current;
    const update = () => {
      const bounds = host.getBoundingClientRect();
      const x = Math.ceil(bounds.x), y = Math.ceil(bounds.y);
      const width = Math.floor(bounds.x + bounds.width) - x, height = Math.floor(bounds.y + bounds.height) - y;
      if (width > 0 && height > 0) run(desktop.setBounds({ x, y, width, height }, ++boundsRevision.current));
    };
    const observer = new ResizeObserver(update); observer.observe(host);
    const workspace = host.closest(".workspace"); if (workspace) observer.observe(workspace);
    window.addEventListener("resize", update); update();
    return () => { observer.disconnect(); window.removeEventListener("resize", update); };
  }, [desktop, Boolean(state), shown, blankPage, wide, run]);

  useEffect(() => {
    if (!desktop || !shown) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing || !(event.ctrlKey || event.metaKey) || event.altKey ||
        event.key.toLowerCase() !== "t" || document.querySelector('dialog[open], [aria-modal="true"]')) return;
      event.preventDefault(); void create();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [create, desktop, shown]);

  if (!desktop) return <aside className="taskInspector emptyTaskInspector" hidden={!shown} />;
  const navigate = (event: FormEvent) => {
    event.preventDefault();
    if (!address.trim()) return;
    const target = resolveBrowserInput(address);
    drafts.current.delete(selectedPageRef);
    if (activePage?.role === "personal") run(desktop.navigatePersonal(activePage.page_ref, target));
    else void create(target);
  };
  const choose = (page: DesktopPage) => { setSelectedPageRef(page.page_ref); setMenuOpen(false); };
  const close = (page: DesktopPage) => run(desktop.closePersonal(page.page_ref).then(() => {
    drafts.current.delete(page.page_ref);
    if (page.page_ref === selectedPageRef) {
      const remaining = pages.filter(candidate => candidate.page_ref !== page.page_ref);
      const neighbor = remaining[Math.min(pages.indexOf(page), remaining.length - 1)];
      setSelectedPageRef(neighbor?.page_ref ?? ""); setAddress(""); setFocusRequest(value => value + 1);
    }
  }));
  const leaveBrowser = () => {
    setShowTransfers(false); setMenuOpen(false); setWide(false);
    run(desktop.hideBrowser());
    if (onCollapse) onCollapse(); else setCollapsed(true);
  };
  const toggleTransfers = () => { setShowTransfers(current => !current); setMenuOpen(false); };

  return <aside ref={panelRef} className={`taskInspector desktopBrowserPanel ${wide ? "wide" : ""}`} hidden={!shown} aria-label={text.browser}>
      <ClientTitlebarBrowser active={shown} panel={panelRef}><div className="desktopBrowserTabBar">
        <div className="desktopTabStrip" role="tablist" aria-label={text.browser}>
          {!activePage && <div className="desktopTab desktopTabPlaceholder selected" role="tab" aria-selected="true">
            <span className="desktopTabPlaceholderLabel"><Globe2 size={12} /><span>{text.newTab}</span></span>
          </div>}
          {pages.map((page) => <div key={page.page_ref} className={`desktopTab ${page.page_ref === activePage?.page_ref ? "selected" : ""}`}>
            <button type="button" className="desktopTabSelect" onClick={() => choose(page)} title={page.title || page.url} role="tab" aria-selected={page.page_ref === activePage?.page_ref}>
              {page.role === "task" ? <Eye size={12} aria-hidden="true" /> : <Globe2 size={12} aria-hidden="true" />}
              <span>{page.url === "about:blank" ? text.newTab : page.title || page.url || (page.role === "task" ? text.taskPage : text.newTab)}</span>
            </button>
            {page.role === "personal" && <button type="button" className="desktopTabClose" onClick={() => close(page)} title={text.close} aria-label={text.close}><X size={12} /></button>}
          </div>)}
        </div>
        <button type="button" className="desktopNewTab" onClick={() => void create()} title={text.newTab} aria-label={text.newTab}><Plus size={15} /></button>
        <div className="desktopBrowserWindowActions">
          <button type="button" onClick={() => setWide(!wide)} title={wide ? text.restore : text.expand} aria-label={wide ? text.restore : text.expand}>
            {wide ? <Minimize2 size={14} /> : <Maximize2 size={14} />}
          </button>
          <button type="button" onClick={leaveBrowser} title={text.collapse} aria-label={text.collapse}><PanelRightClose size={15} /></button>
        </div>
      </div></ClientTitlebarBrowser>
      <form className="desktopAddressBar" onSubmit={navigate}>
        <button type="button" disabled={activePage?.role !== "personal" || !activePage.can_go_back} onClick={() => activePage?.role === "personal" && run(desktop.personalNavigation(activePage.page_ref, "back"))} title={text.back}><ArrowLeft size={15} /></button>
        <button type="button" disabled={activePage?.role !== "personal" || !activePage.can_go_forward} onClick={() => activePage?.role === "personal" && run(desktop.personalNavigation(activePage.page_ref, "forward"))} title={text.forward}><ArrowRight size={15} /></button>
        <button type="button" disabled={activePage?.role !== "personal"} onClick={() => activePage?.role === "personal" && run(desktop.personalNavigation(activePage.page_ref, "reload"))} title={text.reload}><RefreshCw size={14} /></button>
        <div className="desktopOmnibox">
          <input ref={addressRef} aria-label={text.address} placeholder={text.address} value={address} onChange={(event) => { setAddress(event.target.value); drafts.current.set(selectedPageRef, event.target.value); }} autoComplete="off" spellCheck={false} />
          <button type="submit" disabled={!address.trim()} title={text.go} aria-label={text.go}><ArrowUpRight size={14} /></button>
        </div>
        <button type="button" className={`desktopTransfersButton ${showTransfers ? "selected" : ""}`} onClick={toggleTransfers} title={text.transfers} aria-label={text.transfers}>
          <Download size={14} />{(state?.downloads.length ?? 0) + (state?.permissions.length ?? 0) > 0 && <small>{(state?.downloads.length ?? 0) + (state?.permissions.length ?? 0)}</small>}
        </button>
        <div className="desktopBrowserOverflow">
          <button type="button" onClick={() => setMenuOpen((current) => !current)} title={text.more} aria-label={text.more} aria-expanded={menuOpen}><EllipsisVertical size={15} /></button>
          {menuOpen && <div ref={menuRef} className="desktopBrowserMenu" role="menu">
            <button type="button" role="menuitem" onClick={() => { void create(); setMenuOpen(false); }}><Plus size={14} /><span>{text.newTab}</span></button>
            <button type="button" role="menuitem" onClick={toggleTransfers}><Download size={14} /><span>{text.transfers}</span></button>
            <button type="button" role="menuitem" onClick={leaveBrowser}><PanelRightClose size={14} /><span>{text.collapse}</span></button>
          </div>}
        </div>
      </form>
      {activePage?.role === "task" && <div className="desktopTaskNotice" role="status"><LockKeyhole size={13} /><span>{text.readOnly}</span></div>}
      {showTransfers && <div className="desktopTransfers">
        {(state?.permissions ?? []).map((request) => <div className="desktopPermissionRequest" key={request.permission_ref}>
          <ShieldCheck size={15} /><span><strong>{request.permission}</strong><small>{request.origin}{request.media_types.length ? ` · ${request.media_types.join(", ")}` : ""}</small></span>
          <button onClick={() => run(desktop.respondPermission(request.permission_ref, false))}>{text.deny}</button>
          <button className="allow" onClick={() => run(desktop.respondPermission(request.permission_ref, true))}>{text.allow}</button>
        </div>)}
        {(state?.downloads ?? []).map((download) => <div className="desktopDownload" key={download.download_ref}>
          <Download size={14} /><span><strong>{download.filename}</strong><small>{download.state} · {formatBytes(download.received_bytes)}{download.total_bytes ? ` / ${formatBytes(download.total_bytes)}` : ""}</small></span>
          {download.state === "in_progress"
            ? <button onClick={() => run(desktop.cancelDownload(download.download_ref))}>{text.cancel}</button>
            : download.state === "complete" && <button onClick={() => run(desktop.showDownload(download.download_ref))} title={text.show}><FolderOpen size={14} /></button>}
        </div>)}
        {!(state?.permissions.length || state?.downloads.length) && <p>{text.transfers}</p>}
      </div>}
      {error && <div className="desktopBrowserError" role="alert">{error}</div>}
      <div ref={hostRef} className="desktopBrowserHost">
        {(!activePage || blankPage) && <div className="desktopBrowserEmptyState"><Globe2 size={30} /><h2>{text.start}</h2><p>{text.startHint}</p></div>}
      </div>
  </aside>;
}

export function resolveBrowserInput(value: string) {
  const trimmed = value.trim();
  if (!trimmed) return "https://www.google.com/";

  const explicit = trimmed.replace(/^http:\/\//iu, "https://");
  if (/^https:\/\//iu.test(explicit)) {
    try {
      const url = new URL(explicit);
      if (url.hostname && !url.username && !url.password) return url.toString();
    } catch {
      // Invalid URL-shaped input is searched instead of being sent to the desktop URL boundary.
    }
  } else if (looksLikeAddress(trimmed)) {
    try {
      return new URL(`https://${trimmed}`).toString();
    } catch {
      // Fall through to search.
    }
  }

  const search = new URL("https://www.google.com/search");
  search.searchParams.set("q", trimmed);
  return search.toString();
}

function looksLikeAddress(value: string) {
  if (/\s/u.test(value)) return false;
  try {
    const { hostname } = new URL(`https://${value}`);
    if (hostname === "localhost" || hostname.includes(":")) return true;
    if (/^(?:\d{1,3}\.){3}\d{1,3}$/u.test(hostname)) {
      return hostname.split(".").every((part) => Number(part) <= 255);
    }
    return /^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+(?:[a-z]{2,63}|xn--[a-z0-9-]{2,59})$/iu.test(hostname);
  } catch {
    return false;
  }
}

function message(reason: unknown) {
  return reason instanceof Error && reason.message ? reason.message : "Desktop browser action failed";
}

function formatBytes(value: number) {
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${Math.round(value / 1024)} KB`;
  return `${(value / (1024 * 1024)).toFixed(1)} MB`;
}
