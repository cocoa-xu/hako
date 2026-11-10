#!/bin/sh
set -eu
binary=${1:?Missing binary path}
snapshot=${2:?Missing snapshot flag}
if [ "$snapshot" = true ]; then
  codesign --force --sign - --identifier moe.uwucocoa.hako --timestamp=none "$binary"
else
  : "${SIGN_IDENTITY:?Missing Developer ID identity}"
  : "${NOTARY_PROFILE:?Missing notarytool profile}"
  codesign --force --sign "$SIGN_IDENTITY" --identifier moe.uwucocoa.hako --options runtime --timestamp "$binary"
  scratch=$(mktemp -d)
  trap 'rm -rf "$scratch"' EXIT HUP INT TERM
  ditto -c -k --keepParent "$binary" "$scratch/hako.zip"
  set -- xcrun notarytool submit "$scratch/hako.zip" --keychain-profile "$NOTARY_PROFILE" --wait --timeout 20m --output-format json
  if [ -n "${NOTARY_KEYCHAIN-}" ]; then set -- "$@" --keychain "$NOTARY_KEYCHAIN"; fi
  "$@" > "$scratch/result.json"
  status=$(plutil -extract status raw -o - "$scratch/result.json")
  if [ "$status" != Accepted ]; then
    printf 'Apple notarization did not accept the binary.\n' >&2; exit 1
  fi
fi
codesign --verify --strict "$binary"
