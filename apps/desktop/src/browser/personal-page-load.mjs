const isAborted = (error) => error?.code === "ERR_ABORTED" || error?.errno === -3;

// A site can replace its first document before it finishes loading. Electron
// rejects that loadURL promise even though the replacement is still loading.
// Personal browsing follows the replacement, but only a finished load is success.
export async function loadPersonalPage(webContents, url, { timeoutMS = 30000 } = {}) {
  let finished = false;
  let failure;
  let timer;
  let settle;
  const settled = new Promise((resolve) => { settle = resolve; });
  const onFinished = () => { finished = true; settle(); };
  const onFailed = (_event, errno, code, failedURL, isMainFrame) => {
    if (!isMainFrame || errno === -3) return;
    failure = Object.assign(new Error(`${code} (${errno}) loading '${failedURL}'`), { code, errno, url: failedURL });
    settle();
  };
  const onClosed = () => { settle(); };
  const onStopped = () => {
    // Let a replacement navigation in the same turn start before deciding that
    // the user stopped loading without reaching a usable document.
    queueMicrotask(() => {
      if (webContents.isDestroyed() || !webContents.isLoadingMainFrame()) settle();
    });
  };
  const listeners = {
    "did-finish-load": onFinished,
    "did-fail-load": onFailed,
    "did-stop-loading": onStopped,
    destroyed: onClosed,
    "render-process-gone": onClosed,
  };
  for (const [event, listener] of Object.entries(listeners)) webContents.on(event, listener);
  try {
    await webContents.loadURL(url);
  } catch (error) {
    if (!isAborted(error)) throw error;
    if (!finished && !failure && !webContents.isDestroyed() && webContents.isLoadingMainFrame()) {
      timer = setTimeout(settle, timeoutMS);
      await settled;
    }
    if (failure) throw failure;
    if (!finished || webContents.isDestroyed()) throw error;
  } finally {
    clearTimeout(timer);
    for (const [event, listener] of Object.entries(listeners)) webContents.removeListener(event, listener);
  }
}
