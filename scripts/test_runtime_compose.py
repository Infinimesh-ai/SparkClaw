#!/usr/bin/env python3

from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import tempfile
import textwrap
import unittest


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "restart_runtime_compose.sh"
COMPOSE = ROOT / "docker" / "compose.yaml"
EXAMPLE_ENV = ROOT / "docker" / "env" / "sparkclaw.example.env"


FAKE_DOCKER = r"""#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

with Path(os.environ["RUNTIME_TEST_DOCKER_LOG"]).open("a", encoding="utf-8") as stream:
    stream.write(json.dumps(sys.argv[1:]) + "\n")
raise SystemExit(0)
"""

FAKE_CURL = r"""#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

with Path(os.environ["RUNTIME_TEST_CURL_LOG"]).open("a", encoding="utf-8") as stream:
    stream.write(json.dumps(sys.argv[1:]) + "\n")
print('{"ok":true,"model_mode":"external","state_backend":"postgres"}')
"""


class RuntimeComposeTest(unittest.TestCase):
    def run_script(
        self,
        port: str,
    ) -> tuple[subprocess.CompletedProcess[str], list[list[str]], list[list[str]]]:
        with tempfile.TemporaryDirectory() as directory:
            temp_path = Path(directory)
            docker = temp_path / "docker"
            curl = temp_path / "curl"
            docker.write_text(textwrap.dedent(FAKE_DOCKER), encoding="utf-8")
            curl.write_text(textwrap.dedent(FAKE_CURL), encoding="utf-8")
            docker.chmod(0o755)
            curl.chmod(0o755)
            docker_log = temp_path / "docker.jsonl"
            curl_log = temp_path / "curl.jsonl"
            environment = os.environ.copy()
            environment.update(
                {
                    "DOCKER_BIN": str(docker),
                    "PATH": f"{temp_path}:{environment['PATH']}",
                    "RUNTIME_TEST_DOCKER_LOG": str(docker_log),
                    "RUNTIME_TEST_CURL_LOG": str(curl_log),
                    "SPARKCLAW_WEBCHAT_PORT": port,
                }
            )
            result = subprocess.run(
                ["bash", str(SCRIPT), "webchat"],
                cwd=ROOT,
                env=environment,
                check=False,
                capture_output=True,
                text=True,
            )
            docker_calls = []
            if docker_log.exists():
                docker_calls = [json.loads(line) for line in docker_log.read_text().splitlines()]
            curl_calls = []
            if curl_log.exists():
                curl_calls = [json.loads(line) for line in curl_log.read_text().splitlines()]
            return result, docker_calls, curl_calls

    def test_non_default_webchat_port_owns_readiness_url(self) -> None:
        result, docker_calls, curl_calls = self.run_script("19876")

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("SparkClaw runtime ready", result.stdout)
        self.assertTrue(any("up" in call and "webchat" in call for call in docker_calls))
        self.assertEqual(len(curl_calls), 1)
        self.assertIn("--connect-timeout", curl_calls[0])
        self.assertIn("--max-time", curl_calls[0])
        self.assertIn("http://127.0.0.1:19876/readyz", curl_calls[0])
        up_call = next(call for call in docker_calls if "up" in call)
        self.assertIn("docker/env/sparkclaw.asr.env", up_call)
        self.assertIn("docker/compose.asr.yaml", up_call)

    def test_invalid_webchat_port_fails_before_docker_access(self) -> None:
        result, docker_calls, curl_calls = self.run_script("0")

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("must be an integer between 1 and 65535", result.stderr)
        self.assertEqual(docker_calls, [])
        self.assertEqual(curl_calls, [])

    def test_store_timeout_overrides_reach_gateway(self) -> None:
        environment = os.environ.copy()
        environment.update(
            {
                "SPARKCLAW_STATE_READ_TIMEOUT_SECONDS": "7",
                "SPARKCLAW_STATE_WRITE_TIMEOUT_SECONDS": "19",
            }
        )
        result = subprocess.run(
            [
                "docker",
                "compose",
                "--env-file",
                str(EXAMPLE_ENV),
                "-f",
                str(COMPOSE),
                "--profile",
                "models-local",
                "config",
                "--format",
                "json",
            ],
            cwd=ROOT,
            env=environment,
            check=False,
            capture_output=True,
            text=True,
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        config = json.loads(result.stdout)
        gateway_environment = config["services"]["gateway"]["environment"]
        self.assertEqual(gateway_environment["SPARKCLAW_STATE_READ_TIMEOUT_SECONDS"], "7")
        self.assertEqual(gateway_environment["SPARKCLAW_STATE_WRITE_TIMEOUT_SECONDS"], "19")


if __name__ == "__main__":
    unittest.main()
