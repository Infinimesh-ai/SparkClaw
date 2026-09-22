import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs/promises";
import http from "node:http";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";

import { PlaywrightMCPClientFactory } from "../../../tools/browser-controller/src/mcp-client.mjs";
import {
  electronConnectionEnvironment,
  registerElectronConnection,
} from "../../../tools/browser-controller/src/electron-adapter-client.mjs";

const TEST_DIR = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(TEST_DIR, "..", "..", "..");
const ELECTRON = path.join(ROOT, "node_modules", "electron", "dist", "electron");
const MAIN = path.join(ROOT, "apps", "desktop", "src", "main", "main.mjs");
const LAUNCHER = path.join(ROOT, "apps", "desktop", "bin", "sparkclaw-electron-browser.mjs");
const CLI = path.join(ROOT, "tools", "browser-controller", "node_modules", "@playwright", "cli", "playwright-cli.js");
const INPUT_HELPER = path.join(TEST_DIR, "x11-send-input.py");
const TOKEN = "phase-one-qualification-token-private";

const temporary = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-electron-phase1-"));
const adapter = {
  socketPath: path.join(temporary, "electron-adapter.sock"),
  secretPath: path.join(temporary, "adapter-secret"),
};
const userDataDir = path.join(temporary, "user-data");
let xvfb;
let electron;
let fixtureServer;
let fixtureServerOrigin = "";
let mcp;
let cliSession = `sc-electron-${process.pid}`;
const evidence = {
  schema_version: 1,
  isolated_display: false,
  disposable_user_data: userDataDir,
  versions: {},
  mcp: {},
  cli: {},
  isolation: {},
  observation: {},
  login: { provider_website_tested: false, local_session_cookie_persisted: false },
};

