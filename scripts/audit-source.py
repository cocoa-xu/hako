#!/usr/bin/env python3
"""Check tracked source for runtime data, credentials, and machine-local details."""
from pathlib import Path
import re
import subprocess
import sys

paths = subprocess.check_output(['git', 'ls-files', '-z']).decode().split('\0')
patterns = {
    'local user path': re.compile(r'/(?:Users|home)/[A-Za-z0-9_.-]+/'),
    'private network address': re.compile(r'\b(?:10\.\d{1,3}\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3}|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3})\b'),
    'private key': re.compile(r'-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----'),
    'service credential': re.compile(r'\b(?:ghp_[A-Za-z0-9]{36}|github_pat_[A-Za-z0-9_]{40,}|AKIA[A-Z0-9]{16})\b'),
}
errors = []
for name in filter(None, paths):
    path = Path(name)
    if not path.exists():
        continue
    if path.parts[0] in {'.local', 'bin', 'dist'} or path.suffix in {'.p12', '.p8', '.key', '.db', '.sqlite', '.pem'}:
        errors.append(f'{name}: runtime or signing material must not be tracked')
        continue
    try:
        text = path.read_text()
    except UnicodeDecodeError:
        errors.append(f'{name}: unexpected binary in source')
        continue
    for label, pattern in patterns.items():
        if pattern.search(text):
            errors.append(f'{name}: {label}')
    if '\u2014' in text:
        errors.append(f'{name}: unsupported punctuation')
if errors:
    print('\n'.join(errors), file=sys.stderr)
    raise SystemExit(1)
print('Source audit passed.')
