// The desktop client draws its own title bar. A workspace portals its sidebar
// toggle, history controls and actions into it; the web keeps the in-page topbar.
import { createContext, useContext, useEffect, useMemo, useState, type CSSProperties, type KeyboardEvent, type PointerEvent, type ReactNode, type RefObject } from "react";
import { createPortal } from "react-dom";
import { ArrowLeft, ArrowRight, MessageCircle } from "lucide-react";
import type { Language } from "../i18n";
import type { PageHistory } from "../hooks/usePageHistory";
import { desktopCapability } from "../desktop/capability";
import { afterCommit, snapState, type DragSnap } from "./inspectorResize";

type TitlebarSlots = { root: HTMLElement | null; host: HTMLElement | null; browser: HTMLElement | null; setBrowser: (element: HTMLElement | null) => void };
const TitlebarHost = createContext<TitlebarSlots | undefined>(undefined);

// Page history belongs to the client title bar; the web keeps browser history.
export function useClientTitlebar() {
  return Boolean(useContext(TitlebarHost));
}

// A dragged sidebar width lasts until the app restarts, like the browser's.
let sidebarSessionWidth: number | null = null;
const SIDEBAR_DEFAULT_WIDTH = 244;
// The sidebar keeps its controls usable above this width and never covers the
// conversation's narrow column. Dragging SNAP_DISTANCE past the narrowest width
// hides it, shown live while dragging.
const SIDEBAR_MIN_WIDTH = 160;
const SIDEBAR_CONVERSATION_RESERVE = 320;

export function ClientWindow({ children }: { children: ReactNode }) {
  const [root, setRoot] = useState<HTMLElement | null>(null);
  const [host, setHost] = useState<HTMLElement | null>(null);
  const [browser, setBrowser] = useState<HTMLElement | null>(null);
  const value = useMemo(() => ({ root, host, browser, setBrowser }), [root, host, browser]);
  return <div className="clientWindow glassWorkbench" ref={setRoot}
    style={sidebarSessionWidth === null ? undefined : { "--sidebar-width": `${sidebarSessionWidth}px` } as CSSProperties}>
    <header className="clientTitlebar workbench glassWorkbench" ref={setHost} aria-label="Spark X" />
    <div className="clientContent"><TitlebarHost.Provider value={value}>{children}</TitlebarHost.Provider></div>
  </div>;
}

export function WorkspaceTitlebar({ language, title = "", tab, sidebar, back, actions, navigation, hidden = false }: {
  language: Language;
  // The open conversation's title, shown over the workspace in the client.
  title?: string;
  // A tab placed just before the actions in the client title bar.
  tab?: ReactNode;
  sidebar?: ReactNode;
  back?: ReactNode;
  actions: ReactNode;
  navigation?: PageHistory;
  hidden?: boolean;
}) {
  const titlebar = useContext(TitlebarHost);
  const zh = language === "zh";
  if (!titlebar) return hidden ? null : <header className="topbar">{sidebar}{back}<div className="topbarActions">{actions}</div></header>;
  if (!titlebar.host) return null;
  // macOS draws its own window controls over the inset title bar; reserve their place.
  const insetControls = desktopCapability()?.windowChrome === "inset";
  return createPortal(<>
    <div className="clientTitlebarLeading">
      {insetControls && <span className="clientWindowControls" aria-hidden="true" />}
      <nav className="clientHistoryControls" aria-label={zh ? "页面历史" : "Page history"}>
        <button className="iconButton" type="button" aria-label={zh ? "后退" : "Back"} title={zh ? "后退" : "Back"}
          disabled={!navigation?.canGoBack} onClick={navigation?.goBack}><ArrowLeft aria-hidden="true" /></button>
        <button className="iconButton" type="button" aria-label={zh ? "前进" : "Forward"} title={zh ? "前进" : "Forward"}
          disabled={!navigation?.canGoForward} onClick={navigation?.goForward}><ArrowRight aria-hidden="true" /></button>
      </nav>
      <div className="clientSidebarControl">{sidebar || <span className="clientSidebarPlaceholder" aria-hidden="true" />}</div>
    </div>
    <div className="clientTitlebarMain">
      {title && <h1 className="clientTitlebarTitle" title={title}>{title}</h1>}
      {tab}
      <div className="clientTitlebarActions topbarActions">{actions}</div>
    </div>
    <div className="clientTitlebarTrailing"><div className="clientTitlebarBrowser" ref={titlebar.setBrowser} /></div>
  </>, titlebar.host);
}

// While the browser covers the conversation, the conversation keeps a tab just
// before the workspace actions; it and the browser tabs switch between the two.
export function ClientConversationTab({ title, selected, onSelect }: { title: string; selected: boolean; onSelect: () => void }) {
  return <button type="button" className={`clientConversationTab ${selected ? "selected" : ""}`} aria-pressed={selected}
    title={title} onClick={onSelect}><MessageCircle size={12} aria-hidden="true" /><span>{title}</span></button>;
}

