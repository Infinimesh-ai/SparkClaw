#!/usr/bin/env python3

from __future__ import annotations

import ctypes
from ctypes import POINTER, Structure, Union, byref, c_char, c_char_p, c_int, c_long, c_ubyte, c_ulong, c_void_p
import os
import sys


CLIENT_MESSAGE = 33
IS_VIEWABLE = 2
SUBSTRUCTURE_NOTIFY_MASK = 1 << 19
SUBSTRUCTURE_REDIRECT_MASK = 1 << 20


class XWindowAttributes(Structure):
    _fields_ = [
        ("x", c_int), ("y", c_int), ("width", c_int), ("height", c_int),
        ("border_width", c_int), ("depth", c_int), ("visual", c_void_p),
        ("root", c_ulong), ("class", c_int), ("bit_gravity", c_int),
        ("win_gravity", c_int), ("backing_store", c_int),
        ("backing_planes", c_ulong), ("backing_pixel", c_ulong),
        ("save_under", c_int), ("colormap", c_ulong), ("map_installed", c_int),
        ("map_state", c_int), ("all_event_masks", c_long),
        ("your_event_mask", c_long), ("do_not_propagate_mask", c_long),
        ("override_redirect", c_int), ("screen", c_void_p),
    ]


class XClientMessageData(Union):
    _fields_ = [("b", c_char * 20), ("s", ctypes.c_short * 10), ("l", c_long * 5)]


class XClientMessageEvent(Structure):
    _fields_ = [
        ("type", c_int), ("serial", c_ulong), ("send_event", c_int),
        ("display", c_void_p), ("window", c_ulong), ("message_type", c_ulong),
        ("format", c_int), ("data", XClientMessageData),
    ]


class XEvent(Union):
    _fields_ = [("type", c_int), ("xclient", XClientMessageEvent), ("pad", c_long * 24)]


def main() -> int:
    if len(sys.argv) != 2 or not sys.argv[1].isdigit() or not os.environ.get("DISPLAY"):
        return 1
    browser_pid = int(sys.argv[1])
    if browser_pid < 2:
        return 1

    x11 = ctypes.cdll.LoadLibrary("libX11.so.6")
    configure_x11(x11)
    display = x11.XOpenDisplay(None)
    if not display:
        return 1
    try:
        root = x11.XDefaultRootWindow(display)
        clients_atom = x11.XInternAtom(display, b"_NET_CLIENT_LIST_STACKING", 0)
        pid_atom = x11.XInternAtom(display, b"_NET_WM_PID", 0)
        active_atom = x11.XInternAtom(display, b"_NET_ACTIVE_WINDOW", 0)
        windows = read_ulong_property(x11, display, root, clients_atom)
        for window in reversed(windows):
            pid_values = read_ulong_property(x11, display, window, pid_atom)
            attributes = XWindowAttributes()
            if pid_values != [browser_pid] or not x11.XGetWindowAttributes(display, window, byref(attributes)):
                continue
            if attributes.map_state != IS_VIEWABLE or attributes.override_redirect:
                continue
            event = XEvent()
            event.xclient.type = CLIENT_MESSAGE
            event.xclient.send_event = 1
            event.xclient.display = display
            event.xclient.window = window
            event.xclient.message_type = active_atom
            event.xclient.format = 32
            event.xclient.data.l[0] = 2
            x11.XRaiseWindow(display, window)
            sent = x11.XSendEvent(
                display,
                root,
                0,
                SUBSTRUCTURE_NOTIFY_MASK | SUBSTRUCTURE_REDIRECT_MASK,
                byref(event),
            )
            x11.XFlush(display)
            return 0 if sent else 1
        return 1
    finally:
        x11.XCloseDisplay(display)


def configure_x11(x11: ctypes.CDLL) -> None:
    x11.XOpenDisplay.argtypes = [c_char_p]
    x11.XOpenDisplay.restype = c_void_p
    x11.XDefaultRootWindow.argtypes = [c_void_p]
    x11.XDefaultRootWindow.restype = c_ulong
    x11.XInternAtom.argtypes = [c_void_p, c_char_p, c_int]
    x11.XInternAtom.restype = c_ulong
    x11.XGetWindowProperty.argtypes = [
        c_void_p, c_ulong, c_ulong, c_long, c_long, c_int, c_ulong,
        POINTER(c_ulong), POINTER(c_int), POINTER(c_ulong), POINTER(c_ulong), POINTER(POINTER(c_ubyte)),
    ]
    x11.XGetWindowProperty.restype = c_int
    x11.XFree.argtypes = [c_void_p]
    x11.XGetWindowAttributes.argtypes = [c_void_p, c_ulong, POINTER(XWindowAttributes)]
    x11.XGetWindowAttributes.restype = c_int
    x11.XRaiseWindow.argtypes = [c_void_p, c_ulong]
    x11.XSendEvent.argtypes = [c_void_p, c_ulong, c_int, c_long, POINTER(XEvent)]
    x11.XSendEvent.restype = c_int
    x11.XFlush.argtypes = [c_void_p]
    x11.XCloseDisplay.argtypes = [c_void_p]


def read_ulong_property(x11: ctypes.CDLL, display: int, window: int, atom: int) -> list[int]:
    actual_type = c_ulong()
    actual_format = c_int()
    item_count = c_ulong()
    bytes_after = c_ulong()
    data = POINTER(c_ubyte)()
    status = x11.XGetWindowProperty(
        display, window, atom, 0, 4096, 0, 0,
        byref(actual_type), byref(actual_format), byref(item_count), byref(bytes_after), byref(data),
    )
    if status != 0 or actual_format.value != 32 or not data:
        return []
    try:
        values = ctypes.cast(data, POINTER(c_ulong))
        return [int(values[index]) for index in range(item_count.value)]
    finally:
        x11.XFree(data)


if __name__ == "__main__":
    raise SystemExit(main())
