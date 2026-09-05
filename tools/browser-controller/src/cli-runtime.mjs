import fs from "node:fs/promises";
import path from "node:path";

import { ControllerError } from "./errors.mjs";

export const MAX_CLI_OUTPUT_BYTES = 2 << 20;

const SESSION_DIRECTORY_PATTERN = /^session-[0-9a-f]{24}$/u;
const SESSION_NAME_PATTERN = /^sc-cli-[0-9a-f]{20}$/u;
const SECRET_NAMES = Object.freeze({
  recipient: "SPARKCLAW_EMAIL_RECIPIENT",
  subject: "SPARKCLAW_EMAIL_SUBJECT",
  body: "SPARKCLAW_EMAIL_BODY",
});

export async function prepareRuntimeRoot(runtimeRoot) {
  validateRuntimeRoot(runtimeRoot);
  await fs.mkdir(runtimeRoot, { recursive: true, mode: 0o700 });
  const stat = await fs.lstat(runtimeRoot);
  if (!stat.isDirectory() || stat.isSymbolicLink()) {
    throw new TypeError("runtimeRoot must be a real directory");
  }
  await fs.chmod(runtimeRoot, 0o700);
  for (const entry of await fs.readdir(runtimeRoot, { withFileTypes: true })) {
    if (entry.isDirectory() && SESSION_DIRECTORY_PATTERN.test(entry.name)) {
      await reconcileStaleInvocation(path.join(runtimeRoot, entry.name));
    }
  }
}

export async function createInvocationState(runtimeRoot, sessionID, input, operation) {
  if (typeof sessionID !== "string" || !/^session_[0-9a-f]{32}$/u.test(sessionID)) {
    throw clientContractError();
  }
  const digest = await sha256Hex(sessionID);
  const directory = path.join(runtimeRoot, `session-${digest.slice(0, 24)}`);
  const cacheDir = path.join(directory, "cache");
  const outputDir = path.join(directory, "output");
  await fs.mkdir(directory, { mode: 0o700 });
  await fs.mkdir(cacheDir, { mode: 0o700 });
  await fs.mkdir(outputDir, { mode: 0o700 });

  const secrets = operation === "send" ? messageSecrets(input) : null;
  let secretsPath = "";
  if (secrets) {
    secretsPath = path.join(directory, "secrets.env");
    await fs.writeFile(secretsPath, encodeDotenv(secrets), { mode: 0o600, flag: "wx" });
  }
  const secretValues = Object.values(secrets ?? {}).filter(Boolean);
  return {
    sessionID,
    directory,
    cacheDir,
    outputDir,
    secretsPath,
    secretValues,
    environment: { XDG_CACHE_HOME: cacheDir },
    secretName(value) {
      if (value === "") return "";
      for (const [name, secretValue] of Object.entries(secrets ?? {})) {
        if (secretValue === value) return name;
      }
      throw clientContractError();
    },
    async writeMetadata(pid, sessionName) {
      if (!Number.isSafeInteger(pid) || pid <= 1 || !SESSION_NAME_PATTERN.test(sessionName)) {
        throw clientContractError();
      }
      await fs.writeFile(
        path.join(directory, "metadata.json"),
        `${JSON.stringify({ pid, session_name: sessionName })}\n`,
        { mode: 0o600, flag: "wx" },
      );
    },
    async reapDaemon() {
      await reapMetadataBoundDaemon(directory);
    },
    async remove() {
      await fs.rm(directory, { recursive: true, force: true });
    },
  };
}

