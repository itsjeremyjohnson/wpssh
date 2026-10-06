package cmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
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

// dumpTrailer starts the last line of every complete mysqldump/mariadb-dump
// output; a dump whose final non-empty line does not start with it was cut off.
const dumpTrailer = "-- Dump completed"

// dumpTailSize bounds the bytes kept to find a dump's final line. The
// mysqldump trailer line is about 40 bytes.
const dumpTailSize = 4096

// unsafeFilenameChars matches characters replaced in dump names.
var unsafeFilenameChars = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// savedDump describes a database dump written on the operator machine.
type savedDump struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// defaultDumpPath returns
// ${WPGO_LOCAL_BACKUP_DIR:-$HOME/wpgo-backups}/<alias>/<alias>_DB_<desc>_<ts>_<rand>.sql
// on the operator machine, creating the base and alias dirs with mode 0700.
// <ts> has millisecond precision and <rand> is 6 random hex digits, so
// concurrent backups of one site get distinct names.
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
	suffix := make([]byte, 3)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("dump name suffix: %w", err)
	}
	name := fmt.Sprintf("%s_DB_%s_%s%03d_%x.sql", safeAlias, unsafeFilenameChars.ReplaceAllString(desc, "_"),
		now.Format("20060102_150405_"), now.Nanosecond()/int(time.Millisecond), suffix)
	return filepath.Join(dir, name), nil
}

// saveDump writes the stdout of run to path on the operator machine.
//
// It streams into a 0600 temp file next to path, fsyncs it and hard-links it
// to path, which fails if path already exists, even when another process
// created it during the stream. If run fails or verifyDump rejects the dump,
// the temp file is removed and an error is returned. An existing file at path
// is never replaced. If the temp file cannot be removed, the returned error
// names it, even when the dump was published.
func saveDump(path string, run func(io.Writer) (internalssh.ExecResult, error)) (_ savedDump, err error) {
	if _, err := os.Lstat(path); err == nil {
		return savedDump{}, fmt.Errorf("refusing to overwrite existing file %s", path)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".partial-*")
	if err != nil {
		return savedDump{}, fmt.Errorf("create local dump: %w", err)
	}
	defer func() {
		_ = tmp.Close()
		rerr := os.Remove(tmp.Name())
		switch {
		case rerr == nil || errors.Is(rerr, fs.ErrNotExist):
		case err == nil:
			err = fmt.Errorf("saved dump to %s, but could not remove temp file %s: %w", path, tmp.Name(), rerr)
		default:
			err = errors.Join(err, fmt.Errorf("could not remove partial dump %s: %w", tmp.Name(), rerr))
		}
	}()

	hash := sha256.New()
	tail := &tailWriter{}
	result, err := run(io.MultiWriter(tmp, hash, tail))
	if err != nil {
		return savedDump{}, fmt.Errorf("stream dump: %w", err)
	}
	if err := verifyDump(result, tail); err != nil {
		return savedDump{}, err
	}
	if err := tmp.Sync(); err != nil {
		return savedDump{}, fmt.Errorf("fsync local dump: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return savedDump{}, fmt.Errorf("close local dump: %w", err)
	}
	if err := os.Link(tmp.Name(), path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return savedDump{}, fmt.Errorf("refusing to overwrite existing file %s", path)
		}
		return savedDump{}, fmt.Errorf("publish local dump: %w", err)
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return savedDump{Path: abs, Bytes: tail.n, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

// verifyDump rejects a `wp db export -` stream that exited non-zero, was
// empty, or whose final non-empty line does not start with dumpTrailer.
func verifyDump(result internalssh.ExecResult, tail *tailWriter) error {
	if result.ExitCode != 0 {
		return fmt.Errorf("wp db export exited %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	if tail.n == 0 {
		return errors.New("wp db export produced an empty dump")
	}
	if !tail.endsWithTrailer() {
		return fmt.Errorf("dump is truncated: last line after %d bytes does not start with %q", tail.n, dumpTrailer)
	}
	return nil
}

// tailWriter counts the bytes written and keeps the last dumpTailSize of them.
type tailWriter struct {
	n   int64
	buf []byte
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	w.buf = append(w.buf, p...)
	if excess := len(w.buf) - dumpTailSize; excess > 0 {
		w.buf = w.buf[:copy(w.buf, w.buf[excess:])]
	}
	return len(p), nil
}

// endsWithTrailer reports whether the final non-empty line starts with
// dumpTrailer. A final line longer than the kept tail is rejected.
func (w *tailWriter) endsWithTrailer() bool {
	t := bytes.TrimRight(w.buf, " \t\r\n")
	i := bytes.LastIndexByte(t, '\n')
	if i < 0 && w.n > int64(len(w.buf)) {
		return false
	}
	return bytes.HasPrefix(t[i+1:], []byte(dumpTrailer))
}

// exportToLocal streams `wp db export -` from site into path on the operator
// machine. Nothing is written on the server. Ctrl-C cancels the stream and
// removes the partial file.
func exportToLocal(rc *RunContext, site *registry.Site, path string) (savedDump, error) {
	remoteCmd := dbExportStdout(site.WPPath)
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

// exportToStdout streams `wp db export -` from site to rc.Stdout with the
// checks of writeDump.
func exportToStdout(rc *RunContext, site *registry.Site) error {
	remoteCmd := dbExportStdout(site.WPPath)
	if rc.Globals.DryRun {
		fmt.Fprintf(rc.Stderr, "[dry-run] %s\n", remoteCmd)
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return writeDump(rc.Stdout, func(w io.Writer) (internalssh.ExecResult, error) {
		return rc.ExecWPStream(ctx, site, remoteCmd, w)
	})
}

// writeDump copies the stdout of run to w and applies verifyDump, keeping
// only a bounded tail in memory. It cannot take back bytes already written,
// so on error w holds a partial dump.
func writeDump(w io.Writer, run func(io.Writer) (internalssh.ExecResult, error)) error {
	tail := &tailWriter{}
	result, err := run(io.MultiWriter(w, tail))
	if err != nil {
		return fmt.Errorf("stream dump: %w", err)
	}
	return verifyDump(result, tail)
}

// dbExportStdout is the remote command that writes the dump to stdout and
// nothing on the server.
func dbExportStdout(wpPath string) string {
	return wpcli.New("db", "export").Arg("-").Build(wpPath)
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
