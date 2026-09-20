#!/usr/bin/env python3

from __future__ import annotations

import base64
import os
from pathlib import Path
import stat
import subprocess
import tempfile
import textwrap
import unittest


ROOT = Path(__file__).resolve().parents[1]
DOTENV = ROOT / "scripts" / "lib" / "dotenv.sh"
PROFILE = ROOT / "scripts" / "lib" / "deployment-profile.sh"


FAKE_DOCKER = r"""#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == *"volume ls"* && "${SPARKCLAW_TEST_EXISTING_POSTGRES:-false}" == true ]]; then
  printf 'sparkclaw_sparkclaw_pg18\n'
fi
"""


class CredentialKeyGuardTest(unittest.TestCase):
    def run_guard(
        self,
        *,
        existing_postgres: bool,
        key: bytes | None = None,
        key_mode: int = 0o600,
    ) -> subprocess.CompletedProcess[str]:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "relocated-install"
            memory = root / "data" / "memory"
            memory.mkdir(parents=True)
            env_file = root / "effective.env"
            env_file.write_text("SPARKCLAW_CREDENTIAL_KEY=\n", encoding="utf-8")
            if key is not None:
                key_file = memory / "gateway-credentials.key"
                key_file.write_text(
                    base64.b64encode(key).decode("ascii").rstrip("=") + "\n",
                    encoding="utf-8",
                )
                key_file.chmod(key_mode)
            docker = Path(directory) / "docker"
            docker.write_text(textwrap.dedent(FAKE_DOCKER), encoding="utf-8")
            docker.chmod(docker.stat().st_mode | stat.S_IXUSR)
            environment = os.environ.copy()
            environment["SPARKCLAW_TEST_EXISTING_POSTGRES"] = (
                "true" if existing_postgres else "false"
            )
            command = (
                f"source {DOTENV!s}; source {PROFILE!s}; "
                'sparkclaw_guard_credential_key "$1" "$2" "$3"'
            )
            return subprocess.run(
                ["bash", "-c", command, "guard", str(root), str(env_file), str(docker)],
                env=environment,
                check=False,
                capture_output=True,
                text=True,
            )

    def test_relocated_install_refuses_existing_database_without_key(self) -> None:
        result = self.run_guard(existing_postgres=True)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("restore data/memory/gateway-credentials.key", result.stderr)
        self.assertIn("do not generate a replacement key", result.stderr)

    def test_first_install_without_database_may_create_key_in_gateway(self) -> None:
        result = self.run_guard(existing_postgres=False)

        self.assertEqual(result.returncode, 0, result.stderr)

    def test_restored_raw_base64_key_allows_existing_database(self) -> None:
        result = self.run_guard(existing_postgres=True, key=b"k" * 32)

        self.assertEqual(result.returncode, 0, result.stderr)

    def test_invalid_or_broad_key_file_is_rejected(self) -> None:
        invalid = self.run_guard(existing_postgres=True, key=b"short")
        broad = self.run_guard(existing_postgres=True, key=b"k" * 32, key_mode=0o644)

        self.assertNotEqual(invalid.returncode, 0)
        self.assertNotEqual(broad.returncode, 0)
        self.assertIn("exactly 32 bytes", invalid.stderr)
        self.assertIn("0600 or stricter", broad.stderr)


if __name__ == "__main__":
    unittest.main()
