#!/usr/bin/env python3
"""Host-owned Chromium lifecycle and capability-gated CDP proxy."""

from __future__ import annotations

import argparse
import base64
import hashlib
import http.client
import http.server
import json
import os
from pathlib import Path
import secrets
import select
import signal
import socket
import socketserver
import stat
import struct
import subprocess
import sys
import threading
import time
from typing import Any
from urllib.parse import urlsplit


DEFAULT_PROXY_PORT = 18791
MAX_CONTROL_BYTES = 64 << 10
MAX_CDP_MESSAGE_BYTES = 4 << 20


def new_generation_epoch() -> int:
    return secrets.randbits(63) or 1


def default_runtime_dir() -> Path:
    root = os.environ.get("XDG_RUNTIME_DIR", f"/run/user/{os.getuid()}")
    return Path(root) / "sparkclaw" / "browserd"


def default_config_path() -> Path:
    root = os.environ.get("XDG_CONFIG_HOME", str(Path.home() / ".config"))
    return Path(root) / "sparkclaw" / "browserd.json"


def load_config(path: Path) -> dict[str, Any]:
    with path.open("r", encoding="utf-8") as stream:
        config = json.load(stream)
    required = (
        "executable",
        "profileDir",
        "runtimeDir",
        "profileID",
        "browserVersion",
    )
    missing = [key for key in required if not str(config.get(key, "")).strip()]
    if missing:
        raise ValueError(f"browserd config is missing: {', '.join(missing)}")
    allowed = {
        "version",
        "executable",
        "profileDir",
        "runtimeDir",
        "profileID",
        "browserVersion",
        "proxyPort",
        "display",
        "xauthority",
    }
    unknown = sorted(set(config) - allowed)
    if unknown:
        raise ValueError(f"browserd config has unknown fields: {', '.join(unknown)}")
    if config.get("version") != 1:
        raise ValueError("browserd config version must be 1")
    config["proxyPort"] = int(config.get("proxyPort", DEFAULT_PROXY_PORT))
    if not 1024 <= config["proxyPort"] <= 65535:
        raise ValueError("browserd proxyPort must be between 1024 and 65535")
    return config


def validate_executable(executable: Path, expected_version: str) -> str:
    if not executable.is_absolute() or not executable.is_file():
        raise ValueError("browserd executable must be an absolute regular file")
    resolved = executable.resolve()
    expected_root = Path("/opt/sparkclaw").resolve()
    if expected_root not in resolved.parents:
        raise ValueError("browserd executable must be installed under /opt/sparkclaw")
    output = subprocess.run(
        [str(resolved), "--version"],
        check=True,
        capture_output=True,
        text=True,
        timeout=10,
    ).stdout.strip()
    expected_version = expected_version.strip()
    if not expected_version or output != expected_version:
        raise ValueError(
            f"browserd requires version {expected_version}, got {output!r}"
        )
    return output


def validate_sandbox(executable: Path) -> Path:
    sandbox = executable.with_name("chrome_sandbox")
    try:
        info = sandbox.lstat()
    except OSError as error:
        raise ValueError("browserd sandbox is missing") from error
    if (
        not stat.S_ISREG(info.st_mode)
        or sandbox.is_symlink()
        or info.st_uid != 0
        or info.st_mode & stat.S_ISUID == 0
        or not os.access(sandbox, os.X_OK)
    ):
        raise ValueError("browserd sandbox must be a root-owned setuid executable")
    return sandbox.resolve()


def validate_display(display: str, xauthority: str) -> tuple[str, str] | None:
    display = display.strip()
    xauthority = xauthority.strip()
    if not display.startswith(":"):
        return None
    number = display[1:].split(".", 1)[0]
    if not number.isdigit():
        return None
    x_socket = Path("/tmp/.X11-unix") / f"X{number}"
    authority = Path(xauthority)
    if not x_socket.exists() or not authority.is_file() or authority.stat().st_size == 0:
        return None
    return display, str(authority)


