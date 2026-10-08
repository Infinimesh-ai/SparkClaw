#!/usr/bin/env node
import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { DesktopAuth } from "../apps/desktop/src/main/desktop-auth.mjs";
import { SecureCredentialStore } from "../apps/desktop/src/main/secure-credential-store.mjs";
import { ClientStore } from "../apps/desktop/src/main/client-store.mjs";
import { ExecutionClient } from "../apps/desktop/src/main/execution-client.mjs";
import { loadISCPProfile } from "../apps/desktop/src/main/iscp-profile.mjs";
import { assertNoSymlinkPath, assertPrivateDirectory } from "./lib/private-workbench.mjs";

const repository = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const hash = (value) => crypto.createHash("sha256").update(value).digest("hex");
const pause = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const MOCK_ANSWER = "I can answer this directly from the current conversation.";

export function parseSmokeArguments(argv) {
  const options = { text: "Hello.", reconnect: false, timeoutMS: 90000 };
  const names = new Map([["--profile", "profilePath"], ["--user-data", "userData"], ["--text", "text"], ["--timeout-ms", "timeoutMS"], ["--client-store-directory", "clientStoreDirectory"]]);
  const seen = new Set();
  for (let index = 0; index < argv.length; index++) {
    const flag = argv[index];
    if (seen.has(flag)) throw new Error("Duplicate smoke option");
    seen.add(flag);
    if (flag === "--reconnect") { options.reconnect = true; continue; }
    const name = names.get(flag), value = argv[++index];
    if (!name || !value || value.startsWith("--")) throw new Error("Usage: iscp-desktop-smoke --profile <private profile> --user-data <private directory> [--text <short input>] [--reconnect] [--client-store-directory <private normal workbench directory>] [--timeout-ms <1000–300000>]");
    options[name] = name === "timeoutMS" ? Number(value) : value;
  }
  validateOptions(options);
  return options;
}

export function assertSuccessfulSmokeResult(content, state) {
  if (!["completed", "delivered"].includes(state)) throw new Error("ISCP execution did not complete successfully");
  if (typeof content !== "string" || content.startsWith("Blocked:")) throw new Error("ISCP smoke received a blocked or missing assistant result");
  if (content !== MOCK_ANSWER) throw new Error("ISCP smoke did not receive the expected successful mock answer");
}

function validateOptions(options) {
  if (!path.isAbsolute(options.profilePath || "") || !path.isAbsolute(options.userData || "") ||
      path.resolve(options.userData) === repository || path.resolve(options.userData).startsWith(repository + path.sep)) throw new Error("Smoke requires absolute private profile and dedicated user-data outside the repository");
  if (options.clientStoreDirectory !== undefined && (!path.isAbsolute(options.clientStoreDirectory) || path.resolve(options.clientStoreDirectory) === repository || path.resolve(options.clientStoreDirectory).startsWith(repository + path.sep))) throw new Error("ClientStore directory must be an absolute private directory outside the repository");
  if (!Number.isInteger(options.timeoutMS) || options.timeoutMS < 1000 || options.timeoutMS > 300000 ||
      typeof options.text !== "string" || !options.text.trim() || Buffer.byteLength(options.text) > 4096) throw new Error("Smoke input or deadline is invalid");
}

