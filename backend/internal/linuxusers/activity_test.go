package linuxusers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var authLog = []string{
	// This host's own format.
	"2026-10-30T10:59:48.268018+01:00 web-01 sudo:   ubuntu : PWD=/home/ubuntu/Just-Dashboard ; USER=root ; COMMAND=/usr/bin/grep -h -E (useradd) /var/log/auth.log",
	"2026-10-30T11:00:01.000000+01:00 web-01 sudo: pam_unix(sudo:session): session opened for user root(uid=0) by (uid=1000)",
	"2026-10-30T11:01:00.000000+01:00 web-01 sudo:   ubuntu : TTY=pts/0 ; PWD=/home/my project ; USER=postgres ; ENV=FOO=bar ; COMMAND=/usr/bin/psql",
	"2026-10-30T11:02:00.000000+01:00 web-01 sudo:     zoro : 3 incorrect password attempts ; TTY=pts/1 ; PWD=/home/zoro ; USER=root ; COMMAND=/bin/ls",
	"2026-10-30T11:03:00.000000+01:00 web-01 sudo:     zoro : user NOT in sudoers ; TTY=pts/1 ; PWD=/home/zoro ; USER=root ; COMMAND=/bin/cat /etc/shadow",
	"2026-10-30T11:04:00.000000+01:00 web-01 sshd-session[1234]: Accepted publickey for ubuntu from 203.0.113.9 port 50022 ssh2: ED25519 SHA256:abcDEF+/123",
	"2026-10-30T11:05:00.000000+01:00 web-01 sshd[1234]: Accepted password for root from 198.51.100.23 port 50022 ssh2",
	// The older syslog format.
	"Oct 30 09:00:00 web-01 sshd[99]: Accepted publickey for ubuntu from 198.51.100.1 port 1 ssh2: RSA SHA256:oldKey",
	"Oct 30 09:30:00 web-01 sudo[12]:   ubuntu : TTY=pts/0 ; PWD=/root ; USER=root ; COMMAND=/bin/id",
	"Oct 30 09:40:00 web-01 sshd[99]: Accepted publickey for ubuntu from 198.51.100.2 port 1 ssh2: RSA SHA256:oldKey",
	"Oct 30 09:45:00 web-01 sshd[99]: Accepted publickey for mira from 198.51.100.3 port 1 ssh2: RSA SHA256:miraKey",
	"not a log line",
	"",
}

func TestSudoHistoryReadsBothStampsAndNewestComesFirst(t *testing.T) {
	events := parseSudoHistory(authLog, "ubuntu", 0, fixedNow)
	if len(events) != 3 {
		t.Fatalf("events = %+v", events)
	}
	newest := events[0]
	if newest.RunAs != "postgres" || newest.Command != "/usr/bin/psql" || newest.Failed {
		t.Fatalf("a directory with a space or an ENV= lost the line: %+v", newest)
	}
	if !newest.At.Equal(time.Date(2026, 10, 30, 10, 1, 0, 0, time.UTC)) {
		t.Fatalf("at = %v", newest.At)
	}
	if got := events[1]; got.RunAs != "root" || !strings.HasPrefix(got.Command, "/usr/bin/grep -h") {
		t.Fatalf("no-TTY line = %+v", got)
	}
	if got := events[2]; got.Command != "/bin/id" {
		t.Fatalf("syslog line = %+v", got)
	}
	if got := parseSudoHistory(authLog, "ubuntu", 1, fixedNow); len(got) != 1 {
		t.Fatalf("the limit was ignored: %+v", got)
	}
}

func TestARefusedSudoSaysWhy(t *testing.T) {
	events := parseSudoHistory(authLog, "zoro", 0, fixedNow)
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	if !events[0].Failed || events[0].Reason != "user NOT in sudoers" {
		t.Fatalf("event = %+v", events[0])
	}
	if !events[1].Failed || events[1].Reason != "3 incorrect password attempts" || events[1].Command != "/bin/ls" {
		t.Fatalf("event = %+v", events[1])
	}
}

func TestKeyUseIsTheNewestSignInPerFingerprintForThatAccount(t *testing.T) {
	uses := keyUses(authLog, "ubuntu", fixedNow)
	if len(uses) != 2 {
		t.Fatalf("uses = %+v", uses)
	}
	if got := uses["SHA256:abcDEF+/123"]; got.from != "203.0.113.9" {
		t.Fatalf("got %+v", got)
	}
	if got := uses["SHA256:oldKey"]; got.from != "198.51.100.2" {
		t.Fatalf("the newer of two lines should win: %+v", got)
	}
	if _, ok := uses["SHA256:miraKey"]; ok {
		t.Fatal("another account's key was counted")
	}
}

