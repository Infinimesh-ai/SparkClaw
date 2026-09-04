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
const MAX_ATTACHMENTS = 50;

export const QQMAIL_UNREAD_SELECTORS = Object.freeze({
  inboxPage: ".mail-list-page",
  loginPage: ".login-page",
  firstUnread: ":nth-match(.mail-list-page-item:has(.mail-subject.mail-unread), 1)",
  listSender: ":nth-match(.mail-list-page-item:has(.mail-subject.mail-unread), 1) .mail-sender",
  listSubject: ":nth-match(.mail-list-page-item:has(.mail-subject.mail-unread), 1) .mail-subject",
  listDigest: ":nth-match(.mail-list-page-item:has(.mail-subject.mail-unread), 1) .mail-digest",
  listTime: ":nth-match(.mail-list-page-item:has(.mail-subject.mail-unread), 1) .mail-time",
  reader: ".mail-list-page-reader",
  subject: ".mail-detail-subject .mail-subject-text",
  senderName: ".mail-detail-basic .basic-body-item:first-child .cmp-account-nick",
  senderAddress: ".mail-detail-basic .basic-body-item:first-child .cmp-account-email",
  receivedAt: ".mail-detail-basic .time-text",
  body: ".mail-reader-body .reader-body-children",
  attachmentCard: ".mail-detail-attaches > .mail-detail-attach-card",
});

function strictInput(input) {
  if (!input || typeof input !== "object" || Array.isArray(input)) {
    throw new QQMailScriptError("invalid_input", "stdin must contain one JSON object");
  }
  const expectedKeys = ["account", "invocation_id", "operation", "provider", "schema_version"];
  const actualKeys = Object.keys(input).sort();
  if (
    actualKeys.length !== expectedKeys.length ||
    actualKeys.some((key, index) => key !== expectedKeys[index]) ||
    input.schema_version !== 1 ||
    input.operation !== "read_first_unread" ||
    input.provider !== "qq_mail" ||
    input.account !== "default" ||
    typeof input.invocation_id !== "string" ||
    !/^[A-Za-z0-9._:-]{1,128}$/u.test(input.invocation_id)
  ) {
    throw new QQMailScriptError(
      "invalid_input",
      "stdin does not match the QQ Mail read-first-unread schema",
    );
  }
}

async function readJSONInput(stream) {
  let raw = "";
  for await (const chunk of stream) {
    raw += chunk;
    if (Buffer.byteLength(raw, "utf8") > INPUT_LIMIT_BYTES) {
      throw new QQMailScriptError("input_too_large", "stdin exceeds the QQ Mail read input limit");
    }
  }
  if (!raw.trim()) {
    throw new QQMailScriptError("missing_input", "provide the QQ Mail read request on stdin");
  }
  try {
    return JSON.parse(raw);
  } catch {
    throw new QQMailScriptError("invalid_json", "stdin is not valid JSON");
  }
}

function requireCount(results, index, phase) {
  const count = resultAt(results, index, phase).count;
  if (!Number.isInteger(count) || count < 0) {
    throw new QQMailScriptError(
      "read_browser_output_invalid",
      "agent-browser returned an invalid count",
    );
  }
  return count;
}

function requireBoolean(results, index, field, phase) {
  const value = resultAt(results, index, phase)[field];
  if (typeof value !== "boolean") {
    throw new QQMailScriptError(
      "read_browser_output_invalid",
      "agent-browser returned an invalid control state",
    );
  }
  return value;
}

function inboxLocation(value) {
  const parsed = parseQQMailURL(value, "read_browser_output_invalid");
  return parsed.pathname === "/home/index" && /^#\/list\/1(?:$|[/?])/u.test(parsed.hash);
}

function extractedText(value) {
  return String(value ?? "").replace(/\r\n?/gu, "\n").trim();
}

function senderAddress(value) {
  const normalized = normalizeVisibleText(value);
  const bracketed = normalized.match(/<([^<>]+)>/u);
  return (bracketed?.[1] ?? normalized).trim();
}

function attachmentFieldSelector(index, field) {
  return `${QQMAIL_UNREAD_SELECTORS.attachmentCard}:nth-child(${index + 1} of .mail-detail-attach-card) .${field}`;
}

