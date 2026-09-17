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
import { PlaywrightCLITask } from "../src/cli-task.mjs";
import {
  MAX_CLI_OUTPUT_BYTES,
  prepareRuntimeRoot,
  runProcess,
} from "../src/cli-runtime.mjs";
import { ProviderScriptRegistry } from "../src/provider-scripts.mjs";
import { QQMailScriptError } from "../../../scripts/email/lib/qqmail-browser.mjs";
import { MANAGED_SEND_SELECTOR } from "../../../scripts/email/lib/managed-send.mjs";

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

test("hidden sends read contenteditable recipients and never request a window handoff", async (t) => {
  const harness = await createHarness(t, { FAKE_CLI_EDITABLE: "1", FAKE_CLI_HIDDEN: "1" });
  const result = await harness.factory.runScript({ token, sessionID: sessionID(91), provider: "gmail",
    operation: "send", scriptID: "gmail.test_send", revision: 1,
    input: sendInput({ recipient: "person@example.test", subject: "subject", body: { format: "text", content: "body" } }),
  });
  assert.equal(result.state, "completed");
  const records = (await fs.readFile(harness.logPath, "utf8")).trim().split("\n").map(JSON.parse);
  assert.equal(records.filter(record => record.command === "click").length, 1);
  assert.equal(records.some(record => record.argv?.some(arg => arg.includes("sparkclaw-browser-bridge-handoff-v1"))), false);
  assert.deepEqual(await fs.readdir(harness.runtimeRoot), []);
});

test("managed To and CC use independent private slots and are erased after completion", async t => {
  const expected = { to: ["first@example.invalid", "second@example.invalid"], cc: ["copy@example.invalid"], subject: "Synthetic private subject", body: { format: "text", content: "Synthetic private body" } };
  const input = { ...sendInput(structuredClone(expected)), mode: "compose", account_address: "owner@example.invalid" };
  const harness = await createHarness(t, {}, { sendHandler: async (request, runtime) => runtime.withSendTab(async tab => {
    for (const field of ["to", "cc"]) for (const address of request.message[field]) await tab.fill(`#${field}`, address);
    await tab.fill("#subject", request.message.subject);
    await tab.fill("#body", request.message.body.content);
    return { status: "prepared" };
  }) });
  const result = await harness.factory.runScript({ token, sessionID: sessionID(120), provider: "gmail", operation: "send", scriptID: "gmail.test_send", revision: 1, input });
  assert.equal(result.state, "completed");
  const records = await commandRecords(harness);
  const fills = records.filter(record => record.event === "fill");
  assert.deepEqual(fills.map(record => record.argument), ["EMAIL_RECIPIENT_0", "EMAIL_RECIPIENT_1", "EMAIL_RECIPIENT_2", "SPARKCLAW_EMAIL_SUBJECT", "SPARKCLAW_EMAIL_BODY"]);
  const values = [...expected.to, ...expected.cc, expected.subject, expected.body.content];
  assert.deepEqual(fills.map(record => record.value_sha256), values.map(digest));
  const log = await fs.readFile(harness.logPath, "utf8");
  for (const value of [token, ...values]) assert.equal(log.includes(value), false);
  assert.equal(input.message.to.some(Boolean), false);
  assert.equal(input.message.cc.some(Boolean), false);
  assert.equal(input.message.subject, "");
  assert.equal(input.message.body.content, "");
  assert.deepEqual(await fs.readdir(harness.runtimeRoot), []);
});

test("managed reply lookup permits only fixed folder queries outside private message slots", async t => {
  const queries = ["in:inbox", "in:sent", "-in:trash -in:spam -in:drafts"];
  const harness = await createHarness(t, {}, { sendHandler: async (input, runtime) => runtime.withSendTab(async tab => {
    for (const query of queries) await tab.fill('input[name="q"]', query);
    for (const [selector, value] of [['input[name="q"]', "from:unregistered@example.invalid"], ["#body", "in:inbox"], ["#to", "unregistered@example.invalid"]]) {
      await assert.rejects(async () => tab.fill(selector, value));
    }
    await tab.fill("#body", input.message.body.content);
    return { status: "prepared" };
  }) });
  const result = await harness.factory.runScript({ token, sessionID: sessionID(121), provider: "gmail", operation: "send", scriptID: "gmail.test_send", revision: 1, input: sendInput(basicMessage()) });
  assert.equal(result.state, "completed");
  const records = await commandRecords(harness);
  const queriesRun = records.filter(record => record.command === "run-code");
  assert.equal(queriesRun.length, 3);
  queries.forEach((query, index) => assert.ok(queriesRun[index].argv.some(arg => arg.includes(JSON.stringify(query)))));
  assert.deepEqual(records.filter(record => record.event === "fill").map(record => record.argument), ["SPARKCLAW_EMAIL_BODY"]);
  assert.equal(JSON.stringify(records).includes("unregistered@example.invalid"), false);
  assert.deepEqual(await fs.readdir(harness.runtimeRoot), []);
});

