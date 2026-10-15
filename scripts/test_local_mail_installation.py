"""Execute the production installer against disposable host/service boundaries."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
EXECUTOR = 'sparkclaw-app-cli-executor.service'
CONTROLLER = 'sparkclaw-browser-controller.service'


class LocalMailInstallationTest(unittest.TestCase):
    def setUp(self):
        self.bash = next((candidate for candidate in [shutil.which('bash'), '/opt/homebrew/bin/bash']
                          if candidate and Path(candidate).exists() and subprocess.run(
                              [candidate, '-c', '(( BASH_VERSINFO[0] >= 4 ))'], capture_output=True).returncode == 0), None)
        if not self.bash or os.geteuid() == 0:
            self.skipTest('The owner installer requires Bash 4+ and a non-root test user')
        self.tmp = tempfile.TemporaryDirectory(prefix='mail-install-', dir='/tmp')
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.repo = self.root / 'repo'
        self.commands = self.root / 'bin'
        self.commands.mkdir()
        self.state_path = self.root / 'services.json'
        self.runtime = self.root / 'runtime'
        self.data = self.root / 'data'
        self.config = self.root / 'config'
        self.workspace = self.root / 'existing-workspace'
        self.workspace.mkdir()
        self.journal = self.workspace / 'send-journal.json'
        self.journal.write_text('{"request_id":"original-send","state":"unknown"}')
        self.environment = self.repo / '.env.local'
        for relative in ['scripts/setup-browser-controller.sh', 'scripts/lib/dotenv.sh',
                         'scripts/sync-mail-projections.mjs', 'scripts/local-mail-assets.mjs',
                         'configs/browser-components.json',
                         'services/gateway/internal/emailautomation/provider_scripts.json',
                         'tools/browser-controller/package.json', 'tools/browser-controller/package-lock.json']:
            target = self.repo / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(ROOT / relative, target)
        package = self.repo / 'tools/browser-controller'
        (package / 'mail-runtime').symlink_to(ROOT / 'tools/browser-controller/mail-runtime', target_is_directory=True)
        (package / 'src').mkdir()
        for filename in ['main.mjs', 'browser-bridge-launcher.mjs', 'browser-bridge-native-host.mjs',
                         'browser-bridge-focus.py', 'install-playwright-downloads.mjs']:
            path = package / 'src' / filename
            path.write_text('// Test external browser boundary.\n' if filename.endswith('.mjs') else '# fixture\n')
            path.chmod(0o700)
        browser = self.commands / 'browser'
        browser.write_text('#!/bin/sh\nexit 0\n')
        browser.chmod(0o700)
        browser_config = self.config / 'sparkclaw/browser.json'
        browser_config.parent.mkdir(parents=True)
        browser_config.write_text(json.dumps({'executable': str(browser)}))
        self.environment.write_text(f'SPARKCLAW_BROWSER_EMAIL_WORKSPACE_ROOT={self.workspace}\nUNCHANGED=value\n')
        self.install_command('uname', "print('Linux')")
        self.install_command('npm', """import json, os, sys
p=Path(os.environ['MAIL_INSTALL_TEST_STATE']);s=json.loads(p.read_text())
s['calls'].append(['npm', *sys.argv[1:]]);p.write_text(json.dumps(s))
if sys.argv[1:]==['--version']: print('11.4.2')
elif not ('ci' in sys.argv or 'ls' in sys.argv): sys.exit(96)
""")
        self.install_command('stat', """import sys
assert sys.argv[1:3]==['-c','%u:%a']
s=Path(sys.argv[3]).lstat();print(f'{s.st_uid}:{s.st_mode & 0o777:o}')
""")
        self.install_command('realpath', "import sys\nassert sys.argv[1]=='-m'\nprint(Path(sys.argv[2]).resolve())")
        self.install_command('curl', "print('{\"schema_version\":1,\"profile_id\":\"default\",\"state\":\"ready\",\"versions\":{\"app_cli\":\"local-mail-runtime\"}}')")
        self.install_command('systemctl', """import json, os, socket, sys
