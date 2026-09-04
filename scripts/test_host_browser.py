#!/usr/bin/env python3

from __future__ import annotations

import importlib.util
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest import mock


sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[1]
MANIFEST = ROOT / "configs" / "host-browser-artifacts.json"
INSTALLER = ROOT / "scripts" / "install-host-browser.sh"
CONTROLLER_SETUP = ROOT / "scripts" / "setup-browser-controller.sh"


def load_module(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load {path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


artifacts = load_module(
    "sparkclaw_host_browser_artifacts", ROOT / "scripts" / "host_browser_artifacts.py"
)
browserd = load_module(
    "sparkclaw_browserd", ROOT / "scripts" / "sparkclaw_browserd.py"
)


class HostBrowserTest(unittest.TestCase):
    def test_installer_skips_interactive_sudo_refresh_when_passwordless_sudo_works(self) -> None:
        source = INSTALLER.read_text(encoding="utf-8")

        self.assertIn("if ! sudo -n env true >/dev/null 2>&1; then", source)
        self.assertIn("  sudo -v\nfi", source)

    def test_browser_version_validation_normalizes_trailing_whitespace(self) -> None:
        completed = subprocess.CompletedProcess(
            ["chrome", "--version"],
            0,
            stdout="Chromium 148.0.7778.0 \n",
            stderr="",
        )
        executable = Path("/opt/sparkclaw/chromium-test/chrome")
        with mock.patch.object(Path, "is_file", return_value=True), mock.patch.object(
            browserd.subprocess, "run", return_value=completed
        ):
            self.assertEqual(
                browserd.validate_executable(
                    executable, "Chromium 148.0.7778.0 "
                ),
                "Chromium 148.0.7778.0",
            )

    def test_browser_sandbox_requires_root_owned_setuid_executable(self) -> None:
        executable = Path("/opt/sparkclaw/chromium-test/chrome")
        valid = mock.Mock(st_mode=stat.S_IFREG | stat.S_ISUID | 0o755, st_uid=0)
        with mock.patch.object(Path, "lstat", return_value=valid), mock.patch.object(
            Path, "is_symlink", return_value=False
        ), mock.patch.object(browserd.os, "access", return_value=True):
            self.assertEqual(
                browserd.validate_sandbox(executable),
                executable.with_name("chrome_sandbox"),
            )

        invalid = mock.Mock(st_mode=stat.S_IFREG | 0o755, st_uid=0)
        with mock.patch.object(Path, "lstat", return_value=invalid), mock.patch.object(
            Path, "is_symlink", return_value=False
        ), mock.patch.object(browserd.os, "access", return_value=True):
            with self.assertRaisesRegex(ValueError, "root-owned setuid"):
                browserd.validate_sandbox(executable)

    def test_generation_epoch_is_nonzero_and_opaque(self) -> None:
        with mock.patch.object(browserd.secrets, "randbits", return_value=0):
            self.assertEqual(browserd.new_generation_epoch(), 1)
        with mock.patch.object(browserd.secrets, "randbits", return_value=123456789):
            self.assertEqual(browserd.new_generation_epoch(), 123456789)

    def test_manifest_resolves_only_approved_architecture_artifacts(self) -> None:
        arm = artifacts.load_artifact(MANIFEST, "arm64")
        x86 = artifacts.load_artifact(MANIFEST, "x86_64")

        self.assertEqual(
            arm["sha256"],
            "3f7fd102f646dad864a987de04782e238e98a3d38543a1c8be0129b96706a283",
        )
        self.assertEqual(arm["playwrightBuild"], "1223")
        self.assertEqual(
            x86["sha256"],
            "c9058012550254b5b658f3f111369c8df60c48406946da25673ba27e0220e8ac",
        )
        self.assertEqual(x86["version"], "148.0.7778.0")
        with self.assertRaisesRegex(ValueError, "unsupported"):
            artifacts.load_artifact(MANIFEST, "ppc64le")

    def test_manifest_rejects_untrusted_url_or_checksum(self) -> None:
        value = json.loads(MANIFEST.read_text(encoding="utf-8"))
        value["artifacts"]["arm64"]["url"] = "http://example.invalid/chromium.zip"
        value["artifacts"]["arm64"]["sha256"] = "not-a-checksum"
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "manifest.json"
            path.write_text(json.dumps(value), encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "URL"):
                artifacts.load_artifact(path, "arm64")

    def test_endpoint_publication_is_atomic_and_mode_0600(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "runtime" / "cdp-endpoint"
            browserd.atomic_write_json(path, {"version": 1, "browserPID": 123})

            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            self.assertEqual(
                json.loads(path.read_text(encoding="utf-8")),
                {"version": 1, "browserPID": 123},
            )
            self.assertEqual(list(path.parent.glob(".cdp-endpoint.*")), [])

    def test_docker_bridge_discovery_tracks_docker0_and_compose_bridges(self) -> None:
        output = "\n".join(
            (
                "1: lo    inet 127.0.0.1/8 scope host lo",
                "2: eth0    inet 192.0.2.10/24 scope global eth0",
                "3: docker0    inet 172.17.0.1/16 scope global docker0",
                "4: br-abc@if5    inet 172.18.0.1/16 scope global br-abc",
            )
        )
        completed = subprocess.CompletedProcess(["ip"], 0, stdout=output, stderr="")
        with mock.patch.object(browserd.subprocess, "run", return_value=completed):
            self.assertEqual(
                browserd.docker_bridge_addresses(),
                {"172.17.0.1", "172.18.0.1"},
            )

    def test_browserd_requires_the_loopback_proxy_listener(self) -> None:
        daemon = browserd.BrowserDaemon.__new__(browserd.BrowserDaemon)
        daemon.proxy_lock = browserd.threading.Lock()
        daemon.proxy_servers = {}
        daemon.proxy_state = browserd.ProxyState()
        daemon.config = {"proxyPort": 18791}

        with mock.patch.object(browserd, "CDPProxyServer", side_effect=OSError("busy")):
            with self.assertRaisesRegex(OSError, "busy"):
                daemon._start_proxy_servers()

    def test_open_or_focus_records_display_during_browser_restart(self) -> None:
        daemon = browserd.BrowserDaemon.__new__(browserd.BrowserDaemon)
        daemon.desired_display = ""
        daemon.desired_xauthority = ""
        daemon.lock = browserd.threading.Lock()
        daemon.presentation = "unavailable"
        daemon.generation = 1
        daemon.launch_revision = 0
        daemon.ready_event = mock.Mock()
        daemon.ready_event.wait.return_value = False

        with mock.patch.object(
            browserd,
            "validate_display",
            return_value=(":1", "/run/user/1000/gdm/Xauthority"),
        ):
            with self.assertRaisesRegex(RuntimeError, "unavailable"):
                daemon.open_or_focus(
                    {"display": ":1", "xauthority": "/tmp/ignored"}
                )

        self.assertEqual(daemon.desired_display, ":1")
        self.assertEqual(
            daemon.desired_xauthority, "/run/user/1000/gdm/Xauthority"
        )

    def test_hidden_transition_sets_headless_before_restart(self) -> None:
        daemon = browserd.BrowserDaemon.__new__(browserd.BrowserDaemon)
        daemon.lock = browserd.threading.Lock()
        daemon.presentation = "headed"
        daemon.generation = 4
        daemon.launch_revision = 0
        daemon.force_headless = False
        daemon.ready_event = browserd.threading.Event()
        daemon.ready_event.set()
        daemon.process = mock.Mock()
        daemon.process.poll.return_value = None
        daemon.config = {"profileID": "default"}
        daemon.browser_version = "test"

        def stop_browser() -> None:
            daemon.presentation = "headless"
            daemon.generation = 5
            daemon.ready_event.set()

        daemon._stop_browser = stop_browser
        result = daemon.ensure_hidden()

        self.assertTrue(daemon.force_headless)
        self.assertEqual(result["presentation"], "headless")
        self.assertEqual(result["generation"], 5)

    def test_headed_transition_requires_configured_display(self) -> None:
        daemon = browserd.BrowserDaemon.__new__(browserd.BrowserDaemon)
        daemon.lock = browserd.threading.Lock()
        daemon.presentation = "headless"
        daemon.generation = 1
        daemon.force_headless = True
        daemon.desired_display = ""
        daemon.desired_xauthority = ""
        daemon.ready_event = browserd.threading.Event()

        with self.assertRaisesRegex(RuntimeError, "usable desktop display"):
            daemon._ensure_presentation("headed")

    def test_manual_login_browser_has_no_cdp_or_headless_flags(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            daemon = browserd.BrowserDaemon.__new__(browserd.BrowserDaemon)
            daemon.executable = Path("/opt/sparkclaw/chromium-test/chrome")
            daemon.profile_dir = Path(directory) / "profile"
            daemon.runtime_dir = Path(directory) / "runtime"
            daemon.profile_dir.mkdir()
            daemon.runtime_dir.mkdir()
            daemon.sandbox = Path("/opt/sparkclaw/chromium-test/chrome_sandbox")
            daemon.desired_display = ":1"
            daemon.desired_xauthority = "/run/user/1000/gdm/Xauthority"
            process = mock.Mock()
            process.poll.return_value = None

            with mock.patch.object(
                browserd, "validate_display", return_value=(":1", "/tmp/xauthority")
            ), mock.patch.object(
                browserd.subprocess, "Popen", return_value=process
            ) as popen, mock.patch.object(
                daemon, "_publish_manual_login_browser"
            ) as publish:
                self.assertIs(
                    daemon._start_manual_login_browser("https://mail.google.com/"),
                    process,
                )

            args = popen.call_args.args[0]
            self.assertFalse(any(arg.startswith("--remote-debugging") for arg in args))
            self.assertFalse(any(arg.startswith("--headless") for arg in args))
            self.assertEqual(args[-1], "https://mail.google.com/")
            publish.assert_called_once_with(process)

    def test_old_manual_login_exit_cannot_replace_a_new_login_request(self) -> None:
        daemon = browserd.BrowserDaemon.__new__(browserd.BrowserDaemon)
        daemon.lock = browserd.threading.Lock()
        process = mock.Mock()
        daemon.process = process
        daemon.launch_mode = "manual-login"
        daemon.launch_revision = 8
        daemon.manual_login_url = "https://outlook.live.com/mail/"
        daemon.force_headless = False

        daemon._finish_manual_login(process, 7)

        self.assertEqual(daemon.launch_mode, "manual-login")
        self.assertEqual(daemon.launch_revision, 8)
        self.assertEqual(
            daemon.manual_login_url, "https://outlook.live.com/mail/"
        )

    def test_manual_login_exit_restores_headless_automation(self) -> None:
        daemon = browserd.BrowserDaemon.__new__(browserd.BrowserDaemon)
        daemon.lock = browserd.threading.Lock()
        process = mock.Mock()
        daemon.process = process
        daemon.launch_mode = "manual-login"
        daemon.launch_revision = 3
        daemon.manual_login_url = "https://mail.google.com/"
        daemon.force_headless = False

        daemon._finish_manual_login(process, 3)

        self.assertEqual(daemon.launch_mode, "automation")
        self.assertEqual(daemon.launch_revision, 4)
        self.assertEqual(daemon.manual_login_url, "")
        self.assertTrue(daemon.force_headless)

    def test_installer_check_does_not_modify_environment_file(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "sparkclaw.env"
            original = "SPARKCLAW_BROWSER_CDP_PROFILE_ID=default\n"
            path.write_text(original, encoding="utf-8")
            path.chmod(0o600)
            before = path.stat()
            environment = os.environ.copy()
            environment["PYTHONDONTWRITEBYTECODE"] = "1"

            subprocess.run(
                ["bash", str(INSTALLER), "--check", "--env-file", str(path)],
                cwd=ROOT,
                env=environment,
                check=False,
                capture_output=True,
                text=True,
                timeout=20,
            )

            after = path.stat()
            self.assertEqual(path.read_text(encoding="utf-8"), original)
            self.assertEqual(stat.S_IMODE(after.st_mode), 0o600)
            self.assertEqual(after.st_ino, before.st_ino)
            self.assertEqual(list(Path(directory).iterdir()), [path])

    def test_container_browser_artifacts_are_retired(self) -> None:
        dockerfile = (ROOT / "docker" / "images" / "gateway.Dockerfile").read_text(
            encoding="utf-8"
        ).lower()
        compose = (ROOT / "docker" / "compose.yaml").read_text(encoding="utf-8")

        self.assertNotIn("xvfb", dockerfile)
        self.assertNotRegex(dockerfile, r"apt-get install[^\n]*\bchromium\b")
        self.assertNotIn("browser-profiles", compose)
        self.assertFalse((ROOT / "docker" / "compose.visible-browser.yaml").exists())
        self.assertFalse((ROOT / "scripts" / "resolve-chromium.sh").exists())

    def test_gateway_receives_the_preview_controller_socket_without_a_browser_binary(self) -> None:
        compose = (ROOT / "docker" / "compose.yaml").read_text(encoding="utf-8")
        product = (ROOT / "docker" / "env" / "sparkclaw.product.env").read_text(
            encoding="utf-8"
        )

        self.assertIn("SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET", compose)
        self.assertIn(
            ":/run/sparkclaw/browser-controller:ro", compose
        )
        self.assertIn(
            "SPARKCLAW_BROWSER_EXTENSION_RUNTIME_DIR_HOST=", product
        )
        self.assertNotIn("PLAYWRIGHT_MCP_EXTENSION_TOKEN", compose)

    def test_preview_controller_setup_is_explicit_and_uses_a_disposable_profile(self) -> None:
        setup = CONTROLLER_SETUP.read_text(encoding="utf-8")
        opener = (ROOT / "scripts" / "open-browser-extension-preview.sh").read_text(
            encoding="utf-8"
        )

        self.assertIn("npm ci --prefix", setup)
        self.assertIn("PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1", setup)
        self.assertIn("npm_config_audit=false", setup)
        self.assertIn("sparkclaw-browser-controller.service", setup)
        self.assertIn("WorkingDirectory=$PACKAGE_DIR", setup)
        self.assertNotIn("WorkingDirectory=$(systemd_quote", setup)
        self.assertIn("SPARKCLAW_BROWSER_CHANNEL=chromium", setup)
        self.assertNotIn("SPARKCLAW_BROWSER_CHANNEL=chrome\"", setup)
        self.assertIn("extension-qualification/user-data", setup)
        self.assertIn("extension-qualification/user-data", opener)
        self.assertNotIn("PLAYWRIGHT_MCP_EXTENSION_TOKEN=", setup)

    def test_gateway_extension_timeout_exceeds_controller_handshake_timeout(self) -> None:
        product = (ROOT / "docker" / "env" / "sparkclaw.product.env").read_text(
            encoding="utf-8"
        )
        controller = (ROOT / "tools" / "browser-controller" / "src" / "main.mjs").read_text(
            encoding="utf-8"
        )

        gateway_match = re.search(
            r"^SPARKCLAW_BROWSER_EXTENSION_CONNECT_TIMEOUT_MS=(\d+)$",
            product,
            re.MULTILINE,
        )
        controller_match = re.search(
            r'SPARKCLAW_BROWSER_CONNECT_TIMEOUT_MS",\s*([\d_]+)', controller
        )
        self.assertIsNotNone(gateway_match)
        self.assertIsNotNone(controller_match)
        gateway_timeout = int(gateway_match.group(1))
        controller_timeout = int(controller_match.group(1).replace("_", ""))

        self.assertGreater(gateway_timeout, controller_timeout)
        self.assertEqual(gateway_timeout, 20_000)
        self.assertEqual(controller_timeout, 15_000)

    def test_preview_controller_install_and_check_use_only_private_host_state(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            temporary = Path(directory)
            home = temporary / "home"
            bin_dir = temporary / "bin"
            config_home = temporary / "config"
            data_home = temporary / "data"
            runtime_home = temporary / "runtime"
            browser_runtime = runtime_home / "sparkclaw" / "browser-controller"
            socket_path = browser_runtime / "controller.sock"
            env_file = temporary / "sparkclaw.env"
            systemctl_log = temporary / "systemctl.log"

            for path in (home, bin_dir, config_home, data_home, runtime_home):
                path.mkdir(parents=True)

            browser = temporary / "chromium"
            browser.write_text("#!/usr/bin/env bash\nexit 0\n", encoding="utf-8")
            browser.chmod(0o755)
            browser_config = config_home / "sparkclaw" / "browserd.json"
            browser_config.parent.mkdir(parents=True)
            browser_config.write_text(
                json.dumps({"executable": str(browser)}), encoding="utf-8"
            )
            env_file.write_text(
                "\n".join(
                    (
                        f"SPARKCLAW_BROWSER_EXTENSION_RUNTIME_DIR_HOST={browser_runtime}",
                        f"SPARKCLAW_BROWSER_EXTENSION_CONTROLLER_SOCKET_HOST={socket_path}",
                        "",
                    )
                ),
                encoding="utf-8",
            )
            env_file.chmod(0o600)

            self._write_executable(
                bin_dir / "node",
                """#!/usr/bin/env bash
if [[ "${1:-}" == "-p" ]]; then
  printf '26\\n'
fi
""",
            )
            self._write_executable(
                bin_dir / "npm",
                """#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then
  printf '11.17.0\\n'
fi
""",
            )
            self._write_executable(
                bin_dir / "systemctl",
                """#!/usr/bin/env bash
printf '%s\\n' "$*" >>"$SPARKCLAW_TEST_SYSTEMCTL_LOG"
if [[ " $* " == *" restart "* ]]; then
  rm -f -- "$SPARKCLAW_TEST_SOCKET"
  python3 -c 'import os, socket, sys; value = socket.socket(socket.AF_UNIX); value.bind(sys.argv[1]); value.close(); os.chmod(sys.argv[1], 0o600)' "$SPARKCLAW_TEST_SOCKET"
fi
""",
            )
            self._write_executable(
                bin_dir / "curl",
                """#!/usr/bin/env bash
printf '{"schema_version":1,"profile_id":"default","state":"ready"}\\n'
""",
            )

            environment = os.environ.copy()
            environment.update(
                {
                    "HOME": str(home),
                    "PATH": f"{bin_dir}:{environment['PATH']}",
                    "PYTHONDONTWRITEBYTECODE": "1",
                    "XDG_CONFIG_HOME": str(config_home),
                    "XDG_DATA_HOME": str(data_home),
                    "XDG_RUNTIME_DIR": str(runtime_home),
                    "SPARKCLAW_TEST_SOCKET": str(socket_path),
                    "SPARKCLAW_TEST_SYSTEMCTL_LOG": str(systemctl_log),
                    "PLAYWRIGHT_MCP_EXTENSION_TOKEN": "test-token-must-not-persist",
                }
            )

            installed = subprocess.run(
                ["bash", str(CONTROLLER_SETUP), "--env-file", str(env_file)],
                cwd=ROOT,
                env=environment,
                check=False,
                capture_output=True,
                text=True,
                timeout=20,
            )
            self.assertEqual(installed.returncode, 0, installed.stderr)
            unit_path = config_home / "systemd" / "user" / "sparkclaw-browser-controller.service"
            unit = unit_path.read_text(encoding="utf-8")
            configured = env_file.read_text(encoding="utf-8")
            service_calls = systemctl_log.read_text(encoding="utf-8")

            self.assertTrue(socket_path.is_socket())
            self.assertEqual(stat.S_IMODE(browser_runtime.stat().st_mode), 0o700)
            self.assertEqual(stat.S_IMODE(socket_path.stat().st_mode), 0o600)
            self.assertEqual(stat.S_IMODE(unit_path.stat().st_mode), 0o600)
            self.assertIn(str(data_home / "sparkclaw/browser/extension-qualification/user-data"), unit)
            self.assertIn("SPARKCLAW_BROWSER_CHANNEL=chromium", unit)
            self.assertIn("enable sparkclaw-browser-controller.service", service_calls)
            self.assertIn("restart sparkclaw-browser-controller.service", service_calls)
            self.assertNotIn(environment["PLAYWRIGHT_MCP_EXTENSION_TOKEN"], unit)
            self.assertNotIn(environment["PLAYWRIGHT_MCP_EXTENSION_TOKEN"], configured)
            self.assertNotIn(environment["PLAYWRIGHT_MCP_EXTENSION_TOKEN"], service_calls)

            before = env_file.read_bytes()
            checked = subprocess.run(
                [
                    "bash",
                    str(CONTROLLER_SETUP),
                    "--check",
                    "--env-file",
                    str(env_file),
                ],
                cwd=ROOT,
                env=environment,
                check=False,
                capture_output=True,
                text=True,
                timeout=20,
            )
            self.assertEqual(checked.returncode, 0, checked.stderr)
            self.assertEqual(env_file.read_bytes(), before)

    @staticmethod
    def _write_executable(path: Path, content: str) -> None:
        path.write_text(content, encoding="utf-8")
        path.chmod(0o755)

    def test_installer_restarts_browserd_after_reconciling_files(self) -> None:
        installer = INSTALLER.read_text(encoding="utf-8")

        self.assertIn(
            "systemctl --user enable sparkclaw-browserd.service", installer
        )
        self.assertIn(
            "systemctl --user restart sparkclaw-browserd.service", installer
        )
        self.assertNotIn(
            "systemctl --user enable --now sparkclaw-browserd.service", installer
        )

    def test_mcp_smoke_owns_and_closes_a_dedicated_tab(self) -> None:
        smoke = (ROOT / "scripts" / "host_browser_mcp_smoke.mjs").read_text(
            encoding="utf-8"
        )

        self.assertIn('callTool("agent_browser_tab_new"', smoke)
        self.assertIn('callTool("agent_browser_tab_switch", { tab: smokeTab })', smoke)
        self.assertIn('callTool("agent_browser_tab_close", { tab: smokeTab })', smoke)
        self.assertIn('callTool("agent_browser_close")', smoke)
        self.assertNotIn('callTool("agent_browser_open"', smoke)


if __name__ == "__main__":
    unittest.main()
