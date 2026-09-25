package deploy

import (
	"strings"
	"testing"
)

// The kernel's own list of listening sockets names a candidate that is up
// but unreachable, which its output rarely does.
func TestListeningSocketsNameALoopbackOnlyCandidate(t *testing.T) {
	t.Parallel()
	procNet := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 41231 1 0000000000000000 100 0 0 10 0
   1: 0100007F:0BB8 0100007F:D431 01 00000000:00000000 00:00000000 00000000  1000        0 41232 1 0000000000000000 20 4 30 10 -1
  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000001000000:0BB8 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 41233 1 0000000000000000 100 0 0 10 0
`
	sockets := parseProcNetTCP([]byte(procNet))
	if len(sockets) != 2 || sockets[0].String() != "127.0.0.1:3000" || sockets[1].String() != "[::1]:3000" {
		t.Fatalf("sockets = %+v", sockets)
	}
	cause := applicationOutputCause([]ContainerDiagnostics{{State: "running", Listening: sockets}}, runtimeCauseContext{})
	if cause == nil || cause.Code != "runtime_loopback_bind" || len(cause.Subjects) != 1 || cause.Subjects[0] != "127.0.0.1:3000" ||
		!strings.Contains(cause.sentence(), "only on 127.0.0.1:3000") {
		t.Fatalf("cause = %+v", cause)
	}
	// A recipe whose server takes its address as a flag is started on every
	// interface instead.
	flagged := applicationOutputCause([]ContainerDiagnostics{{State: "running", Listening: sockets}}, runtimeCauseContext{
		build: BuildPlanConfig{Method: BuildRecipe, Recipe: "python", StartCommand: "uvicorn main:app --host 127.0.0.1 --port 3000"},
	})
	if flagged == nil || flagged.Fix == nil || flagged.Fix.Value != "uvicorn main:app --host 0.0.0.0 --port 3000" {
		t.Fatalf("flagged = %+v", flagged)
	}
	// Once anything listens on every interface, loopback is not the cause.
	open := parseProcNetTCP([]byte("   0: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000 0 0 1 1\n" +
		"   1: 0100007F:1F91 00000000:0000 0A 00000000:00000000 00:00000000 00000000 0 0 2 1\n"))
	if len(open) != 2 || open[0].String() != "0.0.0.0:8080" || loopbackOnlyListener([]ContainerDiagnostics{{Listening: open}}) != "" {
		t.Fatalf("open = %+v", open)
	}
	// What the output proves comes first.
	schema := applicationOutputCause([]ContainerDiagnostics{{
		Lines: []RuntimeLogLine{{Text: `relation "users" does not exist`}}, Listening: sockets,
	}}, runtimeCauseContext{})
	if schema == nil || schema.Code != "schema_missing" {
		t.Fatalf("precedence = %+v", schema)
	}
	if parseProcNetTCP([]byte("cat: /proc/net/tcp6: No such file or directory\n")) != nil {
		t.Fatal("an error line was read as a socket")
	}
	// A silent candidate still gets its cause in the failure message.
	suffix := diagnosticsSuffix(&runtimeDiagnosticsEvidence{Available: true, Cause: cause})
	if !strings.Contains(suffix, "only on 127.0.0.1:3000") || strings.Contains(suffix, "build log") {
		t.Fatalf("suffix = %q", suffix)
	}
	if suffix := diagnosticsSuffix(&runtimeDiagnosticsEvidence{Available: true}); suffix != "" {
		t.Fatalf("empty diagnostics suffix = %q", suffix)
	}
}
