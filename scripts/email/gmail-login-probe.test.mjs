import assert from "node:assert/strict";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import {
  LOGIN_SELECTORS,
  assertError,
  assertOwnedTabLifecycle,
  createFixture,
  parseOnlyJson,
  runCli
} from "./gmail-cli-test-helper.mjs";

const SCRIPT_DIR = dirname(fileURLToPath(import.meta.url));
const PROBE_PATH = resolve(SCRIPT_DIR, "gmail-login-probe.mjs");
const VALID_REQUEST = {
  schema_version: 1,
  operation: "probe",
  invocation_id: "probe-invocation",
  provider: "gmail",
  account: "default"
};

function readyScenario(overrides = {}) {
  return {
    pageUrl: "https://mail.google.com/mail/u/0/#inbox",
    loginCounts: {
      [LOGIN_SELECTORS.accountControl]: 1,
      [LOGIN_SELECTORS.composeControl]: 1,
      [LOGIN_SELECTORS.mainMenu]: 1
    },
    accountLabel: "Google Account: Alias other@example.net (用户名字@Example.COM)",
    ...overrides
  };
}

test("probes a task-owned tab and returns only a masked account hint", async (t) => {
  const fullEmail = "用户名字@Example.COM";
  const fixture = await createFixture(t, readyScenario());
  const result = await runCli(PROBE_PATH, fixture, VALID_REQUEST);

  assert.equal(result.code, 0);
  assert.equal(result.stderr, "");
  assert.deepEqual(parseOnlyJson(result.stdout), {
    schema_version: 1,
    status: "ready",
    provider: "gmail",
    account_hint: "用户***@example.com"
  });
  assert.equal(result.stdout.includes(fullEmail), false);
  assert.equal(result.stdout.includes("other@example.net"), false);
  const ownership = assertOwnedTabLifecycle(await fixture.readLog(), "probe");
  assert.equal(result.stdout.includes(ownership.session), false);
  assert.equal(result.stdout.includes(ownership.label), false);
  assert.deepEqual((await fixture.readState()).tabs, {});
});

test("uses a different session and label for every invocation", async (t) => {
  const firstFixture = await createFixture(t, readyScenario());
  const secondFixture = await createFixture(t, readyScenario());

  assert.equal((await runCli(PROBE_PATH, firstFixture, VALID_REQUEST)).code, 0);
  assert.equal((await runCli(PROBE_PATH, secondFixture, {
    ...VALID_REQUEST,
    invocation_id: "probe-invocation-2"
  })).code, 0);

  const first = assertOwnedTabLifecycle(await firstFixture.readLog(), "probe");
  const second = assertOwnedTabLifecycle(await secondFixture.readLog(), "probe");
  assert.notEqual(first.session, second.session);
  assert.notEqual(first.label, second.label);
});

test("reports login required from a task-owned Google account chooser", async (t) => {
  const fixture = await createFixture(t, {
    pageUrl: "https://accounts.google.com/signin/accountchooser",
    loginCounts: {
      [LOGIN_SELECTORS.accountChoice]: 2,
      [LOGIN_SELECTORS.useAnotherAccount]: 1
    }
  });
  const result = await runCli(PROBE_PATH, fixture, VALID_REQUEST);

  assert.notEqual(result.code, 0);
  assert.equal(result.stdout, "");
  assertError(result.stderr, "email_login_required");
  assertOwnedTabLifecycle(await fixture.readLog(), "probe");
});

test("fails closed on conflicting login evidence", async (t) => {
  const fixture = await createFixture(t, readyScenario({
    loginCounts: {
      [LOGIN_SELECTORS.accountControl]: 1,
      [LOGIN_SELECTORS.composeControl]: 1,
      [LOGIN_SELECTORS.mainMenu]: 1,
      [LOGIN_SELECTORS.accountChoice]: 1,
      [LOGIN_SELECTORS.useAnotherAccount]: 1
    }
  }));
  const result = await runCli(PROBE_PATH, fixture, VALID_REQUEST);

  assert.notEqual(result.code, 0);
  assertError(result.stderr, "email_login_evidence_conflict");
  assertOwnedTabLifecycle(await fixture.readLog(), "probe");
});

test("fails closed when the Gmail page contract is incomplete", async (t) => {
  const fixture = await createFixture(t, readyScenario({
    loginCounts: {
      [LOGIN_SELECTORS.accountControl]: 1,
      [LOGIN_SELECTORS.mainMenu]: 1
    }
  }));
  const result = await runCli(PROBE_PATH, fixture, VALID_REQUEST);

  assert.notEqual(result.code, 0);
  assertError(result.stderr, "email_page_contract_changed");
  assertOwnedTabLifecycle(await fixture.readLog(), "probe");
});

