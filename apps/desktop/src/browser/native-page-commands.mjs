import crypto from "node:crypto";

const WORLD = 1004;
const CAPTURE_WIDTH = 480;
const hash = (value) => crypto.createHash("sha256").update(typeof value === "string" ? value : JSON.stringify(value)).digest("hex");
const execute = (record, code) => record.webContents.executeJavaScriptInIsolatedWorld(WORLD, [{ code }]);
// The rendered-content digest a snapshot reports and a stable wait compares.
const CONTENT_TEXT_CHARS = 12000;
export const contentDigest = (title, text) => hash(`${title}\n${text}`);
// Room left for page data inside the 56 KiB result budget after its fields.
const READ_TEXT_BYTES = 40 << 10;
const SNAPSHOT_BYTES = 52 << 10;
const SCREENSHOT_BYTES = 39 << 10;
const STABLE_QUIET_MS = 500;
const STABLE_POLL_MS = 100;
const STABLE_TIMEOUT_MS = 15000;
const encoded = (value) => Buffer.byteLength(JSON.stringify(value).replace(/[<>&\u2028\u2029]/gu, "\\u0000"));
// Whether the owner can currently see this task's page; the requested
// presentation lane is the Gateway's to report.
const surfaceVisible = (record) => record.view?.getVisible?.() !== false && record.webContents?.isDestroyed?.() !== true;
function staleLayout() {
  return Object.assign(new Error("Browser viewport changed; take a fresh snapshot"), { code: "browser_snapshot_stale" });
}
export function assertSnapshotLayout(record, snapshotID) {
  const snapshot = record.lastHostSnapshot;
  if (snapshot?.id === snapshotID && snapshot.layoutRevision !== undefined && snapshot.layoutRevision !== (record.layoutRevision ?? 0)) throw staleLayout();
}
function fitText(text, bytes) {
  if (encoded(text) <= bytes) return text;
  let low = 0, high = text.length;
  while (low < high) { const mid = Math.ceil((low + high) / 2); if (encoded(text.slice(0, mid)) <= bytes) low = mid; else high = mid - 1; }
  return text.slice(0, low);
}

