import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { parseSmokeArguments, runDesktopSmoke, headlessTestVault, assertSuccessfulSmokeResult } from "../iscp-desktop-smoke.mjs";

test("smoke requires explicit private paths and bounded text/deadline, without secret or executable options", () => {
  const options = parseSmokeArguments(["--profile", "/private/lab/profile.json", "--user-data", "/private/lab/user-data", "--reconnect"]);
  assert.equal(options.reconnect, true); assert.equal(options.timeoutMS, 90000); assert.equal(options.text, "Hello.");
  const shared = parseSmokeArguments(["--profile", "/private/lab/profile.json", "--user-data", "/private/lab/headless", "--client-store-directory", "/private/lab/userdata/workbench"]);
  assert.equal(shared.clientStoreDirectory, "/private/lab/userdata/workbench");
  assert.throws(() => parseSmokeArguments(["--profile", "/private/lab/profile.json", "--user-data", "/private/lab/headless", "--client-store-directory", "relative"]), /ClientStore/u);
  for (const args of [[], ["--profile", "relative", "--user-data", "/private/lab/user-data"], ["--profile", "/private/lab/profile.json", "--user-data", "/private/lab/user-data", "--timeout-ms", "0"], ["--token", "secret"], ["--helper-executable", "/anywhere"], ["--profile", "/one", "--profile", "/two"]]) assert.throws(() => parseSmokeArguments(args));
});

test("smoke requires the successful mock answer and rejects blocked, failed or unrelated results even after durable delivery", () => {
  const answer = "I can answer this directly from the current conversation.";
  assert.doesNotThrow(() => assertSuccessfulSmokeResult(answer, "completed"));
  assert.doesNotThrow(() => assertSuccessfulSmokeResult(answer, "delivered"));
  assert.throws(() => assertSuccessfulSmokeResult("Blocked: semantic_coverage_low", "delivered"), /blocked/u);
  assert.throws(() => assertSuccessfulSmokeResult(undefined, "delivered"), /missing/u);
  assert.throws(() => assertSuccessfulSmokeResult(answer, "failed"), /did not complete/u);
  assert.throws(() => assertSuccessfulSmokeResult("Acknowledged transport, no successful answer", "delivered"), /expected successful/u);
});

test("headless test adapter encrypts and reopens the ordinary credential vault using a private per-lab key", async (t) => {
  const directory = await fs.mkdtemp(path.join(await fs.realpath(os.tmpdir()), "sparkclaw-headless-vault-")); await fs.chmod(directory, 0o700);
  t.after(() => fs.rm(directory, { force: true, recursive: true }));
  const vault = await headlessTestVault(directory), token = crypto.randomBytes(32).toString("base64url");
  await vault.save({ authorization: `Bearer ${token}`, binding: "test", clientID: "client", ownerID: "owner", deploymentID: "deployment" });
  const bytes = await fs.readFile(vault.filename); assert.ok(!bytes.includes(Buffer.from(token)));
  assert.equal((await (await headlessTestVault(directory)).load()).authorization, `Bearer ${token}`);
  const key = await fs.stat(path.join(directory, "headless-vault-test-key.bin")); assert.equal(key.mode & 0o777, 0o600);
});

test("headless acceptance refuses a production Relay profile before creating user data or starting helper", async (t) => {
  const directory = await fs.mkdtemp(path.join(await fs.realpath(os.tmpdir()), "sparkclaw-headless-profile-")); await fs.chmod(directory, 0o700);
  t.after(() => fs.rm(directory, { force: true, recursive: true }));
  const helperPath = path.join(directory, "helper.json"), profilePath = path.join(directory, "profile.json"), userData = path.join(directory, "desktop");
  const binding = { deployment_id: "deployment", owner_id: "owner", client_id: "client" };
  await fs.writeFile(helperPath, JSON.stringify({ schema_version: 1, mode: "local-test", role: "initiator", binding }), { mode: 0o600 });
  await fs.writeFile(profilePath, JSON.stringify({ schema_version: 1, transport: "iscp", test_mode: true, helper_config: helperPath,
    backend: { schema_version: 3, transport: "iscp", origin: "https://iscp.invalid", ...binding, domain_id: "domain", initiator_device_id: "desktop", responder_device_id: "gateway", responder_key_thumbprint: "a".repeat(64), relay_url: "https://relay.example.test", test_mode: true } }), { mode: 0o600 });
  await assert.rejects(runDesktopSmoke({ profilePath, userData, text: "test", reconnect: false, timeoutMS: 1000 }), /local-lab/u);
  await assert.rejects(fs.stat(userData), { code: "ENOENT" });
});
