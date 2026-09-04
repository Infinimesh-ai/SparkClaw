import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";
import { fileURLToPath } from "node:url";

import {
  FAKE_CDP,
  READY_EVIDENCE,
  assertErrorCode,
  assertOwnedTabDiscipline,
  createFixture,
  readInvocations,
  readState,
  runCli,
} from "./outlook-cli-test-fixture.mjs";

const SCRIPT = fileURLToPath(new URL("./outlook-send.mjs", import.meta.url));
const VALID_INPUT = {
  schema_version: 1,
  operation: "send",
  invocation_id: "send-opaque-id",
  provider: "outlook",
  account: "default",
  message: {
    recipient: "owner@example.com",
    subject: "Status",
    body: { format: "text", content: "Line one\nLine two" },
  },
};

test("sends once after verifying every supplied field and cleans up", async () => {
  const fixture = await createFixture({ probe: READY_EVIDENCE, sendOutcome: "sent" });
  const result = await runCli(SCRIPT, VALID_INPUT, fixture);
  assert.equal(result.code, 0);
  assert.equal(result.stderr, "");
  assert.deepEqual(JSON.parse(result.stdout), {
    schema_version: 1,
    status: "sent",
    provider: "outlook",
    recipient_digest: `sha256:${createHash("sha256")
      .update(VALID_INPUT.message.recipient, "utf8")
      .digest("hex")}`,
  });

  const state = await readState(fixture);
  const invocations = await readInvocations(fixture);
  assert.deepEqual(state.fields, {
    recipient: VALID_INPUT.message.recipient,
    subject: VALID_INPUT.message.subject,
    body: VALID_INPUT.message.body.content,
  });
  assert.equal(state.newMailClicks, 1);
  assert.equal(state.sendClicks, 1);
  assert.deepEqual(state.presses, ["Enter"]);
  assertOwnedTabDiscipline(invocations, state);
  assertSensitiveValuesAbsent(result, state);

  const boundActions = state.bound.map((item) => item.commands.at(-1));
  assert.ok(boundActions.some((action) => action[0] === "get" && action[1] === "value"));
  assert.ok(boundActions.some((action) => action[0] === "get" && action[1] === "text"));
});

test("fails before Send when a field cannot be verified", async () => {
  const fixture = await createFixture({
    probe: READY_EVIDENCE,
    recipientValueOverride: "different@example.com",
  });
  const result = await runCli(SCRIPT, VALID_INPUT, fixture);
  assertErrorCode(result, "field_verification_failed");
  const state = await readState(fixture);
  assert.equal(state.sendClicks, 0);
  assertOwnedTabDiscipline(await readInvocations(fixture), state);
  assertSensitiveValuesAbsent(result, state);
});

test("does not compose when the owned tab requires login", async () => {
  const fixture = await createFixture({
    probe: {
      url: "https://login.microsoftonline.com/common/oauth2/authorize",
      positive: { app_shell: false, compose_command: false, mail_navigation: false },
      negative: { credential_entry: true, account_chooser: false, sign_in_action: true },
    },
  });
  const result = await runCli(SCRIPT, VALID_INPUT, fixture);
  assertErrorCode(result, "email_login_required");
  const state = await readState(fixture);
  assert.equal(state.newMailClicks, 0);
  assert.equal(state.sendClicks, 0);
  assertOwnedTabDiscipline(await readInvocations(fixture), state);
});

test("returns send_outcome_unknown without retry after an inconclusive click", async () => {
  const fixture = await createFixture({ probe: READY_EVIDENCE, sendOutcome: "unknown" });
  const result = await runCli(SCRIPT, VALID_INPUT, fixture);
  assertErrorCode(result, "send_outcome_unknown");
  const state = await readState(fixture);
  assert.equal(state.sendClicks, 1);
  assertOwnedTabDiscipline(await readInvocations(fixture), state);
  assertSensitiveValuesAbsent(result, state);
});

test("treats a failed Send command as unknown and never clicks twice", async () => {
  const fixture = await createFixture({ probe: READY_EVIDENCE, sendClickFailure: true });
  const result = await runCli(SCRIPT, VALID_INPUT, fixture);
  assertErrorCode(result, "send_outcome_unknown");
  const state = await readState(fixture);
  assert.equal(state.sendClicks, 1);
  assertOwnedTabDiscipline(await readInvocations(fixture), state);
});

test("treats cleanup failure after the Send click as an unknown outcome", async () => {
  const fixture = await createFixture({
    probe: READY_EVIDENCE,
    sendOutcome: "sent",
    cleanupFailure: true,
  });
  const result = await runCli(SCRIPT, VALID_INPUT, fixture);
  assertErrorCode(result, "send_outcome_unknown");
  const state = await readState(fixture);
  assert.equal(state.sendClicks, 1);
  assert.deepEqual(state.closeAttempts, state.created.map(({ session, label }) => ({
    session,
    label,
  })));
});

