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

export function surfaceEnabled(connection: import("./types").DesktopConnectionStatus | undefined, surface: string) {
  if (!connection) return false;
  if (connection.backend?.transport !== "iscp") return true;
  return connection.capabilities?.surfaces?.[surface]?.enabled === true;
}
