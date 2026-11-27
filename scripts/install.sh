#!/usr/bin/env bash
# GoWork installer and updater for Linux and macOS.
#
# Installs a release with its prompts, logo and helper scripts, links the
# gowork command into the user's bin directory, and keeps a marked PATH block
# in the shell profile. Use --update to replace an existing install and
# --uninstall to remove it.

set -euo pipefail

REPO="vortex3964/GoWork"
RELEASES_URL="https://github.com/${REPO}/releases"
PATH_START="# >>> GoWork installer >>>"
PATH_END="# <<< GoWork installer <<<"

MODE="install"
VERSION="latest"
INSTALL_DIR=""
MODIFY_PATH=1
TMP_DIR=""
OS=""
ARCH=""
DOWNLOADER=""

say() {
	printf '%s\n' "$*"
}

warn() {
	printf 'warning: %s\n' "$*" >&2
}

die() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

usage() {
	cat <<'EOF'
GoWork installer

Usage:
  install.sh [options]

Options:
  --update             Replace an existing installation (latest or --version)
  --uninstall          Remove GoWork, its launcher link, and its PATH block
  --version <tag>      Release tag to install (default: latest)
  --dir <path>         Install directory
  --no-modify-path     Do not add the launcher directory to the shell PATH
  -h, --help           Show this help

Defaults:
  install directory   ${XDG_DATA_HOME:-$HOME/.local/share}/gowork
  launcher            ${XDG_BIN_HOME:-$HOME/.local/bin}/gowork

Examples:
  ./install.sh
  ./install.sh --update
  ./install.sh --uninstall
  ./install.sh --version v1.2.3 --dir "$HOME/opt/gowork"
EOF
}

parse_args() {
	while [ "$#" -gt 0 ]; do
		case "$1" in
		--update)
			MODE="update"
			;;
		--uninstall)
			MODE="uninstall"
			;;
		--version)
			[ "$#" -ge 2 ] || die "--version requires a release tag"
			VERSION="$2"
			shift
			;;
		--version=*)
			VERSION="${1#*=}"
			;;
		--dir)
			[ "$#" -ge 2 ] || die "--dir requires a path"
			INSTALL_DIR="$2"
			shift
			;;
		--dir=*)
			INSTALL_DIR="${1#*=}"
			;;
		--no-modify-path)
			MODIFY_PATH=0
			;;
		-h | --help)
			usage
			exit 0
			;;
		--)
			shift
			[ "$#" -eq 0 ] || die "unexpected argument: $1"
			break
			;;
		-*)
			die "unknown option: $1 (try --help)"
			;;
		*)
			die "unexpected argument: $1 (try --help)"
			;;
		esac
		shift
	done
}

normalize_version() {
	case "$1" in
	"" | latest)
		printf 'latest\n'
		;;
	v*)
		printf '%s\n' "$1"
		;;
	V*)
		printf 'v%s\n' "${1#?}"
		;;
	*)
		printf 'v%s\n' "$1"
		;;
	esac
}

expand_dir() {
	case "$1" in
	"~")
		printf '%s\n' "$HOME"
		;;
	"~/"*)
		printf '%s/%s\n' "$HOME" "${1#\~/}"
		;;
	*)
		printf '%s\n' "$1"
		;;
	esac
}

