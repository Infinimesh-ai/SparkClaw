import crypto from "node:crypto";
import {isManagedSend,validateManagedSend,MANAGED_SEND_SELECTOR} from "../../../scripts/email/lib/managed-send.mjs";
import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { GMAIL_REGISTRATION_URL } from "../../../scripts/email/gmail-browser.mjs";
import { probeGmailLogin } from "../../../scripts/email/gmail-login-probe.mjs";
import { GMAIL_SEND_SELECTORS, sendGmail } from "../../../scripts/email/gmail-send.mjs";
import { isMicrosoftOutlookLanding } from "../../../scripts/email/outlook-browser.mjs";
import { probeOutlookLogin } from "../../../scripts/email/outlook-login-probe.mjs";
import {
  OUTLOOK_SEND_SELECTOR,
  sendOutlook,
} from "../../../scripts/email/outlook-send.mjs";
import { probeQQMailLogin } from "../../../scripts/email/qqmail-login-probe.mjs";
import { QQMAIL_SELECTORS, sendQQMail } from "../../../scripts/email/qqmail-send.mjs";
import { ControllerError, invalidRequest } from "./errors.mjs";
import { READ_PROVIDERS, readQQMail, readOutlook, readGmail, readEmail, discoverEmail, enumerateThread, markEmailRead, collectEmailPage } from "../../../scripts/email/read.mjs";
import { validateCaptureInput } from "../../../scripts/email/lib/read-capture.mjs";

const REPOSITORY_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
const SCRIPT_CODE_PATTERN = /^[a-z0-9_]{1,64}$/u;

const readRegistrations = [
  ...[
    ["qq_mail", "qqmail.read", readQQMail],
    ["outlook", "outlook.read", readOutlook],
    ["gmail", "gmail.read", readGmail],
  ].flatMap(([provider, scriptID, handler]) => ['read','discover','capture','enumerate_thread','mark_read','collect_page'].map(operation => registration({
    provider, operation, scriptID:scriptID.replace(/read$/u,operation),
    handler:operation==='read'?handler:(input,runtime)=>({discover:discoverEmail,capture:readEmail,enumerate_thread:enumerateThread,mark_read:markEmailRead,collect_page:collectEmailPage}[operation])(input,runtime,provider), revision: 1,
    loginURL: READ_PROVIDERS[provider].url,
    downloadOrigins: [...READ_PROVIDERS[provider].origins,
      ...(provider === "gmail" ? ["https://mail-attachment.googleusercontent.com"] : [])],
    origins: [...READ_PROVIDERS[provider].origins,
      ...(provider === "gmail" ? ["https://accounts.google.com"] : []),
      ...(provider === "outlook" ? ["https://login.live.com", "https://login.microsoftonline.com", "https://www.microsoft.com"] : [])],
    ...(provider === "outlook" ? { signedOutURL: outlookSignedOutURL } : {}),
    timeoutMS: operation === 'collect_page' ? 1_800_000 : 180_000,
    sourceFiles: ["scripts/email/read.mjs", "scripts/email/lib/read-capture.mjs", "scripts/email/lib/gmail-list.mjs", "scripts/email/lib/outlook-list.mjs", "scripts/email/lib/qqmail-list.mjs", "scripts/email/lib/qqmail-detail.mjs"],
  }))),
];

