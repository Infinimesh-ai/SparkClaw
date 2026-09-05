import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { PlaywrightMCPClientFactory } from "../src/mcp-client.mjs";
import { BACKGROUND_CLICK_FUNCTION } from "../src/dom-actions.mjs";

const testDir = path.dirname(fileURLToPath(import.meta.url));
const fixture = path.join(testDir, "fixtures", "fake-mcp.mjs");
const stderrFixture = path.join(testDir, "fixtures", "fake-mcp-stderr.mjs");

test("MCP client passes the extension token only through a scrubbed environment", async () => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-browser-mcp-"));
  const logPath = path.join(dir, "fake-mcp.log");
  const outputRoot = path.join(dir, "mcp-output");
  const staleOutput = path.join(outputRoot, "session-000000000000000000000000");
  await fs.mkdir(staleOutput, { recursive: true });
  await fs.writeFile(path.join(staleOutput, "stale.log"), "stale");
  const token = "test-extension-token-private";
  const factory = new PlaywrightMCPClientFactory({
    entryPoint: fixture,
    cwd: dir,
    executablePath: "/opt/sparkclaw/chromium",
    userDataDir: "/home/owner/browser-profile",
    connectTimeoutMS: 1000,
    outputRoot,
    extraEnv: {
      FAKE_MCP_LOG: logPath,
      PLAYWRIGHT_MCP_HEADLESS: "true",
      PLAYWRIGHT_MCP_EXTENSION_TOKEN: "stale-token",
    },
  });
  await factory.prepare();
  assert.deepEqual(await fs.readdir(outputRoot), []);

  const client = await factory.open({ token, sessionID: "session-test" });
  await client.createTaskPage();
  await client.close();

  const records = (await fs.readFile(logPath, "utf8")).trim().split("\n").map((line) => JSON.parse(line));
  const started = records[0];
  assert.equal(started.extension_token_present, true);
  assert.equal(started.inherited_forbidden_env_present, false);
  assert.equal(started.argv.includes("--extension"), true);
  assert.deepEqual(started.argv.slice(started.argv.indexOf("--browser"), started.argv.indexOf("--browser") + 2), ["--browser", "chromium"]);
  assert.equal(started.argv.includes("--headless"), false);
  assert.equal(started.argv.includes(token), false);
  assert.equal(started.debug_namespace, "pw:mcp:relay");
  assert.deepEqual(started.argv.slice(started.argv.indexOf("--image-responses"), started.argv.indexOf("--image-responses") + 2), ["--image-responses", "allow"]);
  const outputDirIndex = started.argv.indexOf("--output-dir");
  assert.notEqual(outputDirIndex, -1);
  assert.equal(started.argv[outputDirIndex + 1].startsWith(`${outputRoot}${path.sep}session-`), true);
  assert.deepEqual(started.argv.slice(started.argv.indexOf("--output-max-size"), started.argv.indexOf("--output-max-size") + 2), ["--output-max-size", String(8 << 20)]);
  assert.equal(started.argv.includes("--executable-path"), true);
  assert.equal(started.argv.includes("--user-data-dir"), true);
  assert.deepEqual(
    records.filter((record) => record.event === "tool").map((record) => [record.name, record.arguments.action]),
    [["browser_tabs", "list"], ["browser_tabs", "new"], ["browser_tabs", "list"], ["browser_tabs", "close"]],
  );
  assert.equal((await fs.readFile(logPath, "utf8")).includes(token), false);
  assert.deepEqual(await fs.readdir(outputRoot), []);
});

