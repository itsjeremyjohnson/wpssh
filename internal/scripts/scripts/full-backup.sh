#!/bin/bash
# full-backup.sh — Named database backup for wpgo.
# Executed via SSH stdin from the WordPress path: bash -s -- <client_name> <description>
# Writes to ${WPGO_BACKUP_DIR:-$HOME/backups/wpgo} (dir 0700, file 0600) and
# refuses (exit 64) any target inside the WordPress path, ~/public_html or ~/www.
# Output: JSON with status, filename, path.

set -e
umask 077

CLIENT="${1:-Site}"
DESC="${2:-Manual}"
DATE=$(date +%Y%m%d_%H%M%S)

# Sanitize inputs: replace spaces/special chars with underscores.
CLIENT_SAFE=$(echo "$CLIENT" | sed 's/[^a-zA-Z0-9_-]/_/g')
DESC_SAFE=$(echo "$DESC" | sed 's/[^a-zA-Z0-9_-]/_/g')

FILENAME="${CLIENT_SAFE}_DB_${DESC_SAFE}_${DATE}.sql"

json_escape() {
    printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

fail() {
    cat <<ENDJSON
{
  "status": "error",
  "filename": "$FILENAME",
  "path": "$(json_escape "$2")",
  "size": "",
  "error": "$(json_escape "$3")"
}
ENDJSON
    exit "$1"
}

# Never write dumps into a web-served directory.
WEBROOT=$(pwd -P)
EXPORT_DIR=$(realpath -m -- "${WPGO_BACKUP_DIR:-$HOME/backups/wpgo}")
EXPORT_PATH="${EXPORT_DIR}/${FILENAME}"
for ROOT in "$WEBROOT" "$(realpath -m -- "$HOME/public_html")" "$(realpath -m -- "$HOME/www")"; do
    case "$EXPORT_PATH/" in
        "$ROOT/"*) fail 64 "$EXPORT_PATH" "refusing backup target inside web root $ROOT; set WPGO_BACKUP_DIR outside it" ;;
    esac
done

# umask 077 makes any parent dirs 0700 too.
# shellcheck disable=SC2174
mkdir -p -m 0700 -- "$EXPORT_DIR"
chmod 0700 -- "$EXPORT_DIR"

if wp db export "$EXPORT_PATH" 2>/dev/null; then
    chmod 0600 -- "$EXPORT_PATH"
    FILESIZE=$(du -h -- "$EXPORT_PATH" 2>/dev/null | cut -f1 || echo "unknown")

    cat <<ENDJSON
{
  "status": "ok",
  "filename": "$FILENAME",
  "path": "$(json_escape "$EXPORT_PATH")",
  "size": "$FILESIZE"
}
ENDJSON
else
    fail 1 "" "wp db export failed"
fi
