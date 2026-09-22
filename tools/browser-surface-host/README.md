# Retired Browser Surface Route And Isolated Qualification

> Language: English | [简体中文](../../zh-cn/tools/browser-surface-host/README.md)

> Status: retired from the product architecture on 2026-09-21. The code now has
> a narrow, disposable Electron-anchor qualification suite for the historical
> X11 host boundary; it is not a browser runtime, fallback, or Electron release gate.

Electron's bundled Chromium and native `WebContentsView` remain the sole target
browser architecture. This directory must not be connected to the live Browser
Bridge, an existing Chromium process, the production browser profile, or the
user's active desktop.

## What The Qualification Covers

The test-only Electron fixture creates one frameless 640×480 anchor on a
process-owned Xvfb display. It loads only a `data:` URL, uses a random identity
marker, and stores all Electron state in a per-run temporary directory. A local
synthetic X11 child stands in for a browser surface; no extension or Browser
Bridge is loaded.

The automated checks prove only that this historical host:

- accepts the exact Electron anchor PID, executable, UID, marker, and role, and
  rejects mismatches;
- preserves anchor focus and redirects focus away from a task child;
- clips an overflowing personal surface to the anchor bounds, including input;
- restores or preserves the child after normal detach and host `SIGKILL`; and
- exits cleanly after the Electron anchor is killed without killing the child.

The runner starts Xvfb itself with TCP disabled, records its PID and display,
and refuses to run if the test display is the inherited active display. Both the
Electron and synthetic suites verify that registered PID is a live Xvfb process;
`SPARKCLAW_SURFACE_TEST_ISOLATED=1` alone is no longer sufficient.

Install and run the disposable qualification with:

```sh
npm ci --prefix tools/browser-surface-host/test/electron-anchor
make -C tools/browser-surface-host test
make -C tools/browser-surface-host test-electron-anchor
```

These tests do not qualify a real window manager/compositor, popups or dialogs,
GPU behavior, production Chromium, the Browser Bridge, or the accepted Electron
adapter/WebContentsView design.

## Why The Active Desktop Failed

The earlier tests repeatedly reparented, unmapped, focused, and restacked X11
windows directly on the user's active `DISPLAY=:1`. During those runs, the
system journal repeatedly reported a disposed `MetaWindowActorX11`,
stage/allocation errors, and a Clutter assertion. At
`2026-09-21T10:10:40.586013+08:00`, it records `GNOME Shell crashed with signal
11`.

That establishes the immediate failure as a Mutter/GNOME Shell compositor crash
triggered through the active-display X11 window-manipulation path—not a compile
failure. The Xorg log later ended with `Server terminated successfully (0)`, and
the surrounding kernel log contained no NVIDIA Xid, GPU reset, or OOM evidence.
The logs do not isolate which reparent/unmap/focus/stacking operation exposed the
defect or identify the exact defective Mutter code path. A later test revision
also targeted a window-manager decoration frame directly, demonstrating an
additional unsafe path, but it was written after the recorded crash and is not
claimed as its trigger. The evidence therefore does not justify a broader claim
that every GPU/display risk has been eliminated.

The host uses an override-redirect root-level frame, exact window identity,
an InputOnly task shield, focus redirection, and an X11 save-set. It is retained
only to reproduce and guard this historical boundary safely; production work
continues from the accepted
[desktop browser design](../../docs/desktop-client-embedded-browser-design.md).
