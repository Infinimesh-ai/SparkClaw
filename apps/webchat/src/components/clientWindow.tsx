// The desktop client draws its own title bar. A workspace portals its sidebar
// toggle, history controls and actions into it; the web keeps the in-page topbar.
import { createContext, useContext, useMemo, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { ArrowLeft, ArrowRight } from "lucide-react";
import type { Language } from "../i18n";
import type { PageHistory } from "../hooks/usePageHistory";
import { desktopCapability } from "../desktop/capability";

const TitlebarHost = createContext<{ host: HTMLElement | null } | undefined>(undefined);

// Page history belongs to the client title bar; the web keeps browser history.
export function useClientTitlebar() {
  return Boolean(useContext(TitlebarHost));
}

export function ClientWindow({ children }: { children: ReactNode }) {
  const [host, setHost] = useState<HTMLElement | null>(null);
  const value = useMemo(() => ({ host }), [host]);
  return <div className="clientWindow glassWorkbench">
    <header className="clientTitlebar workbench glassWorkbench" ref={setHost} aria-label="Spark X" />
    <div className="clientContent"><TitlebarHost.Provider value={value}>{children}</TitlebarHost.Provider></div>
  </div>;
}

export function WorkspaceTitlebar({ language, sidebar, back, actions, navigation, hidden = false }: {
  language: Language;
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
    <div className="clientTitlebarSpace" aria-hidden="true" />
    <div className="clientTitlebarActions topbarActions">{actions}</div>
  </>, titlebar.host);
}
