#!/usr/bin/env python3
"""Reproduce native upload ready proof and exact chooser cleanup; no installed state changes."""
from pathlib import Path
import runpy
if __name__ == '__main__':
    build = runpy.run_path(str(Path(__file__).with_name('build-workspace-mail-release.py')))['build']
    build(version='0.3.0-sparkclaw.17', base_version='0.3.0-sparkclaw.16', patch_name='native-mail-cards.patch')
