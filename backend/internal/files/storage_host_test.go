package files

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStorageMapsHostRootAndTemporaryCandidatesWithoutWideningRestrictions(t *testing.T) {
	hostRoot := t.TempDir()
	temporary := filepath.Join(hostRoot, "tmp", "old-fixture")
	if err := os.MkdirAll(filepath.Dir(temporary), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(temporary, []byte("owned-test-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(temporary, old, old); err != nil {
		t.Fatal(err)
	}
	service := New([]string{"/"})
	service.storageHostRoot = hostRoot
	service.storageProc = t.TempDir()
	report, err := service.ScanStorage(t.Context(), "/", false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Path != hostRoot || report.RequestedPath != "/" || !report.HostFilesystem || len(report.TemporaryFiles) != 1 || report.TemporaryFiles[0].Path != temporary {
		t.Fatalf("host evidence: %+v", report)
	}
	result, err := service.CleanupStorage(t.Context(), []StorageSelection{{Kind: "temporary", File: report.TemporaryFiles[0]}})
	if err != nil || len(result.Items) != 1 || !result.Items[0].Removed {
		t.Fatalf("host candidate cleanup: %+v %v", result, err)
	}
	restrictedRoot := t.TempDir()
	restricted := New([]string{restrictedRoot})
	restricted.storageHostRoot = hostRoot
	if _, err := restricted.ScanStorage(t.Context(), restrictedRoot, false); err == nil {
		t.Fatal("host alias bypassed restricted roots")
	}
}

func TestStorageMountedHostFilesystem(t *testing.T) {
	if os.Getenv("JD_ADVISOR_HOST_MOUNT_LIVE") != "1" {
		t.Skip("requires a read-only /host mount, host PID namespace and the harness fixture")
	}
	path := os.Getenv("JD_ADVISOR_HOST_FIXTURE")
	if path == "" || !strings.HasPrefix(path, "/tmp/jd-advisor-linux.") {
		t.Fatal("only the harness-owned fixture may be scanned")
	}
	service := New([]string{"/"})
	if service.storageHostRoot != "/host" {
		t.Fatal("real host mount was not identified against PID 1")
	}
	report, err := service.ScanStorage(t.Context(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join("/host", path)
	if report.Path != expected || !report.Complete || !report.HostFilesystem || report.RequestedPath != path || len(report.LargeFiles) != 1 || report.LargeFiles[0].Path != filepath.Join(expected, "marker") {
		t.Fatalf("not actual host evidence: %+v", report)
	}
}
