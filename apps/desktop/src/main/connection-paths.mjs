import fs from "node:fs/promises";
import path from "node:path";

export function desktopConnectionConfigPath({ env = process.env, home } = {}) {
  const configHome = env.XDG_CONFIG_HOME || path.join(home, ".config");
  if (!path.isAbsolute(configHome)) throw new Error("Desktop config directory must be absolute");
  return path.join(configHome, "sparkclaw", "desktop-connection.json");
}

export async function resolveDesktopConnectionPaths({ env = process.env, home, packaged, moduleDirectory }) {
  const descriptorPath = env.SPARKCLAW_DESKTOP_CONNECTION_FILE?.trim();
  const credentialPath = env.SPARKCLAW_DESKTOP_CREDENTIAL_FILE?.trim();
  if (Boolean(descriptorPath) !== Boolean(credentialPath)) {
    throw new Error("Both desktop local-backend paths must be configured together");
  }
  if (descriptorPath && credentialPath) return validatePaths({ descriptorPath, credentialPath });

  if (!packaged) {
    const runtimeDirectory = path.resolve(moduleDirectory, "../../../../data/runtime");
    return pathsInRuntime(runtimeDirectory);
  }

  const configPath = desktopConnectionConfigPath({ env, home });
  try {
    const config = JSON.parse(await readPrivateConfig(configPath));
    if (config?.schema_version !== 1 || Object.keys(config).sort().join(",") !==
        "credential_path,descriptor_path,schema_version") {
      throw new Error("Desktop connection configuration is invalid");
    }
    return validatePaths({ descriptorPath: config.descriptor_path, credentialPath: config.credential_path });
  } catch (error) {
    if (error?.code !== "ENOENT") throw error;
    throw new Error("Desktop local-backend paths are not configured");
  }
}

export function pathsInRuntime(runtimeDirectory) {
  if (!path.isAbsolute(runtimeDirectory)) throw new Error("Desktop runtime directory must be absolute");
  return {
    descriptorPath: path.join(runtimeDirectory, "local-workbench.json"),
    credentialPath: path.join(runtimeDirectory, "desktop-client.json"),
  };
}

function validatePaths(paths) {
  for (const value of Object.values(paths)) {
    if (typeof value !== "string" || !path.isAbsolute(value) || /[\r\n\0]/u.test(value)) {
      throw new Error("Desktop local-backend path is invalid");
    }
  }
  return paths;
}

async function readPrivateConfig(filename) {
  const [file, directory, real] = await Promise.all([
    fs.lstat(filename), fs.lstat(path.dirname(filename)), fs.realpath(filename),
  ]);
  if (!file.isFile() || file.isSymbolicLink() || real !== filename ||
      (file.mode & 0o777) !== 0o600 || file.uid !== process.getuid() ||
      !directory.isDirectory() || directory.isSymbolicLink() ||
      (directory.mode & 0o777) !== 0o700 || directory.uid !== process.getuid()) {
    throw new Error("Desktop connection configuration is not owner-only");
  }
  const content = await fs.readFile(filename, "utf8");
  if (Buffer.byteLength(content) > 64 << 10) throw new Error("Desktop connection configuration is too large");
  return content;
}