// Every script is owned by this module. The wire supplies only bounded data;
// it cannot select a JS expression, debugger method, selector or file path.
export async function nativePageCommand(record, operation, args, binding, fence) {
  if (["snapshot", "screenshot", "click", "fill", "select"].includes(operation)) {
    const revision = record.layoutRevision ?? 0, leaseFence = fence;
    fence = () => { leaseFence(); if ((record.layoutRevision ?? 0) !== revision) throw staleLayout(); };
  }
  if (["click", "fill", "select"].includes(operation)) assertSnapshotLayout(record, args.snapshot_id);
  fence();
  if (operation === "navigate") {
    await record.webContents.loadURL(args.url);
    fence();
    return { page_id: binding.page_id, url: record.webContents.getURL(), title: record.webContents.getTitle(), page_generation: binding.page_generation, surface_visible: surfaceVisible(record) };
  }
  if (operation === "read") {
    const max = Math.min(args.max_chars || 16384, 24000);
    const page = await execute(record, `(()=>({url:location.href,final_url:location.href,title:document.title,text:(document.body?.innerText||'').slice(0,${max}),ready_state:document.readyState,lang:document.documentElement.lang,rendered:true,content_type:'text/html'}))()`);
    fence();
    const text = fitText(page.text, READ_TEXT_BYTES);
    return { ...page, text, ...(text.length < page.text.length ? { truncated: true } : {}), page_id: binding.page_id, untrusted: true, surface_visible: surfaceVisible(record) };
  }
  if (operation === "snapshot") {
    const snapshotID = `snapshot_${crypto.randomUUID().replaceAll("-", "")}`;
    const result = await execute(record, `(()=>{
      const snapshotID=${JSON.stringify(snapshotID)};const refs=new Map();const controls=[];
      const elements=[...document.querySelectorAll('button,a[href],input,textarea,select,[role="button"],[role="link"],[role="textbox"]')].slice(0,120);
      for(const element of elements){const box=element.getBoundingClientRect();if(!box.width||!box.height)continue;
        const style=getComputedStyle(element);if(style.visibility==='hidden'||style.display==='none')continue;
        const tag=element.tagName.toLowerCase(),type=(element.type||'').toLowerCase();if(['password','file','hidden'].includes(type))continue;
        const name=(element.getAttribute('aria-label')||element.labels?.[0]?.innerText||element.innerText||element.getAttribute('placeholder')||'').trim().slice(0,180);
        const role=element.getAttribute('role')||(tag==='a'?'link':tag==='select'?'combobox':['input','textarea'].includes(tag)?'textbox':'button');
        const uid=snapshotID+':e'+(controls.length+1);refs.set(uid,element);controls.push({ref:uid,uid,role,name,label:name,tag,type,visible:true,enabled:!element.disabled,clickable:['button','link'].includes(role),editable:role==='textbox',value_kind:'text',box:{x:box.x,y:box.y,width:box.width,height:box.height}});
      }
      globalThis.__sparkclawBrowserRefs={snapshotID,refs,url:location.href,width:innerWidth,height:innerHeight};
      return {url:location.href,title:document.title,controls,text:(document.body?.innerText||'').slice(0,${CONTENT_TEXT_CHARS})};
    })()`);
    fence();
    const digest = hash(result); const content = contentDigest(result.title, result.text);
    const previous = record.lastHostSnapshot;
    record.lastHostSnapshot = { id: snapshotID, digest, pageGeneration: binding.page_generation, layoutRevision: record.layoutRevision ?? 0 };
    const sameSession = previous?.pageGeneration === binding.page_generation;
    const all = result.controls.map((control, index) => ({ ...control, ordinal: index + 1, short_ref: `e${index + 1}`, fingerprint: hash(control), snapshot_id: snapshotID }));
    const build = (controls) => {
      const aria = controls.map((control) => `[ref=${control.ref}] ${control.role} ${control.name}`).join("\n");
      const snapshot = { schema_version: "browser_interaction_snapshot_v1", snapshot_id: snapshotID, previous_snapshot_id: sameSession ? previous.id : "", repeated: sameSession && previous.digest === digest && previous.layoutRevision === record.lastHostSnapshot.layoutRevision,
        page_id: binding.page_id, page_generation: binding.page_generation,
        session_generation: binding.page_generation, url: result.url, title: result.title, digest, content_digest: content, controls_total: all.length, controls_returned: controls.length,
        controls, refs: controls, action_refs: controls.filter((control) => control.clickable).map((control) => control.ref), aria, truncated: controls.length < all.length, browser_page_auth_state: "unknown", browser_page_auth_confidence: "unknown" };
      return { snapshot_id: snapshotID, page_id: binding.page_id, digest, content_digest: content, snapshot, text: `Page: ${result.url}\n${aria}`, browser_page_auth_state: "unknown", surface_visible: surfaceVisible(record) };
    };
    // Keep the most relevant leading controls when the page has more than one
    // reverse response can carry; the snapshot says it was truncated.
    let count = all.length, output = build(all);
    while (count > 0 && encoded(output) > SNAPSHOT_BYTES) { count = Math.floor(count / 2); output = build(all.slice(0, count)); }
    return output;
  }
  if (operation === "click") {
    const position = await execute(record, `(()=>{${lookup(args)} const box=element.getBoundingClientRect();const x=box.x+box.width/2,y=box.y+box.height/2;
      if(element.disabled||box.width<=0||box.height<=0||x<0||y<0||x>=innerWidth||y>=innerHeight)throw Error('Element is unavailable');
      const top=document.elementFromPoint(x,y);if(!top||!(top===element||element.contains(top)))throw Error('Element is obscured');return {x,y};})()`);
    fence();
    // A shielded unfocused task view cannot consume sendInputEvent reliably.
    // Main uses only these fixed native input commands on the fenced contents;
    // the broker never exposes a debugger method or parameter escape hatch.
    // The task input shield deliberately prevents owner mouse input. Execute
    // the fixed native DOM activation on this exact referenced embedded node;
    // no selector, script or debugger method is accepted from the command.
    await execute(record, `(()=>{${lookup(args)} if(element.disabled)throw Error('Element is disabled');globalThis.__sparkclawBrowserRefs=null;HTMLElement.prototype.click.call(element);return true;})()`);
    fence();
    return { page_id: binding.page_id, snapshot_id: args.snapshot_id, clicked: args.ref };
  }
  if (operation === "fill" || operation === "select") {
    fence();
    await execute(record, `(()=>{${lookup(args)} const value=${JSON.stringify(args.value)};const tag=element.tagName.toLowerCase();
      if(element.disabled||element.readOnly)throw Error('Element is not editable');
      if(${JSON.stringify(operation)}==='select') {if(tag!=='select'||![...element.options].some(option=>option.value===value&&!option.disabled))throw Error('Invalid option');element.value=value;}
      else {if(!['input','textarea'].includes(tag)||tag==='input'&&!['text','search','email','url','tel','number',''].includes(element.type))throw Error('Field is not a reversible text draft');
        const prototype=tag==='input'?HTMLInputElement.prototype:HTMLTextAreaElement.prototype;Object.getOwnPropertyDescriptor(prototype,'value').set.call(element,value);}
      globalThis.__sparkclawBrowserRefs=null;element.dispatchEvent(new Event('input',{bubbles:true}));element.dispatchEvent(new Event('change',{bubbles:true}));return true;})()`);
    fence(); return { page_id: binding.page_id, snapshot_id: args.snapshot_id, typed: args.ref, draft_only: true };
  }
  if (operation === "screenshot") {
    const data = await captureScreenshot(record, fence);
    // Base64 output must fit the reverse result budget with its fields.
    if (data.length > SCREENSHOT_BYTES) throw new Error("Screenshot exceeds the bounded output budget");
    return { page_id: binding.page_id, data: data.toString("base64"), mimeType: "image/png" };
  }
  if (operation === "wait" && args.mode === "stable_state") return stableState(record, args, binding, fence);
  if (operation === "wait") { await new Promise((resolve) => setTimeout(resolve, args.milliseconds)); fence(); return { page_id: binding.page_id, waited: true }; }
  throw new Error("Browser operation is unavailable");
}
// The page counts as settled when its URL and rendered content stay the same
// for the quiet period, decided here inside the page with no remote polling.
async function stableState(record, args, binding, fence) {
  const started = performance.now(), timeout = Math.min(args.timeout_ms || STABLE_TIMEOUT_MS, STABLE_TIMEOUT_MS);
  const requireReady = args.require_ready_state !== false, allowNoChange = args.allow_no_change === true;
  let last, stableSince = 0, observations = 0;
  for (;;) {
    fence();
    const page = await execute(record, `(()=>({url:location.href,title:document.title,text:(document.body?.innerText||'').slice(0,${CONTENT_TEXT_CHARS}),ready_state:document.readyState}))()`);
    fence(); observations++;
    const digest = contentDigest(page.title, page.text), now = performance.now();
    if (!last || last.url !== page.url || last.digest !== digest) { last = { url: page.url, digest }; stableSince = now; }
    const ready = !requireReady || ["interactive", "complete"].includes(page.ready_state);
    const changed = !args.before_digest || digest !== args.before_digest;
    const base = { page_id: binding.page_id, url: page.url, title: page.title, state_digest: digest, state_changed: changed, observations, quiet_period_ms: STABLE_QUIET_MS, surface_visible: surfaceVisible(record) };
    if (ready && now - stableSince >= STABLE_QUIET_MS && (changed || allowNoChange)) return { status: "stable", ...base };
    if (now - started >= timeout) return { status: "timeout", ...base };
    await new Promise((resolve) => setTimeout(resolve, STABLE_POLL_MS));
  }
}
function lookup(args) {
  return `const state=globalThis.__sparkclawBrowserRefs;if(!state||state.snapshotID!==${JSON.stringify(args.snapshot_id)}||state.url!==location.href||state.width!==innerWidth||state.height!==innerHeight)throw Error('Snapshot is stale');
    const element=state.refs.get(${JSON.stringify(args.ref)});if(!element||!element.isConnected)throw Error('Reference is stale');`;
}