test("rejects an origin outside the Gmail provider allowlist", async (t) => {
  const fixture = await createFixture(t, {
    pageUrl: "https://example.com/inbox",
    loginCounts: {}
  });
  const result = await runCli(PROBE_PATH, fixture, VALID_REQUEST);

  assert.notEqual(result.code, 0);
  assertError(result.stderr, "email_provider_origin_invalid");
  const log = await fixture.readLog();
  assert.deepEqual(log[0].command.slice(0, 2), ["tab", "new"]);
  assert.deepEqual(log.at(-2).command.slice(0, 2), ["tab", "close"]);
  assert.deepEqual(log.at(-1).command, ["close"]);
});

test("rejects profile, state, restore, auto-connect, and inherited config environments", async (t) => {
  const cases = [
    ["AGENT_BROWSER_PROFILE", "/tmp/profile"],
    ["AGENT_BROWSER_STATE", "/tmp/state.json"],
    ["AGENT_BROWSER_RESTORE", "saved"],
    ["AGENT_BROWSER_AUTO_CONNECT", "false"],
    ["AGENT_BROWSER_CONFIG", "/tmp/config.json"],
    ["AGENT_BROWSER_ARGS", "--user-data-dir=/tmp/profile"],
    ["AGENT_BROWSER_EXECUTABLE_PATH", "/tmp/chromium"]
  ];

  for (const [key, value] of cases) {
    await t.test(key, async (subtest) => {
      const fixture = await createFixture(subtest, readyScenario());
      const result = await runCli(PROBE_PATH, fixture, VALID_REQUEST, {
        extraEnvironment: { [key]: value }
      });
      assert.notEqual(result.code, 0);
      assert.equal(result.stdout, "");
      assertError(result.stderr, "email_probe_configuration_error");
      assert.deepEqual(await fixture.readLog(), []);
    });
  }
});

test("accepts the Runner-owned session environment without forwarding it", async (t) => {
  const fixture = await createFixture(t, readyScenario());
  const result = await runCli(PROBE_PATH, fixture, VALID_REQUEST, {
    extraEnvironment: { AGENT_BROWSER_SESSION: "sc-email-runtime" }
  });

  assert.equal(result.code, 0);
  assertOwnedTabLifecycle(await fixture.readLog(), "probe");
});

test("requires AGENT_BROWSER_CDP", async (t) => {
  const fixture = await createFixture(t, readyScenario());
  const result = await runCli(PROBE_PATH, fixture, VALID_REQUEST, { omitCdp: true });

  assert.notEqual(result.code, 0);
  assertError(result.stderr, "email_probe_configuration_error");
  assert.deepEqual(await fixture.readLog(), []);
});

test("strictly rejects unknown input fields before creating a tab", async (t) => {
  const fixture = await createFixture(t, readyScenario());
  const result = await runCli(PROBE_PATH, fixture, {
    ...VALID_REQUEST,
    unexpected: true
  });

  assert.notEqual(result.code, 0);
  assertError(result.stderr, "email_probe_invalid_input");
  assert.deepEqual(await fixture.readLog(), []);
});

test("fails closed on invalid agent-browser output and attempts label cleanup", async (t) => {
  const fixture = await createFixture(t, readyScenario({ invalidOutputAt: 3 }));
  const result = await runCli(PROBE_PATH, fixture, VALID_REQUEST);

  assert.notEqual(result.code, 0);
  assertError(result.stderr, "email_agent_browser_invalid_output");
  const log = await fixture.readLog();
  assert.deepEqual(log.at(-2).command.slice(0, 2), ["tab", "close"]);
  assert.deepEqual(log.at(-1).command, ["close"]);
});

test("fails closed on an agent-browser timeout and attempts label cleanup", async (t) => {
  const fixture = await createFixture(t, readyScenario({ timeoutAt: 3 }));
  const result = await runCli(PROBE_PATH, fixture, VALID_REQUEST, {
    commandTimeoutMs: 50
  });

  assert.notEqual(result.code, 0);
  assertError(result.stderr, "email_probe_timeout");
  const log = await fixture.readLog();
  assert.deepEqual(log.at(-2).command.slice(0, 2), ["tab", "close"]);
  assert.deepEqual(log.at(-1).command, ["close"]);
});