func TestAStampWithNoYearIsTheMostRecentTimeItCouldMean(t *testing.T) {
	now := time.Date(2027, time.January, 2, 0, 0, 0, 0, time.Local)
	at, ok := logLineTime("Dec 31 23:00:00 host sshd[1]: x", now)
	if !ok || at.Year() != 2026 {
		t.Fatalf("at = %v, %v", at, ok)
	}
	if _, ok := logLineTime("garbage", now); ok {
		t.Fatal("read garbage")
	}
	if at, ok := logLineTime("2026-10-30T10:59:48+0100 host x", now); !ok || at.Hour() != 9 {
		t.Fatalf("journalctl's short-iso: %v, %v", at, ok)
	}
}

func TestATailReadStartsOnAWholeLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.log")
	content := strings.Repeat("x", 100) + "\nfirst whole line\nsecond\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, ok := tailLines(path, 30)
	if !ok || lines[0] != "first whole line" {
		t.Fatalf("lines = %q, %v", lines, ok)
	}
	if _, ok := tailLines(filepath.Join(t.TempDir(), "missing"), 30); ok {
		t.Fatal("a missing file read")
	}
}

func TestProcessesTreatNoneAsAnAnswer(t *testing.T) {
	s, fake, _ := hostAccounts(t)
	ctx := context.Background()
	fake.answer = func(_, name string, _ ...string) (string, error) {
		return "", errors.New("ps: exit status 1")
	}
	count, rows, err := s.Processes(ctx, "ubuntu", 5)
	if err != nil || count != 0 || len(rows) != 0 {
		t.Fatalf("count %d rows %v err %v", count, rows, err)
	}
	fake.answer = func(_, _ string, _ ...string) (string, error) {
		return "    10  0.5  1.0     60 bash\n    11 12.5  2.0    120 node server\n    12  3.0  0.1      5 tmux: server\n", nil
	}
	count, rows, err = s.Processes(ctx, "ubuntu", 2)
	if err != nil || count != 3 || len(rows) != 2 {
		t.Fatalf("count %d rows %v err %v", count, rows, err)
	}
	if rows[0].Name != "node server" || rows[0].PID != 11 || rows[0].CPU != 12.5 {
		t.Fatalf("busiest = %+v", rows[0])
	}
	if call := fake.lastCall("ps"); strings.Join(call.args, " ") != "-u ubuntu -o pid=,pcpu=,pmem=,etimes=,comm=" {
		t.Fatalf("ps = %+v", call)
	}
	if _, _, err := s.Processes(ctx, "-x", 5); err == nil {
		t.Fatal("a bad name reached ps")
	}
}

func TestDiskUsageTakesDusFigureEvenWhenSomethingWasUnreadable(t *testing.T) {
	s, fake, _ := hostAccounts(t)
	ctx := context.Background()
	fake.answer = func(_, name string, _ ...string) (string, error) {
		return "du: cannot read directory 'x': Permission denied\n12345\t/home/ubuntu\n", errors.New("du: exit status 1")
	}
	got, err := s.pathBytes(ctx, "/home/ubuntu")
	if err != nil || got != 12345 {
		t.Fatalf("got %d, %v", got, err)
	}
	fake.answer = func(_, name string, _ ...string) (string, error) {
		return "du: cannot access '/gone': No such file or directory\n", errors.New("du: exit status 1")
	}
	if _, err := s.pathBytes(ctx, "/gone"); err == nil || strings.Contains(err.Error(), "ParseInt") {
		t.Fatalf("err = %v", err)
	}
}

func TestEndingSessionsRefusesTheMachinesOwnAccounts(t *testing.T) {
	s, fake, _ := hostAccounts(t)
	ctx := context.Background()
	if err := s.EndSessions(ctx, "root"); !errors.Is(err, ErrProtected) {
		t.Fatalf("err = %v", err)
	}
	if fake.ran("pkill") {
		t.Fatal("pkill was asked")
	}
	if err := s.EndSessions(ctx, "ubuntu"); err != nil {
		t.Fatal(err)
	}
	if call := fake.lastCall("pkill"); call == nil || !strings.HasPrefix(strings.Join(call.args, " "), "--signal KILL --uid ") {
		t.Fatalf("pkill = %+v", call)
	}
	// pkill exits 1 when nothing matched, and nothing left is the goal.
	fake.answer = func(_, name string, _ ...string) (string, error) {
		if name == "pkill" {
			return "", errors.New("pkill: exit status 1")
		}
		return "", nil
	}
	if err := s.EndSessions(ctx, "ubuntu"); err != nil {
		t.Fatal(err)
	}
}
