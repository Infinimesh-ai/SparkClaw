import React from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { LocalWorkbench } from "./desktop/LocalWorkbench";
import { clientStore } from "./desktop/clientStore";
import { desktopCapability } from "./desktop/capability";
import { DesktopLoginGate } from "./desktop/DesktopLoginGate";
import { ConfirmationProvider } from "./components/confirmation";
import { ClientWindow } from "./components/clientWindow";
import "./styles/app.css";
// The client title bar overrides the shared surfaces, so it loads last.
import "./styles/client-window.css";

const workbench = <ConfirmationProvider><DesktopLoginGate>{clientStore() ? <LocalWorkbench /> : <App />}</DesktopLoginGate></ConfirmationProvider>;

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    {desktopCapability() ? <ClientWindow>{workbench}</ClientWindow> : workbench}
  </React.StrictMode>
);
