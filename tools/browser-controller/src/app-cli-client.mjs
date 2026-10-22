import fs from 'node:fs/promises';
import crypto from 'node:crypto';
import path from 'node:path';
import {spawn} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {MailboxClient} from '@infinimesh/app-cli-runtime/mail-client';
import {verifyRelease} from '@infinimesh/app-cli-runtime/release';
import {BrowserHostPort} from '@infinimesh/app-cli-runtime/host-port';
import {ApplicationHostDriver} from './host-driver.mjs';
import {ControllerError} from './errors.mjs';
import {scrubPlaywrightEnvironment} from './cli-runtime.mjs';
import {AI_PLATFORM_URLS} from './ai-platform-login.mjs';

const PACKAGE_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

export class AppCLIClientFactory {
  constructor(options = {}) {
    Object.assign(this, options);
    this.configFile = options.configFile ?? process.env.SPARKCLAW_APP_CLI_CONFIG;
    this.python = options.python ?? process.env.SPARKCLAW_APP_CLI_PYTHON;
    this.managesLifecycle = true;
    this.sharedMailPages = options.sharedMailPages ?? (process.env.SPARKCLAW_MAIL_SHARED_PAGE_CANDIDATE !== '0');
  }
  async prepare() {
    try {await this.prepareApplication();}
    catch (error) {
      await this.host?.close().catch(() => {});
      await this.driver?.shutdown().catch(() => {});
      this.client = null; this.host = null; this.driver = null;
      this.initializationError = 'application_release_unavailable';
      this.diagnostic?.({event: 'application_initialization_failed', code: this.initializationError});
      if (this.fixtureAssembly) throw error;
    }
  }
  async prepareApplication() {
    if (!this.configFile || !this.python) return;
    this.client = new MailboxClient({configFile: this.configFile, python: this.python, sharedPages: this.sharedMailPages});
    if (this.client.config.release_digest) {
      verifyRelease(this.client.config);
      const loadedRuntime = path.resolve(path.dirname(fileURLToPath(import.meta.resolve('@infinimesh/app-cli-runtime/release'))), '..');
      verifyRelease({...this.client.config, runtime_directory: loadedRuntime, bindings: []});
      const projection = JSON.parse(await fs.readFile(new URL('../../../configs/app-cli-release.json', import.meta.url), 'utf8'));
      if (projection.runtime_digest !== this.client.config.release_digest) throw new Error('SparkClaw application projection mismatch');
    } else if (!this.fixtureAssembly) throw new Error('Pinned application release is required');
    this.driver = new ApplicationHostDriver({...this,
      entryPoint: path.join(PACKAGE_ROOT, 'node_modules/@playwright/cli/playwright-cli.js'),
      releaseRoot: this.client.config.runtime_directory,
      spawn, connectTimeoutMS: this.connectTimeoutMS ?? 15000, actionTimeoutMS: this.actionTimeoutMS ?? 10000,
      navigationTimeoutMS: this.navigationTimeoutMS ?? 30000});
    await this.driver.prepare();
    await fs.mkdir(this.client.config.host_state_directory, {recursive: true, mode: 0o700});
    const bindings = await Promise.all(this.client.config.bindings.map(async installed => JSON.parse(await fs.readFile(installed.path, 'utf8'))));
    this.host = new BrowserHostPort({authorization: this.client.client.authorization,
      bindings, driver: this.driver, releaseDigest: this.client.config.release_digest, stateDirectory: this.client.config.host_state_directory});
    // Drop authorization heads of reclaimed requests; failures retry later.
    this.maintenance = setInterval(() => {void this.client?.maintain?.().catch(() => {});}, this.maintenanceIntervalMS ?? 10 * 60_000);
    this.maintenance.unref();
  }
  // Lifecycle 3.0 product control for the Gateway outbox (see controller).
  async lifecycle({action, provider, operation, taskID, terminal_revision, result_digest, receipt_id, resolution, epoch, drained_digest}) {
    const client = this.requireClient();
    if (action === 'epoch') return {epoch_state: await client.epochState(provider)};
    if (action === 'seal') return {epoch_state: await client.epochState(provider, 'seal', {epoch, drained_digest})};
    if (action === 'lookup') return client.lookupTask(provider, operation, taskID);
    if (action === 'ack') return client.acknowledge({provider, operation, taskID, terminal_revision, result_digest, receipt_id});
    const resolved = await client.resolve({provider, operation, taskID, resolution});
    return {task: resolved.task ?? (resolved.lifecycle && {id: resolved.lifecycle.task_id, status: resolved.lifecycle.status}),
      ...(resolved.lifecycle ? {lifecycle: resolved.lifecycle} : {})};
  }
  bindController(controller) {
    if (!this.driver) return;
    this.driver.controller = controller;
    this.driver.drainIdle = () => this.host.drainIdle();
    this.driver.drainCleanupReads = handles => this.drainCleanupReads(handles);
  }
  async drainCleanupReads(handles) {
    const host = this.host;
    if (!host || host.closed) return;
    const slots = handles.map(handle => [...host.resources.values()].find(slot => slot.handle === handle));
    // Verify the whole batch before retiring anything. Pending admission may
    // acquire a write reservation; unknown or changing resources stay fenced.
    if (slots.some(slot => !slot || slot.creating || slot.fenced || slot.disposing || slot.retiring ||
        slot.handle.closed || slot.handle.cleanupFailure || slot.handle.bootstrap ||
        !['read', 'watch'].includes(slot.handle.activityKind) ||
        [...slot.leases].some(id => {
          const lease = host.leases.get(id);
          return !lease || lease.pending || !['read', 'watch'].includes(lease.kind);
        }))) return;
    // Epoch hello may already have retired receive leases before revisiting
    // the failed slot. They are safe to drain without waiting on that hello.
    // Close admission synchronously for every selected pool before yielding.
    // Existing read calls drain through dispose; no write lease is cancelled.
    for (const slot of slots) slot.retiring = (slot.retiring ?? 0) + 1;
    try {for (const slot of slots) await host.dispose(slot);}
    finally {for (const slot of slots) slot.retiring--;}
  }
  info() {return {app_cli: this.client ? 'lifecycle-3.0' : this.initializationError ?? 'unconfigured', browser_host: '1.0'};}
  requireClient() {
    if (!this.client) throw new ControllerError('browser_script_unavailable', 'Application release is not configured', {status: 503});
    return this.client;
  }
  async runScript(request) {
    const client = this.requireClient();
    try {
      // The product subscription identity is independent of App-CLI's private
      // release/cache identity. Retain the original credential identity in the
      // callback so events from a retired subscription cannot enter a new one.
      const identity = request.operation === 'observe' ? crypto.createHash('sha256').update(JSON.stringify([
        request.token, request.credentialGeneration, request.provider, request.input.account_address?.toLowerCase(), request.input.owner_scope,
      ])).digest('hex') : null;
      const response = await client.execute({...request, onEvent: (slot, kind, reason, targets) => this.mailObserverSink?.(
        request.provider, {...slot, identity}, kind, reason, targets)});
      // Executor admission contention is a returned task, not necessarily a
      // thrown error. Preserve the Gateway's short yield for the same read.
      if (['read', 'capture', 'collect_page'].includes(request.operation) && response.state === 'failed' &&
          response.task?.status === 'blocked' && ['browser_busy', 'host_resource_busy'].includes(response.result?.code)) {
        throw Object.assign(new Error('Browser is busy'), {code: 'BROWSER_BUSY'});
      }
      return response;
    } catch (error) {
      if (error.code === 'HOST_RESOURCE_BUSY' || error.code === 'BROWSER_BUSY') throw new ControllerError('browser_busy', 'Browser is busy', {status: 409, retryable: true});
      if (['RELEASE_MISMATCH', 'COMMAND_NOT_FOUND', 'DEPLOYMENT_INVALID'].includes(error.code)) throw new ControllerError('browser_script_unavailable', 'Application release is unavailable', {status: 503});
      throw new ControllerError('browser_extension_unavailable', 'Application execution is unavailable', {status: 503, cause: error});
    }
  }
  describe(provider, operation) {return this.requireClient().describe(provider, operation).spec;}
  pollWatch(provider, options) {return this.requireClient().pollWatch(provider, options);}
  stopWatch(provider) {return this.requireClient().stopWatch(provider);}
  async drainIdleMailReads() {await this.host?.drainIdle();}
  async openProviderLogin(provider) {
    if (this.client?.watches.has(provider)) await this.client.stopWatch(provider);
    const url = AI_PLATFORM_URLS[provider] ?? this.requireClient().describe(provider, 'probe').spec.host.page.loginURL;
    if (!path.isAbsolute(this.executablePath ?? '') || !path.isAbsolute(this.userDataDir ?? '')) throw new ControllerError('browser_extension_unavailable', 'Browser launcher is unavailable', {status: 503});
    const child = spawn(this.executablePath, [`--user-data-dir=${this.userDataDir}`, url], {
      env: scrubPlaywrightEnvironment({...process.env, ...this.extraEnv, ...(this.electronAdapter ? {
        SPARKCLAW_ELECTRON_ADAPTER_SOCKET: this.electronAdapter.socketPath,
        SPARKCLAW_ELECTRON_ADAPTER_SECRET_FILE: this.electronAdapter.secretPath,
      } : {})}), detached: true, stdio: 'ignore', windowsHide: true,
    });
    await new Promise((resolve, reject) => {child.once('spawn', resolve); child.once('error', reject);});
    child.unref(); return {provider};
  }
  async close() {
    clearInterval(this.maintenance);
    let failure;
    try {await this.client?.close();} catch (error) {failure = error;}
    try {await this.host?.close();} catch (error) {failure ??= error;}
    try {await this.driver?.shutdown();} catch (error) {failure ??= error;}
    if (failure) throw failure;
  }
}
