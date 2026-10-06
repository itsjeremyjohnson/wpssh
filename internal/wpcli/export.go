package wpcli

import (
	"path"
	"strconv"
	"strings"
)

// backupDirExpr is the remote backup directory: $WPGO_BACKUP_DIR, or
// $HOME/backups/wpgo when unset.
const backupDirExpr = `"${WPGO_BACKUP_DIR:-$HOME/backups/wpgo}"`

// WebrootRefusedExit is the remote exit status when an export target is
// inside a web-served directory.
const WebrootRefusedExit = 64

// DBExport builds the remote command for `wp db export` that keeps dumps out
// of the docroot.
//
// file "" writes defaultName in the backup dir; a relative file resolves under
// the backup dir (not the WP path); "-" streams to stdout and writes nothing.
// The remote shell resolves the target with `realpath -m` and exits 64 if it is
// inside the WP path, $HOME/public_html or $HOME/www. Otherwise it creates the
// backup dir 0700, exports with umask 077 and chmods the dump 0600.
func DBExport(wpPath, file, defaultName string) string {
	if file == "-" {
		return New("db", "export").Arg(file).Build(wpPath)
	}

	var target string
	switch {
	case file == "":
		target = `"$WPGO_DIR"/` + shellEscape(defaultName)
	case path.IsAbs(file), file == "~", strings.HasPrefix(file, "~/"):
		target = pathEscape(file)
	default:
		target = `"$WPGO_DIR"/` + shellEscape(file)
	}

	return strings.Join([]string{
		"cd " + pathEscape(wpPath),
		"umask 077",
		`WPGO_WEBROOT="$(pwd -P)"`,
		`WPGO_DIR="$(realpath -m -- ` + backupDirExpr + `)"`,
		`WPGO_DEST="$(realpath -m -- ` + target + `)"`,
		`for WPGO_ROOT in "$WPGO_WEBROOT" "$(realpath -m -- "$HOME/public_html")" "$(realpath -m -- "$HOME/www")"; do ` +
			`case "$WPGO_DEST/" in "$WPGO_ROOT/"*) ` +
			`echo "refusing to write database export inside web root $WPGO_ROOT: $WPGO_DEST (use a path outside it or set WPGO_BACKUP_DIR)" >&2; exit ` + strconv.Itoa(WebrootRefusedExit) + `;; ` +
			`esac; done`,
		`mkdir -p -m 0700 -- "$WPGO_DIR"`,
		`chmod 0700 -- "$WPGO_DIR"`,
		`wp db export "$WPGO_DEST"`,
		`chmod 0600 -- "$WPGO_DEST"`,
	}, " && ")
}
