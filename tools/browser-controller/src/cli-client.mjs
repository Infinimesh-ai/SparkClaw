import {AI_PLATFORM_URLS} from './ai-platform-login.mjs';
import { spawn } from "node:child_process";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { PlaywrightCLITask, createProviderRuntime } from "./cli-task.mjs";
import {
  MAX_CLI_OUTPUT_BYTES,
  clearMessageInput,
  createInvocationState,
  prepareRuntimeRoot,
  scrubPlaywrightEnvironment,
} from "./cli-runtime.mjs";
import { ControllerError } from "./errors.mjs";
import { ProviderScriptRegistry, providerFailureEnvelope } from "./provider-scripts.mjs";
import {MailReadPool} from './mail-read-pool.mjs';

const PACKAGE_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const DEFAULT_CLI_ENTRY = path.join(
  PACKAGE_ROOT,
  "node_modules",
  "@playwright",
  "cli",
  "playwright-cli.js",
);
const DIAGNOSTIC_REASONS = new Set([
  "forbidden_output",
  "output_overflow",
  "process_exit",
  "process_exit_action_timeout",
  "process_exit_download_timeout",
  "process_exit_context_destroyed",
  "process_exit_invalid_arguments",
  "process_exit_page_closed",
  "process_exit_syntax",
  "page_extension_origin",
  "page_google_myaccount_origin",
  "page_google_other_origin",
  "page_google_workspace_origin",
  "page_google_www_origin",
  "page_invalid_url",
  "page_non_https_origin",
  "page_origin_mismatch",
  "page_topology_changed",
  "page_unregistered_microsoft_origin",
  "page_unregistered_other_origin",
  "page_unregistered_qq_origin",
  "page_url_credentials",
  "spawn_error",
  "task_page_missing",
  "timeout",
]);
const DIAGNOSTIC_COMMANDS = new Set([
  "attach",
  "click",
  "close",
  "detach",
  "eval",
  "fill",
  "goto",
  "press",
  "run-code",
  "tab-close",
  "tab-list",
  "tab-select",
]);

