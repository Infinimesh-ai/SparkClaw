#!/usr/bin/env python3

from __future__ import annotations

import os
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
INSTALLER = ROOT / "install-cloud.sh"


class CloudInstallerTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.base = Path(self.temporary.name)
        self.source = self.base / "source"
        self.target = self.base / "target"
        self.record = self.base / "deploy-record"
        self.source.mkdir()
        deploy = self.source / "scripts" / "deploy_cloud_vm.sh"
        deploy.parent.mkdir()
        deploy.write_text(
            "#!/usr/bin/env bash\n"
            "printf '%s\\n' \"$*\" >>\"$SPARKCLAW_TEST_DEPLOY_RECORD\"\n",
            encoding="utf-8",
        )
        deploy.chmod(deploy.stat().st_mode | stat.S_IXUSR)
        (self.source / "README.md").write_text("cloud bootstrap fixture\n", encoding="utf-8")

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

    def git(self, *args: str, cwd: Path | None = None) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ["git", *args],
            cwd=cwd or self.source,
            check=True,
            capture_output=True,
            text=True,
            timeout=10,
        )

    def run_installer(self, *args: str) -> subprocess.CompletedProcess[str]:
        environment = os.environ.copy()
        environment.update(
            {
                "GIT_SSH": str(self.ssh_wrapper),
                "GIT_SSH_VARIANT": "ssh",
                "HOME": str(self.base / "home"),
                "SPARKCLAW_BOOTSTRAP_TIMEOUT_SECONDS": "30",
                "SPARKCLAW_INSTALL_DIR": str(self.target),
                "SPARKCLAW_REPOSITORY_URL": f"ssh://bootstrap-test{self.source}",
                "SPARKCLAW_TEST_DEPLOY_RECORD": str(self.record),
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

    def test_clone_rerun_and_configure_forwarding(self) -> None:
        first = self.run_installer("--check")
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertEqual(self.record.read_text(encoding="utf-8"), "--check\n")

        second = self.run_installer("--configure")
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertEqual(self.record.read_text(encoding="utf-8"), "--check\n--configure\n")

    def test_existing_dirty_checkout_is_not_overwritten(self) -> None:
        first = self.run_installer("--check")
        self.assertEqual(first.returncode, 0, first.stderr)
        marker = self.target / "README.md"
        marker.write_text("local edit\n", encoding="utf-8")

        dirty = self.run_installer("--check")
        self.assertEqual(dirty.returncode, 1)
        self.assertIn("has local changes", dirty.stderr)
        self.assertEqual(marker.read_text(encoding="utf-8"), "local edit\n")


if __name__ == "__main__":
    unittest.main()
