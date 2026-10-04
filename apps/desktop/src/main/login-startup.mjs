import fs from "node:fs/promises";
import path from "node:path";

export async function linuxLoginStartup({ home, executable, enabled }) {
  const autostartPath = path.join(home, ".config", "autostart", "sparkclaw.desktop");
  if (enabled === true) {
    if (!path.isAbsolute(executable) || /[\r\n]/.test(executable)) throw new Error("Invalid desktop executable path");
    const command = executable.replaceAll("\\", "\\\\").replaceAll('"', '\\"').replaceAll("$", "\\$").replaceAll("`", "\\`").replaceAll("%", "%%");
    await fs.mkdir(path.dirname(autostartPath), { recursive: true });
    await fs.writeFile(autostartPath, `[Desktop Entry]\nType=Application\nName=SparkX\nExec="${command}"\nTerminal=false\nX-GNOME-Autostart-enabled=true\n`, { mode: 0o600 });
    await fs.chmod(autostartPath, 0o600);
  } else if (enabled === false) {
    await fs.rm(autostartPath, { force: true });
  }
  return { supported: true, enabled: await fs.access(autostartPath).then(() => true, () => false) };
}