// The open browser's tabs sit in the title bar above the panel, which keeps
// that part of the title bar as wide as itself; the workspace actions stay over
// the conversation. The web keeps the tabs in place.
export function ClientTitlebarBrowser({ active, panel, children }: { active: boolean; panel: RefObject<HTMLElement | null>; children: ReactNode }) {
  const titlebar = useContext(TitlebarHost);
  const host = titlebar?.host, slot = titlebar?.browser;
  const placed = Boolean(titlebar && active && host && slot);
  useEffect(() => {
    const element = panel.current;
    if (!placed || !host || !element) return;
    // A browser covering the workspace keeps its tabs there while the
    // conversation is in front; the hidden panel then measures by the workspace.
    const workspace = element.parentElement;
    const update = () => {
      const width = element.getBoundingClientRect().width || (workspace?.getBoundingClientRect().width ?? 0);
      host.style.setProperty("--client-browser-width", `${Math.round(width)}px`);
    };
    const observer = new ResizeObserver(update);
    observer.observe(element); if (workspace) observer.observe(workspace); update();
    host.dataset.browserOpen = "";
    return () => { observer.disconnect(); delete host.dataset.browserOpen; host.style.removeProperty("--client-browser-width"); };
  }, [placed, host, panel]);
  if (!titlebar) return <>{children}</>;
  return placed && slot ? createPortal(children, slot) : null;
}

// The sidebar's edge can be dragged to any width the window leaves room for;
// the title bar's leading part follows it. Dragging on past the narrowest width
// hides the sidebar, while the widest width is a firm stop; a double click
// restores the default width.
export function ClientSidebarResizeHandle({ label, onHide }: { label: string; onHide: () => void }) {
  const titlebar = useContext(TitlebarHost);
  const [width, setWidth] = useState(sidebarSessionWidth);
  const root = titlebar?.root;
  if (!root) return null;
  const sidebar = () => root.querySelector<HTMLElement>(".clientContent .sidebar");
  const maxWidth = () => Math.max(SIDEBAR_MIN_WIDTH, root.getBoundingClientRect().width - SIDEBAR_CONVERSATION_RESERVE);
  const currentWidth = () => sidebar()?.getBoundingClientRect().width ?? SIDEBAR_DEFAULT_WIDTH;
  const apply = (width: number) => root.style.setProperty("--sidebar-width", `${Math.round(width)}px`);
  const commit = (width: number) => {
    sidebarSessionWidth = Math.round(Math.min(maxWidth(), Math.max(SIDEBAR_MIN_WIDTH, width)));
    apply(sidebarSessionWidth);
    setWidth(sidebarSessionWidth);
  };
  const preview = (state: DragSnap) => {
    if (state) root.dataset.sidebarSnap = state; else delete root.dataset.sidebarSnap;
  };

  const onPointerDown = (event: PointerEvent<HTMLDivElement>) => {
    const element = sidebar();
    if (!element || event.button !== 0) return;
    event.preventDefault();
    const left = element.getBoundingClientRect().left;
    const limit = maxWidth();
    const before = currentWidth();
    const target = event.currentTarget;
    target.setPointerCapture(event.pointerId);
    root.classList.add("resizingSidebar");
    let state: DragSnap = null;
    let latest = before;
    const stop = () => {
      target.removeEventListener("pointermove", move);
      target.removeEventListener("pointerup", stop);
      target.removeEventListener("pointercancel", stop);
      target.removeEventListener("lostpointercapture", stop);
      window.removeEventListener("pointerup", stop);
      window.removeEventListener("blur", stop);
      root.classList.remove("resizingSidebar");
      if (!state) { commit(latest); preview(null); return; }
      commit(before);
      onHide();
      afterCommit(() => preview(null));
    };
    const move = (moveEvent: globalThis.PointerEvent) => {
      // A release outside the renderer (for example over the browser view) still ends the drag.
      if (moveEvent.buttons === 0) return stop();
      const raw = moveEvent.clientX - left;
      state = snapState(raw, SIDEBAR_MIN_WIDTH, Infinity);
      preview(state);
      latest = Math.min(limit, Math.max(SIDEBAR_MIN_WIDTH, raw));
      if (!state) apply(latest);
    };
    target.addEventListener("pointermove", move);
    target.addEventListener("pointerup", stop);
    target.addEventListener("pointercancel", stop);
    target.addEventListener("lostpointercapture", stop);
    window.addEventListener("pointerup", stop);
    window.addEventListener("blur", stop);
  };

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const step = event.shiftKey ? 64 : 16;
    if (event.key === "ArrowRight") commit(currentWidth() + step);
    else if (event.key === "ArrowLeft") commit(currentWidth() - step);
    else if (event.key === "Home") commit(SIDEBAR_MIN_WIDTH);
    else if (event.key === "End") commit(maxWidth());
    else return;
    event.preventDefault();
  };

  return <div className="clientSidebarResizeHandle" role="separator" aria-orientation="vertical"
    aria-label={label} title={label} aria-valuemin={SIDEBAR_MIN_WIDTH}
    aria-valuenow={width ?? SIDEBAR_DEFAULT_WIDTH} tabIndex={0}
    onPointerDown={onPointerDown} onKeyDown={onKeyDown}
    onDoubleClick={() => { sidebarSessionWidth = null; setWidth(null); root.style.removeProperty("--sidebar-width"); }} />;
}
