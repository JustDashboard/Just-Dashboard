package procs

import (
	"strings"
	"testing"
	"time"
)

func TestActiveSinceCombinesBootUptimeWithMonotonicTimestamp(t *testing.T) {
	now := time.Date(2026, 9, 1, 22, 0, 0, 0, time.UTC)
	// Host booted two hours ago and the service became active half an hour
	// after boot, so it has been active for ninety minutes.
	got := activeSinceUnix(uint64((30*time.Minute)/time.Microsecond), uint64((2*time.Hour)/time.Second), now)
	want := now.Add(-90 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("active since = %s, want %s", got, want)
	}
}

func TestParseSystemdTimestampFallback(t *testing.T) {
	got, ok := parseSystemdTimestamp("Tue 2026-09-01 20:30:00 UTC")
	if !ok || got.Unix() != time.Date(2026, 9, 1, 20, 30, 0, 0, time.UTC).Unix() {
		t.Fatalf("parsed timestamp = %s, %v", got, ok)
	}
}

// The manager's lines about a unit are written by PID 1: their _SYSTEMD_UNIT
// is init.scope, and the unit and the run they describe are in UNIT and
// INVOCATION_ID. This record is the shape systemd 257 writes (checked on a
// host journal); reading only the underscore fields loses how the run ended.
func TestParseJournalLineKeepsTheManagerFields(t *testing.T) {
	e, ok := ParseJournalLine([]byte(`{"__REALTIME_TIMESTAMP":"1790000000000000","PRIORITY":"4",` +
		`"_SYSTEMD_UNIT":"init.scope","UNIT":"nordvpnd-killswitch.service","INVOCATION_ID":"bce17d0c1f2a4c7e",` +
		`"SYSLOG_IDENTIFIER":"systemd","_COMM":"systemd","_PID":"1","EXIT_CODE":"exited","EXIT_STATUS":"1",` +
		`"MESSAGE_ID":"98e322203f7a4ed290d09fe03c09fe15",` +
		`"MESSAGE":"nordvpnd-killswitch.service: Main process exited, code=exited, status=1/FAILURE"}`))
	if !ok {
		t.Fatal("a manager record did not parse")
	}
	if e.Unit != "init.scope" || e.About != "nordvpnd-killswitch.service" || e.Invocation != "bce17d0c1f2a4c7e" {
		t.Errorf("unit %q about %q invocation %q", e.Unit, e.About, e.Invocation)
	}
	if e.ExitCode != "exited" || e.ExitStatus != "1" || e.MessageID != "98e322203f7a4ed290d09fe03c09fe15" || e.Priority != 4 {
		t.Errorf("entry = %+v", e)
	}
	result, _ := ParseJournalLine([]byte(`{"UNIT_RESULT":"exit-code","USER_INVOCATION_ID":"u1","_SYSTEMD_INVOCATION_ID":"s1",` +
		`"USER_UNIT":"sync.service","_COMM":"systemd","MESSAGE":"sync.service: Failed with result 'exit-code'."}`))
	if result.Result != "exit-code" || result.Invocation != "u1" || result.About != "sync.service" || result.Comm != "systemd" {
		t.Errorf("entry = %+v", result)
	}
}

// A user unit's lifecycle is written by the user manager, which is itself a
// run of user@1000.service: every line it writes carries that run as
// _SYSTEMD_INVOCATION_ID. The run a line is about is USER_INVOCATION_ID, and
// reading the manager's own would make a week of restarts one run. The
// unit's own output keeps the only invocation it has.
func TestParseJournalLineTakesTheRunAUserManagerLineIsAbout(t *testing.T) {
	manager := func(invocation, message string) JournalEntry {
		e, ok := ParseJournalLine([]byte(`{"__REALTIME_TIMESTAMP":"1790000000000000","PRIORITY":"6",` +
			`"_SYSTEMD_UNIT":"user@1000.service","_SYSTEMD_INVOCATION_ID":"a1f0c2d3e4b5a6978877665544332211",` +
			`"_SYSTEMD_USER_UNIT":"init.scope","USER_UNIT":"sync.service","USER_INVOCATION_ID":"` + invocation + `",` +
			`"SYSLOG_IDENTIFIER":"systemd","_COMM":"systemd","_PID":"2211","__CURSOR":"s=1;i=` + invocation + `",` +
			`"MESSAGE":"` + message + `"}`))
		if !ok {
			t.Fatalf("%q did not parse", message)
		}
		return e
	}
	first := manager("0b7e3a5c9d2f4e6a8b1c3d5e7f9a1b2c", "Started sync.service - Sync the notes.")
	second := manager("5d9c1e3a7b2f4d6e8a0c2e4a6c8e0a2b", "sync.service: Main process exited, code=exited, status=1/FAILURE")
	if first.Invocation != "0b7e3a5c9d2f4e6a8b1c3d5e7f9a1b2c" || second.Invocation != "5d9c1e3a7b2f4d6e8a0c2e4a6c8e0a2b" {
		t.Errorf("runs = %q, %q; want each line's USER_INVOCATION_ID", first.Invocation, second.Invocation)
	}
	if first.About != "sync.service" || first.Unit != "user@1000.service" || first.Cursor != "s=1;i=0b7e3a5c9d2f4e6a8b1c3d5e7f9a1b2c" {
		t.Errorf("entry = %+v", first)
	}
	own, _ := ParseJournalLine([]byte(`{"_SYSTEMD_UNIT":"user@1000.service","_SYSTEMD_USER_UNIT":"sync.service",` +
		`"_SYSTEMD_INVOCATION_ID":"7c1d","SYSLOG_IDENTIFIER":"rclone","MESSAGE":"Copied 3 files"}`))
	if own.Invocation != "7c1d" || own.About != "" {
		t.Errorf("the unit's own line = %+v", own)
	}
}

// A search that must reach its window's newest end asks for it first.
func TestJournalCommandReadsNewestFirst(t *testing.T) {
	cmd, err := JournalCommandOpts(t.Context(), JournalOptions{Unit: "nordvpnd.service", Reverse: true, MaxPriority: -1})
	if err != nil {
		t.Fatal(err)
	}
	if argv := strings.Join(cmd.Args, " "); !strings.Contains(argv, "-u nordvpnd.service") || !strings.Contains(argv, " --reverse") {
		t.Errorf("argv = %s", argv)
	}
}

// Each identifier is its own -t, validated like a unit name, so a journal-id
// source cannot smuggle an option into journalctl's argv.
func TestJournalCommandTakesSeveralIdentifiers(t *testing.T) {
	cmd, err := JournalCommandOpts(t.Context(), JournalOptions{
		Identifiers: []string{"sshd", "sshd-session", "sudo"}, Kernel: true, MaxPriority: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(cmd.Args, " ")
	if !strings.Contains(argv, "-t sshd -t sshd-session -t sudo") || !strings.Contains(argv, " -k") {
		t.Errorf("argv = %s", argv)
	}
	if _, err := JournalCommandOpts(t.Context(), JournalOptions{Identifiers: []string{"--output=cat"}}); err == nil {
		t.Error("an identifier that is an option was accepted")
	}
}
