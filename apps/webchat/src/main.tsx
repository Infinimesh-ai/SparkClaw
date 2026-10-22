import React from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { LocalWorkbench } from "./desktop/LocalWorkbench";
import { clientStore } from "./desktop/clientStore";
import { DesktopLoginGate } from "./desktop/DesktopLoginGate";
import { ConfirmationProvider } from "./components/confirmation";
import "./styles/app.css";

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <ConfirmationProvider><DesktopLoginGate>{clientStore() ? <LocalWorkbench /> : <App />}</DesktopLoginGate></ConfirmationProvider>
  </React.StrictMode>
);
