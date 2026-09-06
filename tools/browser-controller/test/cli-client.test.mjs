import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import crypto from "node:crypto";
import { EventEmitter, once } from "node:events";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { PlaywrightCLIClientFactory } from "../src/cli-client.mjs";
import {
  MAX_CLI_OUTPUT_BYTES,
  prepareRuntimeRoot,
  runProcess,
} from "../src/cli-runtime.mjs";
import { ProviderScriptRegistry } from "../src/provider-scripts.mjs";

const testDir = path.dirname(fileURLToPath(import.meta.url));
const fixture = path.join(testDir, "fixtures", "fake-cli.mjs");
const fixtureSource = "tools/browser-controller/test/fixtures/fake-cli.mjs";
const token = "private-extension-token-value";

test("provider registry requires an exact script revision and checksums its source closure", async () => {
  const registry = createRegistry();
  await registry.prepare();

  const registration = registry.resolve({
    provider: "gmail",
    operation: "probe",
    scriptID: "gmail.test_probe",
    revision: 1,
  });
  assert.match(registration.sourceChecksum, /^sha256:[0-9a-f]{64}$/u);
  assert.throws(
    () => registry.resolve({
      provider: "gmail",
      operation: "probe",
      scriptID: "gmail.test_probe",
      revision: 2,
    }),
    (error) => error.code === "browser_script_unavailable" && error.status === 400,
  );
});

test("CLI send keeps token and message values out of argv, logs, and artifacts", async (t) => {
  if (process.platform !== "linux") return t.skip("requires /proc daemon reaping");
  const harness = await createHarness(t, {
    PLAYWRIGHT_MCP_EXECUTABLE_PATH: "/tmp/inherited-browser",
    PLAYWRIGHT_MCP_USER_DATA_DIR: "/tmp/inherited-profile",
  });
  const message = {
    recipient: "person@example.test",
    subject: "A # \"subject\" \u4e2d\u6587",
    body: { format: "text", content: "line 1\n# \"quoted\" \u4e2d\u6587" },
  };
  const expected = structuredClone(message);
  const input = sendInput(message);

  const result = await harness.factory.runScript({
    token,
    sessionID: sessionID(1),
    provider: "gmail",
    operation: "send",
    scriptID: "gmail.test_send",
    revision: 1,
    input,
  });

  assert.equal(result.state, "completed", result.result?.code);
  assert.match(result.sourceChecksum, /^sha256:[0-9a-f]{64}$/u);
  assert.equal(input.message.recipient, "");
  assert.equal(input.message.subject, "");
  assert.equal(input.message.body.content, "");
  assert.deepEqual(await fs.readdir(harness.runtimeRoot), []);

  const logText = await fs.readFile(harness.logPath, "utf8");
  for (const secret of [token, expected.recipient, expected.subject, expected.body.content]) {
    assert.equal(logText.includes(secret), false);
  }
  const records = logText.trim().split("\n").map((line) => JSON.parse(line));
  const commands = records.filter((record) => record.event === "command");
  assert.equal(commands.every((record) => record.extension_token_present), true);
  assert.equal(commands.every((record) => !record.inherited_forbidden_env_present), true);
  assert.equal(commands.every((record) => record.executable_path === "/opt/sparkclaw/chromium"), true);
  assert.equal(commands.every((record) => record.user_data_dir === "/home/owner/browser-profile"), true);
  assert.equal(commands.filter((record) => record.command === "attach").length, 1);
  assert.equal(commands.filter((record) => record.command === "tab-new").length, 0);
  assert.equal(commands.filter((record) => record.command === "tab-close").length, 1);
  assert.equal(commands.filter((record) => record.command === "close").length, 1);
  assert.deepEqual(commands.slice(-2).map((record) => record.command), ["tab-close", "close"]);

  const fills = records.filter((record) => record.event === "fill");
  assert.deepEqual(fills.map((record) => record.argument), [
    "SPARKCLAW_EMAIL_RECIPIENT",
    "SPARKCLAW_EMAIL_SUBJECT",
    "SPARKCLAW_EMAIL_BODY",
  ]);
  assert.deepEqual(fills.map((record) => record.value_sha256), [
    digest(expected.recipient),
    digest(expected.subject),
    digest(expected.body.content),
  ]);
});

