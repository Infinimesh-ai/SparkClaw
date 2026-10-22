"""Deployment contract checks without touching a user's browser profile."""
import base64
import copy
import json
import io
import os
from pathlib import Path
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest
import zipfile
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

    def test_readiness_allows_policy_import_after_the_previous_minute_budget(self):
        attempts = [ValueError('policy not yet imported')] * 40 + [None]
        with patch.object(components, 'verify_profile', side_effect=attempts) as verify, \
                patch.object(components.time, 'sleep') as sleep:
            components.wait_profile(self.profile)
        self.assertEqual(verify.call_count, 41)
        self.assertEqual(sleep.call_count, 40)

    def test_readiness_still_fails_when_policy_never_imports(self):
        with patch.object(components, 'verify_profile', side_effect=ValueError('not imported')) as verify, \
                patch.object(components.time, 'sleep'):
            with self.assertRaisesRegex(ValueError, 'readiness timed out: not imported'):
                components.wait_profile(self.profile)
        self.assertEqual(verify.call_count, components.PROFILE_READINESS_ATTEMPTS)

    def test_unpacked_extension_omits_signed_metadata_but_retains_code(self):
        data = io.BytesIO()
        with zipfile.ZipFile(data, 'w') as archive:
            archive.writestr('_metadata/verified_contents.json', '{"signed":true}')
            archive.writestr('manifest.json', '{"manifest_version":3}')
            archive.writestr('worker.js', 'self.loaded = true;')
        extension = self.root / 'extension'
        with zipfile.ZipFile(data) as archive:
            components.extract_unpacked_extension(archive, extension)
        self.assertFalse((extension / '_metadata').exists())
        self.assertEqual((extension / 'worker.js').read_text(), 'self.loaded = true;')
        self.assertEqual(json.loads((extension / 'manifest.json').read_text()), {'manifest_version': 3})

    def test_reserved_metadata_does_not_bypass_archive_safety(self):
        data = io.BytesIO()
        with zipfile.ZipFile(data, 'w') as archive:
            archive.writestr('_metadata/../../escape', 'unsafe')
        with zipfile.ZipFile(data) as archive:
            with self.assertRaisesRegex(ValueError, 'unsafe CRX entry'):
                components.extract_unpacked_extension(archive, self.root / 'extension')
        self.assertFalse((self.root / 'escape').exists())

    def worker_update_fixture(self, version='5.5.0.11', worker='old-worker.js', body='old worker'):
        system = self.root / 'public/browser-components'
        extension = system.parent / 'tampermonkey'
        policy = self.root / 'policy/managed.json'
        candidate = self.root / 'candidate'
        for path in [system, extension, policy.parent, candidate / 'tampermonkey']:
            path.mkdir(parents=True, exist_ok=True)

        def write(directory, release, filename, source):
            (directory / 'tampermonkey').mkdir(exist_ok=True)
            (directory / 'tampermonkey/manifest.json').write_text(json.dumps({
                'version': release, 'background': {'service_worker': filename}}))
            (directory / 'tampermonkey' / filename).write_text(source)
            (directory / 'receipt.json').write_text(json.dumps({'manifest': {'tampermonkey': {
                'path': str(extension), 'productVersion': release}}}))
            (directory / 'policy.json').write_text(json.dumps({'fixture_version': release, 'worker': filename}))

        old = self.root / 'old'
        old.mkdir()
        write(old, '5.5.0.11', 'old-worker.js', 'old worker')
        shutil.copytree(old / 'tampermonkey', extension, dirs_exist_ok=True)
        shutil.copyfile(old / 'receipt.json', system / 'receipt.json')
        shutil.copyfile(old / 'policy.json', policy)
        write(candidate, version, worker, body)
        self.prefs.write_text('{"fixture":"preserve owner session"}')
        return system, extension, policy, candidate

    def install_fixture(self, paths):
        system, extension, policy, candidate = paths
        with patch.object(components, 'SYSTEM', system), patch.object(components, 'POLICY', policy), \
                patch.object(components, 'POLICY_DIRECTORY_DEPTH', 1), \
                patch.object(components, 'MANAGED_EXTENSION', extension), \
                patch.object(components.os, 'getuid', return_value=0), patch.object(components.os, 'chown'):
            components.install_system(candidate)

    def test_same_version_worker_change_rejected_before_any_production_write(self):
        for filename, body in [('new-worker.js', 'old worker'), ('old-worker.js', 'changed worker')]:
            with self.subTest(filename=filename):
                paths = self.worker_update_fixture(worker=filename, body=body)
                system, extension, policy, _ = paths
                before = {str(p): (p.read_bytes(), stat.S_IMODE(p.stat().st_mode))
                          for root in [system.parent, policy.parent, self.profile]
                          for p in root.rglob('*') if p.is_file()}
                with patch.object(components, 'install_public_directories') as write:
                    with self.assertRaisesRegex(ValueError, 'increase productVersion'):
                        self.install_fixture(paths)
                    write.assert_not_called()
                after = {str(p): (p.read_bytes(), stat.S_IMODE(p.stat().st_mode))
                         for root in [system.parent, policy.parent, self.profile]
                         for p in root.rglob('*') if p.is_file()}
                self.assertEqual(before, after)
                self.assertFalse(extension.with_name('tampermonkey.pending').exists())
                self.assertFalse(extension.with_name('tampermonkey.previous').exists())
                for root in [system.parent, policy.parent, self.root / 'candidate', self.root / 'old']:
                    shutil.rmtree(root)

    def test_changed_worker_installs_with_new_version_and_keeps_previous_tree(self):
        paths = self.worker_update_fixture(version='5.5.0.12', worker='new-worker.js', body='new worker')
        _, extension, policy, candidate = paths
        original_preferences = self.prefs.read_bytes()
        self.install_fixture(paths)
        self.assertEqual((extension / 'new-worker.js').read_text(), 'new worker')
        self.assertEqual((extension.with_name('tampermonkey.previous') / 'old-worker.js').read_text(), 'old worker')
        self.assertEqual(policy.read_bytes(), (candidate / 'policy.json').read_bytes())
        self.assertEqual(self.prefs.read_bytes(), original_preferences)

    def test_same_worker_bytes_can_be_reinstalled_at_the_same_version(self):
        paths = self.worker_update_fixture()
        self.install_fixture(paths)
        self.install_fixture(paths)
        self.assertEqual((paths[1] / 'old-worker.js').read_text(), 'old worker')

    def test_different_version_paired_rollback_is_not_blocked(self):
        paths = self.worker_update_fixture(version='5.5.0.10', worker='rollback-worker.js', body='previous release')
        self.install_fixture(paths)
        self.assertEqual((paths[1] / 'rollback-worker.js').read_text(), 'previous release')

    def test_system_check_rejects_reserved_metadata_even_if_receipt_is_current(self):
        extension = self.root / 'extension'
        (extension / '_metadata').mkdir(parents=True)
        m = {'tampermonkey': {'path': str(extension)}}
        with patch.object(components, 'manifest', return_value=m):
            with self.assertRaisesRegex(ValueError, 'prevents unpacked extension loading'):
                components.verify_system()

    def test_public_install_directories_are_readable_with_private_umask(self):
        system = self.root / 'opt/sparkclaw/browser-components'
        policy = self.root / 'etc/chromium/policies/managed/product.json'
        self.profile.chmod(0o700)
        secret = self.profile / 'private.key'
        secret.write_bytes(b'private fixture')
        secret.chmod(0o600)
        previous = os.umask(0o077)
        try:
            with patch.object(components, 'SYSTEM', system), patch.object(components, 'POLICY', policy), patch.object(components, 'POLICY_DIRECTORY_DEPTH', 3):
                components.install_public_directories()
                # Repair directories left by an earlier restricted installation.
                system.chmod(0o700)
                policy.parent.chmod(0o700)
                components.install_public_directories()
        finally:
            os.umask(previous)
        for directory in (system.parent, system, *list(policy.parents)[:3]):
            self.assertEqual(directory.stat().st_mode & 0o777, 0o755)
        self.assertEqual(self.profile.stat().st_mode & 0o777, 0o700)
        self.assertEqual(secret.stat().st_mode & 0o777, 0o600)

    def test_pinned_browser_brand_selects_its_actual_policy_location(self):
        for machine in ('x86_64', 'amd64'):
            path, depth = components.policy_location(machine)
            self.assertEqual(str(path), '/etc/opt/chrome_for_testing/policies/managed/sparkclaw-userscripts.json')
            self.assertEqual(depth, 4)
        for machine in ('aarch64', 'arm64'):
            path, depth = components.policy_location(machine)
            self.assertEqual(str(path), '/etc/chromium/policies/managed/sparkclaw-userscripts.json')
            self.assertEqual(depth, 3)
        with self.assertRaisesRegex(ValueError, 'architecture'):
            components.policy_location('unsupported')

    def test_cft_public_policy_ancestors_are_readable_with_private_umask(self):
        system = self.root / 'opt/sparkclaw/browser-components'
        policy = self.root / 'etc/opt/chrome_for_testing/policies/managed/product.json'
        self.profile.chmod(0o700)
        secret = self.profile / 'private.key'
        secret.write_bytes(b'private fixture')
        secret.chmod(0o600)
        previous = os.umask(0o077)
        try:
            with patch.object(components, 'SYSTEM', system), patch.object(components, 'POLICY', policy), patch.object(components, 'POLICY_DIRECTORY_DEPTH', 4):
                components.install_public_directories()
        finally:
            os.umask(previous)
        for directory in (system.parent, system, *list(policy.parents)[:4]):
            self.assertEqual(directory.stat().st_mode & 0o777, 0o755)
        self.assertEqual(self.profile.stat().st_mode & 0o777, 0o700)
        self.assertEqual(secret.stat().st_mode & 0o777, 0o600)

    def test_cft_public_policy_rejects_symlink_before_changing_private_target(self):
        system = self.root / 'public/browser-components'
        policy = self.root / 'etc/opt/chrome_for_testing/policies/managed/product.json'
        policy.parents[3].mkdir(parents=True)
        policy.parents[2].symlink_to(self.profile, target_is_directory=True)
        self.profile.chmod(0o700)
        with patch.object(components, 'SYSTEM', system), patch.object(components, 'POLICY', policy), patch.object(components, 'POLICY_DIRECTORY_DEPTH', 4):
            with self.assertRaisesRegex(ValueError, 'symlink'):
                components.install_public_directories()
        self.assertEqual(self.profile.stat().st_mode & 0o777, 0o700)

    def test_redeploy_reuses_verified_worker_but_still_prepares_profile(self):
        fixture = self.root / 'installer'
        scripts = fixture / 'scripts'
        scripts.mkdir(parents=True)
        shutil.copyfile(Path(__file__).with_name('install-browser-components.sh'), scripts / 'install-browser-components.sh')
        commands = fixture / 'bin'
        commands.mkdir()
        state = fixture / 'state.json'
        state.write_text(json.dumps({'verified': False, 'generation': 0, 'release': 'first', 'actions': []}))
        fake_python = commands / 'python3'
        fake_python.write_text('#!' + sys.executable + '\n' + '''import json,os,pathlib,sys
p=pathlib.Path(os.environ['COMPONENT_INSTALL_TEST_STATE'])
s=json.loads(p.read_text());action=sys.argv[2];s['actions'].append(action)
status=0
if action=='check':status=0 if s['verified'] else 1
elif action=='stage':s['generation']+=1
elif action=='install-system':s['verified']=True;s['worker']='worker-'+s['release']
p.write_text(json.dumps(s));sys.exit(status)
''')
        fake_python.chmod(0o755)
        sudo = commands / 'sudo'
        sudo.write_text('#!/bin/sh\n[ "$1" = -n ] && shift\nexec "$@"\n')
        sudo.chmod(0o755)
        systemctl = commands / 'systemctl'
        systemctl.write_text('#!/bin/sh\nexit 0\n')
        systemctl.chmod(0o755)
        env = dict(os.environ, PATH=str(commands) + os.pathsep + os.environ['PATH'],
                   COMPONENT_INSTALL_TEST_STATE=str(state), HOME=str(fixture))
        invoke = lambda: subprocess.run(['bash', str(scripts / 'install-browser-components.sh')], env=env, check=True, capture_output=True)
        invoke()
        first = json.loads(state.read_text())
        invoke()
        repeated = json.loads(state.read_text())
        self.assertEqual(repeated['worker'], first['worker'])
        self.assertEqual(repeated['actions'].count('stage'), 1)
        self.assertEqual(repeated['actions'].count('install-system'), 1)
        self.assertEqual(repeated['actions'].count('prepare-profile'), 2)
        # Repair public files by restaging the same artifact, not a new worker.
        repeated['verified'] = False
        state.write_text(json.dumps(repeated))
        invoke()
        changed = json.loads(state.read_text())
        self.assertEqual(changed['worker'], repeated['worker'])
        self.assertEqual(changed['actions'].count('stage'), 2)
        self.assertEqual(changed['actions'].count('install-system'), 2)
        self.assertEqual(changed['actions'].count('prepare-profile'), 3)
        changed.update(verified=False, release='next')
        state.write_text(json.dumps(changed))
        invoke()
        upgraded = json.loads(state.read_text())
        self.assertNotEqual(upgraded['worker'], changed['worker'])
        self.assertEqual(upgraded['actions'].count('stage'), 3)
        self.assertEqual(upgraded['actions'].count('install-system'), 3)
        self.assertEqual(upgraded['actions'].count('prepare-profile'), 4)

    def test_reuse_requires_safe_public_ownership_permissions_and_no_symlinks(self):
        system = self.root / 'opt/sparkclaw/browser-components'
        policy = self.root / 'etc/opt/chrome_for_testing/policies/managed/product.json'
        extension = system.parent / 'tampermonkey'
        with patch.object(components, 'SYSTEM', system), patch.object(components, 'POLICY', policy), patch.object(components, 'POLICY_DIRECTORY_DEPTH', 4):
            components.install_public_directories()
            extension.mkdir(mode=0o755)
            files = [extension / 'worker.js', system / 'receipt.json', policy]
            for path in files:
                path.write_text('public fixture')
                path.chmod(0o644)
            original = Path.lstat
            def root_stat(path):
                values = list(original(path))
                values[4] = 0
                return os.stat_result(values)
            with patch.object(Path, 'lstat', autospec=True, side_effect=root_stat):
                components.verify_public_permissions(extension)
                files[0].chmod(0o666)
                with self.assertRaisesRegex(ValueError, 'permissions'):
                    components.verify_public_permissions(extension)
                files[0].chmod(0o644)
                (extension / 'linked-worker.js').symlink_to(files[0])
                with self.assertRaisesRegex(ValueError, 'permissions'):
                    components.verify_public_permissions(extension)
                (extension / 'linked-worker.js').unlink()
            # A non-root test owner cannot impersonate a reusable installation.
            if os.getuid() != 0:
                with self.assertRaisesRegex(ValueError, 'ownership'):
                    components.verify_public_permissions(extension)

    def test_public_install_rejects_symlink_before_changing_target_permissions(self):
        system = self.root / 'public/browser-components'
        system.parent.mkdir()
        system.symlink_to(self.profile, target_is_directory=True)
        self.profile.chmod(0o700)
        with patch.object(components, 'SYSTEM', system), self.assertRaisesRegex(ValueError, 'symlink'):
            components.install_public_directories()
        self.assertEqual(self.profile.stat().st_mode & 0o777, 0o700)

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
            uid = entry['uuid']
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

    def test_retirement_checks_exact_managed_identity_and_preserves_user_copies(self):
        state, policy = self.valid_state()
        retired = components.RETIRED_MANAGED_UUIDS[0]
        state['!extdb.@meta#user-copy'] = {'name': 'ChatGPT Exporter', 'system': False, 'enabled': True}
        current = components.manifest()['scripts'][0]['uuid']
        state['!extdb.@meta#user-fork'] = {**state['!extdb.@meta#' + current], 'system': False}
        self.verify_with_state(state, policy)
        state['!extdb.@meta#' + retired] = {'system': True, 'enabled': True}
        with self.assertRaisesRegex(ValueError, 'retired managed'):
            self.verify_with_state(state, policy)
        state['!extdb.@meta#' + retired]['system'] = False
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
        meta_key = '!extdb.@meta#' + manifest['scripts'][0]['uuid']
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
            'foisted script': changed_meta('evilness', 12),
            'blacklisted script': changed_meta('evilness', 4),
            'non-system script': changed_meta('system', False),
            'wrong version': changed_meta('version', '0.0.0'),
            'uncontrolled updates': changed_meta('options', {'check_for_updates': True}),
            'missing update policy': changed_meta('options', {}),
            'changed source': lambda s: s.update({'!extdb.@source#' + manifest['scripts'][0]['uuid']: '// modified'}),
            'missing source': lambda s: s.pop('!extdb.@source#' + manifest['scripts'][0]['uuid']),
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
