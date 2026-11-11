#!/usr/bin/env python3
"""Verify release archive checksums and required deployment files."""
import hashlib
from pathlib import Path, PurePosixPath
import re
import sys
import tarfile

directory = Path(sys.argv[1] if len(sys.argv) > 1 else 'dist')
required = {
    'hako', 'README.md', 'LICENSE', 'CHANGELOG.md',
    'docs/cloudflare.md', 'docs/deployment.md', 'docs/security.md',
    'deploy/Caddyfile', 'deploy/nginx.conf', 'deploy/linux/hako.service',
    'deploy/macos/moe.uwucocoa.hako.server.plist',
    'deploy/macos/moe.uwucocoa.hako.worker.plist',
    'scripts/install-server.sh', 'scripts/install-worker.sh',
}
targets = set()
versions = set()
names = set()
for line in (directory / 'SHA256SUMS').read_text().splitlines():
    digest, name = line.split()
    match = re.fullmatch(r'hako_(.+)_(darwin|linux)_(amd64|arm64)\.tar\.gz', name)
    if not match or name in names:
        raise SystemExit('Unexpected or duplicate release archive: ' + name)
    names.add(name)
    version, system, arch = match.groups()
    versions.add(version)
    targets.add((system, arch))
    archive = directory / name
    if hashlib.sha256(archive.read_bytes()).hexdigest() != digest:
        raise SystemExit('Checksum mismatch: ' + name)
    package = name.removesuffix('.tar.gz')
    with tarfile.open(archive) as tar:
        files = set()
        for member in tar.getmembers():
            path = PurePosixPath(member.name)
            if path.is_absolute() or '..' in path.parts or not path.parts or path.parts[0] != package:
                raise SystemExit('Invalid archive path: ' + member.name)
            if not (member.isfile() or member.isdir()):
                raise SystemExit('Unexpected archive entry: ' + member.name)
            if any(part.startswith('.') for part in path.parts) or path.suffix in {'.key', '.p12', '.p8', '.db', '.sqlite'}:
                raise SystemExit('Private or runtime file in archive: ' + member.name)
            if member.isfile():
                files.add(str(path.relative_to(package)))
        missing = required - files
        if missing:
            raise SystemExit(name + ' is missing: ' + ', '.join(sorted(missing)))
    print(name + ': checksum and deployment contents verified')
if targets != {(system, arch) for system in ('darwin', 'linux') for arch in ('amd64', 'arm64')} or len(versions) != 1:
    raise SystemExit('Release must contain all four platform archives at one version')
if names != {path.name for path in directory.glob('*.tar.gz')}:
    raise SystemExit('Release contains an archive absent from SHA256SUMS')
