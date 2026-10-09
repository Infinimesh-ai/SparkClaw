#!/usr/bin/env python3
"""Rebuild the paired Outlook mailbox identity release from pinned App-CLI .12.

Use --check to reproduce the runtime, wheel and consumer projection. This only
builds artifacts; it never modifies installed application state or services.
"""
from pathlib import Path
import runpy

if __name__ == '__main__':
    build = runpy.run_path(str(Path(__file__).with_name('build-workspace-mail-release.py')))['build']
    build(version='0.3.0-sparkclaw.13', base_version='0.3.0-sparkclaw.12',
          patch_name='mail-account-identity.patch', build_userscripts=True)
