from __future__ import annotations

import ctypes
import json
import os
from pathlib import Path
import re
import secrets
import select
import signal
import subprocess
import tempfile
import time
import unittest


ROOT = Path(__file__).resolve().parents[1]
HOST = ROOT / "bin" / "surface-host"
FIXTURE = ROOT / "bin" / "surface-fixture"
HARNESS = Path(__file__).resolve().parent / "electron-anchor" / "anchor.cjs"
ELECTRON = Path(os.environ.get("SPARKCLAW_ELECTRON_ANCHOR_ELECTRON", ""))
RUN_ROOT = Path(os.environ.get("SPARKCLAW_ELECTRON_ANCHOR_RUN_ROOT", ""))


def read_json(process: subprocess.Popen[str], timeout: float = 8.0) -> dict[str, object]:
    if process.stdout is None:
        raise AssertionError("process stdout is unavailable")
    ready, _, _ = select.select([process.stdout], [], [], timeout)
    if not ready:
        raise AssertionError(f"process did not become ready: {process.args}")
    line = process.stdout.readline()
    if not line:
        stderr = process.stderr.read() if process.stderr else ""
        raise AssertionError(f"process exited before readiness: {stderr}")
    return json.loads(line)


def xprop(window: str, *properties: str) -> str:
    result = subprocess.run(
        ["xprop", "-id", window, *properties],
        check=True,
        capture_output=True,
        text=True,
    )
    return result.stdout


def set_xprop_string(window: str, name: str, value: str) -> None:
    subprocess.run(
        ["xprop", "-id", window, "-f", name, "8s", "-set", name, value],
        check=True,
        capture_output=True,
        text=True,
    )


def x_window_exists(window: str) -> bool:
    return subprocess.run(
        ["xwininfo", "-id", window],
        check=False,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    ).returncode == 0


def window_info(window: str) -> dict[str, int | str]:
    result = subprocess.run(
        ["xwininfo", "-id", window],
        check=True,
        capture_output=True,
        text=True,
    )
    patterns = {
        "absolute_x": r"Absolute upper-left X:\s+(-?\d+)",
        "absolute_y": r"Absolute upper-left Y:\s+(-?\d+)",
        "relative_x": r"Relative upper-left X:\s+(-?\d+)",
        "relative_y": r"Relative upper-left Y:\s+(-?\d+)",
        "width": r"Width:\s+(\d+)",
        "height": r"Height:\s+(\d+)",
        "state": r"Map State:\s+(\S+)",
    }
    matches = {name: re.search(pattern, result.stdout) for name, pattern in patterns.items()}
    if any(match is None for match in matches.values()):
        raise AssertionError(result.stdout)
    return {
        name: match.group(1) if name == "state" else int(match.group(1))
        for name, match in matches.items()
    }


def window_parent(window: str) -> str:
    result = subprocess.run(
        ["xwininfo", "-id", window, "-tree"],
        check=True,
        capture_output=True,
        text=True,
    )
    match = re.search(r"Parent window id: (0x[0-9a-f]+)", result.stdout, re.IGNORECASE)
    if not match:
        raise AssertionError(result.stdout)
    return match.group(1).lower()


def is_descendant(window: str, ancestor: str) -> bool:
    current = window.lower()
    target = ancestor.lower()
    for _ in range(128):
        if current == target:
            return True
        try:
            parent = window_parent(current)
        except subprocess.CalledProcessError:
            return False
        if parent == current or parent == "0x0":
            return False
        current = parent
    return False


def set_input_focus(window: str) -> None:
    x11 = ctypes.CDLL("libX11.so.6")
    x11.XOpenDisplay.argtypes = [ctypes.c_char_p]
    x11.XOpenDisplay.restype = ctypes.c_void_p
    x11.XSetInputFocus.argtypes = [ctypes.c_void_p, ctypes.c_ulong, ctypes.c_int, ctypes.c_ulong]
    x11.XSync.argtypes = [ctypes.c_void_p, ctypes.c_int]
    x11.XCloseDisplay.argtypes = [ctypes.c_void_p]
    display = x11.XOpenDisplay(None)
    if not display:
        raise AssertionError("X11 display unavailable")
    try:
        x11.XSetInputFocus(display, int(window, 0), 2, 0)
        x11.XSync(display, 0)
    finally:
        x11.XCloseDisplay(display)


