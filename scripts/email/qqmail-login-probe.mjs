#!/usr/bin/env node

import path from "node:path";
import { pathToFileURL } from "node:url";

import {
  QQMailScriptError,
  normalizeVisibleText,
  resultAt,
} from "./lib/qqmail-browser.mjs";
import {
  parseQQMailURL,
  withQQMailTaskTab,
} from "./lib/qqmail-host-cdp.mjs";

const INPUT_LIMIT_BYTES = 16 * 1024;
const ACCOUNT_HINT_LIMIT = 64;

export const QQMAIL_LOGIN_PROBE_SELECTORS = Object.freeze({
  composeControl: '.frame-sidebar .frame-sidebar-compose-btn[data-a11y="button"]',
  profileControl: ".frame-header .xmail-cmp-profile-btn",
  accountMarker: ".frame-header .xmail-cmp-profile-btn .profile-user-info .user-email",
  loginPage: ".login-page",
  loginTabs: ".login-page .xmail-cmp-head-tabs",
  appLogin: ".login-page .xmail-cmp-app-login",
  wechatLoginFrame: ".login-page .xmail-cmp-wx-login-wrap iframe",
  qqPasswordLoginFrame: ".login-page .xmail-cmp-qq-pt-login-wrap iframe",
  qqConnectLoginFrame: ".login-page .xmail-cmp-qq-connect-login-wrap iframe",
});

const POSITIVE_EVIDENCE = Object.freeze([
  ["composeControl", QQMAIL_LOGIN_PROBE_SELECTORS.composeControl],
  ["profileControl", QQMAIL_LOGIN_PROBE_SELECTORS.profileControl],
  ["accountMarker", QQMAIL_LOGIN_PROBE_SELECTORS.accountMarker],
]);

const NEGATIVE_EVIDENCE = Object.freeze([
  ["loginPage", QQMAIL_LOGIN_PROBE_SELECTORS.loginPage],
  ["loginTabs", QQMAIL_LOGIN_PROBE_SELECTORS.loginTabs],
  ["appLogin", QQMAIL_LOGIN_PROBE_SELECTORS.appLogin],
  ["wechatLoginFrame", QQMAIL_LOGIN_PROBE_SELECTORS.wechatLoginFrame],
  ["qqPasswordLoginFrame", QQMAIL_LOGIN_PROBE_SELECTORS.qqPasswordLoginFrame],
  ["qqConnectLoginFrame", QQMAIL_LOGIN_PROBE_SELECTORS.qqConnectLoginFrame],
]);

function strictInput(input) {
  if (!input || typeof input !== "object" || Array.isArray(input)) {
    throw new QQMailScriptError("invalid_input", "stdin must contain one JSON object");
  }

  const expectedKeys = ["account", "invocation_id", "operation", "provider", "schema_version"];
  const actualKeys = Object.keys(input).sort();
  if (
    actualKeys.length !== expectedKeys.length ||
    actualKeys.some((key, index) => key !== expectedKeys[index])
  ) {
    throw new QQMailScriptError("invalid_input", "stdin does not match the login probe schema");
  }
  if (
    input.schema_version !== 1 ||
    input.operation !== "probe" ||
    input.provider !== "qq_mail" ||
    input.account !== "default"
  ) {
    throw new QQMailScriptError("invalid_input", "stdin does not match the login probe schema");
  }
  if (
    typeof input.invocation_id !== "string" ||
    !/^[A-Za-z0-9._:-]{1,128}$/u.test(input.invocation_id)
  ) {
    throw new QQMailScriptError("invalid_input", "invocation_id must be an opaque ASCII identifier");
  }
}

async function readJSONInput(stream) {
  let raw = "";
  for await (const chunk of stream) {
    raw += chunk;
    if (Buffer.byteLength(raw, "utf8") > INPUT_LIMIT_BYTES) {
      throw new QQMailScriptError("input_too_large", "stdin exceeds the login probe input limit");
    }
  }
  if (!raw.trim()) {
    throw new QQMailScriptError("missing_input", "provide the login probe request on stdin");
  }
  try {
    return JSON.parse(raw);
  } catch {
    throw new QQMailScriptError("invalid_json", "stdin is not valid JSON");
  }
}

