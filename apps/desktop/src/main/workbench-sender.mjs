// Only the workbench's own top frame may call the backend handlers.
export function authorizeWorkbenchSender(event, window) {
  const frame = event.senderFrame;
  if (!window || event.sender !== window.webContents || frame !== window.webContents.mainFrame ||
      !frame || !frame.url.startsWith("sparkclaw-app://workbench/") || new URL(frame.url).host !== "workbench") {
    throw new Error("Desktop authentication sender is not trusted");
  }
}
