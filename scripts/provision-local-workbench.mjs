#!/usr/bin/env node

import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import { assertNoSymlinkPath, assertPrivateDirectory, readPrivateJSON, writePrivateJSON } from "./lib/private-workbench.mjs";
import { parseBackendDescriptor } from "../apps/desktop/src/main/local-backend.mjs";

const options = parseArguments(process.argv.slice(2));
const runtimeDirectory = path.resolve(options.runtimeDirectory);
const descriptorPath = path.join(runtimeDirectory, "local-workbench.json");
const credentialPath = path.join(runtimeDirectory, "desktop-client.json");
const managementPath = path.join(runtimeDirectory, "local-management.json");
const clientBackendPath = path.join(runtimeDirectory, "client-backend.json");
const managementDirectory = path.join(runtimeDirectory, "management");
const localWebChatDirectory = path.join(runtimeDirectory, "local-webchat");
const localWebChatPath = path.join(runtimeDirectory, "local-webchat.json");

if (!options.check) {
  await assertNoSymlinkPath(runtimeDirectory);
  await fs.mkdir(runtimeDirectory, { recursive: true, mode: 0o700 });
}
await assertPrivateDirectory(runtimeDirectory);
if (!options.check) await fs.mkdir(managementDirectory, { mode: 0o700 }).catch(error => { if (error.code !== "EEXIST") throw error; });
await assertPrivateDirectory(managementDirectory);
if (!options.check) await fs.mkdir(localWebChatDirectory, { mode: 0o700 }).catch(error => { if (error.code !== "EEXIST") throw error; });
if (!options.check || options.localWebChatEnabled) await assertPrivateDirectory(localWebChatDirectory);

const existingDescriptor = await readOptionalJSON(descriptorPath);
const existingCredential = await readOptionalJSON(credentialPath);
const existingManagement = await readOptionalJSON(managementPath);
const existingClientBackend = await readOptionalJSON(clientBackendPath);
const existingLocalWebChat = options.localWebChatEnabled ? await readOptionalJSON(localWebChatPath) : null;
const persistedDeploymentID = stringField(existingCredential?.deployment_id) || stringField(existingDescriptor?.deployment_id);
const requestedDeploymentID = stringField(options.deploymentID);
if (persistedDeploymentID && requestedDeploymentID && persistedDeploymentID !== requestedDeploymentID) {
  throw new Error("configured deployment identity conflicts with the existing local workbench files");
}
const deploymentID = requestedDeploymentID || persistedDeploymentID || crypto.randomUUID();

if (options.check) {
  if (!existingDescriptor || !existingCredential || !existingManagement) throw new Error("local workbench provisioning files are missing");
  validateDescriptor(existingDescriptor, options.origin, deploymentID);
  validateCredential(existingCredential, deploymentID);
  validateManagementCredential(existingManagement, existingCredential, deploymentID);
  if (options.localWebChatEnabled) validateLocalWebChatCredential(existingLocalWebChat, existingCredential, existingManagement, deploymentID);
  if (options.clientOrigin) {
    if (!existingClientBackend) throw new Error("client-backend.json is missing");
    const expected = await createClientBackend(options, deploymentID, existingCredential.owner_id);
    if (JSON.stringify(existingClientBackend) !== JSON.stringify(expected)) throw new Error("client-backend.json does not match the configured LAN identity");
  } else if (existingClientBackend) throw new Error("client-backend.json exists but LAN desktop issuance is not configured");
} else {
  const credential = existingCredential || {
    schema_version: 1,
    deployment_id: deploymentID,
    client_id: `client_desktop_${crypto.randomUUID().replaceAll("-", "")}`,
    owner_id: "owner",
    actor_id: "owner",
    client_name: "SparkClaw Desktop",
    token: crypto.randomBytes(32).toString("base64url"),
  };
  validateCredential(credential, deploymentID);
  const clientBackend = options.clientOrigin ? await createClientBackend(options, deploymentID, credential.owner_id) : undefined;
  if (!existingCredential) {
    await writeAtomicJSON(credentialPath, credential, { replace: false });
  }
  const management = existingManagement || { ...credential, client_id: `local_management_${crypto.randomUUID().replaceAll("-", "")}`, client_name: "Local credential management", token: crypto.randomBytes(32).toString("base64url") };
  validateManagementCredential(management, credential, deploymentID);
  if (!existingManagement) await writeAtomicJSON(managementPath, management, { replace: false });
  if (options.localWebChatEnabled) {
    const localWebChat = existingLocalWebChat || { ...credential, client_id: `local_webchat_${crypto.randomUUID().replaceAll("-", "")}`, client_name: "Local WebChat", token: crypto.randomBytes(32).toString("base64url") };
    validateLocalWebChatCredential(localWebChat, credential, management, deploymentID);
    if (!existingLocalWebChat) await writeAtomicJSON(localWebChatPath, localWebChat, { replace: false });
  }
  await writeAtomicJSON(descriptorPath, {
    schema_version: 1,
    origin: options.origin,
    deployment_id: deploymentID,
  }, { replace: true });
  if (clientBackend) await writeAtomicJSON(clientBackendPath, clientBackend, { replace: true });
  else if (existingClientBackend) await fs.rm(clientBackendPath);
}

process.stdout.write(`${deploymentID}\n`);

