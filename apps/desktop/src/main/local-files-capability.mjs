import { publicError } from './local-files-policy.mjs';

export const LOCAL_FILES_CHANNEL = 'sparkclaw-local-files:invoke';
export class LocalFilesCapability {
  constructor({ ipcMain, window, service }) { Object.assign(this, { ipcMain, window, service }); }
  start() { this.ipcMain.handle(LOCAL_FILES_CHANNEL, (event, request) => this.dispatch(event, request)); return this; }
  async dispatch(event, request) {
    try {
      const denied = { ok: false, error: { code: 'untrusted_sender', message: 'Local files sender is not trusted' } };
      if (!this.trusted(event)) return denied;
      if (request?.schema_version !== 1) return { ok: false, error: { code: 'invalid_request', message: 'Unsupported local files request' } };
      const value = await this.service.dispatch(request);
      return this.trusted(event) ? { ok: true, value } : denied;
    } catch (error) { return { ok: false, error: publicError(error) }; }
  }
  trusted(event) {
    const frame = event.senderFrame; let url;
    try { url = new URL(frame?.url); } catch { return false; }
    return event.sender === this.window.webContents && frame === this.window.webContents.mainFrame && url.protocol === 'sparkclaw-app:' && url.host === 'workbench' && !url.username && !url.password;
  }
  close() { this.ipcMain.removeHandler(LOCAL_FILES_CHANNEL); return this.service.close(); }
}
