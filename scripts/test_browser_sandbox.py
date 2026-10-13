"""Sandbox qualification checks without changing the host kernel or policy."""
import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parent))
import browser_sandbox as sandbox


class BrowserSandboxTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.policy = self.root / 'policy'
        self.policy.mkdir()
        self.name, self.executable, self.text = sandbox.profile('148.0.7778.0', '5.0')
        self.target = self.policy / self.name
        self.calls = []

    def fake_parser(self, args, check):
        self.calls.append(args)
        self.assertTrue(check)
        self.assertEqual(Path(args[-1]).read_text(), self.text)

    def settings(self):
        return (
            patch.object(sandbox, 'guarded', return_value='1'),
            patch.object(sandbox.os, 'geteuid', return_value=0),
            patch.object(sandbox, 'POLICY_DIR', self.policy),
            patch.object(sandbox, 'expected_profile', return_value=(self.name, self.executable, self.target, self.text)),
            patch.object(sandbox, 'root_owned_path', side_effect=lambda p, directory=False: Path(p)),
            patch.object(sandbox, 'verify_loaded'),
            patch.object(sandbox.subprocess, 'run', side_effect=self.fake_parser),
        )

    def apply(self, check=False):
        from contextlib import ExitStack
        with ExitStack() as stack:
            for fixture in self.settings():
                stack.enter_context(fixture)
            return sandbox.configure(check)

    def test_install_atomic_public_policy_under_private_umask_and_idempotent_check(self):
        before = os.umask(0o077)
        try:
            self.assertEqual(self.apply()['state'], 'installed')
        finally:
            os.umask(before)
        self.assertEqual(stat.S_IMODE(self.target.stat().st_mode), 0o644)
        self.assertEqual(self.target.read_text(), self.text)
        self.assertEqual([call[1:3] for call in self.calls], [['-Q', '-T'], ['-r', '-W']])
        self.calls.clear()
        self.assertEqual(self.apply(True)['state'], 'verified')
        self.assertEqual(self.calls, [])
        self.assertEqual(list(self.policy.iterdir()), [self.target])

    def test_unmanaged_policy_is_preserved(self):
        self.target.write_text('operator policy')
        with self.assertRaisesRegex(ValueError, 'unmanaged'):
            self.apply()
        self.assertEqual(self.target.read_text(), 'operator policy')
        self.assertEqual(self.calls, [])

    def test_invalid_parse_never_publishes_or_replaces_existing_policy(self):
        self.target.write_text(sandbox.MARKER + 'old policy')
        fixtures = self.settings()
        from contextlib import ExitStack
        with ExitStack() as stack:
            for fixture in fixtures:
                stack.enter_context(fixture)
            with patch.object(sandbox.subprocess, 'run', side_effect=subprocess.CalledProcessError(1, 'parse')):
                with self.assertRaises(subprocess.CalledProcessError):
                    sandbox.configure()
        self.assertEqual(self.target.read_text(), sandbox.MARKER + 'old policy')
        self.assertEqual(list(self.policy.iterdir()), [self.target])

    def test_check_rejects_missing_stale_or_unsafe_policy(self):
        with self.assertRaises(FileNotFoundError):
            self.apply(True)
        self.target.write_text(self.text)
        self.target.chmod(0o600)
        with self.assertRaisesRegex(ValueError, 'stale or unsafe'):
            self.apply(True)
        self.target.chmod(0o644)
        self.target.write_text(self.text + '# stale')
        with self.assertRaisesRegex(ValueError, 'stale or unsafe'):
            self.apply(True)

    def test_exact_pinned_path_without_wildcard_or_global_toggle(self):
        self.assertIn('"/opt/sparkclaw/chromium-148.0.7778.0/chrome"', self.text)
        self.assertIn('  userns,', self.text)
        self.assertNotIn('*', self.text)
        self.assertNotIn('sysctl', self.text)
        self.assertNotIn('no-sandbox', self.text)
        for version in ('148.*', '148.0.0.0" /tmp/evil', '../chrome'):
            with self.assertRaises(ValueError):
                sandbox.profile(version, '5.0')

    def test_root_owned_chain_rejects_symlink_or_writable_ancestor(self):
        # Simulate a real root-owned /opt chain; do not require root test runners.
        good = lambda kind=stat.S_IFDIR, mode=0o755, uid=0: os.stat_result((kind | mode, 0, 0, 0, uid, 0, 0, 0, 0, 0))
        path = Path('/opt/sparkclaw/chrome')
        with patch.object(Path, 'lstat', side_effect=lambda p=None: good(stat.S_IFREG) ):
            # All-directory ancestors are validated separately below.
            with self.assertRaisesRegex(ValueError, 'file type'):
                sandbox.root_owned_path(path)
        for bad, error in ((good(stat.S_IFLNK), 'symlink'), (good(mode=0o777), 'root-owned'), (good(uid=1000), 'root-owned')):
            def info(item):
                return good(stat.S_IFREG) if item == path else bad if item == path.parent else good()
            with patch.object(Path, 'lstat', autospec=True, side_effect=info):
                with self.assertRaisesRegex(ValueError, error):
                    sandbox.root_owned_path(path)
        with patch.object(Path, 'lstat', autospec=True, side_effect=lambda item: good(stat.S_IFREG) if item == path else good()):
            self.assertEqual(sandbox.root_owned_path(path), path)

    def test_missing_or_disabled_restriction_never_changes_policy(self):
        for value in (None, '0'):
            with patch.object(sandbox, 'guarded', return_value=value), patch.object(sandbox, 'expected_profile') as expected:
                self.assertEqual(sandbox.configure()['state'], 'not-required')
                expected.assert_not_called()

    def test_global_restriction_changes_fail_closed(self):
        from contextlib import ExitStack
        with ExitStack() as stack:
            for fixture in self.settings():
                stack.enter_context(fixture)
            with patch.object(sandbox, 'guarded', side_effect=['1', '0']):
                with self.assertRaisesRegex(ValueError, 'global AppArmor'):
                    sandbox.configure()

    def test_loaded_profile_has_exact_name_and_mode(self):
        profiles = self.root / 'profiles'
        with patch.object(sandbox, 'PROFILES', profiles):
            profiles.write_text(self.name + ' (enforce)\n')
            with self.assertRaisesRegex(ValueError, 'not loaded'):
                sandbox.verify_loaded(self.name)
            profiles.write_text(self.name + ' (unconfined)\n')
            sandbox.verify_loaded(self.name)

    def test_manifest_pin_and_existing_legacy_attachment_are_checked(self):
        version, arch, digest = '148.0.7778.0', 'x86_64', 'a' * 64
        install = self.root / 'chromium-148.0.7778.0'
        install.mkdir()
        exe = install / 'chrome'
        exe.write_text('fixture executable')
        exe.chmod(0o755)
        receipt = install / 'sparkclaw-manifest.json'
        receipt.write_text(json.dumps({'artifactVersion': version, 'architecture': arch, 'archiveSHA256': digest}))
        (self.policy / 'abi').mkdir()
        (self.policy / 'abi/5.0').touch()
        with patch.object(sandbox, 'POLICY_DIR', self.policy), patch.object(sandbox, 'pin', return_value=(version, arch, digest)), patch.object(sandbox, 'profile', return_value=(self.name, exe, self.text)), patch.object(sandbox, 'root_owned_path', side_effect=lambda p, directory=False: Path(p)):
            sandbox.expected_profile()
            receipt.write_text(json.dumps({'artifactVersion': version, 'architecture': arch, 'archiveSHA256': 'b' * 64}))
            with self.assertRaisesRegex(ValueError, 'verified pinned'):
                sandbox.expected_profile()
            receipt.write_text(json.dumps({'artifactVersion': version, 'architecture': arch, 'archiveSHA256': digest}))
            (self.policy / 'sparkclaw-chromium').write_text('profile old ' + str(exe) + ' { userns, }')
            with self.assertRaisesRegex(ValueError, 'legacy'):
                sandbox.expected_profile()


if __name__ == '__main__':
    unittest.main()
