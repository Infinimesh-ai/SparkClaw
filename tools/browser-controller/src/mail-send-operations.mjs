import fs from 'node:fs';
import path from 'node:path';
import {randomUUID} from 'node:crypto';
import {privateDirectory, privateRead} from '../mail-runtime/src/authorization.mjs';
import {decode, digest, encode, requireCondition} from '../mail-runtime/src/protocol.mjs';
import {attachmentMessageFingerprint, validateAttachments} from '../mail-runtime/applications/mail/lib/workspace-attachments.mjs';

// Only sends need a durable admission tombstone. Polls, reads and watches never
// accumulate task rows. These small records have no count/capacity quota and
// contain neither message text nor browser credentials.
export class MailSendOperations {
  constructor(directory) {this.directory = path.join(directory, 'send-operations'); privateDirectory(this.directory);}
  key(owner, profile, request) {return digest({owner, profile, provider: request.provider, command: 'send', task: request.taskID});}
  intent(input, mode = input.mode) {
    validateAttachments(input.message.attachments, 'reconcile');
    const value = {...input, message: attachmentMessageFingerprint(input.message)};
    if (mode == null) delete value.mode; else value.mode = mode;
    return digest(value);
  }
  read(key, input) {
    let saved;
    try {saved = decode(privateRead(this.file(key)));} catch (error) {if (error.code === 'ENOENT') return null; throw error;}
    requireCondition(saved.version === 1 && saved.key === key && saved.evidence &&
      /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/u.test(saved.evidence.task_id) &&
      ['intent_digest', 'resource_digest', 'binding_digest'].every(name => /^[a-f0-9]{64}$/u.test(saved.evidence[name])) &&
      Number.isSafeInteger(saved.evidence.ledger_epoch) && saved.evidence.ledger_epoch > 0 &&
      typeof saved.effect === 'boolean' && (saved.outcome === null || saved.outcome?.id === saved.evidence.task_id &&
      ['completed', 'uncertain', 'blocked', 'failed', 'cancelled', 'waiting_confirmation'].includes(saved.outcome.status)), 'SEND_OUTCOME_UNKNOWN');
    requireCondition(saved.intent === this.intent(input, input.mode === 'reconcile' ? saved.mode : input.mode), 'REQUEST_KEY_CONFLICT');
    return saved;
  }
  file(key) {return path.join(this.directory, key + '.json');}
  create(key, input, evidence, {mode = input.mode, effect = false, outcome = null, deadline = null} = {}) {
    const saved = {version: 1, key, intent: this.intent(input, mode), mode: mode ?? null, evidence, effect, outcome, deadline_ms: deadline};
    if (!this.write(saved, true)) return {saved: this.read(key, input), fresh: false};
    return {saved, fresh: true};
  }
  write(saved, create = false) {
    const file = this.file(saved.key), temporary = file + '.' + randomUUID() + '.tmp';
    const fd = fs.openSync(temporary, 'wx', 0o600);
    try {fs.writeFileSync(fd, encode(saved, 65536)); fs.fsyncSync(fd);} finally {fs.closeSync(fd);}
    try {
      if (create) fs.linkSync(temporary, file); else fs.renameSync(temporary, file);
      const directory = fs.openSync(this.directory, 'r');
      try {fs.fsyncSync(directory);} finally {fs.closeSync(directory);}
      return true;
    } catch (error) {if (create && error.code === 'EEXIST') return false; throw error;}
    finally {try {fs.unlinkSync(temporary);} catch (error) {if (error.code !== 'ENOENT') throw error;}}
  }
  beforeEffect(saved) {if (!saved.effect) {saved.effect = true; this.write(saved);}}
  finish(saved, outcome) {saved.outcome = outcome; this.write(saved); return outcome;}
}
