import { createHash } from "node:crypto";

export const GMAIL_REGISTRATION_URL = "https://mail.google.com/mail/u/0/";
export const GMAIL_ORIGIN = "https://mail.google.com";
export const GOOGLE_ACCOUNTS_ORIGIN = "https://accounts.google.com";

const ALLOWED_HTTPS_ORIGINS = new Set([GMAIL_ORIGIN, GOOGLE_ACCOUNTS_ORIGIN]);
const LOGIN_SELECTORS = Object.freeze({
  accountControl: '[aria-label^="Google Account:"]',
  composeControl: '[role="button"][gh="cm"]',
  mainMenu: '[aria-label="Main menu"]',
  accountChoice: "[data-identifier]",
  useAnotherAccount: '[jsname="rwl3qc"]',
  identifierInput: "input#identifierId",
  identifierNext: "#identifierNext",
});

export class GmailCliError extends Error {
  constructor(code) {
    super(code);
    this.name = "GmailCliError";
    this.code = code;
  }
}

export function requireExactObject(value, requiredKeys, optionalKeys, errorCode) {
  if (value === null || Array.isArray(value) || typeof value !== "object") {
    throw new GmailCliError(errorCode);
  }
  const required = new Set(requiredKeys);
  const allowed = new Set([...requiredKeys, ...optionalKeys]);
  const actualKeys = Object.keys(value);
  if (
    actualKeys.some((key) => !allowed.has(key)) ||
    [...required].some((key) => !Object.hasOwn(value, key))
  ) {
    throw new GmailCliError(errorCode);
  }
  return value;
}

export function requireInvocationId(value, errorCode) {
  if (typeof value !== "string" || !/^[\x21-\x7e]{1,128}$/.test(value)) {
    throw new GmailCliError(errorCode);
  }
  return value;
}

export function recipientDigest(value) {
  return "sha256:" + createHash("sha256").update(value, "utf8").digest("hex");
}

export async function inspectGmailLogin(tab, { includeAccountHint = false } = {}) {
  const initialURL = await tab.getUrl();
  const initialOrigin = requireAllowedOrigin(initialURL, "email_provider_origin_invalid");
  const evidence = {};
  for (const [name, selector] of Object.entries(LOGIN_SELECTORS)) {
    evidence[name] = await tab.getCount(selector, initialOrigin);
  }

  let accountHint;
  if (includeAccountHint && initialOrigin === GMAIL_ORIGIN && evidenceIsReady(evidence)) {
    const label = await tab.getAttribute(
      LOGIN_SELECTORS.accountControl,
      "aria-label",
      initialOrigin,
    );
    accountHint = extractAccountHint(label);
  }

  const finalURL = await tab.getUrl(initialOrigin);
  const finalOrigin = requireAllowedOrigin(finalURL, "email_provider_origin_invalid");
  if (finalOrigin !== initialOrigin) {
    throw new GmailCliError("email_login_evidence_conflict");
  }

  const result = classifyEvidence(initialOrigin, evidence);
  if (result !== "ready") {
    throw new GmailCliError(result);
  }
  return { accountHint };
}

export function requireAllowedOrigin(rawURL, errorCode) {
  let parsed;
  try {
    parsed = new URL(rawURL);
  } catch {
    throw new GmailCliError(errorCode);
  }
  if (!ALLOWED_HTTPS_ORIGINS.has(parsed.origin)) {
    throw new GmailCliError(errorCode);
  }
  return parsed.origin;
}

function evidenceIsReady(evidence) {
  return (
    evidence.accountControl === 1 &&
    evidence.composeControl === 1 &&
    evidence.mainMenu === 1 &&
    negativeEvidenceCount(evidence) === 0
  );
}

function classifyEvidence(origin, evidence) {
  const positivePresent =
    evidence.accountControl > 0 ||
    evidence.composeControl > 0 ||
    evidence.mainMenu > 0;
  const negativePresent = negativeEvidenceCount(evidence) > 0;

  if (positivePresent && negativePresent) {
    return "email_login_evidence_conflict";
  }
  if (origin === GMAIL_ORIGIN) {
    if (negativePresent) {
      return "email_login_evidence_conflict";
    }
    return evidenceIsReady(evidence) ? "ready" : "email_page_contract_changed";
  }
  if (origin === GOOGLE_ACCOUNTS_ORIGIN) {
    if (positivePresent) {
      return "email_login_evidence_conflict";
    }
    const chooserReady = evidence.accountChoice >= 1 && evidence.useAnotherAccount === 1;
    const identifierReady = evidence.identifierInput === 1 && evidence.identifierNext === 1;
    const chooserEvidenceCount = evidence.accountChoice + evidence.useAnotherAccount;
    const identifierEvidenceCount = evidence.identifierInput + evidence.identifierNext;
    if (
      (chooserReady && identifierEvidenceCount === 0) ||
      (identifierReady && chooserEvidenceCount === 0)
    ) {
      return "email_login_required";
    }
    return "email_page_contract_changed";
  }
  return "email_provider_origin_invalid";
}

function negativeEvidenceCount(evidence) {
  return (
    evidence.accountChoice +
    evidence.useAnotherAccount +
    evidence.identifierInput +
    evidence.identifierNext
  );
}

function extractAccountHint(label) {
  if (!label.startsWith("Google Account:")) {
    return undefined;
  }
  const accountMarker = label.match(/\(([^()\r\n]{1,128})\)\s*$/);
  if (accountMarker === null) {
    return undefined;
  }
  const candidate = accountMarker[1];
  const separator = candidate.indexOf("@");
  if (
    separator <= 0 ||
    separator !== candidate.lastIndexOf("@") ||
    separator === candidate.length - 1
  ) {
    return undefined;
  }

  const localPart = candidate.slice(0, separator);
  const rawDomain = candidate.slice(separator + 1);
  const localPattern = /^[\p{L}\p{N}\p{M}.!#$%&'*+/=?^_\x60{|}~-]+$/u;
  const domainPattern =
    /^[A-Z0-9](?:[A-Z0-9-]{0,61}[A-Z0-9])?(?:\.[A-Z0-9](?:[A-Z0-9-]{0,61}[A-Z0-9])?)+$/i;
  if (
    !localPattern.test(localPart) ||
    localPart.startsWith(".") ||
    localPart.endsWith(".") ||
    localPart.includes("..") ||
    !domainPattern.test(rawDomain)
  ) {
    return undefined;
  }

  const prefix = Array.from(localPart).slice(0, 2).join("");
  const hint = prefix + "***@" + rawDomain.toLowerCase();
  return Array.from(hint).length <= 64 ? hint : undefined;
}
