"""Stage and install public artifacts in isolation, without browser/profile writes.

Set SPARKCLAW_TEST_TAMPERMONKEY_CRX to the pinned official package to run the
same contracts against the complete upstream extension instead of patch anchors.
"""
import base64
import copy
import io
import json
import os
from pathlib import Path
import shutil
import struct
import subprocess
import sys
import tempfile
import unittest
import zipfile
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parent))
import browser_components as components


# The exact upstream anchors needed for staging. Only the managed-policy read is
# executable (upstream's own text, run by a Node harness with fake storage and
# timers); the other anchors stay inert in a comment. The optional pinned CRX
# run additionally checks all patches against the real distribution. No
# fixture starts a browser.
MANAGED_READ = '''globalThis.__sparkclawImport=(async()=>{const e=await(async()=>{const e=await(async()=>{let e;if(!xt.managed.supported)return e;for(let t=5;!e&&t>0;--t)5!==t&&console.warn(`Managed storage is slow or not responding! Retrying (attempt ${5-t})...`),await Promise.race([new Promise((e=>se((()=>e(!1)),200))),new Promise((t=>{xt.managed.get(["jsonImport"],(n=>{e=n,t(!0)}))}))]);return e})();return{
...e}})();if(!e||!e.jsonImport)return;return e.jsonImport.map(x=>x.hash)})();
'''
PATCH_ANCHORS = MANAGED_READ + '''/*
const n=Pe(),{file_url:r,source:s,storage:i,options:c,resources:l,requires:u}=e
defaultscript:!1!==o,replace:!0,internal:!0})}catch(e){}if(p&&p.installed)
if(e.defaultscript)return y.Breach();if(e.noreinstall)return y.Breach()
const{version:p,scripts:h,settings:f}=u;if("1"===p){if(h)
t.script&&t.cond&&t.script.evilness!=Uc.SEVERITY_FOISTED_SCRIPT&&(
*/
'''
# Runs the staged worker's managed read: storage returns {} until `arrive_ms`,
# when the policy appears and storage.onChanged(area 'managed') fires (0: the
# policy is already present when the worker starts).
POLICY_HARNESS = r'''
import fs from 'node:fs';
import vm from 'node:vm';
const [worker, expected, arriveText, consumedText] = process.argv.slice(2);
const arrive = Number(arriveText), consumed = consumedText === '1';
let now = 0, reads = 0, policy = {}, settled = false, value;
const timers = [], listeners = [], logs = [];
const schedule = (fn, ms) => {timers.push({at: now + Math.max(0, ms), fn});};
const context = vm.createContext({
  Promise, Set, JSON, structuredClone, queueMicrotask,
  Date: {now: () => now},
  console: {warn: () => {}, info: message => logs.push(message), log: () => {}},
  chrome: {storage: {onChanged: {addListener: listener => listeners.push(listener)}}},
  se: schedule,
  xt: {managed: {supported: true, get: (keys, callback) => {reads++; const copy = structuredClone(policy); queueMicrotask(() => callback(copy));}}},
  Rh: {get: async () => consumed ? {[expected]: true} : {}},
});
context.self = context;
if (arrive <= 0) policy = {jsonImport: [{hash: expected}]};
vm.runInContext(fs.readFileSync(worker, 'utf8'), context);
const registeredAtLoad = listeners.length;
if (arrive > 0 && Number.isFinite(arrive)) schedule(() => {
  policy = {jsonImport: [{hash: expected}]};
  for (const listener of listeners) listener({jsonImport: {newValue: policy.jsonImport}}, 'managed');
}, arrive);
context.__sparkclawImport.then(result => {settled = true; value = result;});
for (let step = 0; step < 100000; step++) {
  for (let i = 0; i < 5; i++) await new Promise(resolve => setImmediate(resolve));
  if (settled) break;
  timers.sort((a, b) => a.at - b.at);
  const next = timers.shift();
  if (!next) throw new Error('worker read is waiting without a timer or event');
  now = next.at; next.fn();
}
process.stdout.write(JSON.stringify({hashes: value ?? null, finished_ms: now, reads, registered_at_load: registeredAtLoad, logs}));
'''


class BrowserComponentStageTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.repo = self.root / 'source'
        self.scripts = self.repo / 'tools/browser-userscripts'
        self.scripts.mkdir(parents=True)
        self.system = self.root / 'public/browser-components'
        self.extension = self.system.parent / 'tampermonkey'
        self.policy = self.root / 'public/policy/managed.json'
        self.m = copy.deepcopy(components.manifest())
        crx_path = os.environ.get('SPARKCLAW_TEST_TAMPERMONKEY_CRX')
        if crx_path:
            package = Path(crx_path).read_bytes()
            self.assertEqual(components.digest(package), self.m['tampermonkey']['sha256'])
        else:
            archive = io.BytesIO()
            with zipfile.ZipFile(archive, 'w') as bundle:
                bundle.writestr('manifest.json', json.dumps({
                    'version': self.m['tampermonkey']['version'],
                    'background': {'service_worker': 'background.js'}}))
                bundle.writestr('background.js', PATCH_ANCHORS)
            package = b'Cr24' + struct.pack('<II', 3, 0) + archive.getvalue()
        self.m['tampermonkey']['sha256'] = components.digest(package)
        self.m['tampermonkey']['path'] = str(self.extension)
        self.m['scripts'] = [{
            'file': 'reader.user.js', 'version': '1.0.0', 'url': 'https://example.invalid/reader.user.js',
            'uuid': '5b4323dc-326f-4fbb-a1a0-4ac8c00631c0', 'requires': [{
                'file': 'dependency.js', 'url': 'https://example.invalid/dependency.js'}]}]
        self.write_pinned(self.m['scripts'][0], '// @name Reader fixture\n// @version 1.0.0\nvoid 0;\n')
        self.write_pinned(self.m['scripts'][0]['requires'][0], 'const fixture = 1;\n')
        cache = self.root / 'cache/sparkclaw/browser-components'
        cache.mkdir(parents=True)
        (cache / (self.m['tampermonkey']['sha256'] + '.crx')).write_bytes(package)
        for mock in [patch.object(components, 'ROOT', self.repo),
                     patch.dict(os.environ, {'XDG_CACHE_HOME': str(self.root / 'cache')}),
                     patch.object(components.urllib.request, 'urlopen', side_effect=AssertionError('unexpected network'))]:
            mock.start()
            self.addCleanup(mock.stop)

    def write_pinned(self, entry, source):
        data = source.encode()
        (self.scripts / entry['file']).write_bytes(data)
        entry['sha256'] = components.digest(data)

    def stage(self, name, manifest=None):
        target = self.root / name
        with patch.object(components, 'manifest', return_value=manifest or self.m):
            components.stage(target)
        return target

    def snapshot(self, directory):
        return {str(p.relative_to(directory)): p.read_bytes()
                for p in directory.rglob('*') if p.is_file()}

    def native_import(self, target):
        policy = json.loads((target / 'policy.json').read_text())
        imports = next(iter(policy['3rdparty']['extensions'].values()))['jsonImport']
        self.assertEqual(len(imports), 1)
        item = imports[0]
        payload = json.loads(base64.b64decode(item['url'].split(',', 1)[1]))
        self.assertEqual(item['hash'], '1:' + components.provisioning_hash(payload))
        return item, payload

    def install(self, target):
        with patch.object(components, 'SYSTEM', self.system), \
                patch.object(components, 'MANAGED_EXTENSION', self.extension), \
                patch.object(components, 'POLICY', self.policy), \
                patch.object(components, 'POLICY_DIRECTORY_DEPTH', 1), \
                patch.object(components.os, 'getuid', return_value=0), patch.object(components.os, 'chown'):
            components.install_system(target)

    def test_identical_inputs_stage_identical_bytes_across_clock_paths_and_cache(self):
        with patch.object(components.time, 'time_ns', return_value=111):
            first = self.stage('first')
        shutil.copytree(self.root / 'cache', self.root / 'other-cache')
        with patch.object(components.time, 'time_ns', return_value=222), \
                patch.dict(os.environ, {'XDG_CACHE_HOME': str(self.root / 'other-cache')}):
            second = self.stage('different/path/second')
        self.assertEqual(self.snapshot(first), self.snapshot(second))
        item, payload = self.native_import(first)
        self.assertTrue(item['haltOnError'])
        self.assertTrue(item['installAsSystemScripts'])
        self.assertRegex(payload['deployment'], r'^[0-9a-f]{64}$')
        self.assertNotIn(str(self.root / 'cache'), json.dumps(payload))

    def test_script_dependency_policy_and_version_changes_have_new_native_hash(self):
        baseline, _ = self.native_import(self.stage('baseline'))
        for kind in ('script', 'dependency', 'policy', 'version', 'patch', 'retirement'):
            with self.subTest(kind=kind):
                m = copy.deepcopy(self.m)
                options = dict(components.MANAGED_IMPORT_OPTIONS)
                retired = components.RETIRED_MANAGED_UUIDS
                restore = None
                if kind in ('script', 'dependency'):
                    entry = m['scripts'][0] if kind == 'script' else m['scripts'][0]['requires'][0]
                    restore = self.scripts / entry['file']
                    previous = restore.read_bytes()
                    self.write_pinned(entry, previous.decode() + '// changed public bytes\n')
                elif kind == 'policy':
                    options['haltOnError'] = False
                elif kind == 'version':
                    m['tampermonkey']['productVersion'] = '5.5.0.99'
                elif kind == 'patch':
                    m['tampermonkey']['patchRevision'] += 1
                else:
                    retired = (*retired, '994ec276-cd06-4ba1-8a58-30b65521b0d0')
                try:
                    with patch.object(components, 'MANAGED_IMPORT_OPTIONS', options), \
                            patch.object(components, 'RETIRED_MANAGED_UUIDS', retired):
                        item, _ = self.native_import(self.stage(kind, m))
                    self.assertNotEqual(item['hash'], baseline['hash'])
                    self.assertEqual(item['haltOnError'], options['haltOnError'])
                finally:
                    if restore:
                        restore.write_bytes(previous)

    def test_restage_reinstall_preserves_bytes_native_generation_and_private_data(self):
        private = self.root / 'profile/Default/Local Extension Settings/owner'
        private.mkdir(parents=True)
        first = self.stage('first')
        item, _ = self.native_import(first)
        (private / 'native-state.json').write_text(json.dumps({
            '!misc.managed.consumed': {item['hash']: True},
            'ordinary-script': {'enabled': True, 'storage': {'keep': 'owner data'}}}))
        before = self.snapshot(private.parent.parent)
        self.install(first)
        installed = self.snapshot(self.extension)
        second = self.stage('second')
        self.install(second)
        self.assertEqual(installed, self.snapshot(self.extension))
        self.assertEqual(self.policy.read_bytes(), (first / 'policy.json').read_bytes())
        self.assertEqual((self.system / 'receipt.json').read_bytes(), (first / 'receipt.json').read_bytes())
        self.assertEqual(self.native_import(second)[0]['hash'], item['hash'])
        self.assertEqual(before, self.snapshot(private.parent.parent))

    def test_changed_stage_is_rejected_before_write_until_product_version_changes(self):
        first = self.stage('first')
        self.install(first)
        before = self.snapshot(self.root / 'public')
        m = copy.deepcopy(self.m)
        self.write_pinned(m['scripts'][0], '// @name Reader fixture\n// @version 1.0.0\nvoid 1;\n')
        changed = self.stage('changed-same-version', m)
        with self.assertRaisesRegex(ValueError, 'increase productVersion'):
            self.install(changed)
        self.assertEqual(before, self.snapshot(self.root / 'public'))
        m['tampermonkey']['productVersion'] = '5.5.0.99'
        m['tampermonkey']['patchRevision'] += 1
        upgraded = self.stage('changed-new-version', m)
        self.install(upgraded)
        self.assertEqual(self.snapshot(self.extension), self.snapshot(upgraded / 'tampermonkey'))

    def run_policy_read(self, arrive_ms, consumed=False):
        if os.environ.get('SPARKCLAW_TEST_TAMPERMONKEY_CRX') or not shutil.which('node'):
            self.skipTest('requires the executable anchor fixture and node')
        target = self.stage('policy-' + str(arrive_ms) + ('-consumed' if consumed else ''))
        item, _ = self.native_import(target)
        extension = target / 'tampermonkey'
        worker = extension / json.loads((extension / 'manifest.json').read_text())['background']['service_worker']
        harness = self.root / 'policy-harness.mjs'
        harness.write_text(POLICY_HARNESS)
        result = subprocess.run(['node', str(harness), str(worker), item['hash'], str(arrive_ms), '1' if consumed else '0'],
                                capture_output=True, text=True, timeout=60, check=True)
        return item['hash'], json.loads(result.stdout)

    def test_managed_policy_arriving_after_the_old_retry_budget_is_imported(self):
        # .34 on work2: the policy was still absent after 249 reads over 51 s.
        expected, result = self.run_policy_read(60_500)
        self.assertEqual(result['hashes'], [expected])
        self.assertEqual(result['registered_at_load'], 1)
        # The storage.onChanged wake imports at arrival, not at the next poll.
        self.assertLess(result['finished_ms'], 60_500 + 300)
        self.assertLessEqual(result['reads'], 32)
        self.assertTrue(any('managed policy available after' in line for line in result['logs']))

    def test_managed_policy_read_does_not_wait_when_already_consumed_or_present(self):
        expected, consumed = self.run_policy_read(float('inf'), consumed=True)
        self.assertEqual((consumed['hashes'], consumed['finished_ms'], consumed['reads']), (None, 0, 1))
        _, immediate = self.run_policy_read(0)
        self.assertEqual((immediate['hashes'], immediate['reads']), ([expected], 1))
        self.assertLess(immediate['finished_ms'], 300)

    def test_managed_policy_wait_is_bounded_when_policy_never_arrives(self):
        _, result = self.run_policy_read(float('inf'))
        self.assertIsNone(result['hashes'])
        self.assertGreaterEqual(result['finished_ms'], components.MANAGED_POLICY_WAIT_MS)
        self.assertLess(result['finished_ms'], components.MANAGED_POLICY_WAIT_MS + 2_500)
        self.assertLess(components.MANAGED_POLICY_WAIT_MS / 1000, components.PROFILE_READINESS_ATTEMPTS * 2)

    def test_versioned_reimport_keeps_script_id_and_source_but_changes_consumed_key(self):
        first, source = self.native_import(self.stage('first'))
        m = copy.deepcopy(self.m)
        m['tampermonkey']['productVersion'] = '5.5.0.99'
        m['tampermonkey']['patchRevision'] += 1
        reimport, unchanged = self.native_import(self.stage('versioned-reimport', m))
        self.assertEqual(source['scripts'], unchanged['scripts'])
        self.assertNotEqual(first['hash'], reimport['hash'])


if __name__ == '__main__':
    unittest.main()
