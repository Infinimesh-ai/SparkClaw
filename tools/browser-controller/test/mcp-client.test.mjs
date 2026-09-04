import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { PlaywrightMCPClientFactory } from "../src/mcp-client.mjs";

const testDir = path.dirname(fileURLToPath(import.meta.url));
const fixture = path.join(testDir, "fixtures", "fake-mcp.mjs");

test("MCP client passes the extension token only through a scrubbed environment", async () => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "sparkclaw-browser-mcp-"));
  const logPath = path.join(dir, "fake-mcp.log");
  const token = "test-extension-token-private";
  const factory = new PlaywrightMCPClientFactory({
    entryPoint: fixture,
    cwd: dir,
    executablePath: "/opt/sparkclaw/chromium",
    userDataDir: "/home/owner/browser-profile",
    connectTimeoutMS: 1000,
    extraEnv: {
      FAKE_MCP_LOG: logPath,
      PLAYWRIGHT_MCP_HEADLESS: "true",
      PLAYWRIGHT_MCP_EXTENSION_TOKEN: "stale-token",
    },
  });

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
  assert.equal(started.argv.includes("--executable-path"), true);
  assert.equal(started.argv.includes("--user-data-dir"), true);
  assert.deepEqual(
    records.filter((record) => record.event === "tool").map((record) => [record.name, record.arguments.action]),
    [["browser_tabs", "new"], ["browser_tabs", "close"]],
  );
  assert.equal((await fs.readFile(logPath, "utf8")).includes(token), false);
});
