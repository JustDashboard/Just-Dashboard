package procs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseJournalDiskUsage(t *testing.T) {
	for out, want := range map[string]int64{
		"Archived and active journals take up 1.2G in the file system.\n": 1288490188,
		"Archived and active journals take up 96.0M in the file system.":  96 << 20,
		"Journals take up 8.0M on disk.":                                  8 << 20,
		"Archived and active journals take up 512K in the file system.":   512 << 10,
		"Archived and active journals take up 0B in the file system.":     0,
		"No journal files were found.":                                    0,
		"":                                                                0,
	} {
		if got := ParseJournalDiskUsage(out); got != want {
			t.Errorf("%q read as %d, want %d", out, got, want)
		}
	}
}

func TestJournalRecordTime(t *testing.T) {
	want := time.UnixMicro(1731400000123456).UTC()
	for name, line := range map[string]string{
		"a whole record":   `{"MESSAGE":"Linux version 6.8.0","__REALTIME_TIMESTAMP":"1731400000123456","PRIORITY":"5"}`,
		"a cut-off record": `{"__CURSOR":"s=1","__REALTIME_TIMESTAMP" : "1731400000123456","MESSAGE":"a line longer than the buf`,
	} {
		if got := journalRecordTime([]byte(line)); !got.Equal(want) {
			t.Errorf("%s read as %v, want %v", name, got, want)
		}
	}
	for _, line := range []string{"", "-- No entries --", `{"MESSAGE":"no time"}`, `{"__REALTIME_TIMESTAMP":"soon"}`} {
		if got := journalRecordTime([]byte(line)); !got.IsZero() {
			t.Errorf("%q read as %v, want no time", line, got)
		}
	}
}

// The first record is read and journalctl is stopped: a journal of a million
// lines is not printed to learn when its first was written.
func TestReadJournalFactsReadsTheSizeAndTheFirstRecordOnly(t *testing.T) {
	bin := t.TempDir()
	script := `#!/bin/sh
case "$1" in
--disk-usage)
	echo "Archived and active journals take up 2.0G in the file system."
	exit 0 ;;
esac
echo '{"__REALTIME_TIMESTAMP":"1731400000000000","MESSAGE":"first"}'
echo '{"__REALTIME_TIMESTAMP":"1731500000000000","MESSAGE":"second"}'
exec sleep 30
`
	if err := os.WriteFile(filepath.Join(bin, "journalctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	started := time.Now()
	facts := ReadJournalFacts(context.Background())
	if facts.Size != 2<<30 {
		t.Errorf("size read as %d, want %d", facts.Size, int64(2<<30))
	}
	if want := time.UnixMicro(1731400000000000).UTC(); !facts.Oldest.Equal(want) {
		t.Errorf("oldest read as %v, want %v", facts.Oldest, want)
	}
	if took := time.Since(started); took > 5*time.Second {
		t.Errorf("waited %v for a journal that was still printing", took)
	}
}

func TestReadJournalFactsLeavesOutWhatJournalctlWouldNotSay(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "journalctl"), []byte("#!/bin/sh\necho 'No journal files were opened' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if facts := ReadJournalFacts(context.Background()); facts.Size != 0 || !facts.Oldest.IsZero() {
		t.Errorf("read %+v from a journalctl that failed", facts)
	}
}