function parseArguments(args) {
  const result = { runtimeDirectory: "", origin: "", deploymentID: "", clientOrigin: "", clientTLSCert: "", clientTLSCA: "", localWebChatEnabled: false, check: false };
  for (let index = 0; index < args.length; index += 1) {
    const argument = args[index];
    if (argument === "--check") result.check = true;
    else if (argument === "--runtime-dir") result.runtimeDirectory = args[++index] || "";
    else if (argument === "--origin") result.origin = args[++index] || "";
    else if (argument === "--deployment-id") result.deploymentID = args[++index] || "";
    else if (argument === "--client-origin") result.clientOrigin = args[++index] || "";
    else if (argument === "--client-tls-cert") result.clientTLSCert = args[++index] || "";
    else if (argument === "--client-tls-ca") result.clientTLSCA = args[++index] || "";
    else if (argument === "--local-webchat-enabled") {
      const value = args[++index];
      if (value !== "true" && value !== "false") throw new Error("--local-webchat-enabled must be true or false");
      result.localWebChatEnabled = value === "true";
    }
    else throw new Error(`unknown argument: ${argument}`);
  }
  if (!path.isAbsolute(result.runtimeDirectory)) throw new Error("--runtime-dir must be absolute");
  const url = new URL(result.origin);
  if (url.origin !== result.origin || url.protocol !== "http:" || !["127.0.0.1", "[::1]"].includes(url.hostname)) {
    throw new Error("--origin must be a canonical HTTP loopback origin");
  }
  const clientValues = [result.clientOrigin, result.clientTLSCert, result.clientTLSCA];
  if (clientValues.some(Boolean) && !clientValues.every(Boolean)) throw new Error("LAN client provisioning requires --client-origin, --client-tls-cert and --client-tls-ca together");
  if (result.clientOrigin) {
    if (!path.isAbsolute(result.clientTLSCert) || !path.isAbsolute(result.clientTLSCA)) throw new Error("LAN client TLS paths must be absolute");
    const clientURL = new URL(result.clientOrigin);
    if (clientURL.protocol !== "https:" || clientURL.origin !== result.clientOrigin || clientURL.pathname !== "/" || clientURL.search || clientURL.hash || clientURL.username || clientURL.password) {
      throw new Error("--client-origin must be a canonical HTTPS origin");
    }
  }
  return result;
}

async function createClientBackend(options, deploymentID, ownerID) {
  const [certificatePEM, caPEM] = await Promise.all([
    readPublicCertificate(options.clientTLSCert, "server certificate"),
    readPublicCertificate(options.clientTLSCA, "CA certificate"),
  ]);
  let certificate;
  try { certificate = new crypto.X509Certificate(certificatePEM); }
  catch { throw new Error("LAN server certificate is invalid"); }
  try { new crypto.X509Certificate(caPEM); }
  catch { throw new Error("LAN CA certificate is invalid"); }
  const descriptor = {
    schema_version: 2,
    origin: options.clientOrigin,
    deployment_id: deploymentID,
    owner_id: ownerID,
    tls_certificate_sha256: crypto.createHash("sha256").update(certificate.raw).digest("hex"),
    tls_ca_pem: caPEM.trim(),
  };
  parseBackendDescriptor(descriptor);
  return descriptor;
}

async function readPublicCertificate(filename, label) {
  const value = await fs.readFile(filename, "utf8");
  if (!value.trim() || Buffer.byteLength(value) > 32768) throw new Error(`LAN ${label} is missing or too large`);
  return value;
}

async function readOptionalJSON(filename) {
  return readPrivateJSON(filename, { optional: true });
}

function validateDescriptor(value, origin, deploymentID) {
  if (value?.schema_version !== 1 || value.origin !== origin || value.deployment_id !== deploymentID) {
    throw new Error("local-workbench.json does not match this deployment");
  }
}

function validateCredential(value, deploymentID) {
  if (value?.schema_version !== 1 || value.deployment_id !== deploymentID || !stringField(value.client_id) ||
      !stringField(value.owner_id) || !stringField(value.client_name) || stringField(value.token).length < 32) {
    throw new Error("desktop-client.json is incomplete or belongs to another deployment");
  }
}

function validateManagementCredential(value, desktop, deploymentID) {
  validateCredential(value, deploymentID);
  if (!value.client_id.startsWith("local_management_") || value.client_id === desktop.client_id || value.token === desktop.token) throw new Error("local-management.json must contain an independent host management identity");
}

function validateLocalWebChatCredential(value, desktop, management, deploymentID) {
  if (value?.schema_version !== 1 || value.deployment_id !== deploymentID ||
      !stringField(value.client_id).startsWith("local_webchat_") || !stringField(value.client_name) ||
      value.owner_id !== desktop.owner_id || (value.actor_id || value.owner_id) !== (desktop.actor_id || desktop.owner_id) ||
      !/^[A-Za-z0-9_-]{32,512}$/u.test(stringField(value.token)) ||
      [desktop, management].some(other => value.client_id === other.client_id || value.token === other.token)) {
    throw new Error("local-webchat.json must contain an independent local WebChat identity bound to this deployment Owner");
  }
}

async function writeAtomicJSON(filename, value, options) {
  try { await writePrivateJSON(filename, value, options); }
  catch (error) {
    if (!options.replace && error.code === "EEXIST") throw new Error(`${path.basename(filename)} appeared during provisioning; retry to reconcile it`);
    throw error;
  }
}

function stringField(value) {
  return typeof value === "string" && value === value.trim() ? value : "";
}
