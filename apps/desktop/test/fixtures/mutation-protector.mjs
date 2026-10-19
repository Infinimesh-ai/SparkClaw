import crypto from 'node:crypto';
import { SecureCredentialStore } from '../../src/main/secure-credential-store.mjs';
// Exercise the real vault envelope with a deterministic per-test-process native
// crypto substitute, so reopening the same journal can decrypt its records.
const key = crypto.randomBytes(32);
export const mutationProtector = new SecureCredentialStore({ directory: '/unused', platform: 'darwin', safeStorage: {
  isEncryptionAvailable: () => true,
  encryptString(value) {
    const nonce = crypto.randomBytes(12), cipher = crypto.createCipheriv('aes-256-gcm', key, nonce);
    return Buffer.concat([nonce, cipher.update(value, 'utf8'), cipher.final(), cipher.getAuthTag()]);
  },
  decryptString(value) {
    const cipher = crypto.createDecipheriv('aes-256-gcm', key, value.subarray(0, 12));
    cipher.setAuthTag(value.subarray(-16));
    return Buffer.concat([cipher.update(value.subarray(12, -16)), cipher.final()]).toString('utf8');
  },
} });
