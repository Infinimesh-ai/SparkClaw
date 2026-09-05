import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";

import { parseID } from "./protocol.mjs";

export const SESSION_OUTPUT_PATTERN = /^session-[0-9a-f]{24}$/u;

export async function createSessionOutputDir(outputRoot, sessionID) {
  const normalized = parseID(sessionID, "session_id");
  const digest = crypto.createHash("sha256").update(normalized).digest("hex").slice(0, 24);
  await fs.mkdir(outputRoot, { recursive: true, mode: 0o700 });
  await fs.chmod(outputRoot, 0o700);
  const outputDir = path.join(outputRoot, `session-${digest}`);
  await fs.mkdir(outputDir, { mode: 0o700 });
  return outputDir;
}

export function validateOutputRoot(outputRoot) {
  if (
    !path.isAbsolute(outputRoot) ||
    path.basename(outputRoot) !== "mcp-output" ||
    path.dirname(outputRoot) === path.parse(outputRoot).root
  ) {
    throw new TypeError("outputRoot must be an absolute mcp-output directory below a private runtime directory");
  }
}

export async function prepareOutputRoot(outputRoot) {
  await fs.mkdir(outputRoot, { recursive: true, mode: 0o700 });
  const stat = await fs.lstat(outputRoot);
  if (!stat.isDirectory() || stat.isSymbolicLink()) {
    throw new TypeError("outputRoot must be a real directory");
  }
  await fs.chmod(outputRoot, 0o700);
  for (const entry of await fs.readdir(outputRoot, { withFileTypes: true })) {
    if (entry.isDirectory() && SESSION_OUTPUT_PATTERN.test(entry.name)) {
      await fs.rm(path.join(outputRoot, entry.name), { recursive: true, force: true });
    }
  }
}

export async function removeSessionOutputDir(outputDir) {
  if (!outputDir) return;
  await fs.rm(outputDir, { recursive: true, force: true }).catch(() => {});
}