absolute_dir() {
	case "$1" in
	/*)
		printf '%s\n' "$1"
		;;
	*)
		printf '%s/%s\n' "$(pwd -P)" "$1"
		;;
	esac
}

default_install_dir() {
	if [ -n "${XDG_DATA_HOME:-}" ]; then
		printf '%s/gowork\n' "$XDG_DATA_HOME"
	else
		printf '%s/.local/share/gowork\n' "$HOME"
	fi
}

detect_platform() {
	uname_s="$(uname -s)"
	case "$uname_s" in
	Linux)
		OS="linux"
		;;
	Darwin)
		OS="darwin"
		;;
	MINGW* | MSYS* | CYGWIN* | Windows_NT)
		die "this script is for Linux and macOS; on Windows use scripts/install.ps1 in PowerShell"
		;;
	*)
		die "unsupported operating system: $uname_s"
		;;
	esac

	uname_m="$(uname -m)"
	case "$uname_m" in
	x86_64 | amd64)
		ARCH="amd64"
		;;
	aarch64 | arm64)
		ARCH="arm64"
		;;
	*)
		die "unsupported architecture: $uname_m"
		;;
	esac
}

detect_downloader() {
	if [ -z "$DOWNLOADER" ]; then
		if command -v curl >/dev/null 2>&1; then
			DOWNLOADER="curl"
		elif command -v wget >/dev/null 2>&1; then
			DOWNLOADER="wget"
		else
			die "curl or wget is required to download GoWork"
		fi
	fi
}

download_file() {
	local url="$1"
	local destination="$2"
	case "$DOWNLOADER" in
	curl)
		curl -fsSL --retry 3 --retry-delay 1 --connect-timeout 15 -o "$destination" "$url"
		;;
	wget)
		wget -q --tries=3 --timeout=30 -O "$destination" "$url"
		;;
	esac
}

latest_tag() {
	local url=""
	local tag=""
	local headers=""
	case "$DOWNLOADER" in
	curl)
		url="$(curl -fsSIL --retry 2 --connect-timeout 15 -o /dev/null -w '%{url_effective}' "${RELEASES_URL}/latest")" || return 1
		tag="${url%%\?*}"
		tag="${tag##*/}"
		;;
	wget)
		headers="$(wget -S --spider --max-redirect=20 "${RELEASES_URL}/latest" 2>&1)" || return 1
		tag="$(printf '%s\n' "$headers" | sed -n 's|^[[:space:]]*[Ll]ocation: .*/tag/\([^[:space:]\r]*\).*|\1|p' | tail -n 1)"
		;;
	*)
		return 1
		;;
	esac

	case "$tag" in
	"" | latest | releases)
		return 1
		;;
	esac
	printf '%s\n' "$tag"
}

verify_checksum() {
	local archive="$1"
	local checks="$2"
	local asset="$3"
	local expected=""
	local actual=""

	expected="$(awk -v name="$asset" '{ candidate = $2; sub(/^\*/, "", candidate); if (candidate == name) { print tolower($1); exit } }' "$checks")"
	if [ -z "$expected" ]; then
		die "checksums.txt does not contain an entry for $asset"
	fi

	if command -v sha256sum >/dev/null 2>&1; then
		actual="$(sha256sum "$archive" | awk '{ print tolower($1) }')"
	elif command -v shasum >/dev/null 2>&1; then
		actual="$(shasum -a 256 "$archive" | awk '{ print tolower($1) }')"
	else
		die "sha256sum or shasum is required to verify the download"
	fi

	if [ "$actual" != "$expected" ]; then
		die "sha256 mismatch for $asset: expected $expected, got $actual"
	fi
}

installed_version() {
	local binary="$INSTALL_DIR/gowork"
	local output=""

	[ -x "$binary" ] || return 1
	output="$("$binary" --version 2>/dev/null)" || return 1
	output="$(printf '%s\n' "$output" | tail -n 1)"
	output="${output##* }"
	[ -n "$output" ] || return 1
	printf '%s\n' "$output"
}

install_bundle() {
	local source="$1"
	local parent
	local backup=""

	parent="$(dirname "$INSTALL_DIR")"
	mkdir -p "$parent" || die "could not create $parent"

	if [ -e "$INSTALL_DIR" ] || [ -L "$INSTALL_DIR" ]; then
		backup="${INSTALL_DIR}.old.$$"
		rm -rf "$backup"
		if ! mv "$INSTALL_DIR" "$backup"; then
			die "could not move the existing installation aside"
		fi
	fi

	if ! mv "$source" "$INSTALL_DIR"; then
		if [ -n "$backup" ]; then
			rm -rf "$INSTALL_DIR"
			mv "$backup" "$INSTALL_DIR" || warn "could not restore the previous installation from $backup"
		fi
		die "could not move the new installation into $INSTALL_DIR"
	fi

	if [ -n "$backup" ]; then
		rm -rf "$backup"
	fi
	say "Installed to $INSTALL_DIR"
}

link_binary() {
	local link="$BIN_DIR/gowork"
	local target="$INSTALL_DIR/gowork"
	local current=""

	mkdir -p "$BIN_DIR" || die "could not create $BIN_DIR"

	if [ -L "$link" ]; then
		current="$(readlink "$link" 2>/dev/null || true)"
		if [ "$current" = "$target" ]; then
			say "Launcher already points to $target"
			return 0
		fi
		if ! ln -sfn "$target" "$link"; then
			die "could not update $link"
		fi
	elif [ -e "$link" ]; then
		warn "not replacing $link because it is not a symlink"
		warn "move it aside or put $BIN_DIR on PATH yourself"
		return 0
	else
		if ! ln -s "$target" "$link"; then
			die "could not create $link"
		fi
	fi
	say "Linked $link -> $target"
}

