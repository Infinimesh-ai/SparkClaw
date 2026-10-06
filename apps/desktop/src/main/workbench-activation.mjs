// macOS reopens an existing app through activate (including Dock clicks),
// while other platforms launch a second instance. Both must restore the same
// workbench after close-to-hide or minimization.
export function bindWorkbenchActivation(app, getWindow) {
  const showWorkbench = () => {
    const window = getWindow();
    if (!window || window.isDestroyed()) return;
    if (window.isMinimized()) window.restore();
    if (!window.isVisible()) window.show();
    window.focus();
  };
  app.on("activate", showWorkbench);
  app.on("second-instance", showWorkbench);
}
