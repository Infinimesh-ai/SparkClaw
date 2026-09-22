import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { ArrowLeft, ArrowRight, Download, Eye, FolderOpen, Globe2, Plus, RefreshCw, ShieldCheck, X } from "lucide-react";
import type { Language } from "../i18n";
import { desktopCapability } from "./capability";
import type { DesktopPage, DesktopState } from "./types";

const copy = {
  en: {
    personal: "My browsing", task: "Task observation", newTab: "New personal tab", address: "HTTPS address",
    go: "Go", noPersonal: "Open a personal tab to browse interactively.", noTask: "No task browser page is active.",
    readOnly: "Read-only task view", small: "The panel is too small for the fixed 640 × 720 task viewport.",
    close: "Close tab", back: "Back", forward: "Forward", reload: "Reload",
    transfers: "Downloads & permissions", allow: "Allow", deny: "Deny", cancel: "Cancel", show: "Show in folder",
  },
  zh: {
    personal: "我的浏览", task: "任务观察", newTab: "新建个人标签页", address: "HTTPS 地址",
    go: "前往", noPersonal: "新建个人标签页后可交互浏览。", noTask: "当前没有任务浏览器页面。",
    readOnly: "任务只读观察", small: "面板空间不足，任务视口固定为 640 × 720。",
    close: "关闭标签页", back: "后退", forward: "前进", reload: "刷新",
    transfers: "下载与权限", allow: "允许", deny: "拒绝", cancel: "取消", show: "在文件夹中显示",
  },
} as const;

