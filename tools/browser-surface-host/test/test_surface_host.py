from __future__ import annotations

import ctypes
import json
import os
from pathlib import Path
import re
import select
import signal
import subprocess
import time
import unittest


ROOT = Path(__file__).resolve().parents[1]
HOST = ROOT / "bin" / "surface-host"
FIXTURE = ROOT / "bin" / "surface-fixture"
MARKER = "0123456789abcdef0123456789abcdef"


def read_json(process: subprocess.Popen[str], timeout: float = 5.0) -> dict[str, object]:
    ready, _, _ = select.select([process.stdout], [], [], timeout)
    if not ready:
        raise AssertionError(f"process did not become ready: {process.args}")
    line = process.stdout.readline()
    if not line:
        stderr = process.stderr.read() if process.stderr else ""
        raise AssertionError(f"process exited before readiness: {stderr}")
    return json.loads(line)


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


def root_window() -> str:
    result = subprocess.run(
        ["xwininfo", "-root"],
        check=True,
        capture_output=True,
        text=True,
    )
    match = re.search(r"Window id: (0x[0-9a-f]+)", result.stdout, re.IGNORECASE)
    if not match:
        raise AssertionError(result.stdout)
    return match.group(1).lower()


def window_position(window: str) -> tuple[int, int]:
    result = subprocess.run(
        ["xwininfo", "-id", window],
        check=True,
        capture_output=True,
        text=True,
    )
    x_match = re.search(r"Absolute upper-left X:\s+(-?\d+)", result.stdout)
    y_match = re.search(r"Absolute upper-left Y:\s+(-?\d+)", result.stdout)
    if not x_match or not y_match:
        raise AssertionError(result.stdout)
    return int(x_match.group(1)), int(y_match.group(1))


def window_geometry(window: str) -> tuple[int, int, int, int, str]:
    result = subprocess.run(
        ["xwininfo", "-id", window],
        check=True,
        capture_output=True,
        text=True,
    )
    patterns = {
        "x": r"Absolute upper-left X:\s+(-?\d+)",
        "y": r"Absolute upper-left Y:\s+(-?\d+)",
        "width": r"Width:\s+(\d+)",
        "height": r"Height:\s+(\d+)",
        "state": r"Map State:\s+(\S+)",
    }
    matches = {name: re.search(pattern, result.stdout) for name, pattern in patterns.items()}
    if any(match is None for match in matches.values()):
        raise AssertionError(result.stdout)
    return (
        int(matches["x"].group(1)),
        int(matches["y"].group(1)),
        int(matches["width"].group(1)),
        int(matches["height"].group(1)),
        matches["state"].group(1),
    )


def move_window(window: str, x: int, y: int) -> None:
    x11 = ctypes.CDLL("libX11.so.6")
    x11.XOpenDisplay.argtypes = [ctypes.c_char_p]
    x11.XOpenDisplay.restype = ctypes.c_void_p
    x11.XMoveWindow.argtypes = [ctypes.c_void_p, ctypes.c_ulong, ctypes.c_int, ctypes.c_int]
    x11.XSync.argtypes = [ctypes.c_void_p, ctypes.c_int]
    x11.XCloseDisplay.argtypes = [ctypes.c_void_p]
    display = x11.XOpenDisplay(None)
    if not display:
        raise AssertionError("X11 display unavailable")
    try:
        x11.XMoveWindow(display, int(window, 0), x, y)
        x11.XSync(display, 0)
    finally:
        x11.XCloseDisplay(display)


def set_window_mapped(window: str, mapped: bool) -> None:
    x11 = ctypes.CDLL("libX11.so.6")
    x11.XOpenDisplay.argtypes = [ctypes.c_char_p]
    x11.XOpenDisplay.restype = ctypes.c_void_p
    x11.XMapRaised.argtypes = [ctypes.c_void_p, ctypes.c_ulong]
    x11.XUnmapWindow.argtypes = [ctypes.c_void_p, ctypes.c_ulong]
    x11.XSync.argtypes = [ctypes.c_void_p, ctypes.c_int]
    x11.XCloseDisplay.argtypes = [ctypes.c_void_p]
    display = x11.XOpenDisplay(None)
    if not display:
        raise AssertionError("X11 display unavailable")
    try:
        if mapped:
            x11.XMapRaised(display, int(window, 0))
        else:
            x11.XUnmapWindow(display, int(window, 0))
        x11.XSync(display, 0)
    finally:
        x11.XCloseDisplay(display)


def wait_for_geometry(window: str, predicate, timeout: float = 2.0) -> tuple[int, int, int, int, str]:
    deadline = time.monotonic() + timeout
    last = window_geometry(window)
    while time.monotonic() < deadline:
        last = window_geometry(window)
        if predicate(last):
            return last
        time.sleep(0.02)
    raise AssertionError(f"window geometry did not converge: {window} {last}")