shell_config() {
	local shell_name="$1"
	local config=""

	case "$shell_name" in
	zsh)
		config="$HOME/.zshrc"
		;;
	bash)
		if [ -f "$HOME/.bashrc" ]; then
			config="$HOME/.bashrc"
		elif [ -f "$HOME/.bash_profile" ]; then
			config="$HOME/.bash_profile"
		else
			config="$HOME/.bashrc"
		fi
		;;
	fish)
		config="$HOME/.config/fish/config.fish"
		;;
	*)
		if [ -f "$HOME/.profile" ]; then
			config="$HOME/.profile"
		elif [ -f "$HOME/.bashrc" ]; then
			config="$HOME/.bashrc"
		else
			config="$HOME/.profile"
		fi
		;;
	esac
	printf '%s\n' "$config"
}

shell_quote() {
	local value="$1"
	value="${value//\'/\'\\\'\'}"
	printf "'%s'" "$value"
}

remove_path_block() {
	local file="$1"
	local tmp=""

	[ -f "$file" ] || return 1
	grep -Fq "$PATH_START" "$file" || return 1

	tmp="${file}.gowork.$$"
	awk -v start="$PATH_START" -v end="$PATH_END" '
		$0 == start { skip = 1; next }
		$0 == end { skip = 0; next }
		skip != 1 { print }
	' "$file" >"$tmp" || {
		rm -f "$tmp"
		return 1
	}
	if ! cat "$tmp" >"$file"; then
		rm -f "$tmp"
		return 1
	fi
	rm -f "$tmp"
	return 0
}

add_path_block() {
	if [ "$MODIFY_PATH" -eq 0 ]; then
		say "Skipping PATH changes (--no-modify-path)."
		return 0
	fi

	case ":${PATH:-}:" in
	*":$BIN_DIR:"*)
		say "$BIN_DIR is already on PATH."
		return 0
		;;
	esac

	local shell_name
	local config
	local quoted

	shell_name="$(basename "${SHELL:-}")"
	config="$(shell_config "$shell_name")"
	quoted="$(shell_quote "$BIN_DIR")"

	mkdir -p "$(dirname "$config")" 2>/dev/null || true
	if [ -f "$config" ] && remove_path_block "$config"; then
		say "Updated the GoWork PATH block in $config"
	fi

	if [ "$shell_name" = "fish" ]; then
		if ! {
			printf '\n%s\n' "$PATH_START"
			printf '%s\n' "# Added by the GoWork installer. Remove this block to undo the PATH change."
			printf 'if not contains %s $PATH\n' "$quoted"
			printf '    set -gx PATH %s $PATH\n' "$quoted"
			printf 'end\n'
			printf '%s\n' "$PATH_END"
		} >>"$config"; then
			warn "could not update $config; add $BIN_DIR to PATH yourself"
			return 0
		fi
	else
		if ! {
			printf '\n%s\n' "$PATH_START"
			printf '%s\n' "# Added by the GoWork installer. Remove this block to undo the PATH change."
			printf 'export PATH=%s:"$PATH"\n' "$quoted"
			printf '%s\n' "$PATH_END"
		} >>"$config"; then
			warn "could not update $config; add $BIN_DIR to PATH yourself"
			return 0
		fi
	fi

	say "Added $BIN_DIR to PATH in $config"
	say "Open a new terminal (or source $config) to use the gowork command."
}

print_install_summary() {
	local label="$1"
	local actual=""

	actual="$(installed_version 2>/dev/null || true)"

	say ""
	say "GoWork is ready."
	if [ -n "$actual" ]; then
		say "  Version: $actual"
	else
		say "  Version: $label"
	fi
	say "  Install: $INSTALL_DIR"
	say "  Command: $BIN_DIR/gowork"
	say "Project files and databases stay in the project you open."
}