def input_focus() -> str:
    x11 = ctypes.CDLL("libX11.so.6")
    x11.XOpenDisplay.argtypes = [ctypes.c_char_p]
    x11.XOpenDisplay.restype = ctypes.c_void_p
    x11.XGetInputFocus.argtypes = [ctypes.c_void_p, ctypes.POINTER(ctypes.c_ulong), ctypes.POINTER(ctypes.c_int)]
    x11.XCloseDisplay.argtypes = [ctypes.c_void_p]
    display = x11.XOpenDisplay(None)
    if not display:
        raise AssertionError("X11 display unavailable")
    try:
        focused = ctypes.c_ulong()
        revert_to = ctypes.c_int()
        x11.XGetInputFocus(display, ctypes.byref(focused), ctypes.byref(revert_to))
        return f"0x{focused.value:x}"
    finally:
        x11.XCloseDisplay(display)


def fake_click(x: int, y: int) -> None:
    x11 = ctypes.CDLL("libX11.so.6")
    xtst = ctypes.CDLL("libXtst.so.6")
    x11.XOpenDisplay.argtypes = [ctypes.c_char_p]
    x11.XOpenDisplay.restype = ctypes.c_void_p
    x11.XFlush.argtypes = [ctypes.c_void_p]
    x11.XCloseDisplay.argtypes = [ctypes.c_void_p]
    xtst.XTestFakeMotionEvent.argtypes = [ctypes.c_void_p, ctypes.c_int, ctypes.c_int, ctypes.c_int, ctypes.c_ulong]
    xtst.XTestFakeMotionEvent.restype = ctypes.c_int
    xtst.XTestFakeButtonEvent.argtypes = [ctypes.c_void_p, ctypes.c_uint, ctypes.c_int, ctypes.c_ulong]
    xtst.XTestFakeButtonEvent.restype = ctypes.c_int
    display = x11.XOpenDisplay(None)
    if not display:
        raise AssertionError("X11 display unavailable")
    try:
        if not xtst.XTestFakeMotionEvent(display, -1, x, y, 0):
            raise AssertionError("pointer motion failed")
        if not xtst.XTestFakeButtonEvent(display, 1, 1, 0):
            raise AssertionError("button press failed")
        if not xtst.XTestFakeButtonEvent(display, 1, 0, 0):
            raise AssertionError("button release failed")
        x11.XFlush(display)
    finally:
        x11.XCloseDisplay(display)


def fake_key(keycode: int = 38) -> None:
    x11 = ctypes.CDLL("libX11.so.6")
    xtst = ctypes.CDLL("libXtst.so.6")
    x11.XOpenDisplay.argtypes = [ctypes.c_char_p]
    x11.XOpenDisplay.restype = ctypes.c_void_p
    x11.XFlush.argtypes = [ctypes.c_void_p]
    x11.XCloseDisplay.argtypes = [ctypes.c_void_p]
    xtst.XTestFakeKeyEvent.argtypes = [ctypes.c_void_p, ctypes.c_uint, ctypes.c_int, ctypes.c_ulong]
    xtst.XTestFakeKeyEvent.restype = ctypes.c_int
    display = x11.XOpenDisplay(None)
    if not display:
        raise AssertionError("X11 display unavailable")
    try:
        if not xtst.XTestFakeKeyEvent(display, keycode, 1, 0):
            raise AssertionError("key press failed")
        if not xtst.XTestFakeKeyEvent(display, keycode, 0, 0):
            raise AssertionError("key release failed")
        x11.XFlush(display)
    finally:
        x11.XCloseDisplay(display)


def wait_until(predicate, description: str, timeout: float = 4.0) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return
        time.sleep(0.02)
    raise AssertionError(f"timed out waiting for {description}")


