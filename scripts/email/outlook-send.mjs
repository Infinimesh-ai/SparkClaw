#!/usr/bin/env node

import {
  OUTLOOK_ORIGINS,
  OutlookCliError,
  PROBE_EXPRESSION,
  classifyProbeEvidence,
  hasExactKeys,
  parseProbeEvidence,
  recipientDigest,
  validateInvocationId,
  withOwnedOutlookTab,
} from "./outlook-browser.mjs";

const INPUT_KEYS = [
  "account",
  "invocation_id",
  "message",
  "operation",
  "provider",
  "schema_version",
];
const MESSAGE_REQUIRED_KEYS = ["body", "recipient"];
const MESSAGE_OPTIONAL_KEYS = ["subject"];
const BODY_KEYS = ["content", "format"];
const MAX_SUBJECT_LENGTH = 998;
const MAX_BODY_LENGTH = 200_000;

const NEW_MAIL_SELECTOR = [
  'button[aria-label="New mail"]',
  'button[aria-label="New message"]',
  'button[aria-label="\u65b0\u90ae\u4ef6"]',
  'button[aria-label="\u65b0\u5efa\u90ae\u4ef6"]',
].join(", ");
const RECIPIENT_SELECTOR = [
  'input[aria-label="To"]',
  'input[aria-label="Recipients"]',
  'input[aria-label="\u6536\u4ef6\u4eba"]',
  'input[placeholder="To"]',
  'input[placeholder="\u6536\u4ef6\u4eba"]',
  '[role="combobox"][aria-label="To"]',
  '[role="combobox"][aria-label="\u6536\u4ef6\u4eba"]',
].join(", ");
const SUBJECT_SELECTOR = [
  'input[aria-label="Add a subject"]',
  'input[aria-label="Subject"]',
  'input[aria-label="\u6dfb\u52a0\u4e3b\u9898"]',
  'input[aria-label="\u4e3b\u9898"]',
  'input[placeholder="Add a subject"]',
  'input[placeholder="\u6dfb\u52a0\u4e3b\u9898"]',
].join(", ");
const BODY_SELECTOR = [
  '[contenteditable="true"][aria-label="Message body"]',
  '[contenteditable="true"][aria-label="Email body"]',
  '[contenteditable="true"][aria-label="\u90ae\u4ef6\u6b63\u6587"]',
  '[contenteditable="true"][aria-label="\u6d88\u606f\u6b63\u6587"]',
].join(", ");
export const OUTLOOK_SEND_SELECTOR = [
  'button[aria-label="Send"]',
  'button[aria-label="\u53d1\u9001"]',
  'button[title="Send"]',
  'button[title="\u53d1\u9001"]',
].join(", ");

export const OUTLOOK_SEND_VERIFICATION_EXPRESSION = String.raw`(async () => {
  const isVisible = (element) => {
    if (!element || !element.isConnected) return false;
    const style = window.getComputedStyle(element);
    const rect = element.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0 &&
      style.display !== "none" && style.visibility !== "hidden" &&
      Number.parseFloat(style.opacity || "1") > 0;
  };
  const anyVisible = (selector) =>
    Array.from(document.querySelectorAll(selector)).some(isVisible);
  const bodySelector = ${JSON.stringify(BODY_SELECTOR)};
  const sendSelector = ${JSON.stringify(OUTLOOK_SEND_SELECTOR)};
  const statusPattern = /^(Message sent|Email sent|\u90ae\u4ef6\u5df2\u53d1\u9001|\u5df2\u53d1\u9001\u90ae\u4ef6)(?:[.!\s]|$)/i;
  const collect = () => {
    const statusVisible = Array.from(document.querySelectorAll('[role="status"], [role="alert"]'))
      .filter(isVisible)
      .some((element) => statusPattern.test((element.textContent || "").trim()));
    const composeOpen = anyVisible(bodySelector) || anyVisible(sendSelector);
    return {
      contract_version: 1,
      url: window.location.href,
      sent_evidence: statusVisible || !composeOpen,
      compose_open: composeOpen,
    };
  };
  const deadline = Date.now() + 5000;
  let result = collect();
  while (!result.sent_evidence && Date.now() < deadline) {
    await new Promise((resolve) => setTimeout(resolve, 100));
    result = collect();
  }
  return result;
})()`;

export async function sendOutlook(rawInput, runtime = {}) {
  let sendClickAttempted = false;
  try {
    const input = validateInput(rawInput);
    const timeoutMs = runtime.timeoutMs ?? 10_000;
    return await withOwnedOutlookTab({
      invocationId: input.invocation_id,
      operation: "send",
      timeoutMs,
      runtime,
    }, async (tab) => {
      const evidence = parseProbeEvidence(await tab.inspect(PROBE_EXPRESSION));
      classifyProbeEvidence(evidence);
      await composeAndVerify(tab, input.message, timeoutMs);

      sendClickAttempted = true;
      try {
        await tab.act(["click", OUTLOOK_SEND_SELECTOR]);
      } catch {
        throw new OutlookCliError("send_outcome_unknown");
      }

      let verification;
      try {
        verification = parseSendVerification(
          await tab.act([
            "eval",
            "-b",
            Buffer.from(OUTLOOK_SEND_VERIFICATION_EXPRESSION, "utf8").toString("base64"),
          ]),
        );
      } catch {
        throw new OutlookCliError("send_outcome_unknown");
      }
      if (!verification.sent_evidence) {
        throw new OutlookCliError("send_outcome_unknown");
      }
      return {
        schema_version: 1,
        status: "sent",
        provider: "outlook",
        recipient_digest: recipientDigest(input.message.recipient),
      };
    });
  } catch (error) {
    if (sendClickAttempted) {
      throw new OutlookCliError("send_outcome_unknown");
    }
    throw error;
  }
}