function attachmentName(base, suffix) {
  const normalizedBase = normalizeVisibleText(base);
  const normalizedSuffix = normalizeVisibleText(suffix);
  if (!normalizedSuffix || normalizedBase.endsWith(normalizedSuffix)) return normalizedBase;
  return `${normalizedBase}${normalizedSuffix}`;
}

function attachmentSize(value) {
  return normalizeVisibleText(value).replace(/^\((.*)\)$/u, "$1");
}

function normalizedReadError(error, readClickAttempted) {
  if (readClickAttempted) {
    return new QQMailScriptError(
      "read_outcome_unknown",
      "the unread message may already have been marked read and will not be retried",
    );
  }
  if (!(error instanceof QQMailScriptError)) {
    return new QQMailScriptError("read_precondition_failed", "QQ Mail read preparation failed");
  }
  if (error.code.endsWith("_timeout")) {
    return new QQMailScriptError("read_timeout", "QQ Mail read preparation timed out");
  }
  const preserved = new Set([
    "agent_browser_unavailable",
    "email_login_required",
    "forbidden_browser_environment",
    "host_cdp_required",
    "input_too_large",
    "invalid_arguments",
    "invalid_input",
    "invalid_json",
    "missing_input",
    "provider_origin_mismatch",
    "read_browser_output_invalid",
    "read_precondition_failed",
    "task_tab_cleanup_failed",
  ]);
  if (preserved.has(error.code)) return error;
  return new QQMailScriptError("read_precondition_failed", "QQ Mail read preparation failed");
}