def wait_for_frame_alignment(anchor: str, frame: str, timeout: float = 2.0) -> tuple[int, int]:
    deadline = time.monotonic() + timeout
    last = (window_position(anchor), window_geometry(frame))
    while time.monotonic() < deadline:
        anchor_position = window_position(anchor)
        frame_geometry = window_geometry(frame)
        last = (anchor_position, frame_geometry)
        if frame_geometry[:4] == (anchor_position[0] + 20, anchor_position[1] + 30, 320, 240):
            return frame_geometry[0], frame_geometry[1]
        time.sleep(0.02)
    raise AssertionError(f"frame did not align with anchor: {anchor} {frame} {last}")


def x_window_exists(window: str) -> bool:
    return subprocess.run(
        ["xwininfo", "-id", window],
        check=False,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    ).returncode == 0


def wait_for_viewable(window: str, timeout: float = 2.0) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        result = subprocess.run(
            ["xwininfo", "-id", window],
            check=False,
            capture_output=True,
            text=True,
        )
        if result.returncode == 0 and "Map State: IsViewable" in result.stdout:
            return
        time.sleep(0.02)
    raise AssertionError(f"window did not become viewable: {window}")


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


@unittest.skipUnless(
    os.environ.get("SPARKCLAW_SURFACE_TEST_ISOLATED") == "1"
    and os.environ.get("DISPLAY")
    and Path("/usr/bin/xwininfo").exists(),
    "disposable X11 display required; run through xvfb-run",
)
class SurfaceHostTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        display = os.environ.get("DISPLAY", "")
        isolated_display = os.environ.get("SPARKCLAW_SURFACE_TEST_XSERVER_DISPLAY", "")
        original_display = os.environ.get("SPARKCLAW_SURFACE_TEST_ORIGINAL_DISPLAY", "")
        xserver_pid = os.environ.get("SPARKCLAW_SURFACE_TEST_XSERVER_PID", "")
        if (
            display != isolated_display
            or display == original_display
            or not xserver_pid.isdigit()
        ):
            raise RuntimeError("tests require the process-owned disposable Xvfb runner")
        command_line = Path(f"/proc/{xserver_pid}/cmdline").read_bytes().replace(b"\0", b" ").decode()
        if "Xvfb" not in command_line or "-nolisten tcp" not in command_line:
            raise RuntimeError("the registered disposable X server is not Xvfb")
        subprocess.run(["make", "-C", str(ROOT)], check=True, capture_output=True, text=True)

    def setUp(self) -> None:
        self.processes: list[subprocess.Popen[str]] = []
        self.root_window = root_window()
        self.anchor, self.anchor_info = self.fixture("anchor", x=40, y=40, override_redirect=True)
        self.child, self.child_info = self.fixture("task", x=760, y=80, override_redirect=True)

    def tearDown(self) -> None:
        for process in reversed(self.processes):
            if process.poll() is None:
                process.terminate()
        for process in reversed(self.processes):
            if process.poll() is None:
                try:
                    process.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=2)
        for process in self.processes:
            if process.stdout is not None:
                process.stdout.close()
            if process.stderr is not None:
                process.stderr.close()

    def fixture(self, role: str, *, x: int, y: int,
                override_redirect: bool = False) -> tuple[subprocess.Popen[str], dict[str, object]]:
        process = subprocess.Popen(
            [
                str(FIXTURE),
                "--role", role,
                "--marker", MARKER,
                "--x", str(x),
                "--y", str(y),
                "--override-redirect", "true" if override_redirect else "false",
            ],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            bufsize=1,
        )
        self.processes.append(process)
        return process, read_json(process)

    def host(self, *, role: str = "task", marker: str = MARKER) -> tuple[subprocess.Popen[str], dict[str, object]]:
        process = subprocess.Popen(
            [
                str(HOST),
                "--anchor", str(self.anchor_info["window"]),
                "--expected-anchor-pid", str(self.anchor_info["pid"]),
                "--expected-anchor-exe", str(FIXTURE.resolve()),
                "--anchor-marker", MARKER,
                "--child", str(self.child_info["window"]),
                "--expected-pid", str(self.child_info["pid"]),
                "--expected-exe", str(FIXTURE.resolve()),
                "--marker", marker,
                "--role", role,
                "--x", "20",
                "--y", "30",
                "--width", "320",
                "--height", "240",
            ],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            bufsize=1,
        )
        self.processes.append(process)
        return process, read_json(process)

    def test_rejects_wrong_marker_and_role(self) -> None:
        for role, marker in (("task", "f" * 32), ("personal", MARKER)):
            process = subprocess.run(
                [
                    str(HOST),
                    "--anchor", str(self.anchor_info["window"]),
                    "--expected-anchor-pid", str(self.anchor_info["pid"]),
                    "--expected-anchor-exe", str(FIXTURE.resolve()),
                    "--anchor-marker", MARKER,
                    "--child", str(self.child_info["window"]),
                    "--expected-pid", str(self.child_info["pid"]),
                    "--expected-exe", str(FIXTURE.resolve()),
                    "--marker", marker,
                    "--role", role,
                    "--x", "0", "--y", "0", "--width", "320", "--height", "240",
                ],
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertEqual(process.returncode, 4, process.stderr)
            self.assertIn("window identity rejected", process.stderr)

    def test_rejects_wrong_anchor_identity(self) -> None:
        process = subprocess.run(
            [
                str(HOST),
                "--anchor", str(self.anchor_info["window"]),
                "--expected-anchor-pid", str(self.anchor_info["pid"]),
                "--expected-anchor-exe", str(FIXTURE.resolve()),
                "--anchor-marker", "f" * 32,
                "--child", str(self.child_info["window"]),
                "--expected-pid", str(self.child_info["pid"]),
                "--expected-exe", str(FIXTURE.resolve()),
                "--marker", MARKER,
                "--role", "task",
                "--x", "0", "--y", "0", "--width", "320", "--height", "240",
            ],
            check=False,
            capture_output=True,
            text=True,
        )
        self.assertEqual(process.returncode, 4, process.stderr)
        self.assertIn("window identity rejected", process.stderr)

    def test_task_surface_blocks_pointer_input(self) -> None:
        host, info = self.host()
        self.assertNotEqual(window_parent(str(self.child_info["window"])), self.root_window)
        frame_x, frame_y = wait_for_frame_alignment(
            str(self.anchor_info["window"]), str(info["frame"])
        )
        fake_click(frame_x + 100, frame_y + 100)
        ready, _, _ = select.select([self.child.stdout], [], [], 0.5)
        self.assertFalse(ready, "task child received pointer input through the shield")
        host.terminate()
        self.assertEqual(host.wait(timeout=3), 0)
        self.assertTrue(x_window_exists(str(self.child_info["window"])))

    def test_task_surface_redirects_focus_and_blocks_keyboard_input(self) -> None:
        child_window = str(self.child_info["window"])
        wait_for_viewable(child_window)
        set_input_focus(child_window)
        self.assertEqual(input_focus(), child_window)
        host, _ = self.host()
        self.assertNotEqual(input_focus(), child_window)
        fake_key()
        ready, _, _ = select.select([self.child.stdout], [], [], 0.5)
        self.assertFalse(ready, "task child received keyboard input after focus redirection")
        host.terminate()
        self.assertEqual(host.wait(timeout=3), 0)

    def test_personal_surface_allows_pointer_input(self) -> None:
        self.child.terminate()
        self.child.wait(timeout=2)
        self.child, self.child_info = self.fixture("personal", x=760, y=80, override_redirect=True)
        host, info = self.host(role="personal")
        frame_x, frame_y = wait_for_frame_alignment(
            str(self.anchor_info["window"]), str(info["frame"])
        )
        fake_click(frame_x + 100, frame_y + 100)
        event = read_json(self.child, timeout=2)
        self.assertEqual(event, {"event": "button"})
        host.terminate()
        self.assertEqual(host.wait(timeout=3), 0)

    def test_frame_tracks_anchor_geometry_and_visibility(self) -> None:
        host, info = self.host()
        anchor_window = str(self.anchor_info["window"])
        child_window = str(self.child_info["window"])
        frame_window = str(info["frame"])

        wait_for_frame_alignment(anchor_window, frame_window)
        self.assertEqual(window_geometry(child_window)[2:4], (320, 240))

        move_window(anchor_window, 180, 150)
        wait_for_frame_alignment(anchor_window, frame_window)

        anchor_parent = window_parent(anchor_window)
        visibility_window = anchor_window if anchor_parent == self.root_window else anchor_parent
        set_window_mapped(visibility_window, False)
        wait_for_geometry(frame_window, lambda geometry: geometry[4] == "IsUnMapped")
        set_window_mapped(visibility_window, True)
        wait_for_geometry(anchor_window, lambda geometry: geometry[4] == "IsViewable")
        wait_for_geometry(frame_window, lambda geometry: geometry[4] == "IsViewable")

        host.terminate()
        self.assertEqual(host.wait(timeout=3), 0)

    def test_sigkill_reparents_child_from_save_set(self) -> None:
        host, info = self.host()
        frame_window = str(info["frame"])
        host.send_signal(signal.SIGKILL)
        self.assertEqual(host.wait(timeout=3), -signal.SIGKILL)
        for _ in range(30):
            if x_window_exists(str(self.child_info["window"])) and not x_window_exists(frame_window):
                break
            time.sleep(0.05)
        self.assertTrue(x_window_exists(str(self.child_info["window"])))
        self.assertFalse(x_window_exists(frame_window))
        self.assertNotEqual(window_parent(str(self.child_info["window"])), frame_window.lower())

    def test_anchor_exit_keeps_child_alive(self) -> None:
        host, info = self.host()
        frame_window = str(info["frame"])
        self.anchor.terminate()
        self.anchor.wait(timeout=2)
        self.assertEqual(host.wait(timeout=3), 0)
        self.assertTrue(x_window_exists(str(self.child_info["window"])))
        self.assertFalse(x_window_exists(frame_window))
        self.assertNotEqual(window_parent(str(self.child_info["window"])), frame_window.lower())

    def test_child_exit_stops_host_cleanly(self) -> None:
        host, _ = self.host()
        self.child.terminate()
        self.child.wait(timeout=2)
        self.assertEqual(host.wait(timeout=3), 0)


if __name__ == "__main__":
    unittest.main()
