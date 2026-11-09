// Electron does not wait for promises returned by before-quit handlers. Hold
// the first quit until owned processes, previews and stores have been closed.
export function bindDesktopShutdown(app, stop, onError = () => {}) {
  let pending, complete = false;
  const request = () => {
    if (complete) return Promise.resolve();
    if (pending) return pending;
    pending = Promise.resolve().then(stop).then(() => {
      complete = true;
      app.quit();
    }).catch(error => { pending = null; throw error; });
    return pending;
  };
  app.on("before-quit", event => {
    if (complete) return;
    event.preventDefault();
    void request().catch(onError);
  });
  return request;
}
