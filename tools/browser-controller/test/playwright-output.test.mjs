import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { classifyProcessExit } from "../src/cli-runtime.mjs";
import {
  NO_OPEN_TABS_TEXT,
  collectSnapshotRefs,
  parseTabsMarkdown,
  renderTabLine,
} from "../src/playwright-output.mjs";

const testDir = path.dirname(fileURLToPath(import.meta.url));
const packageRoot = path.resolve(testDir, "..");
const golden = JSON.parse(fs.readFileSync(path.join(testDir, "fixtures", "playwright-golden.json"), "utf8"));

const mcpPayload = (key) => JSON.parse(golden.mcp[key].content[0].text);
const fixtureTab = (index, current) => ({
  index,
  current,
  title: "Playwright Extension Adapter Live Fixture",
  url: golden.recorded.fixture_url,
  crashed: false,
});
const blankTab = (index, current) => ({ index, current, title: "", url: "about:blank", crashed: false });

test("golden fixture was recorded from the pinned Playwright packages", () => {
  const version = (name) => JSON.parse(fs.readFileSync(path.join(packageRoot, "node_modules", name, "package.json"), "utf8")).version;
  assert.equal(golden.recorded.playwright_mcp, version("@playwright/mcp"));
  assert.equal(golden.recorded.playwright_cli, version("@playwright/cli"));
  assert.equal(golden.recorded.playwright_core, version("playwright-core"));
  const manifest = JSON.parse(fs.readFileSync(path.join(packageRoot, "package.json"), "utf8")).dependencies;
  assert.equal(golden.recorded.playwright_mcp, manifest["@playwright/mcp"]);
  assert.equal(golden.recorded.playwright_cli, manifest["@playwright/cli"]);
});

test("MCP tab lists parse from the recorded browser_tabs markdown", () => {
  assert.deepEqual(parseTabsMarkdown(mcpPayload("tabs_list_initial").result), [blankTab(0, true)]);
  assert.deepEqual(parseTabsMarkdown(mcpPayload("tabs_list_one").result), [fixtureTab(0, true)]);
  assert.deepEqual(parseTabsMarkdown(mcpPayload("tabs_new").result), [fixtureTab(0, false), blankTab(1, true)]);
  assert.deepEqual(
    parseTabsMarkdown(mcpPayload("tabs_new_url").result),
    [fixtureTab(0, false), blankTab(1, false), fixtureTab(2, true)],
  );
  assert.deepEqual(
    parseTabsMarkdown(mcpPayload("tabs_select").result),
    [fixtureTab(0, true), blankTab(1, false), fixtureTab(2, false)],
  );
  assert.deepEqual(parseTabsMarkdown(mcpPayload("tabs_close_index").result), [fixtureTab(0, true), blankTab(1, false)]);
  assert.deepEqual(parseTabsMarkdown(mcpPayload("tabs_close_current").result), [blankTab(0, true)]);
  assert.equal(mcpPayload("tabs_close_last").result, NO_OPEN_TABS_TEXT);
  assert.deepEqual(parseTabsMarkdown(mcpPayload("tabs_close_last").result), []);
});

test("CLI tab lists render the same markdown as MCP in raw and json modes", () => {
  assert.deepEqual(parseTabsMarkdown(golden.cli.tab_list_raw.stdout), [blankTab(0, true)]);
  assert.deepEqual(parseTabsMarkdown(JSON.parse(golden.cli.tab_list_json.stdout).result), [blankTab(0, true)]);
  assert.deepEqual(parseTabsMarkdown(golden.cli.tab_new_raw.stdout), [blankTab(0, false), blankTab(1, true)]);
  assert.deepEqual(parseTabsMarkdown(golden.cli.tab_close_raw.stdout), [blankTab(0, true), blankTab(1, false)]);
  assert.equal(golden.cli.tab_close_last_raw.stdout.trim(), NO_OPEN_TABS_TEXT);
  assert.deepEqual(parseTabsMarkdown(golden.cli.tab_close_last_raw.stdout), []);
  const selected = parseTabsMarkdown(JSON.parse(golden.cli.tab_select_json.stdout).result);
  assert.equal(selected.length, 3);
  assert.equal(selected[2].title, "Second tab");
  assert.equal(selected[2].url, "data:text/html,<title>Second tab</title>second");
  // tab-new with a URL appends a snapshot link after the list; the parser must
  // reject that rather than misread it as a tab.
  assert.equal(parseTabsMarkdown(golden.cli.tab_new_url_raw.stdout), undefined);
  // The default (non-raw) mode wraps the list in a section header.
  assert.equal(parseTabsMarkdown(golden.cli.tab_list_default.stdout), undefined);
  assert.equal(golden.cli.tab_list_default.stdout.startsWith("### Result\n"), true);
});

test("tab line rendering round-trips every recorded tab line", () => {
  for (const key of ["tabs_list_one", "tabs_new_url", "tabs_select"]) {
    const markdown = mcpPayload(key).result;
    assert.equal(parseTabsMarkdown(markdown).map(renderTabLine).join("\n"), markdown);
  }
  assert.equal(parseTabsMarkdown(""), undefined);
  assert.equal(parseTabsMarkdown("- 1: (current) [x](https://x.invalid/)"), undefined);
  assert.equal(parseTabsMarkdown(undefined), undefined);
  assert.deepEqual(
    parseTabsMarkdown("- 0: (current) [Crashed](https://x.invalid/) [crashed]"),
    [{ index: 0, current: true, title: "Crashed", url: "https://x.invalid/", crashed: true }],
  );
});

