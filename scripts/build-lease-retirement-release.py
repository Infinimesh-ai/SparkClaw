#!/usr/bin/env python3
"""Rebuild the paired Host lease retirement release from pinned App-CLI .13.

Use --check to reproduce the runtime, wheel and consumer projection. Reader
assets remain unchanged; this never modifies installed state or services.
"""
from pathlib import Path
import runpy

if __name__ == '__main__':
    build = runpy.run_path(str(Path(__file__).with_name('build-workspace-mail-release.py')))['build']
    build(version='0.3.0-sparkclaw.14', base_version='0.3.0-sparkclaw.13',
          patch_name='host-lease-retirement.patch')
