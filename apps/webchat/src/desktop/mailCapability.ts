import type { LocalFile } from "./clientStore";

type Mailbox = { id: string; address: string; provider: string; intake_enabled?: boolean };
type CachedMail = { id: string; subject: string; from: string; summary: string; body_text: string; body_truncated: boolean; attachments: Array<{ id: string; name: string; size: number; available: boolean }> };
type MailCache = { mailbox_id: string; sequence: number; synced_at: string; messages: CachedMail[] };
export type MailCapability = {
  catalog(): Promise<Mailbox[]>;
  refreshCatalog(): Promise<Mailbox[]>;
  read(mailbox: string): Promise<MailCache>;
  sync(mailbox: string): Promise<MailCache>;
  saveAttachment?(mailbox: string, mail: string, part: string, conversation: string): Promise<LocalFile>;
};

declare global { interface Window { sparkclawMailSync?: MailCapability; } }