async function captureScreenshot(record, fence) {
  // Hidden task views may have a native surface, or need Chromium to render
  // one. Both requests share one deadline; retain the first valid image.
  if (record.view.getVisible?.() === false) return captureHiddenViewport(record, fence);

  try {
    const image = await captureDeadline(record.webContents.capturePage(), 5000);
    fence();
    if (image.isEmpty()) throw new Error("Browser display surface is unavailable");
    return image.resize({ width: CAPTURE_WIDTH }).toPNG();
  } catch (error) {
    fence();
    return captureTaskViewport(record, fence, error);
  }
}

async function captureHiddenViewport(record, fence) {
  const debuggerSession = record.webContents.debugger;
  if (record.webContents.isDestroyed() || debuggerSession.isAttached()) throw new Error("Browser display surface is unavailable");
  const { width, height } = record.view.getBounds();
  const completed = new AbortController();
  debuggerSession.attach("1.3");
  try {
    const image = await captureDeadline(Promise.any([
      captureNativeSurface(record, fence, completed.signal),
      debuggerSession.sendCommand("Page.captureScreenshot", {
        format: "png", fromSurface: true, captureBeyondViewport: true,
        clip: { x: 0, y: 0, width, height, scale: CAPTURE_WIDTH / width },
      }).then(async result => {
        if (typeof result.data !== "string" || !result.data) throw new Error("Browser screenshot is empty");
        fence();
        const { nativeImage } = await import("electron");
        fence();
        const image = nativeImage.createFromBuffer(Buffer.from(result.data, "base64"));
        if (image.isEmpty()) throw new Error("Browser screenshot is empty");
        return image;
      }),
    ]), 5000);
    fence();
    return image.resize({ width: CAPTURE_WIDTH }).toPNG();
  } finally {
    completed.abort();
    // A later native/CDP result cannot escape after the winner or timeout.
    if (!record.webContents.isDestroyed() && debuggerSession.isAttached()) debuggerSession.detach();
  }
}

