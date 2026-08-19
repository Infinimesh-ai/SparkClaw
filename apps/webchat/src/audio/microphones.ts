export const MICROPHONE_STORAGE_KEY = "sparkclaw.voice.microphone";

export type MicrophoneDevice = {
  deviceId: string;
  label: string;
};

export async function enumerateMicrophones(): Promise<MicrophoneDevice[]> {
  if (!navigator.mediaDevices?.enumerateDevices) return [];
  const devices = await navigator.mediaDevices.enumerateDevices();
  let unnamed = 0;
  return devices
    .filter((device) => device.kind === "audioinput" && device.deviceId !== "default")
    .map((device) => ({
      deviceId: device.deviceId,
      label: device.label.trim() || `Microphone ${++unnamed}`
    }));
}

export function loadPreferredMicrophone() {
  try {
    return window.localStorage.getItem(MICROPHONE_STORAGE_KEY) ?? "";
  } catch {
    return "";
  }
}

export function savePreferredMicrophone(deviceId: string) {
  try {
    if (deviceId) window.localStorage.setItem(MICROPHONE_STORAGE_KEY, deviceId);
    else window.localStorage.removeItem(MICROPHONE_STORAGE_KEY);
  } catch {
    // A browser may deny storage while still allowing microphone use.
  }
}

export function isUnavailableMicrophoneError(error: unknown) {
  return error instanceof DOMException && (
    error.name === "NotFoundError" || error.name === "OverconstrainedError"
  );
}
