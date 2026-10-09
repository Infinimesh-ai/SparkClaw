import { useState } from "react";
import { createRoot } from "react-dom/client";
import { BrowserPanel } from "../../webchat/src/desktop/BrowserPanel";
import "../../webchat/src/styles/app.css";
import "../../webchat/src/styles/local-workbench.css";

// Real panel, styles and preload; the native fixture owns the window and IPC.
function LayoutFixture() {
  const [open, setOpen] = useState(true);
  return (
    <div className="shell workbench localWorkbench">
      <aside className="sidebar" />
      <main className="workspace desktopWorkbench withInspector">
        <header className="topbar"><button id="toggle-panel" onClick={() => setOpen(current => !current)}>Toggle panel</button></header>
        <section className="chatColumn" />
        {open && <BrowserPanel language="en" localConversationID="layout-fixture" toolbar={
          <div className="localBrowserAuthorization"><button className="localBrowserGrant">Authorize this browser</button></div>
        } />}
      </main>
    </div>
  );
}
createRoot(document.getElementById("root")!).render(<LayoutFixture />);
