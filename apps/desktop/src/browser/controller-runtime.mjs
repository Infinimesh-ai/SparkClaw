import crypto from "node:crypto";
import fs from "node:fs/promises";
import http from "node:http";
import path from "node:path";
import { spawn } from "node:child_process";
import { verifyControllerBundle, CONTROLLER_PROTOCOL } from "./controller-bundle.mjs";

export const MAX_CONTROLLER_RESPONSE_BYTES = 10 << 20;

// One private process group per incarnation. The packaged Electron is also the
// Node runtime for Controller, MCP and the short-lived adapter launcher.
export class ClientControllerRuntime {
  constructor({ bundleRoot, executable = process.execPath, adapterFactory, onStopped = () => {},
    runtimeRoot = `/tmp/sparkx-${process.getuid()}`, graceMS = 5000 }) {
    Object.assign(this, { bundleRoot, executable, adapterFactory, onStopped, runtimeRoot, graceMS });
    this.state = "stopped";
    this.incarnation = "";
    this.manifest = null;
    this.starting = null;
    this.stopping = null;
    this.child = null;
    this.adapter = null;
    this.directory = "";
    this.controllerGeneration = 0;
    this.closed = false;
  }

  async prepare() {
    try { this.manifest = await verifyControllerBundle(this.bundleRoot); }
    catch (error) { this.manifest = null; this.state = "unavailable"; throw error; }
    return this.snapshot();
  }

  snapshot() {
    return { protocol: CONTROLLER_PROTOCOL, available: Boolean(this.manifest) && !this.closed &&
      !["unavailable", "stopping"].includes(this.state), process_state: this.state,
    supervisor_incarnation: this.incarnation, controller_generation: this.controllerGeneration,
    versions: this.manifest?.versions ?? {}, limits: { response_bytes: MAX_CONTROLLER_RESPONSE_BYTES, timeout_ms: 25000 } };
  }

  async start() {
    if (this.closed || this.stopping) throw new Error("Controller is stopping");
    if (this.state === "ready" || this.state === "busy") return this;
    if (this.starting) return this.starting;
    this.state = "starting";
    this.starting = this.#start().finally(() => { this.starting = null; });
    return this.starting;
  }

  async #start() {
    try {
      await this.prepare();
      if (this.stopping || this.closed) throw new Error("Controller is stopping");
      this.incarnation = crypto.randomBytes(16).toString("hex");
      this.directory = await createControllerDirectory(this.runtimeRoot);
      this.socketPath = path.join(this.directory, "controller.sock");
      this.adapter = await this.adapterFactory({ directory: this.directory, incarnation: this.incarnation });
      const launcher = path.join(this.directory, "launcher");
      const quote = value => `'${value.replaceAll("'", "'\\''")}'`;
      await fs.writeFile(launcher, `#!/bin/sh\nexec ${quote(this.executable)} ${quote(path.join(this.bundleRoot, "bin/sparkclaw-electron-browser.mjs"))} "$@"\n`, { mode: 0o700, flag: "wx" });
      if (this.stopping || this.closed) throw new Error("Controller is stopping");
      const child = this.child = spawn(this.executable, [path.join(this.bundleRoot, "src/client-main.mjs")], {
        cwd: this.bundleRoot, detached: true, stdio: ["pipe", "pipe", "pipe"],
        env: { PATH: "/usr/bin:/bin", HOME: this.directory, TMPDIR: this.directory, ELECTRON_RUN_AS_NODE: "1",
          SPARKCLAW_BROWSER_CONTROLLER_SOCKET: this.socketPath, SPARKCLAW_CLIENT_BROWSER_LAUNCHER: launcher,
          SPARKCLAW_ELECTRON_ADAPTER_SOCKET: this.adapter.socketPath, SPARKCLAW_ELECTRON_ADAPTER_SECRET_FILE: this.adapter.secretPath },
      });
      // This unused input pipe is the parent-liveness channel. Controller
      // closes its whole process group if main exits without running cleanup.
      child.stdin.on("error", () => {});
      this.exit = new Promise(resolve => { child.once("exit", resolve); child.once("error", resolve); });
      // Never retain stderr/page text or tokens in main-process diagnostics.
      child.stderr.resume();
      await new Promise((resolve, reject) => {
        let input = "";
        const timer = setTimeout(() => finish(new Error("Controller startup timed out")), 10000);
        const finish = error => { clearTimeout(timer); child.stdout.off("data", data); error ? reject(error) : resolve(); };
        const data = chunk => {
          input += chunk;
          if (Buffer.byteLength(input) > 8192) return finish(new Error("Controller startup response is invalid"));
          const newline = input.indexOf("\n");
          if (newline < 0) return;
          try {
            const ready = JSON.parse(input.slice(0, newline));
            if (ready.event !== "client_controller_ready" || !Number.isSafeInteger(ready.controller_generation) || ready.controller_generation < 1) throw new Error();
            this.controllerGeneration = ready.controller_generation;
            finish();
          } catch { finish(new Error("Controller startup response is invalid")); }
        };
        child.stdout.on("data", data);
        void this.exit.then(() => finish(new Error("Controller exited during startup")));
      });
      child.stdout.resume();
      if (this.stopping || this.closed) throw new Error("Controller is stopping");
      this.state = "ready";
      const incarnation = this.incarnation;
      void this.exit.then(() => {
        if (this.incarnation === incarnation && !this.stopping) void this.stop("controller_exited").catch(() => {});
      });
      return this;
    } catch (error) {
      // Startup owns partial cleanup. stop() waits for startup before its own
      // cleanup, so this must not recursively wait for stop().
      await this.#cleanup("start_failed");
      this.state = "unavailable";
      if (!this.stopping) this.onStopped("start_failed");
      throw error;
    }
  }

  request(action, body, { signal, timeoutMS = 25000 } = {}) {
    if (!["acquire", "execute", "release", "health"].includes(action) || !["ready", "busy"].includes(this.state)) throw new Error("Controller is unavailable");
    return controllerRequest(this.socketPath, action, body, { signal, timeoutMS });
  }

  stop(reason = "stopped", { permanent = false } = {}) {
    if (permanent) this.closed = true;
    if (this.stopping) return this.stopping;
    this.state = "stopping";
    this.adapter?.revokeControllerTasks(reason);
    if (this.child?.pid) signalGroup(this.child.pid, "SIGTERM");
    this.stopping = (async () => {
      await this.starting?.catch(() => {});
      await this.#cleanup(reason);
      this.state = "stopped";
    })().catch(error => { this.state = "unavailable"; this.closed = true; throw error; })
      .finally(() => {
        // Availability observers and queued acquisitions must see the settled
        // state. Cleanup's transient "stopping" state is not a reusable runtime.
        this.stopping = null;
        this.onStopped(reason);
      });
    return this.stopping;
  }

  async #cleanup(reason) {
    this.adapter?.revokeControllerTasks(reason);
    if (this.child?.pid) {
      const pid = this.child.pid;
      signalGroup(pid, "SIGTERM");
      if (!await waitForGroupExit(pid, this.graceMS)) {
        signalGroup(pid, "SIGKILL");
        if (!await waitForGroupExit(pid, 2000)) throw new Error("Controller process cleanup is unproved");
      }
      await this.exit;
    }
    await this.adapter?.close();
    this.adapter = null;
    this.child = null;
    if (this.directory) await fs.rm(this.directory, { recursive: true, force: true });
    this.directory = "";
    this.controllerGeneration = 0;
    this.incarnation = "";
  }
}