export async function runProcess(spawnImpl, executable, args, options) {
  if (options.secrets.some((secret) => secret && args.some((arg) => arg === secret))) {
    throw clientContractError();
  }
  return await new Promise((resolve, reject) => {
    let child;
    try {
      child = spawnImpl(executable, args, {
        cwd: options.cwd,
        env: options.env,
        stdio: ["ignore", "pipe", "pipe"],
        windowsHide: true,
      });
    } catch (cause) {
      reject(clientUnavailableError(cause, "spawn_error"));
      return;
    }
    let stdout = Buffer.alloc(0);
    let stderr = Buffer.alloc(0);
    let overflow = false;
    let timedOut = false;
    let aborted = false;
    let settled = false;
    const timer = setTimeout(() => {
      timedOut = true;
      child.kill("SIGKILL");
    }, options.timeoutMS);
    timer.unref?.();
    const abort = () => {
      aborted = true;
      child.kill("SIGKILL");
    };
    if (options.signal?.aborted) abort();
    else options.signal?.addEventListener("abort", abort, { once: true });
    const append = (current, chunk) => {
      const next = Buffer.concat([current, Buffer.from(chunk)]);
      if (next.length > MAX_CLI_OUTPUT_BYTES) {
        overflow = true;
        child.kill("SIGKILL");
      }
      return next.subarray(0, MAX_CLI_OUTPUT_BYTES + 1);
    };
    child.stdout?.on("data", (chunk) => { stdout = append(stdout, chunk); });
    child.stderr?.on("data", (chunk) => { stderr = append(stderr, chunk); });
    child.once("error", (cause) => finish(clientUnavailableError(cause, "spawn_error")));
    child.once("close", (code, signal) => {
      if (aborted) return finish(clientUnavailableError());
      if (timedOut) return finish(clientTimeoutError("timeout"));
      if (overflow) return finish(clientContractError("output_overflow"));
      let stdoutText = stdout.toString("utf8");
      const stderrText = stderr.toString("utf8");
      if (code !== 0 || signal !== null) {
        const forbidden = inspectForbiddenOutput(
          stdoutText,
          stderrText,
          options.forbiddenOutputValues,
        );
        if (forbidden) {
          return finish(clientContractError("forbidden_output", forbidden));
        }
        const diagnostic = classifyProcessExit(stdoutText, stderrText);
        return finish(clientUnavailableError(
          undefined,
          diagnostic.reason,
          diagnostic.context,
        ));
      }
      if (options.stdoutTransform) {
        try {
          stdoutText = options.stdoutTransform(stdoutText);
        } catch (error) {
          return finish(
            error instanceof ControllerError ? error : clientContractError(),
          );
        }
      }
      const forbidden = inspectForbiddenOutput(
        stdoutText,
        stderrText,
        options.forbiddenOutputValues,
      );
      if (forbidden) {
        return finish(clientContractError("forbidden_output", forbidden));
      }
      finish(null, stdoutText.trim());
    });

    function finish(error, value) {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      options.signal?.removeEventListener("abort", abort);
      stdout.fill(0);
      stderr.fill(0);
      if (error) reject(error);
      else resolve(value);
    }
  });
}