async function composeAndVerify(tab, message, timeoutMs) {
  const uiTimeout = String(Math.min(timeoutMs, 10_000));
  try {
    await tab.act(["click", NEW_MAIL_SELECTOR]);
    await tab.act(["wait", RECIPIENT_SELECTOR, "--timeout", uiTimeout]);
    await tab.act(["fill", RECIPIENT_SELECTOR, message.recipient]);
    const recipient = await tab.act(["get", "value", RECIPIENT_SELECTOR]);
    if (recipient.value !== message.recipient) {
      throw new OutlookCliError("field_verification_failed");
    }
    await tab.act(["focus", RECIPIENT_SELECTOR]);
    await tab.act(["press", "Enter"]);

    if (message.subject !== undefined) {
      await tab.act(["wait", SUBJECT_SELECTOR, "--timeout", uiTimeout]);
      await tab.act(["fill", SUBJECT_SELECTOR, message.subject]);
      const subject = await tab.act(["get", "value", SUBJECT_SELECTOR]);
      if (subject.value !== message.subject) {
        throw new OutlookCliError("field_verification_failed");
      }
    }

    await tab.act(["wait", BODY_SELECTOR, "--timeout", uiTimeout]);
    await tab.act(["fill", BODY_SELECTOR, message.body.content]);
    const body = await tab.act(["get", "text", BODY_SELECTOR]);
    if (normalizeNewlines(body.text) !== normalizeNewlines(message.body.content)) {
      throw new OutlookCliError("field_verification_failed");
    }

    await tab.act(["wait", OUTLOOK_SEND_SELECTOR, "--timeout", uiTimeout]);
    const send = await tab.act(["is", "enabled", OUTLOOK_SEND_SELECTOR]);
    if (send.enabled !== true) throw new OutlookCliError("send_unavailable");
  } catch (error) {
    if (error instanceof OutlookCliError) throw error;
    throw new OutlookCliError("send_preparation_failed");
  }
}

function validateInput(input) {
  if (!hasExactKeys(input, INPUT_KEYS)) throw new OutlookCliError("invalid_request");
  if (
    input.schema_version !== 1 || input.operation !== "send" ||
    input.provider !== "outlook" || input.account !== "default"
  ) {
    throw new OutlookCliError("invalid_request");
  }
  validateInvocationId(input.invocation_id);
  if (!hasRequiredAndOptionalKeys(input.message, MESSAGE_REQUIRED_KEYS, MESSAGE_OPTIONAL_KEYS)) {
    throw new OutlookCliError("invalid_request");
  }
  if (!hasExactKeys(input.message.body, BODY_KEYS)) {
    throw new OutlookCliError("invalid_request");
  }

  const recipient = input.message.recipient;
  if (!isSingleEmailAddress(recipient)) throw new OutlookCliError("invalid_request");
  if (
    input.message.subject !== undefined &&
    (typeof input.message.subject !== "string" ||
      /[\r\n]/.test(input.message.subject) ||
      Array.from(input.message.subject).length > MAX_SUBJECT_LENGTH)
  ) {
    throw new OutlookCliError("invalid_request");
  }
  if (
    input.message.body.format !== "text" ||
    typeof input.message.body.content !== "string" ||
    input.message.body.content.trim() === "" ||
    Buffer.byteLength(input.message.body.content, "utf8") > MAX_BODY_LENGTH ||
    input.message.body.content.includes("\0")
  ) {
    throw new OutlookCliError("invalid_request");
  }
  return input;
}

function isSingleEmailAddress(value) {
  if (
    typeof value !== "string" || value.length > 320 || value !== value.trim() ||
    /[\s<>]/.test(value)
  ) {
    return false;
  }
  const separator = value.indexOf("@");
  return separator > 0 && separator === value.lastIndexOf("@") &&
    separator < value.length - 1 && value.slice(separator + 1).includes(".");
}

function hasRequiredAndOptionalKeys(value, required, optional) {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return false;
  const keys = Object.keys(value);
  return required.every((key) => keys.includes(key)) &&
    keys.every((key) => required.includes(key) || optional.includes(key));
}

function parseSendVerification(evalData) {
  if (
    evalData === null || typeof evalData !== "object" || Array.isArray(evalData) ||
    evalData.result === null || typeof evalData.result !== "object" ||
    evalData.result.contract_version !== 1 ||
    typeof evalData.result.url !== "string" || evalData.result.url !== evalData.origin ||
    typeof evalData.result.sent_evidence !== "boolean" ||
    typeof evalData.result.compose_open !== "boolean"
  ) {
    throw new OutlookCliError("browser_output_invalid");
  }
  let url;
  try {
    url = new URL(evalData.result.url);
  } catch {
    throw new OutlookCliError("browser_output_invalid");
  }
  if (url.protocol !== "https:" || !OUTLOOK_ORIGINS.has(url.origin)) {
    throw new OutlookCliError("outlook_origin_not_allowed");
  }
  return evalData.result;
}

function normalizeNewlines(value) {
  return typeof value === "string" ? value.replace(/\r\n?/g, "\n") : null;
}