test("invalid provider input fails before attach or secret-file creation", async (t) => {
  const harness = await createHarness(t);
  const input = sendInput({
    recipient: "person@example.test",
    subject: "subject",
    body: { format: "text", content: "body" },
  });
  input.unknown = true;

  await assert.rejects(
    harness.factory.runScript({
      token,
      sessionID: sessionID(2),
      provider: "gmail",
      operation: "send",
      scriptID: "gmail.test_send",
      revision: 1,
      input,
    }),
    (error) => error.code === "invalid_request" && error.status === 400,
  );
  assert.equal(input.message.recipient, "");
  assert.deepEqual(await fs.readdir(harness.runtimeRoot), []);
  await assert.rejects(fs.stat(harness.logPath), (error) => error.code === "ENOENT");
});

test("a failed send command and post-click cleanup failure both become unknown", async (t) => {
  const clickFailure = await createHarness(t, {
    FAKE_CLI_FAIL_AFTER_EFFECT: "1",
    FAKE_CLI_EFFECT_SELECTOR: "#send",
  });
  const clickResult = await clickFailure.factory.runScript({
    token,
    sessionID: sessionID(3),
    provider: "gmail",
    operation: "send",
    scriptID: "gmail.test_send",
    revision: 1,
    input: sendInput(basicMessage()),
  });
  assert.equal(clickResult.state, "failed");
  assert.equal(clickResult.result.code, "send_outcome_unknown");

  const cleanupFailure = await createHarness(t, { FAKE_CLI_FAIL_COMMAND: "close" });
  const cleanupResult = await cleanupFailure.factory.runScript({
    token,
    sessionID: sessionID(4),
    provider: "gmail",
    operation: "send",
    scriptID: "gmail.test_send",
    revision: 1,
    input: sendInput(basicMessage()),
  });
  assert.equal(cleanupResult.state, "failed");
  assert.equal(cleanupResult.result.code, "send_outcome_unknown");
});

test("probe failures remain typed failures and never become send unknown", async (t) => {
  const harness = await createHarness(t, {}, {
    probeHandler: async () => {
      throw Object.assign(new Error("private detail"), { code: "login_probe_failed" });
    },
  });
  const result = await harness.factory.runScript({
    token,
    sessionID: sessionID(5),
    provider: "gmail",
    operation: "probe",
    scriptID: "gmail.test_probe",
    revision: 1,
    input: probeInput(),
  });
  assert.equal(result.state, "failed");
  assert.equal(result.result.code, "login_probe_failed");
});

test("task-page close accepts the extension session closing before command acknowledgement", async (t) => {
  if (process.platform !== "linux") return t.skip("requires /proc daemon reaping");
  const harness = await createHarness(t, { FAKE_CLI_PAGE_CLOSED_COMMAND: "tab-close" });
  const result = await harness.factory.runScript({
    token,
    sessionID: sessionID(14),
    provider: "gmail",
    operation: "probe",
    scriptID: "gmail.test_probe",
    revision: 1,
    input: probeInput(),
  });

  assert.equal(result.state, "completed");
  assert.deepEqual(harness.diagnostics, []);
  assert.deepEqual(await fs.readdir(harness.runtimeRoot), []);
});

test("CLI deadlines and owner-tab topology changes fail closed and clean runtime state", async (t) => {
  const timeoutHarness = await createHarness(t, { FAKE_CLI_HANG_COMMAND: "goto" }, {
    navigationTimeoutMS: 30,
  });
  await assert.rejects(
    timeoutHarness.factory.runScript({
      token,
      sessionID: sessionID(6),
      provider: "gmail",
      operation: "probe",
      scriptID: "gmail.test_probe",
      revision: 1,
      input: probeInput(),
    }),
    (error) => error.code === "browser_script_timeout" && error.status === 504,
  );
  assert.deepEqual(await fs.readdir(timeoutHarness.runtimeRoot), []);
  assert.deepEqual(timeoutHarness.diagnostics, [{
    event: "browser_cli_script_failed",
    provider: "gmail",
    operation: "probe",
    scriptID: "gmail.test_probe",
    phase: "navigate",
    code: "browser_script_timeout",
    reason: "timeout",
    command: "goto",
  }]);

  const staleHarness = await createHarness(t, {
    FAKE_CLI_MUTATE_OWNER_ON: "eval",
    FAKE_CLI_OWNER_TABS: JSON.stringify([
      fakeConnectTab(),
      { title: "Owner", url: "https://owner.test/", current: false, crashed: false },
    ]),
  });
  await assert.rejects(
    staleHarness.factory.runScript({
      token,
      sessionID: sessionID(7),
      provider: "gmail",
      operation: "probe",
      scriptID: "gmail.test_probe",
      revision: 1,
      input: probeInput(),
    }),
    (error) => error.code === "browser_page_stale" && error.status === 409,
  );
  assert.deepEqual(await fs.readdir(staleHarness.runtimeRoot), []);
});

