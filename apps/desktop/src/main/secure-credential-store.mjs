import fs from "node:fs/promises";
import path from "node:path";
import crypto from "node:crypto";

// Electron safeStorage delegates encryption to Keychain on macOS. Linux's
// basic_text backend is deliberately unavailable for this credential vault.
export class SecureCredentialStore {
  constructor({ directory, safeStorage, platform = process.platform, uid = process.getuid?.() }) {
    this.directory = directory;
    this.filename = path.join(directory, "credential.bin");
    this.safeStorage = safeStorage;
    this.platform = platform;
    this.uid = uid;
  }

  available() {
    return this.safeStorage.isEncryptionAvailable() && (this.platform !== "linux" ||
      !["basic_text", "unknown"].includes(this.safeStorage.getSelectedStorageBackend()));
  }

  async load() {
    if (!this.available()) throw new Error("Secure credential storage is unavailable");
    try {
      await this.#directory();
      const info = await fs.lstat(this.filename);
      if (!info.isFile() || info.isSymbolicLink() || (info.mode & 0o777) !== 0o600 ||
          (this.uid !== undefined && info.uid !== this.uid) || info.size > 65536) throw new Error("Credential vault is invalid");
      const record = JSON.parse(this.safeStorage.decryptString(await fs.readFile(this.filename)));
      const validCredential = record?.transport === "iscp" ? record.schema_version === 2 && record.authorization === undefined &&
        path.isAbsolute(record.configPath || "") && record.testMode === true : record?.schema_version === 1 &&
        typeof record.authorization === "string" && record.authorization.startsWith("Bearer ");
      if (!validCredential || typeof record.binding !== "string" ||
          ![record.clientID, record.ownerID, record.deploymentID].every((value) => typeof value === "string" && value.length > 0 && value.length <= 160)) {
        throw new Error("Credential vault is invalid");
      }
      return record;
    } catch (error) {
      if (error?.code === "ENOENT") return undefined;
      // Do not echo decrypt/JSON errors, which could contain sensitive input.
      throw new Error("Saved credential could not be unlocked; sign in again");
    }
  }

  async save(record) {
    if (!this.available()) throw new Error("Secure credential storage is unavailable");
    await this.#directory();
    const encrypted = this.safeStorage.encryptString(JSON.stringify({ ...record, schema_version: record.transport === "iscp" ? 2 : 1 }));
    const temporary = path.join(this.directory, `credential-${crypto.randomUUID()}.tmp`);
    try {
      const file = await fs.open(temporary, "wx", 0o600);
      try { await file.writeFile(encrypted); await file.sync(); } finally { await file.close(); }
      await fs.rename(temporary, this.filename);
    } finally { await fs.rm(temporary, { force: true }); }
  }

  async clear() { await fs.rm(this.filename, { force: true }); }

  async #directory() {
    await fs.mkdir(this.directory, { mode: 0o700, recursive: true });
    const [info, real] = await Promise.all([fs.lstat(this.directory), fs.realpath(this.directory)]);
    if (!info.isDirectory() || info.isSymbolicLink() || real !== path.resolve(this.directory) ||
        (info.mode & 0o777) !== 0o700 || (this.uid !== undefined && info.uid !== this.uid)) {
      throw new Error("Credential vault directory is not private");
    }
  }
}
