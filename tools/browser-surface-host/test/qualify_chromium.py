#!/usr/bin/env python3
from __future__ import annotations

import argparse
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


ROOT = Path(__file__).resolve().parents[1]
HOST = ROOT / "bin" / "surface-host"
FIXTURE = ROOT / "bin" / "surface-fixture"


def read_json(process: subprocess.Popen[str], timeout: float = 5.0) -> dict[str, object]:
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
    return subprocess.run(
        ["xprop", "-id", window, *properties],
        check=False,
        capture_output=True,
        text=True,
    ).stdout


def client_windows() -> list[str]:
    result = subprocess.run(
        ["xprop", "-root", "_NET_CLIENT_LIST"],
        check=True,
        capture_output=True,
        text=True,
    )
    return re.findall(r"0x[0-9a-f]+", result.stdout, re.IGNORECASE)


def wait_for_chromium_window(pid: int, timeout: float = 10.0) -> str:
    deadline = time.monotonic() + timeout
    matching: list[tuple[str, str]] = []
    while time.monotonic() < deadline:
        matching = []
        for window in client_windows():
            properties = xprop(window, "_NET_WM_PID", "_NET_WM_NAME", "WM_NAME", "WM_CLASS")
            if re.search(rf"_NET_WM_PID\(CARDINAL\) = {pid}\b", properties):
                matching.append((window, properties))
        if len(matching) == 1:
            return matching[0][0]
        if len(matching) > 1:
            ready = [window for window, properties in matching if "surface-ready" in properties]
            if len(ready) == 1:
                return ready[0]
        time.sleep(0.05)
    details = "\n".join(f"{window}: {properties.strip()}" for window, properties in matching)
    raise AssertionError(f"unique Chromium window for PID {pid} did not appear\n{details}")


def set_string(window: str, name: str, value: str) -> None:
    subprocess.run(
        ["xprop", "-id", window, "-f", name, "8s", "-set", name, value],
        check=True,
        capture_output=True,
        text=True,
    )


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


def wait_for_frame_alignment(anchor: str, frame: str, timeout: float = 3.0) -> tuple[int, int]:
    deadline = time.monotonic() + timeout
    last = (window_position(anchor), window_position(frame))
    while time.monotonic() < deadline:
        anchor_position = window_position(anchor)
        frame_position = window_position(frame)
        last = (anchor_position, frame_position)
        if frame_position == (anchor_position[0] + 20, anchor_position[1] + 30):
            return frame_position
        time.sleep(0.02)
    raise AssertionError(f"surface frame did not align with anchor: {last}")


def click(x: int, y: int) -> None:
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


def wait_for_title(window: str, expected: str, timeout: float = 3.0) -> bool:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if expected in xprop(window, "_NET_WM_NAME", "WM_NAME"):
            return True
        time.sleep(0.05)
    return False


def stop_process(process: subprocess.Popen[str] | None) -> None:
    if process is None:
        return
    if process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=3)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=3)
    if process.stdout is not None:
        process.stdout.close()
    if process.stderr is not None:
        process.stderr.close()


def start_host(anchor: dict[str, object], window: str, chromium: Path, pid: int,
               marker: str, role: str) -> tuple[subprocess.Popen[str], dict[str, object]]:
    set_string(window, "_SPARKCLAW_SURFACE_MARKER", marker)
    set_string(window, "_SPARKCLAW_SURFACE_ROLE", role)
    process = subprocess.Popen(
        [
            str(HOST),
            "--anchor", str(anchor["window"]),
            "--expected-anchor-pid", str(anchor["pid"]),
            "--expected-anchor-exe", str(FIXTURE.resolve()),
            "--anchor-marker", marker,
            "--child", window,
            "--expected-pid", str(pid),
            "--expected-exe", str(chromium),
            "--marker", marker,
            "--role", role,
            "--x", "20", "--y", "30", "--width", "480", "--height", "360",
        ],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        bufsize=1,
    )
    return process, read_json(process)


