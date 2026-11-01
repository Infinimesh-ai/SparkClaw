import crypto from 'node:crypto';

const ID = /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$/u;
const HASH = /^[a-f0-9]{64}$/u;
const CANDIDATE = /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}#[a-z][a-z0-9_]{0,47}$/u;
const TIME = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/u;
const INVALID_SURROGATE = /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u;
const digest = value => crypto.createHash('sha256').update(value).digest('hex');
const exactFields = (value, fields) => value && typeof value === 'object' && !Array.isArray(value) &&
  Object.keys(value).sort().join(',') === [...fields].sort().join(',');

// Shared contract: no whitespace/HTML escaping, control characters use \u00xx,
// ASCII keys sort by byte order, ordered arrays, and no normalization of text.
export function canonicalContextJSON(value) {
  if (typeof value === 'string') {
    if (INVALID_SURROGATE.test(value)) throw new Error('Invalid Unicode in context');
    return '"' + value.replace(/[\u0000-\u001f"\\]/gu, char => char === '"' ? '\\"' : char === '\\' ? '\\\\' : `\\u${char.charCodeAt(0).toString(16).padStart(4, '0')}`) + '"';
  }
  if (typeof value === 'boolean') return value ? 'true' : 'false';
  if (typeof value === 'number' && Number.isSafeInteger(value) && value >= 0) return String(value);
  if (Array.isArray(value)) return '[' + value.map(canonicalContextJSON).join(',') + ']';
  if (value && typeof value === 'object') {
    const keys = Object.keys(value).sort();
    if (keys.some(key => !/^[a-z][a-z0-9_]*$/u.test(key))) throw new Error('Invalid context key');
    return '{' + keys.map(key => canonicalContextJSON(key) + ':' + canonicalContextJSON(value[key])).join(',') + '}';
  }
  throw new Error('Invalid canonical context value');
}

export function conversationContextDigest(messages, inputFiles = [], sourceKind = 'web') {
  const normalized = messages.map((message, index) => {
    const files = new Map((message.files ?? []).map(file => [file.id, { ...file,
      source: message.role === 'assistant' ? 'conversation_result' : 'conversation_attachment' }]));
    if (index === messages.length - 1 && message.role === 'user') for (const file of inputFiles) {
      const prior = files.get(file.id);
      if (prior && ['name', 'size', 'sha256'].some(key => prior[key] !== file[key])) throw new Error('Conflicting conversation file');
      files.set(file.id, { ...file, source: 'conversation_attachment' });
    }
    return { id: message.id, role: message.role, text_sha256: digest(message.content),
      files: [...files.values()].sort((a, b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0),
      files_unavailable: message.files_unavailable === true };
  });
  return digest(canonicalContextJSON({ source_kind: sourceKind, messages: normalized }));
}

export function validateClarificationRecord(record) {
  const fields = ['schema_version', 'id', 'request_id', 'source_message_id', 'source_text_sha256', 'context_sha256',
    'prompt_sha256', 'issued_at', 'expires_at', 'graph_revision', 'catalog_revision', 'calibration_revision', 'options', 'mac'];
  if (!exactFields(record, fields) || record.schema_version !== 2 ||
      ![record.id, record.request_id, record.source_message_id, record.graph_revision, record.catalog_revision, record.calibration_revision].every(value => typeof value === 'string' && ID.test(value)) ||
      ![record.mac, record.source_text_sha256, record.context_sha256, record.prompt_sha256].every(value => typeof value === 'string' && HASH.test(value)) ||
      ![record.issued_at, record.expires_at].every(value => typeof value === 'string' && TIME.test(value) && Number.isFinite(Date.parse(value)) && new Date(value).toISOString() === value.replace('Z', '.000Z')) ||
      Date.parse(record.expires_at) <= Date.parse(record.issued_at) || Date.parse(record.expires_at) - Date.parse(record.issued_at) > 24 * 60 * 60 * 1000 ||
      !Array.isArray(record.options) || record.options.length < 1 || record.options.length > 2) throw new Error('Invalid clarification record');
  record.options.forEach((option, index) => {
    if (!option || option.n !== index + 1 || typeof option.binding_sha256 !== 'string' || !HASH.test(option.binding_sha256) || option.kind !== record.options[0].kind) throw new Error('Invalid clarification option');
    const common = ['n', 'kind', 'binding_sha256'];
    if (option.kind === 'route') {
      if (!exactFields(option, [...common, 'candidate_id', ...(Object.hasOwn(option, 'file_id') ? ['file_id'] : [])]) ||
          typeof option.candidate_id !== 'string' || !CANDIDATE.test(option.candidate_id) ||
          (Object.hasOwn(option, 'file_id') && (typeof option.file_id !== 'string' || !ID.test(option.file_id)))) throw new Error('Invalid clarification option');
    } else if (option.kind === 'file') {
      if (!exactFields(option, [...common, 'file_id']) || typeof option.file_id !== 'string' || !ID.test(option.file_id)) throw new Error('Invalid clarification option');
    } else if (option.kind === 'delivery') {
      if (!exactFields(option, [...common, 'delivery_mode']) || !['current_route', 'explicit_external'].includes(option.delivery_mode)) throw new Error('Invalid clarification option');
    } else throw new Error('Invalid clarification option');
  });
}

export function clarificationSelection(reply, record, prompt) {
  if (typeof reply !== 'string' || typeof prompt !== 'string') return 0;
  const value = reply.trim();
  const direct = new Map([['1', 1], ['第一个', 1], ['第一個', 1], ['first', 1], ['2', 2], ['第二个', 2], ['第二個', 2], ['second', 2]]);
  const number = direct.get(value.toLowerCase());
  if (number) return number <= record.options.length ? number : 0;
  const matches = [];
  for (const line of prompt.split('\n')) {
    const match = line.match(/^\s*([12])[.、)]\s+(.+)$/u);
    if (match && match[2] === value && Number(match[1]) <= record.options.length) matches.push(Number(match[1]));
  }
  return matches.length === 1 ? matches[0] : 0;
}

export function clarificationRecordDigest(record) { return digest(canonicalContextJSON(record)); }

// Paired release schemas must never silently upgrade an immutable, queued
// request. Already submitted work uses its original identity for lookup only.
export function validateSubmissionContext(context) {
  if (context.schema_version !== 2 || !Array.isArray(context.messages) || !context.messages.length ||
      context.messages.some(message => typeof message.id !== 'string' || !ID.test(message.id)) ||
      new Set(context.messages.map(message => message.id)).size !== context.messages.length) throw new Error('This queued request uses an older context format; your input is preserved. Submit it again as a new request.');
  for (const message of context.messages) if (message.clarification) validateClarificationRecord(message.clarification);
}
