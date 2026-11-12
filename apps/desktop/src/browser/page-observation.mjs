import crypto from "node:crypto";
import { setTimeout as delay } from "node:timers/promises";

export const OBSERVATION_SIGNALS = Object.freeze(["url", "ready_state", "rendered_content", "frames"]);
const hash = value => crypto.createHash("sha256").update(JSON.stringify(value)).digest("hex");
const READ_FRAME = `(() => ({title:document.title, ready_state:document.readyState,
  text:(document.body?.innerText || document.documentElement?.innerText || '').slice(0,120000),
  controls:Array.from(document.querySelectorAll('input,textarea,select,button,a,[role],[aria-label],[aria-checked],[aria-selected],[aria-expanded],[aria-pressed]')).filter(e=>e.getClientRects().length).slice(0,2048).map(e=>
    [e.tagName,e.type === 'password' ? '' : String(e.value ?? '').slice(0,2048),e.checked ?? null,e.disabled ?? null,e.readOnly ?? null,
     ...['role','aria-label','aria-checked','aria-selected','aria-expanded','aria-pressed','aria-disabled'].map(a=>e.getAttribute(a)),
     e.selectedOptions ? Array.from(e.selectedOptions).map(o=>[o.value,o.textContent]) : null])}))()`;

export function validateObservationOptions(value, capture = false) {
  const required = capture ? [] : ["timeout_ms", "quiet_period_ms", "poll_interval_ms", "signals"];
  const optional = capture ? ["page_ref", "document_version"] : ["minimum_samples", "before_digest", "allow_no_change"];
  if (!value || typeof value !== "object" || Array.isArray(value) || required.some(k => !Object.hasOwn(value, k)) ||
      Object.keys(value).some(k => ![...required, ...optional].includes(k))) throw new Error("Invalid page observation options");
  if (capture) {
    if (value.page_ref !== undefined && !/^page_[a-f0-9]{32}$/u.test(value.page_ref) ||
        value.document_version !== undefined && !/^[a-f0-9]{64}$/u.test(value.document_version)) throw new Error("Invalid document binding");
    return;
  }
  const bounded = (n, min, max) => Number.isSafeInteger(n) && n >= min && n <= max;
  if (!bounded(value.timeout_ms, 1, 20000) || !bounded(value.quiet_period_ms, 100, 10000) ||
      !bounded(value.poll_interval_ms, 25, value.quiet_period_ms) || !bounded(value.minimum_samples ?? 2, 2, 401) ||
      !Array.isArray(value.signals) || value.signals.length !== OBSERVATION_SIGNALS.length ||
      OBSERVATION_SIGNALS.some(s => !value.signals.includes(s)) ||
      value.before_digest !== undefined && !/^[a-f0-9]{64}$/u.test(value.before_digest) ||
      value.allow_no_change !== undefined && typeof value.allow_no_change !== "boolean") throw new Error("Invalid page observation limits");
}

// Native frame identity and navigation revision live in main, outside page JS.
// The same sampler binds the wait evidence and the later snapshot guard.
export async function capturePageObservation(record, assertCurrent, signal) {
  assertCurrent(); signal?.throwIfAborted();
  const revision = record.documentRevision;
  const frames = record.webContents.mainFrame.framesInSubtree;
  if (!frames.length || frames.length > 64) throw new Error("Page frame coverage unavailable");
  const identities = frames.map(frame => [frame.frameTreeNodeId, frame.processId, frame.routingId, frame.url]);
  const samples = [];
  for (const frame of frames) {
    assertCurrent(); signal?.throwIfAborted();
    let sample;
    try { sample = await frame.executeJavaScript(READ_FRAME); }
    catch (error) {
      if (revision !== record.documentRevision || frame.detached) throw Object.assign(new Error("Document changed"), { code: "document_changed" });
      throw error;
    }
    if (!sample || typeof sample.text !== "string" || typeof sample.title !== "string" ||
        !["loading", "interactive", "complete"].includes(sample.ready_state) || !Array.isArray(sample.controls)) throw new Error("Page signals unavailable");
    samples.push(sample);
  }
  assertCurrent(); signal?.throwIfAborted();
  const current = record.webContents.mainFrame.framesInSubtree.map(frame => [frame.frameTreeNodeId, frame.processId, frame.routingId, frame.url]);
  if (revision !== record.documentRevision || JSON.stringify(current) !== JSON.stringify(identities)) {
    throw Object.assign(new Error("Page changed during observation"), { code: "document_changed" });
  }
  const url = record.webContents.getURL();
  const loading = record.webContents.isLoading();
  if (typeof url !== "string" || url.length > 8192) throw new Error("Page URL unavailable");
  return {
    page_ref: record.pageRef, document_version: hash([record.pageRef, revision, identities]),
    url, title: samples[0].title.slice(0, 1024), ready_state: samples[0].ready_state, loading,
    state_digest: hash(samples.map(({ title, text, controls }) => [title, text, controls])),
    frames_digest: hash(samples.slice(1)), frame_count: frames.length - 1,
    frames_ready: !loading && samples.every(s => ["interactive", "complete"].includes(s.ready_state)),
  };
}

