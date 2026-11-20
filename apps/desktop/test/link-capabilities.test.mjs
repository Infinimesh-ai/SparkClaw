import test from 'node:test';
import assert from 'node:assert/strict';
import { LINK_SURFACES, projectLinkCapabilities } from '../src/main/link-capabilities.mjs';
import { TYPED_METHODS, linkDomainOperations } from '../src/main/link-domain-routes.mjs';
import { LINK_REVERSE_OPERATIONS } from '../src/main/link-transport.mjs';

const allDomains = [...new Set([...TYPED_METHODS.keys()].map(name => name.split('.')[0]))];
const full = { operations: [...linkDomainOperations(allDomains), 'presentation.config', 'presentation.owner', 'presentation.ready', ...LINK_REVERSE_OPERATIONS] };

test('a link connection opens every surface whose domain methods it reaches; files, approvals and events are core', () => {
  const projected = projectLinkCapabilities(full);
  for (const id of ['files', 'approvals', 'events', 'settings_owner', 'mail_popup', 'mail_send', 'mail_send_attachments', 'browser', 'speech_recording', 'speech_realtime', 'medicine_box_read', 'medicine_box_write', 'notifications', 'mcd_selection', 'mcd_prepare'])
    assert.equal(projected.surfaces[id].enabled, true, id);
  assert.ok(projected.files && projected.mail && projected.browser && projected.speech && projected.approvals && projected.settings && projected.notifications && projected.events);
  // Devices are managed on the console, and playback has no link domain.
  for (const id of ['settings_devices', 'settings_devices_delete', 'speech_playback']) assert.equal(projected.surfaces[id].enabled, false, id);
  assert.deepEqual(Object.keys(projected.surfaces), [...LINK_SURFACES]);
});

test('without a connection nothing is open', () => {
  const projected = projectLinkCapabilities(undefined);
  for (const id of LINK_SURFACES) assert.equal(projected.surfaces[id].enabled, false, id);
  assert.equal(projected.files, false);
});

test('a missing local Controller disables only browser access', () => {
  const ready = projectLinkCapabilities(full), unavailable = projectLinkCapabilities(full, false);
  assert.equal(unavailable.browser, false);
  assert.equal(unavailable.surfaces.browser.reason, 'local_runtime_unavailable');
  for (const id of LINK_SURFACES.filter(id => id !== 'browser')) assert.deepEqual(unavailable.surfaces[id], ready.surfaces[id]);
  assert.equal(projectLinkCapabilities({ operations: full.operations.filter(op => op !== 'browser.controller.execute') }).browser, false);
});

test('a surface needs all its methods, and dependent surfaces follow it', () => {
  const without = (...ops) => projectLinkCapabilities({ operations: full.operations.filter(op => !ops.includes(op)) }).surfaces;
  assert.equal(without('mail.drafts.send').mail_send.enabled, false);
  assert.equal(without('mail.drafts.send').mail_send_attachments.enabled, false);
  assert.equal(without('mail.mailboxes').mail_send.reason, 'dependency_unavailable');
  assert.equal(without('medicine.sync').medicine_box_write.reason, 'dependency_unavailable');
  assert.equal(without('speech.status').speech_realtime.reason, 'dependency_unavailable');
  const settings = projectLinkCapabilities({ operations: ['mail.providers.list', 'mail.providers.update', 'mail.providers.check'] });
  assert.equal(settings.surfaces.mail_settings.enabled, true);
  assert.equal(settings.settings, true);
  assert.equal(settings.mail, false);
});
