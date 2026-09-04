import crypto from "node:crypto";

import { ControllerError } from "./errors.mjs";
import {
  MAX_SESSION_TTL_MS,
  MAX_WAIT_MS,
  parseBoundedMilliseconds,
  parseGeneration,
  parseID,
  parseProfileID,
  parseToken,
  requireExactObject,
} from "./protocol.mjs";

const DEFAULT_SESSION_TTL_MS = 2 * 60_000;

export class BrowserController {
  constructor({
    profileID = "default",
    clientFactory,
    controllerGeneration = randomGeneration(),
    defaultSessionTTLMS = DEFAULT_SESSION_TTL_MS,
  }) {
    if (!clientFactory || typeof clientFactory.open !== "function") {
      throw new TypeError("clientFactory.open is required");
    }
    this.profileID = parseID(profileID, "profile_id");
    this.clientFactory = clientFactory;
    this.controllerGeneration = parseGeneration(controllerGeneration, "controller_generation");
    this.defaultSessionTTLMS = parseBoundedMilliseconds(
      defaultSessionTTLMS,
      "default_session_ttl_ms",
      MAX_SESSION_TTL_MS,
      DEFAULT_SESSION_TTL_MS,
    );
    this.sessionGeneration = 0;
    this.active = null;
    this.shuttingDown = false;
  }

  health() {
    return {
      schema_version: 1,
      state: this.shuttingDown ? "stopping" : this.active ? "busy" : "ready",
      profile_id: this.profileID,
      controller_generation: this.controllerGeneration,
      active_session: Boolean(this.active),
      versions: this.clientFactory.info?.() ?? {},
    };
  }

  async validateToken(input) {
    requireExactObject(input, ["profile_id", "token"]);
    parseProfileID(input.profile_id, this.profileID);
    const token = parseToken(input.token);
    const reservation = await this.#reserve(0, "validation", "validation");
    let client;
    try {
      client = await this.clientFactory.open({ token, sessionID: reservation.sessionID });
      await client.createTaskPage();
      reservation.pageGeneration = 1;
      await client.closeTaskPage();
      return {
        schema_version: 1,
        state: "ready",
        profile_id: this.profileID,
        controller_generation: this.controllerGeneration,
        session_generation: reservation.sessionGeneration,
        page_generation: reservation.pageGeneration,
        versions: this.clientFactory.info?.() ?? {},
      };
    } catch (error) {
      throw normalizeClientError(error);
    } finally {
      await client?.close().catch(() => {});
      this.#finishReservation(reservation);
      input.token = "";
    }
  }