test("supports omission of the optional subject", async () => {
  const fixture = await createFixture({ probe: READY_EVIDENCE, sendOutcome: "sent" });
  const input = {
    ...VALID_INPUT,
    message: {
      recipient: VALID_INPUT.message.recipient,
      body: VALID_INPUT.message.body,
    },
  };
  const result = await runCli(SCRIPT, input, fixture);
  assert.equal(result.code, 0);
  const state = await readState(fixture);
  assert.equal(Object.hasOwn(state.fields, "subject"), false);
  assert.equal(state.sendClicks, 1);
});

test("preserves an explicitly empty subject and accepts the Runner session", async () => {
  const fixture = await createFixture({ probe: READY_EVIDENCE, sendOutcome: "sent" });
  const input = {
    ...VALID_INPUT,
    message: { ...VALID_INPUT.message, subject: "" },
  };
  const result = await runCli(SCRIPT, input, fixture, {
    AGENT_BROWSER_SESSION: "sc-email-runtime",
  });

  assert.equal(result.code, 0);
  assert.equal((await readState(fixture)).fields.subject, "");
  const invocations = await readInvocations(fixture);
  assert.ok(invocations.every((entry) => entry.env.AGENT_BROWSER_SESSION === undefined));
});

test("strictly rejects invalid fields before opening a tab", async (t) => {
  const cases = [
    ["unknown field", { ...VALID_INPUT, extra: true }],
    ["multiple recipients", {
      ...VALID_INPUT,
      message: { ...VALID_INPUT.message, recipient: "a@example.com,b@example.com" },
    }],
    ["newline subject", {
      ...VALID_INPUT,
      message: { ...VALID_INPUT.message, subject: "one\ntwo" },
    }],
    ["empty body", {
      ...VALID_INPUT,
      message: { ...VALID_INPUT.message, body: { format: "text", content: "  \n" } },
    }],
    ["unknown message field", {
      ...VALID_INPUT,
      message: { ...VALID_INPUT.message, cc: "other@example.com" },
    }],
    ["unknown body field", {
      ...VALID_INPUT,
      message: {
        ...VALID_INPUT.message,
        body: { ...VALID_INPUT.message.body, encoding: "utf8" },
      },
    }],
    ["non-text body", {
      ...VALID_INPUT,
      message: {
        ...VALID_INPUT.message,
        body: { format: "html", content: "<b>no</b>" },
      },
    }],
  ];
  for (const [name, input] of cases) {
    await t.test(name, async () => {
      const fixture = await createFixture();
      const result = await runCli(SCRIPT, input, fixture);
      assertErrorCode(result, "invalid_request");
      assert.deepEqual(await readInvocations(fixture), []);
    });
  }
});

test("rejects every profile, state, restore, and auto-connect startup path", async (t) => {
  for (const name of [
    "AGENT_BROWSER_PROFILE",
    "AGENT_BROWSER_STATE",
    "AGENT_BROWSER_RESTORE",
    "AGENT_BROWSER_RESTORE_CHECK_URL",
    "AGENT_BROWSER_AUTO_CONNECT",
    "AGENT_BROWSER_CONFIG",
  ]) {
    await t.test(name, async () => {
      const fixture = await createFixture();
      const result = await runCli(SCRIPT, VALID_INPUT, fixture, { [name]: "forbidden" });
      assertErrorCode(result, "browser_configuration_invalid");
      assert.deepEqual(await readInvocations(fixture), []);
    });
  }
});

test("agent-browser receives CDP but no forbidden startup path", async () => {
  const fixture = await createFixture({ probe: READY_EVIDENCE, sendOutcome: "sent" });
  const result = await runCli(SCRIPT, VALID_INPUT, fixture);
  assert.equal(result.code, 0);
  const invocations = await readInvocations(fixture);
  for (const invocation of invocations) {
    assert.equal(invocation.env.AGENT_BROWSER_CDP, FAKE_CDP);
    for (const name of Object.keys(invocation.env)) {
      assert.doesNotMatch(name, /PROFILE|STATE|RESTORE|AUTO_CONNECT|SESSION_NAME/);
    }
    assert.equal(invocation.args.includes("--profile"), false);
    assert.equal(invocation.args.includes("--state"), false);
    assert.equal(invocation.args.includes("--restore"), false);
    assert.equal(invocation.args.includes("--auto-connect"), false);
  }
});

test("requires AGENT_BROWSER_CDP", async () => {
  const fixture = await createFixture();
  const result = await runCli(SCRIPT, VALID_INPUT, fixture, { AGENT_BROWSER_CDP: "" });
  assertErrorCode(result, "browser_configuration_invalid");
  assert.deepEqual(await readInvocations(fixture), []);
});

function assertSensitiveValuesAbsent(result, state) {
  const output = result.stdout + result.stderr;
  assert.equal(output.includes(VALID_INPUT.message.recipient), false);
  assert.equal(output.includes(VALID_INPUT.message.body.content), false);
  assert.equal(output.includes(FAKE_CDP), false);
  assert.equal(output.includes(state.created[0].session), false);
  assert.equal(output.includes(state.created[0].label), false);
  assert.equal(output.includes("t-owned"), false);
}
