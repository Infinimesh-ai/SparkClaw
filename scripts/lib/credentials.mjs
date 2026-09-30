import crypto from "node:crypto";
import fs from "node:fs/promises";
import http from "node:http";
import path from "node:path";
import { assertPrivateDirectory, assertNoSymlinkPath, readPrivateJSON, writePrivateJSON, canonicalLoopbackOrigin, validateProvisioning, nonempty } from "./private-workbench.mjs";

export function parseCredentialArguments(args) {
  const options = { mode: args.shift(), runtimeDirectory: process.env.SPARKCLAW_LOCAL_WORKBENCH_RUNTIME_DIR || path.resolve("data/runtime"), name: "", revokeID: "" };
  if (!["initial", "recover"].includes(options.mode)) throw new Error("use initial or recover");
  for (let index = 0; index < args.length; index += 1) {
    if (args[index] === "--runtime-dir") options.runtimeDirectory = args[++index] || "";
    else if (args[index] === "--name") options.name = args[++index] || "";
    else if (args[index] === "--revoke-id") options.revokeID = args[++index] || "";
    else throw new Error("unknown credential retrieval option");
  }
  if (!path.isAbsolute(options.runtimeDirectory)) throw new Error("--runtime-dir must be absolute");
  options.name = options.name.trim();
  if (!options.name || [...options.name].length > 80 || /[\x00-\x1f\x7f]/u.test(options.name)) throw new Error("--name must be a device name of 1 to 80 characters");
  if (options.mode === "recover" && !/^[A-Za-z0-9_-]{1,160}$/u.test(options.revokeID)) throw new Error("recover requires a specified --revoke-id");
  if (options.mode === "initial" && options.revokeID) throw new Error("--revoke-id is only available with recover");
  return options;
}

export async function claimCredential(options, { input = process.stdin, output = process.stdout, errorOutput = process.stderr, request = socketRequest, now = () => new Date().toISOString() } = {}) {
  if (!input.isTTY || !output.isTTY || !errorOutput.isTTY) throw new Error("run this command in an interactive terminal; credentials cannot be redirected or collected in deployment logs");
  const runtime = options.runtimeDirectory;
  await assertPrivateDirectory(runtime);
  const descriptor = await readPrivateJSON(path.join(runtime, "local-workbench.json"));
  if (descriptor?.schema_version !== 1 || !nonempty(descriptor.deployment_id)) throw new Error("local workbench descriptor is invalid");
  canonicalLoopbackOrigin(descriptor.origin);
  const management = validateProvisioning(await readPrivateJSON(path.join(runtime, "local-management.json")), descriptor.deployment_id);
  const managementDirectory = path.join(runtime, "management");
  await assertPrivateDirectory(managementDirectory);
  const socketPath = path.join(managementDirectory, "credentials.sock");
  await assertNoSymlinkPath(socketPath);
  const socket = await fs.lstat(socketPath);
  if (!socket.isSocket() || socket.uid !== process.getuid() || (socket.mode & 0o777) !== 0o600) throw new Error("local credential management socket must be owned by the deployment user with mode 0600");
  const suffix = options.mode === "initial" ? "initial" : `recovery-${crypto.createHash("sha256").update(options.revokeID).digest("hex").slice(0, 24)}`;
  const journalPath = path.join(managementDirectory, `${suffix}.json`);
  const unlock = await lockJournal(path.join(managementDirectory, "claim-lock.json"));
  try {
    let journal = await readPrivateJSON(journalPath, { optional: true });
    if (journal && (journal.schema_version !== 1 || journal.deployment_id !== descriptor.deployment_id || journal.mode !== options.mode || journal.revoke_id !== options.revokeID || !/^credential-[A-Za-z0-9-]{36}$/u.test(journal.request_key) || journal.client_id !== issuedClientID(management.owner_id, journal.request_key) || Object.keys(journal).some(key => !["schema_version", "deployment_id", "mode", "name", "revoke_id", "request_key", "client_id", "state", "created_at", "updated_at"].includes(key)) || !["pending_revoke", "pending_issue", "completed"].includes(journal.state))) throw new Error("credential retrieval record does not match this deployment");
    if (journal?.state === "completed") {
      output.write("This retrieval is complete. Use Settings → Devices & credentials on a signed-in client, or credentials:recover for a specified lost device. Old credentials cannot be displayed again.\n");
      return { completed: true, clientID: journal.client_id };
    }
    if (journal && journal.name !== options.name) throw new Error("retry the pending retrieval with its original device name; the request key is unchanged");
    const call = (method, route, body, key) => request({ socketPath, origin: descriptor.origin, token: management.token, method, route, body, key });
    const identity = await call("GET", "/api/workbench/identity");
    if (identity.status !== 200 || identity.body?.deployment_id !== descriptor.deployment_id || identity.body?.owner_id !== management.owner_id || identity.body?.client_id !== management.client_id) throw new Error("local management identity verification failed; check the deployment and its private management files");
    if (!journal) {
      const requestKey = `credential-${crypto.randomUUID()}`;
      journal = { schema_version: 1, deployment_id: descriptor.deployment_id, mode: options.mode, name: options.name, revoke_id: options.revokeID, request_key: requestKey, client_id: issuedClientID(management.owner_id, requestKey), state: options.mode === "recover" ? "pending_revoke" : "pending_issue", created_at: now(), updated_at: now() };
      await writePrivateJSON(journalPath, journal, { replace: false });
    }
    if (journal.state === "pending_revoke") {
      const result = await call("POST", `/api/clients/${encodeURIComponent(options.revokeID)}/revoke`, {});
      if (result.status !== 200 || result.body?.id !== options.revokeID || !result.body?.revoked_at) throw new Error("specified lost device could not be revoked; no replacement credential was requested");
      journal = { ...journal, state: "pending_issue", updated_at: now() };
      await writePrivateJSON(journalPath, journal);
    }
    let result;
    try {
      result = await call("POST", "/api/clients", { client_name: journal.name }, journal.request_key);
    } catch {
      throw new Error("issuance outcome is unknown; rerun the same command and device name to retry the saved request key");
    }
    if (result.status === 409) {
      if (!["CLIENT_CREDENTIAL_UNRECOVERABLE", "CLIENT_REVOKED"].includes(result.body?.code) || result.body?.client_id !== journal.client_id) throw new Error("issuance conflicts with persisted state; inspect the device list before revoking any device");
      throw new Error(`credential cannot be recovered from this request (service restart, expiry or revocation). Revoke device ${journal.client_id} and issue a replacement: npm run credentials:recover -- --revoke-id ${journal.client_id} --name '<device name>'`);
    }
    if (![200, 201].includes(result.status)) throw new Error("issuance was not confirmed; rerun the same command and device name to retry the saved request key");
    if (!nonempty(result.body?.token) || !/^[A-Za-z0-9_-]{32,512}$/u.test(result.body.token) || result.body.client?.id !== journal.client_id || result.body.client?.owner_id !== management.owner_id || result.body.client?.name !== journal.name || result.body.client?.revoked_at) throw new Error("issuance response failed identity validation; revoke the recorded device before attempting replacement");
    // Mark delivery before printing: even a crash while displaying cannot make
    // a completed retrieval print a credential a second time. No secret enters
    // the journal; a lost terminal display requires explicit revoke/reissue.
    journal = { ...journal, state: "completed", updated_at: now() };
    await writePrivateJSON(journalPath, journal);
    output.write(`\nDevice: ${journal.name}\nDeployment: ${journal.deployment_id}\nOwner: ${management.owner_id}\nDevice ID: ${journal.client_id}\nSparkClaw service login credential (shown once):\n${result.body.token}\n\nEnter it in the target client's first-login screen. Keep it in that client's secure credential storage.\n`);
    return { completed: true, clientID: journal.client_id };
  } finally {
    await unlock();
  }
}