test("CLI process diagnostics expose only fixed internal failure reasons", async () => {
  const base = {
    cwd: os.tmpdir(),
    env: { ...process.env },
    timeoutMS: 1_000,
    secrets: [],
    forbiddenOutputValues: [],
  };
  const cases = [
    {
      reason: "spawn_error",
      executable: path.join(os.tmpdir(), "sparkclaw-missing-playwright-cli"),
      args: [],
    },
    {
      reason: "process_exit",
      executable: process.execPath,
      args: ["-e", "process.exit(2)"],
    },
    {
      reason: "timeout",
      executable: process.execPath,
      args: ["-e", "setInterval(() => {}, 1000)"],
      options: { timeoutMS: 25 },
    },
    {
      reason: "output_overflow",
      executable: process.execPath,
      args: ["-e", `process.stdout.write("x".repeat(${MAX_CLI_OUTPUT_BYTES + 1}))`],
    },
    {
      reason: "forbidden_output",
      executable: process.execPath,
      args: ["-e", "process.stdout.write('private-token')"],
      options: { forbiddenOutputValues: ["private-token"] },
    },
  ];

  for (const testCase of cases) {
    await assert.rejects(
      runProcess(spawn, testCase.executable, testCase.args, {
        ...base,
        ...testCase.options,
      }),
      (error) => error.diagnosticReason === testCase.reason,
    );
  }

  await assert.rejects(
    runProcess(
      spawn,
      process.execPath,
      ["-e", "process.stderr.write('private-token'); process.stdout.write('{}')"],
      {
        ...base,
        forbiddenOutputValues: ["private-token"],
        stdoutTransform: () => "{}",
      },
    ),
    (error) => error.diagnosticReason === "forbidden_output",
  );

  await assert.rejects(
    runProcess(
      spawn,
      process.execPath,
      ["-e", "process.stderr.write('SyntaxError'); process.exit(2)"],
      base,
    ),
    (error) => error.diagnosticReason === "process_exit_syntax" &&
      error.diagnosticContext?.stream === "stderr" &&
      error.diagnosticContext?.stdoutResidualBytes === 0 &&
      error.diagnosticContext?.stderrResidualBytes === 11,
  );
});

test("CLI factory diagnoses forbidden stderr without exposing its content", async (t) => {
  const harness = await createHarness(t, { FAKE_CLI_STDERR_TOKEN_COMMAND: "attach" });
  await assert.rejects(
    harness.factory.runScript({
      token,
      sessionID: sessionID(9),
      provider: "gmail",
      operation: "probe",
      scriptID: "gmail.test_probe",
      revision: 1,
      input: probeInput(),
    }),
    (error) => error.code === "browser_extension_unavailable",
  );
  assert.equal(harness.diagnostics.length, 1);
  assert.deepEqual(
    {
      event: harness.diagnostics[0].event,
      phase: harness.diagnostics[0].phase,
      code: harness.diagnostics[0].code,
      reason: harness.diagnostics[0].reason,
      command: harness.diagnostics[0].command,
      stream: harness.diagnostics[0].stream,
      stdoutOccurrences: harness.diagnostics[0].stdoutOccurrences,
      stderrOccurrences: harness.diagnostics[0].stderrOccurrences,
      stderrResidualBytes: harness.diagnostics[0].stderrResidualBytes,
    },
    {
      event: "browser_cli_script_failed",
      phase: "attach",
      code: "browser_extension_unavailable",
      reason: "forbidden_output",
      command: "attach",
      stream: "stderr",
      stdoutOccurrences: 0,
      stderrOccurrences: 1,
      stderrResidualBytes: 0,
    },
  );
});