  async acquire(input) {
    requireExactObject(
      input,
      ["profile_id", "lane", "task_id", "credential_generation", "token"],
      ["wait_timeout_ms", "session_ttl_ms"],
    );
    parseProfileID(input.profile_id, this.profileID);
    if (input.lane !== "mcp") {
      throw new ControllerError("browser_lane_unavailable", "browser lane is unavailable", {
        status: 400,
      });
    }
    const taskID = parseID(input.task_id, "task_id");
    const credentialGeneration = parseGeneration(input.credential_generation, "credential_generation");
    const token = parseToken(input.token);
    const waitMS = parseBoundedMilliseconds(input.wait_timeout_ms, "wait_timeout_ms", MAX_WAIT_MS, 0);
    const sessionTTLMS = parseBoundedMilliseconds(
      input.session_ttl_ms,
      "session_ttl_ms",
      MAX_SESSION_TTL_MS,
      this.defaultSessionTTLMS,
    );
    if (sessionTTLMS === 0) throw new ControllerError("invalid_request", "session_ttl_ms is invalid", { status: 400 });

    const reservation = await this.#reserve(waitMS, input.lane, taskID);
    let client;
    try {
      client = await this.clientFactory.open({ token, sessionID: reservation.sessionID });
      await client.createTaskPage();
      reservation.client = client;
      reservation.credentialGeneration = credentialGeneration;
      reservation.pageGeneration = 1;
      reservation.timer = setTimeout(() => {
        void this.#releaseReservation(reservation);
      }, sessionTTLMS);
      reservation.timer.unref?.();
      void client.closed.then(() => this.#clientClosed(reservation));
      return {
        schema_version: 1,
        state: "acquired",
        profile_id: this.profileID,
        lane: reservation.lane,
        session_id: reservation.sessionID,
        credential_generation: reservation.credentialGeneration,
        controller_generation: this.controllerGeneration,
        session_generation: reservation.sessionGeneration,
        page_generation: reservation.pageGeneration,
        expires_at: new Date(Date.now() + sessionTTLMS).toISOString(),
      };
    } catch (error) {
      await client?.close().catch(() => {});
      this.#finishReservation(reservation);
      throw normalizeClientError(error);
    } finally {
      input.token = "";
    }
  }

  async release(input) {
    requireExactObject(input, ["session_id", "controller_generation", "session_generation"]);
    const sessionID = parseID(input.session_id, "session_id");
    const controllerGeneration = parseGeneration(input.controller_generation, "controller_generation");
    const sessionGeneration = parseGeneration(input.session_generation, "session_generation");
    if (controllerGeneration !== this.controllerGeneration) {
      throw new ControllerError("browser_controller_stale", "browser controller generation is stale", {
        status: 409,
      });
    }
    const active = this.active;
    if (!active || active.sessionID !== sessionID) {
      throw new ControllerError("browser_session_not_found", "browser session was not found", {
        status: 404,
      });
    }
    if (active.sessionGeneration !== sessionGeneration) {
      throw new ControllerError("browser_session_stale", "browser session generation is stale", {
        status: 409,
      });
    }
    await this.#releaseReservation(active);
    return {
      schema_version: 1,
      state: "released",
      profile_id: this.profileID,
      controller_generation: this.controllerGeneration,
      session_generation: active.sessionGeneration,
    };
  }

  async shutdown() {
    if (this.shuttingDown) return;
    this.shuttingDown = true;
    if (this.active) await this.#releaseReservation(this.active);
  }

  async #reserve(waitMS, lane, taskID) {
    if (this.shuttingDown) {
      throw new ControllerError("browser_controller_stopping", "browser controller is stopping", {
        status: 503,
        retryable: true,
      });
    }
    const deadline = Date.now() + waitMS;
    while (this.active) {
      const remaining = deadline - Date.now();
      if (remaining <= 0) {
        throw new ControllerError("browser_busy", "browser profile is busy", {
          status: 409,
          retryable: true,
        });
      }
      await waitForDone(this.active.done, remaining);
    }
    if (this.shuttingDown) {
      throw new ControllerError("browser_controller_stopping", "browser controller is stopping", {
        status: 503,
        retryable: true,
      });
    }
    const done = deferred();
    const reservation = {
      sessionID: `session_${crypto.randomUUID().replaceAll("-", "")}`,
      sessionGeneration: ++this.sessionGeneration,
      pageGeneration: 0,
      credentialGeneration: 0,
      lane,
      taskID,
      client: null,
      timer: null,
      done,
      cleanupPromise: null,
    };
    this.active = reservation;
    return reservation;
  }

  async #releaseReservation(reservation) {
    if (reservation.cleanupPromise) return reservation.cleanupPromise;
    reservation.cleanupPromise = (async () => {
      if (reservation.timer) clearTimeout(reservation.timer);
      try {
        await reservation.client?.closeTaskPage();
      } catch {
        // The MCP process is still terminated below and the lease is released.
      }
      await reservation.client?.close().catch(() => {});
      this.#finishReservation(reservation);
    })();
    return reservation.cleanupPromise;
  }

  #finishReservation(reservation) {
    if (reservation.timer) clearTimeout(reservation.timer);
    if (this.active === reservation) this.active = null;
    reservation.done.resolve();
  }

  async #clientClosed(reservation) {
    if (this.active !== reservation) return;
    await this.#releaseReservation(reservation);
  }
}

function normalizeClientError(error) {
  if (error instanceof ControllerError) return error;
  return new ControllerError("browser_extension_unavailable", "browser extension is unavailable", {
    status: 503,
    retryable: true,
    cause: error,
  });
}

function randomGeneration() {
  const bytes = crypto.randomBytes(6);
  return bytes.readUIntBE(0, 6) + 1;
}

function deferred() {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

async function waitForDone(done, timeoutMS) {
  let timer;
  const timedOut = new Promise((_, reject) => {
    timer = setTimeout(() => {
      reject(new ControllerError("browser_busy", "browser profile is busy", {
        status: 409,
        retryable: true,
      }));
    }, timeoutMS);
    timer.unref?.();
  });
  try {
    await Promise.race([done.promise, timedOut]);
  } finally {
    clearTimeout(timer);
  }
}