test("snapshot refs collect from the recorded browser_snapshot JSON tree", () => {
  const full = mcpPayload("snapshot");
  assert.deepEqual(Object.keys(full), ["snapshot"], "json snapshot responses carry only the snapshot array");
  assert.deepEqual([...collectSnapshotRefs(full.snapshot)], ["e2", "e3", "e4", "e5", "e6", "e7", "e8", "e9"]);
  const named = new Map();
  const walk = (node) => {
    if (Array.isArray(node)) node.forEach(walk);
    else if (node && typeof node === "object") {
      if (node.ref) named.set(node.ref, `${node.role}:${node.name ?? ""}`);
      walk(node.children);
    }
  };
  walk(full.snapshot);
  assert.equal(named.get("e5"), "textbox:Name");
  assert.equal(named.get("e7"), "combobox:Country");
  assert.equal(named.get("e8"), "button:Advance");
  assert.deepEqual([...collectSnapshotRefs(mcpPayload("snapshot_depth1").snapshot)], ["e2", "e3", "e4", "e6", "e8", "e9"]);
  assert.deepEqual(mcpPayload("snapshot_about_blank"), { snapshot: [] });
  assert.deepEqual([...collectSnapshotRefs([])], []);
});

test("recorded JSON responses never carry a page markdown block", () => {
  for (const key of ["navigate", "snapshot", "snapshot_depth1", "evaluate", "screenshot", "wait_for", "tabs_list_three"]) {
    assert.equal(Object.hasOwn(mcpPayload(key), "page"), false, key);
  }
  assert.deepEqual(mcpPayload("navigate"), {});
  assert.equal(golden.mcp.snapshot_text_mode.content[0].text.includes("### Page\n- Page URL: "), true);
  assert.deepEqual(JSON.parse(mcpPayload("evaluate").result), {
    url: golden.recorded.fixture_url,
    title: "Playwright Extension Adapter Live Fixture",
    ready_state: "complete",
  });
  assert.equal(golden.mcp.screenshot.content[1].mimeType, "image/png");
});

test("recorded tool errors use the isError envelope", () => {
  assert.equal(golden.mcp.error_unknown_ref.isError, true);
  assert.deepEqual(mcpPayload("error_unknown_ref"), {
    isError: true,
    error: "Error: Ref e999 not found in the current page snapshot. Try capturing new snapshot.",
  });
  assert.equal(golden.mcp.type_focus_without_focus.isError, true);
  assert.equal(JSON.parse(golden.cli.tab_close_missing_json.stdout).error, "Error: Tab 99 not found");
  assert.equal(golden.cli.tab_close_missing_raw.stdout, "### Error\nError: Tab 99 not found\n");
  assert.equal(golden.cli.tab_close_missing_raw.exit_code, 1);
  assert.deepEqual(JSON.parse(golden.cli.close_json.stdout), { session: "sparkclaw-golden", status: "closed" });
  assert.deepEqual(JSON.parse(golden.cli.close_not_open_json.stdout), { session: "sparkclaw-golden", status: "not-open" });
  const opened = JSON.parse(golden.cli.open_json.stdout);
  assert.deepEqual(Object.keys(opened).sort(), ["pid", "result", "session"]);
});

test("CLI exit classification matches recorded and pinned Playwright messages", () => {
  const classify = (run) => classifyProcessExit(run.stdout, run.stderr).reason;
  assert.equal(classify(golden.cli.tab_list_not_open_raw), "process_exit_page_closed");
  assert.equal(classify(golden.cli.tab_list_not_open_json), "process_exit_page_closed");
  assert.equal(classify(golden.cli.unknown_option_raw), "process_exit_invalid_arguments");
  assert.equal(classify(golden.cli.too_many_arguments_raw), "process_exit_invalid_arguments");
  assert.equal(classify(golden.cli.tab_close_missing_raw), "process_exit");
  assert.equal(classify(golden.cli.click_unknown_ref_json), "process_exit");

  // Messages that cannot be provoked deterministically are pinned to the
  // playwright-core bundle that ships them.
  const bundle = fs.readFileSync(path.join(packageRoot, "node_modules", "playwright-core", "lib", "coreBundle.js"), "utf8");
  for (const [literal, sample, reason] of [
    [
      "Target page, context or browser has been closed",
      "Error: Target page, context or browser has been closed",
      "process_exit_page_closed",
    ],
    [
      "Execution context was destroyed",
      "Error: Execution context was destroyed, most likely because of a navigation",
      "process_exit_context_destroyed",
    ],
    ["ms exceeded.", "TimeoutError: Timeout 10000ms exceeded.", "process_exit_action_timeout"],
  ]) {
    assert.equal(bundle.includes(literal), true, literal);
    assert.equal(classifyProcessExit("", sample).reason, reason, literal);
  }
});
