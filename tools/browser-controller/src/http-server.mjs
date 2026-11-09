import {decode} from '@infinimesh/app-cli-runtime/protocol';

import { asControllerError, invalidRequest, publicError } from "./errors.mjs";
import { MAX_SCRIPT_REQUEST_BYTES } from "./protocol.mjs";
import { startControllerUnixServer, readJSON, rejectQuery, writeJSON } from "./http-primitives.mjs";

export function createRequestHandler(controller) {
  return async function requestHandler(request, response) {
    try {
      const url = new URL(request.url ?? "/", "http://browser-controller.local");
      if (request.method === "GET" && url.pathname === "/v1/health") {
        rejectQuery(url);
        return writeJSON(response, 200, controller.health());
      }
      if (request.method === 'POST' && url.pathname === '/v1/application-host') {
        rejectQuery(url);
        if (!controller.scriptFactory?.host) return writeJSON(response, 503, {error: {code: 'HOST_UNAVAILABLE'}});
        try {
          const value = await controller.scriptFactory.host.control(await readJSON(request, 2 * 1024 * 1024, decode));
          return writeJSON(response, 200, value);
        } catch (error) {
          const code = /^[A-Za-z][A-Za-z0-9_]{0,63}$/u.test(error.code) ? error.code.toUpperCase() : 'HOST_UNAVAILABLE';
          return writeJSON(response, 400, {error: {code, ...(error.diagnosticReason === 'process_exit_context_destroyed' ? {diagnostic: error.diagnosticReason} : {})}});
        }
      }
      if (request.method === "POST" && url.pathname === "/v1/validate-token") {
        rejectQuery(url);
        return writeJSON(response, 200, await controller.validateToken(await readJSON(request)));
      }
      if (request.method === "POST" && url.pathname === "/v1/acquire") {
        rejectQuery(url);
        return writeJSON(response, 201, await controller.acquire(await readJSON(request)));
      }
      if (request.method === "POST" && url.pathname === "/v1/release") {
        rejectQuery(url);
        return writeJSON(response, 200, await controller.release(await readJSON(request)));
      }
      if (request.method === "POST" && url.pathname === "/v1/execute") {
        rejectQuery(url);
        return writeJSON(response, 200, await controller.execute(await readJSON(request)));
      }
      if (request.method === "POST" && url.pathname === "/v1/run-script") {
        rejectQuery(url);
        const abort = new AbortController();
        const disconnected = () => {
          if (!response.writableEnded) abort.abort();
        };
        response.once("close", disconnected);
        try {
          const input = await readJSON(request, MAX_SCRIPT_REQUEST_BYTES);
          if (response.destroyed) abort.abort();
          const result = await controller.runScript(input, { signal: abort.signal });
          return writeJSON(response, 200, result);
        } finally {
          response.removeListener("close", disconnected);
        }
      }
      if (request.method === "POST" && url.pathname === "/v1/application-lifecycle") {
        rejectQuery(url);
        return writeJSON(response, 200, await controller.applicationLifecycle(await readJSON(request)));
      }
      if (request.method === "POST" && url.pathname === "/v1/open-provider-login") {
        rejectQuery(url);
        return writeJSON(response, 200, await controller.openProviderLogin(await readJSON(request)));
      }
      if (request.method === 'POST' && ['/v1/mail-observers/reconcile','/v1/mail-observers/events','/v1/mail-observers/ack'].includes(url.pathname)) {
        rejectQuery(url);
        const abort = new AbortController();
        const disconnected = () => {if (!response.writableEnded) abort.abort();};
        response.once('close', disconnected);
        try {
          const input = await readJSON(request);
          if (response.destroyed) abort.abort();
          const action = url.pathname.split('/').at(-1);
          const feed = controller.mailObserverFeed;
          const result = action === 'reconcile' ? await feed.reconcile(input) : action === 'ack' ? feed.ack(input) : await feed.poll(input, abort.signal);
          return writeJSON(response, 200, result);
        } finally {response.removeListener('close', disconnected);}
      }
      return writeJSON(response, 404, {
        error: "browser controller route was not found",
        code: "not_found",
        retryable: false,
      });
    } catch (error) {
      const safe = asControllerError(error);
      return writeJSON(response, safe.status, publicError(safe));
    }
  };
}

export function startUnixServer({ socketPath, controller }) {
  return startControllerUnixServer({ socketPath, controller, requestHandler: createRequestHandler(controller) });
}
