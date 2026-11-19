import type { SparkClawDesktop } from "./types";

export function desktopCapability(): SparkClawDesktop | null {
  if (typeof window === "undefined") return null;
  const capability = window.sparkclawDesktop;
  return capability?.runtimeKind === "electron" && capability.capabilityVersion === 1 ? capability : null;
}

export function desktopGatewayBase() {
  return desktopCapability()?.gatewayBase ?? "";
}

export function desktopSpeechBase() {
  return desktopCapability()?.speechBase ?? "";
}

// Whether the backend is reached through operations whose availability the
// main process reports per surface: the link layer, or the previous ISCP
// helper that only the qualification launcher still configures.
export function operationBackend(connection: import("./types").DesktopConnectionStatus | undefined) {
  const transport = connection?.backend?.transport;
  return transport === "link" || transport === "iscp";
}

export function surfaceEnabled(connection: import("./types").DesktopConnectionStatus | undefined, surface: string) {
  if (!connection) return false;
  if (!operationBackend(connection)) return true;
  return connection.capabilities?.surfaces?.[surface]?.enabled === true;
}
