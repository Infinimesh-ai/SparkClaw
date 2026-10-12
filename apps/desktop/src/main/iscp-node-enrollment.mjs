import { spawn } from "node:child_process";
import path from "node:path";
import fs from "node:fs/promises";
import { iscpHelperExecutable } from "./iscp-transport.mjs";
import { parseBackendDescriptor } from "./local-backend.mjs";
import { parseISCPConnectionCredential } from "./iscp-connection-credential.mjs";
import { parseStrictJSON } from "./strict-json.mjs";

export async function enrollNodeISCP({ credential, directory, installationID, signal, packaged, resourcesPath, spawnProcess = spawn, timeoutMS = 120000 }) {
  parseISCPConnectionCredential(credential);
  if (!path.isAbsolute(directory || "") || !/^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/u.test(installationID || "")) throw new Error("ISCP enrollment storage identity is invalid");
  await fs.mkdir(directory, { recursive: true, mode: 0o700 });
  const info = await fs.lstat(directory);
  if (!info.isDirectory() || info.isSymbolicLink() || (info.mode & 0o777) !== 0o700 || await fs.realpath(directory) !== directory ||
      process.getuid && info.uid !== process.getuid()) throw new Error("ISCP enrollment storage is not private");
  if (signal?.aborted) throw new Error("ISCP enrollment was canceled");
  return new Promise((resolve, reject) => {
    let child, done = false, buffer = Buffer.alloc(0), killTimer;
    const finish = (error, value) => {
      if (done) return;
      done = true; clearTimeout(timer); signal?.removeEventListener("abort", cancel);
      child?.stdin.destroy();
      if (child && child.exitCode === null) {
        child.kill();
        killTimer = setTimeout(() => { if (child.exitCode === null) child.kill("SIGKILL"); }, 1000); killTimer.unref?.();
      }
      error ? reject(error) : resolve(value);
    };
    const cancel = () => finish(new Error("ISCP enrollment was canceled"));
    const timer = setTimeout(() => finish(new Error("ISCP enrollment requires reconciliation; retry the same credential")), timeoutMS);
    signal?.addEventListener("abort", cancel, { once: true });
    try {
      child = spawnProcess(iscpHelperExecutable({ packaged, resourcesPath }), ["-enroll-node", "-directory", directory, "-installation-id", installationID, "-key-backend", "keyring", "-keyring-service", `ai.infinimesh.sparkx.iscp.${installationID}`], { stdio: ["pipe", "pipe", "pipe"], windowsHide: true });
      child.stderr.on("data", () => {}); // Helper errors may carry SDK secrets.
      child.on("error", () => finish(new Error("ISCP enrollment helper is unavailable")));
      child.stdin.on("error", () => finish(new Error("ISCP enrollment helper is unavailable")));
      child.stdout.on("data", chunk => {
        buffer = Buffer.concat([buffer, chunk]);
        if (buffer.length > 65536) finish(new Error("ISCP enrollment result exceeds bounds"));
      });
      child.on("close", code => {
        clearTimeout(killTimer);
        if (done) return;
        if (code !== 0) { finish(new Error("ISCP enrollment failed; the existing connection is retained")); return; }
        try {
          if (!buffer.length || buffer.at(-1) !== 10 || buffer.subarray(0, -1).includes(10)) throw new Error();
          const value = parseStrictJSON(new TextDecoder("utf-8", { fatal: true }).decode(buffer.subarray(0, -1)));
          if (Object.keys(value).sort().join(",") !== "descriptor,ipc_version,profile_path,type" || value.ipc_version !== 1 || value.type !== "enrollment" ||
              typeof value.profile_path !== "string" || path.dirname(value.profile_path) !== directory || !path.isAbsolute(value.profile_path) || value.profile_path !== path.resolve(value.profile_path) || /[\x00-\x1f\x7f]/u.test(value.profile_path)) throw new Error();
          if (parseBackendDescriptor(value.descriptor).mode !== "node-managed") throw new Error();
          finish(undefined, Object.freeze(value));
        } catch { finish(new Error("ISCP enrollment result is invalid")); }
      });
      // The secret is neither a process argument nor a log/renderer result.
      child.stdin.end(`${credential}\n`);
    } catch { finish(new Error("ISCP enrollment helper is unavailable")); }
  });
}
