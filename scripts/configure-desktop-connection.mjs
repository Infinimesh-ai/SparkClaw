#!/usr/bin/env node

import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";

const args = process.argv.slice(2);
const check = args.includes("--check");
const runtimeIndex = args.indexOf("--runtime-dir");
if (runtimeIndex < 0 || runtimeIndex + 1 >= args.length ||
    args.length !== (check ? 3 : 2)) {
  throw new Error("Usage: configure-desktop-connection.mjs --runtime-dir ABSOLUTE_PATH [--check]");
}
const runtimeDirectory = args[runtimeIndex + 1];
if (!path.isAbsolute(runtimeDirectory)) throw new Error("Desktop runtime directory must be absolute");
const configHome = process.env.XDG_CONFIG_HOME || path.join(os.homedir(), ".config");
if (!path.isAbsolute(configHome)) throw new Error("Desktop config directory must be absolute");
const directory = path.join(configHome, "sparkclaw");
const filename = path.join(directory, "desktop-connection.json");
const descriptorPath = path.join(runtimeDirectory, "local-workbench.json");
const credentialPath = path.join(runtimeDirectory, "desktop-client.json");
const desired = `${JSON.stringify({
  schema_version: 1,
  descriptor_path: descriptorPath,
  credential_path: credentialPath,
}, null, 2)}\n`;

const runtime = await fs.lstat(runtimeDirectory);
const descriptor = await fs.lstat(descriptorPath);
const credential = await fs.lstat(credentialPath);
if (!runtime.isDirectory() || runtime.isSymbolicLink() || runtime.uid !== process.getuid() ||
    (runtime.mode & 0o777) !== 0o700 ||
    !descriptor.isFile() || descriptor.isSymbolicLink() || descriptor.uid !== process.getuid() ||
    !credential.isFile() || credential.isSymbolicLink() || credential.uid !== process.getuid() ||
    (credential.mode & 0o777) !== 0o600) {
  throw new Error("Desktop runtime files are not owned by the deployment user with private credential permissions");
}

if (check) {
  const [folder, file, content] = await Promise.all([
    fs.lstat(directory), fs.lstat(filename), fs.readFile(filename, "utf8"),
  ]);
  if (!folder.isDirectory() || folder.isSymbolicLink() || folder.uid !== process.getuid() ||
      (folder.mode & 0o777) !== 0o700 ||
      !file.isFile() || file.isSymbolicLink() || file.uid !== process.getuid() ||
      (file.mode & 0o777) !== 0o600 || content !== desired) {
    throw new Error("Desktop connection configuration is stale or unsafe");
  }
} else {
  await fs.mkdir(directory, { recursive: true, mode: 0o700 });
  const folder = await fs.lstat(directory);
  if (!folder.isDirectory() || folder.isSymbolicLink() || folder.uid !== process.getuid() ||
      (folder.mode & 0o777) !== 0o700) {
    throw new Error("Desktop config directory is not owner-only");
  }
  const temporary = `${filename}.tmp-${process.pid}`;
  try {
    await fs.writeFile(temporary, desired, { flag: "wx", mode: 0o600 });
    await fs.rename(temporary, filename);
  } finally {
    await fs.rm(temporary, { force: true });
  }
}

process.stdout.write(`Desktop connection configured: ${filename}\n`);