async function captureNativeSurface(record, fence, signal) {
  // A never-presented view can reject its first read while Viz creates its
  // surface. Retry only these surface errors within the shared capture budget.
  // This never retries a browser action, changes presentation or extends a lease.
  while (!signal.aborted) {
    fence();
    try {
      const image = await record.webContents.capturePage();
      if (image.isEmpty()) throw new Error("Browser display surface is unavailable");
      return image;
    } catch (error) {
      if (signal.aborted || !/^(UnknownVizError|Current display surface not available for capture|Browser display surface is unavailable)$/u.test(error.message)) throw error;
    }
    await new Promise(resolve => {
      const done = () => { clearTimeout(timer); signal.removeEventListener("abort", done); resolve(); };
      const timer = setTimeout(done, 50);
      signal.addEventListener("abort", done, { once: true });
      if (signal.aborted) done();
    });
  }
  throw new Error("Browser capture completed");
}

async function captureTaskViewport(record, fence, surfaceError = new Error("Browser display surface is unavailable"), timeout = 3000) {
  // Ask Chromium to render only the page's current viewport, leaving the selected
  // conversation, window visibility and owner focus unchanged. No debugger
  // method or parameters are accepted from the wire, and an existing
  // debugger session must never be displaced.
  const debuggerSession = record.webContents.debugger;
  if (record.webContents.isDestroyed() || debuggerSession.isAttached()) throw surfaceError;
  const { width, height } = record.view.getBounds();
  debuggerSession.attach("1.3");
  try {
    const result = await captureDeadline(debuggerSession.sendCommand("Page.captureScreenshot", {
      format: "png", fromSurface: true, captureBeyondViewport: true,
      clip: { x: 0, y: 0, width, height, scale: CAPTURE_WIDTH / width },
    }), timeout);
    fence();
    // Electron display scale differs between Retina and ordinary displays.
    // Normalize both native and Chromium captures to the same output width.
    const { nativeImage } = await import("electron");
    fence();
    const image = nativeImage.createFromBuffer(Buffer.from(result.data, "base64"));
    if (image.isEmpty()) throw new Error("Browser screenshot is empty");
    return image.resize({ width: CAPTURE_WIDTH }).toPNG();
  } finally {
    if (!record.webContents.isDestroyed() && debuggerSession.isAttached()) debuggerSession.detach();
  }
}

async function captureDeadline(promise, milliseconds) {
  let timer;
  try {
    return await Promise.race([promise, new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error("Browser screenshot timed out")), milliseconds);
    })]);
  } finally { clearTimeout(timer); }
}
