import { invalidRequest } from "./errors.mjs";
import { parseID, requireExactObject } from "./protocol.mjs";
import { PLAYWRIGHT_REF_PATTERN } from "./playwright-output.mjs";
import { clientContractError } from "./mcp-errors.mjs";

export const MAX_PAGE_TEXT_CHARS = 120_000;

export const MAX_INPUT_TEXT_BYTES = 24 << 10;

export function exactArgs(args, required, optional = []) {
  requireExactObject(args, required, optional);
}

export function requiredPageID(value) {
  return parseID(value, "page_id");
}

export function optionalPageID(value) {
  return value === undefined ? "" : requiredPageID(value);
}

export function requiredRef(value) {
  if (typeof value !== "string" || !PLAYWRIGHT_REF_PATTERN.test(value)) {
    throw invalidRequest("ref is invalid");
  }
  return value;
}

export function requiredText(value) {
  if (typeof value !== "string" || Buffer.byteLength(value, "utf8") > MAX_INPUT_TEXT_BYTES) {
    throw invalidRequest("text is invalid");
  }
  return value;
}

export function optionalText(value, field) {
  if (value === undefined) return "";
  if (typeof value !== "string" || value.length === 0 || Buffer.byteLength(value, "utf8") > MAX_INPUT_TEXT_BYTES) {
    throw invalidRequest(`${field} is invalid`);
  }
  return value;
}

export function optionalBoolean(value, field) {
  if (value === undefined) return undefined;
  if (typeof value !== "boolean") throw invalidRequest(`${field} is invalid`);
  return value;
}

export function optionalInteger(value, field, minimum, maximum) {
  if (value === undefined) return undefined;
  if (!Number.isSafeInteger(value) || value < minimum || value > maximum) {
    throw invalidRequest(`${field} is invalid`);
  }
  return value;
}

export function optionalMaximum(value) {
  return optionalInteger(value, "max_chars", 1, MAX_PAGE_TEXT_CHARS) ?? MAX_PAGE_TEXT_CHARS;
}

export function optionalEnum(value, field, allowed) {
  if (value === undefined) return "";
  if (typeof value !== "string" || !allowed.includes(value)) throw invalidRequest(`${field} is invalid`);
  return value;
}

export function optionalStringArray(value, field, allowed) {
  if (value === undefined) return undefined;
  if (!Array.isArray(value) || value.length > allowed.length || value.some((item) => !allowed.includes(item))) {
    throw invalidRequest(`${field} is invalid`);
  }
  return [...new Set(value)];
}

export function requiredURL(value) {
  return optionalURL(value, { required: true });
}

export function optionalURL(value, { required = false, allowBlank = false } = {}) {
  if (value === undefined && !required) return "";
  if (typeof value !== "string" || value.length === 0 || Buffer.byteLength(value, "utf8") > 4096) {
    throw invalidRequest("url is invalid");
  }
  if (allowBlank && value === "about:blank") return value;
  let parsed;
  try {
    parsed = new URL(value);
  } catch {
    throw invalidRequest("url is invalid");
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") throw invalidRequest("url is invalid");
  return parsed.href;
}

export function selectValues(args) {
  if (args.value !== undefined && args.values !== undefined) throw invalidRequest("select values are invalid");
  const values = args.values ?? (args.value === undefined ? [] : [args.value]);
  if (!Array.isArray(values) || values.length === 0 || values.length > 32) {
    throw invalidRequest("select values are invalid");
  }
  return values.map(requiredText);
}

export function normalizePageInfo(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw clientContractError();
  const url = observedURL(value.url);
  if (typeof value.title !== "string" || typeof value.ready_state !== "string") throw clientContractError();
  return { url, title: value.title, ready_state: value.ready_state };
}

export function observedURL(value) {
  if (value === "about:blank") return value;
  return requiredURL(value);
}

export function normalizePageRead(value, maximum) {
  const info = normalizePageInfo(value);
  if (typeof value.text !== "string" || typeof value.html !== "string" || typeof value.lang !== "string" || !Number.isSafeInteger(value.scroll_height) || value.scroll_height < 0) {
    throw clientContractError();
  }
  const originalLength = [...value.text].length;
  return {
    ...info,
    lang: value.lang,
    text: [...value.text].slice(0, maximum).join(""),
    html: [...value.html].slice(0, maximum).join(""),
    text_length: originalLength,
    text_truncated: originalLength > maximum,
    scroll_height: value.scroll_height,
  };
}

export function parseJSONResult(value) {
  if (typeof value !== "string") throw clientContractError();
  try {
    return JSON.parse(value);
  } catch {
    throw clientContractError();
  }
}