install_release() {
	detect_platform
	detect_downloader

	local asset="gowork_${OS}_${ARCH}.tar.gz"
	local base=""
	local label="latest"
	local archive=""
	local checks=""
	local staging=""

	if [ "$VERSION" = "latest" ]; then
		base="${RELEASES_URL}/latest/download"
	else
		base="${RELEASES_URL}/download/${VERSION}"
		label="$VERSION"
	fi

	TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/gowork-install.XXXXXX")"
	archive="${TMP_DIR}/${asset}"
	checks="${TMP_DIR}/checksums.txt"
	staging="${TMP_DIR}/extract"

	say "Downloading GoWork ($label) for ${OS}/${ARCH}..."
	download_file "${base}/${asset}" "$archive" || die "failed to download ${base}/${asset}"
	download_file "${base}/checksums.txt" "$checks" || die "failed to download ${base}/checksums.txt"

	say "Verifying checksum..."
	verify_checksum "$archive" "$checks" "$asset"

	mkdir -p "$staging"
	tar -xzf "$archive" -C "$staging" || die "failed to extract $asset"
	[ -f "$staging/gowork" ] || die "release archive did not contain a gowork binary"
	chmod +x "$staging/gowork"

	say "Installing GoWork to $INSTALL_DIR..."
	install_bundle "$staging"
	link_binary
	add_path_block
	print_install_summary "$label"
}

update_release() {
	detect_platform
	detect_downloader

	local current=""
	local target="$VERSION"

	if ! current="$(installed_version)"; then
		die "GoWork is not installed at $INSTALL_DIR; run the installer without --update first"
	fi

	if [ "$target" = "latest" ]; then
		if target="$(latest_tag)"; then
			:
		else
			warn "could not determine the latest release tag; installing the latest build"
			target=""
		fi
	fi

	if [ -n "$target" ] && [ "$current" = "$target" ]; then
		say "GoWork is already up to date ($current)."
		return 0
	fi

	if [ -n "$target" ]; then
		say "Updating GoWork $current -> $target"
		VERSION="$target"
	else
		say "Updating GoWork $current -> latest"
	fi

	install_release
}

uninstall() {
	local link="$BIN_DIR/gowork"
	local target="$INSTALL_DIR/gowork"
	local current=""
	local config=""
	local found=0

	say "Uninstalling GoWork..."

	if [ -L "$link" ]; then
		current="$(readlink "$link" 2>/dev/null || true)"
		if [ "$current" = "$target" ] || [ "${current%/*}" = "$INSTALL_DIR" ]; then
			rm -f "$link"
			say "Removed launcher $link"
			found=1
		else
			warn "left $link in place because it points to $current"
		fi
	elif [ -e "$link" ]; then
		warn "left $link in place because it is not a symlink"
	fi

	if [ -d "$INSTALL_DIR" ] || [ -L "$INSTALL_DIR" ]; then
		rm -rf "$INSTALL_DIR"
		say "Removed $INSTALL_DIR"
		found=1
	else
		say "No installation found at $INSTALL_DIR."
	fi

	for config in \
		"$HOME/.bashrc" \
		"$HOME/.bash_profile" \
		"$HOME/.bash_login" \
		"$HOME/.profile" \
		"$HOME/.zshrc" \
		"$HOME/.zshenv" \
		"$HOME/.config/fish/config.fish"
	do
		if remove_path_block "$config"; then
			say "Removed the GoWork PATH block from $config"
			found=1
		fi
	done

	if [ "$found" -eq 0 ]; then
		say "Nothing to remove."
	fi

	say "Project files and databases (for example Db/gowork.db) were not touched."
}

cleanup() {
	if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ]; then
		rm -rf "$TMP_DIR"
	fi
}

main() {
	parse_args "$@"

	[ -n "${HOME:-}" ] || die "HOME is not set; set it or pass --dir explicitly"

	VERSION="$(normalize_version "$VERSION")"

	if [ -z "$INSTALL_DIR" ]; then
		INSTALL_DIR="$(default_install_dir)"
	else
		INSTALL_DIR="$(absolute_dir "$(expand_dir "$INSTALL_DIR")")"
	fi

	BIN_DIR="${XDG_BIN_HOME:-$HOME/.local/bin}"
	BIN_DIR="$(absolute_dir "$(expand_dir "$BIN_DIR")")"

	[ "$INSTALL_DIR" != "/" ] || die "refusing to use / as the install directory"

	trap cleanup EXIT

	case "$MODE" in
	install)
		install_release
		;;
	update)
		update_release
		;;
	uninstall)
		uninstall
		;;
	esac
}

main "$@"
