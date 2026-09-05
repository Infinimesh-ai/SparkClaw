import { parseTabsMarkdown } from "./playwright-output.mjs";
import { BRIDGE_EXTENSION_ID } from "./bridge-native-protocol.mjs";
import { clientContractError } from "./mcp-errors.mjs";

export const BRIDGE_CONNECT_URL_PREFIX = `chrome-extension://${BRIDGE_EXTENSION_ID}/connect.html?`;

// browser_tabs renders its tab list as markdown in `result` even under
// `_meta.json`; playwright-output.mjs owns that format.
export function tabsFromPayload(payload) {
  const tabs = parseTabsMarkdown(payload.result);
  if (tabs === undefined) throw clientContractError();
  return tabs;
}

export function tabFingerprint(tab) {
  return `${tab.title}\u0000${tab.url}\u0000${tab.crashed ? "1" : "0"}`;
}

export function isBridgeConnectionPage(tab) {
  return typeof tab?.url === "string" && tab.url.startsWith(BRIDGE_CONNECT_URL_PREFIX);
}

export function sameFingerprintList(left, right) {
  return left.length === right.length && left.every((value, index) => value === right[index]);
}

export function findCurrentInsertion(before, after) {
  const current = after.findIndex((tab) => tab.current);
  if (current < 0 || after.length !== before.length + 1) return -1;
  const reduced = after.filter((_, index) => index !== current).map(tabFingerprint);
  return sameFingerprintList(reduced, before.map(tabFingerprint)) ? current : -1;
}

export function sameTabsAfterRemoval(before, after, removed) {
  if (after.length !== before.length - 1) return false;
  const reduced = before.filter((_, index) => index !== removed).map(tabFingerprint);
  return sameFingerprintList(reduced, after.map(tabFingerprint));
}

export function currentOwnedPageID(pages, tabs) {
  for (const page of pages.values()) {
    if (tabs[page.index]?.current) return page.pageID;
  }
  return "";
}
