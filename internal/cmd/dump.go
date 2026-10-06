package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/builtbyrobben/wpssh/internal/registry"
	internalssh "github.com/builtbyrobben/wpssh/internal/ssh"
	"github.com/builtbyrobben/wpssh/internal/wpcli"
)

// dumpTrailer ends every complete mysqldump/mariadb-dump output; a dump
// without it in its last bytes was cut off.
const dumpTrailer = "-- Dump completed"

// unsafeFilenameChars matches characters replaced in dump names, mirroring
// full-backup.sh.
var unsafeFilenameChars = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// savedDump describes a database dump written on the operator machine.
type savedDump struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// defaultDumpPath returns
// ${WPGO_LOCAL_BACKUP_DIR:-$HOME/wpgo-backups}/<alias>/<alias>_DB_<desc>_<ts>.sql
// on the operator machine, creating the base and alias dirs with mode 0700.
func defaultDumpPath(alias, desc string, now time.Time) (string, error) {
	base := os.Getenv("WPGO_LOCAL_BACKUP_DIR")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("local backup dir: %w", err)
		}
		base = filepath.Join(home, "wpgo-backups")
	}
	safeAlias := unsafeFilenameChars.ReplaceAllString(alias, "_")
	dir := filepath.Join(base, safeAlias)
	for _, d := range []string{base, dir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return "", fmt.Errorf("create local backup dir: %w", err)
		}
		if err := os.Chmod(d, 0o700); err != nil {
			return "", fmt.Errorf("chmod local backup dir: %w", err)
		}
	}
	name := fmt.Sprintf("%s_DB_%s_%s.sql", safeAlias, unsafeFilenameChars.ReplaceAllString(desc, "_"), now.Format("20060102_150405"))
	return filepath.Join(dir, name), nil
}

// saveDump writes the stdout of run to path on the operator machine.
//
// It streams into a 0600 temp file next to path, fsyncs it and renames it into
// place. If run fails, the remote command exits non-zero, or the dump is empty
// or lacks the mysqldump trailer, the temp file is removed and an error is
// returned. An existing file at path is never overwritten.
func saveDump(path string, run func(io.Writer) (internalssh.ExecResult, error)) (savedDump, error) {
	if _, err := os.Lstat(path); err == nil {
		return savedDump{}, fmt.Errorf("refusing to overwrite existing file %s", path)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".partial-*")
	if err != nil {
		return savedDump{}, fmt.Errorf("create local dump: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()

	hash := sha256.New()
	counter := &countingWriter{}
	result, err := run(io.MultiWriter(tmp, hash, counter))
	if err != nil {
		return savedDump{}, fmt.Errorf("stream dump: %w", err)
	}
	if result.ExitCode != 0 {
		return savedDump{}, fmt.Errorf("wp db export exited %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	if counter.n == 0 {
		return savedDump{}, errors.New("wp db export produced an empty dump")
	}
	tail := make([]byte, min(counter.n, 512))
	if _, err := tmp.ReadAt(tail, counter.n-int64(len(tail))); err != nil {
		return savedDump{}, fmt.Errorf("read dump tail: %w", err)
	}
	if !bytes.Contains(tail, []byte(dumpTrailer)) {
		return savedDump{}, fmt.Errorf("dump is truncated: no %q trailer after %d bytes", dumpTrailer, counter.n)
	}
	if err := tmp.Sync(); err != nil {
		return savedDump{}, fmt.Errorf("fsync local dump: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return savedDump{}, fmt.Errorf("close local dump: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return savedDump{}, fmt.Errorf("rename local dump: %w", err)
	}
	committed = true
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return savedDump{Path: abs, Bytes: counter.n, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

type countingWriter struct{ n int64 }

func (w *countingWriter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	return len(p), nil
}

// exportToLocal streams `wp db export -` from site into path on the operator
// machine. Nothing is written on the server. Ctrl-C cancels the stream and
// removes the partial file.
func exportToLocal(rc *RunContext, site *registry.Site, path string) (savedDump, error) {
	remoteCmd := wpcli.DBExport(site.WPPath, "-", "")
	if rc.Globals.DryRun {
		fmt.Fprintf(rc.Stderr, "[dry-run] %s > %s (local)\n", remoteCmd, path)
		return savedDump{Path: path}, nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return saveDump(path, func(w io.Writer) (internalssh.ExecResult, error) {
		return rc.ExecWPStream(ctx, site, remoteCmd, w)
	})
}

// printDump reports a local dump as JSON or text.
func printDump(rc *RunContext, label string, d savedDump) error {
	if rc.Globals.DryRun {
		return nil
	}
	if rc.Globals.JSON {
		out, err := json.MarshalIndent(struct {
			Status string `json:"status"`
			savedDump
		}{"ok", d}, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(rc.Stdout, string(out))
		return nil
	}
	fmt.Fprintf(rc.Stdout, "%-14s%s\n", label, d.Path)
	fmt.Fprintf(rc.Stdout, "%-14s%d bytes\n", "Size:", d.Bytes)
	fmt.Fprintf(rc.Stdout, "%-14s%s\n", "SHA256:", d.SHA256)
	return nil
}
