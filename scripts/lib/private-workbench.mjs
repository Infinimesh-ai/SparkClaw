import crypto from "node:crypto";
import fs from "node:fs/promises";
import { constants } from "node:fs";
import path from "node:path";

export async function assertNoSymlinkPath(filename) {
  const absolute = path.resolve(filename);
  const segments = absolute.split(path.sep).filter(Boolean);
  let current = path.parse(absolute).root;
  for (const segment of segments) {
    current = path.join(current, segment);
    try {
      if ((await fs.lstat(current)).isSymbolicLink()) throw new Error("local workbench paths must not contain symbolic links");
    } catch (error) {
      if (error.code === "ENOENT") break;
      throw error;
    }
  }
}

export async function assertPrivateDirectory(directory) {
  await assertNoSymlinkPath(directory);
  const info = await fs.lstat(directory);
  if (!info.isDirectory() || info.uid !== process.getuid() || (info.mode & 0o777) !== 0o700) {
    throw new Error("local workbench directory must be owned by the deployment user with mode 0700");
  }
}

export async function readPrivateJSON(filename, { optional = false } = {}) {
  await assertNoSymlinkPath(filename);
  let file;
  try {
    file = await fs.open(filename, constants.O_RDONLY | constants.O_NOFOLLOW);
  } catch (error) {
    if (optional && error.code === "ENOENT") return null;
    throw error;
  }
  try {
    const info = await file.stat();
    if (!info.isFile() || info.uid !== process.getuid() || (info.mode & 0o777) !== 0o600 || info.size > 64 << 10) {
      throw new Error(`${path.basename(filename)} must be an owned regular file with mode 0600, at most 64 KiB`);
    }
    try { return JSON.parse(await file.readFile("utf8")); }
    catch { throw new Error(`${path.basename(filename)} contains invalid JSON`); }
  } finally {
    await file.close();
  }
}

export async function writePrivateJSON(filename, value, { replace = true } = {}) {
  await assertPrivateDirectory(path.dirname(filename));
  await assertNoSymlinkPath(filename);
  const temporary = `${filename}.tmp-${process.pid}-${crypto.randomBytes(6).toString("hex")}`;
  const file = await fs.open(temporary, "wx", 0o600);
  try {
    await file.writeFile(`${JSON.stringify(value, null, 2)}\n`);
    await file.sync();
  } finally {
    await file.close();
  }
  try {
    if (replace) await fs.rename(temporary, filename);
    else await fs.link(temporary, filename);
    const directory = await fs.open(path.dirname(filename), "r");
    try { await directory.sync(); } finally { await directory.close(); }
  } finally {
    await fs.rm(temporary, { force: true });
  }
}

export function canonicalLoopbackOrigin(value) {
  const url = new URL(value);
  if (url.origin !== value || url.protocol !== "http:" || !["127.0.0.1", "[::1]"].includes(url.hostname)) {
    throw new Error("local workbench origin must be a canonical HTTP loopback origin");
  }
  return value;
}

export function validateProvisioning(value, deploymentID) {
  if (value?.schema_version !== 1 || value.deployment_id !== deploymentID || (!nonempty(value.client_id) || !value.client_id.startsWith("local_management_")) ||
      !nonempty(value.owner_id) || !nonempty(value.client_name) || !nonempty(value.token) || !/^[A-Za-z0-9_-]{32,512}$/u.test(value.token) ||
      (value.actor_id !== undefined && !nonempty(value.actor_id))) {
    throw new Error("local management credential is incomplete or belongs to another deployment");
  }
  return value;
}

export function nonempty(value) { return typeof value === "string" && value.trim() === value && value.length > 0; }