def main() -> int:
    parser = argparse.ArgumentParser(description="Qualify surface-host with an isolated Chromium profile")
    parser.add_argument("--chromium", type=Path, required=True)
    args = parser.parse_args()
    chromium = args.chromium.resolve(strict=True)
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
        raise SystemExit(
            "refusing to manipulate an unverified display; use a process-owned disposable Xvfb"
        )
    command_line = Path(f"/proc/{xserver_pid}/cmdline").read_bytes().replace(b"\0", b" ").decode()
    if "Xvfb" not in command_line or "-nolisten tcp" not in command_line:
        raise SystemExit("the registered disposable X server is not Xvfb")
    subprocess.run(["make", "-C", str(ROOT)], check=True)

    anchor_process: subprocess.Popen[str] | None = None
    chrome_process: subprocess.Popen[str] | None = None
    host_process: subprocess.Popen[str] | None = None
    marker = secrets.token_hex(32)
    with tempfile.TemporaryDirectory(prefix="sparkclaw-surface-profile.") as profile:
        page = Path(profile) / "qualification.html"
        page.write_text(
            """<!doctype html><title>surface-ready</title>
            <style>html,body,button{box-sizing:border-box;width:100%;height:100%;margin:0}</style>
            <button onclick=\"document.title='surface-clicked'\">qualification surface</button>""",
            encoding="utf-8",
        )
        try:
            anchor_process = subprocess.Popen(
                [str(FIXTURE), "--role", "anchor", "--marker", marker, "--x", "40", "--y", "40"],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                bufsize=1,
            )
            anchor = read_json(anchor_process)
            chrome_process = subprocess.Popen(
                [
                    str(chromium),
                    f"--user-data-dir={profile}",
                    "--ozone-platform=x11",
                    "--no-first-run",
                    "--no-default-browser-check",
                    "--disable-background-networking",
                    "--disable-component-update",
                    "--disable-default-apps",
                    "--disable-sync",
                    "--metrics-recording-only",
                    "--new-window",
                    page.as_uri(),
                ],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                start_new_session=True,
            )
            window = wait_for_chromium_window(chrome_process.pid)
            if not wait_for_title(window, "surface-ready", timeout=5):
                raise AssertionError(
                    "qualification page did not load in the isolated Chromium window: "
                    + xprop(window, "_NET_WM_NAME", "WM_NAME")
                )

            host_process, attached = start_host(
                anchor, window, chromium, chrome_process.pid, marker, "task"
            )
            if attached.get("shield") == "0x0":
                raise AssertionError("task surface has no input shield")
            if window_parent(window) != str(attached["frame"]).lower():
                raise AssertionError("Chromium window was not attached to the surface frame")
            frame_x, frame_y = wait_for_frame_alignment(
                str(anchor["window"]), str(attached["frame"])
            )
            click(frame_x + 240, frame_y + 220)
            if wait_for_title(window, "surface-clicked", timeout=0.5):
                raise AssertionError("task surface accepted pointer input")

            host_process.send_signal(signal.SIGKILL)
            host_process.wait(timeout=3)
            if chrome_process.poll() is not None:
                raise AssertionError("Chromium exited when the task surface host was killed")
            stop_process(host_process)
            host_process = None

            host_process, attached = start_host(
                anchor, window, chromium, chrome_process.pid, marker, "personal"
            )
            if attached.get("shield") != "0x0":
                raise AssertionError("personal surface unexpectedly has an input shield")
            frame_x, frame_y = wait_for_frame_alignment(
                str(anchor["window"]), str(attached["frame"])
            )
            click(frame_x + 240, frame_y + 220)
            if not wait_for_title(window, "surface-clicked"):
                raise AssertionError("personal surface did not receive pointer input")

            host_process.terminate()
            if host_process.wait(timeout=3) != 0:
                raise AssertionError("surface host did not detach cleanly")
            print(json.dumps({
                "result": "passed",
                "chromiumPid": chrome_process.pid,
                "window": window,
                "checks": [
                    "exact-identity",
                    "task-input-shield",
                    "helper-sigkill-survival",
                    "personal-input",
                    "normal-detach",
                ],
            }))
            return 0
        finally:
            stop_process(host_process)
            stop_process(anchor_process)
            if chrome_process is not None and chrome_process.poll() is None:
                os.killpg(chrome_process.pid, signal.SIGTERM)
                try:
                    chrome_process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    os.killpg(chrome_process.pid, signal.SIGKILL)
                    chrome_process.wait(timeout=5)


if __name__ == "__main__":
    raise SystemExit(main())
