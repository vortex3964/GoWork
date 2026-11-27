#!/bin/sh
# GoWork updater for Linux and macOS.
#
# Thin wrapper around install.sh so update behavior (path safety, checksum
# verification and user data preservation) stays in one place.

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P) || {
	printf 'error: could not resolve the GoWork scripts directory\n' >&2
	exit 1
}

installer="$script_dir/install.sh"
if [ ! -f "$installer" ]; then
	printf 'error: install.sh was not found next to update.sh\n' >&2
	exit 1
fi

exec bash "$installer" --update "$@"
