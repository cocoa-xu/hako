#!/bin/sh
set -eu
umask 077

cd "$(dirname "$0")/.."
project_dir=$(pwd)
data_dir="$project_dir/.local"
binary="$project_dir/bin/hako"
root_agent=false

case "${1-}" in
  "") ;;
  --root-agent) root_agent=true ;;
  *) printf 'Usage: %s [--root-agent]\n' "$0" >&2; exit 1 ;;
esac

if [ ! -x "$binary" ]; then
  printf 'Build Hako first with make build.\n' >&2
  exit 1
fi

mkdir -p "$data_dir"

set -- "$binary" serve --data-dir "$data_dir" --listen 127.0.0.1:8787
if [ -f "$data_dir/listen-addresses" ]; then
  while IFS= read -r address || [ -n "$address" ]; do
    if [ -n "$address" ]; then set -- "$@" --listen "$address"; fi
  done < "$data_dir/listen-addresses"
fi
"$@" > "$data_dir/server.log" 2>&1 &
server_pid=$!
cleanup() {
  kill "$server_pid" 2>/dev/null || true
  wait "$server_pid" 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

attempt=0
while :; do
  if ! kill -0 "$server_pid" 2>/dev/null; then
    cat "$data_dir/server.log" >&2
    exit 1
  fi
  case "$(cat "$data_dir/server.log")" in
    *'Hako is listening'*)
      if curl --silent --fail http://127.0.0.1:8787/healthz > /dev/null; then break; fi
      ;;
  esac
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 50 ]; then
    printf 'The local receiver did not become ready. See .local/server.log.\n' >&2
    exit 1
  fi
  sleep 0.1
done

if [ ! -f "$data_dir/local-agent.path" ]; then
  key_file="$data_dir/local-enrollment.key"
  if [ ! -f "$key_file" ]; then
    "$binary" invite --admin-key-file "$data_dir/admin.key" --key-out "$key_file" --label 'Local Mac' > /dev/null
  fi
  config="$data_dir/worker.json"
  "$binary" join --server http://127.0.0.1:8787 --config "$config" --key-file "$key_file"
  rm "$key_file"
  printf '%s\n' "$config" > "$data_dir/local-agent.path"
fi
config=$(cat "$data_dir/local-agent.path")
if [ ! -f "$config" ]; then
  printf 'The local agent credential file is missing: %s\n' "$config" >&2
  exit 1
fi

printf 'Hako: http://127.0.0.1:8787\n'
printf 'Administrator key: %s/admin.key\n' "$data_dir"
if [ -f "$data_dir/listen-addresses" ]; then
  while IFS= read -r address || [ -n "$address" ]; do
    if [ -n "$address" ]; then printf 'Hako: http://%s\n' "$address"; fi
  done < "$data_dir/listen-addresses"
fi
printf 'Press Control-C to stop the receiver and agent.\n'
if [ "$root_agent" = true ]; then
  sudo -k -- "$binary" agent --config "$config"
else
  "$binary" agent --config "$config"
fi
