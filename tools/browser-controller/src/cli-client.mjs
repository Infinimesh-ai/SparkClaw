import {AI_PLATFORM_URLS} from './ai-platform-login.mjs';
import { spawn } from "node:child_process";
import fs from 'node:fs/promises';
import os from "node:os";
import path from "node:path";
import crypto from 'node:crypto';
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
import {ResidentMailObservers} from './mail-observers.mjs';
import { electronConnectionEnvironment, registerElectronConnection } from "./electron-adapter-client.mjs";

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
const SAFE_PROVIDER_FAILURE_CODE = /^[a-z0-9_]{1,64}$/u;

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
    this.electronAdapter = options.electronAdapter ?? null;
    this.diagnostic = options.diagnostic ?? (() => {});
    this.timingDiagnostic = options.timingDiagnostic ?? (() => {});
    this.captureTimingDiagnostic = options.captureTimingDiagnostic;
    this.capturePhaseEvidence = options.capturePhaseEvidence === true;
    this.batchReadCommands = options.batchReadCommands !== false;
    this.runtimeRoot = options.runtimeRoot ?? path.join(
      os.tmpdir(),
      "sparkclaw-browser-controller",
      "cli-runtime",
    );
    this.emailWorkspaceRoot = options.emailWorkspaceRoot ?? "";
    this.registry = options.registry ?? new ProviderScriptRegistry();
    this.sharedMailPages = options.sharedMailPages ?? (process.env.SPARKCLAW_MAIL_SHARED_PAGE_CANDIDATE !== '0');
    this.mailTaskFactory = options.mailTaskFactory ?? (taskOptions => new PlaywrightCLITask(taskOptions));
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
    const manifest = JSON.parse(await fs.readFile(path.resolve(PACKAGE_ROOT,'../../configs/browser-components.json'),'utf8'));
    this.readerArtifactChecksums = new Map(manifest.scripts.filter(item =>
      ['qq-mail-reader.user.js','gmail-mail-reader.user.js','outlook-mail-reader.user.js'].includes(item.file))
      .map(item => [item.file,item.sha256]));
  }

  #observerVersion(provider) {
    const file = {qq_mail:'qq-mail-reader.user.js',gmail:'gmail-mail-reader.user.js',outlook:'outlook-mail-reader.user.js'}[provider];
    const observer = this.registry.entries?.get(`${provider}:observe`)?.sourceChecksum ?? '';
    const reader = this.readerArtifactChecksums?.get(file) ?? '';
    return observer || reader ? `${observer}:${reader}` : '';
  }

  async #ensureMailObservers() {
    if (!this.mailObservers) {
      this.mailObservers = new ResidentMailObservers(this);
      this.mailObserverReady = this.mailObservers.prepare();
    }
    await this.mailObserverReady;
    return this.mailObservers;
  }

  sharedMailKey({provider, ownerScope, account, token, credentialGeneration}) {
    const registration = this.registry.entries?.get(`${provider}:collect_page`);
    const observerChecksum = this.#observerVersion(provider);
    if (!registration) return null;
    return this.mailReads.identity({provider, operation:'collect_page',
      input:{owner_scope:ownerScope,discovery:{provider_mode:'time_range',account_address:account}},
      credentialGeneration, token, registration, observerChecksum});
  }

  async createSharedMailPage({provider, account, token, signal, sessionID, taskID, controllerGeneration, sessionGeneration, pageGeneration}, onLease) {
    const registration = this.registry.entries.get(`${provider}:collect_page`);
    if (!registration) throw new ControllerError('browser_script_unavailable','mail reader unavailable',{status:503});
    const state = await createInvocationState(this.runtimeRoot, sessionID, {}, 'collect_page');
    const lease = {state, client:null, observerKey:crypto.randomBytes(32).toString('hex')};
    onLease?.(lease);
    const observer = await this.#ensureMailObservers();
    const mailObserverConfig = await observer.createConfig(provider, account, state, lease.observerKey);
    const electronConnection = this.electronAdapter ? await registerElectronConnection({adapter:this.electronAdapter,token,
      binding:{task_id:taskID,session_id:sessionID,controller_generation:controllerGeneration,
        session_generation:sessionGeneration,page_generation:pageGeneration}}) : null;
    lease.client = this.mailTaskFactory({...this, registration:{...registration,timeoutMS:120_000}, state, token, signal,
      mailObserverConfig, electronConnection});
    await lease.client.attach();
    await lease.client.createTaskPage();
    await lease.client.navigate(registration.loginURL);
    await lease.client.prepareBackgroundPage();
    await lease.client.prepareMailRound(account);
    lease.client.signal = undefined;
    return lease;
  }

  async startSharedWatch(args, slot) {
    const {provider,input,token,credentialGeneration} = args;
    const key = this.sharedMailKey({provider,ownerScope:input.owner_scope,account:input.account_address,token,credentialGeneration});
    if (!key) throw new ControllerError('browser_script_unavailable','mail reader unavailable',{status:503});
    let lease = await this.mailReads.take(provider,key);
    try {
      if (lease) {
        lease.client.renewReadInvocation(this.registry.entries.get(`${provider}:collect_page`),args.signal);
        await lease.client.prepareMailRound(input.account_address,true);
      } else lease = await this.createSharedMailPage({...args,account:input.account_address}, value => {lease=value;});
      slot.key = lease.observerKey;
      slot.lease = lease;
      await lease.client.activateMailObserver();
      if (!this.mailReads.keep(provider,lease,{watch:true})) throw new ControllerError('browser_controller_stopping','browser controller is stopping',{status:503});
      return lease;
    } catch (error) {
      slot.key = '';
      if (lease) {
        try {await this.#disposeMailRead(lease);this.mailReads.discard(provider);}
        catch (cleanupError) {this.mailReads.fenceFailedCleanup(provider,lease);error.cause = cleanupError;}
      } else this.mailReads.discard(provider);
      throw error;
    }
  }

  invalidateSharedWatch(provider, lease) {
    const slot = this.mailObservers?.slots.get(provider);
    if (slot?.lease === lease) {
      slot.key = '';
      slot.state = 'degraded';
      slot.ready = false;
      this.mailObservers.slots.delete(provider);
    }
  }

  async runScript({ token, sessionID, taskID = "provider-task", controllerGeneration, sessionGeneration = 1, pageGeneration = 1, provider, operation, scriptID, revision, input, signal, credentialGeneration }) {
    if (operation === 'observe') {
      const registration = this.registry.resolve({provider, operation, scriptID, revision});
      registration.validate(input);
      const observer = await this.#ensureMailObservers();
      const result = await observer.run({provider, input, token, credentialGeneration, signal,
        sessionID,taskID,controllerGeneration,sessionGeneration,pageGeneration});
      return {state: 'completed', result, sourceChecksum: registration.sourceChecksum};
    }
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
      poolKey = this.mailReads.identity({provider,operation,input,credentialGeneration,token,registration,
        observerChecksum:this.#observerVersion(provider)});
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
            this.invalidateSharedWatch(provider,lease);
            await this.#disposeMailRead(lease);
            this.mailReads.discard(provider);
            await this.mailReads.take(provider,poolKey);
            state = client = lease = undefined;
          }
        }
      }
      if (!client) {
        const electronConnection = this.electronAdapter ? await registerElectronConnection({
          adapter: this.electronAdapter,
          token,
          binding: {
            task_id: taskID,
            session_id: sessionID,
            controller_generation: controllerGeneration,
            session_generation: sessionGeneration,
            page_generation: pageGeneration,
          },
        }) : null;
        state = await createInvocationState(this.runtimeRoot, sessionID, input, operation);
        const observerKey = this.sharedMailPages && poolKey && this.registry.entries?.has(`${provider}:observe`) ? crypto.randomBytes(32).toString('hex') : undefined;
        const mailObserverConfig = observerKey ? await (await this.#ensureMailObservers()).createConfig(provider,input.discovery.account_address,state,observerKey) : undefined;
        client = this.mailTaskFactory({
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
          capturePhaseEvidence: this.capturePhaseEvidence,
          batchReadCommands: this.batchReadCommands,
          electronConnection,
          mailObserverConfig,
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
        lease = {state,client,observerKey};
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
      if (poolReserved && client && !failure && !signal?.aborted && result && !retained) {
        await this.#diagnosePoolRejection(provider, result, client);
      }
      if(poolReserved && !retained) {
        if(state) {
          lease??={state,client};
          this.invalidateSharedWatch(provider,lease);
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
      if (poolReserved && typeof failure?.code === "string" && SAFE_PROVIDER_FAILURE_CODE.test(failure.code)) {
        this.#diagnose({
          provider,
          operation,
          scriptID,
          phase,
          code: failure.code,
          ...(provider === "qq_mail" && /^(?:qq_(?:source|request|json|head|total|lock|list|complete))$/u.test(failure.diagnosticStage)
            ? { provider_stage: failure.diagnosticStage }
            : {}),
        });
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

  async drainIdleMailReads() {
    if (this.sharedMailPages) this.mailObservers?.suspendAll();
    await this.mailReads.drain();
  }
  async close() {
    let failure;
    try {await this.mailObservers?.close();} catch(error) {failure=error;}
    try {await this.mailReads.close();} catch(error) {failure ??= error;}
    if (failure) throw failure;
  }

  async openProviderLogin(provider) {
    await this.mailObservers?.stop(provider);
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
        env: scrubPlaywrightEnvironment({
          ...process.env,
          ...this.extraEnv,
          ...(this.electronAdapter ? {
            SPARKCLAW_ELECTRON_ADAPTER_SOCKET: this.electronAdapter.socketPath,
            SPARKCLAW_ELECTRON_ADAPTER_SECRET_FILE: this.electronAdapter.secretPath,
          } : {}),
        }),
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

  async #diagnosePoolRejection(provider, result, client) {
    const status = ["partial", "collected", "empty"].includes(result?.status)
      ? result.status
      : "unknown";
    const failureCodes = Array.isArray(result?.failures)
      ? [...new Set(result.failures
        .map((failure) => failure?.error_code)
        .filter((code) => typeof code === "string" && /^[a-z0-9_]{1,64}$/u.test(code)))]
        .slice(0, 8)
      : [];
    try {
      this.diagnostic({
        event: "browser_mail_pool_not_retained",
        provider: ["gmail", "qq_mail", "outlook"].includes(provider) ? provider : "unknown",
        status,
        failure_codes: failureCodes,
      });
    } catch {
      // Diagnostics must not change browser execution behavior.
    }
    if (this.capturePhaseEvidence && client && failureCodes.length) {
      try {
        const details = await client.runReadCode(`async page=>page.evaluate(()=>{
          const value=window.SparkClawMailReader?.diagnostics?.();
          return {original_state:value?.original?.state,original_response:value?.original?.response,list_stage:value?.list?.stage,
            list_template:value?.list?.template,records:value?.records};
        })`, 5_000);
        const state = typeof details?.original_state === 'string' && /^[a-z_0-9]{1,40}$/u.test(details.original_state)
          ? details.original_state : 'unavailable';
        const stage = typeof details?.list_stage === 'string' && /^[a-z_0-9]{1,40}$/u.test(details.list_stage)
          ? details.list_stage : 'unavailable';
        this.diagnostic({event:'browser_mail_reader_rejection',provider,original_state:state,
          original_response: {
            status:Number.isInteger(details?.original_response?.status)?details.original_response.status:0,
            type:['message/rfc822','text/plain','text/html','application/json','application/octet-stream'].includes(details?.original_response?.type)?details.original_response.type:'other',
            bytes:Number.isSafeInteger(details?.original_response?.bytes)?details.original_response.bytes:0,
            provider_code:Number.isSafeInteger(details?.original_response?.provider_code)?details.original_response.provider_code:null,
          },
          list_stage:stage,list_template:details?.list_template===true,
          records:Number.isSafeInteger(details?.records)?details.records:0});
      } catch {
        // Qualification diagnostics cannot affect failure cleanup.
      }
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