export async function readFirstUnreadQQMail(rawInput, runtime = {}) {
  strictInput(rawInput);
  let readClickAttempted = false;
  try {
    return await withQQMailTaskTab("read", runtime, async (task) => {
      const preflight = await task.onTab(
        [
          ["get", "url"],
          ["get", "count", QQMAIL_UNREAD_SELECTORS.inboxPage],
          ["is", "visible", QQMAIL_UNREAD_SELECTORS.inboxPage],
          ["get", "count", QQMAIL_UNREAD_SELECTORS.loginPage],
          ["is", "visible", QQMAIL_UNREAD_SELECTORS.loginPage],
          ["get", "count", QQMAIL_UNREAD_SELECTORS.firstUnread],
          ["is", "visible", QQMAIL_UNREAD_SELECTORS.firstUnread],
        ],
        "read_preflight",
      );
      const inboxCount = requireCount(preflight, 1, "read_preflight");
      const inboxVisible = requireBoolean(preflight, 2, "visible", "read_preflight");
      const loginCount = requireCount(preflight, 3, "read_preflight");
      const loginVisible = requireBoolean(preflight, 4, "visible", "read_preflight");
      const unreadCount = requireCount(preflight, 5, "read_preflight");
      const unreadVisible = requireBoolean(preflight, 6, "visible", "read_preflight");

      if (loginCount > 0 && loginVisible) {
        throw new QQMailScriptError("email_login_required", "QQ Mail login is required");
      }
      if (
        !inboxLocation(resultAt(preflight, 0, "read_preflight").url) ||
        inboxCount !== 1 ||
        !inboxVisible
      ) {
        throw new QQMailScriptError("read_precondition_failed", "QQ Mail inbox is unavailable");
      }
      if (unreadCount === 0 && !unreadVisible) {
        return { schema_version: 1, status: "no_unread", provider: "qq_mail" };
      }
      if (unreadCount !== 1 || !unreadVisible) {
        throw new QQMailScriptError(
          "read_precondition_failed",
          "the first unread QQ Mail message was not uniquely available",
        );
      }

      const list = await task.onTab(
        [
          ["get", "text", QQMAIL_UNREAD_SELECTORS.listSender],
          ["get", "text", QQMAIL_UNREAD_SELECTORS.listSubject],
          ["get", "text", QQMAIL_UNREAD_SELECTORS.listDigest],
          ["get", "text", QQMAIL_UNREAD_SELECTORS.listTime],
        ],
        "read_list_metadata",
      );
      const listSender = normalizeVisibleText(resultAt(list, 0, "read_list_metadata").text);
      const listSubject = normalizeVisibleText(resultAt(list, 1, "read_list_metadata").text);
      const listPreview = normalizeVisibleText(resultAt(list, 2, "read_list_metadata").text);
      const listTime = normalizeVisibleText(resultAt(list, 3, "read_list_metadata").text);

      readClickAttempted = true;
      const opened = await task.onTab(
        [
          ["click", QQMAIL_UNREAD_SELECTORS.firstUnread],
          ["wait", QQMAIL_UNREAD_SELECTORS.reader],
          ["get", "url"],
          ["is", "visible", QQMAIL_UNREAD_SELECTORS.reader],
        ],
        "read_open_unread",
      );
      parseQQMailURL(resultAt(opened, 2, "read_open_unread").url, "read_browser_output_invalid");
      if (!requireBoolean(opened, 3, "visible", "read_open_unread")) {
        throw new QQMailScriptError("read_browser_output_invalid", "QQ Mail reader is unavailable");
      }

      const detail = await task.onTab(
        [
          ["get", "text", QQMAIL_UNREAD_SELECTORS.subject],
          ["get", "text", QQMAIL_UNREAD_SELECTORS.senderName],
          ["get", "text", QQMAIL_UNREAD_SELECTORS.senderAddress],
          ["get", "text", QQMAIL_UNREAD_SELECTORS.receivedAt],
          ["get", "text", QQMAIL_UNREAD_SELECTORS.body],
          ["get", "count", QQMAIL_UNREAD_SELECTORS.attachmentCard],
        ],
        "read_message_detail",
      );
      const subject = normalizeVisibleText(resultAt(detail, 0, "read_message_detail").text);
      const name = normalizeVisibleText(resultAt(detail, 1, "read_message_detail").text);
      const address = senderAddress(resultAt(detail, 2, "read_message_detail").text);
      const attachmentCount = requireCount(detail, 5, "read_message_detail");
      if (!(name || listSender) || !(subject || listSubject)) {
        throw new QQMailScriptError(
          "read_browser_output_invalid",
          "QQ Mail message identity is incomplete",
        );
      }

      const attachmentReadCount = Math.min(attachmentCount, MAX_ATTACHMENTS);
      const attachments = [];
      if (attachmentReadCount > 0) {
        const commands = [];
        for (let index = 0; index < attachmentReadCount; index += 1) {
          commands.push(
            ["get", "text", attachmentFieldSelector(index, "attach-name")],
            ["get", "text", attachmentFieldSelector(index, "attach-suffix")],
            ["get", "text", attachmentFieldSelector(index, "attach-size")],
          );
        }
        const attachmentResults = await task.onTab(commands, "read_attachment_metadata");
        for (let index = 0; index < attachmentReadCount; index += 1) {
          const offset = index * 3;
          const suffix = normalizeVisibleText(
            resultAt(attachmentResults, offset + 1, "read_attachment_metadata").text,
          );
          attachments.push({
            name: attachmentName(
              resultAt(attachmentResults, offset, "read_attachment_metadata").text,
              suffix,
            ),
            extension: suffix,
            size: attachmentSize(
              resultAt(attachmentResults, offset + 2, "read_attachment_metadata").text,
            ),
          });
        }
      }

      return {
        schema_version: 1,
        status: attachmentCount > MAX_ATTACHMENTS ? "partial" : "read",
        provider: "qq_mail",
        was_unread: true,
        marked_read: true,
        message: {
          sender: { name: name || listSender, address },
          subject: subject || listSubject,
          received_at: normalizeVisibleText(resultAt(detail, 3, "read_message_detail").text) || listTime,
          preview: listPreview,
          list_time: listTime,
          body: extractedText(resultAt(detail, 4, "read_message_detail").text),
          list_matches_detail:
            (!listSubject || !subject || listSubject === subject) &&
            (!listSender || !name || listSender === name),
          attachment_count: attachmentCount,
          attachments_complete: attachmentCount <= MAX_ATTACHMENTS,
          attachments_truncated: attachmentCount > MAX_ATTACHMENTS,
          attachments,
        },
      };
    });
  } catch (error) {
    throw normalizedReadError(error, readClickAttempted);
  }
}

async function main() {
  if (process.argv.length !== 2) {
    throw new QQMailScriptError("invalid_arguments", "the read script accepts stdin only");
  }
  const input = await readJSONInput(process.stdin);
  const result = await readFirstUnreadQQMail(input);
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