export class PlaywrightCLIClientFactory {
  constructor(options = {}) {
    this.entryPoint = options.entryPoint ?? DEFAULT_CLI_ENTRY;
    this.cwd = options.cwd ?? PACKAGE_ROOT;
    this.browserChannel = options.browserChannel ?? "chromium";
    this.executablePath = options.executablePath ?? "";
    this.userDataDir = options.userDataDir ?? "";
    this.connectTimeoutMS = options.connectTimeoutMS ?? 15_000;
    this.actionTimeoutMS = options.actionTimeoutMS ?? 10_000;
    this.navigationTimeoutMS = options.navigationTimeoutMS ?? 30_000;
    this.spawn = options.spawn ?? spawn;
    this.extraEnv = options.extraEnv ?? {};
    this.diagnostic = options.diagnostic ?? (() => {});
    this.timingDiagnostic = options.timingDiagnostic ?? (() => {});
    this.captureTimingDiagnostic = options.captureTimingDiagnostic;
    this.batchReadCommands = options.batchReadCommands !== false;
    this.runtimeRoot = options.runtimeRoot ?? path.join(
      os.tmpdir(),
      "sparkclaw-browser-controller",
      "cli-runtime",
    );
    this.emailWorkspaceRoot = options.emailWorkspaceRoot ?? "";
    this.registry = options.registry ?? new ProviderScriptRegistry();
    this.mailReads = new MailReadPool({idleMS:options.mailReadIdleMS,dispose:lease=>this.#disposeMailRead(lease),diagnostic:this.diagnostic});
    if (
      this.executablePath && !path.isAbsolute(this.executablePath) ||
      this.userDataDir && !path.isAbsolute(this.userDataDir)
    ) {
      throw new TypeError("CLI browser paths must be absolute");
    }
  }

  info() {
    return {
      cli: "playwright-cli",
      cli_version: "0.1.19",
    };
  }

  async prepare() {
    await prepareRuntimeRoot(this.runtimeRoot);
    await this.registry.prepare();
  }

  async runScript({ token, sessionID, provider, operation, scriptID, revision, input, signal, credentialGeneration }) {
    const started = performance.now();
    const timings = {};
    const measure = async (name, action) => {
      const start = performance.now();
      try { return await action(); }
      finally { timings[name] = Math.max(0, performance.now() - start); }
    };
    let phase = "registry";
    let state;
    let client;
    let registration;
    let result;
    let failure;
    let cleanupFailure;
    let cleanupPhase;
    let poolKey;
    let poolReserved = false;
    let retained = false;
    let lease;
    try {
      registration = this.registry.resolve({ provider, operation, scriptID, revision });
      registration.validate(input);
      phase = "runtime";
      poolKey = this.mailReads.identity({provider,operation,input,credentialGeneration,token,registration});
      if (poolKey) {
        lease = await this.mailReads.take(provider,poolKey);
        poolReserved = true;
        if (lease) {
          ({state,client} = lease);
          client.renewReadInvocation(registration,signal);
          phase = 'resume_mail_round';
          try {await measure(phase,()=>client.prepareMailRound(input.discovery.account_address,true));}
          catch (error) {
            // No provider query has run yet. Only a broken task/connection may
            // be rebuilt once; an account/login failure is never papered over.
            if (!(error instanceof ControllerError) || !['browser_page_stale','browser_extension_unavailable'].includes(error.code)) throw error;
            await this.#disposeMailRead(lease);
            state = client = lease = undefined;
          }
        }
      }
      if (!client) {
        state = await createInvocationState(this.runtimeRoot, sessionID, input, operation);
        client = new PlaywrightCLITask({
          entryPoint: this.entryPoint,
          browserChannel: this.browserChannel,
          connectTimeoutMS: this.connectTimeoutMS,
          actionTimeoutMS: this.actionTimeoutMS,
          navigationTimeoutMS: this.navigationTimeoutMS,
          spawn: this.spawn,
          extraEnv: this.extraEnv,
          registration,
          state,
          token,
          signal,
          executablePath: this.executablePath,
          userDataDir: this.userDataDir,
          emailWorkspaceRoot: this.emailWorkspaceRoot,
          captureTimingDiagnostic: this.captureTimingDiagnostic,
          batchReadCommands: this.batchReadCommands,
        });
        phase = "attach";
        await measure("attach", () => client.attach());
        phase = "create_task_page";
        await measure("create_task_page", () => client.createTaskPage());
        phase = "navigate";
        await measure("navigate", () => client.navigate(registration.loginURL));
        if (["send", "read", "discover", "capture", "enumerate_thread", "mark_read", "collect_page"].includes(operation)) {
          phase = "prepare_background_page";
          await measure("prepare_background_page", () => client.prepareBackgroundPage());
        }
        if(poolKey) {
          phase = 'prepare_mail_round';
          await measure(phase,()=>client.prepareMailRound(input.discovery.account_address));
        }
        lease = {state,client,createdAt:Date.now()};
      }
      phase = "provider_handler";
      result = await measure("provider_handler", () => registration.handler(input, createProviderRuntime(client, registration)));
    } catch (error) {
      failure = error;
    } finally {
      if(poolReserved && client && !failure && !signal?.aborted &&
          ['collected','empty'].includes(result?.status) && !result?.failures?.length) {
        try {
          await measure('park_mail_round',()=>client.parkMailRound(input.discovery.account_address));
          if(!signal?.aborted) retained = this.mailReads.keep(provider,lease);
        } catch(error) {cleanupFailure=error;cleanupPhase='park_mail_round';}
      }
      if(poolReserved && !retained) {
        if(state) {
          lease??={state,client,createdAt:Date.now()};
          try {
            await measure('dispose_mail_round',()=>this.#disposeMailRead(lease));
            this.mailReads.discard(provider);
          } catch(error) {
            cleanupFailure??=error;cleanupPhase??='dispose_mail_round';
            this.mailReads.fenceFailedCleanup(provider,lease);
          }
        } else this.mailReads.discard(provider);
      }
      try {
        if (client && !poolReserved) await measure("close_task_page", () => client.closeTaskPage());
      } catch (error) {
        cleanupFailure = error;
        cleanupPhase = "close_task_page";
      }
      try {
        if (client && !poolReserved) await measure("stop_cli", () => client.stop());
      } catch (error) {
        cleanupFailure ??= error;
        cleanupPhase ??= "stop_cli";
      }
      try {
        if (state && !poolReserved) await measure("reap_daemon", () => state.reapDaemon());
      } catch (error) {
        cleanupFailure ??= error;
        cleanupPhase ??= "reap_daemon";
      }
      try {
        if (state && !poolReserved) await measure("remove_runtime", () => state.remove());
      } catch (error) {
        cleanupFailure ??= error;
        cleanupPhase ??= "remove_runtime";
      } finally {
        clearMessageInput(input);
        // Independent opt-in telemetry: no IDs, URLs, input, output, or errors.
        // Omitted stages were never entered; failed entered stages retain time.
        try {
          const record = {
            provider: ["gmail", "qq_mail", "outlook"].includes(provider) ? provider : "unknown",
            operation: ["probe", "send", "read", "discover", "capture", "enumerate_thread", "mark_read", "collect_page"].includes(operation) ? operation : "unknown",
            milliseconds: {...timings, total: Math.max(0, performance.now() - started)},
          };
          Promise.resolve(this.timingDiagnostic(record)).catch(() => {});
        } catch { /* Timing callbacks must not affect execution or cleanup. */ }
      }
    }

    if (operation === "send" && client?.effectAttempted && (failure || cleanupFailure)) {
      if (cleanupFailure) {
        const cleanupReason = DIAGNOSTIC_REASONS.has(cleanupFailure.diagnosticReason)
          ? cleanupFailure.diagnosticReason
          : undefined;
        this.#diagnose({
          provider,
          operation,
          scriptID,
          phase: cleanupPhase,
          code: cleanupFailure instanceof ControllerError
            ? cleanupFailure.code
            : "cleanup_failed",
          reason: cleanupReason,
          command: safeDiagnosticCommand(cleanupFailure.diagnosticCommand),
          ...safeDiagnosticContext(cleanupFailure.diagnosticContext),
        });
      }
      return failedResult(
        registration,
        providerFailureEnvelope(provider, { code: "send_outcome_unknown" }),
      );
    }
    if (failure) {
      const runtimeFailure = failure instanceof ControllerError ? failure :
        failure.cause instanceof ControllerError ? failure.cause : null;
      if (runtimeFailure) {
        const reason = DIAGNOSTIC_REASONS.has(runtimeFailure.diagnosticReason)
          ? runtimeFailure.diagnosticReason
          : undefined;
        const context = safeDiagnosticContext(runtimeFailure.diagnosticContext);
        this.#diagnose({
          provider,
          operation,
          scriptID,
          phase,
          code: runtimeFailure.code,
          reason,
          command: safeDiagnosticCommand(runtimeFailure.diagnosticCommand),
          ...context,
        });
        throw runtimeFailure;
      }
      return failedResult(registration, providerFailureEnvelope(provider, failure));
    }
    if (cleanupFailure) {
      const cleanupReason = DIAGNOSTIC_REASONS.has(cleanupFailure.diagnosticReason)
        ? cleanupFailure.diagnosticReason
        : undefined;
      this.#diagnose({
        provider,
        operation,
        scriptID,
        phase: cleanupPhase ?? "cleanup",
        code: cleanupFailure.code,
        reason: cleanupReason,
        command: safeDiagnosticCommand(cleanupFailure.diagnosticCommand),
        ...safeDiagnosticContext(cleanupFailure.diagnosticContext),
      });
      throw cleanupFailure;
    }
    return {
      state: "completed",
      result,
      sourceChecksum: registration.sourceChecksum,
    };
  }

  async #disposeMailRead(lease) {
    const {client,state}=lease;
    const cleanup=lease.cleanup??={};
    if(cleanup.removed)return;
    let failure;
    const step=async(name,action)=>{
      if(cleanup[name])return;
      try{await action();cleanup[name]=true;}catch(error){failure??=error;}
    };
    if(!cleanup.reaped){
      await step('pageClosed',()=>client?.closeTaskPage());
      await step('cliStopped',()=>client?.stop());
      await step('reaped',()=>state.reapDaemon());
    }
    if(cleanup.reaped){
      // Reaping is the owned-process terminal fence. Preserve its metadata
      // when it fails, and never relaunch CLI cleanup in a removed directory.
      if(client){client.token='';client.signal=undefined;}
      await step('removed',()=>state.remove());
    }
    if(failure)throw failure;
  }

  async drainIdleMailReads() {await this.mailReads.drain();}
  async close() {await this.mailReads.close();}

  async openProviderLogin(provider) {
    const registration = Object.hasOwn(AI_PLATFORM_URLS, provider) ? {loginURL:AI_PLATFORM_URLS[provider]} : this.registry.provider(provider);
    if (
      !this.executablePath ||
      !path.isAbsolute(this.executablePath) ||
      !this.userDataDir ||
      !path.isAbsolute(this.userDataDir)
    ) {
      throw extensionUnavailable();
    }
    const child = this.spawn(
      this.executablePath,
      [`--user-data-dir=${this.userDataDir}`, registration.loginURL],
      {
        cwd: this.cwd,
        env: scrubPlaywrightEnvironment({ ...process.env, ...this.extraEnv }),
        detached: true,
        stdio: "ignore",
        windowsHide: true,
      },
    );
    await new Promise((resolve, reject) => {
      child.once("spawn", resolve);
      child.once("error", reject);
    }).catch((cause) => {
      throw extensionUnavailable(cause);
    });
    child.unref?.();
    return { provider };
  }

  #diagnose(record) {
    try {
      this.diagnostic({ event: "browser_cli_script_failed", ...record });
    } catch {
      // Diagnostics must not change browser execution behavior.
    }
  }
}

