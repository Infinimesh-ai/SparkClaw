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
    secretsPath = path.join(directory, "secrets.json");
    await fs.writeFile(secretsPath, JSON.stringify({ secrets }), { mode: 0o600, flag: "wx" });
  }
  const secretValues = Object.values(secrets ?? {}).filter(Boolean);
  let cleanupSafe = false;
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
    async writeAttachIntent(sessionName, daemonEntry) {
      // /proc ownership recovery is Linux-only; other platforms retain their
      // existing metadata path, without introducing an unresolvable intent.
      if (process.platform !== "linux") return;
      if(!SESSION_NAME_PATTERN.test(sessionName)||!path.isAbsolute(daemonEntry))throw clientContractError();
      await fs.writeFile(path.join(directory,'attach-intent.json'),JSON.stringify({session_name:sessionName,daemon_entry:daemonEntry}),{flag:'wx',mode:0o600});
    },
    async reapDaemon() {
      await reapMetadataBoundDaemon(directory);
      cleanupSafe = true;
    },
    async remove() {
      if(!cleanupSafe)throw clientContractError();
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

// Classifies a non-zero Playwright CLI exit from its output. Playwright emits
// no machine-readable error code, so these patterns are pinned against the
// pinned playwright-core bundle and test/fixtures/playwright-golden.json.
export function classifyProcessExit(stdout, stderr) {
  const output = `${stdout}\n${stderr}`;
  let reason = "process_exit";
  if (/SyntaxError|Unexpected token|Unexpected identifier/u.test(output)) {
    reason = "process_exit_syntax";
  } else if (
    /Execution context was destroyed|Cannot find context with specified id|navigation.+destroyed/iu.test(output)
  ) {
    reason = "process_exit_context_destroyed";
  } else if (
    /Target page, context or browser has been closed|Session closed|browser ['"].+['"] is not open/iu.test(output)
  ) {
    reason = "process_exit_page_closed";
  } else if (/too many arguments|Unknown option|Invalid input|invalid_type/iu.test(output)) {
    reason = "process_exit_invalid_arguments";
  } else if (/Timeout|timed out/iu.test(output) && /waiting for event ["']download["']/iu.test(output)) {
    reason = "process_exit_download_timeout";
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
  if(Array.isArray(input.message.to))input.message.to.fill("");
  if(Array.isArray(input.message.cc))input.message.cc.fill("");
  if(input.account_address)input.account_address="";
  if(input.reply_target)for(const k of Object.keys(input.reply_target))input.reply_target[k]="";
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
  // Keep the private evidence/fence when ownership cannot be proven.
  await reapMetadataBoundDaemon(directory);
  await fs.rm(directory, { recursive: true, force: true });
}

async function reapPendingAttach(directory) {
  let intent;
  try {
    const intentPath=path.join(directory,'attach-intent.json');
    const stat=await fs.lstat(intentPath);
    if(!stat.isFile()||stat.isSymbolicLink()||stat.size>8192||stat.uid!==process.getuid?.()||(stat.mode&0o077)!==0)throw clientContractError();
    intent=JSON.parse(await fs.readFile(intentPath,'utf8'));
  }
  catch(error){if(error.code==='ENOENT')return;throw error;}
  if(process.platform!=='linux'||!SESSION_NAME_PATTERN.test(intent.session_name)||!path.isAbsolute(intent.daemon_entry))throw clientContractError();
  const cache=path.join(directory,'cache'),cwd=path.join(directory,'output');
  const matching=[];
  for(const entry of await fs.readdir('/proc')){
    if(!/^[1-9][0-9]*$/.test(entry))continue;
    const pid=Number(entry);
    let procStat,args;
    try{
      procStat=await fs.stat(`/proc/${pid}`);if(procStat.uid!==process.getuid())continue;
      args=(await fs.readFile(`/proc/${pid}/cmdline`)).toString().split('\0');
    }catch(error){if(['ENOENT','ESRCH'].includes(error.code))continue;throw clientContractError();}
    if(!args.includes(intent.session_name))continue;
    try{
      const environment=(await fs.readFile(`/proc/${pid}/environ`)).toString().split('\0');
      // Exact source, invocation cache, session and cwd together identify only
      // this launch's daemon. A same-name process with weaker evidence fences.
      if(args[1]!==intent.daemon_entry||args[2]!==intent.session_name||
          !environment.includes(`XDG_CACHE_HOME=${cache}`)||await fs.readlink(`/proc/${pid}/cwd`)!==cwd)throw clientContractError();
      matching.push({pid,start:await processStart(pid)});
    }catch(error){if(['ENOENT','ESRCH'].includes(error.code))continue;throw clientContractError();}
  }
  if(matching.length>1)throw clientContractError();
  for(const {pid,start} of matching)await terminateProcess(pid,start);
}

async function reapMetadataBoundDaemon(directory) {
  let metadata;
  try {
    metadata = JSON.parse(await fs.readFile(path.join(directory, "metadata.json"), "utf8"));
  } catch (error) {
    if (error?.code === "ENOENT") return reapPendingAttach(directory);
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

async function processStart(pid) {
  try {
    const stat=await fs.readFile(`/proc/${pid}/stat`,"utf8");
    return stat.slice(stat.lastIndexOf(") ")+2).split(" ")[19];
  } catch(error) {
    if(error.code==="ENOENT"||error.code==="ESRCH")return null;
    throw error;
  }
}

async function terminateProcess(pid, expectedStart=undefined) {
  const start=expectedStart??await processStart(pid);
  if(start===null||await processStart(pid)!==start)return;
  try {
    process.kill(pid, "SIGTERM");
  } catch (error) {
    if (error?.code === "ESRCH") return;
    throw error;
  }
  for (let attempt = 0; attempt < 40; attempt += 1) {
    if (await processStart(pid)!==start) return;
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  if(await processStart(pid)!==start)return;
  try {
    process.kill(pid, "SIGKILL");
  } catch (error) {
    if (error?.code !== "ESRCH") throw error;
  }
  for (let attempt = 0; attempt < 40; attempt += 1) {
    if (await processStart(pid)!==start) return;
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  throw clientUnavailableError();
}

function messageSecrets(input) {
  const recipients=[...input.message.to??[],...input.message.cc??[]];
  return {
    ...Object.fromEntries(recipients.map((value,index)=>["EMAIL_RECIPIENT_"+index,value])),
    [SECRET_NAMES.recipient]: input.message.recipient,
    [SECRET_NAMES.subject]: input.message.subject ?? "",
    [SECRET_NAMES.body]: input.message.body.content,
  };
}

async function sha256Hex(value) {
  const crypto = await import("node:crypto");
  return crypto.createHash("sha256").update(value).digest("hex");
}