const registrations = [
  registration({
    provider: "qq_mail",
    operation: "probe",
    scriptID: "qqmail.login_probe",
    revision: 1,
    loginURL: "https://wx.mail.qq.com/",
    origins: ["https://mail.qq.com", "https://wx.mail.qq.com"],
    timeoutMS: 90_000,
    handler: probeQQMailLogin,
    sourceFiles: [
      "scripts/email/qqmail-login-probe.mjs",
      "scripts/email/lib/qqmail-browser.mjs",
      "scripts/email/lib/qqmail-task.mjs",
    ],
  }),
  registration({
    provider: "qq_mail",
    operation: "send",
    effectSelectors:[MANAGED_SEND_SELECTOR],
    scriptID: "qqmail.send",
    revision: 1,
    loginURL: "https://wx.mail.qq.com/",
    origins: ["https://mail.qq.com", "https://wx.mail.qq.com"],
    timeoutMS: 180_000,
    effectSelector: QQMAIL_SELECTORS.sendButton,
    handler: sendQQMail,
    sourceFiles: [
      "scripts/email/qqmail-send.mjs",
      "scripts/email/lib/qqmail-browser.mjs",
      "scripts/email/lib/qqmail-task.mjs",
    ],
  }),
  registration({
    provider: "outlook",
    operation: "probe",
    scriptID: "outlook.login_probe",
    revision: 1,
    loginURL: "https://outlook.live.com/mail/",
    origins: [
      "https://outlook.live.com",
      "https://outlook.office.com",
      "https://outlook.office365.com",
      "https://login.live.com",
      "https://login.microsoftonline.com",
      "https://www.microsoft.com",
    ],
    signedOutURL: outlookSignedOutURL,
    timeoutMS: 45_000,
    handler: probeOutlookLogin,
    sourceFiles: [
      "scripts/email/outlook-login-probe.mjs",
      "scripts/email/outlook-browser.mjs",
    ],
  }),
  registration({
    provider: "outlook",
    operation: "send",
    effectSelectors:[MANAGED_SEND_SELECTOR],
    scriptID: "outlook.send",
    revision: 1,
    loginURL: "https://outlook.live.com/mail/0/sentitems",
    origins: [
      "https://outlook.live.com",
      "https://outlook.office.com",
      "https://outlook.office365.com",
      "https://login.live.com",
      "https://login.microsoftonline.com",
      "https://www.microsoft.com",
    ],
    signedOutURL: outlookSignedOutURL,
    timeoutMS: 180_000,
    effectSelector: OUTLOOK_SEND_SELECTOR,
    handler: sendOutlook,
    sourceFiles: [
      "scripts/email/outlook-send.mjs",
      "scripts/email/outlook-browser.mjs",
    ],
  }),
  registration({
    provider: "gmail",
    operation: "probe",
    scriptID: "gmail.login_probe",
    revision: 1,
    loginURL: GMAIL_REGISTRATION_URL,
    origins: ["https://mail.google.com", "https://accounts.google.com"],
    timeoutMS: 45_000,
    handler: probeGmailLogin,
    sourceFiles: [
      "scripts/email/gmail-login-probe.mjs",
      "scripts/email/gmail-browser.mjs",
    ],
  }),
  registration({
    provider: "gmail",
    operation: "send",
    effectSelectors:[MANAGED_SEND_SELECTOR],
    scriptID: "gmail.send",
    revision: 1,
    loginURL: GMAIL_REGISTRATION_URL,
    origins: ["https://mail.google.com", "https://accounts.google.com"],
    timeoutMS: 180_000,
    effectSelector: GMAIL_SEND_SELECTORS.send,
    handler: sendGmail,
    sourceFiles: [
      "scripts/email/gmail-send.mjs",
      "scripts/email/gmail-browser.mjs",
    ],
  }),
  ...readRegistrations,
];

function outlookSignedOutURL(rawURL) {
  try {
    return isMicrosoftOutlookLanding(new URL(rawURL));
  } catch {
    return false;
  }
}

// PROVIDER_SCRIPT_CONTRACT_PATH is the Gateway-embedded projection of this
// registry: the script identity, revision, and budget the Go side binds each
// probe, send and intake call to. Regenerate it with `npm run sync:provider-contract`;
// test/provider-script-contract.test.mjs fails when it drifts.
export const PROVIDER_SCRIPT_CONTRACT_PATH = "services/gateway/internal/emailautomation/provider_scripts.json";

export function providerScriptContract(entries = registrations) {
  const scripts = entries
    .map((entry) => ({
      provider: entry.provider,
      operation: entry.operation,
      script_id: entry.scriptID,
      revision: entry.revision,
      timeout_ms: entry.timeoutMS,
    }))
    .sort((a, b) => a.provider.localeCompare(b.provider) || a.operation.localeCompare(b.operation));
  return { schema_version: 1, scripts };
}

export function renderProviderScriptContract(entries = registrations) {
  return `${JSON.stringify(providerScriptContract(entries), null, 2)}\n`;
}

export class ProviderScriptRegistry {
  constructor(entries = registrations) {
    this.entries = new Map(entries.map((entry) => {
      const normalized = registration(entry);
      return [`${normalized.provider}:${normalized.operation}`, { ...normalized }];
    }));
    this.prepared = false;
  }

  async prepare() {
    for (const entry of this.entries.values()) {
      entry.sourceChecksum = await checksumSourceClosure(entry.sourceFiles);
    }
    this.prepared = true;
  }

  resolve({ provider, operation, scriptID, revision }) {
    if (!this.prepared) throw new TypeError("provider script registry is not prepared");
    const entry = this.entries.get(`${provider}:${operation}`);
    if (!entry || entry.scriptID !== scriptID || entry.revision !== revision) {
      throw new ControllerError("browser_script_unavailable", "browser provider script is unavailable", {
        status: 400,
      });
    }
    return entry;
  }

