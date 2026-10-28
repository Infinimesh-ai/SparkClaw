// The desktop client draws its own title bar. A workspace portals its sidebar
// toggle, history controls and actions into it; the web keeps the in-page topbar.
import { createContext, useContext, useEffect, useMemo, useState, type ReactNode, type RefObject } from "react";
import { createPortal } from "react-dom";
import { ArrowLeft, ArrowRight } from "lucide-react";
import type { Language } from "../i18n";
import type { PageHistory } from "../hooks/usePageHistory";
import { desktopCapability } from "../desktop/capability";

type TitlebarSlots = { host: HTMLElement | null; browser: HTMLElement | null; setBrowser: (element: HTMLElement | null) => void };
const TitlebarHost = createContext<TitlebarSlots | undefined>(undefined);

// Page history belongs to the client title bar; the web keeps browser history.
export function useClientTitlebar() {
  return Boolean(useContext(TitlebarHost));
}

export function ClientWindow({ children }: { children: ReactNode }) {
  const [host, setHost] = useState<HTMLElement | null>(null);
  const [browser, setBrowser] = useState<HTMLElement | null>(null);
  const value = useMemo(() => ({ host, browser, setBrowser }), [host, browser]);
  return <div className="clientWindow glassWorkbench">
    <header className="clientTitlebar workbench glassWorkbench" ref={setHost} aria-label="Spark X" />
    <div className="clientContent"><TitlebarHost.Provider value={value}>{children}</TitlebarHost.Provider></div>
  </div>;
}

export function WorkspaceTitlebar({ language, title = "", sidebar, back, actions, navigation, hidden = false }: {
  language: Language;
  // The open conversation's title, shown over the workspace in the client.
  title?: string;
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
      <div className="clientTitlebarActions topbarActions">{actions}</div>
    </div>
    <div className="clientTitlebarTrailing"><div className="clientTitlebarBrowser" ref={titlebar.setBrowser} /></div>
  </>, titlebar.host);
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
    const update = () => host.style.setProperty("--client-browser-width", `${Math.round(element.getBoundingClientRect().width)}px`);
    const observer = new ResizeObserver(update);
    observer.observe(element); update();
    host.dataset.browserOpen = "";
    return () => { observer.disconnect(); delete host.dataset.browserOpen; host.style.removeProperty("--client-browser-width"); };
  }, [placed, host, panel]);
  if (!titlebar) return <>{children}</>;
  return placed && slot ? createPortal(children, slot) : null;
}
