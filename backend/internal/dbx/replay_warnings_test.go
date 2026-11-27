package dbx

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// A replay hands on what the server warns about and nothing below it.
func TestAReplayPassesOnWarningsAlone(t *testing.T) {
	for _, c := range []struct {
		notice pgconn.Notice
		want   bool
	}{
		{pgconn.Notice{SeverityUnlocalized: "WARNING", Severity: "AVERTISSEMENT"}, true},
		{pgconn.Notice{SeverityUnlocalized: "NOTICE", Severity: "WARNING"}, false},
		{pgconn.Notice{SeverityUnlocalized: "INFO"}, false},
		{pgconn.Notice{SeverityUnlocalized: "DEBUG"}, false},
		{pgconn.Notice{Severity: "WARNING"}, true},
		{pgconn.Notice{Severity: "NOTICE"}, false},
	} {
		if got := replayWarning(&c.notice); got != c.want {
			t.Errorf("%+v: %v, want %v", c.notice, got, c.want)
		}
	}
}

// Against a real server: a script whose DROP … IF EXISTS raises a NOTICE and
// whose block raises a WARNING hands Warnings the warning alone, in the
// words psql would print it with, and its COPY rows still arrive.
func TestLiveAReplayedScriptHandsOnItsWarningsAndNotItsNotices(t *testing.T) {
	const fallback = "postgres://jdtest:jdtest@127.0.0.1:5432/jdtest?sslmode=disable"
	// Skips unless a server answers.
	liveSQL(t, DriverPostgres, "JD_TEST_POSTGRES_DSN", fallback)
	table := "jd_replay_" + strings.ToLower(rand.Text()[:10])
	script := strings.Join([]string{
		"SET standard_conforming_strings = on;",
		"DROP TABLE IF EXISTS " + table + ";",
		"CREATE TABLE " + table + " (id integer);",
		// Rows handed over on the connection the warnings are read from.
		"COPY " + table + " (id) FROM stdin;",
		"1",
		"2",
		`\.`,
		"DO $$ BEGIN RAISE WARNING 'jd-replay-warning'; END $$;",
		"DROP TABLE " + table + ";",
		"",
	}, "\n")
	path := filepath.Join(t.TempDir(), "warnings.sql")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var warnings, progress []string
	if _, err := RestoreWith(ctx, DriverPostgres, liveDSN(t, "JD_TEST_POSTGRES_DSN", fallback), path, RestoreOptions{
		Warnings: func(line string) { warnings = append(warnings, line) },
		Progress: func(line string) { progress = append(progress, line) },
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(warnings, []string{"WARNING: jd-replay-warning"}) {
		t.Fatalf("warnings = %q", warnings)
	}
	if len(progress) == 0 || progress[len(progress)-1] != "6 statements applied, 2 rows loaded" {
		t.Fatalf("progress = %q", progress)
	}
}
