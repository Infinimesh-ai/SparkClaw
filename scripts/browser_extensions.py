#!/usr/bin/env python3
"""The product browser always loads the pinned Bridge and managed Tampermonkey."""
import json
from pathlib import Path
import sys


def extension_paths(config_path: Path, bridge: str) -> str:
    receipt = json.loads(Path('/opt/sparkclaw/browser-components/receipt.json').read_text())
    extension = Path(receipt['manifest']['tampermonkey']['path'])
    if extension != Path('/opt/sparkclaw/tampermonkey') or extension.is_symlink() or extension.stat().st_uid != 0:
        raise ValueError('managed Tampermonkey installation is missing or unsafe')
    if not (extension / 'manifest.json').is_file():
        raise ValueError('managed Tampermonkey manifest is missing')
    return f'{bridge},{extension}'


if __name__ == '__main__':
    print(extension_paths(Path(sys.argv[1]), sys.argv[2]))
