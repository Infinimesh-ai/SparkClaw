#!/usr/bin/env python3
"""Build-time-only relocatable runtime from a verified build and Python prefix.

No dependency installation, account import or official networking occurs here.
The same builder runs natively on the matching Mac arm64 and Linux x86_64 hosts.
"""
import argparse
import hashlib
import json
from pathlib import Path
import platform
import shutil
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
MODULES = ('annotated_types', 'blake3', 'pydantic', 'pydantic_core', 'typing_inspection', 'typing_extensions.py')

def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

def copy(source, target):
    if source.is_symlink():
        resolved = source.resolve(strict=True)
        if not resolved.is_relative_to(PREFIX):
            raise RuntimeError('standalone runtime links outside its prefix')
        source = resolved
    if source.is_dir():
        target.mkdir(parents=True, exist_ok=True)
        for child in source.iterdir():
            if child.name in ('__pycache__', 'site-packages', 'test', 'tests', 'idlelib', 'tkinter', 'ensurepip') or child.suffix in ('.pyc', '.a'):
                continue
            copy(child, target / child.name)
    else:
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, target)

def main():
    global PREFIX
    parser=argparse.ArgumentParser()
    parser.add_argument('--build',type=Path,required=True)
    parser.add_argument('--runtime-prefix',type=Path,required=True)
    parser.add_argument('--output',type=Path,required=True)
    args=parser.parse_args()
    build=args.build.absolute(); PREFIX=args.runtime_prefix.resolve(strict=True); output=args.output.absolute()
    if output.exists(): raise RuntimeError('runtime output exists; choose a new directory')
    # Source/build freshness and exact dependency versions are checked first.
    subprocess.run([sys.executable,str(ROOT/'scripts/setup-ezmcd-adapter.py'),'--check','--output',str(build)],check=True)
    lock=json.loads((build/'manifest.json').read_text())['upstream']
    contract=json.loads((ROOT/'tools/ezmcd-adapter/runtime-contract.json').read_text())
    adapter_files={str(p.relative_to(build/'adapter')):sha(p) for p in sorted((build/'adapter').rglob('*')) if p.is_file() and '__pycache__' not in p.parts and p.suffix!='.pyc'}
    if hashlib.sha256(json.dumps(adapter_files,sort_keys=True,separators=(',',':')).encode()).hexdigest()!=contract['adapter_source_sha256']:
        raise RuntimeError('runtime source contract is stale')
    version=subprocess.check_output([str(PREFIX/'bin/python3.11'),'-I','-B','-c','import platform;print(platform.python_version())'],text=True).strip()
    if version!=lock['python'] or (platform.system(),platform.machine()) not in (('Darwin','arm64'),('Linux','x86_64')): raise RuntimeError('runtime target is unqualified')
    output.mkdir(mode=0o755,parents=True)
    copy(PREFIX/'bin/python3.11',output/'bin/python3')
    copy(PREFIX/'lib',output/'lib')
    site=output/'lib/python3.11/site-packages';site.mkdir(parents=True,exist_ok=True)
    source_site=Path(subprocess.check_output([str(build/'venv/bin/python'),'-I','-B','-c','import site;print(site.getsitepackages()[0])'],text=True).strip())
    for module in MODULES: copy(source_site/module,site/module)
    packages=[line.split('==')[0].replace('-','_') for line in (build/'requirements.txt').read_text().splitlines() if line and not line.startswith('#')]
    for entry in source_site.glob('*.dist-info'):
        if any(entry.name.startswith(name+'-') for name in packages): copy(entry,site/entry.name)
    copy(build/'upstream/src/ezmcd',site/'ezmcd')
    copy(build/'adapter/sparkclaw_mcd',site/'sparkclaw_mcd')
    for source,name in ((build/'NOTICE','NOTICE'),(build/'upstream/LICENSE','EzMcDonalds-LICENSE'),(build/'requirements.txt','requirements.txt')):copy(source,output/name)
    source_manifest=hashlib.sha256(json.dumps(json.loads((build/'manifest.json').read_text())['files'],sort_keys=True,separators=(',',':')).encode()).hexdigest()
    manifest={'schema_version':1,'kind':'sparkclaw-mcd-runtime-v1','python':version,'adapter_revision':lock['adapter_revision'],'upstream_revision':lock['revision'],
        'patch_sha256':[p['sha256'] for p in lock['patches']], 'platform':platform.system().lower(),'architecture':platform.machine(),
        'source_manifest_sha256':source_manifest,'runtime_base_build':(PREFIX/'BUILD').read_text().strip() if (PREFIX/'BUILD').is_file() else 'operator-provided',
        'contract':contract,'files':{str(p.relative_to(output)):sha(p) for p in sorted(output.rglob('*')) if p.is_file()}}
    (output/'manifest.json').write_text(json.dumps(manifest,sort_keys=True,indent=2)+'\n')
    # Move the complete runtime before smoke testing. Absolute venv/pth links
    # cannot accidentally make the test pass against the source installation.
    moved=output.with_name(output.name+'-relocation-check')
    if moved.exists():raise RuntimeError('relocation check output already exists')
    output.rename(moved)
    try:
        subprocess.run([str(moved/'bin/python3'),'-I','-B','-c','import sparkclaw_mcd.helper,ezmcd.provider,pydantic,blake3;print("relocatable helper imports passed")'],cwd='/',check=True,timeout=30)
    finally:moved.rename(output)
    print(json.dumps({'runtime':str(output),'manifest_sha256':sha(output/'manifest.json'),'files':len(manifest['files']),'bytes':sum(p.stat().st_size for p in output.rglob('*') if p.is_file())}))

if __name__=='__main__':main()
