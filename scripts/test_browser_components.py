"""Deployment contract checks without touching a user's browser profile."""
import base64
import copy
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parent))
import browser_components as components


class BrowserComponentsTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.profile = self.root / 'profile'
        self.default = self.profile / 'Default'
        self.default.mkdir(parents=True)
        self.identity = components.extension_id('/opt/sparkclaw/tampermonkey')
        self.prefs = self.default / 'Preferences'

    def test_prepare_preserves_unrelated_preferences_and_original_database(self):
        old = self.root / 'old-tampermonkey'
        (old / '_locales/en').mkdir(parents=True)
        (old / 'manifest.json').write_text(json.dumps({'version': '5.5.0'}))
        (old / '_locales/en/messages.json').write_text(json.dumps({'extName': {'message': 'Tampermonkey'}}))
        previous = self.default / 'Local Extension Settings/oldidentity'
        previous.mkdir(parents=True)
        original = b'opaque userscript data\x00\xff'
        (previous / '000001.ldb').write_bytes(original)
        unrelated = self.default / 'Local Extension Settings/anotherextension'
        unrelated.mkdir()
        (unrelated / '000002.ldb').write_bytes(b'unrelated extension data')
        preferences = {
            'profile': {'name': 'Owner', 'avatar_index': 7},
            'session': {'restore_on_startup': 1},
            'extensions': {'settings': {
                'oldidentity': {'path': str(old), 'user_scripts_enabled': True},
                'anotherextension': {'state': 1, 'custom_setting': 'keep'},
            }},
        }
        self.prefs.write_text(json.dumps(preferences))
        before = self.prefs.read_bytes()
        components.prepare_profile(self.profile)
        after = json.loads(self.prefs.read_text())
        managed = after['extensions']['settings'].pop(self.identity)
        self.assertEqual(managed, {'user_scripts_enabled': True})
        self.assertEqual(after, preferences)
        destination = self.default / 'Local Extension Settings' / self.identity
        self.assertFalse(destination.exists())
        self.assertEqual((previous / '000001.ldb').read_bytes(), original)
        self.assertEqual((unrelated / '000002.ldb').read_bytes(), b'unrelated extension data')
        backups = list((self.root / 'component-backups').iterdir())
        self.assertEqual(len(backups), 1)
        self.assertEqual((backups[0] / 'Preferences').read_bytes(), before)
        self.assertEqual((backups[0] / 'tampermonkey-storage/000001.ldb').read_bytes(), original)
        self.assertEqual(self.prefs.stat().st_mode & 0o777, 0o600)

        # Product scripts arrive through native system import. Re-deployment must
        # retain that new database without mixing in ordinary old user scripts.
        destination.mkdir(parents=True)
        (destination / '000001.ldb').write_bytes(b'new product storage')
        components.prepare_profile(self.profile)
        self.assertEqual((destination / '000001.ldb').read_bytes(), b'new product storage')
        self.assertEqual((previous / '000001.ldb').read_bytes(), original)

    def test_prepare_initializes_fresh_profile(self):
        components.prepare_profile(self.profile)
        self.assertTrue(json.loads(self.prefs.read_text())['extensions']['settings'][self.identity]['user_scripts_enabled'])
        self.assertFalse((self.default / 'Local Extension Settings' / self.identity).exists())

    def test_corrupt_preferences_are_not_overwritten(self):
        self.prefs.write_bytes(b'{broken existing preferences')
        with self.assertRaises(ValueError):
            components.prepare_profile(self.profile)
        self.assertEqual(self.prefs.read_bytes(), b'{broken existing preferences')

    def valid_state(self):
        manifest = components.manifest()
        provision = components.make_provisioning(manifest)
        state = {'!misc.managed.consumed': {'1:current': 123}}
        for index, (entry, script) in enumerate(zip(manifest['scripts'], provision['scripts'])):
            uid = f'fixture-{index}'
            state['!extdb.@meta#' + uid] = {
                'name': script['name'], 'version': entry['version'],
                'enabled': True, 'system': True,
                'options': {'check_for_updates': False},
            }
            state['!extdb.@source#' + uid] = base64.b64decode(script['source']).decode()
            for dep_index, requirement in enumerate(entry['requires']):
                state[f'!extdb.@ext#{uid}:{dep_index}'] = {
                    'url': requirement['url'],
                    'resource': {'base': base64.b64encode(components.pinned_file(requirement)).decode()},
                }
        self.prefs.write_text(json.dumps({'extensions': {'settings': {self.identity: {'user_scripts_enabled': True}}}}))
        policy = self.root / 'policy.json'
        policy.write_text(json.dumps({'3rdparty': {'extensions': {self.identity: {'jsonImport': [{'hash': '1:current'}]}}}}))
        return state, policy

    def verify_with_state(self, state, policy):
        with patch.object(components, 'POLICY', policy), patch('browser_component_state.read_extension_state', return_value=state):
            components.verify_profile(self.profile)

    def test_current_complete_profile_passes(self):
        state, policy = self.valid_state()
        self.verify_with_state(state, policy)

    def test_missing_permission_fails_even_with_valid_database(self):
        state, policy = self.valid_state()
        self.prefs.write_text('{}')
        with self.assertRaisesRegex(ValueError, 'permission'):
            self.verify_with_state(state, policy)

    def test_disabled_extension_fails_even_with_enabled_scripts(self):
        state, policy = self.valid_state()
        preferences = json.loads(self.prefs.read_text())
        preferences['extensions']['settings'][self.identity]['disable_reasons'] = [1]
        self.prefs.write_text(json.dumps(preferences))
        with self.assertRaises(ValueError):
            self.verify_with_state(state, policy)

    def test_receipt_metadata_sources_and_dependencies_fail_closed(self):
        # Dependency validation remains covered even when the production fork
        # is self-contained and no longer installs ChatGPT Exporter.
        manifest = components.manifest()
        data = (components.ROOT / 'tools/browser-userscripts/jszip.min.js').read_bytes()
        manifest['scripts'][0]['requires'] = [{'file': 'jszip.min.js', 'url': 'https://example.test/jszip.js', 'sha256': components.digest(data)}]
        override = patch.object(components, 'manifest', return_value=manifest)
        override.start()
        self.addCleanup(override.stop)
        valid, policy = self.valid_state()
        meta_key = '!extdb.@meta#fixture-0'
        dependency_key = next(key for key in valid if key.startswith('!extdb.@ext#'))

        def changed_meta(field, value):
            def change(state):
                state[meta_key][field] = value
            return change

        mutations = {
            'deployment not consumed': lambda s: s['!misc.managed.consumed'].clear(),
            'only old deployment consumed': lambda s: s.update({'!misc.managed.consumed': {'1:old': 123}}),
            'missing script': lambda s: s.pop(meta_key),
            'duplicate script': lambda s: s.update({'!extdb.@meta#duplicate': copy.deepcopy(s[meta_key])}),
            'disabled script': changed_meta('enabled', False),
            'non-system script': changed_meta('system', False),
            'wrong version': changed_meta('version', '0.0.0'),
            'uncontrolled updates': changed_meta('options', {'check_for_updates': True}),
            'missing update policy': changed_meta('options', {}),
            'changed source': lambda s: s.update({'!extdb.@source#fixture-0': '// modified'}),
            'missing source': lambda s: s.pop('!extdb.@source#fixture-0'),
            'missing dependency': lambda s: s.pop(dependency_key),
            'duplicate dependency': lambda s: s.update({dependency_key + '-duplicate': copy.deepcopy(s[dependency_key])}),
            'changed dependency': lambda s: s[dependency_key].update({'resource': {'base': 'Y2hhbmdlZA=='}}),
            'invalid dependency encoding': lambda s: s[dependency_key].update({'resource': {'base': '%%%'}}),
        }
        for description, mutation in mutations.items():
            with self.subTest(description=description):
                state = copy.deepcopy(valid)
                mutation(state)
                with self.assertRaises(ValueError):
                    self.verify_with_state(state, policy)

    def test_versioned_sources_and_dependency_pins_make_portable_provisioning(self):
        manifest = components.manifest()
        actual = components.make_provisioning(manifest)
        self.assertEqual(actual['version'], '1')
        self.assertEqual(len(actual['scripts']), len(manifest['scripts']))
        for expected, script in zip(manifest['scripts'], actual['scripts']):
            source = base64.b64decode(script['source'], validate=True)
            self.assertEqual(components.digest(source), expected['sha256'])
            metadata = dict(line[3:].split(None, 1) for line in source.decode().splitlines()
                            if line.startswith('// @version '))
            self.assertEqual(metadata['@version'], expected['version'])
            self.assertTrue(script['enabled'])
            self.assertFalse(script['options']['check_for_updates'])
            self.assertNotIn('storage', script)
            for expected_dep, cached in zip(expected['requires'], script['requires']):
                self.assertEqual(cached['url'], expected_dep['url'])
                self.assertEqual(components.digest(cached['content'].encode()), expected_dep['sha256'])
                self.assertIn('ts', cached)

    def test_source_and_dependency_checksum_changes_are_rejected(self):
        manifest = components.manifest()
        for kind in ('script', 'dependency'):
            with self.subTest(kind=kind):
                changed = copy.deepcopy(manifest)
                if kind == 'script':
                    changed['scripts'][0]['sha256'] = '0' * 64
                else:
                    changed['scripts'][0]['requires'] = [{'file': 'jszip.min.js', 'url': 'https://example.test/jszip.js', 'sha256': '0' * 64}]
                with self.assertRaisesRegex(ValueError, 'checksum'):
                    components.make_provisioning(changed)


if __name__ == '__main__':
    unittest.main()
