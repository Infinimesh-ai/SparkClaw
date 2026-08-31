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
ONLINE_FAST_ENV = ROOT / "docker" / "env" / "sparkclaw.online-fast.env"
ASR_ENV = ROOT / "docker" / "env" / "sparkclaw.asr.env"
OCR_ENV = ROOT / "docker" / "env" / "sparkclaw.ocr.env"
ASR_COMPOSE = ROOT / "docker" / "compose.asr.yaml"
OCR_COMPOSE = ROOT / "docker" / "compose.ocr.yaml"


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
        runtime_profile: str = "local",
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
                ["bash", str(SCRIPT), runtime_profile, "webchat"],
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
        self.assertIn("docker/env/sparkclaw.single-fast.env", up_call)
        self.assertIn("docker/env/sparkclaw.asr.env", up_call)
        self.assertIn("docker/compose.asr.yaml", up_call)

    def test_online_profile_selects_only_hosted_chat_env(self) -> None:
        result, docker_calls, _ = self.run_script("19876", "online")

        self.assertEqual(result.returncode, 0, result.stderr)
        up_call = next(call for call in docker_calls if "up" in call)
        self.assertIn("docker/env/sparkclaw.online-fast.env", up_call)
        self.assertNotIn("docker/env/sparkclaw.single-fast.env", up_call)

    def test_profile_is_required_and_rejects_unknown_names(self) -> None:
        for profile in ("", "automatic"):
            with self.subTest(profile=profile):
                result, docker_calls, curl_calls = self.run_script("19876", profile)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("online|local", result.stderr)
                self.assertEqual(docker_calls, [])
                self.assertEqual(curl_calls, [])

    def test_entrypoints_choose_profiles_explicitly(self) -> None:
        package = json.loads((ROOT / "package.json").read_text(encoding="utf-8"))
        scripts = package["scripts"]

        self.assertEqual(scripts["dev:gateway"], "npm run dev:gateway:online")
        self.assertIn("restart_runtime_compose.sh online gateway", scripts["dev:gateway:online"])
        self.assertIn("restart_runtime_compose.sh local gateway", scripts["dev:gateway:local"])
        self.assertIn("restart_runtime_compose.sh online", scripts["runtime:restart:online"])
        self.assertIn("restart_runtime_compose.sh local", scripts["runtime:restart:local"])
        self.assertIn(
            "restart_runtime_compose.sh local",
            (ROOT / "scripts" / "start_compose.sh").read_text(encoding="utf-8"),
        )

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
                "SPARKCLAW_STATE_TRANSACTION_TIMEOUT_SECONDS": "23",
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
        self.assertEqual(gateway_environment["SPARKCLAW_STATE_TRANSACTION_TIMEOUT_SECONDS"], "23")

    def test_online_fast_profile_reaches_gateway_with_local_adapters(self) -> None:
        result = subprocess.run(
            [
                "docker",
                "compose",
                "--env-file",
                str(ONLINE_FAST_ENV),
                "--env-file",
                str(ASR_ENV),
                "--env-file",
                str(OCR_ENV),
                "-f",
                str(COMPOSE),
                "-f",
                str(ASR_COMPOSE),
                "-f",
                str(OCR_COMPOSE),
                "--profile",
                "models-local",
                "config",
                "--format",
                "json",
            ],
            cwd=ROOT,
            check=False,
            capture_output=True,
            text=True,
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        environment = json.loads(result.stdout)["services"]["gateway"]["environment"]
        self.assertEqual(
            environment["SPARKCLAW_FAST_BASE_URL"],
            "https://sparkclaw.infinimesh.cloud/fast/v1",
        )
        self.assertEqual(environment["SPARKCLAW_MODEL_CAPACITY_PROFILE"], "infinimesh-online-fast-v1")
        self.assertEqual(environment["SPARKCLAW_DEEP_MODEL"], "sparkclaw-fast")
        for legacy in (
            "SPARKCLAW_FAST_CONTEXT_TOKENS", "SPARKCLAW_FAST_MAX_INPUT_TOKENS", "SPARKCLAW_FAST_MAX_TOKENS",
            "SPARKCLAW_DEEP_CONTEXT_TOKENS", "SPARKCLAW_DEEP_MAX_INPUT_TOKENS", "SPARKCLAW_DEEP_MAX_TOKENS",
        ):
            self.assertNotIn(legacy, environment)
        self.assertEqual(
            environment["SPARKCLAW_EMBEDDING_BASE_URL"],
            "http://sparkclaw-embedding:8003/v1",
        )
        self.assertEqual(
            environment["SPARKCLAW_GUARD_BASE_URL"],
            "http://sparkclaw-guard:8005/v1",
        )
        self.assertEqual(environment["SPARKCLAW_SPEECH_BASE_URL"], "http://sparkclaw-asr:8006")
        self.assertEqual(environment["SPARKCLAW_OCR_BASE_URL"], "http://sparkclaw-ocr:8007/v1")
        self.assertEqual(environment["SPARKCLAW_WORKFLOW_STAGE_EVIDENCE_MAX_BYTES"], "200000")
        self.assertEqual(
            environment["SPARKCLAW_WORKFLOW_RUN_OBSERVATION_COMPACTION_BYTES"],
            "72000",
        )
        self.assertEqual(environment["SPARKCLAW_WORKFLOW_RUN_MAX_OBSERVATION_BYTES"], "96000")


if __name__ == "__main__":
    unittest.main()
