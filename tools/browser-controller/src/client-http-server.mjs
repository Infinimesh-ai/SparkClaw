import operations from "./generic-operations.json" with { type: "json" };
import { ControllerError, asControllerError, publicError } from "./errors.mjs";
import { parseOperationArguments, requireExactObject } from "./protocol.mjs";
import { startControllerUnixServer, readJSON, rejectQuery, writeJSON } from "./http-primitives.mjs";

export function validateClientOperation(operation, args) {
  if (!Object.hasOwn(operations, operation) || !operations[operation].client) {
    throw new ControllerError("browser_operation_unavailable", "browser operation is unavailable", { status: 400 });
  }
  const spec = operations[operation];
  parseOperationArguments(args);
  requireExactObject(args, spec.required, spec.optional);
  if (operation === "page.wait" && args.duration_ms !== undefined &&
      (!Number.isSafeInteger(args.duration_ms) || args.duration_ms < 0 || args.duration_ms > 15000)) {
    throw new ControllerError("invalid_request", "wait exceeds client deadline", { status: 400 });
  }
}

export function createClientRequestHandler(controller) {
  return async (request, response) => {
    try {
      const url = new URL(request.url ?? "/", "http://browser-controller.local");
      rejectQuery(url);
      if (request.method === "GET" && url.pathname === "/v1/health") {
        return writeJSON(response, 200, controller.health());
      }
      if (request.method === "POST" && ["/v1/acquire", "/v1/execute", "/v1/release"].includes(url.pathname)) {
        const input = await readJSON(request);
        if (url.pathname === "/v1/execute") validateClientOperation(input.operation, input.arguments);
        const method = url.pathname.slice(4);
        return writeJSON(response, method === "acquire" ? 201 : 200, await controller[method](input));
      }
      return writeJSON(response, 404, { code: "not_found", retryable: false });
    } catch (error) {
      const safe = asControllerError(error);
      return writeJSON(response, safe.status, publicError(safe));
    }
  };
}

export function startClientUnixServer({ socketPath, controller }) {
  return startControllerUnixServer({ socketPath, controller, requestHandler: createClientRequestHandler(controller) });
}
