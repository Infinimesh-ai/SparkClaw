import assert from "node:assert/strict";
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

const SCRIPT = fileURLToPath(new URL("./outlook-login-probe.mjs", import.meta.url));
const VALID_INPUT = {
  schema_version: 1,
  operation: "probe",
  invocation_id: "opaque-id",
  provider: "outlook",
  account: "default",
};

test("probes only unique task-owned tabs and closes each by label", async () => {
  const fixture = await createFixture({ probe: READY_EVIDENCE });
  const first = await runCli(SCRIPT, VALID_INPUT, fixture);
  const second = await runCli(SCRIPT, VALID_INPUT, fixture);
  assert.equal(first.code, 0);
  assert.equal(second.code, 0);
  assert.deepEqual(JSON.parse(first.stdout), {
    schema_version: 1,
    status: "ready",
    provider: "outlook",
  });

  const state = await readState(fixture);
  const invocations = await readInvocations(fixture);
  assert.equal(state.created.length, 2);
  assert.notEqual(state.created[0].session, state.created[1].session);
  assert.notEqual(state.created[0].label, state.created[1].label);
  assert.ok(state.created.every((item) => item.url === "about:blank"));
  assert.ok(state.opened.every((item) => item.url === "https://outlook.live.com/mail/"));
  assertOwnedTabDiscipline(invocations, state);
  assert.ok(invocations.every((item) => !item.command.includes("list")));
});

test("keeps the probe read-only and away from message content selectors", async () => {
  const fixture = await createFixture({ probe: READY_EVIDENCE });
  const result = await runCli(SCRIPT, VALID_INPUT, fixture);
  assert.equal(result.code, 0);

  const state = await readState(fixture);
  const mutatingActions = new Set(["click", "fill", "type", "press", "focus"]);
  const forbiddenContentPatterns = [
    /message[-_ ]?list/i,
    /reading[-_ ]?pane/i,
    /attachment/i,
    /aria-label=.?message body/i,
    /aria-label=.?email body/i,
    /role=.?row/i,
  ];

  for (const bound of state.bound) {
    if (bound.commands[1][0] === "open") {
      assert.deepEqual(bound.commands, [
        ["tab", bound.label],
        ["open", "https://outlook.live.com/mail/"],
        ["wait", "3000"],
        ["get", "url"],
      ]);
      continue;
    }
    assert.equal(bound.commands.length, 2);
    assert.equal(bound.commands[1][0], "eval");
    assert.ok(bound.commands.every((command) => !mutatingActions.has(command[0])));
    const expression = Buffer.from(bound.commands[1][2], "base64").toString("utf8");
    for (const pattern of forbiddenContentPatterns) {
      assert.doesNotMatch(expression, pattern);
    }
  }
});

test("reports login required and still closes the owned tab", async () => {
  const fixture = await createFixture({
    probe: {
      url: "https://login.live.com/login.srf",
      positive: { app_shell: false, compose_command: false, mail_navigation: false },
      negative: { credential_entry: true, account_chooser: false, sign_in_action: true },
    },
  });
  const result = await runCli(SCRIPT, VALID_INPUT, fixture);
  assertErrorCode(result, "email_login_required");
  assertOwnedTabDiscipline(await readInvocations(fixture), await readState(fixture));
});

test("reports login required for the exact Microsoft Outlook signed-out landing", async () => {
  const fixture = await createFixture({
    probe: {
      url: "https://www.microsoft.com/en-us/microsoft-365/outlook/email-and-calendar-software-microsoft-outlook?deeplink=%2Fmail%2F&sdf=0",
    },
  });
  const result = await runCli(SCRIPT, VALID_INPUT, fixture);
  assertErrorCode(result, "email_login_required");
  assertOwnedTabDiscipline(await readInvocations(fixture), await readState(fixture));
});

test("returns only a masked email hint from an explicit account label", async () => {
  const displayName = "Zhang Junsong";
  const fullEmail = "User.Name@Example.COM";
  const fixture = await createFixture({
    probe: {
      ...READY_EVIDENCE,
      accountMarker: {
        source: "account_control",
        label: `Account manager for ${displayName} (${fullEmail})`,
      },
    },
  });
  const result = await runCli(SCRIPT, VALID_INPUT, fixture);
  assert.deepEqual(JSON.parse(result.stdout), {
    schema_version: 1,
    status: "ready",
    provider: "outlook",
    account_hint: "Us***@example.com",
  });
  assert.doesNotMatch(result.stdout, /User\.Name@Example\.COM|user\.name@example\.com/);
  assert.equal(result.stdout.includes(displayName), false);
});