export async function createControllerDirectory(candidate) {
  if (!path.isAbsolute(candidate)) throw new Error("Controller runtime root is invalid");
  // /tmp itself is a platform symlink on macOS; canonicalize only its parent,
  // never the owned root, which must be a real directory owned by this user.
  const parent = await fs.realpath(path.dirname(candidate));
  const root = path.join(parent, path.basename(candidate));
  await fs.mkdir(root, { mode: 0o700 }).catch(error => { if (error.code !== "EEXIST") throw error; });
  const stat = await fs.lstat(root);
  if (!stat.isDirectory() || stat.isSymbolicLink() || stat.uid !== process.getuid() || stat.mode & 0o077) throw new Error("Controller runtime root is not private");
  const directory = await fs.mkdtemp(path.join(root, "c-"));
  if (Buffer.byteLength(path.join(directory, "electron-adapter.sock")) >= 104) {
    await fs.rmdir(directory); throw new Error("Controller runtime socket path is too long");
  }
  return directory;
}

export function controllerRequest(socketPath, action, body, { signal, timeoutMS = 25000 } = {}) {
  if (!Number.isSafeInteger(timeoutMS) || timeoutMS < 1 || timeoutMS > 25000) throw new Error("Controller timeout is invalid");
  const encoded = body === undefined ? "" : JSON.stringify(body);
  if (Buffer.byteLength(encoded) > 48 << 10) throw new Error("Controller request is too large");
  return new Promise((resolve, reject) => {
    const request = http.request({ socketPath, path: `/v1/${action}`, method: action === "health" ? "GET" : "POST", signal,
      headers: { "content-type": "application/json", "content-length": Buffer.byteLength(encoded) } }, response => {
      const chunks = []; let size = 0;
      response.on("error", reject);
      response.on("data", chunk => { size += chunk.length; if (size > MAX_CONTROLLER_RESPONSE_BYTES) request.destroy(new Error("Controller response is too large")); else chunks.push(chunk); });
      response.on("end", () => {
        try {
          const payload = JSON.parse(Buffer.concat(chunks).toString("utf8"));
          resolve({ status: response.statusCode, payload });
        } catch { reject(new Error("Controller response is invalid")); }
        finally { for (const chunk of chunks) chunk.fill(0); }
      });
    });
    const timer = setTimeout(() => request.destroy(new Error("Controller request timed out")), timeoutMS);
    request.once("close", () => clearTimeout(timer));
    request.once("error", reject);
    request.end(encoded);
  });
}

function signalGroup(pid, signal) {
  if (!Number.isSafeInteger(pid) || pid <= 1 || pid === process.pid) throw new Error("Controller process group is invalid");
  try { process.kill(-pid, signal); } catch (error) { if (error.code !== "ESRCH") throw error; }
}
async function waitForGroupExit(pid, timeoutMS) {
  const deadline = performance.now() + timeoutMS;
  do {
    try { process.kill(-pid, 0); } catch (error) { if (error.code === "ESRCH") return true; throw error; }
    await new Promise(resolve => setTimeout(resolve, 25));
  } while (performance.now() < deadline);
  return false;
}
