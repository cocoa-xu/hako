#!/bin/sh
set -eu
umask 077
package_dir=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
server_url=
key_file=
access_file=
allow_http=false
staging_root=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --server) server_url=${2:?Missing server URL}; shift 2 ;;
    --key-file) key_file=${2:?Missing key file}; shift 2 ;;
    --access-service-token-file) access_file=${2:?Missing Access service token file}; shift 2 ;;
    --allow-http) allow_http=true; shift ;;
    --staging-root) staging_root=${2:?Missing staging directory}; shift 2 ;;
    --help) printf 'Usage: %s [--server https://host --key-file enrollment.key] [--access-service-token-file access.json] [--allow-http] [--staging-root directory]\n' "$0"; exit 0 ;;
    *) printf 'Unknown option: %s\n' "$1" >&2; exit 1 ;;
  esac
done
if [ -n "$staging_root" ]; then
  case "$staging_root" in /) exit 1 ;; /*) ;; *) printf 'Staging root must be an absolute directory.\n' >&2; exit 1 ;; esac
elif [ "$(uname -s)" != Darwin ] || [ "$(id -u)" != 0 ]; then
  printf 'Run this installer as root on macOS.\n' >&2; exit 1
fi
source_binary="$package_dir/hako"
if [ ! -f "$source_binary" ]; then source_binary="$package_dir/bin/hako"; fi
if [ ! -x "$source_binary" ]; then printf 'Release binary not found.\n' >&2; exit 1; fi
binary_dir="$staging_root/usr/local/libexec/hako"
config_dir="$staging_root/Library/Application Support/Hako"
log_dir="$staging_root/Library/Logs/Hako"
plist_dir="$staging_root/Library/LaunchDaemons"
config="$config_dir/worker.json"
if [ -z "$staging_root" ] && [ ! -f "$config" ] && { [ -z "$server_url" ] || [ -z "$key_file" ]; }; then
  printf 'First installation requires --server and --key-file.\n' >&2; exit 1
fi
if { [ -n "$server_url" ] && [ -z "$key_file" ]; } || { [ -z "$server_url" ] && [ -n "$key_file" ]; }; then
  printf 'Provide both --server and --key-file for enrollment.\n' >&2; exit 1
fi
install -d -m 0755 "$binary_dir" "$plist_dir"
install -d -m 0700 "$config_dir" "$log_dir"
install -m 0755 "$source_binary" "$binary_dir/hako.next"
mv -f "$binary_dir/hako.next" "$binary_dir/hako"
install -m 0644 "$package_dir/deploy/macos/moe.uwucocoa.hako.worker.plist" "$plist_dir/moe.uwucocoa.hako.worker.plist"
if [ ! -f "$log_dir/worker.log" ]; then install -m 0600 /dev/null "$log_dir/worker.log"; fi
if [ -n "$staging_root" ]; then printf 'Staged worker files in %s\n' "$staging_root"; exit 0; fi
if [ ! -f "$config" ] || [ -n "$server_url" ]; then
  set -- "$binary_dir/hako" join --server "$server_url" --config "$config" --key-file "$key_file"
  if [ -n "$access_file" ]; then set -- "$@" --access-service-token-file "$access_file"; fi
  if [ "$allow_http" = true ]; then set -- "$@" --allow-http; fi
  "$@"
elif [ -n "$access_file" ]; then
  "$binary_dir/hako" configure-access --config "$config" --service-token-file "$access_file"
fi
launchctl bootout system/moe.uwucocoa.hako.worker 2>/dev/null || true
launchctl enable system/moe.uwucocoa.hako.worker
launchctl bootstrap system "$plist_dir/moe.uwucocoa.hako.worker.plist"
printf 'Hako worker installed. Logs: %s/worker.log\n' "$log_dir"
printf 'The enrollment key is no longer needed; remove its input file.\n'
