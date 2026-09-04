import json
import os
from pathlib import Path
import re
import stat
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
INSTALLER = ROOT / "install.sh"
CANONICAL_REPOSITORY_URL = "https://github.com/Infinimesh-ai/SparkClaw.git"
LEGACY_REPOSITORY_URL = "https://github.com/Chiiz0/SparkClaw.git"
CANONICAL_RAW_INSTALLER_URL = (
    "https://raw.githubusercontent.com/Infinimesh-ai/SparkClaw/main/install.sh"
)


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

        deploy = self.source / "scripts" / "deploy_local.sh"
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

    def run_installer(self, *args, repository_url=None, use_default_repository=False):
        environment = os.environ.copy()
        environment.update(
            {
                "GIT_SSH": str(self.ssh_wrapper),
                "GIT_SSH_VARIANT": "ssh",
                "HOME": str(self.base / "home"),
                "SPARKCLAW_BOOTSTRAP_TIMEOUT_SECONDS": "30",
                "SPARKCLAW_INSTALL_DIR": str(self.target),
                "SPARKCLAW_TEST_DEPLOY_RECORD": str(self.deploy_record),
            }
        )
        if use_default_repository:
            environment.pop("SPARKCLAW_REPOSITORY_URL", None)
            environment.update(
                {
                    "GIT_CONFIG_COUNT": "1",
                    "GIT_CONFIG_KEY_0": (
                        f"url.ssh://bootstrap-test{self.source}.insteadOf"
                    ),
                    "GIT_CONFIG_VALUE_0": CANONICAL_REPOSITORY_URL,
                }
            )
        else:
            environment["SPARKCLAW_REPOSITORY_URL"] = (
                repository_url or f"ssh://bootstrap-test{self.source}"
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

    def test_default_repository_clones_canonical_origin(self):
        result = self.run_installer("--check", use_default_repository=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        origin = self.git("remote", "get-url", "origin", cwd=self.target).stdout.strip()
        self.assertEqual(origin, CANONICAL_REPOSITORY_URL)

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

    def test_migrates_clean_legacy_default_origin(self):
        first = self.run_installer("--check")
        self.assertEqual(first.returncode, 0, first.stderr)
        self.git("remote", "set-url", "origin", LEGACY_REPOSITORY_URL, cwd=self.target)

        migrated = self.run_installer("--check", use_default_repository=True)
        self.assertEqual(migrated.returncode, 0, migrated.stderr)
        self.assertIn("migrated the legacy origin", migrated.stdout)
        origin = self.git("remote", "get-url", "origin", cwd=self.target).stdout.strip()
        self.assertEqual(origin, CANONICAL_REPOSITORY_URL)

    def test_deployment_surfaces_use_canonical_repository(self):
        installer = INSTALLER.read_text(encoding="utf-8")
        default_match = re.search(
            r'^DEFAULT_REPOSITORY_URL="([^"]+)"$',
            installer,
            re.MULTILINE,
        )
        self.assertIsNotNone(default_match)
        self.assertEqual(default_match.group(1), CANONICAL_REPOSITORY_URL)

        for relative_path in (
            "README.md",
            "zh-cn/README.md",
            "docs/deployment.md",
            "zh-cn/docs/deployment.md",
        ):
            with self.subTest(path=relative_path):
                text = (ROOT / relative_path).read_text(encoding="utf-8")
                self.assertIn(CANONICAL_RAW_INSTALLER_URL, text)
                self.assertNotIn("Chiiz0/SparkClaw", text)

        package = json.loads((ROOT / "package.json").read_text(encoding="utf-8"))
        self.assertEqual(
            package["homepage"],
            CANONICAL_REPOSITORY_URL.removesuffix(".git") + "#readme",
        )
        self.assertEqual(package["repository"]["url"], "git+" + CANONICAL_REPOSITORY_URL)
        self.assertEqual(
            package["bugs"]["url"],
            CANONICAL_REPOSITORY_URL.removesuffix(".git") + "/issues",
        )


if __name__ == "__main__":
    unittest.main()