p=Path(os.environ['MAIL_INSTALL_TEST_STATE']);s=json.loads(p.read_text())
args=sys.argv[1:];assert args.pop(0)=='--user';action=args.pop(0);unit=args[-1] if args else ''
s['calls'].append([action, unit]);status=0
if action=='cat': status=0 if unit in s['installed'] else 1
elif action=='is-active': status=0 if unit in s['active'] else 3
elif action=='is-enabled': status=0 if unit in s['enabled'] else 1
elif action in ['stop','disable']:
    if s.get('fail')==action+' '+unit: status=19
    elif unit in s['active' if action=='stop' else 'enabled']:
        s['active' if action=='stop' else 'enabled'].remove(unit)
elif action=='enable':
    assert len(args)==1
    if unit not in s['enabled']:s['enabled'].append(unit)
elif action=='restart':
    if unit not in s['active']:s['active'].append(unit)
    if unit=='sparkclaw-browser-controller.service':
        sock=Path(os.environ['XDG_RUNTIME_DIR'])/'sparkclaw/browser-controller/controller.sock'
        sock.unlink(missing_ok=True);handle=socket.socket(socket.AF_UNIX);handle.bind(str(sock));sock.chmod(0o600)
elif action!='daemon-reload': status=97
p.write_text(json.dumps(s));sys.exit(status)
""")
        self.env = dict(os.environ, PATH=str(self.commands) + os.pathsep + os.environ['PATH'],
                        HOME=str(self.root), XDG_DATA_HOME=str(self.data), XDG_CONFIG_HOME=str(self.config),
                        XDG_RUNTIME_DIR=str(self.runtime), MAIL_INSTALL_TEST_STATE=str(self.state_path))
        for key in list(self.env):
            if key.startswith('SPARKCLAW_'):
                del self.env[key]

    def install_command(self, name, program):
        path = self.commands / name
        path.write_text('#!' + sys.executable + '\nfrom pathlib import Path\n' + program + '\n')
        path.chmod(0o700)

    def run_installer(self, *args):
        return subprocess.run([self.bash, str(self.repo / 'scripts/setup-browser-controller.sh'), *args],
                              env=self.env, capture_output=True, text=True, timeout=30)

    def state(self):
        return json.loads(self.state_path.read_text())

    def seed(self, legacy=False, failure=''):
        installed = [EXECUTOR, CONTROLLER] if legacy else []
        self.state_path.write_text(json.dumps({'installed': installed, 'enabled': installed,
                                               'active': installed, 'calls': [], 'fail': failure}))
        self.legacy_unit = self.config / 'systemd/user' / EXECUTOR
        self.legacy_data = self.data / 'sparkclaw/app-cli/ledger.sqlite'
        for path, content in [(self.legacy_unit, 'retained executor unit'), (self.legacy_data, 'retained ledger')]:
            if legacy:
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(content)

    def test_fresh_install_and_check_use_repository_mail_with_durable_state(self):
        self.seed()
        self.assert_installed()
        self.assertFalse(self.legacy_unit.exists())
        self.assertFalse(self.legacy_data.exists())

    def test_upgrade_retires_executor_and_preserves_all_existing_data(self):
        self.seed(legacy=True)
        self.assert_installed()
        self.assertEqual(self.legacy_unit.read_text(), 'retained executor unit')
        self.assertEqual(self.legacy_data.read_text(), 'retained ledger')
        calls = self.state()['calls']
        self.assertLess(calls.index(['stop', EXECUTOR]), calls.index(['disable', EXECUTOR]))
        self.assertLess(calls.index(['disable', EXECUTOR]), calls.index(['stop', CONTROLLER]))
        self.assertLess(calls.index(['stop', CONTROLLER]), next(i for i, call in enumerate(calls) if call[0]=='npm' and 'ci' in call))

    def assert_installed(self):
        result = self.run_installer()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        local_state = self.data / 'sparkclaw/browser-controller/mail-runtime'
        self.assertTrue(local_state.is_dir())
        self.assertEqual(local_state.stat().st_mode & 0o777, 0o700)
        unit = (self.config / 'systemd/user' / CONTROLLER).read_text()
        self.assertIn(f'SPARKCLAW_MAIL_RUNTIME_STATE_DIR={local_state}', unit)
        self.assertIn(f'SPARKCLAW_BROWSER_EMAIL_WORKSPACE_ROOT={self.workspace.resolve()}', unit)
        self.assertNotIn('APP_CLI', unit)
        self.assertNotIn('app-cli', unit)
        self.assertEqual(self.journal.read_text(), '{"request_id":"original-send","state":"unknown"}')
        self.assertIn('UNCHANGED=value\n', self.environment.read_text())
        self.assertNotIn(EXECUTOR, self.state()['active'])
        self.assertNotIn(EXECUTOR, self.state()['enabled'])
        self.assertIn(CONTROLLER, self.state()['active'])
        self.assertIn(CONTROLLER, self.state()['enabled'])
        # Re-running install or a check cannot erase durable recovery fences.
        recovery = local_state / 'recovery.json'
        recovery.write_text('{"recovery":"preserved"}')
        for arguments in [('--check',), ()]:
            result = self.run_installer(*arguments)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(recovery.read_text(), '{"recovery":"preserved"}')

    def test_stop_or_disable_failure_prevents_installation_and_restart(self):
        for failure in ['stop ' + EXECUTOR, 'disable ' + EXECUTOR, 'stop ' + CONTROLLER]:
            with self.subTest(failure=failure):
                self.seed(legacy=True, failure=failure)
                result = self.run_installer()
                self.assertEqual(result.returncode, 19, result.stdout + result.stderr)
                calls = self.state()['calls']
                self.assertFalse(any(call[0]=='restart' or call[0]=='npm' and 'ci' in call for call in calls))
                self.assertEqual(self.legacy_data.read_text(), 'retained ledger')
                self.assertEqual(self.legacy_unit.read_text(), 'retained executor unit')


class LocalMailProjectionTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='mail-projections-')
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        for relative in ['scripts/sync-mail-projections.mjs', 'scripts/local-mail-assets.mjs',
                         'configs/browser-components.json',
                         'services/gateway/internal/emailautomation/provider_scripts.json']:
            target = self.root / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(ROOT / relative, target)
        package = self.root / 'tools/browser-controller'
        package.mkdir(parents=True)
        shutil.copytree(ROOT / 'tools/browser-controller/mail-runtime', package / 'mail-runtime')
        (package / 'node_modules').symlink_to(ROOT / 'tools/browser-controller/node_modules', target_is_directory=True)

    def check(self):
        return subprocess.run(['node', str(self.root / 'scripts/sync-mail-projections.mjs'), '--check'],
                              capture_output=True, text=True, timeout=20)

    def test_local_generation_and_consumers_need_no_release_archive(self):
        result = self.check()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse((self.root / 'vendor').exists())

    def test_source_bindings_userscripts_and_consumer_drift_are_rejected(self):
        for relative, expected in [
            ('tools/browser-controller/mail-runtime/applications/mail/qqmail-send.mjs', 'Stale generated artifact'),
            ('tools/browser-controller/mail-runtime/bindings/mail-gmail.json', 'Stale generated artifact'),
            ('tools/browser-controller/mail-runtime/assets/mail/qq-mail-reader.user.js', 'checksum mismatch'),
            ('configs/browser-components.json', 'projection is stale'),
            ('services/gateway/internal/emailautomation/provider_scripts.json', 'projection is stale'),
        ]:
            with self.subTest(relative=relative):
                target = self.root / relative
                original = target.read_bytes()
                target.write_bytes(original + b'\n')
                try:
                    result = self.check()
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn(expected, result.stderr)
                finally:
                    target.write_bytes(original)


if __name__ == '__main__':
    unittest.main()
