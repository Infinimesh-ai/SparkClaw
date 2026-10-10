#!/usr/bin/env python3
"""Reproduce the bounded Outlook native-menu settle delta; no installed state changes."""
from pathlib import Path
import runpy
if __name__ == '__main__':
    build = runpy.run_path(str(Path(__file__).with_name('build-workspace-mail-release.py')))['build']
    build(version='0.3.0-sparkclaw.18', base_version='0.3.0-sparkclaw.17', patch_name='outlook-menu-settle.patch')
