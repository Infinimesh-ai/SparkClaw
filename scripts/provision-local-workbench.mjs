#!/usr/bin/env node

import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";

const options = parseArguments(process.argv.slice(2));
const runtimeDirectory = path.resolve(options.runtimeDirectory);
const descriptorPath = path.join(runtimeDirectory, "local-workbench.json");
const credentialPath = path.join(runtimeDirectory, "desktop-client.json");

if (!options.check) {
  await fs.mkdir(runtimeDirectory, { recursive: true, mode: 0o700 });
  await fs.chmod(runtimeDirectory, 0o700);
}
await assertPrivateDirectory(runtimeDirectory);

const existingDescriptor = await readOptionalJSON(descriptorPath);
const existingCredential = await readOptionalJSON(credentialPath, true);
const persistedDeploymentID = stringField(existingCredential?.deployment_id) || stringField(existingDescriptor?.deployment_id);
const requestedDeploymentID = stringField(options.deploymentID);
if (persistedDeploymentID && requestedDeploymentID && persistedDeploymentID !== requestedDeploymentID) {
  throw new Error("configured deployment identity conflicts with the existing local workbench files");
}
const deploymentID = requestedDeploymentID || persistedDeploymentID || crypto.randomUUID();

if (options.check) {
  if (!existingDescriptor || !existingCredential) throw new Error("local workbench provisioning files are missing");
  validateDescriptor(existingDescriptor, options.origin, deploymentID);
  validateCredential(existingCredential, deploymentID);
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
  if (!existingCredential) {
    await writeAtomicJSON(credentialPath, credential, { replace: false });
  }
  await writeAtomicJSON(descriptorPath, {
    schema_version: 1,
    origin: options.origin,
    deployment_id: deploymentID,
  }, { replace: true });
}

process.stdout.write(`${deploymentID}\n`);

function parseArguments(args) {
  const result = { runtimeDirectory: "", origin: "", deploymentID: "", check: false };
  for (let index = 0; index < args.length; index += 1) {
    const argument = args[index];
    if (argument === "--check") result.check = true;
    else if (argument === "--runtime-dir") result.runtimeDirectory = args[++index] || "";
    else if (argument === "--origin") result.origin = args[++index] || "";
    else if (argument === "--deployment-id") result.deploymentID = args[++index] || "";
    else throw new Error(`unknown argument: ${argument}`);
  }
  if (!path.isAbsolute(result.runtimeDirectory)) throw new Error("--runtime-dir must be absolute");
  const url = new URL(result.origin);
  if (url.origin !== result.origin || url.protocol !== "http:" || !["127.0.0.1", "[::1]"].includes(url.hostname)) {
    throw new Error("--origin must be a canonical HTTP loopback origin");
  }
  return result;
}

async function assertPrivateDirectory(directory) {
  const info = await fs.lstat(directory);
  if (!info.isDirectory() || info.isSymbolicLink() || (info.mode & 0o777) !== 0o700 || info.uid !== process.getuid()) {
    throw new Error("local workbench runtime directory must be owned by the deployment user with mode 0700");
  }
}

async function readOptionalJSON(filename, privateFile = false) {
  let info;
  try {
    info = await fs.lstat(filename);
  } catch (error) {
    if (error?.code === "ENOENT") return null;
    throw error;
  }
  if (!info.isFile() || info.isSymbolicLink() || info.uid !== process.getuid()) throw new Error(`${path.basename(filename)} is not a controlled regular file`);
  if (privateFile && (info.mode & 0o777) !== 0o600) throw new Error(`${path.basename(filename)} must have mode 0600`);
  const raw = await fs.readFile(filename, "utf8");
  if (Buffer.byteLength(raw) > 64 << 10) throw new Error(`${path.basename(filename)} is too large`);
  return JSON.parse(raw);
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

async function writeAtomicJSON(filename, value, { replace }) {
  const temporary = `${filename}.tmp-${process.pid}-${crypto.randomBytes(6).toString("hex")}`;
  await fs.writeFile(temporary, `${JSON.stringify(value, null, 2)}\n`, { mode: 0o600, flag: "wx" });
	try {
		if (replace) {
			await fs.rename(temporary, filename);
		} else {
			await fs.link(temporary, filename);
			await fs.unlink(temporary);
		}
		await fs.chmod(filename, 0o600);
	} catch (error) {
		await fs.rm(temporary, { force: true });
		if (!replace && error?.code === "EEXIST") {
			throw new Error(`${path.basename(filename)} appeared during provisioning; retry to reconcile it`);
		}
		throw error;
	}
}

function stringField(value) {
  return typeof value === "string" && value === value.trim() ? value : "";
}
