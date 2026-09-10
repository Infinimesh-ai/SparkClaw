"""Exercise deployment lifecycle without Docker, networking or real product data."""
import datetime
import json
from pathlib import Path
import runpy
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
api = runpy.run_path(str(ROOT / 'scripts/record-deployment.py'))


class EmailDeploymentTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.workspace = self.root / 'data/workspaces'
        self.workspace.mkdir(parents=True)
        (self.workspace / '.gitkeep').touch()  # A real fresh clone already has this.
        (self.root / 'scripts').mkdir()
        shutil.copy(ROOT / 'scripts/record-deployment.py', self.root / 'scripts')
        self.docker = self.root / 'docker'
        self.docker.write_text('#!/bin/bash\nset -e\n[[ ! -f "${0%/*}/docker-unavailable" ]]\nif [[ "$1" == volume && -f "${0%/*}/old-volume" ]]; then echo sparkclaw_pg18; fi\nif [[ "$1" == ps && -f "${0%/*}/old-container" ]]; then echo container-id; fi\nexit 0\n')
        self.docker.chmod(0o700)
        self.boundary = self.workspace / '.sparkclaw-deployment.json'
        self.at = datetime.datetime(2026, 9, 10, 9, tzinfo=datetime.timezone.utc)

    def begin(self):
        subprocess.run(['bash', '-c', 'set -e; source "$1"; sparkclaw_begin_email_deployment "$2" "$3"', 'deployment-test', str(ROOT / 'scripts/lib/email-deployment.sh'), str(self.root), str(self.docker)], check=True, capture_output=True)

    def test_fresh_clone_records_only_success(self):
        self.begin()
        self.assertFalse(self.boundary.exists())
        api['complete'](self.workspace, self.at)
        self.assertEqual(json.loads(self.boundary.read_text())['started_at'], self.at.isoformat())

    def test_failed_fresh_deployment_retry_does_not_become_legacy(self):
        failed = subprocess.run(['bash', '-c',
            'set -e; source "$1"; sparkclaw_begin_email_deployment "$2" "$3"; touch "$2/old-volume"; false; python3 "$2/scripts/record-deployment.py" complete "$2/data/workspaces"',
            'failed-deployment', str(ROOT / 'scripts/lib/email-deployment.sh'), str(self.root), str(self.docker)], capture_output=True)
        self.assertNotEqual(failed.returncode, 0)
        # Services created state before failing readiness; complete did not run.
        self.assertFalse(self.boundary.exists())
        self.begin()
        api['complete'](self.workspace, self.at)
        self.assertEqual(json.loads(self.boundary.read_text())['started_at'], self.at.isoformat())

    def test_legacy_deployment_without_record_uses_first_enable_fallback(self):
        for evidence in ('old-volume', 'old-container'):
            with self.subTest(evidence=evidence):
                (self.workspace / '.sparkclaw-deployment-state.json').unlink(missing_ok=True)
                (self.root / evidence).touch()
                self.begin()
                api['complete'](self.workspace, self.at)
                self.assertFalse(self.boundary.exists())
                (self.root / evidence).unlink()
        memory = self.root / 'data/memory'
        memory.mkdir()
        (memory / 'gateway-credentials.key').write_text('fixture')
        (self.workspace / '.sparkclaw-deployment-state.json').unlink()
        self.begin()
        api['complete'](self.workspace, self.at)
        self.assertFalse(self.boundary.exists())

    def test_upgrade_restart_and_repeated_success_preserve_first_boundary(self):
        self.begin()
        api['complete'](self.workspace, self.at)
        before = self.boundary.read_bytes()
        (self.root / 'old-volume').touch()
        self.begin()
        api['complete'](self.workspace, self.at + datetime.timedelta(days=5))
        self.assertEqual(self.boundary.read_bytes(), before)
        self.assertEqual(list(self.workspace.glob('.deployment-*')), [])

    def test_preexisting_record_survives_lifecycle_migration(self):
        self.boundary.write_text('{"started_at":"2026-08-01T00:00:00Z"}\n')
        before = self.boundary.read_bytes()
        self.begin()
        api['complete'](self.workspace, self.at)
        self.assertEqual(self.boundary.read_bytes(), before)

    def test_unavailable_docker_cannot_be_interpreted_as_fresh(self):
        (self.root / 'docker-unavailable').touch()
        with self.assertRaises(subprocess.CalledProcessError):
            self.begin()
        self.assertFalse((self.workspace / '.sparkclaw-deployment-state.json').exists())
        self.assertFalse(self.boundary.exists())

    def test_both_deploy_entrypoints_place_success_after_service_start(self):
        for profile in ('local', 'remote'):
            source = (ROOT / f'scripts/deploy_{profile}.sh').read_text()
            begin = source.index('sparkclaw_begin_email_deployment "$ROOT"')
            complete = source.index('record-deployment.py" complete')
            self.assertLess(begin, source.index(f'start_{profile}_compose.sh', begin))
            self.assertLess(source.index(f'start_{profile}_compose.sh', begin), complete)
            self.assertRegex(source, r'set -[Ee]*euo pipefail')
            self.assertNotIn('email_new_install', source)


if __name__ == '__main__':
    unittest.main()
