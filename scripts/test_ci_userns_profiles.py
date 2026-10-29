import importlib.util
from pathlib import Path
import tempfile
import subprocess
import json
import unittest

spec = importlib.util.spec_from_file_location('ci_userns', Path(__file__).with_name('configure-ci-userns.py'))
ci_userns = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ci_userns)


class CIUserNamespaceProfiles(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()

    def executable(self, name):
        path = self.root / name
        path.write_text('synthetic executable')
        path.chmod(0o755)
        return path

    def test_exact_canonical_attachment_and_unique_profile(self):
        executable = self.executable('electron')
        alias = self.root / 'alias'
        alias.symlink_to(executable)
        name, resolved, profile = ci_userns.profile_for(alias)
        self.assertEqual(resolved, executable)
        self.assertEqual(ci_userns.profile_for(executable), (name, resolved, profile))
        self.assertIn(f'profile {name} "{executable}" flags=(unconfined)', profile)
        self.assertIn('  userns,\n', profile)
        self.assertNotIn(str(alias), profile)
        self.assertNotEqual(name, ci_userns.profile_for(self.executable('bwrap'))[0])

    def test_profile_syntax_cannot_be_injected_through_a_filename(self):
        for name in ['electron*', 'electron?', 'electron[0]', 'electron{a,b}', 'electron"', 'electron\\', 'electron\nuserns,']:
            with self.subTest(name=name), self.assertRaises(ValueError):
                ci_userns.profile_for(self.executable(name))

    def test_cold_electron_installer_stdout_never_becomes_an_attachment(self):
        executable = self.executable('electron-binary')
        module = self.root / 'electron-module.cjs'
        module.write_text(
            "const fs=require('node:fs'),cp=require('node:child_process');"
            "const marker=__filename+'.ready';if(!fs.existsSync(marker)){"
            "console.log('Downloading Electron binary...');"
            '''cp.execFileSync(process.execPath,['-e',"console.log('child installer progress')"],{stdio:'inherit'});'''
            "fs.writeFileSync(marker,'ready');}module.exports=" + json.dumps(str(executable)) + ';'
        )
        resolver = Path(__file__).with_name('resolve-ci-electron.sh')
        result = subprocess.run(['bash', str(resolver), str(module)], capture_output=True, text=True, check=True)
        self.assertEqual(result.stdout, str(executable) + '\n')
        self.assertIn('Downloading Electron binary...', result.stderr)
        self.assertIn('child installer progress', result.stderr)
        self.assertEqual(ci_userns.profile_for(result.stdout.strip())[1], executable)

    def test_electron_install_failure_returns_no_path(self):
        module = self.root / 'failed-install.cjs'
        module.write_text("console.log('Downloading Electron binary...');throw Error('synthetic install failure')")
        resolver = Path(__file__).with_name('resolve-ci-electron.sh')
        result = subprocess.run(['bash', str(resolver), str(module)], capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, '')
        self.assertIn('synthetic install failure', result.stderr)

    def test_missing_directory_and_non_executable_targets_are_rejected(self):
        with self.assertRaises(FileNotFoundError):
            ci_userns.profile_for(self.root / 'missing')
        with self.assertRaises(ValueError):
            ci_userns.profile_for(self.root)
        target = self.executable('not-executable')
        target.chmod(0o644)
        with self.assertRaises(ValueError):
            ci_userns.profile_for(target)


if __name__ == '__main__':
    unittest.main()
