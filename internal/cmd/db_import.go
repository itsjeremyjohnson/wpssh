package cmd

import (
	"context"
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

	"github.com/builtbyrobben/wpssh/internal/cache"
	"github.com/builtbyrobben/wpssh/internal/registry"
	internalssh "github.com/builtbyrobben/wpssh/internal/ssh"
	"github.com/builtbyrobben/wpssh/internal/wpcli"
)

type DBImportCmd struct {
	File string `arg:"" help:"Dump file on this machine, or '-' for stdin. wpgo refuses dumps with mysql client commands, backs up the current database to this machine, then streams the dump into 'wp db import -'. Nothing is written on the server. Requires --yes --ack-destructive."`
}

// scpPath matches host:path, the form scp and rsync use for a server file.
var scpPath = regexp.MustCompile(`^[A-Za-z0-9._@-]+:`)

func (c *DBImportCmd) Run(g *Globals) error {
	src, name, err := openLocalDump(c.File, os.Stdin)
	if err != nil {
		return err
	}
	defer src.Close()

	rc, err := NewRunContext(g)
	if err != nil {
		return err
	}
	defer rc.Close()
	site, err := rc.ResolveSite()
	if err != nil {
		return err
	}
	backupPath, err := defaultDumpPath(site.Alias, "pre-import", time.Now())
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	res, err := restoreDB(ctx, restoreRequest{
		Site:       site.Alias,
		Source:     src,
		SourceName: name,
		BackupPath: backupPath,
		Confirmed:  g.Yes && g.AckDestructive,
		DryRun:     g.DryRun,
		Stderr:     rc.Stderr,
	}, &siteDatabase{rc: rc, site: site})
	if res.Imported {
		// DB import can change everything — invalidate all categories.
		rc.CacheInvalidate(site.Alias, []string{
			cache.CategoryPlugins, cache.CategoryThemes, cache.CategoryCore,
			cache.CategoryUsers, cache.CategoryOptions, cache.CategorySnapshot,
		})
		fmt.Fprint(rc.Stdout, res.Output)
	}
	return err
}

