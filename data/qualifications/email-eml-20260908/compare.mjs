import fs from 'node:fs/promises';
import path from 'node:path';
import crypto from 'node:crypto';
import { createRequire } from 'node:module';

const root = process.cwd();
const folder = path.join(root, 'data/qualifications/email-eml-20260908');
const observations = path.join(root, 'data/qualifications/email-dom-20260907');
const require = createRequire(path.join(root, 'tools/browser-controller/package.json'));
const { simpleParser } = require('mailparser');
const { chromium } = require('playwright-core');
const normalize = value => String(value ?? '').normalize('NFC').replace(/\s+/gu, ' ').trim();
const sha = bytes => crypto.createHash('sha256').update(bytes).digest('hex');
const json = async file => JSON.parse(await fs.readFile(file, 'utf8'));
const browser = await chromium.launch({ headless: true, timeout: 20000, executablePath: await fs.realpath('/usr/local/bin/chromium') });
const report = { date: '2026-09-08', decision: 'candidate_not_adopted', assessment: 'sample_basic_information_capture_feasible', complete_read_workflow_qualified: false, production_path_changed: false, providers: {} };
try {
  const context = await browser.newContext({ javaScriptEnabled: false, offline: true });
  await context.route('**/*', route => route.abort());
  const page = await context.newPage();
  const render = async html => {
    await page.setContent(html, { waitUntil: 'domcontentloaded', timeout: 10000 });
    return await page.evaluate(() => ({
      text: document.body.innerText,
      links: Array.from(document.querySelectorAll('a[href]')).map(node => node.getAttribute('href')),
      images: Array.from(document.querySelectorAll('img[src]')).map(node => node.getAttribute('src')),
      tables: document.querySelectorAll('table').length,
    }));
  };
  for (const provider of ['qq_mail', 'gmail']) {
    let bytes;
    try { bytes = await fs.readFile(path.join(folder, provider + '.eml')); }
    catch (error) { if (error.code === 'ENOENT') { report.providers[provider] = { reference_obtained: false }; continue; } throw error; }
    const acquisition = await json(path.join(folder, provider + '-acquisition.json'));
    if (bytes.length !== acquisition.bytes || sha(bytes) !== acquisition.sha256) throw new Error('reference_integrity_failed');
    const original = await simpleParser(bytes);
    const observationBytes = await fs.readFile(path.join(observations, provider + '-observations.json'));
    const raw = JSON.parse(observationBytes), observed = Array.isArray(raw) ? raw[0] : raw;
    const current = await json(path.join(folder, provider + '-current-observation.json'));
    const reference = original.html ? await render(original.html) : { text: original.text, links: [], images: [], tables: 0 };
    const saved = await render(observed.body_html);
    const before = normalize(reference.text), after = normalize(observed.body_text), htmlText = normalize(saved.text);
    let prefix = 0; while (prefix < Math.min(before.length, after.length) && before[prefix] === after[prefix]) prefix++;
    let suffix = 0; while (suffix < Math.min(before.length, after.length) - prefix && before.at(-1 - suffix) === after.at(-1 - suffix)) suffix++;
    const refLinks = [...new Set(reference.links)], actualLinks = new Set(saved.links);
    const addressList = header => original[header]?.value?.flatMap(value => value.group ?? [value]).map(value => value.address.toLowerCase()).filter(Boolean) ?? [];
    const headerEvidence = { subject_matches: observed.subject === original.subject };
    if (provider === 'gmail') {
      const liveHeaders = await json(path.join(folder, provider + '-headers-observation.json'));
      const fields = Object.fromEntries(observed.detail_rows.filter(row => row.length >= 2).map(row => [row[0].trim().toLowerCase().replace(/:$/, ''), row.slice(1).join(' ').trim()]));
      for (const header of ['from', 'to', 'cc', 'replyTo']) {
        const field = header === 'replyTo' ? 'reply-to' : header;
        const addresses = addressList(header);
        headerEvidence[field] = { reference_count: addresses.length, field_observed: Object.hasOwn(fields, field), all_reference_addresses_present: addresses.length ? addresses.every(address => (fields[field] ?? '').toLowerCase().includes(address)) : null };
      }
      const parsedDate = Date.parse(fields.date ?? '');
      headerEvidence.displayed_date_matches_to_minute = Number.isFinite(parsedDate) && Math.floor(parsedDate / 60000) === Math.floor(original.date.getTime() / 60000);
      headerEvidence.date_parse_available = Number.isFinite(parsedDate);
      headerEvidence.browser_timezone = liveHeaders.browser_timezone;
      if (liveHeaders.browser_timezone !== Intl.DateTimeFormat().resolvedOptions().timeZone) throw new Error('date_comparison_timezone_mismatch');
      headerEvidence.current_displayed_date_matches_prior = liveHeaders.dates.some(value => value.title === fields.date);
      headerEvidence.displayed_minute_minus_date_header_seconds = (parsedDate - original.date.getTime()) / 1000;
      headerEvidence.received_trace = original.headerLines.filter(line => line.key === 'received').map(line => {
        const received = Date.parse(line.line.slice(line.line.lastIndexOf(';') + 1));
        return { parsed: Number.isFinite(received), seconds_after_date_header: (received - original.date.getTime()) / 1000, matches_displayed_minute: Math.floor(received / 60000) === Math.floor(parsedDate / 60000) };
      });
      headerEvidence.displayed_time_semantics = 'matches_received_trace_minute_but_provider_definition_not_proven';
    } else {
      headerEvidence.previously_captured_fields = Object.keys(observed).filter(key => ['subject', 'sender', 'from', 'to', 'cc', 'date', 'reply_to'].includes(key));
      headerEvidence.not_previously_captured = ['from', 'to', 'cc', 'date', 'reply_to'];
      const expandedBytes = await fs.readFile(path.join(folder, provider + '-expanded-headers-observation.json'));
      const expanded = JSON.parse(expandedBytes);
      const fields = Object.fromEntries(expanded.text.filter(node => node.class === 'xmail-ui-label-align basic-item-name').map(node => [node.text.toLowerCase(), node.parent_text]));
      headerEvidence.supplemental_observation_sha256 = sha(expandedBytes);
      headerEvidence.detail_expansions = 1;
      headerEvidence.browser_timezone = expanded.browser_timezone;
      for (const header of ['from', 'to', 'cc', 'replyTo']) {
        const field = header === 'replyTo' ? 'reply-to' : header;
        const addresses = addressList(header);
        headerEvidence[field] = { reference_count: addresses.length, field_observed: Object.hasOwn(fields, field), all_reference_addresses_present: addresses.length ? addresses.every(address => (fields[field] ?? '').toLowerCase().includes(address)) : null };
      }
      const dateNode = expanded.text.find(node => node.class === 'basic-item-text' && node.parent_text === fields.date);
      const parsedDate = Date.parse(dateNode?.text ?? '');
      if (expanded.browser_timezone !== Intl.DateTimeFormat().resolvedOptions().timeZone) throw new Error('date_comparison_timezone_mismatch');
      headerEvidence.displayed_date_matches_to_minute = Number.isFinite(parsedDate) && Math.floor(parsedDate / 60000) === Math.floor(original.date.getTime() / 60000);
      headerEvidence.grouping_coverage = 'one_sender_one_recipient_no_cc_only';
    }
    const providerReport = {
      reference_obtained: true, reference_integrity_verified: true, original_bytes: bytes.length, original_sha256: sha(bytes), observation_sha256: sha(observationBytes),
      acquisition: { mode: acquisition.acquisition, response: acquisition.response, earlier_native_click_timeout: provider === 'gmail' },
      account_matches: acquisition.account_matches, identity_matches: acquisition.identity_matches, headers: headerEvidence,
      body: {
        reference_kind: original.html ? 'independently_rendered_original_html' : 'original_text_part', scripts_disabled: true, network_blocked: true,
        reference_visible_characters: before.length, captured_visible_characters: after.length,
        visible_text_matches: before === after, captured_html_visible_text_matches: before === htmlText,
        current_body_matches_prior_capture: normalize(current.body_text) === after,
        alternate_plain_text_matches: normalize(original.text) === after,
        common_prefix_characters: prefix, common_suffix_characters: suffix,
        differing_reference_characters: before.length - prefix - suffix, differing_capture_characters: after.length - prefix - suffix,
        reference_links: refLinks.length, captured_links: actualLinks.size,
        all_original_link_targets_preserved: refLinks.every(link => actualLinks.has(link)),
        missing_link_count: refLinks.filter(link => !actualLinks.has(link)).length,
        reference_images: reference.images.length, captured_images: saved.images.length,
        reference_tables: reference.tables, captured_tables: saved.tables,
      },
      attachments: { original_mime_parts: original.attachments.length, prior_dom_inventory_complete: observed.inventory_complete ?? null, live_attachment_download_qualified: false },
      original_export_elapsed_ms: acquisition.elapsed_ms,
    };
    report.providers[provider] = providerReport;
    await fs.writeFile(path.join(folder, provider + '-comparison-private.json'), JSON.stringify({ reference_text: reference.text, observed_text: observed.body_text, captured_html_text: saved.text, reference_links: reference.links, captured_links: saved.links, header_lines: original.headerLines }, null, 2), { mode: 0o600 });
  }
} finally { await browser.close(); }
report.limitations = ['Two existing pinned read samples only', 'No unread selection or read-state mutation qualification', 'Both original samples contain zero attachments; no live attachment download qualification', 'QQ expanded details do not expose Reply-To as a separate field', 'Gmail displayed time must not be treated as the original Date header', 'Normalized visible-text equality does not prove identical HTML or layout', 'Remote image bytes and complex quoted history were not qualified'];
await fs.writeFile(path.join(folder, 'report.json'), JSON.stringify(report, null, 2), { mode: 0o600 });
console.log(JSON.stringify(report, null, 2));
