import fs from "node:fs";
import path from "node:path";
import { parseStrictJSON } from "./strict-json.mjs";

function absolutePath(value) {
  if (typeof value !== "string" || value !== value.trim() || !path.isAbsolute(value) || /[\u0000-\u001f\u007f]/u.test(value)) {
    throw new Error("Desktop launch paths must be absolute and contain no control characters");
  }
}

function privateDirectory(directory, uid) {
  const stat = fs.lstatSync(directory);
  if (!stat.isDirectory() || fs.realpathSync(directory) !== path.resolve(directory) ||
      (stat.mode & 0o777) !== 0o700 || stat.uid !== uid) {
    throw new Error("Desktop user-data selection requires owner-only directories without symbolic links");
  }
}

// Before the first Electron lock, honor the directory recorded by an earlier
// install. Only the selector is opened. The old connection profile is unused.
export function resolveDesktopUserData({ defaultUserData, qualification = false, env = process.env, uid = process.getuid?.() }) {
  absolutePath(defaultUserData);
  const override = env.SPARKCLAW_DESKTOP_USER_DATA_DIR;
  if (override !== undefined) absolutePath(override);
  if (qualification) {
    if (!override || path.resolve(override) === path.resolve(defaultUserData)) throw new Error("Qualification requires a separate user-data directory");
    return override;
  }

  const root = override ?? defaultUserData;
  const filename = path.join(root, "iscp-launch-profile.json");
  let file;
  try {
    file = fs.openSync(filename, fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW | fs.constants.O_NONBLOCK);
  } catch (error) {
    if (error.code === "ENOENT") return override;
    throw error;
  }
  let selection;
  try {
    privateDirectory(root, uid);
    const stat = fs.fstatSync(file);
    if (!stat.isFile() || stat.uid !== uid || (stat.mode & 0o777) !== 0o600 || stat.size > 65536) {
      throw new Error("Desktop user-data selector must be an owner-only file of at most 64 KiB");
    }
    try {
      selection = parseStrictJSON(fs.readFileSync(file, "utf8"));
    } catch {
      throw new Error("Desktop user-data selector contains invalid JSON");
    }
  } finally {
    fs.closeSync(file);
  }

  const keys = Object.keys(selection ?? {}).sort().join(",");
  const managed = selection?.schema_version === 2 && selection.mode === "node-managed" &&
    keys === "mode,profile_path,schema_version,user_data_directory";
  const dedicated = selection?.schema_version === 1 && selection.test_mode === true &&
    keys === "profile_path,schema_version,test_mode,user_data_directory";
  if (!managed && (!dedicated || override !== undefined)) throw new Error("Invalid desktop user-data selection");
  absolutePath(selection.profile_path);
  const directory = selection.user_data_directory;
  absolutePath(directory);
  if (dedicated && path.resolve(directory) === path.resolve(root)) throw new Error("A test profile requires a dedicated user-data directory");
  privateDirectory(directory, uid);
  return directory;
}