def docker_bridge_addresses() -> set[str]:
    addresses: set[str] = set()
    try:
        output = subprocess.run(
            ["ip", "-4", "-o", "addr", "show"],
            check=True,
            capture_output=True,
            text=True,
            timeout=5,
        ).stdout
    except (FileNotFoundError, subprocess.SubprocessError):
        return set()
    for line in output.splitlines():
        fields = line.split()
        if len(fields) < 4:
            continue
        interface = fields[1].split("@", 1)[0].rstrip(":")
        if interface != "docker0" and not interface.startswith("br-"):
            continue
        for token in fields[2:]:
            if "/" not in token:
                continue
            address = token.split("/", 1)[0]
            try:
                socket.inet_aton(address)
            except OSError:
                continue
            addresses.add(address)
            break
    return addresses


def atomic_write_json(path: Path, value: dict[str, Any], mode: int = 0o600) -> None:
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(path.parent, 0o700)
    temporary = path.with_name(f".{path.name}.{os.getpid()}.{secrets.token_hex(4)}")
    raw = (json.dumps(value, sort_keys=True, separators=(",", ":")) + "\n").encode()
    descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, mode)
    try:
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(raw)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        os.chmod(path, mode)
    finally:
        try:
            temporary.unlink()
        except FileNotFoundError:
            pass


class ProxyState:
    def __init__(self) -> None:
        self.lock = threading.Lock()
        self.capability = ""
        self.raw_port = 0
        self.raw_browser_path = ""

    def publish(self, capability: str, raw_port: int, raw_browser_path: str) -> None:
        with self.lock:
            self.capability = capability
            self.raw_port = raw_port
            self.raw_browser_path = raw_browser_path

    def clear(self) -> None:
        self.publish("", 0, "")

    def authorize(self, request_path: str) -> tuple[int, str] | None:
        path = urlsplit(request_path).path
        with self.lock:
            capability = self.capability
            raw_port = self.raw_port
        prefix = f"/{capability}/" if capability else ""
        if not prefix or not path.startswith(prefix) or raw_port <= 0:
            return None
        upstream_path = "/" + path[len(prefix) :]
        if not upstream_path.startswith(("/devtools/", "/json/")):
            return None
        return raw_port, upstream_path


class CDPProxyServer(http.server.ThreadingHTTPServer):
    daemon_threads = True
    allow_reuse_address = True

    def __init__(self, address: tuple[str, int], state: ProxyState):
        self.proxy_state = state
        super().__init__(address, CDPProxyHandler)


class CDPProxyHandler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, _format: str, *_args: Any) -> None:
        return

    def do_GET(self) -> None:
        authorized = self.server.proxy_state.authorize(self.path)  # type: ignore[attr-defined]
        if authorized is None:
            self.send_error(404)
            return
        raw_port, upstream_path = authorized
        if self.headers.get("Upgrade", "").lower() == "websocket":
            self._proxy_websocket(raw_port, upstream_path)
        else:
            self._proxy_http(raw_port, upstream_path)

    def _upstream_headers(self) -> dict[str, str]:
        blocked = {"connection", "host", "proxy-connection"}
        return {
            key: value
            for key, value in self.headers.items()
            if key.lower() not in blocked
        }

    def _proxy_http(self, raw_port: int, upstream_path: str) -> None:
        connection = http.client.HTTPConnection("127.0.0.1", raw_port, timeout=10)
        try:
            connection.request("GET", upstream_path, headers=self._upstream_headers())
            response = connection.getresponse()
            body = response.read(MAX_CDP_MESSAGE_BYTES + 1)
            if len(body) > MAX_CDP_MESSAGE_BYTES:
                self.send_error(502)
                return
            self.send_response(response.status)
            for key, value in response.getheaders():
                if key.lower() not in {"connection", "transfer-encoding"}:
                    self.send_header(key, value)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        finally:
            connection.close()

    def _proxy_websocket(self, raw_port: int, upstream_path: str) -> None:
        upstream = socket.create_connection(("127.0.0.1", raw_port), timeout=10)
        try:
            headers = [f"GET {upstream_path} HTTP/1.1", f"Host: 127.0.0.1:{raw_port}"]
            for key, value in self.headers.items():
                if key.lower() not in {"host", "proxy-connection"}:
                    headers.append(f"{key}: {value}")
            upstream.sendall(("\r\n".join(headers) + "\r\n\r\n").encode("latin-1"))
            response = bytearray()
            while b"\r\n\r\n" not in response and len(response) <= 64 << 10:
                chunk = upstream.recv(4096)
                if not chunk:
                    break
                response.extend(chunk)
            if not response.startswith(b"HTTP/1.1 101"):
                self.send_error(502)
                return
            self.connection.sendall(response)
            upstream.settimeout(None)
            self.connection.settimeout(None)
            self._tunnel(self.connection, upstream)
        finally:
            upstream.close()

    @staticmethod
    def _tunnel(client: socket.socket, upstream: socket.socket) -> None:
        sockets = [client, upstream]
        while True:
            readable, _, exceptional = select.select(sockets, [], sockets, 60)
            if exceptional:
                return
            if not readable:
                continue
            for source in readable:
                target = upstream if source is client else client
                data = source.recv(64 << 10)
                if not data:
                    return
                target.sendall(data)