export function BrowserPanel({ language }: { language: Language }) {
  const desktop = desktopCapability();
  const text = copy[language];
  const hostRef = useRef<HTMLDivElement | null>(null);
  const boundsRevision = useRef(0);
  const [state, setState] = useState<DesktopState | null>(null);
  const [mode, setMode] = useState<"personal" | "task">("personal");
  const [address, setAddress] = useState("https://");
  const [error, setError] = useState("");
  const [showTransfers, setShowTransfers] = useState(false);

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
    if (!desktop || !hostRef.current) return;
    const host = hostRef.current;
    const update = () => {
      const bounds = host.getBoundingClientRect();
      if (bounds.width < 480 || bounds.height < 240) return;
      boundsRevision.current += 1;
      void desktop.setBounds({
        x: Math.round(bounds.x), y: Math.round(bounds.y),
        width: Math.round(bounds.width), height: Math.round(bounds.height),
      }, boundsRevision.current).catch((reason) => setError(message(reason)));
    };
    const observer = new ResizeObserver(update);
    observer.observe(host);
    update();
    return () => observer.disconnect();
  }, [desktop]);

  const personal = useMemo(() => state?.pages.filter((page) => page.role === "personal") ?? [], [state]);
  const tasks = useMemo(() => state?.pages.filter((page) => page.role === "task") ?? [], [state]);
  const presented = state?.pages.find((page) => page.page_ref === state.presentation.presented_page_ref);
  const activePersonal = presented?.role === "personal" ? presented : personal[0];
  const activeTask = presented?.role === "task" ? presented : tasks[0];

  useEffect(() => {
    const page = mode === "personal" ? activePersonal : activeTask;
    if (!desktop || !page || page.presented) return;
    const action = page.role === "personal" ? desktop.presentPersonal(page.page_ref) : desktop.observeTask(page.page_ref);
    void action.catch((reason) => setError(message(reason)));
  }, [activePersonal?.page_ref, activeTask?.page_ref, desktop, mode]);

  useEffect(() => {
    if (mode === "personal" && activePersonal?.url.startsWith("https://")) setAddress(activePersonal.url);
  }, [activePersonal?.url, mode]);

  if (!desktop) return <aside className="taskInspector emptyTaskInspector" />;

  const run = (action: Promise<unknown>) => void action.then(() => setError("")).catch((reason) => setError(message(reason)));
  const create = () => run(desktop.createPersonal().then(({ page_ref }) => desktop.presentPersonal(page_ref)));
  const navigate = (event: FormEvent) => {
    event.preventDefault();
    if (!activePersonal) return;
    run(desktop.navigatePersonal(activePersonal.page_ref, normalizeAddress(address)));
  };
  const choose = (page: DesktopPage) => run(page.role === "personal"
    ? desktop.presentPersonal(page.page_ref)
    : desktop.observeTask(page.page_ref));

  return <aside className="taskInspector desktopBrowserPanel" aria-label={language === "zh" ? "桌面浏览器" : "Desktop browser"}>
    <div className="desktopBrowserModes" role="tablist">
      <button className={mode === "personal" ? "selected" : ""} onClick={() => setMode("personal")} role="tab"><Globe2 size={15} />{text.personal}</button>
      <button className={mode === "task" ? "selected" : ""} onClick={() => setMode("task")} role="tab"><Eye size={15} />{text.task}</button>
      {mode === "personal" && <button className="desktopNewTab" onClick={create} title={text.newTab} aria-label={text.newTab}><Plus size={15} /></button>}
      <button className={`desktopTransfersButton ${showTransfers ? "selected" : ""}`} onClick={() => setShowTransfers((current) => !current)} title={text.transfers} aria-label={text.transfers}>
        <Download size={15} />{(state?.downloads.length ?? 0) + (state?.permissions.length ?? 0) > 0 && <small>{(state?.downloads.length ?? 0) + (state?.permissions.length ?? 0)}</small>}
      </button>
    </div>
    {mode === "personal" ? <>
      <div className="desktopTabStrip">
        {personal.map((page) => <button key={page.page_ref} className={page.page_ref === activePersonal?.page_ref ? "selected" : ""} onClick={() => choose(page)} title={page.title || page.url}>
          <span>{page.title || page.url || text.personal}</span><X size={12} onClick={(event) => { event.stopPropagation(); run(desktop.closePersonal(page.page_ref)); }} aria-label={text.close} />
        </button>)}
      </div>
      <form className="desktopAddressBar" onSubmit={navigate}>
        <button type="button" disabled={!activePersonal?.can_go_back} onClick={() => activePersonal && run(desktop.personalNavigation(activePersonal.page_ref, "back"))} title={text.back}><ArrowLeft size={15} /></button>
        <button type="button" disabled={!activePersonal?.can_go_forward} onClick={() => activePersonal && run(desktop.personalNavigation(activePersonal.page_ref, "forward"))} title={text.forward}><ArrowRight size={15} /></button>
        <button type="button" disabled={!activePersonal} onClick={() => activePersonal && run(desktop.personalNavigation(activePersonal.page_ref, "reload"))} title={text.reload}><RefreshCw size={14} /></button>
        <input aria-label={text.address} value={address} onChange={(event) => setAddress(event.target.value)} disabled={!activePersonal} />
        <button type="submit" disabled={!activePersonal}>{text.go}</button>
      </form>
    </> : <div className="desktopTaskPicker">
      <span>{text.readOnly}</span>
      <select value={activeTask?.page_ref ?? ""} onChange={(event) => { const page = tasks.find((item) => item.page_ref === event.target.value); if (page) choose(page); }} disabled={!tasks.length}>
        {!tasks.length && <option value="">{text.noTask}</option>}
        {tasks.map((page) => <option key={page.page_ref} value={page.page_ref}>{page.title || page.task_id || page.url}</option>)}
      </select>
    </div>}
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
      {mode === "personal" && !activePersonal && <p>{text.noPersonal}</p>}
      {mode === "task" && !activeTask && <p>{text.noTask}</p>}
      {mode === "task" && state?.presentation.insufficient_space && <p>{text.small}</p>}
    </div>
  </aside>;
}

function normalizeAddress(value: string) {
  const trimmed = value.trim();
  return trimmed.startsWith("https://") ? trimmed : `https://${trimmed.replace(/^\/+/, "")}`;
}

function message(reason: unknown) {
  return reason instanceof Error && reason.message ? reason.message : "Desktop browser action failed";
}

function formatBytes(value: number) {
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${Math.round(value / 1024)} KB`;
  return `${(value / (1024 * 1024)).toFixed(1)} MB`;
}
