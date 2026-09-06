#!/usr/bin/env python3

from __future__ import annotations

import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import textwrap
import unittest


sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "qualify-playwright-email.sh"
PROXY_TOKEN = "qualification_test_proxy_token_0123456789abcdefghijklmnop"

FAKE_GO = r"""#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

payload = {
    "argv": sys.argv[1:],
    "cwd": os.getcwd(),
    "providers": os.environ.get("SPARKCLAW_TEST_PLAYWRIGHT_EMAIL_PROVIDERS", ""),
    "config": os.environ.get("SPARKCLAW_TEST_CONFIG", ""),
    "controller_socket": os.environ.get("SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET", ""),
    "credential_key_file": os.environ.get("SPARKCLAW_CREDENTIAL_KEY_FILE", ""),
    "credential_key_present": bool(os.environ.get("SPARKCLAW_CREDENTIAL_KEY", "")),
    "state_dsn_has_host": "@127.0.0.1:15432/" in os.environ.get("SPARKCLAW_STATE_DSN", ""),
    "state_dsn_has_container": "@postgres:5432/" in os.environ.get("SPARKCLAW_STATE_DSN", ""),
    "postgres_dsn_present": bool(os.environ.get("SPARKCLAW_POSTGRES_DSN", "")),
}
Path(os.environ["QUALIFICATION_TEST_GO_LOG"]).write_text(json.dumps(payload), encoding="utf-8")
raise SystemExit(int(os.environ.get("FAKE_GO_EXIT", "0")))
"""


class PlaywrightEmailQualificationTest(unittest.TestCase):
    def run_script(
        self,
        *arguments: str,
        go_exit: int = 0,
        key_mode: int = 0o600,
        create_socket: bool = True,
        profile: str = "remote",
    ) -> tuple[subprocess.CompletedProcess[str], dict[str, object] | None]:
        with tempfile.TemporaryDirectory() as directory:
            temp_path = Path(directory)
            socket_path = temp_path / "controller.sock"
            listener: socket.socket | None = None
            if create_socket:
                listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
                listener.bind(str(socket_path))
                listener.listen(1)

            private_env = temp_path / f".env.{profile}"
            private_env.write_text(
                f"SPARKCLAW_WEBCHAT_PROXY_TOKEN={PROXY_TOKEN}\n"
                f"SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET_HOST={socket_path}\n",
                encoding="utf-8",
            )
            private_env.chmod(0o600)

            credential_key = temp_path / "gateway-credentials.key"
            credential_key.write_text("A" * 44, encoding="utf-8")
            credential_key.chmod(key_mode)

            fake_go = temp_path / "go"
            fake_go.write_text(textwrap.dedent(FAKE_GO), encoding="utf-8")
            fake_go.chmod(0o755)
            go_log = temp_path / "go.json"

            environment = os.environ.copy()
            environment.update(
                {
                    "PATH": f"{temp_path}:{environment['PATH']}",
                    "QUALIFICATION_TEST_GO_LOG": str(go_log),
                    "FAKE_GO_EXIT": str(go_exit),
                    "SPARKCLAW_POSTGRES_DSN": "postgres://polluted.invalid/example",
                }
            )
            command = [
                "bash",
                str(SCRIPT),
                "--profile",
                profile,
                "--env-file",
                str(private_env),
                "--credential-key-file",
                str(credential_key),
                *arguments,
            ]
            try:
                result = subprocess.run(
                    command,
                    cwd=ROOT,
                    env=environment,
                    check=False,
                    capture_output=True,
                    text=True,
                )
            finally:
                if listener is not None:
                    listener.close()
            payload = json.loads(go_log.read_text(encoding="utf-8")) if go_log.exists() else None
            return result, payload

    def test_maps_effective_product_environment_to_host_live_test(self) -> None:
        result, payload = self.run_script("--providers", " outlook, gmail ")

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIsNotNone(payload)
        assert payload is not None
        self.assertEqual(payload["providers"], "outlook,gmail")
        self.assertEqual(payload["cwd"], str(ROOT / "services" / "gateway"))
        self.assertEqual(
            payload["argv"],
            [
                "test",
                "-count=1",
                "-run",
                "^TestPlaywrightExtensionLiveEmailProbes$",
                "-v",
                "./internal/emailautomation",
            ],
        )
        self.assertEqual(payload["config"], str(ROOT / "configs" / "sparkclaw.default.json"))
        self.assertTrue(str(payload["controller_socket"]).endswith("/controller.sock"))
        self.assertTrue(str(payload["credential_key_file"]).endswith("/gateway-credentials.key"))
        self.assertFalse(payload["credential_key_present"])
        self.assertTrue(payload["state_dsn_has_host"])
        self.assertFalse(payload["state_dsn_has_container"])
        self.assertFalse(payload["postgres_dsn_present"])
        self.assertNotIn("send", " ".join(str(value) for value in payload["argv"]))

    def test_supports_the_local_product_profile_with_the_same_host_boundaries(self) -> None:
        result, payload = self.run_script("--providers", "qq_mail", profile="local")

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIsNotNone(payload)
        assert payload is not None
        self.assertEqual(payload["providers"], "qq_mail")
        self.assertTrue(payload["state_dsn_has_host"])
        self.assertTrue(str(payload["controller_socket"]).endswith("/controller.sock"))

    def test_defaults_to_all_registered_probe_providers(self) -> None:
        result, payload = self.run_script()

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIsNotNone(payload)
        assert payload is not None
        self.assertEqual(payload["providers"], "qq_mail,outlook,gmail")

    def test_rejects_an_unregistered_provider_before_go(self) -> None:
        result, payload = self.run_script("--providers", "gmail,example")

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("unsupported provider", result.stderr)
        self.assertIsNone(payload)

    def test_rejects_duplicate_providers_before_go(self) -> None:
        result, payload = self.run_script("--providers", "gmail,gmail")

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("duplicate provider", result.stderr)
        self.assertIsNone(payload)

    def test_requires_an_owner_only_credential_key_file(self) -> None:
        result, payload = self.run_script("--providers", "gmail", key_mode=0o640)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("mode 0600", result.stderr)
        self.assertIsNone(payload)

    def test_requires_the_installed_host_controller_socket(self) -> None:
        result, payload = self.run_script("--providers", "outlook", create_socket=False)

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("host browser-controller socket is unavailable", result.stderr)
        self.assertIsNone(payload)

    def test_propagates_the_live_go_test_exit_status(self) -> None:
        result, payload = self.run_script("--providers", "qq_mail", go_exit=7)

        self.assertEqual(result.returncode, 7)
        self.assertIsNotNone(payload)


if __name__ == "__main__":
    unittest.main()