test("CLI factory diagnoses the fixed provider-handler command without arguments", async (t) => {
  const harness = await createHarness(t, {
    FAKE_CLI_FAIL_COMMAND: "eval",
    FAKE_CLI_FAIL_COMMAND_AFTER: "1",
  }, {
    probeHandler: async (_input, runtime) => {
      const tab = await runtime.createOwnedTab();
      await tab.getUrl("https://mail.google.test");
    },
  });
  await assert.rejects(
    harness.factory.runScript({
      token,
      sessionID: sessionID(10),
      provider: "gmail",
      operation: "probe",
      scriptID: "gmail.test_probe",
      revision: 1,
      input: probeInput(),
    }),
    (error) => error.code === "browser_extension_unavailable",
  );
  assert.deepEqual(harness.diagnostics, [{
    event: "browser_cli_script_failed",
    provider: "gmail",
    operation: "probe",
    scriptID: "gmail.test_probe",
    phase: "provider_handler",
    code: "browser_extension_unavailable",
    reason: "process_exit",
    command: "eval",
  }]);
});

test("provider inspection retries a destroyed context only after revalidating origin", async (t) => {
  if (process.platform !== "linux") return t.skip("requires /proc daemon reaping");
  const harness = await createHarness(t, {
    FAKE_CLI_CONTEXT_DESTROYED_COMMAND: "eval",
    FAKE_CLI_CONTEXT_DESTROYED_ON_COUNT: "2",
  }, {
    outlookProbeHandler: async (_input, runtime) => await runtime.withTaskTab(
      "probe",
      async (tab) => await tab.inspect("() => null"),
    ),
  });
  const result = await harness.factory.runScript({
    token,
    sessionID: sessionID(11),
    provider: "outlook",
    operation: "probe",
    scriptID: "outlook.test_probe",
    revision: 1,
    input: { ...probeInput(), provider: "outlook" },
  });
  assert.equal(result.state, "completed");
  assert.equal(result.result.result, null);
  assert.equal(result.result.origin, "https://mail.google.test/");
  assert.deepEqual(harness.diagnostics, []);

  const records = (await fs.readFile(harness.logPath, "utf8")).trim()
    .split("\n")
    .map((line) => JSON.parse(line));
  assert.equal(records.filter((record) => record.command === "eval").length, 5);
});

test("provider inspection classifies a signed-out redirect before retrying its expression", async (t) => {
  const signedOutURL = "https://www.microsoft.test/outlook?deeplink=%2Fmail%2F";
  const harness = await createHarness(t, {
    FAKE_CLI_CONTEXT_DESTROYED_COMMAND: "eval",
    FAKE_CLI_CONTEXT_DESTROYED_ON_COUNT: "2",
    FAKE_CLI_CONTEXT_DESTROYED_URL: signedOutURL,
  }, {
    outlookProbeHandler: async (_input, runtime) => await runtime.withTaskTab(
      "probe",
      async (tab) => await tab.inspect("() => null"),
    ),
    outlookOrigins: ["https://mail.google.test", "https://www.microsoft.test"],
    outlookSignedOutURL: (rawURL) => rawURL === signedOutURL,
  });
  const result = await harness.factory.runScript({
    token,
    sessionID: sessionID(12),
    provider: "outlook",
    operation: "probe",
    scriptID: "outlook.test_probe",
    revision: 1,
    input: { ...probeInput(), provider: "outlook" },
  });
  assert.equal(result.state, "failed");
  assert.equal(result.result.code, "email_login_required");
  assert.deepEqual(harness.diagnostics, []);

  const records = (await fs.readFile(harness.logPath, "utf8")).trim()
    .split("\n")
    .map((line) => JSON.parse(line));
  assert.equal(records.filter((record) => record.command === "eval").length, 3);
});

test("an aborted invocation kills the active CLI process and removes private state", async (t) => {
  const harness = await createHarness(t, { FAKE_CLI_HANG_COMMAND: "goto" }, {
    navigationTimeoutMS: 2_000,
  });
  const abort = new AbortController();
  const running = harness.factory.runScript({
    token,
    sessionID: sessionID(8),
    provider: "gmail",
    operation: "probe",
    scriptID: "gmail.test_probe",
    revision: 1,
    input: probeInput(),
    signal: abort.signal,
  });
  setTimeout(() => abort.abort(), 50);

  await assert.rejects(
    running,
    (error) => error.code === "browser_extension_unavailable" && error.status === 503,
  );
  assert.deepEqual(await fs.readdir(harness.runtimeRoot), []);
});