  provider(provider) {
    for (const entry of this.entries.values()) {
      if (entry.provider === provider) return entry;
    }
    throw new ControllerError("browser_script_unavailable", "browser provider script is unavailable", {
      status: 400,
    });
  }
}

export function providerFailureEnvelope(provider, error) {
  const code = typeof error?.code === "string" && SCRIPT_CODE_PATTERN.test(error.code)
    ? error.code
    : "provider_script_failed";
  return {
    schema_version: 1,
    status: "error",
    provider,
    code,
  };
}

function registration(value) {
  return Object.freeze({
    ...value,
    validate: value.validate ?? ((input) => validateScriptInput(value.provider, value.operation, input)),
    origins: Object.freeze([...value.origins]),
    sourceFiles: Object.freeze([...new Set([...value.sourceFiles,...(value.operation==="send"?["scripts/email/lib/managed-send.mjs","scripts/email/lib/managed-send-dom.mjs","scripts/email/lib/send-journal.mjs","scripts/email/read.mjs","scripts/email/lib/read-capture.mjs","scripts/email/lib/gmail-list.mjs","scripts/email/lib/outlook-list.mjs","scripts/email/lib/qqmail-list.mjs","scripts/email/lib/qqmail-detail.mjs"]:[])])]),
  });
}

async function checksumSourceClosure(sourceFiles) {
  const digest = crypto.createHash("sha256");
  for (const relative of [...sourceFiles].sort()) {
    const absolute = path.resolve(REPOSITORY_ROOT, relative);
    const real = await fs.realpath(absolute);
    if (!real.startsWith(`${REPOSITORY_ROOT}${path.sep}`)) {
      throw new TypeError("provider script source escaped the repository");
    }
    const stat = await fs.lstat(real);
    if (!stat.isFile() || stat.isSymbolicLink()) {
      throw new TypeError("provider script source must be a regular repository file");
    }
    digest.update(relative, "utf8");
    digest.update("\0");
    digest.update(await fs.readFile(real));
    digest.update("\0");
  }
  return `sha256:${digest.digest("hex")}`;
}

function validateScriptInput(provider, operation, input) {
  if(operation==="send"&&isManagedSend(input)){validateManagedSend(input,provider);return;}
  if (['read','discover','capture','enumerate_thread','mark_read','collect_page'].includes(operation)) {
    if (input?.operation !== operation) throw invalidRequest();
    validateCaptureInput(input,provider);
    return;
  }
  const common = ["account", "invocation_id", "operation", "provider", "schema_version"];
  requireKeys(input, operation === "send" ? [...common, "message"] : common);
  if (
    input.schema_version !== 1 ||
    input.operation !== operation ||
    input.provider !== provider ||
    input.account !== "default" ||
    typeof input.invocation_id !== "string" ||
    !/^[A-Za-z0-9._:-]{1,128}$/u.test(input.invocation_id)
  ) {
    throw invalidRequest();
  }
  if (operation !== "send") return;

  requireKeys(input.message, ["body", "recipient"], ["subject"]);
  requireKeys(input.message.body, ["content", "format"]);
  const recipient = input.message.recipient;
  if (
    typeof recipient !== "string" ||
    recipient.length > 320 ||
    recipient !== recipient.trim() ||
    /[\s<>\u0000-\u001f\u007f]/u.test(recipient) ||
    !/^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$/u.test(recipient)
  ) {
    throw invalidRequest();
  }
  if (Object.hasOwn(input.message, "subject")) {
    const subject = input.message.subject;
    if (
      typeof subject !== "string" ||
      /[\r\n\u0000]/u.test(subject) ||
      [...subject].length > 998 ||
      Buffer.byteLength(subject, "utf8") > 4_000
    ) {
      throw invalidRequest();
    }
  }
  const body = input.message.body;
  if (
    body.format !== "text" ||
    typeof body.content !== "string" ||
    !body.content.trim() ||
    body.content.includes("\0") ||
    Buffer.byteLength(body.content, "utf8") > 200 * 1024
  ) {
    throw invalidRequest();
  }
}

function requireKeys(value, required, optional = []) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw invalidRequest();
  const allowed = new Set([...required, ...optional]);
  if (
    required.some((key) => !Object.hasOwn(value, key)) ||
    Object.keys(value).some((key) => !allowed.has(key))
  ) {
    throw invalidRequest();
  }
}
