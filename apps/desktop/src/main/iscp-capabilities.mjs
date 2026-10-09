const SURFACES = Object.freeze({
  settings_owner: ['settings.owner.get', 'settings.owner.patch'],
  settings_connectors: ['settings.connectors.list', 'settings.connectors.patch'],
  settings_credentials: ['settings.integrations.list', 'settings.credentials.add', 'settings.credentials.activate', 'settings.credentials.check', 'settings.credentials.delete'],
  notifications: ['notifications.list', 'notifications.read', 'notifications.read_all'],
  files: ['transfer.open', 'transfer.status', 'transfer.chunk', 'transfer.commit', 'transfer.abort', 'object.describe', 'object.read', 'object.release', 'execution.input.put', 'execution.file.get'],
  approvals: ['approvals.list', 'approvals.get', 'approvals.decide'],
  events: ['events.pull', 'events.ack', 'events.snapshot'],
  mail_settings: ['mail.providers.list', 'mail.providers.update', 'mail.providers.check', 'mail.providers.login'],
  mail_read: ['mail.mailboxes', 'mail.sync', 'mail.message'],
  mail_attachments: ['mail.attachment'],
  mail_send: ['mail.drafts.list', 'mail.drafts.save', 'mail.drafts.send', 'mail.drafts.reconcile'],
  mail_send_attachments: ['mail.drafts.list', 'mail.drafts.save', 'mail.drafts.send', 'mail.drafts.reconcile'],
  browser: ['browser.host.grant', 'browser.host.revoke', 'browser.host.register', 'browser.host.poll', 'browser.host.reply', 'browser.host.heartbeat', 'browser.host.close', 'browser.receipt', 'browser.reconcile'],
  speech_recording: ['speech.status', 'speech.transcribe', 'speech.cancel'],
  speech_realtime: ['speech.session.open', 'speech.session.frame', 'speech.session.events', 'speech.session.finish', 'speech.session.cancel'],
  speech_playback: ['audio.playback'],
});
const DEPENDENCIES = { mail_read: ['events'], mail_attachments: ['mail_read', 'files'], mail_send: ['mail_read', 'files', 'approvals'], mail_send_attachments: ['mail_send'], browser: ['files', 'approvals', 'events'], speech_recording: ['files'], speech_realtime: ['speech_recording', 'events'], speech_playback: ['speech_realtime'] };

export function isISCPCapabilityReportCurrent(manifest, report, now = Date.now()) {
  return manifest?.schema_version === 2 && manifest.profile === 'sparkclaw.workbench.transport.v2' &&
    typeof manifest.session_id === 'string' && manifest.session_id.length > 0 &&
    Number.isSafeInteger(manifest.authorization_revision) && manifest.authorization_revision > 0 &&
    Date.parse(manifest.expires_at) > now && report?.schema_version === 2 &&
    report.profile === manifest.profile && report.session_id === manifest.session_id &&
    report.authorization_revision === manifest.authorization_revision && Date.parse(report.expires_at) > now;
}

// A permission advertisement is necessary, but is never a release certificate.
// Qualification is private launcher configuration, not a renderer-controlled bit.
export function projectISCPCapabilities(manifest, report, now = Date.now()) {
  const operations = Array.isArray(manifest?.operations) ? manifest.operations : [];
  const valid = isISCPCapabilityReportCurrent(manifest, report, now);
  const granted = new Set(operations);
  const rows = new Map((Array.isArray(report?.capabilities) ? report.capabilities : []).map((row) => [row.id, row]));
  const surfaces = {};
  for (const [surface, required] of Object.entries(SURFACES)) {
    const reason = !valid ? 'negotiation_required' : !required.every((op) => granted.has(op)) ? 'permission_or_implementation_missing' : !(rows.get(surface)?.qualified === true && rows.get(surface)?.enabled === true && rows.get(surface)?.permitted === true && rows.get(surface)?.supported === true && rows.get(surface)?.dependencies_ready === true) ? (rows.get(surface)?.reason || 'qualification_required') : !(DEPENDENCIES[surface] || []).every((dependency) => surfaces[dependency]?.enabled) ? 'dependency_unavailable' : '';
    surfaces[surface] = Object.freeze({ enabled: !reason, reason });
  }
  const enabled = (surface) => surfaces[surface].enabled;
  return Object.freeze({ operations: Object.freeze([...operations]), surfaces: Object.freeze(surfaces),
    files: enabled('files'), mail: enabled('mail_read'), browser: enabled('browser'), speech: enabled('speech_recording'),
    approvals: enabled('approvals'), settings: enabled('settings_owner') || enabled('settings_connectors') || enabled('settings_credentials') || enabled('mail_settings'),
    notifications: enabled('notifications'), events: enabled('events'),
  });
}

export const ISCP_SURFACES = Object.freeze(Object.keys(SURFACES));