export function issuedClientID(ownerID, requestKey) {
  return `client_web_${crypto.createHash("sha256").update(`${ownerID}\0${requestKey}`).digest().subarray(0, 18).toString("base64url")}`;
}

async function lockJournal(filename) {
  for (let attempt = 0; attempt < 2; attempt += 1) {
    try {
      await writePrivateJSON(filename, { pid: process.pid }, { replace: false });
      return () => fs.unlink(filename);
    } catch (error) {
      if (error.code !== "EEXIST") throw error;
      const lock = await readPrivateJSON(filename);
      if (!Number.isInteger(lock.pid) || lock.pid <= 0) throw new Error("invalid credential retrieval lock; inspect local management records");
      try { process.kill(lock.pid, 0); }
      catch (error) {
        if (error.code === "ESRCH") { await fs.unlink(filename); continue; }
      }
      throw new Error("another credential retrieval command is active");
    }
  }
  throw new Error("credential retrieval lock changed; retry");
}

export function socketRequest({ socketPath, origin, token, method, route, body, key, timeoutMS = 15000 }) {
  return new Promise((resolve, reject) => {
    const encoded = body === undefined ? null : JSON.stringify(body);
    const req = http.request({ socketPath, path: route, method, signal: AbortSignal.timeout(timeoutMS), headers: { Host: new URL(origin).host, Authorization: `Bearer ${token}`, ...(encoded ? { "Content-Type": "application/json", "Content-Length": Buffer.byteLength(encoded) } : {}), ...(key ? { "Idempotency-Key": key } : {}) } }, response => {
      const chunks = []; let bytes = 0;
      response.on("data", chunk => { bytes += chunk.length; if (bytes > 64 << 10) response.destroy(new Error("management response exceeded its limit")); else chunks.push(chunk); });
      response.on("error", () => reject(new Error("local management response failed")));
      response.on("end", () => { try { resolve({ status: response.statusCode, body: JSON.parse(Buffer.concat(chunks).toString("utf8")) }); } catch { reject(new Error("local management response is invalid")); } });
    });
    req.on("error", () => reject(new Error("local management service is unavailable; verify the Gateway is running")));
    req.end(encoded);
  });
}
