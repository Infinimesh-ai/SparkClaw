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
SCRIPT = ROOT / "scripts" / "start_remote_compose.sh"
COMPOSE = ROOT / "docker" / "compose.yaml"
MODELS_COMPOSE = ROOT / "docker" / "compose.models.local.yaml"
PRODUCT_PROFILE = ROOT / "docker" / "env" / "sparkclaw.product.env"
LOCAL_PROFILE = ROOT / "docker" / "env" / "sparkclaw.local.env"
REMOTE_PROFILE = ROOT / "docker" / "env" / "sparkclaw.remote.env"
DOCTOR_SCRIPT = ROOT / "scripts" / "doctor.sh"
TEST_PROXY_TOKEN = "remote_test_webchat_proxy_token_0123456789abcdef"


def profile_values(*paths: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    for path in paths:
        for line in path.read_text(encoding="utf-8").splitlines():
            if line and not line.startswith("#"):
                key, value = line.split("=", 1)
                values[key] = value
    return values


def compose_config(mode_profile: Path, *, local_models: bool) -> dict[str, object]:
    with tempfile.TemporaryDirectory() as directory:
        effective = Path(directory) / "effective.env"
        values = profile_values(PRODUCT_PROFILE, mode_profile)
        values["SPARKCLAW_WEBCHAT_PROXY_TOKEN"] = TEST_PROXY_TOKEN
        effective.write_text(
            "".join(f"{key}={value}\n" for key, value in values.items()),
            encoding="utf-8",
        )
        command = [
            "docker",
            "compose",
            "--env-file",
            str(effective),
            "-f",
            str(COMPOSE),
        ]
        if local_models:
            command.extend(["-f", str(MODELS_COMPOSE)])
        command.extend(["--profile", "product"])
        if local_models:
            command.extend(["--profile", "models-local"])
        command.extend(["config", "--format", "json"])
        result = subprocess.run(
            command,
            cwd=ROOT,
            check=False,
            capture_output=True,
            text=True,
        )
    if result.returncode != 0:
        raise AssertionError(result.stderr)
    return json.loads(result.stdout)


FAKE_DOCKER = r"""#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

with Path(os.environ["REMOTE_TEST_DOCKER_LOG"]).open("a", encoding="utf-8") as stream:
    stream.write(json.dumps(sys.argv[1:]) + "\n")
raise SystemExit(0)
"""

FAKE_CURL = r"""#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

with Path(os.environ["REMOTE_TEST_CURL_LOG"]).open("a", encoding="utf-8") as stream:
    stream.write(json.dumps(sys.argv[1:]) + "\n")
print('{"ok":true,"auth_required":false,"model_mode":"external","state_backend":"postgres"}')
"""

FAKE_BROWSER_SETUP = r"""#!/usr/bin/env bash
set -euo pipefail
exit 0
"""

FAKE_SYSTEMCTL = r"""#!/usr/bin/env python3
import os

print(os.environ["SPARKCLAW_TEST_BROWSER_PID"])
"""


class RemoteComposeTest(unittest.TestCase):
    def run_script(
        self,
        private_extra: str = "",
        *args: str,
    ) -> tuple[subprocess.CompletedProcess[str], list[list[str]], list[list[str]]]:
        with tempfile.TemporaryDirectory() as directory:
            temp_path = Path(directory)
            private_env = temp_path / ".env.remote"
            docker = temp_path / "docker"
            curl = temp_path / "curl"
            browser_setup = temp_path / "setup-browser.sh"
            systemctl = temp_path / "systemctl"
            private_env.write_text(
                f"SPARKCLAW_WEBCHAT_PROXY_TOKEN={TEST_PROXY_TOKEN}\n"
                f"{private_extra}",
                encoding="utf-8",
            )
            docker.write_text(textwrap.dedent(FAKE_DOCKER), encoding="utf-8")
            curl.write_text(textwrap.dedent(FAKE_CURL), encoding="utf-8")
            browser_setup.write_text(textwrap.dedent(FAKE_BROWSER_SETUP), encoding="utf-8")
            systemctl.write_text(textwrap.dedent(FAKE_SYSTEMCTL), encoding="utf-8")
            for executable in (docker, curl, browser_setup, systemctl):
                executable.chmod(0o755)
            docker_log = temp_path / "docker.jsonl"
            curl_log = temp_path / "curl.jsonl"
            environment = os.environ.copy()
            environment.update(
                {
                    "DOCKER_BIN": str(docker),
                    "PATH": f"{temp_path}:{environment['PATH']}",
                    "SPARKCLAW_REMOTE_ENV_FILE": str(private_env),
                    "REMOTE_TEST_DOCKER_LOG": str(docker_log),
                    "REMOTE_TEST_CURL_LOG": str(curl_log),
                    "SPARKCLAW_BROWSER_SETUP": str(browser_setup),
                    "SPARKCLAW_TEST_BROWSER_PID": str(os.getpid()),
                }
            )
            result = subprocess.run(
                ["bash", str(SCRIPT), *args],
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

    def test_remote_stops_local_models_before_five_application_services(self) -> None:
        result, docker_calls, curl_calls = self.run_script("SPARKCLAW_WEBCHAT_PORT=19876\n")

        self.assertEqual(result.returncode, 0, result.stderr)
        stop_index, stop_call = next(
            (index, call) for index, call in enumerate(docker_calls) if "stop" in call
        )
        for service in (
            "sparkclaw-fast",
            "sparkclaw-deep",
            "sparkclaw-embedding",
            "sparkclaw-guard",
            "sparkclaw-asr",
            "sparkclaw-ocr",
        ):
            self.assertIn(service, stop_call)
        up_index, up_call = next(
            (index, call)
            for index, call in enumerate(docker_calls)
            if "up" in call and "gateway" in call
        )
        self.assertLess(stop_index, up_index)
        self.assertEqual(
            up_call[-5:],
            ["postgres", "sandbox-runner", "gotenberg", "gateway", "webchat"],
        )
        self.assertTrue(any("sparkclaw-remote-env." in argument for argument in up_call))
        self.assertIn(str(COMPOSE), up_call)
        self.assertNotIn(str(MODELS_COMPOSE), up_call)
        self.assertNotIn("compose.remote.yaml", " ".join(up_call))
        self.assertIn("--profile", up_call)
        self.assertIn("product", up_call)
        self.assertIn(str(MODELS_COMPOSE), stop_call)
        self.assertNotIn(str(COMPOSE), stop_call)
        self.assertNotIn("compose.dual-light.yaml", " ".join(stop_call))
        self.assertEqual(len(curl_calls), 1)
        self.assertIn("http://127.0.0.1:19876/readyz", curl_calls[0])

    def test_check_never_stops_or_starts_containers(self) -> None:
        result, docker_calls, curl_calls = self.run_script("", "--check")

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("remote configuration valid", result.stdout)
        self.assertTrue(any("config" in call for call in docker_calls))
        self.assertFalse(any("stop" in call or "up" in call or "exec" in call for call in docker_calls))
        self.assertEqual(curl_calls, [])

    def test_remote_profile_versions_five_public_endpoints(self) -> None:
        product_values = profile_values(PRODUCT_PROFILE)
        remote_values = profile_values(REMOTE_PROFILE)
        values = product_values | remote_values

        self.assertEqual(values["SPARKCLAW_DEPLOYMENT_PROFILE"], "remote")
        self.assertEqual(values["SPARKCLAW_MODEL_CAPACITY_PROFILE"], "sparkclaw-product-v1")
        self.assertEqual(values["SPARKCLAW_PAIRING_REQUIRED"], "true")
        self.assertEqual(values["SPARKCLAW_FAST_BASE_URL"], "https://sparkclaw.infinimesh.cloud/fast/v1")
        self.assertEqual(values["SPARKCLAW_DEEP_BASE_URL"], values["SPARKCLAW_FAST_BASE_URL"])
        self.assertEqual(values["SPARKCLAW_EMBEDDING_BASE_URL"], "https://sparkclaw.infinimesh.cloud/embedding/v1")
        self.assertEqual(values["SPARKCLAW_GUARD_BASE_URL"], "https://sparkclaw.infinimesh.cloud/guard/v1")
        self.assertEqual(values["SPARKCLAW_SPEECH_BASE_URL"], "https://sparkclaw.infinimesh.cloud/asr")
        self.assertEqual(values["SPARKCLAW_OCR_BASE_URL"], "https://sparkclaw.infinimesh.cloud/ocr/v1")
        for shared_key in (
            "SPARKCLAW_MODEL_CAPACITY_PROFILE",
            "SPARKCLAW_MODEL_DISABLE_THINKING",
            "SPARKCLAW_API_TOKEN",
            "SPARKCLAW_PAIRING_REQUIRED",
            "SPARKCLAW_WORKFLOW_STAGE_EVIDENCE_MAX_BYTES",
            "SPARKCLAW_WORKFLOW_RUN_OBSERVATION_COMPACTION_BYTES",
            "SPARKCLAW_WORKFLOW_RUN_MAX_OBSERVATION_BYTES",
        ):
            self.assertNotIn(shared_key, remote_values)

    def test_private_file_cannot_override_remote_model_addresses(self) -> None:
        result, docker_calls, _ = self.run_script(
            "SPARKCLAW_FAST_BASE_URL=http://localhost:8001/v1\n"
        )

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("not a supported credential or machine override", result.stderr)
        self.assertEqual(docker_calls, [])

    def test_remote_profile_validator_rejects_local_model_addresses(self) -> None:
        blocked = (
            "http://sparkclaw-fast:8001/v1",
            "http://localhost:8001/v1",
            "http://127.0.0.1:8001/v1",
            "http://host.docker.internal:8001/v1",
            "http://[::1]:8001/v1",
            "http://10.0.0.8:8001/v1",
            "http://172.16.0.8:8001/v1",
            "http://192.168.1.8:8001/v1",
            "http://[fd00::8]:8001/v1",
            "http://models.internal:8001/v1",
        )
        for url in blocked:
            with self.subTest(url=url):
                with tempfile.TemporaryDirectory() as directory:
                    mode_profile = Path(directory) / "remote.env"
                    private_profile = Path(directory) / "private.env"
                    lines = []
                    for line in REMOTE_PROFILE.read_text(encoding="utf-8").splitlines():
                        if line.startswith("SPARKCLAW_EMBEDDING_BASE_URL="):
                            line = f"SPARKCLAW_EMBEDDING_BASE_URL={url}"
                        lines.append(line)
                    mode_profile.write_text("\n".join(lines) + "\n", encoding="utf-8")
                    private_profile.write_text(
                        f"SPARKCLAW_WEBCHAT_PROXY_TOKEN={TEST_PROXY_TOKEN}\n",
                        encoding="utf-8",
                    )
                    result = subprocess.run(
                        [
                            "bash",
                            "-c",
                            'source scripts/lib/dotenv.sh; source scripts/lib/deployment-profile.sh; '
                            'sparkclaw_validate_product_profile remote "$1" "$2" "$3"',
                            "_",
                            str(PRODUCT_PROFILE),
                            str(mode_profile),
                            str(private_profile),
                        ],
                        cwd=ROOT,
                        check=False,
                        capture_output=True,
                        text=True,
                    )
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("must not use a local model address", result.stderr)

    def test_private_file_cannot_change_deployment_profile(self) -> None:
        result, docker_calls, _ = self.run_script(
            "SPARKCLAW_DEPLOYMENT_PROFILE=local\n"
        )

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("not a supported credential or machine override", result.stderr)
        self.assertEqual(docker_calls, [])

    def test_private_file_cannot_change_capacity_workflow_or_api_token(self) -> None:
        for key, value in (
            ("SPARKCLAW_MODEL_CAPACITY_PROFILE", "mock"),
            ("SPARKCLAW_WORKFLOW_STAGE_EVIDENCE_MAX_BYTES", "1"),
            ("SPARKCLAW_WORKFLOW_RUN_OBSERVATION_COMPACTION_BYTES", "1"),
            ("SPARKCLAW_WORKFLOW_RUN_MAX_OBSERVATION_BYTES", "1"),
            ("SPARKCLAW_API_TOKEN", "enabled"),
        ):
            with self.subTest(key=key):
                result, docker_calls, _ = self.run_script(
                    f"{key}={value}\n"
                )
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("not a supported credential or machine override", result.stderr)
                self.assertEqual(docker_calls, [])

    def test_local_and_remote_common_application_services_are_identical(self) -> None:
        local = compose_config(LOCAL_PROFILE, local_models=True)
        remote = compose_config(REMOTE_PROFILE, local_models=False)
        common_services = ("postgres", "sandbox-runner", "gotenberg", "gateway", "webchat")
        allowed_gateway_environment_differences = {
            "SPARKCLAW_DEPLOYMENT_PROFILE",
            "SPARKCLAW_FAST_BASE_URL",
            "SPARKCLAW_DEEP_BASE_URL",
            "SPARKCLAW_EMBEDDING_BASE_URL",
            "SPARKCLAW_GUARD_BASE_URL",
            "SPARKCLAW_SPEECH_BASE_URL",
            "SPARKCLAW_SPEECH_ALLOWED_HOSTS",
            "SPARKCLAW_SPEECH_EXPECTED_RUNTIME_VERSION",
            "SPARKCLAW_OCR_BASE_URL",
            "SPARKCLAW_OCR_ALLOWED_HOSTS",
        }

        self.assertEqual(set(remote["services"]), set(common_services))
        for service in common_services:
            local_service = dict(local["services"][service])
            remote_service = dict(remote["services"][service])
            if service == "gateway":
                local_environment = dict(local_service.pop("environment"))
                remote_environment = dict(remote_service.pop("environment"))
                differences = {
                    key
                    for key in local_environment.keys() | remote_environment.keys()
                    if local_environment.get(key) != remote_environment.get(key)
                }
                self.assertEqual(differences, allowed_gateway_environment_differences)
                for key in differences:
                    local_environment.pop(key, None)
                    remote_environment.pop(key, None)
                self.assertEqual(local_environment, remote_environment)
                self.assertEqual(local_environment["SPARKCLAW_API_TOKEN"], "")
                self.assertEqual(local_environment["SPARKCLAW_PAIRING_REQUIRED"], "true")
                self.assertEqual(local_environment["SPARKCLAW_WEBCHAT_PROXY_TOKEN"], TEST_PROXY_TOKEN)
                self.assertEqual(local_environment["SPARKCLAW_MODEL_CAPACITY_PROFILE"], "sparkclaw-product-v1")
                self.assertEqual(local_environment["SPARKCLAW_WORKFLOW_STAGE_EVIDENCE_MAX_BYTES"], "200000")
                self.assertEqual(local_environment["SPARKCLAW_WORKFLOW_RUN_OBSERVATION_COMPACTION_BYTES"], "72000")
                self.assertEqual(local_environment["SPARKCLAW_WORKFLOW_RUN_MAX_OBSERVATION_BYTES"], "96000")
            self.assertEqual(local_service, remote_service)

    def test_remote_uses_common_compose_with_gotenberg_and_no_models(self) -> None:
        config = compose_config(REMOTE_PROFILE, local_models=False)
        environment = config["services"]["gateway"]["environment"]
        self.assertEqual(environment["SPARKCLAW_PAIRING_REQUIRED"], "true")
        self.assertEqual(environment["SPARKCLAW_WEBCHAT_PROXY_TOKEN"], TEST_PROXY_TOKEN)
        self.assertEqual(
            config["services"]["webchat"]["environment"]["SPARKCLAW_WEBCHAT_PROXY_TOKEN"],
            TEST_PROXY_TOKEN,
        )
        self.assertEqual(environment["SPARKCLAW_FAST_BASE_URL"], "https://sparkclaw.infinimesh.cloud/fast/v1")
        self.assertEqual(environment["SPARKCLAW_SPEECH_BASE_URL"], "https://sparkclaw.infinimesh.cloud/asr")
        self.assertEqual(config["services"]["gateway"]["depends_on"]["gotenberg"]["condition"], "service_healthy")
        self.assertEqual(config["services"]["gateway"]["depends_on"]["postgres"]["condition"], "service_healthy")
        for service in ("postgres", "sandbox-runner", "gotenberg", "gateway", "webchat"):
            self.assertEqual(config["services"][service]["restart"], "unless-stopped")
        self.assertFalse(any(name.startswith("sparkclaw-") for name in config["services"]))
        self.assertFalse((ROOT / "docker" / "compose.remote.yaml").exists())

    def test_remote_doctor_does_not_pin_the_provider_runtime_version(self) -> None:
        local = profile_values(LOCAL_PROFILE)
        remote = profile_values(REMOTE_PROFILE)
        doctor = DOCTOR_SCRIPT.read_text(encoding="utf-8")

        self.assertIn("SPARKCLAW_SPEECH_EXPECTED_RUNTIME_VERSION", local)
        self.assertNotIn("SPARKCLAW_SPEECH_EXPECTED_RUNTIME_VERSION", remote)
        self.assertIn(
            'SPEECH_RUNTIME_VERSION="${SPARKCLAW_SPEECH_EXPECTED_RUNTIME_VERSION:-}"',
            doctor,
        )
        self.assertIn('if [[ -n "$SPEECH_RUNTIME_VERSION" ]]; then', doctor)


if __name__ == "__main__":
    unittest.main()
