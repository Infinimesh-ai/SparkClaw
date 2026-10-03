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

const copy = {
  en: {
    tools: "Panel tools", browser: "Browser", backToTools: "Back to tools", newTab: "New tab",
    address: "Search or enter an address", go: "Open", start: "Start browsing", startHint: "Enter a URL to open a page",
    taskPage: "Task page", readOnly: "Read-only task page", small: "The panel is too small for the fixed 640 × 720 task viewport.",
    close: "Close tab", back: "Back", forward: "Forward", reload: "Reload", expand: "Widen browser", restore: "Restore browser width", more: "More browser actions",
    transfers: "Downloads & permissions", allow: "Allow", deny: "Deny", cancel: "Cancel", show: "Show in folder",
  },
  zh: {
    tools: "侧栏工具", browser: "浏览器", backToTools: "返回工具列表", newTab: "新标签页",
    address: "搜索或输入网址", go: "打开", start: "开始浏览", startHint: "输入 URL 以打开页面",
    taskPage: "任务页面", readOnly: "任务页面 · 只读", small: "面板空间不足，任务视口固定为 640 × 720。",
    close: "关闭标签页", back: "后退", forward: "前进", reload: "刷新", expand: "加宽浏览器", restore: "恢复浏览器宽度", more: "更多浏览器操作",
    transfers: "下载与权限", allow: "允许", deny: "拒绝", cancel: "取消", show: "在文件夹中显示",
  },
} as const;

