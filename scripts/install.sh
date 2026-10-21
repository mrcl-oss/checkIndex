#!/bin/sh
set -eu

fail() {
	printf 'checkindex installer: %s\n' "$1" >&2
	exit 1
}

command -v go >/dev/null 2>&1 || fail "Go is required but was not found on PATH. Install Go 1.22 or newer and retry."

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd) || fail "cannot resolve installer directory"
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd) || fail "cannot resolve source root"
[ -f "$repo_root/go.mod" ] && [ -f "$repo_root/cmd/checkindex/main.go" ] || fail "source root is incomplete (expected go.mod and cmd/checkindex/main.go)"

home=${HOME:-}
[ -n "$home" ] || fail "HOME is not set; set HOME to your user home directory and retry."
bin_dir=${XDG_BIN_HOME:-"$home/.local/bin"}
[ -n "$bin_dir" ] || fail "XDG_BIN_HOME must not be empty."

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/checkindex-install.XXXXXX") || fail "cannot create a temporary build directory"
cleanup() {
	rm -rf "$tmp_dir"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

if ! (cd "$repo_root" && go build -o "$tmp_dir/checkindex" ./cmd/checkindex); then
	fail "build failed; no installed binary was changed"
fi

mkdir -p "$bin_dir" || fail "cannot create destination directory: $bin_dir"
[ ! -d "$bin_dir/checkindex" ] || fail "destination exists as a directory: $bin_dir/checkindex"
chmod 0755 "$tmp_dir/checkindex" || fail "cannot set executable permissions on built binary"
staged=$(mktemp "$bin_dir/.checkindex.XXXXXX") || fail "cannot stage binary in destination directory: $bin_dir"
trap 'rm -f "$staged"; cleanup' EXIT
cp "$tmp_dir/checkindex" "$staged" || fail "cannot stage binary in destination directory: $bin_dir"
chmod 0755 "$staged" || fail "cannot set executable permissions on staged binary"
mv -f "$staged" "$bin_dir/checkindex" || fail "cannot atomically install binary to $bin_dir/checkindex"

printf 'Installed checkindex to %s/checkindex\n' "$bin_dir"
case ":${PATH:-}:" in
	*":$bin_dir:"*) ;;
	*) printf 'Add %s to PATH to run checkindex from any directory.\n' "$bin_dir" ;;
esac
