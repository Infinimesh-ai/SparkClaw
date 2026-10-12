import fs from "node:fs/promises";
import path from "node:path";
import { parseBackendDescriptor } from "./local-backend.mjs";
import { parseStrictJSON } from "./strict-json.mjs";

export const ISCP_ORIGIN = "https://iscp.invalid";

// Only the main process reads this private profile. Renderer status contains its
// public descriptor, never file locations, enrollment tokens, grants or keys.
export async function loadISCPProfile(filename) {
  const profile = await privateJSON(filename);
  const nodeManaged = profile?.schema_version === 2 && profile.mode === "node-managed";
  const valid = nodeManaged
    ? Object.keys(profile).sort().join(",") === "backend,helper_config,mode,schema_version,transport" && profile.transport === "iscp"
    : profile && Object.keys(profile).sort().join(",") === "backend,helper_config,schema_version,test_mode,transport" && profile.schema_version === 1 && profile.transport === "iscp" && profile.test_mode === true;
  if (!valid) throw new Error("ISCP profile is invalid");
  const descriptor = parseBackendDescriptor(profile.backend);
  if (descriptor.transport !== "iscp" || nodeManaged !== (descriptor.mode === "node-managed")) throw new Error("ISCP profile mode is invalid");
  if (!path.isAbsolute(profile.helper_config || "")) throw new Error("ISCP helper configuration path must be absolute");
  const helper = await privateJSON(profile.helper_config);
  if (helper?.schema_version !== (nodeManaged ? 2 : 1) || helper.mode !== (nodeManaged ? "node-managed" : "local-test") || helper.role !== "initiator" ||
      (helper.relay_profile ?? "production") !== descriptor.relayProfile ||
      helper.binding?.deployment_id !== descriptor.deploymentID || helper.binding?.owner_id !== descriptor.ownerID || helper.binding?.client_id !== descriptor.clientID) {
    throw new Error("ISCP helper configuration does not bind this workbench identity");
  }
  return Object.freeze({ descriptor, publicDescriptor: Object.freeze(profile.backend), configPath: filename, helperConfigPath: profile.helper_config });
}

async function privateJSON(filename) {
  if (!path.isAbsolute(filename || "")) throw new Error("ISCP configuration path must be absolute");
  const [info, real, parent] = await Promise.all([fs.lstat(filename), fs.realpath(filename), fs.stat(path.dirname(filename))]);
  const uid = process.getuid?.();
  if (!info.isFile() || info.isSymbolicLink() || real !== path.resolve(filename) || (info.mode & 0o777) !== 0o600 ||
      !parent.isDirectory() || (parent.mode & 0o777) !== 0o700 || (uid !== undefined && (info.uid !== uid || parent.uid !== uid)) || info.size > 65536) {
    throw new Error("ISCP configuration must be a private regular file");
  }
  try { return parseStrictJSON(await fs.readFile(filename, "utf8")); } catch { throw new Error("ISCP configuration is invalid"); }
}
