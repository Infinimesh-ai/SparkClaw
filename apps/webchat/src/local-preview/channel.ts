export const VIEWER_ORIGIN = 'sparkclaw-preview://viewer';
export const WORKBENCH_ORIGIN = 'sparkclaw-app://workbench';
export const MAX_READ_BYTES = 256 * 1024;
export type PreviewFormat = 'pdf' | 'image' | 'text' | 'markdown' | 'spreadsheet' | 'unsupported';
export type ReadingPosition = { page?: number; zoom?: number; scrollTop?: number; scrollLeft?: number; sheet?: string; offset?: number };
export type PreviewDocument = { id: string; generation: number; loginGeneration: string; name: string; size: number; format: PreviewFormat; language: 'en' | 'zh'; position?: ReadingPosition; warnings?: string[] };
export type PreviewRequest = { type: 'request'; id: number; documentId: string; generation: number; loginGeneration: string; operation: 'read' | 'model' | 'image'; offset?: number; length?: number; relativePath?: string };
export type PreviewResponse = { type: 'response'; id: number; documentId: string; generation: number; loginGeneration: string; ok: boolean; value?: unknown; error?: { code?: string; message?: string } };
export type SpreadsheetCell = { row: number; column: number; value: string | number | boolean | null; formula?: string; missingCachedValue?: boolean; style?: { bold?: boolean; italic?: boolean; color?: string; background?: string; fontSize?: number; align?: string; numberFormat?: string } };
export type SpreadsheetModel = { kind: 'spreadsheet'; sheets: { id: string; name: string; rowCount: number; columnCount: number; cells: SpreadsheetCell[]; merges: { row: number; column: number; rowSpan: number; columnSpan: number }[] }[]; warnings: string[] };
export function validDocument(value: unknown): value is PreviewDocument {
  const d = value as PreviewDocument;
  return !!d && typeof d === 'object' && typeof d.id === 'string' && d.id.length > 0 && d.id.length <= 160 && Number.isSafeInteger(d.generation) && d.generation >= 0 && typeof d.loginGeneration === 'string' && d.loginGeneration.length > 0 && d.loginGeneration.length <= 160 && typeof d.name === 'string' && d.name.length <= 4096 && Number.isSafeInteger(d.size) && d.size >= 0 && ['pdf','image','text','markdown','spreadsheet','unsupported'].includes(d.format) && ['en','zh'].includes(d.language)
    && (d.warnings === undefined || Array.isArray(d.warnings) && d.warnings.length <= 6 && d.warnings.every(value => typeof value === 'string' && value.length <= 512));
}
export class DocumentChannel {
  private next = 0;
  private closed = false;
  private pending = new Map<number, { resolve: (value: unknown) => void; reject: (error: Error) => void; timer: ReturnType<typeof setTimeout> }>();
  constructor(readonly port: MessagePort, readonly document: PreviewDocument, private readonly revoked: () => void = () => {}) {
    port.onmessage = (event: MessageEvent<PreviewResponse | { type: 'revoke' }>) => {
      const message = event.data;
      if (message?.type === 'revoke') { this.close(); this.revoked(); return; }
      if (message?.type !== 'response' || message.documentId !== document.id || message.generation !== document.generation || message.loginGeneration !== document.loginGeneration) return;
      const request = this.pending.get(message.id); if (!request) return;
      clearTimeout(request.timer); this.pending.delete(message.id);
      if (message.ok) request.resolve(message.value); else request.reject(new Error(message.error?.message || message.error?.code || 'Preview unavailable'));
    };
    port.onmessageerror = () => { this.close(); this.revoked(); };
    port.start();
  }
  request(operation: PreviewRequest['operation'], params: Pick<PreviewRequest, 'offset'|'length'|'relativePath'> = {}): Promise<unknown> {
    if (this.closed) return Promise.reject(new Error('Preview closed'));
    if (this.pending.size >= 4) return Promise.reject(new Error('Too many preview reads'));
    const id = ++this.next;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => { this.pending.delete(id); reject(new Error('Preview request timed out')); }, operation === 'model' ? 120_000 : 15_000);
      this.pending.set(id, { resolve, reject, timer });
      this.port.postMessage({ type: 'request', id, documentId: this.document.id, generation: this.document.generation, loginGeneration: this.document.loginGeneration, operation, ...params } satisfies PreviewRequest);
    });
  }
  async read(offset: number, length: number): Promise<Uint8Array> {
    if (!Number.isSafeInteger(offset) || !Number.isSafeInteger(length) || offset < 0 || length < 0 || length > MAX_READ_BYTES || offset + length > this.document.size) throw new Error('Invalid preview range');
    const value = await this.request('read', { offset, length }) as { bytes?: Uint8Array };
    if (!(value?.bytes instanceof Uint8Array) || value.bytes.byteLength !== length) throw new Error('Incomplete preview read');
    return value.bytes;
  }
  async all(limit: number): Promise<Uint8Array> {
    if (this.document.size > limit) throw new Error('This document exceeds the preview size limit');
    const result = new Uint8Array(this.document.size);
    for (let offset = 0; offset < result.length; offset += MAX_READ_BYTES) result.set(await this.read(offset, Math.min(MAX_READ_BYTES, result.length-offset)), offset);
    return result;
  }
  position(position: ReadingPosition) {
    if (!this.closed) this.port.postMessage({ type: 'position', documentId: this.document.id, generation: this.document.generation, loginGeneration: this.document.loginGeneration, position });
  }
  escape() { if(!this.closed)this.port.postMessage({type:'escape',documentId:this.document.id,generation:this.document.generation,loginGeneration:this.document.loginGeneration}); }
  close() {
    if (this.closed) return; this.closed = true;
    for (const request of this.pending.values()) { clearTimeout(request.timer); request.reject(new Error('Preview closed')); }
    this.pending.clear(); this.port.onmessage = null; this.port.onmessageerror = null; this.port.close();
  }
}