export async function observePage({ capture, assertCurrent, options, signal, now = () => performance.now() }) {
  validateObservationOptions(options);
  const started = now();
  const abort = new AbortController();
  const joined = signal ? AbortSignal.any([signal, abort.signal]) : abort.signal;
  const timer = setTimeout(() => abort.abort(new Error("Observation deadline exceeded")), options.timeout_ms);
  let last, signature = "", stableSince = started, stableSamples = 0, observations = 0;
  try {
    while (now() - started < options.timeout_ms) {
      assertCurrent(); joined.throwIfAborted();
      // Racing only observations is safe: they have no mutation and cannot
      // publish a late result. Every native read also rechecks task ownership.
      let sample;
      try { sample = await boundedCapture(() => capture(joined), joined); }
      catch (error) {
        assertCurrent(); joined.throwIfAborted();
        if (error.code !== "document_changed") throw error;
        signature = ""; stableSamples = 0; stableSince = now();
        await delay(options.poll_interval_ms, undefined, { signal: joined });
        continue;
      }
      assertCurrent(); joined.throwIfAborted();
      if (now() - started >= options.timeout_ms) return result("timeout", "browser_settle_timeout", false);
      observations++;
      const next = JSON.stringify(sample);
      if (next !== signature) { stableSince = now(); stableSamples = 0; signature = next; }
      last = sample; stableSamples++;
      const changed = !options.before_digest || sample.state_digest !== options.before_digest;
      if (sample.frames_ready && ["interactive", "complete"].includes(sample.ready_state) &&
          stableSamples >= (options.minimum_samples ?? 2) && now() - stableSince >= options.quiet_period_ms &&
          (changed || options.allow_no_change)) {
        return result("stable", "browser_target_settled", changed);
      }
      await delay(Math.min(options.poll_interval_ms, Math.max(1, options.timeout_ms - (now() - started))), undefined, { signal: joined });
    }
    return result("timeout", "browser_settle_timeout", false);
  } catch (error) {
    signal?.throwIfAborted();
    assertCurrent();
    return result(abort.signal.aborted ? "timeout" : "failed", abort.signal.aborted ? "browser_settle_timeout" : "browser_observation_unavailable", false);
  } finally { clearTimeout(timer); }
  function result(status, reason_code, state_changed) {
    return { schema_version: 1, status, reason_code, ...(last ?? {}), signals: [...options.signals],
      observations, stable_samples: stableSamples, observed_ms: Math.max(0, Math.floor(now() - started)),
      stable_for_ms: last ? Math.max(0, Math.floor(now() - stableSince)) : 0,
      quiet_period_ms: options.quiet_period_ms, state_changed };
  }
}

function boundedCapture(work, signal) {
  return new Promise((resolve, reject) => {
    const aborted = () => reject(signal.reason);
    signal.addEventListener("abort", aborted, { once: true });
    Promise.resolve().then(work).then(resolve, reject).finally(() => signal.removeEventListener("abort", aborted));
    if (signal.aborted) aborted();
  });
}