test("keeps at most two Unicode characters from the account email local part", async () => {
  const displayName = "\u5f20\u5cfb\u677e";
  const fullEmail = "\u5f20\u5cfb\u677e@Example.COM";
  const fixture = await createFixture({
    probe: {
      ...READY_EVIDENCE,
      accountMarker: {
        source: "account_control",
        label: `\u8d26\u6237\u7ba1\u7406 ${displayName} <${fullEmail}>`,
      },
    },
  });
  const result = await runCli(SCRIPT, VALID_INPUT, fixture);
  assert.deepEqual(JSON.parse(result.stdout), {
    schema_version: 1,
    status: "ready",
    provider: "outlook",
    account_hint: "\u5f20\u5cfb***@example.com",
  });
  assert.equal(result.stdout.includes(fullEmail), false);
  assert.equal(result.stdout.includes(displayName), false);
});

test("omits account_hint when the account control has only a display name", async () => {
  const displayName = "Zhang Junsong";
  const fixture = await createFixture({
    probe: {
      ...READY_EVIDENCE,
      accountMarker: {
        source: "account_control",
        label: `Account manager for ${displayName}`,
      },
    },
  });
  const result = await runCli(SCRIPT, VALID_INPUT, fixture);
  assert.deepEqual(JSON.parse(result.stdout), {
    schema_version: 1,
    status: "ready",
    provider: "outlook",
  });
  assert.equal(result.stdout.includes(displayName), false);
});

test("fails closed for an unrelated origin and cleans up", async () => {
  const fixture = await createFixture({
    probe: { ...READY_EVIDENCE, url: "https://example.com/mail/" },
  });
  const result = await runCli(SCRIPT, VALID_INPUT, fixture);
  assertErrorCode(result, "outlook_origin_not_allowed");
  assertOwnedTabDiscipline(await readInvocations(fixture), await readState(fixture));
});

test("fails closed when positive and negative evidence conflict", async () => {
  const fixture = await createFixture({
    probe: {
      positive: { app_shell: true, compose_command: true, mail_navigation: false },
      negative: { credential_entry: true, account_chooser: false, sign_in_action: false },
    },
  });
  const result = await runCli(SCRIPT, VALID_INPUT, fixture);
  assertErrorCode(result, "outlook_evidence_conflict");
  assertOwnedTabDiscipline(await readInvocations(fixture), await readState(fixture));
});

test("fails closed when the Outlook page contract changes", async () => {
  const fixture = await createFixture({
    probe: {
      positive: { app_shell: true, compose_command: false, mail_navigation: false },
      negative: { credential_entry: false, account_chooser: false, sign_in_action: false },
    },
  });
  const result = await runCli(SCRIPT, VALID_INPUT, fixture);
  assertErrorCode(result, "outlook_page_contract_changed");
  assertOwnedTabDiscipline(await readInvocations(fixture), await readState(fixture));
});

test("fails closed on invalid agent-browser output", async () => {
  const fixture = await createFixture({ rawOutput: "not-json\n" });
  const result = await runCli(SCRIPT, VALID_INPUT, fixture);
  assertErrorCode(result, "agent_browser_invalid_output");
});

test("fails closed on agent-browser timeout", async () => {
  const fixture = await createFixture({ hang: true });
  const result = await runCli(SCRIPT, VALID_INPUT, fixture, {
    OUTLOOK_LOGIN_PROBE_TIMEOUT_MS: "40",
  });
  assertErrorCode(result, "agent_browser_timeout");
});

test("rejects forbidden browser startup state before invoking agent-browser", async (t) => {
  for (const name of [
    "AGENT_BROWSER_PROFILE",
    "AGENT_BROWSER_STATE",
    "AGENT_BROWSER_RESTORE",
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

test("requires AGENT_BROWSER_CDP", async () => {
  const fixture = await createFixture();
  const result = await runCli(SCRIPT, VALID_INPUT, fixture, { AGENT_BROWSER_CDP: "" });
  assertErrorCode(result, "browser_configuration_invalid");
  assert.deepEqual(await readInvocations(fixture), []);
});

test("does not forward CDP, tab, or session identifiers", async () => {
  const fixture = await createFixture({ probe: READY_EVIDENCE });
  const result = await runCli(SCRIPT, VALID_INPUT, fixture);
  const state = await readState(fixture);
  const output = result.stdout + result.stderr;
  assert.equal(output.includes(FAKE_CDP), false);
  assert.equal(output.includes(state.created[0].session), false);
  assert.equal(output.includes(state.created[0].label), false);
  assert.equal(output.includes("t-owned"), false);
});
