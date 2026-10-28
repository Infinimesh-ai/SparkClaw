// Native fullscreen is separate from the renderer's DOM Fullscreen API.
// Publish it again after reload so the inset title bar never keeps stale space.
export function bindWindowChrome(window, { inset = false } = {}) {
  const publish = () => {
    if (window.isDestroyed() || window.webContents.isDestroyed()) return;
    const fullscreen = window.isFullScreen();
    if (inset) window.setWindowButtonVisibility(!fullscreen);
    window.webContents.send("sparkclaw-window:fullscreen", fullscreen);
  };
  window.on("enter-full-screen", publish);
  window.on("leave-full-screen", publish);
  window.webContents.on("did-finish-load", publish);
  window.once("closed", () => {
    window.removeListener("enter-full-screen", publish);
    window.removeListener("leave-full-screen", publish);
    window.webContents.removeListener("did-finish-load", publish);
  });
}
