import { PlaywrightMCPClientFactory as CoreFactory, PlaywrightMCPClient as CoreClient } from "./mcp-client-core.mjs";
import { backendOperationHandlers } from "./mcp-backend-operations.mjs";

export class PlaywrightMCPClientFactory extends CoreFactory {
  constructor(options = {}) { super({ ...options, operationHandlers: backendOperationHandlers }); }
}
export class PlaywrightMCPClient extends CoreClient {
  constructor(child, rpc, outputDir, options = {}) {
    super(child, rpc, outputDir, { ...options, operationHandlers: backendOperationHandlers });
  }
}
