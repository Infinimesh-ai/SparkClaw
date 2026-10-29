import importlib.util
from pathlib import Path
import tempfile
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