try {
  progress("starting_xvfb");
  const display = await startXvfb();
  evidence.isolated_display = true;
  progress("starting_fixture");
  const fixture = await startFixture();
  fixtureServer = fixture.server;
  progress("starting_electron");
  const ready = await startElectron(display);
  evidence.versions = {
    electron: ready.electron_version,
    chromium: ready.chromium_version,
    node: ready.node_version,
    architecture: ready.architecture,
  };
  assert.equal(ready.runtime_kind, "electron");
  assert.equal(ready.qualification, true);

  const initialSecret = (await fs.readFile(adapter.secretPath, "utf8")).trim();
  const hiddenWindow = await qualificationLifecycle(initialSecret, "close");
  assert.equal(hiddenWindow.visible, false);
  const shownWindow = await qualificationLifecycle(initialSecret, "show");
  assert.equal(shownWindow.visible, true);
  await qualificationLifecycle(initialSecret, "crash_workbench");
  await waitFor(async () => {
    const state = await qualificationLifecycle(initialSecret, "status");
    return !state.workbench_crashed && Boolean(state.workbench_url);
  }, 5000);
  evidence.lifecycle = {
    close_to_hide: true,
    same_runtime_reopened: true,
    workbench_renderer_recovered: true,
    task_renderer_invalidated: false,
    main_restart_generation_invalidated: false,
    browser_session_persisted_across_restart: false,
    unknown_effects_replayed: false,
  };

  progress("opening_personal_page");
  const secret = (await fs.readFile(adapter.secretPath, "utf8")).trim();
  const scriptProof = await adapterRequest({
    schema_version: 1,
    operation: "openPersonalPage",
    runtime_kind: "electron",
    runtime_secret: secret,
    url: `${fixture.origin}/script-proof`,
  });
  await waitFor(async () => {
    const state = await managedScriptSnapshot(secret);
    return state.statuses.some((status) => status.page_ref === scriptProof.page_ref && status.state === "ready") &&
      state.values["persistent-proof"] === "stored" && state.menus.some((menu) => menu.page_ref === scriptProof.page_ref);
  }, 5000);
  const firstScriptState = await managedScriptSnapshot(secret);
  assert.equal(firstScriptState.values["previous-proof"], "missing");
  const secondScriptProof = await adapterRequest({
    schema_version: 1,
    operation: "openPersonalPage",
    runtime_kind: "electron",
    runtime_secret: secret,
    url: `${fixture.origin}/script-proof`,
  });
  await waitFor(async () => (await managedScriptSnapshot(secret)).statuses.some(
    (status) => status.page_ref === secondScriptProof.page_ref && status.state === "ready",
  ), 5000);
  const secondScriptState = await managedScriptSnapshot(secret);
  assert.equal(secondScriptState.values["previous-proof"], "stored");
  const command = secondScriptState.menus.find((menu) => menu.page_ref === secondScriptProof.page_ref);
  assert.ok(command);
  await adapterRequest({
    schema_version: 1,
    operation: "qualificationInvokeScriptMenu",
    runtime_secret: secret,
    page_ref: secondScriptProof.page_ref,
    command_id: command.command_id,
  });
  await waitFor(async () => (await managedScriptSnapshot(secret)).values["menu-proof"] === "invoked", 5000);
  evidence.scripts = {
    document_start_main_world: true,
    gm_persistence: true,
    gm_menu_command: true,
  };
  await adapterRequest({
    schema_version: 1,
    operation: "openPersonalPage",
    runtime_kind: "electron",
    runtime_secret: secret,
    url: `${fixture.origin}/set-cookie`,
  });

  progress("opening_mcp");
  const factory = new PlaywrightMCPClientFactory({
    executablePath: LAUNCHER,
    userDataDir,
    electronAdapter: adapter,
    outputRoot: path.join(temporary, "mcp-output"),
    connectTimeoutMS: 20_000,
    actionTimeoutMS: 15_000,
    navigationTimeoutMS: 20_000,
  });
  await factory.prepare();
  mcp = await factory.open({
    token: TOKEN,
    sessionID: sessionID(1),
    taskID: "phase1-mcp",
    controllerGeneration: 101,
    sessionGeneration: 1,
    pageGeneration: 1,
  });
  progress("mcp_create_task_page");
  await mcp.createTaskPage();
  progress("mcp_task_page_created");
  const initialPages = await mcp.execute("tabs.list", {});
  assert.equal(initialPages.pages.length, 1, "personal pages leaked into the task lane");
  const initialTaskPageID = initialPages.pages[0].page_id;
  await mcp.execute("page.navigate", { url: `${fixture.origin}/fixture` });
  progress("mcp_navigated");
  await waitFor(async () => {
    const page = (await qualificationSnapshot(secret)).pages.find((candidate) => candidate.url === `${fixture.origin}/fixture`);
    return page?.child_target_types?.includes("worker") && page.child_target_types.includes("iframe");
  }, 5000);
  const initialRead = await mcp.execute("page.read", {});
  assert.match(initialRead.page.text, /Cookie: shared=electron/u);
  assert.match(initialRead.page.text, /Worker: ready/u);
  evidence.login.local_session_cookie_persisted = true;

  const snapshot = await mcp.execute("page.snapshot", { boxes: true });
  if (!findRef(snapshot.snapshot, "Increment") || !findRef(snapshot.snapshot, "Name")) {
    process.stderr.write(`MCP snapshot shape: ${JSON.stringify(snapshot.snapshot).slice(0, 8000)}\n`);
  }
  const buttonRef = findRef(snapshot.snapshot, "Increment");
  const inputRef = findRef(snapshot.snapshot, "Name");
  assert.ok(buttonRef && inputRef, "fixture refs were not exposed by actual MCP");

  await run("python3", [INPUT_HELPER, "click", "820", "130"], { env: { ...process.env, DISPLAY: display } });
  await delay(250);
  let afterInput = await mcp.execute("page.read", {});
  assert.match(afterInput.page.text, /Count: 0/u, "native pointer input reached the task page");
  await run("python3", [INPUT_HELPER, "click-key", "820", "245", "38"], { env: { ...process.env, DISPLAY: display } });
  await run("python3", [INPUT_HELPER, "right-click", "820", "130"], { env: { ...process.env, DISPLAY: display } });
  await adapterRequest({
    schema_version: 1,
    operation: "qualificationSetClipboard",
    runtime_secret: secret,
    text: "native-paste-must-not-arrive",
  });
  await run("python3", [INPUT_HELPER, "chord", "820", "245", "37", "55"], { env: { ...process.env, DISPLAY: display } });
  await run("python3", [INPUT_HELPER, "chord", "820", "245", "37", "38"], { env: { ...process.env, DISPLAY: display } });
  await run("python3", [INPUT_HELPER, "drag", "820", "130", "1040", "320"], { env: { ...process.env, DISPLAY: display } });
  await run("python3", [INPUT_HELPER, "chord", "820", "245", "37", "50", "30"], { env: { ...process.env, DISPLAY: display } });
  await run("python3", [INPUT_HELPER, "keys", "820", "245", "13", "26", "11", "40", "36"], { env: { ...process.env, DISPLAY: display } });
  await delay(250);
  afterInput = await mcp.execute("page.read", {});
  assert.match(afterInput.page.text, /Typed: empty/u, "native keyboard input reached the task page");
  assert.match(afterInput.page.text, /Context: 0 Shortcut: 0 Paste: 0 Drop: 0/u,
    "a blocked native input route reached the task page");

  await mcp.execute("page.click", { ref: buttonRef });
  let read = await mcp.execute("page.read", {});
  assert.match(read.page.text, /Count: 1/u, "approved MCP click did not reach the task page");
  const inputSnapshot = await mcp.execute("page.snapshot", {});
  await mcp.execute("page.fill", { ref: findRef(inputSnapshot.snapshot, "Name"), text: "adapter-proof" });
  read = await mcp.execute("page.read", {});
  assert.match(read.page.text, /Typed: adapter-proof/u);
  const popupSnapshot = await mcp.execute("page.snapshot", {});
  progress("mcp_opening_popup");
  await mcp.execute("page.click", { ref: findRef(popupSnapshot.snapshot, "Open popup") });
  progress("mcp_popup_clicked");
  const popupPages = await mcp.execute("tabs.list", {});
  progress("mcp_popup_listed");
  const popupPage = popupPages.pages.find((page) => page.url === `${fixture.origin}/popup`);
  assert.ok(popupPage, "task popup was not adopted by the exact task owner");
  const popupRead = await mcp.execute("page.read", { page_id: popupPage.page_id });
  assert.match(popupRead.page.text, /Task popup/u);
  await mcp.execute("tabs.close", { page_id: popupPage.page_id });
  progress("mcp_popup_closed");
  const downloadSnapshot = await mcp.execute("page.snapshot", {});
  await mcp.execute("page.click", { ref: findRef(downloadSnapshot.snapshot, "Download fixture") });
  const downloadArtifact = await waitForValue(
    () => findFileContaining(path.join(temporary, "mcp-output"), "sparkclaw electron download proof\n"),
    5000,
  );
  assert.ok(downloadArtifact.startsWith(path.join(temporary, "mcp-output") + path.sep));
  await waitFor(async () => (await listRegularFiles(path.join(userDataDir, "task-downloads"))).length === 0, 5000);
  const screenshot = await mcp.execute("page.screenshot", { type: "png" });
  assert.equal(screenshot.screenshot.mime_type, "image/png");
  assert.ok(screenshot.screenshot.data_base64.length > 100);
  evidence.mcp = {
    actual_client: true,
    navigate: true,
    read: true,
    snapshot: true,
    fill: true,
    click: true,
    screenshot: true,
    download_artifact: true,
    download_source_cleaned: true,
  };
  evidence.isolation = {
    personal_target_hidden: true,
    native_pointer_blocked: true,
    native_keyboard_blocked: true,
    native_context_menu_blocked: true,
    native_shortcuts_blocked: true,
    native_paste_blocked: true,
    native_drag_drop_blocked: true,
    native_ime_sequence_blocked: true,
    javascript_dialog_dismissed: true,
    devtools_closed: true,
    automation_input_working: true,
  };
  evidence.targets = {
    worker_scoped: true,
    oopif_scoped: true,
    popup_adopted: true,
  };

  const firstPage = (await qualificationSnapshot(secret)).pages.find((page) => page.url === `${fixture.origin}/fixture`);
  assert.ok(firstPage);
  const openedSecondPages = await mcp.execute("tabs.new", { url: `${fixture.origin}/second` });
  const secondTaskPageID = openedSecondPages.pages.find((page) => page.url === `${fixture.origin}/second`).page_id;
  const secondRead = await mcp.execute("page.read", {});
  assert.match(secondRead.page.text, /Second task page/u);
  const taskPages = (await qualificationSnapshot(secret)).pages.filter((page) => page.role === "task");
  const secondPage = taskPages.find((page) => page.url === `${fixture.origin}/second`);
  assert.ok(secondPage);
  const beforeBounds = firstPage.bounds;
  await adapterRequest({ schema_version: 1, operation: "qualificationHideTask", runtime_secret: secret });
  const hiddenSnapshot = await mcp.execute("page.snapshot", { page_id: initialTaskPageID });
  await mcp.execute("page.click", { page_id: initialTaskPageID, ref: findRef(hiddenSnapshot.snapshot, "Increment") });
  const hiddenRead = await mcp.execute("page.read", { page_id: initialTaskPageID });
  assert.match(hiddenRead.page.text, /Count: 2/u, "hidden task automation stopped running");
  await qualificationShow(secret, firstPage.page_ref);
  await qualificationShow(secret, secondPage.page_ref);
  const afterSwitch = await qualificationSnapshot(secret);
  const firstAfter = afterSwitch.pages.find((page) => page.page_ref === firstPage.page_ref);
  const secondAfter = afterSwitch.pages.find((page) => page.page_ref === secondPage.page_ref);
  assert.deepEqual(firstAfter.bounds, beforeBounds);
  assert.equal(firstAfter.page_ref, firstPage.page_ref);
  assert.equal(secondAfter.presented, true);
  evidence.observation = {
    opaque_target_stable: true,
    viewport_bounds_stable: true,
    switching_did_not_replace_target: true,
    hidden_background_execution: true,
  };
  await mcp.execute("tabs.close", { page_id: secondTaskPageID });
  await assert.rejects(mcp.execute("tabs.handoff", { page_id: "page_1" }));
  const taskPageBeforeBoundaryTests = (await qualificationSnapshot(secret)).pages.find(
    (page) => page.url === `${fixture.origin}/fixture`,
  );
  const devToolsAttempt = await adapterRequest({
    schema_version: 1,
    operation: "qualificationOpenDevTools",
    runtime_secret: secret,
    page_ref: taskPageBeforeBoundaryTests.page_ref,
  });
  assert.equal(devToolsAttempt.opened, false, "task DevTools remained open");
  await adapterRequest({
    schema_version: 1,
    operation: "qualificationTriggerDialog",
    runtime_secret: secret,
    page_ref: taskPageBeforeBoundaryTests.page_ref,
  });
  await delay(500);
  const dialogState = (await qualificationSnapshot(secret)).pages.find(
    (page) => page.page_ref === taskPageBeforeBoundaryTests.page_ref,
  );
  assert.equal(dialogState.dialog_dismissals, 1, `task dialog dismissal failed: ${dialogState.dialog_error}`);
  await mcp.closeTaskPage();
  await mcp.close();
  mcp = null;

  progress("opening_cli");
  const cliBinding = {
    task_id: "phase1-cli",
    session_id: sessionID(2),
    controller_generation: 102,
    session_generation: 2,
    page_generation: 1,
  };
  const cliConnection = await registerElectronConnection({ adapter, token: TOKEN, binding: cliBinding });
  const cliEnv = {
    ...process.env,
    ...electronConnectionEnvironment(cliConnection),
    PLAYWRIGHT_MCP_EXTENSION_TOKEN: TOKEN,
    PLAYWRIGHT_MCP_EXECUTABLE_PATH: LAUNCHER,
    PLAYWRIGHT_MCP_USER_DATA_DIR: userDataDir,
    PLAYWRIGHT_MCP_TIMEOUT_ACTION: "15000",
    PLAYWRIGHT_MCP_TIMEOUT_NAVIGATION: "20000",
    NO_COLOR: "1",
    NO_UPDATE_NOTIFIER: "1",
  };
  await cli(["--json", `-s=${cliSession}`, "attach", "--extension=chromium"], cliEnv);
  await cli(["--raw", `-s=${cliSession}`, "goto", `${fixture.origin}/fixture`], cliEnv);
  const cliSnapshot = await cli(["--raw", `-s=${cliSession}`, "snapshot"], cliEnv);
  const cliInputRef = findRef(cliSnapshot, "Name");
  const cliButtonRef = findRef(cliSnapshot, "Increment");
  if (!cliInputRef || !cliButtonRef) process.stderr.write(`CLI snapshot shape: ${cliSnapshot.slice(0, 8000)}\n`);
  assert.ok(cliInputRef && cliButtonRef);
  await cli(["--raw", `-s=${cliSession}`, "fill", cliInputRef, "cli-proof"], cliEnv);
  await cli(["--raw", `-s=${cliSession}`, "click", cliButtonRef], cliEnv);
  const cliRead = await cli(["--raw", `-s=${cliSession}`, "eval",
    "() => ({count: window.pointerCount, value: document.querySelector('#name').value})"], cliEnv);
  assert.deepEqual(JSON.parse(cliRead), { count: 1, value: "cli-proof" });
  const cliShot = await cli(["--raw", `-s=${cliSession}`, "screenshot"], cliEnv);
  assert.match(cliShot, /\.png/u);
  await cli(["--raw", `-s=${cliSession}`, "tab-new", `${fixture.origin}/second`], cliEnv);
  const cliTabs = await cli(["--raw", `-s=${cliSession}`, "tab-list"], cliEnv);
  assert.match(cliTabs, /Second/u);
  await cli(["--raw", `-s=${cliSession}`, "tab-close", "1"], cliEnv);
  await cli(["--json", `-s=${cliSession}`, "close"], cliEnv);
  evidence.cli = {
    actual_client: true,
    attach: true,
    navigate: true,
    snapshot: true,
    fill: true,
    click: true,
    evaluate_read: true,
    screenshot: true,
    open_close_page: true,
  };

  progress("injecting_task_renderer_failure");
  mcp = await factory.open({
    token: TOKEN,
    sessionID: sessionID(3),
    taskID: "phase1-crash",
    controllerGeneration: 103,
    sessionGeneration: 3,
    pageGeneration: 1,
  });
  await mcp.createTaskPage();
  await mcp.execute("page.navigate", { url: `${fixture.origin}/fixture` });
  const crashPage = (await qualificationSnapshot(secret)).pages.find((page) => page.task_id === "phase1-crash" && page.url === `${fixture.origin}/fixture`);
  assert.ok(crashPage);
  await adapterRequest({
    schema_version: 1,
    operation: "qualificationCrashTask",
    runtime_secret: secret,
    page_ref: crashPage.page_ref,
  });
  await waitFor(async () => !(await qualificationSnapshot(secret)).pages.some((page) => page.task_id === "phase1-crash"), 5000);
  await assert.rejects(mcp.execute("tabs.list", {}));
  await mcp.close();
  mcp = null;
  evidence.lifecycle.task_renderer_invalidated = true;

  progress("injecting_main_process_failure");
  await qualificationLifecycle(secret, "flush_browser_state");
  await killChild(electron);
  const restarted = await startElectron(display);
  assert.notEqual(restarted.runtime_generation, ready.runtime_generation);
  const restartedSecret = (await fs.readFile(adapter.secretPath, "utf8")).trim();
  assert.equal((await qualificationSnapshot(restartedSecret)).pages.some((page) => page.role === "task"), false);
  evidence.lifecycle.main_restart_generation_invalidated = true;
  mcp = await factory.open({
    token: TOKEN,
    sessionID: sessionID(4),
    taskID: "phase1-restart-cookie",
    controllerGeneration: 104,
    sessionGeneration: 4,
    pageGeneration: 1,
  });
  await mcp.createTaskPage();
  await mcp.execute("page.navigate", { url: `${fixture.origin}/fixture` });
  assert.match((await mcp.execute("page.read", {})).page.text, /Cookie: shared=electron/u);
  await mcp.closeTaskPage();
  await mcp.close();
  mcp = null;
  evidence.lifecycle.browser_session_persisted_across_restart = true;

  progress("complete");
  process.stdout.write(`${JSON.stringify({ event: "sparkclaw_electron_phase1_qualified", evidence })}\n`);
} finally {
  await mcp?.close().catch(() => {});
  await run(process.execPath, [CLI, "--json", `-s=${cliSession}`, "close"], { allowFailure: true }).catch(() => {});
  await stopChild(electron);
  await stopServer(fixtureServer);
  await stopChild(xvfb);
  await fs.rm(temporary, { recursive: true, force: true });
}

