import crypto from 'node:crypto';
import path from 'node:path';
import { ISCPSpeechJournal } from './iscp-speech-journal.mjs';

const ID = /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$/u;
const terminal = event => ['final', 'error', 'cancelled'].includes(event?.event);

// Live PCM belongs to one connection generation. Lifecycle intents and terminal
// results survive disconnects under their original data authority and IDs.
export class ISCPSpeechClient {
  constructor(auth, { root, protector, getScope } = {}) {
    Object.assign(this, { auth, root, protector, getScope });
    this.sessions = new Map(); this.operations = new Map();
  }
  close() {
    this.openingAbort?.abort(); this.opening = false;
    for (const state of this.sessions.values()) state.abort.abort();
    this.sessions.clear();
  }
  #storage() {
    const auth = this.auth;
    const scope = this.getScope?.() ?? (auth.transport?.objectScopeKey && Number.isSafeInteger(auth.status.authorization_revision)
      ? JSON.stringify([auth.transport.objectScopeKey, auth.status.authorization_revision]) : undefined);
    if (!scope) throw new Error('Speech data authority is unavailable');
    if (scope !== this.scope) {
      this.close();
      const root = this.root ?? path.join(auth.userDataDirectory || path.dirname(auth.descriptorPath), 'iscp-speech');
      this.journal = new ISCPSpeechJournal(root, scope, this.protector ?? auth.defaultVault ?? auth.vault);
      this.scope = scope;
    }
    return this.journal;
  }
  #current(journal) { if (journal !== this.#storage()) throw new Error('Speech authorization changed'); }
  #ready() { return this.auth.descriptor?.transport === 'iscp' && this.auth.status.state === 'connected' && this.auth.status.capabilities?.surfaces?.speech_realtime?.enabled === true; }
  async #lifecycle(journal, key, action, body, signal) {
    const flight = journal.scope + ':' + key + ':' + action;
    if (this.operations.has(flight)) return this.operations.get(flight);
    const promise = (async () => {
      let row = journal.read(key), intent = row[action];
      if (intent && JSON.stringify(intent.body) !== JSON.stringify(body)) throw new Error('Speech operation intent differs');
      if (!intent) {
        intent = { id: crypto.randomUUID(), body, attempted: false, not_after: Date.now() + (action === 'finish' ? 12000 : 30000) };
        row = journal.update(key, { [action]: intent });
      }
      if (intent.result) return this.#result(intent.result);
      this.#current(journal);
      let result;
      if (intent.attempted) {
        const receipt = await this.auth.invokeISCP('operations.receipt', {}, { params: { operation_id: intent.id } });
        if (receipt?.operation_id !== intent.id || receipt.state !== 'completed' || !receipt.response) throw new Error('Speech operation outcome is unknown');
        result = receipt.response;
      } else {
        if (Date.now() >= intent.not_after) throw new Error('Original speech operation expired');
        // Unknown writes only reconcile afterwards, even if lookup returns 404.
        intent = { ...intent, attempted: true };
        journal.update(key, { [action]: intent });
        const value = await this.auth.invokeISCP('speech.session.' + action, body, { operationID: intent.id, signal });
        result = { status: 200, body: value };
      }
      journal.update(key, { [action]: { ...intent, result } });
      this.#current(journal);
      return this.#result(result);
    })().finally(() => { if (this.operations.get(flight) === promise) this.operations.delete(flight); });
    this.operations.set(flight, promise); return promise;
  }
  #result(result) {
    if (!Number.isInteger(result.status) || result.status < 200 || result.status >= 300) throw new Error('Speech operation was not accepted');
    return result.body;
  }
  async dispatch(request) {
    if (!request || typeof request !== 'object' || !['open', 'frame', 'events', 'finish', 'cancel'].includes(request.action)) throw new Error('Invalid speech operation');
    const auth = this.auth;
    if ([...this.sessions.values()].some(value => value.generation !== auth.generation)) this.close();
    if (!this.#ready()) {
      // Waiting is bounded by the renderer's original final-result deadline.
      if (request.action === 'events') return { recovering: true };
      throw new Error('Realtime speech is unavailable');
    }
    const journal = this.#storage();
    if (request.action === 'open') {
      if (this.sessions.size || this.opening) throw new Error('Another microphone session is active');
      if (!ID.test(request.request_id) || !ID.test(request.session_id) || typeof request.language !== 'string' || request.language.length > 32) throw new Error('Invalid speech session identity');
      const key = journal.key(request), prior = journal.read(key);
      const generation = auth.generation, abort = new AbortController(); this.openingAbort = abort; this.opening = true;
      const body = { session_id: request.session_id, request_id: request.request_id, language: request.language };
      let result;
      try {
        if (!prior) journal.update(key, { next: 1, samples: 0, cursor: 0, stream_lost: false });
        result = await this.#lifecycle(journal, key, 'open', body, abort.signal);
      } finally { if (this.openingAbort === abort) this.opening = false; }
      this.#current(journal);
      if (generation !== auth.generation || abort.signal.aborted || !ID.test(result?.session_id)) throw new Error('Speech authorization changed');
      const ready = result.ready;
      if (ready?.event !== 'ready' || ready.protocol !== 'sparkclaw.speech.realtime.v1' || ready.format?.sample_rate !== 16000 || ready.format.channels !== 1 || ready.format.bits_per_sample !== 16 || ready.format.frame_ms !== 100 || ready.limits?.max_frame_samples !== 1600 || !Number.isFinite(ready.limits.max_audio_seconds) || ready.limits.max_audio_seconds <= 0 || ready.limits.max_audio_seconds > 300) throw new Error('Invalid speech negotiation');
      journal.update(key, { session_id: result.session_id, max_samples: ready.limits.max_audio_seconds * 16000, ...(prior ? { stream_lost: true } : {}) });
      this.sessions.set(result.session_id, { key, journal, generation, abort, inflight: 0 });
      return result;
    }
    if (!ID.test(request.session_id)) throw new Error('Invalid speech session identity');
    const state = this.sessions.get(request.session_id);
    const row = state?.journal === journal ? journal.read(state.key) : journal.find(request.session_id);
    if (!row) throw new Error('Speech session is unavailable');
    const common = { session_id: request.session_id };
    if (request.action === 'cancel') {
      journal.update(row.key, { stream_lost: true }); state?.abort.abort(); this.sessions.delete(request.session_id);
      return this.#lifecycle(journal, row.key, 'cancel', common);
    }
    if (request.action === 'events') {
      if (!Number.isSafeInteger(request.after) || request.after < 0) throw new Error('Speech event cursor is invalid');
      if (row.terminal) {
        if (request.after < row.terminal_after || request.after > row.cursor) throw new Error('Speech event cursor gap; original terminal is retained');
        return { ...row.terminal, events: row.terminal.events.filter(item => item.sequence > request.after) };
      }
      let result;
      try { result = await auth.invokeISCP('speech.session.events', { ...common, after: request.after }); }
      catch (error) { if (row.finish && (!error.status || error.status >= 500)) return { recovering: true }; throw error; }
      this.#current(journal);
      if (result?.session_id !== request.session_id || !Array.isArray(result.events) || result.events.length > 64) throw new Error('Invalid speech events');
      let sequence = request.after;
      for (const item of result.events) if (!Number.isSafeInteger(item.sequence) || item.sequence !== ++sequence) throw new Error('Speech event sequence differs');
      // Persist the terminal before IPC delivery, and retain it across cancel.
      journal.update(row.key, { cursor: sequence, ...(result.events.some(item => terminal(item.event)) ? { terminal: result, terminal_after: request.after } : {}) });
      return result;
    }
    if (request.action === 'frame') {
      if (!state || row.stream_lost || row.finish || state.inflight >= 2 || !(request.bytes instanceof Uint8Array) || request.bytes.length < 2 || request.bytes.length > 3200 || request.bytes.length % 2 || request.sequence !== row.next || row.samples + request.bytes.length / 2 > row.max_samples) throw new Error('Speech sequence or credit window exceeded');
      state.inflight++;
      journal.update(row.key, { next: row.next + 1, samples: row.samples + request.bytes.length / 2 });
      try {
        const result = await auth.invokeISCP('speech.session.frame', { ...common, sequence: request.sequence, pcm16: Buffer.from(request.bytes).toString('base64') }, { signal: state.abort.signal });
        if (result.accepted_sequence !== request.sequence) throw new Error('Speech acknowledgement differs');
        return result;
      } catch (error) { journal.update(row.key, { stream_lost: true }); state.abort.abort(); throw error; }
      finally { state.inflight--; }
    }
    if (state?.inflight || !row.finish && (row.stream_lost || !state) || request.last_sequence !== row.next - 1 || request.total_samples !== row.samples || !['manual_stop', 'silence_stop', 'max_duration'].includes(request.reason)) throw new Error('Speech finish does not cover captured audio');
    return this.#lifecycle(journal, row.key, 'finish', { ...common, last_sequence: request.last_sequence, total_samples: request.total_samples, reason: request.reason }, state?.abort.signal);
  }
}
