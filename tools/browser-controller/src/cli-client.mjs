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
    this.runtimeRoot = options.runtimeRoot ?? path.join(
      os.tmpdir(),
      "sparkclaw-browser-controller",
      "cli-runtime",
    );
    this.registry = options.registry ?? new ProviderScriptRegistry();
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

  async runScript({ token, sessionID, provider, operation, scriptID, revision, input, signal }) {
    let phase = "registry";
    let state;
    let client;
    let registration;
    let result;
    let failure;
    let cleanupFailure;
    let cleanupPhase;
    try {
      registration = this.registry.resolve({ provider, operation, scriptID, revision });
      registration.validate(input);
      phase = "runtime";
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
      });
      phase = "attach";
      await client.attach();
      phase = "create_task_page";
      await client.createTaskPage();
      phase = "navigate";
      await client.navigate(registration.loginURL);
      phase = "provider_handler";
      result = await registration.handler(input, createProviderRuntime(client, registration));
    } catch (error) {
      failure = error;
    } finally {
      try {
        await client?.closeTaskPage();
      } catch (error) {
        cleanupFailure = error;
        cleanupPhase = "close_task_page";
      }
      try {
        await client?.stop();
      } catch (error) {
        cleanupFailure ??= error;
        cleanupPhase ??= "stop_cli";
      }
      try {
        await state?.reapDaemon();
      } catch (error) {
        cleanupFailure ??= error;
        cleanupPhase ??= "reap_daemon";
      }
      try {
        await state?.remove();
      } catch (error) {
        cleanupFailure ??= error;
        cleanupPhase ??= "remove_runtime";
      } finally {
        clearMessageInput(input);
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
      if (failure instanceof ControllerError) {
        const reason = DIAGNOSTIC_REASONS.has(failure.diagnosticReason)
          ? failure.diagnosticReason
          : undefined;
        const context = safeDiagnosticContext(failure.diagnosticContext);
        this.#diagnose({
          provider,
          operation,
          scriptID,
          phase,
          code: failure.code,
          reason,
          command: safeDiagnosticCommand(failure.diagnosticCommand),
          ...context,
        });
        throw failure;
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

  async openProviderLogin(provider) {
    const registration = this.registry.provider(provider);
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
