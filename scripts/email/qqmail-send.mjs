#!/usr/bin/env node

import { createHash } from "node:crypto";
import {
  QQMailScriptError,
  normalizeVisibleText,
  resultAt,
} from "./lib/qqmail-browser.mjs";
import {
  parseQQMailURL,
  withQQMailTaskTab,
} from "./lib/qqmail-task.mjs";

const BODY_LIMIT_BYTES = 200 * 1024;

export const QQMAIL_SELECTORS = Object.freeze({
  composeButton: '.frame-sidebar-compose-btn[data-a11y="button"]',
  loginPage: ".login-page",
  composePage: ".mail-compose-page",
  recipient: 'input[aria-label="To"], input[aria-label="收件人"]',
  recipientChip: ".mail-compose-page .receiver-editor .xmail-cmp-account:not(.cmp-account-invalid)",
  subject: 'input[aria-label="Subject"], input[aria-label="主题"]',
  body: [
    '.mail-compose-page [contenteditable="true"][aria-label="Enter content"]',
    '.mail-compose-page [contenteditable="true"][aria-label="输入正文"]',
    '.mail-compose-page .mail-content-editor-inner[contenteditable="true"]',
  ].join(", "),
  sendButton: '.mail-compose-header .xmail-ui-btn[data-a11y="button"]',
  sentPage: ".mail-list-page",
});

function exactKeys(value, required, optional = []) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const actual = Object.keys(value).sort();
  const allowed = new Set([...required, ...optional]);
  return required.every((key) => Object.hasOwn(value, key)) && actual.every((key) => allowed.has(key));
}

function comparableBody(value) {
  return String(value ?? "").replace(/\r\n?/gu, "\n");
}

function validateInput(input) {
  if (
    !exactKeys(
      input,
      ["schema_version", "operation", "invocation_id", "provider", "account", "message"],
    ) ||
    input.schema_version !== 1 ||
    input.operation !== "send" ||
    input.provider !== "qq_mail" ||
    input.account !== "default" ||
    typeof input.invocation_id !== "string" ||
    !/^[A-Za-z0-9._:-]{1,128}$/u.test(input.invocation_id)
  ) {
    throw new QQMailScriptError("invalid_input", "stdin does not match the QQ Mail send schema");
  }

  const message = input.message;
  if (!exactKeys(message, ["recipient", "body"], ["subject"])) {
    throw new QQMailScriptError("invalid_message", "message contains missing or unknown fields");
  }
  if (!exactKeys(message.body, ["format", "content"])) {
    throw new QQMailScriptError("invalid_body", "body contains missing or unknown fields");
  }
  if (message.body.format !== "text" || typeof message.body.content !== "string") {
    throw new QQMailScriptError("invalid_body", "body format must be text");
  }

  if (typeof message.recipient !== "string") {
    throw new QQMailScriptError("invalid_recipient", "recipient must be one email address");
  }
  const recipient = message.recipient;
  if (
    recipient.length > 320 ||
    recipient !== recipient.trim() ||
    /[\s<>]/u.test(recipient) ||
    !/^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$/u.test(recipient)
  ) {
    throw new QQMailScriptError("invalid_recipient", "recipient must be one valid email address");
  }

  const body = message.body.content;
  if (!body.trim()) {
    throw new QQMailScriptError("invalid_body", "body content must not be empty");
  }
  if (Buffer.byteLength(body, "utf8") > BODY_LIMIT_BYTES) {
    throw new QQMailScriptError("body_too_large", "body exceeds the script limit");
  }

  let subject = "";
  if (Object.hasOwn(message, "subject")) {
    if (typeof message.subject !== "string") {
      throw new QQMailScriptError("invalid_subject", "subject must be text");
    }
    subject = message.subject;
    if (/[\r\n\0]/u.test(subject) || [...subject].length > 998) {
      throw new QQMailScriptError(
        "invalid_subject",
        "subject must be one line of at most 998 characters",
      );
    }
  }

  return { recipient, subject, body };
}

function requireCount(results, index, phase) {
  const count = resultAt(results, index, phase).count;
  if (!Number.isInteger(count) || count < 0) {
    throw new QQMailScriptError("send_browser_output_invalid", "browser runtime returned an invalid count");
  }
  return count;
}

function requireBoolean(results, index, field, phase) {
  const value = resultAt(results, index, phase)[field];
  if (typeof value !== "boolean") {
    throw new QQMailScriptError(
      "send_browser_output_invalid",
      "browser runtime returned an invalid control state",
    );
  }
  return value;
}

function composeLocation(value) {
  const parsed = parseQQMailURL(value, "send_browser_output_invalid");
  return parsed.pathname === "/home/index" && parsed.hash.startsWith("#/compose");
}

function sentLocation(value) {
  const parsed = parseQQMailURL(value, "send_browser_output_invalid");
  return parsed.pathname === "/home/index" && /^#\/list\/5(?:$|[/?])/u.test(parsed.hash);
}

function normalizedSendError(error) {
  if (!(error instanceof QQMailScriptError)) {
    return new QQMailScriptError("send_precondition_failed", "QQ Mail send preparation failed");
  }
  const preserved = new Set([
    "browser_runtime_unavailable",
    "draft_verification_failed",
    "email_login_required",
    "invalid_body",
    "invalid_input",
    "invalid_json",
    "invalid_message",
    "invalid_recipient",
    "invalid_subject",
    "input_too_large",
    "missing_input",
    "provider_origin_mismatch",
    "send_control_not_ready",
    "send_outcome_unknown",
    "task_tab_cleanup_failed",
  ]);
  if (preserved.has(error.code)) return error;
  return new QQMailScriptError("send_precondition_failed", "QQ Mail send preparation failed");
}

