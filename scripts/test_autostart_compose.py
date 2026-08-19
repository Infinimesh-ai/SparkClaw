#!/usr/bin/env python3

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import textwrap
import time
import unittest


ROOT = Path(__file__).resolve().parents[1]
AUTOSTART_SCRIPT = ROOT / "scripts" / "autostart_compose.sh"
INSTALL_SCRIPT = ROOT / "scripts" / "install_autostart_systemd.sh"


FAKE_COMMAND = r"""#!/usr/bin/env python3
import json
import os
from pathlib import Path
import subprocess
import sys
import time

name = Path(sys.argv[0]).name
with Path(os.environ["AUTOSTART_TEST_LOG"]).open("a", encoding="utf-8") as stream:
    stream.write(json.dumps([name, *sys.argv[1:]]) + "\n")

if name == os.environ.get("AUTOSTART_TEST_HANG_COMMAND"):
    time.sleep(30)
    raise SystemExit(1)

if name == "sudo":
    args = sys.argv[1:]
    if args and args[0] == "-n":
        args = args[1:]
    environment = os.environ.copy()
    environment["AUTOSTART_TEST_UNDER_SUDO"] = "1"
    raise SystemExit(subprocess.run(args, env=environment, check=False).returncode)
if (
    name == "docker"
    and os.environ.get("AUTOSTART_TEST_REQUIRE_SUDO") == "1"
    and os.environ.get("AUTOSTART_TEST_UNDER_SUDO") != "1"
):
    raise SystemExit(1)
if name == "nvidia-smi":
    print("GPU 0: NVIDIA GB10")
raise SystemExit(0)
"""


