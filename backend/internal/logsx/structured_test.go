package logsx

import (
	"testing"
	"time"
)

func TestStripANSIRemovesColourAndCursorMoves(t *testing.T) {
	// The exact shape a deployment's first Logs page showed: Prisma's spinner
	// clearing the line and moving the cursor between frames.
	raw := "\x1b[2K\x1b[1A\x1b[2K\x1b[GGenerated Prisma Client (v6.19.3)"
	if got := StripANSI(raw); got != "Generated Prisma Client (v6.19.3)" {
		t.Fatalf("StripANSI = %q", got)
	}
}

func TestStripANSIKeepsPlainText(t *testing.T) {
	plain := "Sep 19 05:47:57 host sshd[1]: Accepted publickey for root"
	if got := StripANSI(plain); got != plain {
		t.Fatalf("a line with no control bytes must be returned untouched, got %q", got)
	}
}

func TestStripANSIResolvesCarriageReturnsToTheLastFrame(t *testing.T) {
	// A progress bar's whole animation arrives as one line. What the operator
	// would have seen is the last frame, not forty copies of the same sentence.
	raw := "downloading  10%\rdownloading  60%\rdownloading 100%"
	if got := StripANSI(raw); got != "downloading 100%" {
		t.Fatalf("StripANSI = %q", got)
	}
	if got := StripANSI("done\r"); got != "done" {
		t.Fatalf("a trailing carriage return must not empty the line, got %q", got)
	}
}

func TestStripANSIHandlesLongSequencesAndOSC(t *testing.T) {
	// A fixed-length skip would leave the tail of a long colour run on screen.
	if got := StripANSI("\x1b[38;2;255;128;0;1;4mred\x1b[0m"); got != "red" {
		t.Fatalf("StripANSI = %q", got)
	}
	if got := StripANSI("\x1b]0;window title\x07after"); got != "after" {
		t.Fatalf("OSC not stripped: %q", got)
	}
	// An unterminated sequence at the end of a truncated line must not hang
	// or emit the raw bytes.
	if got := StripANSI("text\x1b["); got != "text" {
		t.Fatalf("StripANSI = %q", got)
	}
}

func TestStripANSIHonoursBackspace(t *testing.T) {
	if got := StripANSI("abc\b\bX"); got != "aX" {
		t.Fatalf("StripANSI = %q", got)
	}
}

func TestParseLineStripsBeforeDetectingLevel(t *testing.T) {
	line := ParseLine("\x1b[31mERROR\x1b[0m connection refused", "app")
	if line.Level != "error" {
		t.Fatalf("level = %q: the word scan must see stripped text", line.Level)
	}
	if line.Text != "ERROR connection refused" {
		t.Fatalf("text = %q", line.Text)
	}
}

func TestParseLineReadsStructuredLevel(t *testing.T) {
	line := ParseLine(`{"level":"error","msg":"upstream timeout","requestId":"abc-1","status":502}`, "app")
	if line.Level != "error" {
		t.Fatalf("level = %q", line.Level)
	}
	if line.Message != "upstream timeout" {
		t.Fatalf("message = %q", line.Message)
	}
	if line.Fields["requestId"] != "abc-1" || line.Fields["status"] != "502" {
		t.Fatalf("fields = %v", line.Fields)
	}
	// The raw line is kept: a search for the request id must still find it,
	// and nothing the application logged is thrown away.
	if line.Text == "" || line.Fields["level"] != "" {
		t.Fatalf("text/fields wrong: %q %v", line.Text, line.Fields)
	}
}

func TestParseLineReadsStructuredTimestamps(t *testing.T) {
	line := ParseLine(`{"time":"2026-09-19T05:47:57Z","level":"info","msg":"listening"}`, "app")
	if line.Timestamp == nil || line.Timestamp.UTC().Format(time.RFC3339) != "2026-09-19T05:47:57Z" {
		t.Fatalf("timestamp = %v", line.Timestamp)
	}
}

func TestParseLineReadsNumericLevels(t *testing.T) {
	// pino counts up in tens; syslog counts down from 0. Reading a pino 50 as
	// a syslog priority would file every error as a debug line.
	if got := ParseLine(`{"level":50,"msg":"boom"}`, "app").Level; got != "error" {
		t.Fatalf("pino level 50 read as %q", got)
	}
	if got := ParseLine(`{"level":30,"msg":"hello"}`, "app").Level; got != "info" {
		t.Fatalf("pino level 30 read as %q", got)
	}
	if got := ParseLine(`{"severity":3,"message":"bad"}`, "app").Level; got != "error" {
		t.Fatalf("syslog priority 3 read as %q", got)
	}
}

func TestParseLineLeavesPlainTextAlone(t *testing.T) {
	// A brace at the start is not enough: a pretty-printed fragment inside a
	// stack trace has one too.
	for _, text := range []string{
		"{",
		"{ not json",
		`{"a": 1`,
		"plain text",
	} {
		line := ParseLine(text, "app")
		if line.Message != "" || line.Fields != nil {
			t.Fatalf("%q was read as structured: %+v", text, line)
		}
	}
}

func TestParseLineStructuredWithoutLevelStillScansTheMessage(t *testing.T) {
	line := ParseLine(`{"msg":"error while connecting","host":"db"}`, "app")
	if line.Level != "error" {
		t.Fatalf("level = %q, want the message scanned", line.Level)
	}
	// Scanning the whole JSON instead would find the word inside a key name.
	other := ParseLine(`{"msg":"all good","errorCount":0}`, "app")
	if other.Level != "" {
		t.Fatalf("level = %q: a key named errorCount is not an error", other.Level)
	}
}
