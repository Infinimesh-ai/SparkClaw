import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { BrowserPanel } from "../../webchat/src/desktop/BrowserPanel";
import { LocalFilesPanel } from "../../webchat/src/desktop/LocalFilesPanel";
import { localFilesCapability } from "../../webchat/src/desktop/localFiles";
import { surfaceEnabled } from "../../webchat/src/desktop/capability";
import { InspectorResizeHandle, useInspectorWidth } from "../../webchat/src/components/inspectorResize";
import { useBrowserLayout } from "../../webchat/src/hooks/useBrowserLayout";
import "../../webchat/src/styles/app.css";
import "../../webchat/src/styles/local-workbench.css";

function LayoutFixture() {
  const [open, setOpen] = useState(!new URLSearchParams(location.search).has("closed"));
  const [modal, setModal] = useState(false);
  const [filesOpen, setFilesOpen] = useState(false);
  const [filesMounted, setFilesMounted] = useState(false);
  const files = localFilesCapability();
  const [browserAvailable, setBrowserAvailable] = useState(true);
  useEffect(() => window.sparkclawDesktop!.onLocalConnection(status => setBrowserAvailable(surfaceEnabled(status, "browser"))), []);
  useEffect(() => { if (!browserAvailable) setOpen(false); }, [browserAvailable]);
  const resize = useInspectorWidth();
  const layout = useBrowserLayout(open || filesOpen, resize.width, browserAvailable || Boolean(files));
  const close = () => { setOpen(false); layout.restoreTrigger(); };
  return <div className="shell workbench localWorkbench">
    <aside className="sidebar" />
    <main ref={layout.workspaceRef} className={`workspace desktopWorkbench ${open ? "withInspector" : ""} ${layout.className} ${resize.resizing ? "resizingInspector" : ""}`} style={resize.style}>
      <header className="topbar"><button ref={layout.triggerRef} id="toggle-panel" onClick={() => { if (filesOpen) { setFilesOpen(false); layout.reveal(); } else if (open && layout.visible) close(); else { layout.reveal(); setOpen(true); } }}>Toggle panel</button><button id="show-modal" onClick={() => setModal(true)}>Modal</button>
        <button id="toggle-files" onClick={() => { setFilesMounted(true); setFilesOpen(!filesOpen); layout.reveal(); }}>Files</button></header>
      <section className="chatColumn" inert={layout.className === "browserFocus"}><textarea id="chat-input" defaultValue="Preserved draft" /></section>
      {layout.visible && <InspectorResizeHandle resize={resize} label="Resize panel" onHide={close} />}
      {layout.mounted && <BrowserPanel language="en" open={open} visible={!filesOpen && layout.visible} onCollapse={close} wide={layout.wide} onWideChange={layout.setWide} />}
      {filesMounted && files && <LocalFilesPanel files={files} open={filesOpen} visible={filesOpen && layout.visible} language="en" wide={layout.wide} onWideChange={layout.setWide} onCollapse={() => setFilesOpen(false)} onShowFiles={layout.reveal} />}
      {modal && <div role="dialog" aria-modal="true"><button id="close-modal" onClick={() => setModal(false)}>Close modal</button></div>}
    </main>
  </div>;
}
createRoot(document.getElementById("root")!).render(<LayoutFixture />);
