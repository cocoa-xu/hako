#!/usr/bin/env python3
"""Export prototype SQLite state without modifying its source database."""
import argparse
import datetime
import json
import os
from pathlib import Path
import sqlite3

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--database', required=True, type=Path)
parser.add_argument('--output-dir', required=True, type=Path)
args = parser.parse_args()
os.umask(0o077)


def timestamp(value):
    if value is None:
        return None
    seconds, nanos = divmod(value, 1_000_000_000)
    date = datetime.datetime.fromtimestamp(seconds, datetime.timezone.utc)
    return date.strftime('%Y-%m-%dT%H:%M:%S') + f'.{nanos:09d}Z'


with sqlite3.connect(args.database.resolve().as_uri() + '?mode=ro', uri=True) as db:
    db.execute('BEGIN')
    if db.execute('PRAGMA user_version').fetchone()[0] != 1:
        raise SystemExit('Unsupported SQLite schema version.')
    settings = dict(db.execute('SELECT key,value FROM settings'))
    registry = {'admin_key_hash': settings.get('admin_key_hash', ''),
                'devices': {}, 'enrollments': {}, 'sessions': {}}
    for device, digest, name, created, revoked in db.execute(
            'SELECT id,token_hash,display_name,created_at,revoked_at FROM devices'):
        registry['devices'][device] = {'token_hash': digest, 'display_name': name,
                                      'created_at': timestamp(created),
                                      'revoked_at': timestamp(revoked)}
    for digest, key_id, label, created, expires, device, revoked in db.execute(
            'SELECT key_hash,id,label,created_at,expires_at,device_id,revoked FROM enrollments'):
        registry['enrollments'][digest] = {'id': key_id, 'label': label,
                                          'created_at': timestamp(created),
                                          'expires_at': timestamp(expires),
                                          'device_id': device or '',
                                          'revoked': bool(revoked)}
    for digest, expires in db.execute('SELECT token_hash,expires_at FROM sessions'):
        registry['sessions'][digest] = timestamp(expires)
    snapshots = list(db.execute('SELECT device_id,payload FROM snapshots'))
    args.output_dir.mkdir(mode=0o700)
    (args.output_dir / 'machines').mkdir(mode=0o700)
    (args.output_dir / 'registry.json').write_text(json.dumps(registry) + '\n')
    for device, payload in snapshots:
        # UUID filenames never contain path separators.
        import uuid
        if str(uuid.UUID(device)) != device:
            raise SystemExit('Invalid device UUID in source database.')
        machine = json.loads(payload)
        (args.output_dir / 'machines' / (device + '.json')).write_text(json.dumps(machine) + '\n')
print('Exported private state. Preserve admin.key and import into an empty PostgreSQL database.')