function countAt(results, index, phase) {
  const count = resultAt(results, index, phase).count;
  if (!Number.isInteger(count) || count < 0) {
    throw new QQMailScriptError(
      "login_probe_invalid_output",
      "agent-browser returned invalid login evidence",
    );
  }
  return count;
}

function evidenceCountCommands() {
  const commands = [["get", "url"]];
  for (const [, selector] of [...POSITIVE_EVIDENCE, ...NEGATIVE_EVIDENCE]) {
    commands.push(["get", "count", selector]);
  }
  return commands;
}

function readEvidenceCounts(results, phase) {
  const evidence = {};
  let offset = 1;
  for (const [name] of [...POSITIVE_EVIDENCE, ...NEGATIVE_EVIDENCE]) {
    evidence[name] = { count: countAt(results, offset, phase), visible: false };
    offset += 1;
  }
  return evidence;
}

async function collectEvidence(task, phase) {
  const countResults = await task.onTab(evidenceCountCommands(), `${phase}_counts`);
  const countLocation = parseQQMailURL(
    resultAt(countResults, 0, `${phase}_counts`).url,
    "login_probe_invalid_output",
  );
  const evidence = readEvidenceCounts(countResults, `${phase}_counts`);
  const present = [...POSITIVE_EVIDENCE, ...NEGATIVE_EVIDENCE].filter(
    ([name]) => evidence[name].count > 0,
  );

  if (present.length === 0) return { evidence, location: countLocation };

  const visibleResults = await task.onTab(
    [["get", "url"], ...present.map(([, selector]) => ["is", "visible", selector])],
    `${phase}_visibility`,
  );
  const visibleLocation = parseQQMailURL(
    resultAt(visibleResults, 0, `${phase}_visibility`).url,
    "login_probe_invalid_output",
  );
  if (
    visibleLocation.origin !== countLocation.origin ||
    visibleLocation.pathname !== countLocation.pathname
  ) {
    throw new QQMailScriptError(
      "login_evidence_conflict",
      "QQ Mail changed page while login evidence was collected",
    );
  }
  for (let index = 0; index < present.length; index += 1) {
    const [name] = present[index];
    const visible = resultAt(
      visibleResults,
      index + 1,
      `${phase}_visibility`,
    ).visible;
    if (typeof visible !== "boolean") {
      throw new QQMailScriptError(
        "login_probe_invalid_output",
        "agent-browser returned invalid login visibility evidence",
      );
    }
    evidence[name].visible = visible;
  }
  return { evidence, location: visibleLocation };
}

function isVisibleEvidence(value) {
  return value.count > 0 && value.visible;
}

function hasCompletePositiveEvidence(evidence) {
  return POSITIVE_EVIDENCE.every(([name]) => {
    const value = evidence[name];
    return value.count === 1 && value.visible;
  });
}

function hasAnyPositiveEvidence(evidence) {
  return POSITIVE_EVIDENCE.some(([name]) => isVisibleEvidence(evidence[name]));
}

function hasCompleteNegativeEvidence(evidence) {
  const loginPageVisible = isVisibleEvidence(evidence.loginPage);
  const loginMechanismVisible = NEGATIVE_EVIDENCE.slice(1).some(([name]) =>
    isVisibleEvidence(evidence[name]),
  );
  return loginPageVisible && loginMechanismVisible;
}

function hasAnyNegativeEvidence(evidence) {
  return NEGATIVE_EVIDENCE.some(([name]) => isVisibleEvidence(evidence[name]));
}

function requireConsistentEvidence(evidence) {
  const positiveComplete = hasCompletePositiveEvidence(evidence);
  const negativeComplete = hasCompleteNegativeEvidence(evidence);
  if (
    (hasAnyPositiveEvidence(evidence) && hasAnyNegativeEvidence(evidence)) ||
    (positiveComplete && negativeComplete)
  ) {
    throw new QQMailScriptError(
      "login_evidence_conflict",
      "QQ Mail exposed conflicting authenticated and login-page evidence",
    );
  }
  return { positiveComplete, negativeComplete };
}

