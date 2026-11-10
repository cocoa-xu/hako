#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
if [ -z "$(git remote)" ]; then
  export GIT_CONFIG_COUNT=1
  export GIT_CONFIG_KEY_0=remote.origin.url
  export GIT_CONFIG_VALUE_0=https://github.com/example/hako.git
fi
case "${1-snapshot}" in
  check) exec "${GORELEASER-goreleaser}" check ;;
  snapshot) exec "${GORELEASER-goreleaser}" release --snapshot --clean --parallelism 2 ;;
  *) printf 'Usage: %s [check|snapshot]\n' "$0" >&2; exit 1 ;;
esac