def stop_group(process: subprocess.Popen[str] | None) -> None:
    if process is None:
        return
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    if process.poll() is None:
        try:
            process.wait(timeout=3)
        except subprocess.TimeoutExpired:
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.wait(timeout=3)
    if process.stdin is not None:
        process.stdin.close()
    if process.stdout is not None:
        process.stdout.close()
    if process.stderr is not None:
        process.stderr.close()


class ElectronAnchorQualificationTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        display = os.environ.get("DISPLAY", "")
        isolated_display = os.environ.get("SPARKCLAW_SURFACE_TEST_XSERVER_DISPLAY", "")
        original_display = os.environ.get("SPARKCLAW_SURFACE_TEST_ORIGINAL_DISPLAY", "")
        xserver_pid = os.environ.get("SPARKCLAW_SURFACE_TEST_XSERVER_PID", "")
        if (
            os.environ.get("SPARKCLAW_SURFACE_TEST_ISOLATED") != "1"
            or not display
            or display != isolated_display
            or display == original_display
            or not xserver_pid.isdigit()
        ):
            raise RuntimeError("qualification requires the process-owned disposable Xvfb runner")
        command_line = Path(f"/proc/{xserver_pid}/cmdline").read_bytes().replace(b"\0", b" ").decode()
        if "Xvfb" not in command_line or "-nolisten tcp" not in command_line:
            raise RuntimeError("the registered disposable X server is not Xvfb")
        if not ELECTRON.is_file() or not RUN_ROOT.is_dir():
            raise RuntimeError("Electron fixture or disposable run root is unavailable")
        subprocess.run(["make", "-C", str(ROOT), "clean", "all"], check=True)

    def setUp(self) -> None:
        self.processes: list[subprocess.Popen[str]] = []
        self.temporary = tempfile.TemporaryDirectory(
            prefix="case.", dir=RUN_ROOT
        )
        self.case_root = Path(self.temporary.name).resolve()
        self.anchor_marker = secrets.token_hex(32)
        self.child_marker = secrets.token_hex(32)
        self.anchor, self.anchor_info = self.start_anchor()
        self.child, self.child_info = self.start_fixture("task")

    def tearDown(self) -> None:
        for process in reversed(self.processes):
            stop_group(process)
        self.temporary.cleanup()

    def start_anchor(self) -> tuple[subprocess.Popen[str], dict[str, object]]:
        user_data = self.case_root / "electron-user-data"
        environment = os.environ.copy()
        environment.update(
            {
                "SPARKCLAW_ELECTRON_ANCHOR_MARKER": self.anchor_marker,
                "SPARKCLAW_ELECTRON_ANCHOR_TEMP_ROOT": str(self.case_root),
                "SPARKCLAW_ELECTRON_ANCHOR_USER_DATA_DIR": str(user_data),
            }
        )
        process = subprocess.Popen(
            [str(ELECTRON), str(HARNESS)],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            bufsize=1,
            env=environment,
            start_new_session=True,
        )
        self.processes.append(process)
        information = read_json(process, timeout=15)
        self.assertEqual(information.get("event"), "ready")
        self.assertEqual(information.get("electron"), "44.4.3")
        self.assertRegex(str(information.get("chromium", "")), r"^\d+\.\d+\.\d+\.\d+$")
        return process, information

    def start_fixture(self, role: str) -> tuple[subprocess.Popen[str], dict[str, object]]:
        process = subprocess.Popen(
            [
                str(FIXTURE),
                "--role", role,
                "--marker", self.child_marker,
                "--x", "860",
                "--y", "80",
                "--override-redirect", "true",
            ],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            bufsize=1,
            start_new_session=True,
        )
        self.processes.append(process)
        return process, read_json(process)

    def host_arguments(
        self,
        *,
        role: str = "task",
        anchor_marker: str | None = None,
        anchor_pid: int | None = None,
        anchor_exe: str | None = None,
        x: int = 40,
        y: int = 50,
        width: int = 360,
        height: int = 260,
    ) -> list[str]:
        return [
            str(HOST),
            "--anchor", str(self.anchor_info["window"]),
            "--expected-anchor-pid", str(anchor_pid or self.anchor_info["pid"]),
            "--expected-anchor-exe", anchor_exe or str(self.anchor_info["exe"]),
            "--anchor-marker", anchor_marker or self.anchor_marker,
            "--child", str(self.child_info["window"]),
            "--expected-pid", str(self.child_info["pid"]),
            "--expected-exe", str(FIXTURE.resolve()),
            "--marker", self.child_marker,
            "--role", role,
            "--x", str(x),
            "--y", str(y),
            "--width", str(width),
            "--height", str(height),
        ]

    def start_host(self, **options) -> tuple[subprocess.Popen[str], dict[str, object]]:
        process = subprocess.Popen(
            self.host_arguments(**options),
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            bufsize=1,
            start_new_session=True,
        )
        self.processes.append(process)
        return process, read_json(process)

    def test_electron_anchor_identity_is_exact_and_disposable(self) -> None:
        anchor_window = str(self.anchor_info["window"])
        properties = xprop(
            anchor_window,
            "_NET_WM_PID",
            "_SPARKCLAW_SURFACE_MARKER",
            "_SPARKCLAW_SURFACE_ROLE",
        )
        self.assertIn(f"= {self.anchor_info['pid']}", properties)
        self.assertIn(self.anchor_marker, properties)
        self.assertIn('= "anchor"', properties)
        self.assertEqual(
            Path(f"/proc/{self.anchor_info['pid']}/exe").resolve(),
            Path(str(self.anchor_info["exe"])).resolve(),
        )
        user_data = Path(str(self.anchor_info["user_data_dir"])).resolve()
        self.assertTrue(user_data.is_relative_to(self.case_root))
        command_line = Path(f"/proc/{self.anchor_info['pid']}/cmdline").read_bytes().decode("utf-8", "replace")
        self.assertNotIn("load-extension", command_line)
        self.assertNotIn("browser-bridge", command_line.lower())

        invalid_cases = (
            {"anchor_marker": "f" * 64},
            {"anchor_pid": int(self.anchor_info["pid"]) + 100000},
            {"anchor_exe": str(FIXTURE.resolve())},
        )
        for invalid in invalid_cases:
            result = subprocess.run(
                self.host_arguments(**invalid),
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertEqual(result.returncode, 4, result.stderr)
            self.assertIn("window identity rejected", result.stderr)

        set_xprop_string(anchor_window, "_SPARKCLAW_SURFACE_ROLE", "task")
        invalid_role = subprocess.run(
            self.host_arguments(),
            check=False,
            capture_output=True,
            text=True,
        )
        self.assertEqual(invalid_role.returncode, 4, invalid_role.stderr)
        self.assertIn("window identity rejected", invalid_role.stderr)
        set_xprop_string(anchor_window, "_SPARKCLAW_SURFACE_ROLE", "anchor")

        host, _ = self.start_host()
        host.terminate()
        self.assertEqual(host.wait(timeout=3), 0)

    def test_attach_preserves_electron_focus_and_redirects_task_focus(self) -> None:
        anchor_window = str(self.anchor_info["window"])
        child_window = str(self.child_info["window"])
        set_input_focus(anchor_window)
        wait_until(lambda: is_descendant(input_focus(), anchor_window), "Electron anchor focus")

        host, _ = self.start_host()
        self.assertTrue(is_descendant(input_focus(), anchor_window))
        self.assertFalse(is_descendant(input_focus(), child_window))

        set_input_focus(child_window)
        wait_until(
            lambda: is_descendant(input_focus(), anchor_window)
            and not is_descendant(input_focus(), child_window),
            "task focus redirection",
        )
        fake_key()
        ready, _, _ = select.select([self.child.stdout], [], [], 0.4)
        self.assertFalse(ready, "task child received keyboard input")
        host.terminate()
        self.assertEqual(host.wait(timeout=3), 0)

    def test_surface_is_clipped_to_electron_anchor(self) -> None:
        stop_group(self.child)
        self.child, self.child_info = self.start_fixture("personal")
        anchor_window = str(self.anchor_info["window"])
        anchor = window_info(anchor_window)
        anchor_width = int(anchor["width"])
        anchor_height = int(anchor["height"])
        host, attached = self.start_host(
            role="personal",
            x=-40,
            y=anchor_height - 120,
            width=anchor_width + 120,
            height=220,
        )
        frame_window = str(attached["frame"])
        child_window = str(self.child_info["window"])

        def clipping_ready() -> bool:
            frame = window_info(frame_window)
            child = window_info(child_window)
            return (
                int(frame["absolute_x"]) == int(anchor["absolute_x"])
                and int(frame["absolute_y"]) == int(anchor["absolute_y"]) + anchor_height - 120
                and int(frame["width"]) == anchor_width
                and int(frame["height"]) == 120
                and int(child["relative_x"]) == -40
                and int(child["relative_y"]) == 0
                and int(child["width"]) == anchor_width + 120
                and int(child["height"]) == 220
            )

        wait_until(clipping_ready, "surface clipping geometry")
        frame = window_info(frame_window)
        fake_click(int(frame["absolute_x"]) + 100, int(frame["absolute_y"]) + 50)
        self.assertEqual(read_json(self.child, timeout=2), {"event": "button"})

        fake_click(int(anchor["absolute_x"]) + 100, int(frame["absolute_y"]) - 20)
        ready, _, _ = select.select([self.child.stdout], [], [], 0.3)
        self.assertFalse(ready, "child received input above the clipped frame")
        fake_click(int(anchor["absolute_x"]) + anchor_width + 20, int(frame["absolute_y"]) + 50)
        ready, _, _ = select.select([self.child.stdout], [], [], 0.3)
        self.assertFalse(ready, "child received input beyond the clipped anchor edge")
        host.terminate()
        self.assertEqual(host.wait(timeout=3), 0)

    def test_host_detach_and_crash_recover_child(self) -> None:
        child_window = str(self.child_info["window"])
        original_parent = window_parent(child_window)
        original = window_info(child_window)

        host, attached = self.start_host()
        frame_window = str(attached["frame"])
        host.terminate()
        self.assertEqual(host.wait(timeout=3), 0)
        wait_until(lambda: not x_window_exists(frame_window), "normal detach frame removal")
        self.assertTrue(x_window_exists(child_window))
        self.assertEqual(window_parent(child_window), original_parent)
        restored = window_info(child_window)
        self.assertEqual((restored["width"], restored["height"]), (original["width"], original["height"]))

        host, attached = self.start_host()
        frame_window = str(attached["frame"])
        host.send_signal(signal.SIGKILL)
        self.assertEqual(host.wait(timeout=3), -signal.SIGKILL)
        wait_until(lambda: not x_window_exists(frame_window), "crashed host frame removal")
        self.assertTrue(x_window_exists(child_window))
        self.assertFalse(is_descendant(child_window, frame_window))
        self.assertEqual(window_parent(child_window), original_parent)
        self.assertEqual(window_info(child_window)["state"], "IsViewable")
        self.assertTrue(x_window_exists(str(self.anchor_info["window"])))

    def test_electron_anchor_crash_detaches_without_killing_child(self) -> None:
        child_window = str(self.child_info["window"])
        original_parent = window_parent(child_window)
        original = window_info(child_window)
        host, attached = self.start_host()
        frame_window = str(attached["frame"])
        self.anchor.kill()
        self.assertEqual(self.anchor.wait(timeout=5), -signal.SIGKILL)
        self.assertEqual(host.wait(timeout=5), 0)
        self.assertFalse(x_window_exists(frame_window))
        self.assertTrue(x_window_exists(child_window))
        self.assertFalse(is_descendant(child_window, frame_window))
        self.assertEqual(window_parent(child_window), original_parent)
        restored = window_info(child_window)
        self.assertEqual((restored["width"], restored["height"]), (original["width"], original["height"]))


if __name__ == "__main__":
    unittest.main()