test("managed Send click and cleanup failures remain unknown through the CLI effect fence", async t => {
  const registry = new ProviderScriptRegistry();
  for (const provider of ["gmail", "outlook", "qq_mail"]) {
    const entry = registry.entries.get(`${provider}:send`);
    assert.ok(entry.effectSelectors.includes(MANAGED_SEND_SELECTOR));
  }
  for (const env of [{ FAKE_CLI_FAIL_AFTER_EFFECT: "1", FAKE_CLI_EFFECT_SELECTOR: MANAGED_SEND_SELECTOR }, { FAKE_CLI_FAIL_COMMAND: "close" }]) {
    const harness = await createHarness(t, env, { effectSelectors: [MANAGED_SEND_SELECTOR], sendHandler: async (_input, runtime) => runtime.withSendTab(async tab => {
      await tab.click(MANAGED_SEND_SELECTOR);
      return { status: "sent" };
    }) });
    const result = await harness.factory.runScript({ token, sessionID: sessionID(122), provider: "gmail", operation: "send", scriptID: "gmail.test_send", revision: 1, input: sendInput(basicMessage()) });
    assert.equal(result.state, "failed");
    assert.equal(result.result.code, "send_outcome_unknown");
    assert.equal((await commandRecords(harness)).filter(record => record.command === "click").length, 1);
    assert.deepEqual(await fs.readdir(harness.runtimeRoot), []);
  }
});

