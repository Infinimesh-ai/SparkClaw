const WORKBENCH_ORIGIN = "sparkclaw-app://workbench";

function origin(raw) {
  try { const url = new URL(raw); return `${url.protocol}//${url.host}`; }
  catch { return ""; }
}

export function configureWorkbenchPermissions(targetSession, targetWindow) {
  const trusted = (contents, requestingOrigin) => contents === targetWindow.webContents &&
    origin(requestingOrigin) === WORKBENCH_ORIGIN;
  targetSession.setPermissionCheckHandler((contents, permission, requestingOrigin, details) => {
    if (!trusted(contents, requestingOrigin)) return false;
    if (permission === "clipboard-sanitized-write") return details?.isMainFrame === true;
    return permission === "media" && details?.mediaType === "audio";
  });
  targetSession.setPermissionRequestHandler((contents, permission, callback, details) => {
    if (!trusted(contents, details?.requestingUrl)) { callback(false); return; }
    if (permission === "clipboard-sanitized-write") { callback(details?.isMainFrame === true); return; }
    const mediaTypes = Array.isArray(details?.mediaTypes) ? details.mediaTypes : [];
    callback(permission === "media" && mediaTypes.includes("audio") && !mediaTypes.includes("video"));
  });
}
