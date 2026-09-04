import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import {
  LOGIN_SELECTORS,
  SEND_SELECTORS,
  assertError,
  assertOwnedTabLifecycle,
  createFixture,
  parseOnlyJson,
  runCli
} from "./gmail-cli-test-helper.mjs";

const SCRIPT_DIR = dirname(fileURLToPath(import.meta.url));
const SEND_PATH = resolve(SCRIPT_DIR, "gmail-send.mjs");
const VALID_REQUEST = {
  schema_version: 1,
  operation: "send",
  invocation_id: "send-invocation",
  provider: "gmail",
  account: "default",
  message: {
    recipient: "recipient@example.com",
    subject: "Deterministic subject",
    body: {
      format: "text",
      content: "Private message body\nSecond line"
    }
  }
};

function readyScenario(overrides = {}) {
  return {
    pageUrl: "https://mail.google.com/mail/u/0/#inbox",
    loginCounts: {
      [LOGIN_SELECTORS.accountControl]: 1,
      [LOGIN_SELECTORS.composeControl]: 1,
      [LOGIN_SELECTORS.mainMenu]: 1
    },
    sendOutcome: "success",
    ...overrides
  };
}

test("sends once after field verification and cleans up only the owned tab", async (t) => {
  const fixture = await createFixture(t, readyScenario());
  const result = await runCli(SEND_PATH, fixture, VALID_REQUEST);

  assert.equal(result.code, 0);
  assert.equal(result.stderr, "");
  assert.deepEqual(parseOnlyJson(result.stdout), {
    schema_version: 1,
    status: "sent",
    provider: "gmail",
    recipient_digest: `sha256:${createHash("sha256")
      .update(VALID_REQUEST.message.recipient, "utf8")
      .digest("hex")}`
  });
  assert.equal(result.stdout.includes(VALID_REQUEST.message.recipient), false);
  assert.equal(result.stdout.includes(VALID_REQUEST.message.body.content), false);
  assert.equal(result.stdout.includes("9222"), false);

  const log = await fixture.readLog();
  const ownership = assertOwnedTabLifecycle(log, "send");
  assert.equal(result.stdout.includes(ownership.session), false);
  assert.equal(result.stdout.includes(ownership.label), false);
  assert.equal(result.stdout.includes("t90"), false);
  const sendClicks = log.filter((entry) =>
    entry.command[0] === "click" && entry.command[1] === SEND_SELECTORS.send
  );
  assert.equal(sendClicks.length, 1);
  const state = await fixture.readState();
  assert.equal(state.sendClicks, 1);
  assert.deepEqual(state.tabs, {});
});

test("preserves an explicitly empty subject", async (t) => {
  const fixture = await createFixture(t, readyScenario());
  const request = {
    ...VALID_REQUEST,
    message: { ...VALID_REQUEST.message, subject: "" }
  };
  const result = await runCli(SEND_PATH, fixture, request, {
    extraEnvironment: { AGENT_BROWSER_SESSION: "sc-email-runtime" }
  });

  assert.equal(result.code, 0);
  assert.equal((await fixture.readState()).subject, "");
});

test("strictly rejects invalid fields before creating a tab", async (t) => {
  const cases = [
    ["unknown top-level field", { ...VALID_REQUEST, unexpected: true }],
    ["unknown message field", {
      ...VALID_REQUEST,
      message: { ...VALID_REQUEST.message, cc: "other@example.com" }
    }],
    ["unknown body field", {
      ...VALID_REQUEST,
      message: {
        ...VALID_REQUEST.message,
        body: { ...VALID_REQUEST.message.body, html: "<b>no</b>" }
      }
    }],
    ["multiple recipients", {
      ...VALID_REQUEST,
      message: { ...VALID_REQUEST.message, recipient: "one@example.com,two@example.com" }
    }],
    ["newline subject", {
      ...VALID_REQUEST,
      message: { ...VALID_REQUEST.message, subject: "first\nsecond" }
    }],
    ["empty body", {
      ...VALID_REQUEST,
      message: {
        ...VALID_REQUEST.message,
        body: { format: "text", content: " \n\t " }
      }
    }],
    ["non-text format", {
      ...VALID_REQUEST,
      message: {
        ...VALID_REQUEST.message,
        body: { format: "html", content: "content" }
      }
    }]
  ];

  for (const [name, request] of cases) {
    await t.test(name, async (subtest) => {
      const fixture = await createFixture(subtest, readyScenario());
      const result = await runCli(SEND_PATH, fixture, request);
      assert.notEqual(result.code, 0);
      assert.equal(result.stdout, "");
      assertError(result.stderr, "email_send_invalid_input");
      assert.deepEqual(await fixture.readLog(), []);
    });
  }
});

