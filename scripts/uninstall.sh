#!/bin/sh
# GoWork uninstaller for Linux and macOS.
#
# Thin wrapper around install.sh so removal behavior (path safety, launcher
# handling and user data preservation) stays in one place.

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P) || {
	printf 'error: could not resolve the GoWork scripts directory\n' >&2
	exit 1
}

installer="$script_dir/install.sh"
if [ ! -f "$installer" ]; then
	printf 'error: install.sh was not found next to uninstall.sh\n' >&2
	exit 1
fi

exec bash "$installer" --uninstall "$@"