test("background renderer initialization failure stops before draft changes", async t => {
  const harness = await createHarness(t, { FAKE_CLI_BACKGROUND_INIT_FAIL: "1" });
  await assert.rejects(harness.factory.runScript({ token, sessionID: sessionID(92), provider: "gmail",
    operation: "send", scriptID: "gmail.test_send", revision: 1, input: sendInput(basicMessage()) }));
  const records = (await fs.readFile(harness.logPath, "utf8")).trim().split("\n").map(JSON.parse);
  assert.equal(records.some(record => record.event === "fill" || record.command === "click"), false);
  assert.deepEqual(await fs.readdir(harness.runtimeRoot), []);
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

test("a redacted field with a mismatched digest cannot reach Send", async (t) => {
  const harness = await createHarness(t, { FAKE_CLI_BAD_READBACK_DIGEST: "1" });
  await assert.rejects(harness.factory.runScript({
    token, sessionID: sessionID(31), provider: "gmail", operation: "send",
    scriptID: "gmail.test_send", revision: 1, input: sendInput(basicMessage()),
  }), error => error.code === "browser_extension_unavailable");
  const records = (await fs.readFile(harness.logPath, "utf8")).trim().split("\n").map(JSON.parse);
  assert.equal(records.some(record => record.argv?.includes("#send")), false);
  assert.deepEqual(await fs.readdir(harness.runtimeRoot), []);
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

test('task-owned popup is closed after a topology failure without leaving the primary task page',async t=>{
  const harness=await createHarness(t,{FAKE_CLI_TASK_POPUP_ON:'eval'});
  await assert.rejects(harness.factory.runScript({token,sessionID:sessionID(181),provider:'gmail',operation:'probe',scriptID:'gmail.test_probe',revision:1,input:probeInput()}),{code:'browser_page_stale'});
  const records=await commandRecords(harness);
  const closes=records.filter(r=>r.command==='tab-close');
  assert.deepEqual(closes.map(r=>r.argv.at(-1)),['1','0']);
  assert.equal(records.at(-1).command,'close');
  assert.deepEqual(await fs.readdir(harness.runtimeRoot),[]);
});

test('aborted handler retains a separate bounded budget to close the actual task page',async t=>{
  const abort=new AbortController();
  const harness=await createHarness(t,{}, {probeHandler:async()=>{abort.abort();throw new Error('cancelled handler');}});
  const result=await harness.factory.runScript({token,signal:abort.signal,sessionID:sessionID(182),provider:'gmail',operation:'probe',scriptID:'gmail.test_probe',revision:1,input:probeInput()});
  assert.equal(result.result.status,'error');
  const records=await commandRecords(harness);
  assert.equal(records.filter(r=>r.command==='tab-close').length,1);
  assert.equal(records.at(-1).command,'close');
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

test("wrapped provider runtime failures retain typed diagnostics", async t => {
  const harness = await createHarness(t, {
    FAKE_CLI_FAIL_COMMAND: "eval", FAKE_CLI_FAIL_COMMAND_AFTER: "1",
  }, { probeHandler: async (_input, runtime) => {
    try { await (await runtime.createOwnedTab()).getUrl("https://mail.google.test"); }
    catch (cause) { throw new QQMailScriptError("send_precondition_failed", "preparation failed", { cause }); }
  } });
  await assert.rejects(harness.factory.runScript({
    token, sessionID: sessionID(32), provider: "gmail", operation: "probe",
    scriptID: "gmail.test_probe", revision: 1, input: probeInput(),
  }), error => error.code === "browser_extension_unavailable");
  assert.equal(harness.diagnostics[0].reason, "process_exit");
  assert.equal(harness.diagnostics[0].command, "eval");
});

test("provider inspection combines URL and result in one CLI evaluation", async (t) => {
  const harness = await createHarness(t, {}, {
    outlookProbeHandler: async (_input, runtime) => await runtime.withTaskTab(
      "probe", async (tab) => await tab.inspect("() => null"),
    ),
  });
  const result = await harness.factory.runScript({
    token, sessionID: sessionID(13), provider: "outlook", operation: "probe",
    scriptID: "outlook.test_probe", revision: 1,
    input: { ...probeInput(), provider: "outlook" },
  });
  assert.equal(result.state, "completed");
  assert.equal(result.result.result, null);
  assert.equal(result.result.origin, "https://mail.google.test/");
  const records = (await fs.readFile(harness.logPath, "utf8")).trim()
    .split("\n").map((line) => JSON.parse(line));
  // One URL check after navigation, then one combined login inspection.
  assert.equal(records.filter((record) => record.command === "eval").length, 2);
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
  assert.equal(records.filter((record) => record.command === "eval").length, 4);
});

test("six guarded reads need six evaluations without separate URL reads", async t => {
  const harness = await createHarness(t, {}, { probeHandler: async (_input, runtime) => {
    const tab = await runtime.createOwnedTab();
    const origin = "https://mail.google.test";
    assert.equal(await tab.getCount("#field", origin), 1);
    assert.equal(await tab.getValue("#field", origin), "");
    assert.equal(await tab.getText("#field", origin), "");
    assert.equal(await tab.getAttribute("#field", "title", origin), "");
    await tab.waitFor("#field", origin);
    await tab.inspect("() => null");
    return { status: "ready" };
  } });
  const result = await harness.factory.runScript({ token, sessionID: sessionID(93), provider: "gmail",
    operation: "probe", scriptID: "gmail.test_probe", revision: 1, input: probeInput() });
  assert.equal(result.state, "completed");
  const records = (await fs.readFile(harness.logPath, "utf8")).trim().split("\n").map(JSON.parse);
  assert.equal(records.filter(record => record.command === "eval").length, 7);
});

test("combined field reads reject an unexpected origin and a redirect during reading", async t => {
  for (const redirect of [false, true]) {
    const harness = await createHarness(t, redirect ? { FAKE_CLI_READ_REDIRECT: "1" } : {}, {
      probeHandler: async (_input, runtime) => (await runtime.createOwnedTab()).getValue(
        "#field", redirect ? "https://mail.google.test" : "https://foreign.test"),
    });
    await assert.rejects(harness.factory.runScript({ token, sessionID: sessionID(94), provider: "gmail",
      operation: "probe", scriptID: "gmail.test_probe", revision: 1, input: probeInput() }));
    assert.deepEqual(await fs.readdir(harness.runtimeRoot), []);
  }
});

test("batched draft reads use one evaluation and reject altered digests before Send", async t => {
  for (const invalidDigest of [false, true]) {
    const harness = await createHarness(t, invalidDigest ? { FAKE_CLI_BAD_READBACK_DIGEST: "1" } : {}, {
      sendHandler: async (input, runtime) => {
        const tab = await runtime.createOwnedTab();
        await tab.fill("#recipient", input.message.recipient, "https://mail.google.test");
        await tab.fill("#body", input.message.body.content, "https://mail.google.test");
        const [recipient, body, count, enabled] = await tab.readMany([
          ["get", "value", "#recipient"], ["get", "text", "#body"],
          ["get", "count", "#send"], ["is", "enabled", "#send"],
        ], "https://mail.google.test");
        assert.equal(recipient.value, input.message.recipient);
        assert.equal(body.text, input.message.body.content);
        assert.equal(count.count, 1);
        assert.equal(enabled.enabled, true);
        return { status: "verified" };
      },
    });
    const run = harness.factory.runScript({ token, sessionID: sessionID(95), provider: "gmail",
      operation: "send", scriptID: "gmail.test_send", revision: 1,
      input: sendInput({ recipient: "one@example.test", subject: "subject", body: { format: "text", content: "line one\nline two" } }),
    });
    if (invalidDigest) await assert.rejects(run);
    else assert.equal((await run).state, "completed");
    const records = (await fs.readFile(harness.logPath, "utf8")).trim().split("\n").map(JSON.parse);
    // Navigation, three background-init reads, two fills, and one batched read.
    assert.equal(records.filter(record => record.command === "eval").length, 7);
    assert.deepEqual(await fs.readdir(harness.runtimeRoot), []);
  }
});

test("batch read API refuses actions and oversized batches", async t => {
  const harness = await createHarness(t, {}, { probeHandler: async (_input, runtime) => {
    const tab = await runtime.createOwnedTab();
    for (const commands of [[["click", "#send"]], [], Array(33).fill(["get", "url"])]) {
      await assert.rejects(tab.readMany(commands));
    }
    return {status: "ready"};
  } });
  assert.equal((await harness.factory.runScript({ token, sessionID: sessionID(96), provider: "gmail",
    operation: "probe", scriptID: "gmail.test_probe", revision: 1, input: probeInput() })).state, "completed");
  const records = (await fs.readFile(harness.logPath, "utf8")).trim().split("\n").map(JSON.parse);
  assert.equal(records.filter(record => record.command === "eval").length, 1);
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
  for (const [provider, url] of Object.entries({chatgpt:"https://chatgpt.com/",claude:"https://claude.ai/",gemini:"https://gemini.google.com/",grok:"https://grok.com/"})) {
    await factory.openProviderLogin(provider);
    const last = calls.at(-1);
    assert.equal(last.executable, calls[0].executable);
    assert.deepEqual(last.args, ["--user-data-dir=/home/owner/browser-profile", url]);
    assert.equal("PLAYWRIGHT_MCP_EXTENSION_TOKEN" in last.options.env, false);
  }
});

test("read CLI initializes background input and downloads owned bytes without a handoff", async t => {
  let target;
  const harness = await createHarness(t, {}, { readHandler: async (_input, runtime) => runtime.withReadTab(async tab => {
    assert.deepEqual(await tab.runReadCode("async page => ({ origin: page.url() })"), { origin: "https://mail.google.test/" });
    return tab.download("#attachment", target, 16);
  }) });
  target = path.join(harness.dir, "attachment.bin");
  const result = await runRead(harness, 100);
  assert.equal(result.state, "completed");
  assert.deepEqual(result.result, { bytes: 16 });
  assert.deepEqual(await fs.readFile(target), Buffer.alloc(16, "x"));
  assert.equal((await fs.stat(target)).mode & 0o777, 0o600);
  const records = await commandRecords(harness);
  assert.equal(records.filter(record => record.event === "download_click").length, 1);
  assert.ok(records.some(record => record.command === "eval" && record.argv.some(arg => arg.includes("background-input-v1"))));
  assert.equal(records.some(record => record.argv?.some(arg => arg.includes("handoff-v1"))), false);
  assert.deepEqual(await fs.readdir(harness.runtimeRoot), []);
});

test("read download rejects oversized bytes and preserves existing destinations", async t => {
  for (const existing of [false, true]) {
    let target;
    const harness = await createHarness(t, {}, { readHandler: async (_input, runtime) => runtime.withReadTab(async tab => {
      await assert.rejects(tab.download("#attachment", target, existing ? 16 : 15), { code: existing ? "EEXIST" : "email_download_limit" });
      return { rejected: true };
    }) });
    target = path.join(harness.dir, "attachment.bin");
    if (existing) await fs.writeFile(target, "existing bytes");
    assert.equal((await runRead(harness, existing ? 101 : 102)).state, "completed");
    if (existing) assert.equal(await fs.readFile(target, "utf8"), "existing bytes");
    else await assert.rejects(fs.stat(target), { code: "ENOENT" });
    assert.deepEqual(await fs.readdir(harness.runtimeRoot), []);
  }
});

test("read native saveAs preserves exact binary bytes above the CLI output limit", async t => {
  const length = (1 << 20) + 13;
  let target;
  const harness = await createHarness(t, { FAKE_CLI_DOWNLOAD_BYTES: String(length), FAKE_CLI_DOWNLOAD_PATTERN: "1" }, {
    readHandler: async (_input, runtime) => runtime.withReadTab(tab => tab.download("#original", target, length)),
  });
  target = path.join(harness.dir, "original.eml");
  const result = await runRead(harness, 107);
  assert.deepEqual(result.result, { bytes: length });
  const bytes = await fs.readFile(target);
  assert.equal(bytes.length, length);
  for (let offset = 0; offset < length; offset += 512 << 10) {
    const size = Math.min(512 << 10, length - offset);
    assert.deepEqual(bytes.subarray(offset, offset + size), Buffer.alloc(size, offset / (512 << 10)));
  }
  assert.equal((await commandRecords(harness)).filter(record => record.event === "download_click").length, 1);
  assert.deepEqual(await fs.readdir(harness.runtimeRoot), []);
});

test("read background initialization failure prevents run-code and download", async t => {
  const harness = await createHarness(t, { FAKE_CLI_BACKGROUND_INIT_FAIL: "1" }, { readHandler: async () => assert.fail("read handler must not run") });
  await assert.rejects(runRead(harness, 103));
  assert.equal((await commandRecords(harness)).some(record => record.command === "run-code"), false);
  assert.equal((await commandRecords(harness)).some(record => record.command === "goto"), false);
});

test("read preparation occurs exactly once on owned blank page before navigation",async t=>{
  const harness=await createHarness(t,{}, {readHandler:async(_input,runtime)=>runtime.withReadTab(async()=>({ok:true}))});
  assert.equal((await runRead(harness,114)).state,'completed');
  const commands=(await commandRecords(harness)).filter(record=>record.event==='command');
  const markers=commands.flatMap((record,index)=>record.command==='eval'&&record.argv.includes('() => "sparkclaw-browser-bridge-background-input-v1"')?[index]:[]);
  assert.equal(markers.length,1);
  assert.ok(markers[0]<commands.findIndex(record=>record.command==='goto'));
  assert.equal(commands.some(record=>record.argv.some(arg=>arg.includes('bringToFront'))),false);
});

test("blank-page preparation preserves owner topology checks and blocks navigation on mutation",async t=>{
  const harness=await createHarness(t,{FAKE_CLI_MUTATE_OWNER_ON:'eval',FAKE_CLI_OWNER_TABS:JSON.stringify([fakeConnectTab(),{title:'Owner',url:'https://owner.test/',current:false,crashed:false}])},{readHandler:async()=>assert.fail('handler must not run')});
  await assert.rejects(runRead(harness,115),error=>error.code==='browser_page_stale');
  assert.equal((await commandRecords(harness)).some(record=>record.command==='goto'),false);
});

test("read accepts native popup and Blob download URLs", async t => {
  for (const mode of ["popup", "blob"]) {
    let target;
    const harness = await createHarness(t, { FAKE_CLI_DOWNLOAD_MODE: mode }, {
      readHandler: async (_input, runtime) => runtime.withReadTab(tab => tab.download("#original", target, 16)),
    });
    target = path.join(harness.dir, "original.eml");
    assert.equal((await runRead(harness, mode === "popup" ? 108 : 109)).state, "completed");
    assert.deepEqual(await fs.readFile(target), Buffer.alloc(16, "x"));
    assert.deepEqual(await fs.readdir(harness.runtimeRoot), []);
  }
});

test("read cancels downloads from an unregistered final origin", async t => {
  let target;
  const harness = await createHarness(t, { FAKE_CLI_DOWNLOAD_URL: "https://foreign.test/export" }, {
    readHandler: async (_input, runtime) => runtime.withReadTab(async tab => {
      await assert.rejects(tab.download("#original", target, 16), { code: "email_capture_unavailable" });
      return { rejected: true };
    }),
  });
  target = path.join(harness.dir, "original.eml");
  assert.equal((await runRead(harness, 110)).state, "completed");
  assert.equal((await commandRecords(harness)).filter(record => record.event === "download_canceled").length, 1);
  await assert.rejects(fs.stat(target), { code: "ENOENT" });
  assert.deepEqual(await fs.readdir(harness.runtimeRoot), []);
});

test("read download rejects an origin change inside the subprocess before clicking", async t => {
  let target;
  const harness = await createHarness(t, { FAKE_CLI_RUN_CODE_URL: "https://foreign.test/" }, { readHandler: async (_input, runtime) => runtime.withReadTab(tab => tab.download("#attachment", target, 16)) });
  target = path.join(harness.dir, "attachment.bin");
  await assert.rejects(runRead(harness, 104));
  assert.equal((await commandRecords(harness)).some(record => record.event === "download_click"), false);
  await assert.rejects(fs.stat(target), { code: "ENOENT" });
});

test("read run-code rejects owner-tab changes before a subsequent command", async t => {
  const harness = await createHarness(t, { FAKE_CLI_MUTATE_OWNER_ON: "run-code", FAKE_CLI_OWNER_TABS: JSON.stringify([
    fakeConnectTab(), { title: "Owner", url: "https://owner.test/", current: false, crashed: false },
  ]) }, { readHandler: async (_input, runtime) => runtime.withReadTab(tab => tab.runReadCode("async page => ({ origin: page.url() })")) });
  await assert.rejects(runRead(harness, 105), error => error.code === "browser_page_stale");
});

test('network reads await results without a fixed settle and avoid a separate post-read renderer evaluation', async t => {
  const harness = await createHarness(t, {}, {readHandler: async (_input, runtime) => runtime.withReadTab(tab => tab.runReadCode('async page => ({ok:true})'))});
  assert.equal((await runRead(harness, 141)).state, 'completed');
  const records = (await commandRecords(harness)).filter(r => r.event === 'command');
  assert.ok(records.every(r => r.settle_ms === '0'));
  assert.ok(records.every(r => r.awaited_mail_read === '1'));
  const index = records.findIndex(r => r.command === 'run-code');
  assert.equal(records[index + 1].command, 'tab-list');
  assert.equal(records.slice(index + 1).some(r => r.command === 'eval'), false);
});

test('network reads reject an origin change inside the subprocess before running provider code', async t => {
  const harness = await createHarness(t, {FAKE_CLI_RUN_CODE_URL:'https://foreign.test/'}, {readHandler: async (_input, runtime) => runtime.withReadTab(tab => tab.runReadCode('async page => { throw new Error("must not execute"); }'))});
  await assert.rejects(runRead(harness, 142), error => error.code === 'browser_page_stale');
});

test('send tasks retain the existing settle policy', async t => {
  const harness = await createHarness(t, {SPARKCLAW_AWAITED_MAIL_READ:'1',PLAYWRIGHT_MCP_TIMEOUT_SETTLE:'0'});
  await harness.factory.runScript({token,sessionID:sessionID(143),provider:'gmail',operation:'send',scriptID:'gmail.test_send',revision:1,input:sendInput({recipient:'person@example.test',subject:'subject',body:{format:'text',content:'body'}})});
  assert.ok((await commandRecords(harness)).filter(r=>r.event==='command').every(r=>r.settle_ms==='500'));
  assert.ok((await commandRecords(harness)).filter(r=>r.event==='command').every(r=>r.awaited_mail_read==='0'));
});

test('Outlook native UI reads retain upstream completion even with inherited fast-path flags', async t => {
  const harness = await createHarness(t, {SPARKCLAW_AWAITED_MAIL_READ:'1',PLAYWRIGHT_MCP_TIMEOUT_SETTLE:'0'}, {
    readProvider:'outlook', readHandler:async (_input,runtime)=>runtime.withReadTab(tab=>tab.runReadCode('async page => ({ok:true})')),
  });
  const result=await harness.factory.runScript({token,sessionID:sessionID(144),provider:'outlook',operation:'read',scriptID:'outlook.test_read',revision:1,
    input:{schema_version:1,operation:'read',invocation_id:'outlook-read-test',provider:'outlook',account:'default'}});
  assert.equal(result.state,'completed');
  const commands=(await commandRecords(harness)).filter(r=>r.event==='command');
  assert.ok(commands.length>0);
  assert.ok(commands.every(r=>r.settle_ms==='500'&&r.awaited_mail_read==='0'));
});

test("read run-code limits code and subprocess output and rejects invalid download arguments", async t => {
  const harness = await createHarness(t, {}, { readHandler: async (_input, runtime) => runtime.withReadTab(async tab => {
    await assert.rejects(tab.runReadCode("x".repeat((64 << 10) + 1)));
    for (const [destination, limit] of [["relative.bin", 16], ["/tmp/unused", 0], ["/tmp/unused", (110 << 20) + 1]]) {
      await assert.rejects(tab.download("#attachment", destination, limit));
    }
    await assert.rejects(tab.runReadCode(`async () => "x".repeat(${MAX_CLI_OUTPUT_BYTES + 1})`), error => error.diagnosticReason === "output_overflow");
    return { rejected: true };
  }) });
  assert.equal((await runRead(harness, 106)).state, "completed");
  const commands = (await commandRecords(harness)).filter(record => record.command === "run-code");
  assert.equal(commands.length, 1);
  assert.deepEqual(await fs.readdir(harness.runtimeRoot), []);
});

test("probe tasks cannot use read code, downloads or background input initialization", async () => {
  const task = new PlaywrightCLITask({ state: { sessionID: "probe-test" }, registration: { operation: "probe", timeoutMS: 1000 } });
  await assert.rejects(task.runReadCode("async () => true"));
  await assert.rejects(task.download("#attachment", "/tmp/not-created.bin", 16));
  await assert.rejects(task.prepareBackgroundPage());
});

function runRead(harness, id) {
  return harness.factory.runScript({ token, sessionID: sessionID(id), provider: "gmail", operation: "read", scriptID: "gmail.test_read", revision: 1,
    input: { schema_version: 1, operation: "read", invocation_id: `read-${id}`, provider: "gmail", account: "default", owner_scope: "a".repeat(64) } });
}

test("optional timing records all entered stages without private values", async t => {
  const timings=[];
  const harness=await createHarness(t,{}, {readHandler:async()=>({ok:true}),timingDiagnostic:record=>timings.push(record)});
  assert.equal((await runRead(harness,110)).state,'completed');
  assert.equal(timings.length,1);
  const record=timings[0];
  assert.deepEqual(Object.keys(record).sort(),['milliseconds','operation','provider']);
  assert.equal(record.provider,'gmail');assert.equal(record.operation,'read');
  assert.deepEqual(Object.keys(record.milliseconds).sort(),['attach','create_task_page','navigate','prepare_background_page','provider_handler','close_task_page','stop_cli','reap_daemon','remove_runtime','total'].sort());
  for(const value of Object.values(record.milliseconds))assert.ok(Number.isFinite(value)&&value>=0);
  assert.ok(record.milliseconds.total>=Object.entries(record.milliseconds).filter(([key])=>key!=='total').reduce((sum,[,value])=>sum+value,0));
  assert.equal(JSON.stringify(record).includes(token),false);
  assert.deepEqual(harness.diagnostics,[]);
  assert.deepEqual(await fs.readdir(harness.runtimeRoot),[]);
});

test("throwing and rejecting timing callbacks do not change successful execution",async t=>{
  for(const timingDiagnostic of [()=>{throw new Error('private callback error');},async()=>{throw new Error('private async callback error');}]){
    const harness=await createHarness(t,{}, {readHandler:async()=>({ok:true}),timingDiagnostic});
    assert.equal((await runRead(harness,111)).state,'completed');
    await new Promise(resolve=>setImmediate(resolve));
    assert.deepEqual(await fs.readdir(harness.runtimeRoot),[]);
    assert.deepEqual(harness.diagnostics,[]);
  }
});

test("early failure emits partial timing without leaking uncontrolled labels",async t=>{
  const timings=[];
  const harness=await createHarness(t,{}, {timingDiagnostic:record=>timings.push(record)});
  await assert.rejects(harness.factory.runScript({token,sessionID:sessionID(112),provider:'private-provider',operation:'private-operation',scriptID:'private-script',revision:1,input:{}}),{code:'browser_script_unavailable'});
  assert.equal(timings.length,1);
  assert.equal(timings[0].provider,'unknown');assert.equal(timings[0].operation,'unknown');
  assert.deepEqual(Object.keys(timings[0].milliseconds),['total']);
  assert.ok(timings[0].milliseconds.total>=0);
});

test("failed provider stage timing survives while cleanup still completes",async t=>{
  const timings=[];
  const harness=await createHarness(t,{}, {readHandler:async()=>{throw Object.assign(new Error('private failure'),{code:'email_network_read_failed'});},timingDiagnostic:record=>timings.push(record)});
  const result=await runRead(harness,113);
  assert.equal(result.result.code,'email_network_read_failed');
  assert.equal(timings.length,1);
  for(const key of ['provider_handler','close_task_page','stop_cli','reap_daemon','remove_runtime','total'])assert.ok(Number.isFinite(timings[0].milliseconds[key])&&timings[0].milliseconds[key]>=0);
  assert.equal(JSON.stringify(timings).includes('private failure'),false);
  assert.deepEqual(await fs.readdir(harness.runtimeRoot),[]);
});

async function commandRecords(harness) {
  return (await fs.readFile(harness.logPath, "utf8")).trim().split("\n").map(JSON.parse);
}

async function createHarness(t, extraEnv = {}, options = {}) {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-browser-cli-"));
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
    timingDiagnostic: options.timingDiagnostic,
    mailReadIdleMS:options.mailReadIdleMS,
    registry: createRegistry(options),
  });
  await factory.prepare();
  t.after(async()=>{try{await factory.close();}finally{await fs.rm(dir,{recursive:true,force:true});}});
  return { dir, runtimeRoot, logPath, diagnostics, factory };
}

function pooledInput(overrides={}) {
  return {schema_version:1,operation:'collect_page',invocation_id:'pool-round',provider:'gmail',account:'default',owner_scope:'a'.repeat(64),
    discovery:{provider_mode:'time_range',account_address:'person@example.test'},...overrides};
}

function runPooled(harness,n,input=pooledInput(),extra={}) {
  return harness.factory.runScript({token,sessionID:sessionID(n),provider:'gmail',operation:'collect_page',scriptID:'gmail.test_read',revision:1,
    credentialGeneration:1,input,...extra});
}

async function mutatePooledFixture(harness,mutate) {
  const [directory]=await fs.readdir(harness.runtimeRoot);
  const fixturePath=path.join(harness.runtimeRoot,directory,'cache','fake-cli-state.json');
  const state=JSON.parse(await fs.readFile(fixturePath,'utf8'));
  mutate(state);
  await fs.writeFile(fixturePath,JSON.stringify(state));
}

test('successive timeline rounds reuse exactly one owned task and dispose it on shutdown',async t=>{
  let calls=0;
  const harness=await createHarness(t,{FAKE_CLI_MAIL_READER:'1'},{readOperation:'collect_page',readHandler:async()=>{calls++;return {status:'empty',failures:[]};}});
  for(const n of [301,302,303])assert.equal((await runPooled(harness,n)).state,'completed');
  assert.equal(calls,3);
  let records=await commandRecords(harness);
  for(const command of ['attach','goto'])assert.equal(records.filter(r=>r.command===command).length,1);
  assert.equal(records.filter(r=>r.command==='close').length,0);
  assert.equal((await fs.readdir(harness.runtimeRoot)).length,1);
  await harness.factory.close();
  records=await commandRecords(harness);
  assert.equal(records.filter(r=>r.command==='tab-close').length,1);
  assert.equal(records.filter(r=>r.command==='close').length,1);
  assert.deepEqual(await fs.readdir(harness.runtimeRoot),[]);
});

test('pool binding changes evict old pages before another owner/account/credential uses them',async t=>{
  const harness=await createHarness(t,{FAKE_CLI_MAIL_READER:'1'},{readOperation:'collect_page',readHandler:async()=>({status:'empty',failures:[]})});
  await runPooled(harness,310);
  await runPooled(harness,311,pooledInput({owner_scope:'b'.repeat(64)}));
  await runPooled(harness,312,pooledInput({owner_scope:'b'.repeat(64)}),{credentialGeneration:2});
  assert.equal((await commandRecords(harness)).filter(r=>r.command==='attach').length,3);
  assert.equal((await commandRecords(harness)).filter(r=>r.command==='close').length,2);
});

test('changed document is rebuilt before handler; changed account is rejected without retry',async t=>{
  let calls=0;
  const harness=await createHarness(t,{FAKE_CLI_MAIL_READER:'1'},{readOperation:'collect_page',readHandler:async()=>{calls++;return {status:'empty',failures:[]};}});
  await runPooled(harness,320);
  await mutatePooledFixture(harness,state=>{delete state.mailDocumentNonce;});
  assert.equal((await runPooled(harness,321)).state,'completed');
  assert.equal(calls,2);
  assert.equal((await commandRecords(harness)).filter(r=>r.command==='attach').length,2);
  await mutatePooledFixture(harness,state=>{state.mailAccountMismatch=true;});
  const result=await runPooled(harness,322);
  assert.equal(result.state,'failed');
  assert.equal(calls,2);
  assert.equal((await commandRecords(harness)).filter(r=>r.command==='attach').length,2);
  assert.deepEqual(await fs.readdir(harness.runtimeRoot),[]);
});

test('provider failures are not retried inside a lease and never return a poisoned page to the pool',async t=>{
  let calls=0;
  const harness=await createHarness(t,{FAKE_CLI_MAIL_READER:'1'},{readOperation:'collect_page',readHandler:async()=>{
    calls++; if(calls===2)throw Object.assign(new Error('failed query'),{code:'email_network_read_failed'});
    return {status:'empty',failures:[]};
  }});
  await runPooled(harness,330);
  assert.equal((await runPooled(harness,331)).state,'failed');
  assert.equal(calls,2);
  assert.deepEqual(await fs.readdir(harness.runtimeRoot),[]);
  await runPooled(harness,332);
  assert.equal((await commandRecords(harness)).filter(r=>r.command==='attach').length,2);
});

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
      effectSelectors: options.effectSelectors,
      handler: options.sendHandler ?? (async (input, runtime) => {
        const tab = await runtime.createOwnedTab();
        await tab.fill("#recipient", input.message.recipient, "https://mail.google.test");
        await tab.fill("#subject", input.message.subject ?? "", "https://mail.google.test");
        await tab.fill("#body", input.message.body.content, "https://mail.google.test");
        assert.equal(await tab.getValue("#recipient", "https://mail.google.test"), input.message.recipient);
        assert.equal(await tab.getValue("#subject", "https://mail.google.test"), input.message.subject ?? "");
        assert.equal(await tab.getText("#body", "https://mail.google.test"), input.message.body.content);
        await tab.click("#send", "https://mail.google.test");
        return { schema_version: 1, status: "sent", provider: "gmail" };
      }),
      sourceFiles: [fixtureSource],
    },
  ];
  if (options.readHandler) {
    entries.push({
      provider: options.readProvider ?? "gmail", operation: options.readOperation ?? "read", scriptID: `${options.readProvider ?? "gmail"}.test_read`, revision: 1,
      loginURL: "https://mail.google.test/", origins: ["https://mail.google.test"],
      timeoutMS: 30_000, handler: options.readHandler, sourceFiles: [fixtureSource],
      validate: value => assert.equal(value.operation, options.readOperation ?? "read"),
    });
  }
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


test("collect_page CLI keeps all native downloads in one initialized task and exposes cancellation", async t => {
  let harness;
  const abort = new AbortController();
  harness = await createHarness(t, {}, { readOperation: "collect_page", readHandler: async (_input, runtime) => {
    assert.equal(runtime.signal, abort.signal);
    return runtime.withReadTab(async tab => {
      for (const name of ["a", "b"]) await tab.download("#original", path.join(harness.dir, `${name}.eml`), 16);
      return { captured: 2 };
    });
  } });
  const result = await harness.factory.runScript({ token, sessionID: sessionID(120), provider: "gmail", operation: "collect_page", scriptID: "gmail.test_read", revision: 1,
    input: { schema_version: 1, operation: "collect_page", invocation_id: "page-test", provider: "gmail", account: "default" }, signal: abort.signal });
  assert.equal(result.state, "completed");
  const records = await commandRecords(harness);
  assert.equal(records.filter(record => record.command === "attach").length, 1);
  assert.equal(records.filter(record => record.event === "download_click").length, 2);
  assert.equal(records.filter(record => record.command === "goto").length, 1);
  assert.equal(records.some(record => record.argv?.some(arg => arg.includes("handoff-v1"))), false);
  assert.deepEqual(await fs.readdir(harness.runtimeRoot), []);
});