test("a failed CLI stop reaps the metadata-bound daemon before removing private state", async (t) => {
  if (process.platform !== "linux") return t.skip("requires /proc command-line validation");
  const sessionName = `sc-cli-${digest(sessionID(13)).slice(0, 20)}`;
  const child = spawn(
    process.execPath,
    ["-e", "setInterval(() => {}, 1000)", "/tmp/cliDaemon.js", sessionName],
    { stdio: "ignore" },
  );
  const closed = once(child, "close");
  t.after(() => { if (child.exitCode === null) child.kill("SIGKILL"); });
  await once(child, "spawn");

  const harness = await createHarness(t, {
    FAKE_CLI_DAEMON_PID: String(child.pid),
    FAKE_CLI_FAIL_COMMAND: "close",
  });
  await assert.rejects(
    harness.factory.runScript({
      token,
      sessionID: sessionID(13),
      provider: "gmail",
      operation: "probe",
      scriptID: "gmail.test_probe",
      revision: 1,
      input: probeInput(),
    }),
    (error) => error.code === "browser_extension_unavailable",
  );

  await closed;
  assert.deepEqual(await fs.readdir(harness.runtimeRoot), []);
});

test("prepare reconciles only a metadata-bound stale CLI daemon", async (t) => {
  if (process.platform !== "linux") return t.skip("requires /proc command-line validation");
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-cli-stale-"));
  t.after(() => fs.rm(dir, { recursive: true, force: true }));
  const runtimeRoot = path.join(dir, "cli-runtime");
  const staleDirectory = path.join(runtimeRoot, "session-000000000000000000000000");
  const sessionName = "sc-cli-00000000000000000000";
  await fs.mkdir(staleDirectory, { recursive: true, mode: 0o700 });
  const child = spawn(
    process.execPath,
    ["-e", "setInterval(() => {}, 1000)", "/tmp/cliDaemon.js", sessionName],
    { stdio: "ignore" },
  );
  const closed = once(child, "close");
  t.after(() => { if (child.exitCode === null) child.kill("SIGKILL"); });
  await once(child, "spawn");
  await fs.writeFile(
    path.join(staleDirectory, "metadata.json"),
    `${JSON.stringify({ pid: child.pid, session_name: sessionName })}\n`,
    { mode: 0o600 },
  );

  await prepareRuntimeRoot(runtimeRoot);
  await closed;
  assert.deepEqual(await fs.readdir(runtimeRoot), []);
});

test("provider login launcher uses only the fixed executable, profile, and registered URL", async () => {
  const calls = [];
  const spawnImpl = (executable, args, options) => {
    calls.push({ executable, args, options });
    const child = new EventEmitter();
    child.unref = () => { child.unrefCalled = true; };
    queueMicrotask(() => child.emit("spawn"));
    return child;
  };
  const factory = new PlaywrightCLIClientFactory({
    executablePath: "/opt/sparkclaw/chromium",
    userDataDir: "/home/owner/browser-profile",
    spawn: spawnImpl,
    registry: createRegistry(),
    extraEnv: { PLAYWRIGHT_MCP_EXTENSION_TOKEN: "stale-token" },
  });

  assert.deepEqual(await factory.openProviderLogin("gmail"), { provider: "gmail" });
  assert.equal(calls.length, 1);
  assert.equal(calls[0].executable, "/opt/sparkclaw/chromium");
  assert.deepEqual(calls[0].args, [
    "--user-data-dir=/home/owner/browser-profile",
    "https://mail.google.test/",
  ]);
  assert.equal(calls[0].options.detached, true);
  assert.equal("PLAYWRIGHT_MCP_EXTENSION_TOKEN" in calls[0].options.env, false);
});