test("MCP client exposes only task pages and binds actions to fresh snapshot refs", async () => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-browser-mcp-"));
  const logPath = path.join(dir, "fake-mcp.log");
  const outputRoot = path.join(dir, "mcp-output");
  const factory = new PlaywrightMCPClientFactory({
    entryPoint: fixture,
    cwd: dir,
    connectTimeoutMS: 1000,
    outputRoot,
    extraEnv: { FAKE_MCP_LOG: logPath },
  });
  const client = await factory.open({ token: "test-extension-token-private", sessionID: "session-operations" });
  await client.createTaskPage();

  const listed = await client.execute("tabs.list", {});
  assert.deepEqual(listed.pages, [{
    page_id: "page_1",
    url: "about:blank",
    title: "",
    selected: true,
    crashed: false,
  }]);

  const navigated = await client.execute("page.navigate", { url: "https://example.test/path" });
  assert.equal(navigated.page.page_id, "page_1");
  assert.equal(navigated.page.url, "https://example.test/path");

  const snapshot = await client.execute("page.snapshot", {});
  assert.deepEqual(snapshot.refs, ["e7", "e8"]);
  await client.execute("page.click", { ref: "e7" });
  await assert.rejects(
    client.execute("page.click", { ref: "e7" }),
    (error) => error.code === "browser_page_stale",
  );
  await assert.rejects(
    client.execute("page.click", { ref: "css=.submit" }),
    (error) => error.code === "invalid_request",
  );

  const read = await client.execute("page.read", { max_chars: 8 });
  assert.equal(read.page.text, "Rendered");
  assert.equal(read.page.text_truncated, true);
  const screenshot = await client.execute("page.screenshot", { type: "png" });
  assert.equal(screenshot.screenshot.mime_type, "image/png");
  assert.equal(screenshot.screenshot.data_base64, "c2NyZWVuc2hvdA==");
  await client.execute("page.type", { text: "query", focused: true });
  await client.execute("tabs.handoff", { page_id: "page_1" });

  await client.close();
  const records = (await fs.readFile(logPath, "utf8")).trim().split("\n").map((line) => JSON.parse(line));
  const calls = records.filter((record) => record.event === "tool");
  assert.equal(calls.some((call) => call.name === "browser_evaluate" &&
    call.arguments.target === "e7" && call.arguments.function === BACKGROUND_CLICK_FUNCTION), true);
  assert.equal(calls.some((call) => call.name === "browser_type" && call.arguments.target === ":focus"), true);
  assert.equal(calls.some((call) => JSON.stringify(call.arguments).includes("css=.submit")), false);
  const handoff = calls.find((call) => call.name === "browser_evaluate" &&
    call.arguments.function === "() => \"sparkclaw-browser-bridge-handoff-v1\"");
  assert.ok(handoff, "explicit handoff marker was not evaluated");
  assert.equal(calls.some((call) => call.name === "browser_tabs" && call.arguments.action === "select"), true);
  assert.deepEqual(await fs.readdir(outputRoot), []);
});

test("MCP cleanup closes only the unique SparkClaw Bridge connection page", async () => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-browser-mcp-"));
  const logPath = path.join(dir, "fake-mcp.log");
  const factory = new PlaywrightMCPClientFactory({
    entryPoint: fixture,
    cwd: dir,
    connectTimeoutMS: 1000,
    outputRoot: path.join(dir, "mcp-output"),
    extraEnv: {
      FAKE_MCP_LOG: logPath,
      FAKE_MCP_INITIAL_URL: "chrome-extension://mmlmfjhmonkocbjadbfplnigmagldckm/connect.html?redacted",
    },
  });
  const client = await factory.open({ token: "test-extension-token-private", sessionID: "session-bridge-close" });
  await client.createTaskPage();
  assert.equal(client.bridgeConnectionPage, true);
  await client.close();

  const records = (await fs.readFile(logPath, "utf8")).trim().split("\n").map((line) => JSON.parse(line));
  const closes = records.filter((record) => record.event === "tool" &&
    record.name === "browser_tabs" && record.arguments.action === "close");
  assert.deepEqual(closes.map((record) => record.arguments.index), [1, 0]);
});

test("MCP client classifies a split Bridge rejection marker without retaining stderr", async () => {
  const canary = "private-stderr-canary";
  const factory = stderrFactory({
    FAKE_MCP_STDERR_MODE: "rejected",
    FAKE_MCP_STDERR_CANARY: canary,
  });

  await assert.rejects(
    factory.open({ token: "wrong-token", sessionID: "session-rejected" }),
    (error) => {
      assert.equal(error.code, "browser_extension_rejected");
      assert.equal(error.status, 401);
      assert.equal(error.retryable, false);
      assert.equal(String(error.stack).includes(canary), false);
      assert.equal(String(error.cause ?? "").includes(canary), false);
      return true;
    },
  );
});

test("MCP client does not classify a near-match stderr marker as credential rejection", async () => {
  const factory = stderrFactory({ FAKE_MCP_STDERR_MODE: "near-match" });

  await assert.rejects(
    factory.open({ token: "test-token", sessionID: "session-unavailable" }),
    (error) => error.code === "browser_extension_unavailable" && error.retryable,
  );
});

function stderrFactory(extraEnv) {
  const outputRoot = path.join(os.tmpdir(), `sparkclaw-browser-mcp-stderr-${crypto.randomUUID()}`, "mcp-output");
  return new PlaywrightMCPClientFactory({
    entryPoint: stderrFixture,
    connectTimeoutMS: 1000,
    outputRoot,
    extraEnv,
  });
}
