// Parsers for the Playwright tool output that the pinned @playwright/mcp and
// @playwright/cli packages render through their shared Response serializer.
// The tab list is only ever rendered as markdown (even under `_meta.json` or
// `--json` it arrives as the `result` string), so this module is the single
// place that knows that format. test/fixtures/playwright-golden.json pins the
// exact output recorded from the pinned packages; re-record it with
// test/fixtures/record-playwright-golden.mjs after a version bump.

export const NO_OPEN_TABS_TEXT = "No open tabs. Navigate to a URL to create one.";
export const PLAYWRIGHT_REF_PATTERN = /^e[1-9][0-9]*$/u;

const TAB_LINE_PATTERN = /^- ([0-9]+):( \(current\))? \[(.*)\]\((.*)\)( \[crashed\])?$/u;

// Parses one `- <index>:[ (current)] [<title>](<url>)[ [crashed]]` line.
// Returns undefined when the line does not match or its index is out of order.
export function parseTabLine(line, expectedIndex) {
  const match = TAB_LINE_PATTERN.exec(line);
  if (!match || Number(match[1]) !== expectedIndex) return undefined;
  return {
    index: Number(match[1]),
    current: Boolean(match[2]),
    title: match[3],
    url: match[4],
    crashed: Boolean(match[5]),
  };
}

export function renderTabLine(tab) {
  const current = tab.current ? " (current)" : "";
  const crashed = tab.crashed ? " [crashed]" : "";
  return `- ${tab.index}:${current} [${tab.title}](${tab.url})${crashed}`;
}

// Parses a complete tab list. Returns [] for the no-open-tabs sentinel and
// undefined when the text is not a well-formed, contiguous tab list.
export function parseTabsMarkdown(text) {
  if (typeof text !== "string") return undefined;
  if (text.trim() === NO_OPEN_TABS_TEXT) return [];
  const tabs = [];
  for (const line of text.split("\n")) {
    if (!line.trim()) continue;
    const tab = parseTabLine(line, tabs.length);
    if (!tab) return undefined;
    tabs.push(tab);
  }
  return tabs.length === 0 ? undefined : tabs;
}

// Collects every element ref from a `browser_snapshot` JSON payload. The
// payload is an array of ARIA nodes ({ role, name?, ref?, children?, text? })
// whose children are nodes or bare text strings.
export function collectSnapshotRefs(value, refs = new Set()) {
  if (Array.isArray(value)) {
    for (const item of value) collectSnapshotRefs(item, refs);
  } else if (value && typeof value === "object") {
    if (typeof value.ref === "string" && PLAYWRIGHT_REF_PATTERN.test(value.ref)) refs.add(value.ref);
    for (const item of Object.values(value)) collectSnapshotRefs(item, refs);
  }
  return refs;
}

export function comparePlaywrightRefs(left, right) {
  return Number(left.slice(1)) - Number(right.slice(1));
}