async function startXvfb() {
  const child = spawn("Xvfb", ["-displayfd", "3", "-screen", "0", "1440x900x24", "-nolisten", "tcp", "-noreset"], {
    stdio: ["ignore", "ignore", "pipe", "pipe"],
  });
  xvfb = child;
  const displayNumber = await readLine(child.stdio[3], child, 5000);
  assert.match(displayNumber, /^[0-9]+$/u);
  assert.equal(child.exitCode, null);
  return `:${displayNumber}`;
}

async function startElectron(display) {
  const runtimeDirectory = path.join(temporary, "runtime");
  await fs.mkdir(runtimeDirectory, { mode: 0o700, recursive: true });
  const descriptorPath = path.join(runtimeDirectory, "local-workbench.json");
  const credentialPath = path.join(runtimeDirectory, "desktop-client.json");
  await fs.writeFile(descriptorPath, JSON.stringify({
    schema_version: 1, origin: fixtureServerOrigin, deployment_id: "qualification-deployment",
  }), { mode: 0o600 });
  await fs.writeFile(credentialPath, JSON.stringify({
    schema_version: 1, deployment_id: "qualification-deployment", client_id: "qualification-desktop",
    owner_id: "owner", client_name: "SparkClaw Desktop Qualification", token: TOKEN,
  }), { mode: 0o600 });
  const child = spawn(ELECTRON, [MAIN, "--qualification", "--disable-gpu"], {
    cwd: ROOT,
    env: {
      ...process.env,
      DISPLAY: display,
      SPARKCLAW_DESKTOP_USER_DATA_DIR: userDataDir,
      SPARKCLAW_ELECTRON_ADAPTER_SOCKET: adapter.socketPath,
      SPARKCLAW_ELECTRON_ADAPTER_SECRET_FILE: adapter.secretPath,
      SPARKCLAW_DESKTOP_QUALIFICATION_ORIGIN: fixtureServerOrigin,
      SPARKCLAW_DESKTOP_CONNECTION_FILE: descriptorPath,
      SPARKCLAW_DESKTOP_CREDENTIAL_FILE: credentialPath,
    },
    stdio: ["ignore", "pipe", "pipe"],
  });
  electron = child;
  child.stderr.on("data", () => {});
  const line = await readMatchingJSON(child.stdout, child, "sparkclaw_electron_ready", 15_000);
  const socketStat = await fs.lstat(adapter.socketPath);
  const secretStat = await fs.lstat(adapter.secretPath);
  assert.equal(socketStat.isSocket(), true);
  assert.equal(socketStat.mode & 0o077, 0);
  assert.equal(secretStat.isFile(), true);
  assert.equal(secretStat.mode & 0o077, 0);
  return line;
}