function safeDiagnosticCommand(value) {
  return DIAGNOSTIC_COMMANDS.has(value) ? value : undefined;
}

function safeDiagnosticContext(value) {
  if (
    !value ||
    !["stdout", "stderr", "both"].includes(value.stream) ||
    !safeDiagnosticInteger(value.stdoutOccurrences) ||
    !safeDiagnosticInteger(value.stderrOccurrences) ||
    !safeDiagnosticInteger(value.stdoutResidualBytes) ||
    !safeDiagnosticInteger(value.stderrResidualBytes)
  ) {
    return {};
  }
  return {
    stream: value.stream,
    stdoutOccurrences: value.stdoutOccurrences,
    stderrOccurrences: value.stderrOccurrences,
    stdoutResidualBytes: value.stdoutResidualBytes,
    stderrResidualBytes: value.stderrResidualBytes,
  };
}

function safeDiagnosticInteger(value) {
  return Number.isSafeInteger(value) && value >= 0 && value <= MAX_CLI_OUTPUT_BYTES;
}

function failedResult(registration, result) {
  return {
    state: "failed",
    result,
    sourceChecksum: registration.sourceChecksum,
  };
}

function extensionUnavailable(cause) {
  return new ControllerError("browser_extension_unavailable", "browser extension is unavailable", {
    status: 503,
    retryable: true,
    cause,
  });
}
