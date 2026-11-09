// Opt-in LAN-only qualification of an unchanged installed package. Personal
// browsing stays usable; Controller/legacy reverse probes are explicitly local.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import https from "node:https";
import crypto from "node:crypto";
import { execFileSync } from "node:child_process";
import { pathToFileURL } from "node:url";

const directory = process.env.SPARKCLAW_CONTROLLER_LAN_LAB;
const executablePath = process.env.SPARKCLAW_CONTROLLER_CANDIDATE;
assert.ok(path.isAbsolute(directory || "") && path.basename(directory).startsWith("sx-controller-lan-"));
assert.ok(path.isAbsolute(executablePath || "") && !executablePath.startsWith("/Applications/"));
await fs.mkdir(directory, { recursive: true, mode: 0o700 });
const stat = await fs.lstat(directory);
assert.ok(stat.isDirectory() && !stat.isSymbolicLink() && !(stat.mode & 0o077));
assert.equal(await fs.realpath(directory), directory);
const profile = path.join(directory, "profile"); await fs.mkdir(profile, { recursive: true, mode: 0o700 });
const resources = path.resolve(path.dirname(executablePath), process.platform === "darwin" ? "../Resources" : "resources");
const { _electron } = await import(pathToFileURL(path.join(resources, "browser-controller/node_modules/playwright-core/index.mjs")));
const hash = bytes => crypto.createHash("sha256").update(bytes).digest("hex");
const output = path.join(directory, `controller-lan-${Date.now()}.json`), tlsDirectory = await fs.mkdtemp(path.join(directory, "tls-"));
const evidence = { schema_version: 1, passed: false, checks: {},
  scope: "actual_package_with_synthetic_pinned_HTTPS_LAN_backend", reverse_probes_are_local: true, requests: [] };