class CDPClient:
    def __init__(self, port: int, browser_path: str):
        self.port = port
        self.browser_path = browser_path
        self.socket: socket.socket | None = None
        self.next_id = 1

    def __enter__(self) -> "CDPClient":
        key = base64.b64encode(os.urandom(16)).decode()
        connection = socket.create_connection(("127.0.0.1", self.port), timeout=10)
        request = (
            f"GET {self.browser_path} HTTP/1.1\r\n"
            f"Host: 127.0.0.1:{self.port}\r\n"
            "Upgrade: websocket\r\nConnection: Upgrade\r\n"
            f"Sec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n\r\n"
        )
        connection.sendall(request.encode("ascii"))
        response = bytearray()
        while b"\r\n\r\n" not in response and len(response) <= 64 << 10:
            response.extend(connection.recv(4096))
        if not response.startswith(b"HTTP/1.1 101"):
            connection.close()
            raise RuntimeError("Chromium rejected the CDP WebSocket handshake")
        self.socket = connection
        return self

    def __exit__(self, *_args: Any) -> None:
        if self.socket is not None:
            self.socket.close()

    def call(self, method: str, params: dict[str, Any] | None = None) -> Any:
        request_id = self.next_id
        self.next_id += 1
        self._send_json({"id": request_id, "method": method, "params": params or {}})
        while True:
            message = self._read_json()
            if message.get("id") != request_id:
                continue
            if "error" in message:
                raise RuntimeError(f"CDP {method} failed")
            return message.get("result")

    def _send_json(self, value: dict[str, Any]) -> None:
        assert self.socket is not None
        payload = json.dumps(value, separators=(",", ":")).encode()
        mask = os.urandom(4)
        first = bytes([0x81])
        length = len(payload)
        if length < 126:
            header = first + bytes([0x80 | length])
        elif length <= 0xFFFF:
            header = first + bytes([0x80 | 126]) + struct.pack("!H", length)
        else:
            header = first + bytes([0x80 | 127]) + struct.pack("!Q", length)
        masked = bytes(byte ^ mask[index % 4] for index, byte in enumerate(payload))
        self.socket.sendall(header + mask + masked)

    def _read_json(self) -> dict[str, Any]:
        assert self.socket is not None
        while True:
            first, second = self._recv_exact(2)
            opcode = first & 0x0F
            length = second & 0x7F
            if length == 126:
                length = struct.unpack("!H", self._recv_exact(2))[0]
            elif length == 127:
                length = struct.unpack("!Q", self._recv_exact(8))[0]
            if length > MAX_CDP_MESSAGE_BYTES:
                raise RuntimeError("CDP message exceeds browserd limit")
            payload = self._recv_exact(length)
            if opcode == 0x8:
                raise RuntimeError("CDP WebSocket closed")
            if opcode == 0x9:
                self._send_control(0xA, payload)
                continue
            if opcode == 0x1:
                return json.loads(payload.decode("utf-8"))

    def _send_control(self, opcode: int, payload: bytes) -> None:
        assert self.socket is not None
        mask = os.urandom(4)
        masked = bytes(byte ^ mask[index % 4] for index, byte in enumerate(payload))
        self.socket.sendall(bytes([0x80 | opcode, 0x80 | len(payload)]) + mask + masked)

    def _recv_exact(self, size: int) -> bytes:
        assert self.socket is not None
        output = bytearray()
        while len(output) < size:
            chunk = self.socket.recv(size - len(output))
            if not chunk:
                raise RuntimeError("CDP WebSocket ended unexpectedly")
            output.extend(chunk)
        return bytes(output)


