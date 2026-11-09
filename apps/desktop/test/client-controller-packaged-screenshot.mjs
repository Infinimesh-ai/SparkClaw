import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";

// The owner-facing request uses the existing visual-inspection workflow. Only
// the visited site is synthetic; observers never issue a Controller operation.
export async function qualifyPackagedScreenshot({ application, window, evidence, until }) {
  evidence.instrumented_screenshot_observer = true;
  evidence.screenshot_package = {};
  for (const file of ["app.asar", "browser-controller/manifest.json"]) {
    evidence.screenshot_package[file] = crypto.createHash("sha256").update(await fs.readFile(path.join(evidence.runtime.resources, file))).digest("hex");
  }
  await application.evaluate(async ({ app, nativeImage, session, webContents }) => {
    const require = process.getBuiltinModule("node:module").createRequire(app.getAppPath() + "/package.json");
    const assert = require("node:assert/strict"), crypto = require("node:crypto");
    const { ClientControllerRuntime } = require(app.getAppPath() + "/src/browser/controller-runtime.mjs");
    const { BrowserControllerForwarder } = require(app.getAppPath() + "/src/browser/controller-forwarder.mjs");
    const trace = globalThis.__controllerScreenshotTrace = { images: [], results: [], chunks: [], operations: [], local_errors: [], captures: [], presentations: [] };
    const observe = page => {
      const send = page.debugger.sendCommand.bind(page.debugger);
      page.debugger.sendCommand = async (method, params, sessionID) => {
        if (method !== "Page.captureScreenshot" || page.getURL() !== "https://controller.fixture.test/") return send(method, params, sessionID);
        const capture = { params, started_at: Date.now() }; trace.captures.push(capture);
        try { const result = await send(method, params, sessionID); capture.bytes = Buffer.from(result.data ?? "", "base64").length; return result; }
        catch (error) { capture.error = String(error); throw error; }
        finally { capture.elapsed_ms = Date.now() - capture.started_at; }
      };
    };
    for (const page of webContents.getAllWebContents()) observe(page);
    app.on("web-contents-created", (_event, page) => observe(page));
    const hash = bytes => crypto.createHash("sha256").update(bytes).digest("hex");
    const request = ClientControllerRuntime.prototype.request;
    ClientControllerRuntime.prototype.request = async function(action, body, options) {
      const takingScreenshot = action === "execute" && body.operation === "page.screenshot";
      const registry = this.adapter?.registry;
      const presentation = () => ({ panel_visible: registry.presentation.panelVisible,
        presented_tab: registry.presentation.presented?.tabID ?? null,
        window_visible: registry.presentation.window.isVisible(), window_focused: registry.presentation.window.isFocused(),
        focused_contents: webContents.getFocusedWebContents()?.id ?? null,
        task_views: [...registry.records.values()].filter(record => record.webContents.getURL() === "https://controller.fixture.test/")
          .map(record => ({ tab_id: record.tabID, visible: record.view.getVisible() })) });
      const before = takingScreenshot ? presentation() : null;
      let reply;
      try { reply = await request.call(this, action, body, options); }
      catch (error) { trace.local_errors.push({ action, operation: body?.operation, error: String(error) }); throw error; }
      if (takingScreenshot) {
        if (!reply.payload?.result?.screenshot) {
          trace.local_errors.push({ action, operation: body.operation, status: reply.status, payload: reply.payload });
          return reply;
        }
        const image = reply.payload.result.screenshot, bytes = Buffer.from(image.data_base64, "base64");
        const after = presentation(); trace.presentations.push({ before, after });
        assert.deepEqual(after, before);
        assert.ok(before.task_views.length && before.task_views.every(view => !view.visible));
        assert.equal(image.mime_type, "image/png");
        assert.equal(bytes.subarray(0, 8).toString("hex"), "89504e470d0a1a0a");
        const size = nativeImage.createFromBuffer(bytes).getSize();
        assert.ok(size.width > 0 && size.height > 0);
        trace.images.push({ mime_type: image.mime_type, bytes: bytes.length, sha256: hash(bytes), ...size });
      }
      return reply;
    };
    const results = new Map(), dispatch = BrowserControllerForwarder.prototype.handleReverse;
    BrowserControllerForwarder.prototype.handleReverse = async function(request, authority) {
      const reply = await dispatch.call(this, request, authority);
      trace.operations.push({ operation: request.operation, inner_operation: request.body?.operation,
        status: reply.status, outcome: reply.body?.outcome, error_code: reply.body?.error_code });
      if (request.body?.operation === "page.screenshot") {
        if (reply.body.outcome !== "completed") return reply;
        assert.ok(reply.body.result?.total_bytes > 48 << 10);
        const result = { command_id: request.body.command_id, ...reply.body.result };
        trace.results.push(result); results.set(request.body.command_id, { descriptor: result, chunks: new Map() });
      }
      if (request.operation.endsWith(".chunk") && results.has(request.body.command_id)) {
        assert.equal(reply.status, 200);
        const result = results.get(request.body.command_id), bytes = Buffer.from(reply.body.data_base64, "base64");
        assert.equal(bytes.length, request.body.length);
        result.chunks.set(request.body.offset, bytes);
        trace.chunks.push({ command_id: request.body.command_id, offset: request.body.offset, bytes: bytes.length });
        if ([...result.chunks.values()].reduce((sum, bytes) => sum + bytes.length, 0) === result.descriptor.total_bytes) {
          const payload = Buffer.concat([...result.chunks.entries()].sort(([a], [b]) => a - b).map(([, bytes]) => bytes));
          assert.equal(hash(payload), result.descriptor.sha256);
          const screenshot = JSON.parse(payload).result.screenshot;
          const image = Buffer.from(screenshot.data_base64, "base64");
          assert.equal(screenshot.mime_type, trace.images.at(-1).mime_type);
          assert.equal(hash(image), trace.images.at(-1).sha256);
          result.descriptor.reassembled_image_sha256 = hash(image);
          result.chunks.clear();
        }
      }
      return reply;
    };
    const protocol = session.fromPartition("persist:sparkclaw-browser-default").protocol;
    protocol.unhandle("https");
    await protocol.handle("https", request => {
      assert.equal(new URL(request.url).origin, "https://controller.fixture.test");
      return new Response('<!doctype html><meta charset="utf-8"><title>Packaged screenshot fixture</title><h1>Packaged screenshot evidence</h1><p>Deterministic color texture for large PNG transfer.</p><canvas width="512" height="256" aria-label="Test color texture"></canvas><script>const c=document.querySelector("canvas").getContext("2d"),im=c.createImageData(512,256);let n=123456789;for(let i=0;i<im.data.length;i+=4){for(let k=0;k<3;k++){n^=n<<13;n^=n>>>17;n^=n<<5;im.data[i+k]=n&255}im.data[i+3]=255}c.putImageData(im,0,0)</script>', { headers: { "content-type": "text/html" } });
    });
  });
  const title = `Packaged screenshot ${Date.now()}`;
  const conversation = await window.evaluate(title => window.sparkclawClientStore.create(title), title);
  await window.evaluate(id => window.sparkclawDesktop.selectConversation(id), conversation.id);
  const task = await window.evaluate(async id => {
    const store = window.sparkclawClientStore, task = await store.enqueue(id, "打开 https://controller.fixture.test/ 网页并截图，展示当前页面。", []);
    await store.submit(task.request_id); return task;
  }, conversation.id);
  let state;
  await until(async () => {
    await window.evaluate(id => window.sparkclawClientStore.reconcile(id), task.request_id);
    state = await window.evaluate(id => window.sparkclawClientStore.read(id), conversation.id);
    const current = state.tasks.find(row => row.request_id === task.request_id);
    assert.ok(!current?.approvals?.some(row => row.state === "pending" && row.actionable), "read-only screenshot needs no mutation approval");
    return ["delivered", "failed", "canceled", "unknown"].includes(current?.status);
  }, "packaged screenshot workflow", 120000);
  evidence.tasks.push(state);
  evidence.screenshot_trace = await application.evaluate(() => globalThis.__controllerScreenshotTrace);
  assert.equal(state.tasks.find(row => row.request_id === task.request_id).status, "delivered");
  assert.ok(!state.messages.some(row => row.role === "assistant" && /workflow (?:stopped|is blocked)/iu.test(row.content)));
  const trace = evidence.screenshot_trace;
  assert.equal(trace.images.length, 1); assert.equal(trace.results.length, 1);
  const image = trace.images[0]; assert.ok(image.bytes > 39 << 10);
  assert.ok(trace.chunks.length >= 2);
  assert.equal(trace.results[0].reassembled_image_sha256, image.sha256);
  const file = state.files.find(file => file.sha256 === image.sha256);
  assert.ok(file, "original screenshot bytes must reach the conversation's file store");
  assert.equal(file.content_type, "image/png"); assert.equal(file.size, image.bytes);
  const loaded = await window.evaluate(id => window.sparkclawClientStore.readFile(id), file.id);
  const bytes = Buffer.from(Object.values(loaded.bytes));
  assert.equal(crypto.createHash("sha256").update(bytes).digest("hex"), image.sha256);
  await fs.writeFile(path.join(process.env.SPARKCLAW_CONTROLLER_ISCP_LAB, "evidence/controller-packaged-screenshot.png"), bytes, { mode: 0o600 });
  evidence.screenshot_file = file;
  const desktop = await window.evaluate(() => window.sparkclawDesktop.state());
  const byCommand = (a, b) => a.command_id.localeCompare(b.command_id);
  assert.deepEqual(desktop.browser_host.unknown_writes.sort(byCommand), evidence.unknown_before.sort(byCommand));
  assert.ok(desktop.pages.some(page => page.role === "task" && page.local_conversation_id === conversation.id));
  const pages = await application.evaluate(({ webContents }) => webContents.getAllWebContents().filter(page => page.getURL() === "https://controller.fixture.test/")
    .map(page => ({ sandbox: page.getLastWebPreferences().sandbox, debugger: page.debugger.isAttached() })));
  assert.ok(pages.length && pages.every(page => page.sandbox && !page.debugger));
  evidence.checks.large_native_PNG_over_encrypted_ISCP_matches_delivered_file = true;
  evidence.checks.screenshot_MIME_dimensions_and_payload_hash_preserved = true;
  evidence.checks.read_only_screenshot_workflow_and_original_file_IPC_complete = true;
  evidence.checks.screenshot_release_preserves_task_view_and_prior_fences = true;
  evidence.checks.hidden_screenshot_preserves_panel_window_and_focus = true;
}