class AutostartComposeTest(unittest.TestCase):
    def run_autostart(self, dotenv, extra_env=None):
        with tempfile.TemporaryDirectory() as temp_dir:
            temp_path = Path(temp_dir)
            env_path = temp_path / ".env"
            env_path.write_text(dotenv, encoding="utf-8")
            log_path = temp_path / "commands.jsonl"
            command_paths = {}
            for name in ("docker", "nvidia-smi", "nvidia-container-cli", "bash", "sudo"):
                command_path = temp_path / name
                command_path.write_text(textwrap.dedent(FAKE_COMMAND), encoding="utf-8")
                command_path.chmod(0o755)
                command_paths[name] = command_path

            env = os.environ.copy()
            env.pop("SPARKCLAW_AUTOSTART_ENABLED", None)
            env.pop("SPARKCLAW_AUTOSTART_READY_TIMEOUT_SECONDS", None)
            env.pop("SPARKCLAW_AUTOSTART_PROBE_TIMEOUT_SECONDS", None)
            env.update(
                {
                    "SPARKCLAW_AUTOSTART_ENV_FILE": str(env_path),
                    "AUTOSTART_TEST_LOG": str(log_path),
                    "DOCKER_BIN": str(command_paths["docker"]),
                    "NVIDIA_SMI_BIN": str(command_paths["nvidia-smi"]),
                    "NVIDIA_CONTAINER_CLI_BIN": str(command_paths["nvidia-container-cli"]),
                    "BASH_BIN": str(command_paths["bash"]),
                    "PATH": f"{temp_path}:{env['PATH']}",
                }
            )
            env.update(extra_env or {})
            result = subprocess.run(
                ["/usr/bin/bash", str(AUTOSTART_SCRIPT)],
                cwd=ROOT,
                env=env,
                check=False,
                capture_output=True,
                text=True,
            )
            calls = []
            if log_path.exists():
                calls = [json.loads(line) for line in log_path.read_text().splitlines()]
            return result, calls

    def test_missing_setting_defaults_to_enabled_reconciliation(self):
        result, calls = self.run_autostart("SPARKCLAW_PORT=18789\n")

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Reconciling", result.stdout)
        self.assertIn(["docker", "ps"], calls)
        self.assertTrue(
            any(
                call[0] == "bash"
                and call[1].endswith("/scripts/start_compose.sh")
                for call in calls
            )
        )

    def test_false_setting_skips_all_runtime_commands(self):
        result, calls = self.run_autostart("SPARKCLAW_AUTOSTART_ENABLED=false\n")

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("disabled", result.stdout)
        self.assertEqual(calls, [])

    def test_passwordless_sudo_docker_fallback_starts_product(self):
        result, calls = self.run_autostart(
            "SPARKCLAW_AUTOSTART_ENABLED=true\n",
            {"AUTOSTART_TEST_REQUIRE_SUDO": "1"},
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(any(call[0] == "sudo" for call in calls))
        self.assertTrue(
            any(
                call[0] == "bash"
                and call[1].endswith("/scripts/start_compose.sh")
                for call in calls
            )
        )

    def test_invalid_setting_fails_before_runtime_commands(self):
        result, calls = self.run_autostart("SPARKCLAW_AUTOSTART_ENABLED=maybe\n")

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("must be true or false", result.stderr)
        self.assertEqual(calls, [])

    def test_hung_probe_is_bounded_by_the_ready_deadline(self):
        started = time.monotonic()
        result, calls = self.run_autostart(
            "SPARKCLAW_AUTOSTART_ENABLED=true\n",
            {
                "AUTOSTART_TEST_HANG_COMMAND": "docker",
                "SPARKCLAW_AUTOSTART_READY_TIMEOUT_SECONDS": "1",
                "SPARKCLAW_AUTOSTART_PROBE_TIMEOUT_SECONDS": "1",
            },
        )

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("did not become ready", result.stderr)
        self.assertLess(time.monotonic() - started, 5)
        self.assertFalse(any(call[0] == "bash" for call in calls))

    def test_invalid_timeout_fails_before_runtime_commands(self):
        result, calls = self.run_autostart(
            "SPARKCLAW_AUTOSTART_ENABLED=true\n"
            "SPARKCLAW_AUTOSTART_PROBE_TIMEOUT_SECONDS=0\n",
        )

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("timeouts must be positive integers", result.stderr)
        self.assertEqual(calls, [])


class InstallAutostartSystemdTest(unittest.TestCase):
    def test_installs_and_enables_without_starting_service(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            temp_path = Path(temp_dir)
            unit_dir = temp_path / "systemd"
            systemctl = temp_path / "systemctl"
            log_path = temp_path / "systemctl.jsonl"
            systemctl.write_text(textwrap.dedent(FAKE_COMMAND), encoding="utf-8")
            systemctl.chmod(0o755)
            env = os.environ.copy()
            env.update(
                {
                    "SPARKCLAW_SYSTEMD_UNIT_DIR": str(unit_dir),
                    "SYSTEMCTL_BIN": str(systemctl),
                    "AUTOSTART_TEST_LOG": str(log_path),
                }
            )

            result = subprocess.run(
                ["bash", str(INSTALL_SCRIPT)],
                cwd=ROOT,
                env=env,
                check=True,
                capture_output=True,
                text=True,
            )

            unit = (unit_dir / "sparkclaw-autostart.service").read_text()
            calls = [json.loads(line) for line in log_path.read_text().splitlines()]
            self.assertIn("scripts/autostart_compose.sh", unit)
            self.assertIn("Type=oneshot", unit)
            self.assertIn("RemainAfterExit=yes", unit)
            self.assertIn("TimeoutStartSec=4h", unit)
            self.assertNotIn("TimeoutStartSec=infinity", unit)
            if systemd_analyze := shutil.which("systemd-analyze"):
                verify = subprocess.run(
                    [systemd_analyze, "verify", str(unit_dir / "sparkclaw-autostart.service")],
                    check=False,
                    capture_output=True,
                    text=True,
                )
                self.assertEqual(verify.returncode, 0, verify.stderr)
            self.assertEqual(calls[0][1:], ["daemon-reload"])
            self.assertEqual(calls[1][1:], ["enable", "sparkclaw-autostart.service"])
            self.assertFalse(any("--now" in call or "start" in call for call in calls))
            self.assertIn("was not started now", result.stdout)

            check = subprocess.run(
                ["bash", str(INSTALL_SCRIPT), "--check"],
                cwd=ROOT,
                env=env,
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertEqual(check.returncode, 0, check.stderr)

            with (unit_dir / "sparkclaw-autostart.service").open("a", encoding="utf-8") as stream:
                stream.write("# stale\n")
            stale = subprocess.run(
                ["bash", str(INSTALL_SCRIPT), "--check"],
                cwd=ROOT,
                env=env,
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertNotEqual(stale.returncode, 0)
            self.assertIn("stale", stale.stderr)


if __name__ == "__main__":
    unittest.main()