export function BrowserPanel({ language, localConversationID }: { language: Language; localConversationID?: string }) {
  const desktop = desktopCapability();
  const text = copy[language];
  const hostRef = useRef<HTMLDivElement | null>(null);
  const boundsRevision = useRef(0);
  const [state, setState] = useState<DesktopState | null>(null);
  const [surface, setSurface] = useState<"launcher" | "browser">(localConversationID === undefined ? "launcher" : "browser");
  const [selectedPageRef, setSelectedPageRef] = useState("");
  const [address, setAddress] = useState("");
  const [error, setError] = useState("");
  const [showTransfers, setShowTransfers] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [wide, setWide] = useState(false);

  useEffect(() => {
    if (!desktop) return;
    let active = true;
    void desktop.state().then((next) => { if (active) setState(next); }).catch((reason) => setError(message(reason)));
    const remove = desktop.onState((next) => { if (active) setState(next); });
    return () => {
      active = false;
      remove();
      void desktop.hideBrowser().catch(() => undefined);
    };
  }, [desktop]);

  useEffect(() => {
    if (!desktop || surface !== "browser" || !hostRef.current) return;
    const host = hostRef.current;
    const update = () => {
      const bounds = host.getBoundingClientRect();
      if (bounds.width <= 0 || bounds.height <= 0) return;
      boundsRevision.current += 1;
      void desktop.setBounds({
        x: Math.round(bounds.x), y: Math.round(bounds.y),
        width: Math.round(bounds.width), height: Math.round(bounds.height),
      }, boundsRevision.current).catch((reason) => setError(message(reason)));
    };
    const observer = new ResizeObserver(update);
    observer.observe(host);
    // The host can move without resizing when the navigation sidebar collapses.
    const workspace = host.closest(".workspace");
    if (workspace) observer.observe(workspace);
    window.addEventListener("resize", update);
    update();
    return () => {
      observer.disconnect();
      window.removeEventListener("resize", update);
    };
  }, [desktop, surface, wide]);

  const pages = useMemo(() => (state?.pages ?? []).filter((page) => localConversationID === undefined || page.role === "personal" || (page as DesktopPage & { local_conversation_id?: string }).local_conversation_id === localConversationID), [state, localConversationID]);
  useEffect(() => {
    if (localConversationID === undefined) return;
    setSelectedPageRef("");
    setSurface("browser");
    setError("");
  }, [localConversationID]);
  const presented = pages.find((page) => page.page_ref === state?.presentation.presented_page_ref && (localConversationID === undefined || page.role === "task" || page.page_ref === selectedPageRef));
  const activePage = presented ?? pages.find((page) => page.page_ref === selectedPageRef) ?? (localConversationID === undefined ? pages[0] : pages.find((page) => page.role === "task"));
  const blankPage = activePage?.role === "personal" && activePage.url === "about:blank";

  useEffect(() => {
    if (!desktop || surface !== "browser" || !activePage) return;
    if (blankPage) {
      if (activePage.presented) void desktop.hideBrowser().catch((reason) => setError(message(reason)));
      return;
    }
    if (activePage.presented) return;
    const action = activePage.role === "personal"
      ? desktop.presentPersonal(activePage.page_ref)
      : desktop.observeTask(activePage.page_ref);
    void action.catch((reason) => setError(message(reason)));
  }, [activePage?.page_ref, activePage?.presented, activePage?.url, blankPage, desktop, surface]);

  useEffect(() => {
    if (!activePage || blankPage) {
      setAddress("");
      return;
    }
    if (activePage.url.startsWith("https://")) setAddress(activePage.url);
  }, [activePage?.page_ref, activePage?.url, blankPage]);

  const run = useCallback((action: Promise<unknown>) => {
    void action.then(() => setError("")).catch((reason) => setError(message(reason)));
  }, []);

  const create = useCallback((url?: string) => {
    if (!desktop) return;
    run(desktop.createPersonal(url).then(({ page_ref }) => {
      setSelectedPageRef(page_ref);
      return url ? desktop.presentPersonal(page_ref) : desktop.hideBrowser();
    }));
  }, [desktop, run]);

  useEffect(() => {
    if (!desktop) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (!(event.ctrlKey || event.metaKey) || event.altKey || event.key.toLowerCase() !== "t") return;
      event.preventDefault();
      if (surface === "launcher") setSurface("browser");
      else create();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [create, desktop, surface]);

  if (!desktop) return <aside className="taskInspector emptyTaskInspector" />;

  const navigate = (event: FormEvent) => {
    event.preventDefault();
    const target = resolveBrowserInput(address);
    if (activePage?.role === "personal") run(desktop.navigatePersonal(activePage.page_ref, target));
    else create(target);
  };
  const choose = (page: DesktopPage) => {
    setSelectedPageRef(page.page_ref);
    if (page.role === "personal" && page.url === "about:blank") run(desktop.hideBrowser());
    else run(page.role === "personal" ? desktop.presentPersonal(page.page_ref) : desktop.observeTask(page.page_ref));
  };
  const leaveBrowser = () => {
    setSurface("launcher");
    setShowTransfers(false);
    setMenuOpen(false);
    setWide(false);
    run(desktop.hideBrowser());
  };
  const toggleTransfers = () => {
    setShowTransfers((current) => !current);
    setMenuOpen(false);
  };

  return <aside className={`taskInspector desktopBrowserPanel ${wide ? "wide" : ""}`} aria-label={language === "zh" ? "桌面侧栏工具" : "Desktop panel tools"}>
    {surface === "launcher" ? <nav className="desktopToolLauncher" aria-label={text.tools}>
      <button type="button" onClick={() => setSurface("browser")}>
        <Globe2 size={17} />
        <span>{text.browser}</span>
        <kbd>Ctrl+T</kbd>
      </button>
    </nav> : <>
      <div className="desktopBrowserTabBar">
        <div className="desktopTabStrip" role="tablist" aria-label={text.browser}>
          {!pages.length && <div className="desktopTab desktopTabPlaceholder selected" role="tab" aria-selected="true">
            <span className="desktopTabPlaceholderLabel"><Globe2 size={12} /><span>{text.newTab}</span></span>
            <button type="button" className="desktopTabClose" onClick={leaveBrowser} title={text.close} aria-label={text.close}><X size={12} /></button>
          </div>}
          {pages.map((page) => <div key={page.page_ref} className={`desktopTab ${page.page_ref === activePage?.page_ref ? "selected" : ""}`}>
            <button type="button" className="desktopTabSelect" onClick={() => choose(page)} title={page.title || page.url} role="tab" aria-selected={page.page_ref === activePage?.page_ref}>
              {page.role === "task" ? <Eye size={12} aria-hidden="true" /> : <Globe2 size={12} aria-hidden="true" />}
              <span>{page.title || (blankPage && page.page_ref === activePage?.page_ref ? text.newTab : page.url) || (page.role === "task" ? text.taskPage : text.newTab)}</span>
            </button>
            {page.role === "personal" && <button type="button" className="desktopTabClose" onClick={() => run(desktop.closePersonal(page.page_ref))} title={text.close} aria-label={text.close}><X size={12} /></button>}
          </div>)}
        </div>
        <button type="button" className="desktopNewTab" onClick={() => create()} title={text.newTab} aria-label={text.newTab}><Plus size={15} /></button>
        <div className="desktopBrowserWindowActions">
          <button type="button" onClick={() => setWide((current) => !current)} title={wide ? text.restore : text.expand} aria-label={wide ? text.restore : text.expand}>
            {wide ? <Minimize2 size={14} /> : <Maximize2 size={14} />}
          </button>
          <button type="button" onClick={leaveBrowser} title={text.backToTools} aria-label={text.backToTools}><PanelRightClose size={15} /></button>
        </div>
      </div>
      <form className="desktopAddressBar" onSubmit={navigate}>
        <button type="button" disabled={activePage?.role !== "personal" || !activePage.can_go_back} onClick={() => activePage?.role === "personal" && run(desktop.personalNavigation(activePage.page_ref, "back"))} title={text.back}><ArrowLeft size={15} /></button>
        <button type="button" disabled={activePage?.role !== "personal" || !activePage.can_go_forward} onClick={() => activePage?.role === "personal" && run(desktop.personalNavigation(activePage.page_ref, "forward"))} title={text.forward}><ArrowRight size={15} /></button>
        <button type="button" disabled={activePage?.role !== "personal"} onClick={() => activePage?.role === "personal" && run(desktop.personalNavigation(activePage.page_ref, "reload"))} title={text.reload}><RefreshCw size={14} /></button>
        <div className="desktopOmnibox">
          <input aria-label={text.address} placeholder={text.address} value={address} onChange={(event) => setAddress(event.target.value)} autoComplete="off" spellCheck={false} />
          <button type="submit" disabled={!address.trim()} title={text.go} aria-label={text.go}><ArrowUpRight size={14} /></button>
        </div>
        <button type="button" className={`desktopTransfersButton ${showTransfers ? "selected" : ""}`} onClick={toggleTransfers} title={text.transfers} aria-label={text.transfers}>
          <Download size={14} />{(state?.downloads.length ?? 0) + (state?.permissions.length ?? 0) > 0 && <small>{(state?.downloads.length ?? 0) + (state?.permissions.length ?? 0)}</small>}
        </button>
        <div className="desktopBrowserOverflow">
          <button type="button" onClick={() => setMenuOpen((current) => !current)} title={text.more} aria-label={text.more} aria-expanded={menuOpen}><EllipsisVertical size={15} /></button>
          {menuOpen && <div className="desktopBrowserMenu" role="menu">
            <button type="button" role="menuitem" onClick={() => { create(); setMenuOpen(false); }}><Plus size={14} /><span>{text.newTab}</span></button>
            <button type="button" role="menuitem" onClick={toggleTransfers}><Download size={14} /><span>{text.transfers}</span></button>
            <button type="button" role="menuitem" onClick={leaveBrowser}><PanelRightClose size={14} /><span>{text.backToTools}</span></button>
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
        {activePage?.role === "task" && state?.presentation.insufficient_space && <p>{text.small}</p>}
      </div>
    </>}
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