class BrowserDaemon:
    def __init__(self, config: dict[str, Any]):
        self.config = config
        self.executable = Path(config["executable"])
        self.browser_version = str(config["browserVersion"]).strip()
        self.profile_dir = Path(config["profileDir"])
        self.runtime_dir = Path(config["runtimeDir"])
        self.endpoint_file = self.runtime_dir / "cdp-endpoint"
        self.control_socket = self.runtime_dir / "control.sock"
        self.proxy_state = ProxyState()
        self.stop_event = threading.Event()
        self.ready_event = threading.Event()
        self.lock = threading.Lock()
        self.process: subprocess.Popen[bytes] | None = None
        self.raw_port = 0
        self.raw_browser_path = ""
        self.presentation = "unavailable"
        self.generation = new_generation_epoch()
        self.desired_display = str(config.get("display", ""))
        self.desired_xauthority = str(config.get("xauthority", ""))
        self.force_headless = False
        self.launch_mode = "automation"
        self.launch_revision = 0
        self.manual_login_url = ""
        self.proxy_servers: dict[str, CDPProxyServer] = {}
        self.proxy_lock = threading.Lock()
        self.control_server: socketserver.ThreadingUnixStreamServer | None = None
        self.version_text = validate_executable(self.executable, self.browser_version)
        self.sandbox = validate_sandbox(self.executable)

    def serve(self) -> None:
        self.runtime_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.profile_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
        os.chmod(self.runtime_dir, 0o700)
        os.chmod(self.profile_dir, 0o700)
        try:
            self.control_socket.unlink()
        except FileNotFoundError:
            pass
        self._start_proxy_servers()
        proxy_thread = threading.Thread(target=self._proxy_loop, daemon=True)
        proxy_thread.start()
        self._start_control_server()
        browser_thread = threading.Thread(target=self._browser_loop, daemon=True)
        browser_thread.start()
        while not self.stop_event.wait(0.5):
            pass
        self._stop_browser()
        browser_thread.join(timeout=10)
        proxy_thread.join(timeout=5)
        with self.proxy_lock:
            proxy_servers = list(self.proxy_servers.values())
        for server in proxy_servers:
            server.shutdown()
            server.server_close()
        if self.control_server is not None:
            self.control_server.shutdown()
            self.control_server.server_close()
        for path in (self.endpoint_file, self.control_socket):
            try:
                path.unlink()
            except FileNotFoundError:
                pass

    def _start_proxy_servers(self) -> None:
        self._start_proxy_server("127.0.0.1", required=True)

    def _proxy_loop(self) -> None:
        while not self.stop_event.wait(1):
            for address in docker_bridge_addresses():
                self._start_proxy_server(address)

    def _start_proxy_server(self, address: str, *, required: bool = False) -> None:
        with self.proxy_lock:
            if address in self.proxy_servers:
                return
            try:
                server = CDPProxyServer(
                    (address, int(self.config["proxyPort"])), self.proxy_state
                )
            except OSError:
                if required:
                    raise
                return
            self.proxy_servers[address] = server
        threading.Thread(target=server.serve_forever, daemon=True).start()

    def _start_control_server(self) -> None:
        daemon = self

        class ControlHandler(socketserver.StreamRequestHandler):
            def handle(self) -> None:
                raw = self.rfile.readline(MAX_CONTROL_BYTES + 1)
                if len(raw) > MAX_CONTROL_BYTES:
                    return
                try:
                    request = json.loads(raw)
                    response = daemon.handle_control(request)
                except Exception as error:  # noqa: BLE001
                    response = {"ok": False, "error": str(error)}
                self.wfile.write((json.dumps(response, separators=(",", ":")) + "\n").encode())

        class ControlServer(socketserver.ThreadingUnixStreamServer):
            daemon_threads = True

        self.control_server = ControlServer(str(self.control_socket), ControlHandler)
        os.chmod(self.control_socket, 0o600)
        threading.Thread(target=self.control_server.serve_forever, daemon=True).start()

    def _browser_loop(self) -> None:
        while not self.stop_event.is_set():
            try:
                with self.lock:
                    launch_mode = self.launch_mode
                    launch_revision = self.launch_revision
                    manual_login_url = self.manual_login_url
                if launch_mode == "manual-login":
                    process = self._start_manual_login_browser(manual_login_url)
                else:
                    process = self._start_browser()
                process.wait()
                if launch_mode == "manual-login":
                    self._finish_manual_login(process, launch_revision)
            except Exception as error:  # noqa: BLE001
                print(f"sparkclaw-browserd: browser start failed: {error}", file=sys.stderr)
            finally:
                self._clear_browser_state()
            if not self.stop_event.wait(2):
                continue

    def _start_browser(self) -> subprocess.Popen[bytes]:
        active_port = self.profile_dir / "DevToolsActivePort"
        try:
            active_port.unlink()
        except FileNotFoundError:
            pass
        display = None if self.force_headless else validate_display(
            self.desired_display, self.desired_xauthority
        )
        presentation = "headed" if display else "headless"
        args = [
            str(self.executable),
            f"--user-data-dir={self.profile_dir}",
            "--remote-debugging-address=127.0.0.1",
            "--remote-debugging-port=0",
            "--no-first-run",
            "--no-default-browser-check",
            "--disable-component-update",
        ]
        environment = os.environ.copy()
        environment["CHROME_DEVEL_SANDBOX"] = str(self.sandbox)
        if display:
            environment["DISPLAY"], environment["XAUTHORITY"] = display
        else:
            environment.pop("DISPLAY", None)
            environment.pop("XAUTHORITY", None)
            args.append("--headless=new")
        args.append("about:blank")
        log_path = self.runtime_dir / "chromium.log"
        with log_path.open("ab", buffering=0) as log_stream:
            process = subprocess.Popen(
                args,
                env=environment,
                stdin=subprocess.DEVNULL,
                stdout=log_stream,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise RuntimeError(f"Chromium exited during startup ({process.returncode})")
            try:
                lines = active_port.read_text(encoding="utf-8").splitlines()
            except FileNotFoundError:
                lines = []
            if len(lines) >= 2 and lines[0].isdigit() and lines[1].startswith("/devtools/browser/"):
                self._publish_browser(process, int(lines[0]), lines[1], presentation)
                return process
            time.sleep(0.1)
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)
        raise RuntimeError("Chromium did not publish DevToolsActivePort")

    def _start_manual_login_browser(self, target_url: str) -> subprocess.Popen[bytes]:
        display = validate_display(self.desired_display, self.desired_xauthority)
        if display is None:
            raise RuntimeError("SparkClaw Browser has no usable desktop display")
        parsed = urlsplit(target_url)
        if (
            parsed.scheme != "https"
            or not parsed.hostname
            or parsed.username is not None
            or parsed.password is not None
            or parsed.fragment
        ):
            raise RuntimeError("manual login URL must be an ordinary HTTPS URL")
        args = [
            str(self.executable),
            f"--user-data-dir={self.profile_dir}",
            "--no-first-run",
            "--no-default-browser-check",
            "--disable-component-update",
            target_url,
        ]
        environment = os.environ.copy()
        environment["CHROME_DEVEL_SANDBOX"] = str(self.sandbox)
        environment["DISPLAY"], environment["XAUTHORITY"] = display
        log_path = self.runtime_dir / "chromium.log"
        with log_path.open("ab", buffering=0) as log_stream:
            process = subprocess.Popen(
                args,
                env=environment,
                stdin=subprocess.DEVNULL,
                stdout=log_stream,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise RuntimeError(
                    f"manual login Chromium exited during startup ({process.returncode})"
                )
            time.sleep(0.1)
            self._publish_manual_login_browser(process)
            return process
        raise RuntimeError("manual login Chromium did not start")

    def _publish_manual_login_browser(
        self, process: subprocess.Popen[bytes]
    ) -> None:
        self.proxy_state.clear()
        try:
            self.endpoint_file.unlink()
        except FileNotFoundError:
            pass
        with self.lock:
            self.process = process
            self.raw_port = 0
            self.raw_browser_path = ""
            self.presentation = "manual-login"
            self.generation += 1
        self.ready_event.set()

    def _finish_manual_login(
        self, process: subprocess.Popen[bytes], launch_revision: int
    ) -> None:
        with self.lock:
            if (
                self.process is process
                and self.launch_mode == "manual-login"
                and self.launch_revision == launch_revision
            ):
                self.launch_mode = "automation"
                self.launch_revision += 1
                self.manual_login_url = ""
                self.force_headless = True

    def _publish_browser(
        self,
        process: subprocess.Popen[bytes],
        raw_port: int,
        browser_path: str,
        presentation: str,
    ) -> None:
        capability = secrets.token_urlsafe(32)
        proxy_port = int(self.config["proxyPort"])
        with self.lock:
            self.process = process
            self.raw_port = raw_port
            self.raw_browser_path = browser_path
            self.presentation = presentation
            self.generation += 1
            generation = self.generation
        self.proxy_state.publish(capability, raw_port, browser_path)
        path = f"/{capability}{browser_path}"
        endpoint = {
            "version": 1,
            "profileID": str(self.config["profileID"]),
            "presentation": presentation,
            "browserPID": process.pid,
            "generation": generation,
            "browserVersion": self.browser_version,
            "webSocketURL": f"ws://host.docker.internal:{proxy_port}{path}",
            "hostWebSocketURL": f"ws://127.0.0.1:{proxy_port}{path}",
        }
        atomic_write_json(self.endpoint_file, endpoint)
        self.ready_event.set()

    def _clear_browser_state(self) -> None:
        self.proxy_state.clear()
        self.ready_event.clear()
        with self.lock:
            self.process = None
            self.raw_port = 0
            self.raw_browser_path = ""
            self.presentation = "unavailable"
        try:
            self.endpoint_file.unlink()
        except FileNotFoundError:
            pass

    def _stop_browser(self) -> None:
        with self.lock:
            process = getattr(self, "process", None)
        if process is None or process.poll() is not None:
            return
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)

    def handle_control(self, request: dict[str, Any]) -> dict[str, Any]:
        operation = str(request.get("operation", ""))
        if operation == "status":
            return self.status()
        if operation == "shutdown":
            self.stop_event.set()
            return {"ok": True}
        if operation == "open-or-focus":
            return self.open_or_focus(request)
        if operation == "ensure-headed":
            return self.ensure_headed(request)
        if operation == "open-login":
            return self.open_login(request)
        if operation == "ensure-hidden":
            return self.ensure_hidden()
        raise ValueError("unsupported browserd control operation")

    def status(self) -> dict[str, Any]:
        with self.lock:
            process = self.process
            return {
                "ok": bool(process and process.poll() is None and self.ready_event.is_set()),
                "browserPID": process.pid if process and process.poll() is None else 0,
                "presentation": self.presentation,
                "profileID": str(self.config["profileID"]),
                "browserVersion": self.browser_version,
                "generation": self.generation,
            }

    def open_or_focus(self, request: dict[str, Any]) -> dict[str, Any]:
        display = validate_display(
            str(request.get("display", "")), str(request.get("xauthority", ""))
        )
        if display:
            self.desired_display, self.desired_xauthority = display
        self._ensure_presentation("headed")
        if not self.ready_event.wait(25):
            raise RuntimeError("SparkClaw Browser is unavailable")
        with self.lock:
            raw_port = self.raw_port
            browser_path = self.raw_browser_path
        with CDPClient(raw_port, browser_path) as client:
            targets = client.call("Target.getTargets").get("targetInfos", [])
            pages = [target for target in targets if target.get("type") == "page"]
            url = str(request.get("url", "")).strip()
            if url:
                target_id = client.call("Target.createTarget", {"url": url})["targetId"]
            elif pages:
                target_id = pages[0]["targetId"]
            else:
                target_id = client.call("Target.createTarget", {"url": "about:blank"})["targetId"]
            client.call("Target.activateTarget", {"targetId": target_id})
            try:
                window = client.call("Browser.getWindowForTarget", {"targetId": target_id})
                client.call(
                    "Browser.setWindowBounds",
                    {"windowId": window["windowId"], "bounds": {"windowState": "normal"}},
                )
            except RuntimeError:
                pass
        return self.status()

    def ensure_headed(self, request: dict[str, Any]) -> dict[str, Any]:
        return self.open_or_focus(request)

    def open_login(self, request: dict[str, Any]) -> dict[str, Any]:
        display = validate_display(
            str(request.get("display", "")), str(request.get("xauthority", ""))
        )
        if display is None:
            raise RuntimeError("SparkClaw Browser has no usable desktop display")
        target_url = str(request.get("url", "")).strip()
        if not target_url:
            raise RuntimeError("manual login URL is required")
        with self.lock:
            self.desired_display, self.desired_xauthority = display
            previous_generation = self.generation
            self.launch_mode = "manual-login"
            self.launch_revision += 1
            self.manual_login_url = target_url
            current_process = self.process
        if current_process is not None and current_process.poll() is None:
            self._stop_browser()
        deadline = time.monotonic() + 25
        while time.monotonic() < deadline:
            with self.lock:
                ready = (
                    self.generation > previous_generation
                    and self.presentation == "manual-login"
                    and self.process is not None
                    and self.process.poll() is None
                )
            if ready and self.ready_event.is_set():
                return self.status()
            time.sleep(0.1)
        raise RuntimeError("SparkClaw manual login browser is unavailable")

    def ensure_hidden(self) -> dict[str, Any]:
        self._ensure_presentation("headless")
        return self.status()

    def _ensure_presentation(self, target: str) -> None:
        if target not in {"headed", "headless"}:
            raise ValueError("unsupported browser presentation")
        if target == "headed" and validate_display(
            self.desired_display, self.desired_xauthority
        ) is None:
            raise RuntimeError("SparkClaw Browser has no usable desktop display")
        with self.lock:
            current_presentation = self.presentation
            previous_generation = self.generation
            self.launch_mode = "automation"
            self.launch_revision += 1
            self.manual_login_url = ""
            self.force_headless = target == "headless"
        if current_presentation == "unavailable":
            return
        if current_presentation == target and self.ready_event.is_set():
            return
        if current_presentation != "unavailable":
            self._stop_browser()
        deadline = time.monotonic() + 25
        while time.monotonic() < deadline:
            with self.lock:
                ready = (
                    self.generation > previous_generation
                    and self.presentation == target
                    and self.process is not None
                    and self.process.poll() is None
                )
            if ready and self.ready_event.is_set():
                return
            time.sleep(0.1)
        raise RuntimeError(f"SparkClaw Browser could not enter {target} presentation")