// Headless acceptance intentionally uses a private AES test adapter. It exercises
// the same encrypted record/schema and normal auth lifecycle, while making no
// claim about native Electron Keychain/Secret Service acceptance.
export async function headlessTestVault(directory) {
  await assertPrivateDirectory(directory);
  const filename = path.join(directory, "headless-vault-test-key.bin");
  await assertNoSymlinkPath(filename);
  let key;
  try {
    const info = await fs.lstat(filename);
    if (!info.isFile() || info.isSymbolicLink() || info.uid !== process.getuid() || (info.mode & 0o777) !== 0o600 || info.size !== 32) throw new Error("Headless vault test key must be a private regular file");
    key = await fs.readFile(filename);
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
    key = crypto.randomBytes(32);
    const file = await fs.open(filename, "wx", 0o600);
    try { await file.writeFile(key); await file.sync(); } finally { await file.close(); }
  }
  const safeStorage = {
    isEncryptionAvailable: () => true,
    getSelectedStorageBackend: () => "headless_local_lab_test_adapter",
    encryptString: (text) => {
      const iv = crypto.randomBytes(12), cipher = crypto.createCipheriv("aes-256-gcm", key, iv);
      const content = Buffer.concat([cipher.update(text, "utf8"), cipher.final()]);
      return Buffer.concat([iv, cipher.getAuthTag(), content]);
    },
    decryptString: (bytes) => {
      const decipher = crypto.createDecipheriv("aes-256-gcm", key, bytes.subarray(0, 12));
      decipher.setAuthTag(bytes.subarray(12, 28));
      return Buffer.concat([decipher.update(bytes.subarray(28)), decipher.final()]).toString("utf8");
    },
  };
  return new SecureCredentialStore({ directory: path.join(directory, "authentication"), safeStorage, platform: "linux" });
}