async function startFixture() {
  const server = http.createServer((request, response) => {
    if (request.url === "/api/workbench/identity") {
      if (request.headers.authorization !== `Bearer ${TOKEN}`) return response.writeHead(401).end();
      response.writeHead(200, { "content-type": "application/json", "cache-control": "no-store" });
      response.end(JSON.stringify({ deployment_id: "qualification-deployment", owner_id: "owner", client_id: "qualification-desktop" }));
      return;
    }
    if (request.url === "/set-cookie") {
      response.writeHead(200, {
        "content-type": "text/html",
        "set-cookie": "shared=electron; Path=/; SameSite=Lax; Max-Age=604800",
      });
      response.end("<!doctype html><title>Personal enrollment</title>cookie enrolled");
      return;
    }
    if (request.url === "/fixture") {
      response.writeHead(200, { "content-type": "text/html; charset=utf-8" });
      response.end(`<!doctype html><meta charset="utf-8"><title>Electron adapter fixture</title>
        <style>body{font:16px sans-serif;margin:20px}button{display:block;width:220px;height:100px}
        input{display:block;margin-top:20px;width:220px;height:48px}</style>
        <button id="native-target" onclick="window.pointerCount++;render()">Increment</button>
        <input id="name" aria-label="Name"><a href="/download" download="fixture.txt">Download fixture</a>
        <button id="dialog" onclick="setTimeout(()=>document.querySelector('#dialog-state').textContent='Dialog: '+(confirm('blocked task dialog')?'accepted':'dismissed'),0)">Open dialog</button>
        <a id="popup" href="/popup" target="_blank">Open popup</a>
        <p id="state"></p><p id="events"></p><p id="worker-state">Worker: pending</p><p id="dialog-state">Dialog: none</p><p id="viewport"></p>
        <iframe src="http://localhost:${server.address().port}/oopif" title="OOPIF"></iframe>
        <script>window.pointerCount=0;window.eventCounts={context:0,shortcut:0,paste:0,drop:0};const input=document.querySelector('#name');
        function render(){document.querySelector('#state').textContent='Count: '+pointerCount+' Typed: '+(input.value||'empty')+' Cookie: '+document.cookie;document.querySelector('#events').textContent='Context: '+eventCounts.context+' Shortcut: '+eventCounts.shortcut+' Paste: '+eventCounts.paste+' Drop: '+eventCounts.drop}
        function viewport(){document.querySelector('#viewport').textContent='Viewport: '+innerWidth+'x'+innerHeight}
        addEventListener('contextmenu',()=>{eventCounts.context++;render()});addEventListener('keydown',event=>{if(event.ctrlKey||event.metaKey){eventCounts.shortcut++;render()}});addEventListener('paste',()=>{eventCounts.paste++;render()});addEventListener('drop',()=>{eventCounts.drop++;render()});
        input.addEventListener('input',render);addEventListener('resize',viewport);render();viewport();
        const worker=new Worker(URL.createObjectURL(new Blob(['postMessage("ready");onmessage=()=>{}'])));worker.onmessage=()=>document.querySelector('#worker-state').textContent='Worker: ready';</script>`);
      return;
    }
    if (request.url === "/oopif") {
      response.writeHead(200, { "content-type": "text/html; charset=utf-8" });
      response.end("<!doctype html><title>OOPIF</title><p>OOPIF fixture</p>");
      return;
    }
    if (request.url === "/popup") {
      response.writeHead(200, { "content-type": "text/html; charset=utf-8" });
      response.end("<!doctype html><title>Popup</title><h1>Task popup</h1>");
      return;
    }
    if (request.url === "/download") {
      response.writeHead(200, {
        "content-type": "text/plain",
        "content-disposition": "attachment; filename=fixture.txt",
      });
      response.end("sparkclaw electron download proof\n");
      return;
    }
    if (request.url === "/script-proof") {
      response.writeHead(200, { "content-type": "text/html; charset=utf-8" });
      response.end(`<!doctype html><meta charset="utf-8"><title>Managed script proof</title>
        <script>window.inlineSawDocumentStart=window.__sparkclawDocumentStartProof===true</script>
        <p id="proof">document-start</p>`);
      return;
    }
    if (request.url === "/second") {
      response.writeHead(200, { "content-type": "text/html; charset=utf-8" });
      response.end("<!doctype html><title>Second</title><h1>Second task page</h1>");
      return;
    }
    response.writeHead(404).end();
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  const origin = `http://127.0.0.1:${address.port}`;
  fixtureServerOrigin = origin;
  return { server, origin };
}

async function managedScriptSnapshot(secret) {
  const response = await adapterRequest({
    schema_version: 1,
    operation: "qualificationManagedScripts",
    runtime_secret: secret,
  });
  return response.managed_scripts;
}

async function waitFor(check, timeoutMS) {
  const deadline = Date.now() + timeoutMS;
  while (Date.now() < deadline) {
    if (await check()) return;
    await delay(50);
  }
  throw new Error("qualification condition timed out");
}

async function waitForValue(check, timeoutMS) {
  const deadline = Date.now() + timeoutMS;
  while (Date.now() < deadline) {
    const value = await check();
    if (value) return value;
    await delay(50);
  }
  throw new Error("qualification value timed out");
}

async function findFileContaining(root, expected) {
  for (const file of await listRegularFiles(root)) {
    const value = await fs.readFile(file, "utf8").catch(() => "");
    if (value === expected) return file;
  }
  return "";
}

async function listRegularFiles(root) {
  const files = [];
  const pending = [root];
  while (pending.length) {
    const directory = pending.pop();
    let entries;
    try {
      entries = await fs.readdir(directory, { withFileTypes: true });
    } catch (error) {
      if (error?.code === "ENOENT") continue;
      throw error;
    }
    for (const entry of entries) {
      const candidate = path.join(directory, entry.name);
      if (entry.isDirectory()) pending.push(candidate);
      else if (entry.isFile()) files.push(candidate);
    }
  }
  return files;
}

async function qualificationSnapshot(secret) {
  return await adapterRequest({ schema_version: 1, operation: "qualificationSnapshot", runtime_secret: secret });
}

async function qualificationShow(secret, pageRef) {
  return await adapterRequest({
    schema_version: 1,
    operation: "qualificationShowTask",
    runtime_secret: secret,
    page_ref: pageRef,
  });
}

async function qualificationLifecycle(secret, action) {
  const response = await adapterRequest({
    schema_version: 1,
    operation: "qualificationLifecycle",
    runtime_secret: secret,
    action,
  });
  return response.lifecycle;
}

async function adapterRequest(payload) {
  return await new Promise((resolve, reject) => {
    const socket = net.createConnection(adapter.socketPath);
    let input = "";
    const timer = setTimeout(() => socket.destroy(new Error("adapter request timed out")), 8000);
    socket.setEncoding("utf8");
    socket.on("connect", () => socket.write(`${JSON.stringify(payload)}\n`));
    socket.on("data", (chunk) => {
      input += chunk;
      const newline = input.indexOf("\n");
      if (newline < 0) return;
      clearTimeout(timer);
      const response = JSON.parse(input.slice(0, newline));
      socket.end();
      if (response.state === "rejected") reject(new Error("adapter request rejected"));
      else resolve(response);
    });
    socket.on("error", reject);
  });
}

async function cli(args, env) {
  return (await run(process.execPath, [CLI, ...args], { env })).stdout.trim();
}

async function run(executable, args, { env = process.env, allowFailure = false } = {}) {
  return await new Promise((resolve, reject) => {
    const child = spawn(executable, args, { cwd: temporary, env, stdio: ["ignore", "pipe", "pipe"] });
    const chunks = [];
    const errors = [];
    const timer = setTimeout(() => child.kill("SIGKILL"), 25_000);
    child.stdout.on("data", (chunk) => chunks.push(Buffer.from(chunk)));
    child.stderr.on("data", (chunk) => errors.push(Buffer.from(chunk)));
    child.once("error", reject);
    child.once("close", (code, signal) => {
      clearTimeout(timer);
      const result = { stdout: Buffer.concat(chunks).toString("utf8"), stderr: Buffer.concat(errors).toString("utf8"), code, signal };
      if (allowFailure || code === 0 && signal === null) resolve(result);
      else reject(new Error(`command failed (${code ?? signal}): ${result.stderr.slice(-2000)}`));
    });
  });
}

function findRef(snapshot, name) {
  if (snapshot && typeof snapshot === "object") {
    const queue = Array.isArray(snapshot) ? [...snapshot] : [snapshot];
    while (queue.length) {
      const value = queue.shift();
      if (!value || typeof value !== "object") continue;
      if (value.name === name && /^e[0-9]+$/u.test(value.ref ?? "")) return value.ref;
      for (const child of Object.values(value)) {
        if (child && typeof child === "object") queue.push(...(Array.isArray(child) ? child : [child]));
      }
    }
  }
  const text = typeof snapshot === "string" ? snapshot : JSON.stringify(snapshot);
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&");
  const after = text.match(new RegExp(`${escaped}[^\\n]{0,200}ref(?:=|[\"']?:[\"']?)[\"']?([a-z0-9]*e[0-9]+)`, "iu"));
  if (after) return after[1];
  const before = text.match(new RegExp(`ref(?:=|[\"']?:[\"']?)[\"']?([a-z0-9]*e[0-9]+)[^\\n]{0,200}${escaped}`, "iu"));
  return before?.[1] ?? "";
}

function sessionID(index) {
  return `session_${index.toString(16).padStart(32, "0")}`;
}

async function readLine(stream, child, timeoutMS) {
  return await new Promise((resolve, reject) => {
    let input = "";
    const timer = setTimeout(() => reject(new Error("process output timed out")), timeoutMS);
    const data = (chunk) => {
      input += chunk;
      const newline = input.indexOf("\n");
      if (newline < 0) return;
      clearTimeout(timer);
      stream.removeListener("data", data);
      resolve(input.slice(0, newline).trim());
    };
    stream.on("data", data);
    child.once("exit", (code) => reject(new Error(`process exited before ready: ${code}`)));
  });
}

async function readMatchingJSON(stream, child, event, timeoutMS) {
  const line = await readLine(stream, child, timeoutMS);
  const value = JSON.parse(line);
  if (value.event !== event) throw new Error(`unexpected process event: ${line}`);
  return value;
}

async function stopChild(child) {
  if (!child || child.exitCode !== null) return;
  child.kill("SIGTERM");
  await Promise.race([new Promise((resolve) => child.once("exit", resolve)), delay(3000)]);
  if (child.exitCode === null) child.kill("SIGKILL");
}

async function killChild(child) {
  if (!child || child.exitCode !== null) return;
  child.kill("SIGKILL");
  await new Promise((resolve) => child.once("exit", resolve));
}

async function stopServer(server) {
  if (!server) return;
  await new Promise((resolve) => server.close(() => resolve()));
}

function delay(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}

function progress(stage) {
  process.stdout.write(`${JSON.stringify({ event: "sparkclaw_electron_phase1_progress", stage })}\n`);
}