function accountHint(value) {
  const account = normalizeVisibleText(value);
  if (
    account.length > 254 ||
    !/^[A-Za-z0-9.!#$%&'*+/=?^_`{|}~-]+@[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?$/u.test(account)
  ) {
    throw new QQMailScriptError("page_contract_changed", "QQ Mail account marker is not usable");
  }

  const separator = account.lastIndexOf("@");
  const local = account.slice(0, separator);
  const domain = account.slice(separator + 1).toLowerCase();
  const localPrefix = [...local].slice(0, Math.min(2, [...local].length)).join("");
  const hint = `${localPrefix}***@${domain}`;
  return [...hint].slice(0, ACCOUNT_HINT_LIMIT).join("");
}

function normalizedProbeError(error) {
  if (!(error instanceof QQMailScriptError)) {
    return new QQMailScriptError("login_probe_browser_failure", "QQ Mail login probe failed");
  }
  if (error.code.endsWith("_timeout")) {
    return new QQMailScriptError("login_probe_timeout", "QQ Mail login probe timed out");
  }
  if (error.code.endsWith("_invalid_output") || error.code.endsWith("_browser_output_invalid")) {
    return new QQMailScriptError(
      "login_probe_invalid_output",
      "agent-browser returned invalid login probe output",
    );
  }
  const preserved = new Set([
    "agent_browser_unavailable",
    "email_login_required",
    "forbidden_browser_environment",
    "host_cdp_required",
    "login_evidence_conflict",
    "page_contract_changed",
    "provider_origin_mismatch",
    "task_tab_cleanup_failed",
  ]);
  if (preserved.has(error.code)) return error;
  return new QQMailScriptError("login_probe_browser_failure", "QQ Mail login probe failed");
}

export async function probeQQMailLogin(rawInput, runtime = {}) {
  strictInput(rawInput);
  try {
    return await withQQMailTaskTab("probe", runtime, async (task) => {
      const { evidence, location: evidenceLocation } = await collectEvidence(
        task,
        "login_probe_evidence",
      );
      const state = requireConsistentEvidence(evidence);

      if (state.negativeComplete) {
        throw new QQMailScriptError("email_login_required", "QQ Mail login is required");
      }
      if (!state.positiveComplete || evidenceLocation.pathname !== "/home/index") {
        throw new QQMailScriptError("page_contract_changed", "QQ Mail login evidence changed");
      }

      const { evidence: identityEvidence, location: identityLocation } = await collectEvidence(
        task,
        "login_probe_identity",
      );
      const identityState = requireConsistentEvidence(identityEvidence);
      if (!identityState.positiveComplete || identityLocation.pathname !== "/home/index") {
        throw new QQMailScriptError("page_contract_changed", "QQ Mail login evidence changed");
      }

      const identityResults = await task.onTab(
        [["get", "url"], ["get", "text", QQMAIL_LOGIN_PROBE_SELECTORS.accountMarker]],
        "login_probe_identity_marker",
      );
      const markerLocation = parseQQMailURL(
        resultAt(identityResults, 0, "login_probe_identity_marker").url,
        "login_probe_invalid_output",
      );
      if (
        markerLocation.origin !== identityLocation.origin ||
        markerLocation.pathname !== identityLocation.pathname
      ) {
        throw new QQMailScriptError(
          "login_evidence_conflict",
          "QQ Mail changed page while account identity was collected",
        );
      }
      const hint = accountHint(
        resultAt(identityResults, 1, "login_probe_identity_marker").text,
      );
      return {
        schema_version: 1,
        status: "ready",
        provider: "qq_mail",
        account_hint: hint,
      };
    });
  } catch (error) {
    throw normalizedProbeError(error);
  }
}

async function main() {
  if (process.argv.length !== 2) {
    throw new QQMailScriptError("invalid_arguments", "the login probe accepts stdin only");
  }
  const input = await readJSONInput(process.stdin);
  const result = await probeQQMailLogin(input);
  process.stdout.write(`${JSON.stringify(result)}\n`);
}

if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) {
  main().catch((error) => {
    const code = error instanceof QQMailScriptError ? error.code : "unexpected_error";
    process.stderr.write(
      `${JSON.stringify({ schema_version: 1, status: "failed", provider: "qq_mail", code })}\n`,
    );
    process.exitCode = 1;
  });
}