export async function runDesktopSmoke(options) {
  validateOptions(options);
  const profile = await loadISCPProfile(options.profilePath);
  if (profile.descriptor.relayProfile !== "local-lab") throw new Error("Headless smoke requires an explicit local-lab Relay profile");
  await assertNoSymlinkPath(options.userData);
  await fs.mkdir(options.userData, { recursive: true, mode: 0o700 });
  await assertPrivateDirectory(options.userData);
  // A sequential native/headless acceptance may open the same normal business
  // Store. Authentication stays in each process's own private vault directory.
  // Neither IDs nor database/control records are copied or rewritten.
  const clientStoreDirectory = options.clientStoreDirectory || path.join(options.userData, "workbench");
  await assertNoSymlinkPath(clientStoreDirectory);
  await fs.mkdir(clientStoreDirectory, { recursive: true, mode: 0o700 });
  await assertPrivateDirectory(clientStoreDirectory);
  const scope = { deployment_id: profile.descriptor.deploymentID, owner_id: profile.descriptor.ownerID, client_id: profile.descriptor.clientID };
  const vault = await headlessTestVault(options.userData);
  const deadline = Date.now() + options.timeoutMS;
  const calls = [];
  const emit = (event, fields = {}) => options.onEvent?.({ event, ...fields });
  let store, auth, execution, dropAdmission = options.reconnect, directHTTPCalls = 0, generations = 0;
  const deadlineTimer = setTimeout(() => { execution?.close(); auth?.suspend(); }, options.timeoutMS);
  const open = async () => {
    store = new ClientStore(clientStoreDirectory);
    auth = new DesktopAuth({ installationID: store.installationID, vault, descriptorPath: path.join(options.userData, "backend.json"),
      iscpProfilePath: options.profilePath, allowLocalISCPTest: true, qualification: false,
      fetcher: () => { directHTTPCalls++; throw new Error("Direct Gateway HTTP is forbidden in ISCP smoke"); },
      onChange: (status) => emit("connection", { state: status.state, transport_stage: status.transport_stage }),
    });
    const status = await auth.initialize();
    if (status.state !== "connected") throw new Error(`ISCP connection failed: ${status.state}`);
    generations++;
    const authorizedFetch = auth.authorizedExecutionFetch.bind(auth);
    auth.authorizedExecutionFetch = async (url, init = {}) => {
      const route = new URL(url).pathname, method = init.method || "GET";
      calls.push({ route, method });
      const response = await authorizedFetch(url, init);
      if (dropAdmission && route === "/api/v1/executions" && method === "POST" && response.ok) {
        dropAdmission = false;
        await response.body?.cancel();
        auth.suspend();
        emit("admission_response_dropped");
        throw new Error("Isolated smoke dropped the accepted admission response");
      }
      return response;
    };
    execution = new ExecutionClient({ auth, store, getIdentity: () => auth.connection?.identityVerified ? scope : null });
  };
  const close = () => { execution?.close(); auth?.close(); store?.close(); execution = auth = store = undefined; };
  try {
    await open();
    for (const route of ["/api/config", "/api/owner", "/readyz"]) {
      const response = await auth.authorizedFetch(`${profile.descriptor.origin}${route}`);
      if (!response.ok) throw new Error("Required workbench presentation is unavailable");
      const body = await response.json();
      if (!body || typeof body !== "object") throw new Error("Required workbench presentation is invalid");
    }
    emit("presentation_verified");
    const conversation = store.create(scope, "ISCP local Relay smoke");
    const task = store.enqueue(scope, conversation.id, options.text);
    const original = store.request(scope, task.request_id);
    try { await execution.submit(scope, task.request_id); }
    catch (error) { if (!options.reconnect || dropAdmission) throw error; }
    if (options.reconnect) {
      close(); emit("helper_stopped", { request_id: task.request_id });
      await open(); emit("helper_restarted", { request_id: task.request_id });
      if (store.request(scope, task.request_id).context_json !== original.context_json) throw new Error("Original immutable request changed during restart");
    }
    while (Date.now() < deadline && store.request(scope, task.request_id).status !== "delivered") {
      await execution.reconcile(scope, task.request_id);
      const status = store.request(scope, task.request_id).status;
      if (["failed", "canceled", "delivery_expired", "unknown"].includes(status)) throw new Error(`ISCP execution ended without delivery: ${status}`);
      if (status !== "delivered") await pause(250);
    }
    if (store.request(scope, task.request_id).status !== "delivered") throw new Error("ISCP execution deadline exceeded");
    // Require a real original-ID lookup even if the first submit completed before
    // its response arrived. This is a read; it can never admit a second request.
    const lookup = await auth.authorizedExecutionFetch(`${profile.descriptor.origin}/api/v1/executions/${task.request_id}`, { headers: { "X-SparkClaw-Installation": store.installationID } });
    if (!lookup.ok) throw new Error("Delivered original request lookup failed");
    const lookedUp = await lookup.json();
    if (lookedUp.request_id !== task.request_id || lookedUp.input_digest !== original.input_digest) throw new Error("Recovered request identity or digest changed");
    const submissions = calls.filter((call) => call.method === "POST" && call.route === "/api/v1/executions").length;
    if (submissions !== 1 || directHTTPCalls !== 0) throw new Error("ISCP transport or duplicate-admission invariant failed");
    const content = store.read(scope, conversation.id), result = content.messages.findLast((message) => message.role === "assistant");
    const receipt = store.receipt(scope, task.request_id);
    if (!result || !receipt?.acknowledged) throw new Error("Result was not durably stored and acknowledged");
    assertSuccessfulSmokeResult(result.content, lookedUp.state);
    const evidence = { schema_version: 1, relay_profile: "local-lab", vault_backend: "headless_aes_test_adapter", normal_client_store: true,
      ...scope, installation_id: store.installationID, conversation_id: conversation.id, request_id: task.request_id, input_digest: original.input_digest,
      result_digest: receipt.digest, result_content_sha256: hash(result.content), result_content_bytes: Buffer.byteLength(result.content),
      state: "delivered", successful_mock_answer: true, helper_generations: generations, submit_count: submissions,
      lookup_count: calls.filter((call) => call.method === "GET" && call.route === `/api/v1/executions/${task.request_id}`).length,
      ack_count: calls.filter((call) => call.route.endsWith("/ack")).length, direct_gateway_http_calls: directHTTPCalls };
    emit("iscp_desktop_smoke_complete", evidence);
    return evidence;
  } finally { clearTimeout(deadlineTimer); close(); }
}

if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) {
  try {
    const options = parseSmokeArguments(process.argv.slice(2));
    await runDesktopSmoke({ ...options, onEvent: (event) => process.stdout.write(`${JSON.stringify(event)}\n`) });
  } catch (error) {
    process.stderr.write(`${JSON.stringify({ event: "iscp_desktop_smoke_failed", error: error instanceof Error ? error.message : "Smoke failed" })}\n`);
    process.exitCode = 1;
  }
}
