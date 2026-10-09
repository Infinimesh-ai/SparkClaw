#!/usr/bin/env python3
"""Rebuild SparkClaw's explicit local mail delta from the pinned App-CLI bundle.

Requires Node 26 and installed browser-controller dependencies. No upstream
checkout, remote download, shared contract or installed service is modified.
Use --check to reproduce and compare the committed paired artifacts.
"""
import argparse
import base64
import csv
import gzip
import hashlib
import io
import json
from pathlib import Path
import subprocess
import tarfile
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]
BUNDLE = ROOT / 'vendor/app-cli'
VERSION = '0.3.0-sparkclaw.12'


def sha(data):
    return hashlib.sha256(data).hexdigest()


def encoded(value):
    return (json.dumps(value, indent=2, ensure_ascii=False) + '\n').encode()


def build():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    base = json.loads((BUNDLE / 'base-release-0.3.0-sparkclaw.11.json').read_bytes())
    for artifact in base['artifacts'].values():
        assert sha((BUNDLE / artifact['file']).read_bytes()) == artifact['sha256'], 'Base artifact changed'
    patch = BUNDLE / 'workspace-mail-attachments.patch'
    provenance = {'base_id': base['id'], 'base_runtime_digest': base['runtime_digest'],
                  'patch_file': patch.name, 'patch_sha256': sha(patch.read_bytes())}
    with tempfile.TemporaryDirectory(prefix='workspace-mail-release-') as temp:
        source = Path(temp) / 'package'
        with tarfile.open(BUNDLE / base['artifacts']['runtime']['file']) as archive:
            for member in archive:
                assert member.isfile() and member.name.startswith('package/')
                assert '..' not in Path(member.name).parts
                target = Path(temp) / member.name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(archive.extractfile(member).read())
                target.chmod(member.mode)
        subprocess.run(['patch', '-p1', '--batch', '--forward', '-i', str(patch)], cwd=source, check=True)
        for name in ['package.json', 'npm-shrinkwrap.json']:
            file = source / name
            value = json.loads(file.read_bytes())
            value['version'] = VERSION
            if 'packages' in value:
                value['packages']['']['version'] = VERSION
            file.write_bytes(encoded(value))
        (source / 'node_modules').symlink_to(ROOT / 'tools/browser-controller/node_modules', target_is_directory=True)
        subprocess.run(['node', 'applications/mail/build-bindings.mjs'], cwd=source, check=True)
        (source / 'node_modules').unlink()
        manifest = json.loads((source / 'release.json').read_bytes())
        manifest.update(id=VERSION, local_patch=provenance)
        manifest['files'] = {p.relative_to(source).as_posix(): sha(p.read_bytes())
                             for p in sorted(source.rglob('*')) if p.is_file() and p.name != 'release.json'}
        # Only the root manifest is excluded; nested application release data,
        # should it be introduced later, is part of the verified source closure.
        for p in source.rglob('release.json'):
            if p != source / 'release.json':
                manifest['files'][p.relative_to(source).as_posix()] = sha(p.read_bytes())
        (source / 'release.json').write_bytes(encoded(manifest))
        runtime_bytes = io.BytesIO()
        with gzip.GzipFile(fileobj=runtime_bytes, mode='wb', filename='', mtime=0) as gzip_file:
            with tarfile.open(fileobj=gzip_file, mode='w') as archive:
                for file in sorted(source.rglob('*')):
                    if not file.is_file():
                        continue
                    data = file.read_bytes()
                    entry = tarfile.TarInfo('package/' + file.relative_to(source).as_posix())
                    entry.size = len(data)
                    entry.mode = 0o755 if file.stat().st_mode & 0o111 else 0o644
                    archive.addfile(entry, io.BytesIO(data))
        release = {**base, 'id': VERSION, 'runtime_digest': sha(encoded(manifest)), 'local_patch': provenance}
        with zipfile.ZipFile(BUNDLE / base['artifacts']['wheel']['file']) as archive:
            files = {name.replace('0.3.0+sparkclaw.11.dist-info/', '0.3.0+sparkclaw.12.dist-info/'): archive.read(name)
                     for name in archive.namelist()}
        metadata = 'infinimesh_app_cli-0.3.0+sparkclaw.12.dist-info/METADATA'
        files[metadata] = files[metadata].replace(b'Version: 0.3.0+sparkclaw.11\n', b'Version: 0.3.0+sparkclaw.12\n')
        files['app_cli/release.json'] = encoded({'id': VERSION, 'runtime_digest': release['runtime_digest']})
        record = 'infinimesh_app_cli-0.3.0+sparkclaw.12.dist-info/RECORD'
        rows = io.StringIO()
        writer = csv.writer(rows, lineterminator='\n')
        for name in sorted(files):
            if name != record:
                digest = base64.urlsafe_b64encode(hashlib.sha256(files[name]).digest()).rstrip(b'=').decode()
                writer.writerow([name, 'sha256=' + digest, len(files[name])])
        writer.writerow([record, '', ''])
        files[record] = rows.getvalue().encode()
        wheel_bytes = io.BytesIO()
        with zipfile.ZipFile(wheel_bytes, 'w', zipfile.ZIP_DEFLATED) as archive:
            for name in sorted(files):
                info = zipfile.ZipInfo(name, date_time=(2026, 10, 9, 0, 0, 0))
                info.compress_type = zipfile.ZIP_DEFLATED
                info.external_attr = 0o644 << 16
                archive.writestr(info, files[name])
        generated = {f'infinimesh-app-cli-runtime-{VERSION}.tgz': runtime_bytes.getvalue(),
                     'infinimesh_app_cli-0.3.0+sparkclaw.12-py3-none-any.whl': wheel_bytes.getvalue()}
        release['artifacts'] = {'python_dependencies': base['artifacts']['python_dependencies']}
        for kind, name in zip(['runtime', 'wheel'], generated):
            release['artifacts'][kind] = {'file': name, 'sha256': sha(generated[name])}
        generated['release.json'] = encoded(release)
        for name, data in generated.items():
            destination = BUNDLE / name
            if args.check:
                assert destination.read_bytes() == data, 'Reproduction differs: ' + name
            else:
                destination.write_bytes(data)
        consumer = ROOT / 'configs/app-cli-release.json'
        if args.check:
            assert consumer.read_bytes() == generated['release.json']
        else:
            consumer.write_bytes(generated['release.json'])
        print(json.dumps({'release': VERSION, 'runtime_digest': release['runtime_digest'], 'reproduced': args.check}))


if __name__ == '__main__':
    build()
