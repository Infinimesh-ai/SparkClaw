const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const MAX_DOWNLOADS = 100;
const MAX_BYTES = 110 << 20;

// Page events prove task ownership; Downloads API records supply the native file.
export class TaskDownloads {
  constructor({ chromeAPI, send, ownsTab, now = Date.now, delay = ms => new Promise(resolve => setTimeout(resolve, ms)) }) {
    this.chrome = chromeAPI;
    this.send = send;
    this.ownsTab = ownsTab;
    this.now = now;
    this.delay = delay;
    this.startedAt = now();
    this.downloads = new Map();
    this.created = new Map();
    this.released = new Set();
    this.configured = false;
    this.closed = false;
    this.onCreated = item => { if (this.created.size < 1001) this.created.set(item.id, item); };
    this.chrome.downloads.onCreated.addListener(this.onCreated);
  }

  async command(method, args) {
    if (this.closed || args.length !== 1) throw new Error('Invalid task download command');
    if (method === 'sparkclaw.downloads.configure') {
      if (args[0]?.behavior !== 'allowAndName') throw new Error('Unsupported download behavior');
      this.configured = true;
      return {};
    }
    const state = this.downloads.get(args[0]);
    if (!state) throw new Error('Download is outside the task');
    if (method === 'sparkclaw.downloads.release') {
      if (!state.file || state.progress?.state !== 'completed') throw new Error('Download is not complete');
      await this.#remove(state, state.file);
      return {};
    }
    if (method === 'sparkclaw.downloads.cancel') {
      state.aborted = true;
      await this.#cleanup(state);
      return {};
    }
    throw new Error('Unknown task download command');
  }

  onPageEvent(source, method, params) {
    if (!this.configured || this.closed || !this.ownsTab(source.tabId) || !UUID.test(params?.guid ?? '')) return false;
    if (method === 'Page.downloadWillBegin') {
      if (this.downloads.has(params.guid) || this.downloads.size >= MAX_DOWNLOADS) return true;
      const state = { ...params, tabId: source.tabId, startedAt: this.startedAt, progress: null };
      this.downloads.set(params.guid, state);
      this.send({ method: 'sparkclaw.downloadWillBegin', params: [params] });
      return true;
    }
    if (method !== 'Page.downloadProgress') return false;
    const state = this.downloads.get(params.guid);
    if (!state || state.tabId !== source.tabId || state.finished) return true;
    state.progress = params;
    if (params.receivedBytes > MAX_BYTES || params.totalBytes > MAX_BYTES) {
      state.finished = true;
      state.aborted = true;
      void this.#cleanup(state).catch(() => {});
      this.send({ method: 'sparkclaw.downloadProgress', params: [{ ...params, state: 'canceled' }] });
      return true;
    }
    if (params.state === 'completed') {
      state.finished = true;
      void this.#complete(state, params);
    } else {
      if (params.state === 'canceled') {
        state.finished = true;
        void this.#cleanup(state).catch(() => {});
      }
      this.send({ method: 'sparkclaw.downloadProgress', params: [params] });
    }
    return true;
  }

  async #resolve(state) {
    const items = await this.chrome.downloads.search({ startedAfter: new Date(state.startedAt).toISOString(), limit: 1001 });
    if (items.length >= 1001 || this.created.size >= 1001) throw new Error('Download inventory exceeded');
    const inventory = new Map(this.created);
    for (const item of items) inventory.set(item.id, item);
    const candidates = [...inventory.values()].filter(item => !this.released.has(item.id) &&
      (item.finalUrl || item.url) === state.url && Date.parse(item.startTime) >= state.startedAt);
    // Do not choose by filename, latest item, or completion state when URLs collide.
    if (candidates.length > 1) throw new Error('Download identity is ambiguous');
    if (!candidates.length) return null;
    const item = candidates[0];
    if (!items.some(value => value.id === item.id) ||
        !Number.isSafeInteger(item.id) || typeof item.filename !== 'string') throw new Error('Download file is unavailable');
    return item;
  }

  async #reconcile(state, complete = false) {
    // CDP completion can precede Downloads API completion; wait for the same ID.
    for (let attempt = 0; attempt < 30; attempt++) {
      const file = await this.#resolve(state);
      if (file && (!complete || file.state === 'complete')) return file;
      await this.delay(100);
    }
    throw new Error('Download record did not settle');
  }

  async #remove(state, file) {
    if (!state.removal) state.removal = (async () => {
      if (file.exists !== false && file.state === 'complete') await this.chrome.downloads.removeFile(file.id);
      await this.chrome.downloads.erase({ id: file.id });
      this.released.add(file.id);
      this.created.delete(file.id);
      this.downloads.delete(state.guid);
    })();
    return state.removal;
  }

  async #cleanup(state) {
    if (!state.cleanup) state.cleanup = (async () => {
      const file = state.file ?? await this.#reconcile(state);
      if (file.state === 'in_progress') {
        await this.chrome.downloads.cancel(file.id);
        const current = await this.chrome.downloads.search({ id: file.id });
        if (current.length === 1) return this.#remove(state, current[0]);
      }
      await this.#remove(state, file);
    })();
    try { return await state.cleanup; }
    catch (error) { state.cleanup = null; throw error; }
  }

  async #complete(state, progress) {
    try {
      const file = await this.#reconcile(state, true);
      if (file.state !== 'complete' || file.exists === false || !Number.isSafeInteger(file.fileSize) || file.fileSize < 0 ||
          file.fileSize !== progress.receivedBytes) throw new Error('Download completion is inconsistent');
      state.file = file;
      if (this.closed || state.aborted) { await this.#cleanup(state); return; }
      if (!this.closed) this.send({ method: 'sparkclaw.downloadProgress', params: [{ ...progress, filename: file.filename, bytes: file.fileSize }] });
    } catch {
      await this.#cleanup(state).catch(() => {});
      if (!this.closed) this.send({ method: 'sparkclaw.downloadProgress', params: [{ ...progress, state: 'canceled' }] });
    }
  }

  close() {
    if (this.closed) return;
    this.closed = true;
    this.chrome.downloads.onCreated.removeListener(this.onCreated);
    for (const state of this.downloads.values()) {
      state.aborted = true;
      void this.#cleanup(state).catch(() => {});
    }
  }
}
