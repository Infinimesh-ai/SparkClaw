import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";

// The destination comes from Electron's native save dialog, never Renderer IPC.
export async function exportLocalFile({ name, content }, { dialog, window }) {
  const choice = await dialog.showSaveDialog(window, { defaultPath: name });
  if (choice.canceled || !choice.filePath) return { saved: false };
  const destination = choice.filePath;
  const pending = path.join(path.dirname(destination), `.sparkclaw-export-${crypto.randomUUID()}`);
  let handle;
  try {
    handle = await fs.open(pending, "wx", 0o600);
    await handle.writeFile(content);
    await handle.sync();
    await handle.close(); handle = undefined;
    // The dialog asks the user about replacing an existing destination. The
    // atomic rename avoids exposing a partially written export on disk failure.
    await fs.rename(pending, destination);
    const directory = await fs.open(path.dirname(destination), "r");
    try { await directory.sync(); } finally { await directory.close(); }
    return { saved: true };
  } finally {
    await handle?.close();
    await fs.rm(pending, { force: true });
  }
}