const identity = { deployment_id: "controller-lan-deployment", owner_id: "controller-lan-owner", client_id: "controller-lan-client" };
const token = crypto.randomBytes(32).toString("hex"), sockets = new Set();
let server, application, window, installation, checkpoint;
async function until(check, label, timeout = 60000) {
  const deadline = performance.now() + timeout;
  while (performance.now() < deadline) { if (await check()) return; await new Promise(resolve => setTimeout(resolve, 100)); }
  throw Error(`LAN qualification timeout: ${label}`);
}
async function launch() {
  const env = { ...process.env, SPARKCLAW_DESKTOP_USER_DATA_DIR: profile };
  delete env.SPARKCLAW_DESKTOP_ISCP_CONFIG; delete env.SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST; delete env.ELECTRON_RUN_AS_NODE;
  application = await _electron.launch({ executablePath, chromiumSandbox: true, timeout: 45000, env });
  application.process().stderr.on("data", bytes => process.stderr.write(bytes));
  await application.evaluate(async ({ app, session }) => {
    const require = process.getBuiltinModule("node:module").createRequire(app.getAppPath() + "/package.json");
    const { BrowserControllerForwarder } = require(app.getAppPath() + "/src/browser/controller-forwarder.mjs");
    const { ClientControllerRuntime } = require(app.getAppPath() + "/src/browser/controller-runtime.mjs");
    globalThis.__lanControllerStarts = 0;
    const snapshot = BrowserControllerForwarder.prototype.snapshot;
    BrowserControllerForwarder.prototype.snapshot = function() { globalThis.__lanController = this; return snapshot.call(this); };
    const start = ClientControllerRuntime.prototype.start;
    ClientControllerRuntime.prototype.start = function(...args) { globalThis.__lanControllerStarts++; return start.apply(this, args); };
    await session.fromPartition("persist:sparkclaw-browser-default").protocol.handle("https", request => {
      if (new URL(request.url).origin !== "https://controller.fixture.test") throw Error("Unexpected personal fixture origin");
      return new Response('<!doctype html><title>LAN personal page</title><h1>Personal browsing remains available</h1><input aria-label="Personal note">', { headers: { "content-type": "text/html" } });
    });
  });
  await until(() => (window = application.windows().find(page => page.url() === "sparkclaw-app://workbench/index.html")), "workbench");
  await until(async () => window.evaluate(() => Boolean(window.sparkclawDesktop && window.sparkclawClientStore)), "original IPC");
  const runtime = await application.evaluate(({ app, safeStorage }) => ({ packaged: app.isPackaged, executable: process.execPath,
    profile: app.getPath("userData"), versions: process.versions, secure_storage: safeStorage.isEncryptionAvailable() }));
  assert.ok(runtime.packaged && runtime.secure_storage); assert.equal(runtime.executable, executablePath); assert.equal(runtime.profile, profile);
  evidence.runtime ||= runtime;
}
try {
  evidence.asar_sha256 = hash(await fs.readFile(path.join(resources, "app.asar")));
  evidence.controller_manifest_sha256 = hash(await fs.readFile(path.join(resources, "browser-controller/manifest.json")));
  execFileSync("openssl", ["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost,IP:127.0.0.1",
    "-keyout", path.join(tlsDirectory, "key.pem"), "-out", path.join(tlsDirectory, "cert.pem")], { timeout: 15000, stdio: "ignore" });
  const key = await fs.readFile(path.join(tlsDirectory, "key.pem")), cert = await fs.readFile(path.join(tlsDirectory, "cert.pem"));
  server = https.createServer({ key, cert }, async (request, response) => {
    const pathname = new URL(request.url, "https://fixture.invalid").pathname;
    evidence.requests.push({ method: request.method, path: pathname });
    response.setHeader("content-type", "application/json");
    if (request.headers.authorization !== `Bearer ${token}`) { response.writeHead(401).end("{}"); return; }
    if (request.method === "GET" && pathname === "/api/workbench/identity") { response.end(JSON.stringify(identity)); return; }
    if (request.method === "POST" && pathname === "/api/v1/installations") {
      let input = ""; for await (const chunk of request) { input += chunk; if (input.length > 4096) { response.writeHead(413).end("{}"); return; } }
      const value = JSON.parse(input);
      assert.equal(value.schema_version, 1); assert.match(value.installation_id, /^[a-f0-9-]{36}$/u);
      if (installation) assert.equal(value.installation_id, installation);
      installation = value.installation_id;
      response.end(JSON.stringify({ schema_version: 1, installation_id: installation, ...identity })); return;
    }
    if (request.method === "GET" && pathname === "/api/owner") { response.end(JSON.stringify({ id: identity.owner_id, display_name: "LAN fixture", created_at: "", updated_at: "" })); return; }
    if (request.method === "GET" && pathname === "/api/config") { response.end(JSON.stringify({ speech: { default_language: "auto" } })); return; }
    if (request.method === "GET" && pathname === "/readyz") { response.end('{"ok":true}'); return; }
    response.writeHead(404).end("{}");
  });
  server.on("connection", socket => { sockets.add(socket); socket.once("close", () => sockets.delete(socket)); });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  const descriptor = { schema_version: 2, origin: `https://127.0.0.1:${server.address().port}`, deployment_id: identity.deployment_id,
    owner_id: identity.owner_id, tls_ca_pem: cert.toString(), tls_certificate_sha256: hash(new crypto.X509Certificate(cert).raw) };
  await launch();
  await window.evaluate(value => window.sparkclawDesktop.configureBackend(value), descriptor);
  assert.equal((await window.evaluate(value => window.sparkclawDesktop.login(value), token)).state, "connected");
  await window.waitForSelector(".localWorkbench");
  evidence.connection = await window.evaluate(() => window.sparkclawDesktop.localConnection());
  assert.equal(evidence.connection.backend.schema_version, 2); assert.notEqual(evidence.connection.backend.transport, "iscp");
  const checkpointFile = path.join(directory, "checkpoint.json");
  checkpoint = await fs.readFile(checkpointFile, "utf8").then(JSON.parse, error => { if (error.code !== "ENOENT") throw error; return null; });
  if (!checkpoint) {
    checkpoint = await window.evaluate(async () => {
      const s = window.sparkclawClientStore, c = await s.create("LAN retained conversation"), file = await s.saveFile(c.id, "lan-retained.txt", new TextEncoder().encode("LAN retained bytes"));
      const d = await s.draft(c.id); await s.saveDraft(c.id, "Keep the LAN draft.", [file.id], d.revision, d.scope_key);
      return { conversation_id: c.id, file_id: file.id };
    });
    checkpoint.installation_id = installation;
    await fs.writeFile(checkpointFile, JSON.stringify(checkpoint), { mode: 0o600, flag: "wx" });
  }
  assert.equal(installation, checkpoint.installation_id);
  await window.reload(); await window.waitForSelector(".localWorkbench");
  await window.locator(".sessionSelect").filter({ hasText: "LAN retained conversation" }).click();
  const browser = window.getByRole("button", { name: /^(打开浏览器|Open browser)$/u });
  assert.equal(await browser.count(), 1); assert.equal(await browser.isDisabled(), false); await browser.click();
  // Opening the panel itself creates/selects a blank tab asynchronously. Wait
  // for the renderer's selection, then distinguish the explicitly added tab.
  await until(async () => await window.locator('.desktopTabSelect[aria-selected="true"]').count() === 1, "initial personal tab selected");
  const tabsBefore = await window.locator(".desktopTabSelect").count();
  await window.locator(".desktopNewTab").click();
  await until(async () => await window.locator(".desktopTabSelect").count() === tabsBefore + 1 &&
    await window.locator(".desktopTabSelect").last().getAttribute("aria-selected") === "true", "new personal tab selected");
  const address = window.getByRole("textbox", { name: /^(搜索或输入网址|Search or enter an address)$/u });
  await address.fill("https://controller.fixture.test/");
  evidence.navigation_before_enter = { address: await address.inputValue(), pages: (await window.evaluate(() => window.sparkclawDesktop.state())).pages };
  await address.press("Enter");
  await until(async () => (await window.evaluate(() => window.sparkclawDesktop.state())).pages.some(page => page.role === "personal" && page.title === "LAN personal page"), "personal navigation");
  const page = (await window.evaluate(() => window.sparkclawDesktop.state())).pages.find(page => page.role === "personal" && page.title === "LAN personal page");
  await until(async () => (await window.evaluate(() => window.sparkclawDesktop.state())).presentation.presented_page_ref === page.page_ref, "personal presentation");
  const bounds = (await window.evaluate(() => window.sparkclawDesktop.state())).presentation.panel_bounds;
  await window.getByRole("button", { name: /^(加宽浏览器|Widen browser)$/u }).click();
  await until(async () => (await window.evaluate(() => window.sparkclawDesktop.state())).presentation.panel_bounds.width > bounds.width, "widen");
  await window.getByRole("button", { name: /^(恢复浏览器宽度|Restore browser width)$/u }).click();
  await until(async () => (await window.evaluate(() => window.sparkclawDesktop.state())).presentation.panel_bounds.width === bounds.width, "restore width");
  await window.locator(".desktopBrowserWindowActions").getByRole("button", { name: /^(收起浏览器|Collapse browser)$/u }).click();
  await until(async () => !(await window.evaluate(() => window.sparkclawDesktop.state())).presentation.presented_page_ref, "hide");
  await browser.click(); await window.locator(".desktopTabSelect").filter({ hasText: "LAN personal page" }).click();
  await until(async () => (await window.evaluate(() => window.sparkclawDesktop.state())).presentation.presented_page_ref === page.page_ref, "reopen same page");
  evidence.checks.original_personal_tabs_navigation_hide_reopen_widen_restore = true;
  evidence.probes = await application.evaluate(async ({ webContents }, installation) => {
    const agent = globalThis.__lanController, replies = [];
    for (const operation of ["browser.controller.status", "browser.command"]) {
      const reply = await agent.handleReverse({ operation, profile: "sparkclaw.workbench.transport.v2", installation_id: installation, body: { schema_version: 1 } }, { session_id: "lan-probe", authorization_revision: 1 });
      replies.push({ operation, status: reply.status, error_code: reply.error_code });
    }
    const page = webContents.getAllWebContents().find(page => page.getURL() === "https://controller.fixture.test/");
    return { replies, controller_starts: globalThis.__lanControllerStarts, child_absent: agent.runtime.child === null,
      adapter_absent: agent.runtime.adapter === null, personal_sandbox: page.getLastWebPreferences().sandbox, debugger_attached: page.debugger.isAttached() };
  }, installation);
  assert.deepEqual(evidence.probes.replies, [{ operation: "browser.controller.status", status: 503, error_code: "capability_unavailable" }, { operation: "browser.command", status: 403, error_code: "permission_denied" }]);
  assert.equal(evidence.probes.controller_starts, 0); assert.ok(evidence.probes.child_absent && evidence.probes.adapter_absent && evidence.probes.personal_sandbox && !evidence.probes.debugger_attached);
  evidence.checks.LAN_cannot_admit_Controller_or_legacy_reverse_and_personal_page_stays_sandboxed = true;
  await application.close(); application = undefined;
  await launch();
  await until(async () => (await window.evaluate(() => window.sparkclawDesktop.localConnection())).state === "connected", "encrypted credential restart");
  await window.waitForSelector(".localWorkbench");
  assert.equal(installation, checkpoint.installation_id);
  const retained = await window.evaluate(async checkpoint => ({ file: await window.sparkclawClientStore.readFile(checkpoint.file_id), draft: await window.sparkclawClientStore.draft(checkpoint.conversation_id) }), checkpoint);
  assert.equal(Buffer.from(Object.values(retained.file.bytes)).toString(), "LAN retained bytes");
  assert.equal(retained.draft.content, "Keep the LAN draft."); assert.ok(retained.draft.local_file_ids.includes(checkpoint.file_id));
  assert.equal(await window.evaluate(() => fetch(window.sparkclawDesktop.gatewayBase + "/api/owner").then(response => response.status)), 200);
  assert.equal(await application.evaluate(() => globalThis.__lanControllerStarts), 0);
  evidence.checks.same_installation_encrypted_login_file_draft_and_LAN_reads_survive_restart = true;
  await application.close(); application = undefined;
  assert.equal(evidence.requests.filter(row => row.path.startsWith("/api/v1/browser/")).length, 0);
  assert.equal(evidence.requests.filter(row => row.method === "POST" && row.path.startsWith("/api/v1/executions")).length, 0);
  evidence.checks.no_browser_grant_fallback_or_execution_dispatch = true;
  evidence.passed = true;
} catch (error) {
  evidence.error = String(error?.stack || error);
  if (window) evidence.failure_state = await window.evaluate(() => window.sparkclawDesktop.state()).catch(() => null);
  if (window) evidence.failure_address = await window.locator('.desktopAddressBar input').inputValue().catch(() => null);
}
finally {
  if (application) await application.close().catch(error => { evidence.quit_error = String(error); evidence.passed = false; });
  for (const socket of sockets) socket.destroy();
  if (server?.listening) await new Promise(resolve => server.close(resolve));
  await fs.rm(tlsDirectory, { recursive: true, force: true });
  const children = execFileSync("ps", ["-axo", "command="], { encoding: "utf8" }).split("\n").filter(line => line.includes(resources + "/browser-controller/"));
  evidence.checks.no_owned_children_or_HTTPS_listener_after_quit = children.length === 0 && !server?.listening;
  if (!evidence.checks.no_owned_children_or_HTTPS_listener_after_quit) evidence.passed = false;
  await fs.writeFile(output, JSON.stringify(evidence, null, 2) + "\n", { mode: 0o600, flag: "wx" });
  console.log(JSON.stringify({ passed: evidence.passed, output, checks: evidence.checks, error: evidence.error }));
  if (!evidence.passed) process.exitCode = 1;
}