async function createHarness(t, extraEnv = {}, options = {}) {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-browser-cli-"));
  t.after(() => fs.rm(dir, { recursive: true, force: true }));
  const runtimeRoot = path.join(dir, "cli-runtime");
  const logPath = path.join(dir, "fake-cli.log");
  const diagnostics = [];
  const factory = new PlaywrightCLIClientFactory({
    entryPoint: fixture,
    cwd: dir,
    runtimeRoot,
    executablePath: options.executablePath ?? "/opt/sparkclaw/chromium",
    userDataDir: options.userDataDir ?? "/home/owner/browser-profile",
    connectTimeoutMS: 1_000,
    actionTimeoutMS: options.actionTimeoutMS ?? 1_000,
    navigationTimeoutMS: options.navigationTimeoutMS ?? 1_000,
    extraEnv: { FAKE_CLI_LOG: logPath, ...extraEnv },
    diagnostic: (record) => diagnostics.push(record),
    registry: createRegistry(options),
  });
  await factory.prepare();
  return { dir, runtimeRoot, logPath, diagnostics, factory };
}

function createRegistry(options = {}) {
  const entries = [
    {
      provider: "gmail",
      operation: "probe",
      scriptID: "gmail.test_probe",
      revision: 1,
      loginURL: "https://mail.google.test/",
      origins: ["https://mail.google.test"],
      timeoutMS: 30_000,
      handler: options.probeHandler ?? (async (_input, runtime) => {
        const tab = await runtime.createOwnedTab();
        return {
          schema_version: 1,
          status: "authenticated",
          provider: "gmail",
          url: await tab.getUrl("https://mail.google.test"),
        };
      }),
      sourceFiles: [fixtureSource],
    },
    {
      provider: "gmail",
      operation: "send",
      scriptID: "gmail.test_send",
      revision: 1,
      loginURL: "https://mail.google.test/",
      origins: ["https://mail.google.test"],
      timeoutMS: 30_000,
      effectSelector: "#send",
      handler: async (input, runtime) => {
        const tab = await runtime.createOwnedTab();
        await tab.fill("#recipient", input.message.recipient, "https://mail.google.test");
        await tab.fill("#subject", input.message.subject ?? "", "https://mail.google.test");
        await tab.fill("#body", input.message.body.content, "https://mail.google.test");
        assert.equal(await tab.getValue("#recipient", "https://mail.google.test"), input.message.recipient);
        assert.equal(await tab.getValue("#subject", "https://mail.google.test"), input.message.subject ?? "");
        assert.equal(await tab.getText("#body", "https://mail.google.test"), input.message.body.content);
        await tab.click("#send", "https://mail.google.test");
        return { schema_version: 1, status: "sent", provider: "gmail" };
      },
      sourceFiles: [fixtureSource],
    },
  ];
  if (options.outlookProbeHandler) {
    entries.push({
      provider: "outlook",
      operation: "probe",
      scriptID: "outlook.test_probe",
      revision: 1,
      loginURL: "https://mail.google.test/",
      origins: options.outlookOrigins ?? ["https://mail.google.test"],
      timeoutMS: 30_000,
      signedOutURL: options.outlookSignedOutURL,
      handler: options.outlookProbeHandler,
      sourceFiles: [fixtureSource],
    });
  }
  return new ProviderScriptRegistry(entries);
}

function probeInput() {
  return {
    schema_version: 1,
    operation: "probe",
    invocation_id: "probe-1",
    provider: "gmail",
    account: "default",
  };
}

function sendInput(message) {
  return {
    schema_version: 1,
    operation: "send",
    invocation_id: "send-1",
    provider: "gmail",
    account: "default",
    message,
  };
}

function basicMessage() {
  return {
    recipient: "person@example.test",
    subject: "subject",
    body: { format: "text", content: "body" },
  };
}

function sessionID(value) {
  return `session_${value.toString(16).padStart(32, "0")}`;
}

function digest(value) {
  return crypto.createHash("sha256").update(value).digest("hex");
}

function fakeConnectTab() {
  const url = new URL(
    "chrome-extension://mmlmfjhmonkocbjadbfplnigmagldckm/connect.html",
  );
  url.searchParams.set(
    "mcpRelayUrl",
    "ws://127.0.0.1:45678/extension/12345678-1234-4123-8123-123456789abc",
  );
  url.searchParams.set("client", JSON.stringify({ name: "playwright-cli" }));
  url.searchParams.set("protocolVersion", "2");
  url.searchParams.set("token", token);
  return {
    title: "Welcome",
    url: url.toString(),
    current: true,
    crashed: false,
    task: true,
  };
}