def control_request(runtime_dir: Path, operation: str, url: str = "") -> dict[str, Any]:
    request: dict[str, Any] = {"operation": operation}
    if operation in {"open-or-focus", "ensure-headed", "open-login"}:
        request.update(
            {
                "url": url,
                "display": os.environ.get("DISPLAY", ""),
                "xauthority": os.environ.get("XAUTHORITY", ""),
            }
        )
    client = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    client.settimeout(30)
    try:
        client.connect(str(runtime_dir / "control.sock"))
        client.sendall((json.dumps(request, separators=(",", ":")) + "\n").encode())
        response = bytearray()
        while b"\n" not in response and len(response) <= MAX_CONTROL_BYTES:
            chunk = client.recv(4096)
            if not chunk:
                break
            response.extend(chunk)
    finally:
        client.close()
    if not response:
        raise RuntimeError("browserd control socket returned no response")
    return json.loads(response.split(b"\n", 1)[0])


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="sparkclaw-browserd")
    parser.add_argument("--config", type=Path, default=default_config_path())
    subparsers = parser.add_subparsers(dest="command", required=True)
    subparsers.add_parser("serve")
    subparsers.add_parser("status")
    open_parser = subparsers.add_parser("open-or-focus")
    open_parser.add_argument("url", nargs="?", default="")
    headed_parser = subparsers.add_parser("ensure-headed")
    headed_parser.add_argument("url", nargs="?", default="")
    login_parser = subparsers.add_parser("open-login")
    login_parser.add_argument("url")
    subparsers.add_parser("ensure-hidden")
    subparsers.add_parser("shutdown")
    return parser


def main() -> int:
    args = build_parser().parse_args()
    config = load_config(args.config)
    runtime_dir = Path(config["runtimeDir"])
    if args.command == "serve":
        daemon = BrowserDaemon(config)
        for handled in (signal.SIGINT, signal.SIGTERM):
            signal.signal(handled, lambda _signal, _frame: daemon.stop_event.set())
        daemon.serve()
        return 0
    response = control_request(runtime_dir, args.command, getattr(args, "url", ""))
    print(json.dumps(response, sort_keys=True))
    return 0 if response.get("ok") else 1


if __name__ == "__main__":
    raise SystemExit(main())
