import fs from "node:fs";
import path from "node:path";
import { parseBackendDescriptor } from "./local-backend.mjs";

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
  if (!selection || Object.keys(selection).sort().join(",") !== "profile_path,schema_version,test_mode,user_data_directory" ||
      selection.schema_version !== 1 || selection.test_mode !== true) throw new Error("ISCP launch selection is invalid; remove iscp-launch-profile.json to restore the default connection");
  validateSelection(selection.profile_path, selection.user_data_directory, defaultUserData, uid);
  return Object.freeze({ configuredUserData: selection.user_data_directory, iscpProfilePath: selection.profile_path,
    allowLocalISCPTest: true, source: "persisted" });
}

function validateSelection(profilePath, userData, defaultUserData, uid) {
  absolutePath(profilePath); absolutePath(userData);
  if (path.resolve(userData) === path.resolve(defaultUserData)) throw new Error("ISCP launch requires a dedicated user-data directory");
  privateDirectory(userData, uid);
  const profile = readPrivateJSON(profilePath, uid);
  if (!profile || Object.keys(profile).sort().join(",") !== "backend,helper_config,schema_version,test_mode,transport" ||
      profile.schema_version !== 1 || profile.transport !== "iscp" || profile.test_mode !== true ||
      parseBackendDescriptor(profile.backend).transport !== "iscp") throw new Error("ISCP selected profile is invalid");
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
      const raw = fs.readFileSync(file, "utf8"), value = JSON.parse(raw);
      rejectDuplicateKeys(raw);
      return value;
    }
    catch { throw new Error("ISCP launch file contains invalid JSON"); }
  } finally { fs.closeSync(file); }
}

function rejectDuplicateKeys(raw) {
  // JSON.parse first establishes valid syntax. Lex strings as whole tokens so
  // escaped path text cannot be mistaken for an object key.
  const tokens = raw.match(/"(?:\\.|[^"\\])*"|[{}\[\]:]/gu) ?? [], objects = [];
  for (let index = 0; index < tokens.length; index++) {
    const token = tokens[index];
    if (token === "{") objects.push(new Set());
    else if (token === "[") objects.push(null);
    else if (token === "}" || token === "]") objects.pop();
    else if (token.startsWith('"') && tokens[index + 1] === ":") {
      const key = JSON.parse(token), object = objects.at(-1);
      if (object.has(key)) throw new Error("Duplicate JSON key");
      object.add(key);
    }
  }
}
