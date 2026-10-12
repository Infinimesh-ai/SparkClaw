import fs from "node:fs";
import path from "node:path";
import { parseBackendDescriptor } from "./local-backend.mjs";
import crypto from "node:crypto";
import { parseStrictJSON } from "./strict-json.mjs";

export const ISCP_LAUNCH_PROFILE_NAME = "iscp-launch-profile.json";

// Startup resolves userData synchronously before Electron becomes ready. This
// owner-only file records an explicit choice; it never contains credentials and
// is never exposed to the renderer. Qualifications do not read this choice.
export function resolveISCPLaunchProfile({ defaultUserData, env = process.env, qualification = false, uid = process.getuid?.() }) {
  absolutePath(defaultUserData);
  const explicitEnvironment = ["SPARKCLAW_DESKTOP_ISCP_CONFIG", "SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST", "SPARKCLAW_DESKTOP_USER_DATA_DIR"]
    .some((key) => Object.hasOwn(env, key));
  if (qualification || explicitEnvironment) {
    const configuredUserData = env.SPARKCLAW_DESKTOP_USER_DATA_DIR;
    const iscpProfilePath = env.SPARKCLAW_DESKTOP_ISCP_CONFIG;
    if (configuredUserData !== undefined) absolutePath(configuredUserData);
    if (iscpProfilePath !== undefined) {
      if (qualification || env.SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST !== "1") throw new Error("ISCP requires an explicitly selected test profile outside qualification");
      validateSelection(iscpProfilePath, configuredUserData, defaultUserData, uid);
      return Object.freeze({ configuredUserData, iscpProfilePath, allowLocalISCPTest: true, source: "environment" });
    }
    return Object.freeze({ configuredUserData, allowLocalISCPTest: false, source: "environment" });
  }
  const filename = path.join(defaultUserData, ISCP_LAUNCH_PROFILE_NAME);
  let selection;
  try { selection = readPrivateJSON(filename, uid); }
  catch (error) { if (error.code === "ENOENT") return Object.freeze({ allowLocalISCPTest: false, source: "default" }); throw error; }
  const nodeManaged = selection?.schema_version === 2 && selection.mode === "node-managed";
  const valid = nodeManaged
    ? Object.keys(selection).sort().join(",") === "mode,profile_path,schema_version,user_data_directory"
    : selection && Object.keys(selection).sort().join(",") === "profile_path,schema_version,test_mode,user_data_directory" && selection.schema_version === 1 && selection.test_mode === true;
  if (!valid) throw new Error("ISCP launch selection is invalid; remove iscp-launch-profile.json to restore the default connection");
  validateSelection(selection.profile_path, selection.user_data_directory, defaultUserData, uid, nodeManaged);
  return Object.freeze({ configuredUserData: selection.user_data_directory, iscpProfilePath: selection.profile_path,
    allowLocalISCPTest: !nodeManaged, ...(nodeManaged ? { allowNodeManagedISCP: true } : {}), source: "persisted" });
}

// Commit only a helper-verified node profile. Atomic rename keeps the old choice
// usable if persistence fails; the selection carries no credentials or keys.
export function persistNodeISCPLaunchProfile({ filename, profilePath, userDataDirectory, uid = process.getuid?.() }) {
  absolutePath(filename); privateDirectory(path.dirname(filename), uid);
  validateSelection(profilePath, userDataDirectory, userDataDirectory, uid, true);
  const selection = { schema_version: 2, mode: "node-managed", profile_path: profilePath, user_data_directory: userDataDirectory };
  const temporary = `${filename}.${crypto.randomUUID()}.tmp`;
  const file = fs.openSync(temporary, "wx", 0o600);
  try { fs.writeFileSync(file, JSON.stringify(selection)); fs.fsyncSync(file); } finally { fs.closeSync(file); }
  try { fs.renameSync(temporary, filename); }
  finally { fs.rmSync(temporary, { force: true }); }
}

function validateSelection(profilePath, userData, defaultUserData, uid, nodeManaged = false) {
  absolutePath(profilePath); absolutePath(userData);
  if (!nodeManaged && path.resolve(userData) === path.resolve(defaultUserData)) throw new Error("ISCP launch requires a dedicated user-data directory");
  privateDirectory(userData, uid);
  const profile = readPrivateJSON(profilePath, uid);
  const valid = nodeManaged
    ? profile?.schema_version === 2 && profile.mode === "node-managed" && profile.transport === "iscp" && Object.keys(profile).sort().join(",") === "backend,helper_config,mode,schema_version,transport" && parseBackendDescriptor(profile.backend).mode === "node-managed"
    : profile && Object.keys(profile).sort().join(",") === "backend,helper_config,schema_version,test_mode,transport" && profile.schema_version === 1 && profile.transport === "iscp" && profile.test_mode === true && parseBackendDescriptor(profile.backend).schemaVersion === 3;
  if (!valid) throw new Error("ISCP selected profile is invalid");
  absolutePath(profile.helper_config);
  // The ordinary DesktopAuth validates the helper's identity/binding before it
  // publishes connected. Validate its private storage boundary at startup too.
  readPrivateJSON(profile.helper_config, uid);
}

function absolutePath(value) {
  if (typeof value !== "string" || value !== value.trim() || !path.isAbsolute(value) || /[\r\n\0]/u.test(value)) throw new Error("ISCP launch paths must be absolute and contain no control characters");
}

function privateDirectory(directory, uid) {
  const info = fs.lstatSync(directory);
  if (!info.isDirectory() || info.isSymbolicLink() || fs.realpathSync(directory) !== path.resolve(directory) ||
      (info.mode & 0o777) !== 0o700 || (uid !== undefined && info.uid !== uid)) throw new Error("ISCP launch directory must be owner-only with mode 0700 and no symbolic links");
}

function readPrivateJSON(filename, uid) {
  const file = fs.openSync(filename, fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW);
  try {
    const info = fs.fstatSync(file);
    if (!info.isFile() || fs.realpathSync(filename) !== path.resolve(filename) || (info.mode & 0o777) !== 0o600 ||
        (uid !== undefined && info.uid !== uid) || info.size > 65536) throw new Error("ISCP launch file must be owner-only with mode 0600 and no symbolic links");
    privateDirectory(path.dirname(filename), uid);
    try {
      return parseStrictJSON(fs.readFileSync(file, "utf8"));
    }
    catch { throw new Error("ISCP launch file contains invalid JSON"); }
  } finally { fs.closeSync(file); }
}
