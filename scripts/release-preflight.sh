#!/bin/sh
set -eu
if [ "$(uname -s)" != Darwin ]; then
  printf 'Build the complete release on macOS with Xcode Command Line Tools.\n' >&2; exit 1
fi
python3 scripts/audit-source.py
if [ "${1-false}" != true ]; then
  : "${SIGN_IDENTITY:?A Developer ID Application identity is required for releases}"
  : "${NOTARY_PROFILE:?A notarytool keychain profile is required for releases}"
fi