function classifyProcessExit(stdout, stderr) {
  const output = `${stdout}\n${stderr}`;
  let reason = "process_exit";
  if (/SyntaxError|Unexpected token|Unexpected identifier/u.test(output)) {
    reason = "process_exit_syntax";
  } else if (
    /Execution context was destroyed|Cannot find context with specified id|navigation.+destroyed/iu.test(output)
  ) {
    reason = "process_exit_context_destroyed";
  } else if (
    /Target page, context or browser has been closed|Session closed|Browser ['"].+['"] is not open/u.test(output)
  ) {
    reason = "process_exit_page_closed";
  } else if (/too many arguments|Unknown option|Invalid input|invalid_type/iu.test(output)) {
    reason = "process_exit_invalid_arguments";
  } else if (/Timeout|timed out/iu.test(output)) {
    reason = "process_exit_action_timeout";
  }
  return { reason, context: processOutputContext(stdout, stderr) };
}

function processOutputContext(stdout, stderr) {
  const stdoutResidualBytes = Buffer.byteLength(stdout, "utf8");
  const stderrResidualBytes = Buffer.byteLength(stderr, "utf8");
  if (stdoutResidualBytes === 0 && stderrResidualBytes === 0) return undefined;
  return {
    stream: stdoutResidualBytes > 0 && stderrResidualBytes > 0
      ? "both"
      : stdoutResidualBytes > 0 ? "stdout" : "stderr",
    stdoutOccurrences: 0,
    stderrOccurrences: 0,
    stdoutResidualBytes,
    stderrResidualBytes,
  };
}

function inspectForbiddenOutput(stdout, stderr, forbiddenValues) {
  let stdoutOccurrences = 0;
  let stderrOccurrences = 0;
  let stdoutResidual = stdout;
  let stderrResidual = stderr;
  for (const secret of forbiddenValues) {
    if (!secret) continue;
    stdoutOccurrences += countOccurrences(stdout, secret);
    stderrOccurrences += countOccurrences(stderr, secret);
    stdoutResidual = stdoutResidual.replaceAll(secret, "");
    stderrResidual = stderrResidual.replaceAll(secret, "");
  }
  if (stdoutOccurrences === 0 && stderrOccurrences === 0) return null;
  return {
    stream: stdoutOccurrences > 0 && stderrOccurrences > 0
      ? "both"
      : stdoutOccurrences > 0 ? "stdout" : "stderr",
    stdoutOccurrences,
    stderrOccurrences,
    stdoutResidualBytes: Buffer.byteLength(stdoutResidual, "utf8"),
    stderrResidualBytes: Buffer.byteLength(stderrResidual, "utf8"),
  };
}

function countOccurrences(value, search) {
  let count = 0;
  let offset = 0;
  while ((offset = value.indexOf(search, offset)) >= 0) {
    count += 1;
    offset += search.length;
  }
  return count;
}

export function scrubPlaywrightEnvironment(env) {
  for (const key of Object.keys(env)) {
    if (key.startsWith("PLAYWRIGHT_MCP_") || key === "PLAYWRIGHT_CLI_SESSION") {
      delete env[key];
    }
  }
  return env;
}

export function clearMessageInput(input) {
  if (!input?.message) return;
  input.message.recipient = "";
  if (Object.hasOwn(input.message, "subject")) input.message.subject = "";
  if (input.message.body) input.message.body.content = "";
}

export function clientUnavailableError(cause, diagnosticReason, diagnosticContext) {
  return new ControllerError("browser_extension_unavailable", "browser extension is unavailable", {
    status: 503,
    retryable: true,
    cause,
    diagnosticReason,
    diagnosticContext,
  });
}

export function clientTimeoutError(diagnosticReason) {
  return new ControllerError("browser_script_timeout", "browser provider script timed out", {
    status: 504,
    retryable: true,
    diagnosticReason,
  });
}

export function clientContractError(diagnosticReason, diagnosticContext) {
  return new ControllerError("browser_extension_unavailable", "browser extension is unavailable", {
    status: 503,
    retryable: true,
    diagnosticReason,
    diagnosticContext,
  });
}

export function pageStale(diagnosticReason) {
  return new ControllerError("browser_page_stale", "browser page generation is stale", {
    status: 409,
    diagnosticReason,
  });
}

function validateRuntimeRoot(runtimeRoot) {
  if (
    !path.isAbsolute(runtimeRoot) ||
    path.basename(runtimeRoot) !== "cli-runtime" ||
    path.dirname(runtimeRoot) === path.parse(runtimeRoot).root
  ) {
    throw new TypeError(
      "runtimeRoot must be an absolute cli-runtime directory below a private runtime directory",
    );
  }
}

async function reconcileStaleInvocation(directory) {
  try {
    await reapMetadataBoundDaemon(directory);
  } catch {
    // The private invocation directory is still removed below.
  }
  await fs.rm(directory, { recursive: true, force: true });
}

async function reapMetadataBoundDaemon(directory) {
  let metadata;
  try {
    metadata = JSON.parse(await fs.readFile(path.join(directory, "metadata.json"), "utf8"));
  } catch (error) {
    if (error?.code === "ENOENT") return;
    throw error;
  }
  if (
    process.platform !== "linux" ||
    !Number.isSafeInteger(metadata.pid) ||
    metadata.pid <= 1 ||
    !SESSION_NAME_PATTERN.test(metadata.session_name)
  ) {
    throw clientContractError();
  }
  const command = await fs.readFile(`/proc/${metadata.pid}/cmdline`).catch((error) => {
    if (error?.code === "ENOENT") return Buffer.alloc(0);
    throw error;
  });
  if (command.length === 0) return;
  const argumentsList = command.toString("utf8").split("\0").filter(Boolean);
  const isOwnedDaemon = argumentsList.some((value) => value.endsWith("/cliDaemon.js")) &&
    argumentsList.includes(metadata.session_name);
  command.fill(0);
  if (!isOwnedDaemon) throw clientContractError();
  await terminateProcess(metadata.pid);
}

async function terminateProcess(pid) {
  try {
    process.kill(pid, "SIGTERM");
  } catch (error) {
    if (error?.code === "ESRCH") return;
    throw error;
  }
  for (let attempt = 0; attempt < 40; attempt += 1) {
    if (!await processExists(pid)) return;
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  try {
    process.kill(pid, "SIGKILL");
  } catch (error) {
    if (error?.code !== "ESRCH") throw error;
  }
  for (let attempt = 0; attempt < 40; attempt += 1) {
    if (!await processExists(pid)) return;
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  throw clientUnavailableError();
}

async function processExists(pid) {
  try {
    await fs.access(`/proc/${pid}`);
    return true;
  } catch {
    return false;
  }
}

function messageSecrets(input) {
  return {
    [SECRET_NAMES.recipient]: input.message.recipient,
    [SECRET_NAMES.subject]: input.message.subject ?? "",
    [SECRET_NAMES.body]: input.message.body.content,
  };
}

function encodeDotenv(values) {
  return `${Object.entries(values).map(([name, value]) => {
    if (typeof value !== "string") throw clientContractError();
    return `${name}=${JSON.stringify(value)}`;
  }).join("\n")}\n`;
}

async function sha256Hex(value) {
  const crypto = await import("node:crypto");
  return crypto.createHash("sha256").update(value).digest("hex");
}
