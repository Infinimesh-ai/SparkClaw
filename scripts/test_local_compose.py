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
SCRIPT = ROOT / "scripts" / "start_local_compose.sh"
COMPOSE = ROOT / "docker" / "compose.yaml"
MODELS_COMPOSE = ROOT / "docker" / "compose.models.local.yaml"
DEV_COMPOSE = ROOT / "docker" / "compose.dev.yaml"
PRODUCT_PROFILE = ROOT / "docker" / "env" / "sparkclaw.product.env"
LOCAL_PROFILE = ROOT / "docker" / "env" / "sparkclaw.local.env"
GATEWAY_DOCKERFILE = ROOT / "docker" / "images" / "gateway.Dockerfile"
DOCUMENT_REQUIREMENTS = ROOT / "tools" / "document-runtime" / "requirements.txt"
WEBCHAT_NGINX = ROOT / "docker" / "images" / "webchat.nginx.conf.template"
TEST_PROXY_TOKEN = "local_test_webchat_proxy_token_0123456789abcdef"


def profile_values(*paths: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    for path in paths:
        for line in path.read_text(encoding="utf-8").splitlines():
            if line and not line.startswith("#"):
                key, value = line.split("=", 1)
                values[key] = value
    return values


FAKE_DOCKER = r"""#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

with Path(os.environ["LOCAL_TEST_DOCKER_LOG"]).open("a", encoding="utf-8") as stream:
    stream.write(json.dumps(sys.argv[1:]) + "\n")
raise SystemExit(0)
"""

FAKE_CURL = r"""#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

with Path(os.environ["LOCAL_TEST_CURL_LOG"]).open("a", encoding="utf-8") as stream:
    stream.write(json.dumps(sys.argv[1:]) + "\n")
print('{"ok":true,"model_mode":"external","state_backend":"postgres"}')
"""

FAKE_HOST_BROWSER_INSTALLER = r"""#!/usr/bin/env bash
set -euo pipefail
exit 0
"""


class LocalComposeTest(unittest.TestCase):
    def run_script(
        self,
        port: str = "19876",
        *,
        private_extra: str = "",
        args: tuple[str, ...] = (),
        ambient_extra: dict[str, str] | None = None,
    ) -> tuple[subprocess.CompletedProcess[str], list[list[str]], list[list[str]]]:
        with tempfile.TemporaryDirectory() as directory:
            temp_path = Path(directory)
            docker = temp_path / "docker"
            curl = temp_path / "curl"
            browser_installer = temp_path / "install-host-browser.sh"
            private_env = temp_path / ".env.local"
            endpoint_file = temp_path / "browserd" / "cdp-endpoint"
            endpoint_file.parent.mkdir()
            endpoint_file.write_text(
                json.dumps(
                    {
                        "version": 1,
                        "profileID": "default",
                        "presentation": "headless",
                        "browserPID": os.getpid(),
                        "generation": 1,
                        "browserVersion": "Chromium 148.0.7778.0",
                        "webSocketURL": "ws://host.docker.internal:18791/test/devtools/browser/id",
                        "hostWebSocketURL": "ws://127.0.0.1:18791/test/devtools/browser/id",
                    }
                )
                + "\n",
                encoding="utf-8",
            )
            endpoint_file.chmod(0o600)
            private_env.write_text(
                f"SPARKCLAW_BROWSER_CDP_RUNTIME_DIR_HOST={endpoint_file.parent}\n"
                f"SPARKCLAW_BROWSER_CDP_ENDPOINT_FILE_HOST={endpoint_file}\n"
                f"SPARKCLAW_WEBCHAT_PROXY_TOKEN={TEST_PROXY_TOKEN}\n"
                f"SPARKCLAW_WEBCHAT_PORT={port}\n"
                f"{private_extra}",
                encoding="utf-8",
            )
            docker.write_text(textwrap.dedent(FAKE_DOCKER), encoding="utf-8")
            curl.write_text(textwrap.dedent(FAKE_CURL), encoding="utf-8")
            browser_installer.write_text(
                textwrap.dedent(FAKE_HOST_BROWSER_INSTALLER), encoding="utf-8"
            )
            for executable in (docker, curl, browser_installer):
                executable.chmod(0o755)
            docker_log = temp_path / "docker.jsonl"
            curl_log = temp_path / "curl.jsonl"
            environment = os.environ.copy()
            environment.update(
                {
                    "DOCKER_BIN": str(docker),
                    "PATH": f"{temp_path}:{environment['PATH']}",
                    "LOCAL_TEST_DOCKER_LOG": str(docker_log),
                    "LOCAL_TEST_CURL_LOG": str(curl_log),
                    "SPARKCLAW_LOCAL_ENV_FILE": str(private_env),
                    "SPARKCLAW_HOST_BROWSER_INSTALLER": str(browser_installer),
                    "SPARKCLAW_MODEL_STARTUP_TIMEOUT_SECONDS": "17",
                    "SPARKCLAW_FORCE_MODEL_RECREATE": "false",
                }
            )
            environment.update(ambient_extra or {})
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

    def test_local_start_owns_models_and_five_application_services(self) -> None:
        result, docker_calls, curl_calls = self.run_script()

        self.assertEqual(result.returncode, 0, result.stderr)
        model_up = next(call for call in docker_calls if "up" in call and "sparkclaw-fast" in call)
        for service in (
            "sparkclaw-fast",
            "sparkclaw-embedding",
            "sparkclaw-guard",
            "sparkclaw-asr",
            "sparkclaw-ocr",
        ):
            self.assertIn(service, model_up)
        app_up = next(call for call in docker_calls if "up" in call and "gateway" in call)
        self.assertEqual(
            app_up[-5:],
            ["postgres", "sandbox-runner", "gotenberg", "gateway", "webchat"],
        )
        self.assertTrue(any("sparkclaw-local-env." in argument for argument in app_up))
        self.assertIn(str(MODELS_COMPOSE), app_up)
        self.assertNotIn("sparkclaw.remote.env", " ".join(app_up))
        self.assertEqual(len(curl_calls), 1)
        self.assertIn("http://127.0.0.1:19876/readyz", curl_calls[0])
        smoke_call = next(call for call in docker_calls if "exec" in call)
        self.assertIn("/app/scripts/host_browser_mcp_smoke.mjs", smoke_call)

    def test_check_expands_local_compose_without_starting_containers(self) -> None:
        result, docker_calls, curl_calls = self.run_script(args=("--check",))

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("local configuration valid", result.stdout)
        self.assertTrue(any("config" in call for call in docker_calls))
        self.assertFalse(any("up" in call or "stop" in call or "exec" in call for call in docker_calls))
        self.assertEqual(curl_calls, [])

    def test_local_entry_rejects_remote_model_override(self) -> None:
        result, docker_calls, _ = self.run_script(
            private_extra="SPARKCLAW_FAST_BASE_URL=https://models.example.com/fast/v1\n"
        )

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("not a supported credential or machine override", result.stderr)
        self.assertEqual(docker_calls, [])

    def test_ambient_model_and_api_token_values_cannot_create_a_hybrid_mode(self) -> None:
        result, docker_calls, _ = self.run_script(
            args=("--check",),
            ambient_extra={
                "SPARKCLAW_FAST_BASE_URL": "https://models.example.com/fast/v1",
                "SPARKCLAW_MODEL_CAPACITY_PROFILE": "mock",
                "SPARKCLAW_API_TOKEN": "must-not-be-used",
            },
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(any("config" in call for call in docker_calls))

    def test_product_package_surface_has_only_explicit_local_remote_entries(self) -> None:
        scripts = json.loads((ROOT / "package.json").read_text(encoding="utf-8"))["scripts"]

        self.assertEqual(scripts["deploy:local"], "bash scripts/deploy_local.sh")
        self.assertEqual(scripts["start:local"], "bash scripts/start_local_compose.sh")
        self.assertEqual(scripts["deploy:remote"], "bash scripts/deploy_remote.sh")
        self.assertEqual(scripts["start:remote"], "bash scripts/start_remote_compose.sh")
        for retired in (
            "deploy",
            "start",
            "dev",
            "dev:gateway",
            "dev:webchat",
            "runtime:restart",
            "runtime:restart:online",
            "runtime:restart:local",
        ):
            self.assertNotIn(retired, scripts)
        self.assertFalse(any("online" in name or "cloud" in name for name in scripts))

    def test_mock_dev_compose_does_not_inherit_a_product_mode(self) -> None:
        result = subprocess.run(
            [
                "docker",
                "compose",
                "-f",
                str(DEV_COMPOSE),
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
        config = json.loads(result.stdout)
        environment = config["services"]["gateway"]["environment"]
        self.assertEqual(environment["SPARKCLAW_MODEL_MODE"], "mock")
        self.assertNotIn("SPARKCLAW_DEPLOYMENT_PROFILE", environment)
        self.assertNotIn("SPARKCLAW_MODEL_CAPACITY_PROFILE", environment)
        self.assertNotIn("env_file", config["services"]["gateway"])
        self.assertNotIn("sparkclaw.local.env", DEV_COMPOSE.read_text(encoding="utf-8"))
        self.assertNotIn("sparkclaw.remote.env", DEV_COMPOSE.read_text(encoding="utf-8"))

    def test_product_and_local_profiles_have_single_responsibilities(self) -> None:
        product_values = profile_values(PRODUCT_PROFILE)
        local_values = profile_values(LOCAL_PROFILE)
        values = product_values | local_values

        self.assertEqual(values["SPARKCLAW_DEPLOYMENT_PROFILE"], "local")
        self.assertEqual(product_values["SPARKCLAW_MODEL_CAPACITY_PROFILE"], "sparkclaw-product-v1")
        self.assertEqual(product_values["SPARKCLAW_API_TOKEN"], "")
        self.assertEqual(product_values["SPARKCLAW_PAIRING_REQUIRED"], "true")
        self.assertEqual(product_values["SPARKCLAW_WORKFLOW_STAGE_EVIDENCE_MAX_BYTES"], "200000")
        self.assertEqual(product_values["SPARKCLAW_WORKFLOW_RUN_OBSERVATION_COMPACTION_BYTES"], "72000")
        self.assertEqual(product_values["SPARKCLAW_WORKFLOW_RUN_MAX_OBSERVATION_BYTES"], "96000")
        self.assertEqual(values["SPARKCLAW_FAST_BASE_URL"], "http://sparkclaw-fast:8001/v1")
        self.assertEqual(values["SPARKCLAW_DEEP_BASE_URL"], "http://sparkclaw-fast:8001/v1")
        self.assertEqual(values["SPARKCLAW_EMBEDDING_BASE_URL"], "http://sparkclaw-embedding:8003/v1")
        self.assertEqual(values["SPARKCLAW_GUARD_BASE_URL"], "http://sparkclaw-guard:8005/v1")
        self.assertEqual(values["SPARKCLAW_SPEECH_BASE_URL"], "http://sparkclaw-asr:8006")
        self.assertEqual(values["SPARKCLAW_OCR_BASE_URL"], "http://sparkclaw-ocr:8007/v1")
        self.assertEqual(values["SPARKCLAW_SPEECH_ENABLED"], "true")
        self.assertEqual(values["SPARKCLAW_OCR_ENABLED"], "true")
        for shared_key in (
            "SPARKCLAW_MODEL_CAPACITY_PROFILE",
            "SPARKCLAW_API_TOKEN",
            "SPARKCLAW_PAIRING_REQUIRED",
            "SPARKCLAW_WORKFLOW_STAGE_EVIDENCE_MAX_BYTES",
            "SPARKCLAW_WORKFLOW_RUN_OBSERVATION_COMPACTION_BYTES",
            "SPARKCLAW_WORKFLOW_RUN_MAX_OBSERVATION_BYTES",
        ):
            self.assertNotIn(shared_key, local_values)

    def test_local_compose_keeps_models_separate_and_gotenberg_required(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            effective = Path(directory) / "effective.env"
            values = profile_values(PRODUCT_PROFILE, LOCAL_PROFILE)
            values["SPARKCLAW_WEBCHAT_PROXY_TOKEN"] = TEST_PROXY_TOKEN
            effective.write_text(
                "".join(f"{key}={value}\n" for key, value in values.items()),
                encoding="utf-8",
            )
            result = subprocess.run(
                [
                    "docker",
                    "compose",
                    "--env-file",
                    str(effective),
                    "-f",
                    str(COMPOSE),
                    "-f",
                    str(MODELS_COMPOSE),
                    "--profile",
                    "product",
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
        config = json.loads(result.stdout)
        gotenberg = config["services"]["gotenberg"]
        self.assertEqual(
            gotenberg["image"],
            "gotenberg/gotenberg:8.36.0@sha256:87c16b9f364279d321bc9772d31fa58aa6abe036423c270698bd636c3a8e9466",
        )
        self.assertEqual(
            config["services"]["gateway"]["depends_on"]["gotenberg"]["condition"],
            "service_healthy",
        )
        self.assertEqual(
            config["services"]["gateway"]["depends_on"]["postgres"]["condition"],
            "service_healthy",
        )
        self.assertEqual(config["services"]["gateway"]["environment"]["SPARKCLAW_API_TOKEN"], "")
        self.assertEqual(config["services"]["gateway"]["environment"]["SPARKCLAW_PAIRING_REQUIRED"], "true")
        self.assertEqual(
            config["services"]["gateway"]["environment"]["SPARKCLAW_WEBCHAT_PROXY_TOKEN"],
            TEST_PROXY_TOKEN,
        )
        self.assertEqual(
            config["services"]["webchat"]["environment"]["SPARKCLAW_WEBCHAT_PROXY_TOKEN"],
            TEST_PROXY_TOKEN,
        )
        self.assertIn(
            {
                "mode": "ingress",
                "host_ip": "127.0.0.1",
                "target": 18795,
                "published": "18795",
                "protocol": "tcp",
            },
            config["services"]["webchat"]["ports"],
        )
        base_text = COMPOSE.read_text(encoding="utf-8")
        models_text = MODELS_COMPOSE.read_text(encoding="utf-8")
        for service in (
            "sparkclaw-fast",
            "sparkclaw-deep",
            "sparkclaw-embedding",
            "sparkclaw-guard",
            "sparkclaw-asr",
            "sparkclaw-ocr",
        ):
            self.assertNotIn(f"  {service}:\n", base_text)
            self.assertIn(f"  {service}:\n", models_text)

    def test_shared_capacity_contract_uses_remote_context_and_output_budgets(self) -> None:
        catalog = json.loads((ROOT / "configs" / "model.profiles.json").read_text(encoding="utf-8"))
        profile = catalog["profiles"]["sparkclaw-product-v1"]

        self.assertEqual(profile["physical_models"]["product-fast"]["context_tokens"], 262144)
        self.assertEqual(profile["physical_models"]["product-embedding"]["context_tokens"], 8192)
        self.assertEqual(profile["physical_models"]["product-guard"]["context_tokens"], 8192)
        self.assertEqual(profile["physical_models"]["product-ocr"]["context_tokens"], 32768)
        self.assertEqual(
            profile["lanes"]["fast"]["output_budgets"],
            {
                "compact_structured": 2048,
                "workflow_structured": 8192,
                "answer": 8192,
                "vision_structured": 4096,
            },
        )
        self.assertEqual(
            profile["lanes"]["deep"]["output_budgets"],
            {"workflow_structured": 8192, "answer": 8192},
        )

    def test_gateway_image_keeps_host_cdp_and_document_runtime(self) -> None:
        dockerfile = GATEWAY_DOCKERFILE.read_text(encoding="utf-8")

        self.assertIn("COPY configs /app/configs", dockerfile)
        self.assertNotIn(" chromium", dockerfile.lower())
        self.assertNotIn("xvfb", dockerfile.lower())
        self.assertIn("host_browser_mcp_smoke.mjs", dockerfile)
        self.assertIn("pypdfium2==5.12.1", DOCUMENT_REQUIREMENTS.read_text(encoding="utf-8"))

    def test_webchat_pairing_proxy_is_loopback_only_and_route_bounded(self) -> None:
        nginx = WEBCHAT_NGINX.read_text(encoding="utf-8")
        pairing_server = nginx.split("listen 18795;", 1)[1].split("listen ${SPARKCLAW_JINGSI_LAN_PORT};", 1)[0]
        main_server = nginx.split("listen 18795;", 1)[0]

        self.assertNotIn("X-SparkClaw-WebChat-Proxy", main_server)
        self.assertIn("location = /api/pairing/start", pairing_server)
        self.assertIn("location = /api/pairing/claim", pairing_server)
        self.assertIn("X-SparkClaw-WebChat-Proxy", pairing_server)
        self.assertIn("location / {\n    return 404;", pairing_server)


if __name__ == "__main__":
    unittest.main()
