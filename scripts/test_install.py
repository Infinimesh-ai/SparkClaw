import os
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
INSTALLER = ROOT / "install.sh"


class InstallerTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="sparkclaw-install-test-")
        self.addCleanup(self.temporary.cleanup)
        self.base = Path(self.temporary.name)
        self.source = self.base / "source"
        self.target = self.base / "target"
        self.deploy_record = self.base / "deploy-record"
        self.source.mkdir()
        (self.source / "scripts").mkdir()

        deploy = self.source / "scripts" / "deploy.sh"
        deploy.write_text(
            "#!/usr/bin/env bash\n"
            "set -euo pipefail\n"
            "printf '%s\\n' \"$*\" >\"$SPARKCLAW_TEST_DEPLOY_RECORD\"\n",
            encoding="utf-8",
        )
        deploy.chmod(deploy.stat().st_mode | stat.S_IXUSR)
        (self.source / "README.md").write_text("bootstrap fixture\n", encoding="utf-8")

        self.git("init", "-b", "main")
        self.git("add", ".")
        self.git(
            "-c",
            "user.name=SparkClaw Test",
            "-c",
            "user.email=test@example.invalid",
            "commit",
            "-m",
            "initial",
        )

        self.ssh_wrapper = self.base / "git-ssh-local"
        self.ssh_wrapper.write_text(
            "#!/usr/bin/env bash\n"
            "set -euo pipefail\n"
            "remote_command=\"${*: -1}\"\n"
            "exec bash -c \"$remote_command\"\n",
            encoding="utf-8",
        )
        self.ssh_wrapper.chmod(self.ssh_wrapper.stat().st_mode | stat.S_IXUSR)

    def git(self, *args, cwd=None):
        return subprocess.run(
            ["git", *args],
            cwd=cwd or self.source,
            check=True,
            capture_output=True,
            text=True,
            timeout=10,
        )

    def run_installer(self, *args, repository_url=None):
        environment = os.environ.copy()
        environment.update(
            {
                "GIT_SSH": str(self.ssh_wrapper),
                "GIT_SSH_VARIANT": "ssh",
                "HOME": str(self.base / "home"),
                "SPARKCLAW_BOOTSTRAP_TIMEOUT_SECONDS": "30",
                "SPARKCLAW_INSTALL_DIR": str(self.target),
                "SPARKCLAW_REPOSITORY_URL": repository_url
                or f"ssh://bootstrap-test{self.source}",
                "SPARKCLAW_TEST_DEPLOY_RECORD": str(self.deploy_record),
            }
        )
        return subprocess.run(
            ["bash", "-s", "--", *args],
            cwd=ROOT,
            env=environment,
            input=INSTALLER.read_text(encoding="utf-8"),
            capture_output=True,
            text=True,
            timeout=20,
        )

    def test_clone_rerun_fast_forward_and_dirty_refusal(self):
        first = self.run_installer("--check")
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertEqual(self.deploy_record.read_text(encoding="utf-8"), "--check\n")
        first_head = self.git("rev-parse", "HEAD").stdout.strip()
        target_head = self.git("rev-parse", "HEAD", cwd=self.target).stdout.strip()
        self.assertEqual(target_head, first_head)

        rerun = self.run_installer("--check")
        self.assertEqual(rerun.returncode, 0, rerun.stderr)
        rerun_head = self.git("rev-parse", "HEAD", cwd=self.target).stdout.strip()
        self.assertEqual(rerun_head, first_head)

        marker = self.source / "update-marker"
        marker.write_text("remote update\n", encoding="utf-8")
        self.git("add", "update-marker")
        self.git(
            "-c",
            "user.name=SparkClaw Test",
            "-c",
            "user.email=test@example.invalid",
            "commit",
            "-m",
            "update",
        )
        updated = self.run_installer("--check")
        self.assertEqual(updated.returncode, 0, updated.stderr)
        source_head = self.git("rev-parse", "HEAD").stdout.strip()
        target_head = self.git("rev-parse", "HEAD", cwd=self.target).stdout.strip()
        self.assertEqual(target_head, source_head)

        target_marker = self.target / "update-marker"
        target_marker.write_text("local change\n", encoding="utf-8")
        dirty = self.run_installer("--check")
        self.assertEqual(dirty.returncode, 1)
        self.assertIn("has local changes", dirty.stderr)
        self.assertEqual(target_marker.read_text(encoding="utf-8"), "local change\n")

    def test_rejects_insecure_or_credentialed_repository_urls(self):
        insecure = self.run_installer(
            "--check", repository_url="http://example.invalid/SparkClaw.git"
        )
        self.assertEqual(insecure.returncode, 1)
        self.assertIn("must use HTTPS or SSH", insecure.stderr)

        credentialed = self.run_installer(
            "--check", repository_url="https://secret@example.invalid/SparkClaw.git"
        )
        self.assertEqual(credentialed.returncode, 1)
        self.assertIn("credentials are not allowed", credentialed.stderr)

        query_secret = self.run_installer(
            "--check",
            repository_url="https://example.invalid/SparkClaw.git?token=secret",
        )
        self.assertEqual(query_secret.returncode, 1)
        self.assertIn("query strings and fragments are not allowed", query_secret.stderr)


if __name__ == "__main__":
    unittest.main()