test("rejects forbidden browser startup environments", async (t) => {
  for (const [key, value] of [
    ["AGENT_BROWSER_PROFILE", "/tmp/profile"],
    ["AGENT_BROWSER_STATE", "/tmp/state.json"],
    ["AGENT_BROWSER_RESTORE", "saved"],
    ["AGENT_BROWSER_AUTO_CONNECT", "1"],
    ["AGENT_BROWSER_ARGS", "--profile-directory=Default"],
    ["AGENT_BROWSER_EXECUTABLE_PATH", "/tmp/chromium"]
  ]) {
    await t.test(key, async (subtest) => {
      const fixture = await createFixture(subtest, readyScenario());
      const result = await runCli(SEND_PATH, fixture, VALID_REQUEST, {
        extraEnvironment: { [key]: value }
      });
      assert.notEqual(result.code, 0);
      assertError(result.stderr, "email_send_configuration_error");
      assert.deepEqual(await fixture.readLog(), []);
    });
  }
});

test("fails before the send click when a field readback differs", async (t) => {
  const fixture = await createFixture(t, readyScenario({ bodyReadback: "different body" }));
  const result = await runCli(SEND_PATH, fixture, VALID_REQUEST);

  assert.notEqual(result.code, 0);
  assert.equal(result.stdout, "");
  assertError(result.stderr, "email_send_precondition_failed");
  assert.equal(result.stderr.includes(VALID_REQUEST.message.recipient), false);
  assert.equal(result.stderr.includes(VALID_REQUEST.message.body.content), false);
  const log = await fixture.readLog();
  assertOwnedTabLifecycle(log, "send");
  assert.equal(log.some((entry) =>
    entry.command[0] === "click" && entry.command[1] === SEND_SELECTORS.send
  ), false);
  assert.equal((await fixture.readState()).sendClicks, 0);
});

test("returns send_outcome_unknown without retry after one send click", async (t) => {
  const fixture = await createFixture(t, readyScenario({ sendOutcome: "unknown" }));
  const result = await runCli(SEND_PATH, fixture, VALID_REQUEST);

  assert.notEqual(result.code, 0);
  assert.equal(result.stdout, "");
  assertError(result.stderr, "send_outcome_unknown");
  assert.equal(result.stderr.includes(VALID_REQUEST.message.recipient), false);
  assert.equal(result.stderr.includes(VALID_REQUEST.message.body.content), false);
  const log = await fixture.readLog();
  assertOwnedTabLifecycle(log, "send");
  const sendClicks = log.filter((entry) =>
    entry.command[0] === "click" && entry.command[1] === SEND_SELECTORS.send
  );
  assert.equal(sendClicks.length, 1);
  assert.equal((await fixture.readState()).sendClicks, 1);
});

test("does not compose when the owned tab is logged out", async (t) => {
  const fixture = await createFixture(t, {
    pageUrl: "https://accounts.google.com/signin/accountchooser",
    loginCounts: {
      [LOGIN_SELECTORS.accountChoice]: 1,
      [LOGIN_SELECTORS.useAnotherAccount]: 1
    }
  });
  const result = await runCli(SEND_PATH, fixture, VALID_REQUEST);

  assert.notEqual(result.code, 0);
  assertError(result.stderr, "email_login_required");
  const log = await fixture.readLog();
  assertOwnedTabLifecycle(log, "send");
  assert.equal(log.some((entry) => entry.command[0] === "fill"), false);
  assert.equal((await fixture.readState()).sendClicks, 0);
});
