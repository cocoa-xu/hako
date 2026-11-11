#!/bin/sh
set -eu
umask 077
package_dir=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
database_file=
public_url=
staging_root=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --database-url-file) database_file=${2:?Missing database connection file}; shift 2 ;;
    --public-url) public_url=${2:?Missing public URL}; shift 2 ;;
    --staging-root) staging_root=${2:?Missing staging directory}; shift 2 ;;
    --help) printf 'Usage: %s [--public-url https://host --database-url-file database.url] [--staging-root directory]\n' "$0"; exit 0 ;;
    *) printf 'Unknown option: %s\n' "$1" >&2; exit 1 ;;
  esac
done
if [ -n "$staging_root" ]; then
  case "$staging_root" in /) exit 1 ;; /*) ;; *) printf 'Staging root must be absolute.\n' >&2; exit 1 ;; esac
elif [ "$(uname -s)" != Linux ] || [ "$(id -u)" != 0 ]; then
  printf 'Run this installer as root on Linux with systemd.\n' >&2; exit 1
fi
if [ -n "$public_url" ] && ! printf '%s\n' "$public_url" | LC_ALL=C grep -Eq '^https://[A-Za-z0-9.-]+(:[0-9]+)?/?$'; then
  printf 'Provide an HTTPS origin without a path, query, or credentials.\n' >&2; exit 1
fi
source_binary="$package_dir/hako"
if [ ! -f "$source_binary" ]; then source_binary="$package_dir/bin/hako"; fi
if [ ! -x "$source_binary" ]; then printf 'Release binary not found.\n' >&2; exit 1; fi
binary_dir="$staging_root/usr/local/libexec/hako"
config_dir="$staging_root/etc/hako"
state_dir="$staging_root/var/lib/hako"
unit_dir="$staging_root/etc/systemd/system"
if [ ! -f "$config_dir/server.env" ] && [ -z "$public_url" ]; then
  printf 'First installation requires --public-url.\n' >&2; exit 1
fi
if [ -n "$database_file" ] && [ ! -f "$database_file" ]; then
  printf 'Database connection file not found.\n' >&2; exit 1
fi
if [ ! -f "$config_dir/database.url" ] && [ -z "$database_file" ]; then
  printf 'First installation requires --database-url-file.\n' >&2; exit 1
fi
install -d -m 0755 "$binary_dir" "$config_dir" "$unit_dir"
install -d -m 0700 "$state_dir"
install -m 0755 "$source_binary" "$binary_dir/hako.next"
mv -f "$binary_dir/hako.next" "$binary_dir/hako"
install -m 0644 "$package_dir/deploy/linux/hako.service" "$unit_dir/hako.service"
if [ -n "$public_url" ]; then
  printf 'HAKO_PUBLIC_URL=%s\n' "$public_url" > "$config_dir/server.env"
  chmod 0644 "$config_dir/server.env"
fi
if [ -n "$database_file" ]; then
  install -m 0600 "$database_file" "$config_dir/database.url.next"
  mv -f "$config_dir/database.url.next" "$config_dir/database.url"
fi
if [ -n "$staging_root" ]; then printf 'Staged server files in %s\n' "$staging_root"; exit 0; fi
if ! id hako >/dev/null 2>&1; then useradd --system --user-group --home-dir /var/lib/hako --shell /usr/sbin/nologin hako; fi
chown hako:hako "$state_dir" "$config_dir/database.url"
systemctl daemon-reload
systemctl enable hako.service
systemctl restart hako.service
printf 'Hako server installed. Configure your HTTPS proxy before enrolling remote workers.\n'
printf 'Administrator key: /var/lib/hako/admin.key\nLogs: journalctl -u hako.service\n'