export async function sendQQMail(rawInput, runtime = {}) {
  const input = validateInput(rawInput);
  let sendClickAttempted = false;
  try {
    return await withQQMailTaskTab("send", runtime, async (task) => {
      const preflight = await task.onTab(
        [
          ["get", "url"],
          ["get", "count", QQMAIL_SELECTORS.composeButton],
          ["is", "visible", QQMAIL_SELECTORS.composeButton],
          ["get", "count", QQMAIL_SELECTORS.loginPage],
          ["is", "visible", QQMAIL_SELECTORS.loginPage],
        ],
        "send_preflight",
      );
      parseQQMailURL(resultAt(preflight, 0, "send_preflight").url, "send_browser_output_invalid");
      const composeCount = requireCount(preflight, 1, "send_preflight");
      const composeVisible = requireBoolean(preflight, 2, "visible", "send_preflight");
      const loginCount = requireCount(preflight, 3, "send_preflight");
      const loginVisible = requireBoolean(preflight, 4, "visible", "send_preflight");
      if (loginCount > 0 && loginVisible) {
        throw new QQMailScriptError("email_login_required", "QQ Mail login is required");
      }
      if (composeCount !== 1 || !composeVisible) {
        throw new QQMailScriptError("send_precondition_failed", "QQ Mail compose control is unavailable");
      }

      const prepared = await task.onTab(
        [
          ["click", QQMAIL_SELECTORS.composeButton],
          ["wait", QQMAIL_SELECTORS.recipient],
          ["fill", QQMAIL_SELECTORS.recipient, input.recipient],
          ["press", "Enter"],
          ["wait", QQMAIL_SELECTORS.recipientChip],
          ["fill", QQMAIL_SELECTORS.subject, input.subject],
          ["fill", QQMAIL_SELECTORS.body, input.body],
          ["wait", "500"],
          ["get", "url"],
          ["get", "text", QQMAIL_SELECTORS.recipientChip],
          ["get", "value", QQMAIL_SELECTORS.subject],
          ["get", "text", QQMAIL_SELECTORS.body],
          ["is", "visible", QQMAIL_SELECTORS.composePage],
        ],
        "send_prepare_draft",
      );
      const recipientValue = normalizeVisibleText(
        resultAt(prepared, 9, "send_prepare_draft").text,
      );
      const subjectValue = String(resultAt(prepared, 10, "send_prepare_draft").value ?? "");
      const bodyValue = comparableBody(resultAt(prepared, 11, "send_prepare_draft").text);
      const composePageVisible = requireBoolean(
        prepared,
        12,
        "visible",
        "send_prepare_draft",
      );
      if (!composeLocation(resultAt(prepared, 8, "send_prepare_draft").url) || !composePageVisible) {
        throw new QQMailScriptError("draft_verification_failed", "QQ Mail compose page is unavailable");
      }
      if (
        recipientValue !== input.recipient ||
        subjectValue !== input.subject ||
        bodyValue !== comparableBody(input.body)
      ) {
        throw new QQMailScriptError(
          "draft_verification_failed",
          "QQ Mail draft fields do not match the request",
        );
      }

      const checked = await task.onTab(
        [
          ["get", "url"],
          ["is", "visible", QQMAIL_SELECTORS.composePage],
          ["get", "count", QQMAIL_SELECTORS.sendButton],
          ["get", "text", QQMAIL_SELECTORS.sendButton],
          ["is", "visible", QQMAIL_SELECTORS.sendButton],
          ["is", "enabled", QQMAIL_SELECTORS.sendButton],
        ],
        "send_verify_control",
      );
      const sendLabel = normalizeVisibleText(resultAt(checked, 3, "send_verify_control").text);
      if (
        !composeLocation(resultAt(checked, 0, "send_verify_control").url) ||
        !requireBoolean(checked, 1, "visible", "send_verify_control") ||
        requireCount(checked, 2, "send_verify_control") !== 1 ||
        !["Send", "发送"].includes(sendLabel) ||
        !requireBoolean(checked, 4, "visible", "send_verify_control") ||
        !requireBoolean(checked, 5, "enabled", "send_verify_control")
      ) {
        throw new QQMailScriptError(
          "send_control_not_ready",
          "QQ Mail send control was not uniquely verified",
        );
      }

      let sent;
      try {
        sendClickAttempted = true;
        sent = await task.onTab(
          [
            ["click", QQMAIL_SELECTORS.sendButton],
            ["wait", "1500"],
            ["get", "url"],
            ["is", "visible", QQMAIL_SELECTORS.composePage],
            ["is", "visible", QQMAIL_SELECTORS.sentPage],
          ],
          "send_dispatch",
        );
        const confirmed =
          sentLocation(resultAt(sent, 2, "send_dispatch").url) &&
          requireBoolean(sent, 3, "visible", "send_dispatch") === false &&
          requireBoolean(sent, 4, "visible", "send_dispatch") === true;
        if (!confirmed) {
          throw new QQMailScriptError("send_outcome_unknown", "QQ Mail send was not confirmed");
        }
      } catch {
        throw new QQMailScriptError(
          "send_outcome_unknown",
          "QQ Mail send may have been dispatched and will not be retried",
        );
      }

      return {
        schema_version: 1,
        status: "sent",
        provider: "qq_mail",
        recipient_digest: `sha256:${createHash("sha256")
          .update(input.recipient, "utf8")
          .digest("hex")}`,
      };
    });
  } catch (error) {
    if (sendClickAttempted) {
      throw new QQMailScriptError(
        "send_outcome_unknown",
        "QQ Mail send may have been dispatched and will not be retried",
      );
    }
    throw normalizedSendError(error);
  }
}
