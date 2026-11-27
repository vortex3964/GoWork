#!/bin/sh
# GoWork uninstaller for Linux and macOS.
#
# Thin wrapper around install.sh so removal behavior (path safety, launcher
# handling and user data preservation) stays in one place.

set -eu

# resolve_script_dir follows symlinks so a launcher such as
# ~/bin/gowork-uninstall -> .../scripts/uninstall.sh finds install.sh beside
# the real script.
resolve_script_dir() {
	target="$1"
	hops=0

	while [ -L "$target" ]; do
		hops=$((hops + 1))
		[ "$hops" -le 40 ] || return 1

		link_dir=$(CDPATH= cd -- "$(dirname -- "$target")" && pwd -P) || return 1
		link_target=$(readlink "$target") || return 1
		case "$link_target" in
		/*) target="$link_target" ;;
		*) target="$link_dir/$link_target" ;;
		esac
	done

	CDPATH= cd -- "$(dirname -- "$target")" && pwd -P
}

script_dir=$(resolve_script_dir "$0") || {
	printf 'error: could not resolve the GoWork scripts directory\n' >&2
	exit 1
}

installer="$script_dir/install.sh"
if [ ! -f "$installer" ]; then
	printf 'error: install.sh was not found next to uninstall.sh\n' >&2
	exit 1
fi

exec bash "$installer" --uninstall "$@"
