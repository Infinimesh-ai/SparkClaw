#!/usr/bin/env python3

import ctypes
import os
import sys


def main() -> int:
    if len(sys.argv) < 4:
        return 2
    display_name = os.environ.get("DISPLAY", "").encode()
    if not display_name:
        return 2
    x11 = ctypes.CDLL("libX11.so.6")
    xtst = ctypes.CDLL("libXtst.so.6")
    x11.XOpenDisplay.argtypes = [ctypes.c_char_p]
    x11.XOpenDisplay.restype = ctypes.c_void_p
    x11.XCloseDisplay.argtypes = [ctypes.c_void_p]
    x11.XFlush.argtypes = [ctypes.c_void_p]
    xtst.XTestFakeMotionEvent.argtypes = [ctypes.c_void_p, ctypes.c_int, ctypes.c_int, ctypes.c_int, ctypes.c_ulong]
    xtst.XTestFakeButtonEvent.argtypes = [ctypes.c_void_p, ctypes.c_uint, ctypes.c_int, ctypes.c_ulong]
    xtst.XTestFakeKeyEvent.argtypes = [ctypes.c_void_p, ctypes.c_uint, ctypes.c_int, ctypes.c_ulong]
    display = x11.XOpenDisplay(display_name)
    if not display:
        return 1
    try:
        x = int(sys.argv[2])
        y = int(sys.argv[3])
        if not xtst.XTestFakeMotionEvent(display, -1, x, y, 0):
            return 1
        button = 3 if sys.argv[1] == "right-click" else 1
        if not xtst.XTestFakeButtonEvent(display, button, 1, 0):
            return 1
        if sys.argv[1] == "drag":
            if len(sys.argv) != 6:
                return 2
            if not xtst.XTestFakeMotionEvent(display, -1, int(sys.argv[4]), int(sys.argv[5]), 50):
                return 1
        if not xtst.XTestFakeButtonEvent(display, button, 0, 0):
            return 1
        if sys.argv[1] == "click-key":
            if len(sys.argv) != 5:
                return 2
            keycode = int(sys.argv[4])
            if not xtst.XTestFakeKeyEvent(display, keycode, 1, 0):
                return 1
            if not xtst.XTestFakeKeyEvent(display, keycode, 0, 0):
                return 1
        elif sys.argv[1] == "chord":
            if len(sys.argv) < 6:
                return 2
            keys = [int(value) for value in sys.argv[4:]]
            for keycode in keys:
                if not xtst.XTestFakeKeyEvent(display, keycode, 1, 0):
                    return 1
            for keycode in reversed(keys):
                if not xtst.XTestFakeKeyEvent(display, keycode, 0, 0):
                    return 1
        elif sys.argv[1] == "keys":
            if len(sys.argv) < 5:
                return 2
            for value in sys.argv[4:]:
                keycode = int(value)
                if not xtst.XTestFakeKeyEvent(display, keycode, 1, 0):
                    return 1
                if not xtst.XTestFakeKeyEvent(display, keycode, 0, 0):
                    return 1
        elif sys.argv[1] not in ("click", "right-click", "drag"):
            return 2
        x11.XFlush(display)
        return 0
    finally:
        x11.XCloseDisplay(display)


if __name__ == "__main__":
    raise SystemExit(main())