// openLocalDump opens the dump to import: a regular file on this machine, or
// stdin for "-". It refuses anything that names a file on the server.
func openLocalDump(path string, stdin io.ReadCloser) (io.ReadCloser, string, error) {
	if path == "-" {
		return stdin, "stdin", nil
	}
	const localOnly = "wpgo db import reads a dump on this machine (or '-' for stdin) and streams it to the server; it never imports a file stored on the server"
	fi, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist) && scpPath.MatchString(path):
		return nil, "", fmt.Errorf("%q looks like a server path: %s", path, localOnly)
	case errors.Is(err, fs.ErrNotExist):
		return nil, "", fmt.Errorf("no file %q on this machine: %s", path, localOnly)
	case err != nil:
		return nil, "", fmt.Errorf("dump file: %w", err)
	case !fi.Mode().IsRegular():
		return nil, "", fmt.Errorf("%q is not a regular file; pipe it to 'wpgo db import -' instead", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, "", fmt.Errorf("open dump: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return f, abs, nil
}

// restoreTarget is the site whose database a restore replaces.
type restoreTarget interface {
	// DatabaseName returns the name of the database the import writes to.
	DatabaseName(ctx context.Context) (string, error)
	// Backup saves the current database to path on this machine.
	Backup(ctx context.Context, path string) (savedDump, error)
	// Import streams dump into `wp db import -`.
	Import(ctx context.Context, dump io.Reader) (internalssh.ExecResult, error)
}

type restoreRequest struct {
	Site       string
	Source     io.Reader
	SourceName string
	// BackupPath is where the pre-restore backup is written. The dump is
	// staged next to it while it is scanned.
	BackupPath string
	Confirmed  bool
	DryRun     bool
	Stderr     io.Writer
}

type restoreResult struct {
	Backup   savedDump
	Imported bool
	Output   string
}

// importPreamble goes before the dump in the import stream and sets the
// session state scanDump assumes: an sql_mode without NO_BACKSLASH_ESCAPES or
// ANSI_QUOTES, and a character set in which a backslash byte is always a
// backslash. wp db import keeps the server's default sql_mode, under which
// mysql could end a quote where scanDump does not. The mode is the one
// mysqldump and mariadb-dump headers set.
const importPreamble = "SET SESSION sql_mode='NO_AUTO_VALUE_ON_ZERO';\nSET NAMES utf8mb4;\n"

// restoreDB replaces the target's database with the dump in req.Source.
//
// It copies the dump into a private staging file while scanning it with
// scanDump, so the bytes imported are the bytes checked. It then shows the
// site, database and backup path, and stops unless req.Confirmed. Before
// importing it saves a backup of the current database to req.BackupPath and
// stops if that fails or is empty. The import streams importPreamble, then
// the staged dump.
func restoreDB(ctx context.Context, req restoreRequest, target restoreTarget) (restoreResult, error) {
	var res restoreResult
	database := ""
	if !req.DryRun {
		var err error
		if database, err = target.DatabaseName(ctx); err != nil {
			return res, fmt.Errorf("read the site's DB_NAME: %w", err)
		}
	}

	staged, size, err := stageDump(req.Source, filepath.Dir(req.BackupPath), database)
	if err != nil {
		return res, err
	}
	defer func() {
		staged.Close()
		_ = os.Remove(staged.Name())
	}()

	shownDB := database
	if req.DryRun {
		shownDB = "(not read in dry-run)"
	}
	fmt.Fprintf(req.Stderr, "%-20s%s\n", "Site:", req.Site)
	fmt.Fprintf(req.Stderr, "%-20s%s\n", "Database:", shownDB)
	fmt.Fprintf(req.Stderr, "%-20s%s (%d bytes, no mysql client commands)\n", "Dump:", req.SourceName, size)
	fmt.Fprintf(req.Stderr, "%-20s%s\n", "Pre-restore backup:", req.BackupPath)
	if req.DryRun {
		fmt.Fprintf(req.Stderr, "[dry-run] would back up the database to the path above, then stream the dump into wp db import -\n")
		return res, nil
	}
	if !req.Confirmed {
		return res, fmt.Errorf("db import replaces database %s on %s: rerun with --yes --ack-destructive", database, req.Site)
	}

	backup, err := target.Backup(ctx, req.BackupPath)
	if err != nil {
		return res, fmt.Errorf("pre-restore backup failed, nothing imported: %w", err)
	}
	if backup.Bytes == 0 {
		return res, fmt.Errorf("pre-restore backup %s is empty, nothing imported", backup.Path)
	}
	res.Backup = backup
	fmt.Fprintf(req.Stderr, "%-20s%s (%d bytes, sha256 %s)\n", "Backup saved:", backup.Path, backup.Bytes, backup.SHA256)

	if _, err := staged.Seek(0, io.SeekStart); err != nil {
		return res, fmt.Errorf("rewind staged dump, nothing imported: %w", err)
	}
	result, err := target.Import(ctx, io.MultiReader(strings.NewReader(importPreamble), staged))
	res.Imported = true
	res.Output = result.Stdout
	if err != nil {
		return res, fmt.Errorf("wp db import: %w; the database may be partly imported, restore it from %s", err, backup.Path)
	}
	if result.ExitCode != 0 {
		return res, fmt.Errorf("wp db import exited %d: %s; the database may be partly imported, restore it from %s",
			result.ExitCode, strings.TrimSpace(result.Stderr), backup.Path)
	}
	return res, nil
}

// stageDump copies src into a new 0600 file in dir while scanning it, and
// returns the file and its size. The caller closes and removes the file.
func stageDump(src io.Reader, dir, database string) (_ *os.File, _ int64, err error) {
	f, err := os.CreateTemp(dir, ".import-*.sql")
	if err != nil {
		return nil, 0, fmt.Errorf("stage dump: %w", err)
	}
	defer func() {
		if err != nil {
			f.Close()
			_ = os.Remove(f.Name())
		}
	}()
	counter := &countWriter{w: f}
	if err := scanDump(io.TeeReader(src, counter), database); err != nil {
		if counter.err != nil {
			return nil, 0, fmt.Errorf("stage dump: %w", counter.err)
		}
		return nil, 0, err
	}
	if counter.n == 0 {
		return nil, 0, errors.New("refusing dump: it is empty")
	}
	return f, counter.n, nil
}

// countWriter counts the bytes written to w and keeps its first error.
type countWriter struct {
	w   io.Writer
	n   int64
	err error
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	if err != nil && c.err == nil {
		c.err = err
	}
	return n, err
}

// siteDatabase is the restoreTarget for a registered site.
type siteDatabase struct {
	rc   *RunContext
	site *registry.Site
}

func (d *siteDatabase) DatabaseName(ctx context.Context) (string, error) {
	result, err := d.rc.ExecWP(ctx, d.site, wpcli.New("config", "get").Arg("DB_NAME").Build(d.site.WPPath))
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(result.Stdout)
	if result.ExitCode != 0 || name == "" {
		return "", fmt.Errorf("wp config get DB_NAME exited %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return name, nil
}

func (d *siteDatabase) Backup(ctx context.Context, path string) (savedDump, error) {
	return exportToLocal(ctx, d.rc, d.site, path)
}

func (d *siteDatabase) Import(ctx context.Context, dump io.Reader) (internalssh.ExecResult, error) {
	return d.rc.ExecWPStdin(ctx, d.site, dbImportStdin(d.site.WPPath), dump)
}

// dbImportStdin is the remote command that imports a dump read from stdin.
// wp db import passes --default-character-set to mysql, so the client reads
// the dump as utf8mb4, the character set importPreamble sets on the server.
func dbImportStdin(wpPath string) string {
	return wpcli.New("db", "import").Arg("-").Flag("default-character-set", "utf8mb4").Build(wpPath)
}
