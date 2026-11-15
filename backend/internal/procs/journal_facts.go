package procs

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// JournalFacts is what the whole journal is, for the line a page opens on:
// how much disk it holds and how far back it goes. Either is left zero where
// journalctl would not say.
type JournalFacts struct {
	// Size is what `journalctl --disk-usage` reports, in bytes.
	Size int64
	// Oldest is when the first record still kept was written.
	Oldest time.Time
}

// journalFactsTimeout bounds both questions together. They are a line of
// facts over a log viewer, and a journal slow to open must not hold it back.
const journalFactsTimeout = 6 * time.Second

// ReadJournalFacts asks journalctl how big the journal is and reads its
// first record for the time it was written. Both run on the host, as every
// journal read does (see JournalCommandOpts).
func ReadJournalFacts(ctx context.Context) JournalFacts {
	ctx, cancel := context.WithTimeout(ctx, journalFactsTimeout)
	defer cancel()
	var facts JournalFacts

	usage := hostexec.CommandOnHost(ctx, "journalctl", "--disk-usage")
	usage.Env = append(os.Environ(), "SYSTEMD_IGNORE_CHROOT=1", "LC_ALL=C")
	if out, err := usage.Output(); err == nil {
		facts.Size = ParseJournalDiskUsage(string(out))
	}

	// With no -n and no --reverse journalctl starts at the head, so the
	// first line out is the oldest record. It is read and the command is
	// stopped: the rest of the journal is not what was asked for.
	first, stop := context.WithCancel(ctx)
	defer stop()
	head := hostexec.CommandOnHost(first, "journalctl", "--output=json", "--no-pager", "--quiet")
	head.Env = append(os.Environ(), "SYSTEMD_IGNORE_CHROOT=1")
	if pipe, err := head.StdoutPipe(); err == nil && head.Start() == nil {
		reader := bufio.NewReaderSize(pipe, 64<<10)
		// A record longer than the buffer is still one whose timestamp is
		// read: ReadLine hands back the first part, which is not valid JSON
		// on its own, so the field is found by pattern instead.
		line, _, _ := reader.ReadLine()
		facts.Oldest = journalRecordTime(line)
		stop()
		_ = head.Wait()
	}
	return facts
}

var journalUsage = regexp.MustCompile(`take up ([0-9]+(?:\.[0-9]+)?)\s*([BKMGTPE]?)`)

// ParseJournalDiskUsage reads journalctl's sentence ("Archived and active
// journals take up 1.2G in the file system."; before systemd 234, "Journals
// take up 96.0M on disk.") into bytes. Its units are powers of 1024.
func ParseJournalDiskUsage(out string) int64 {
	m := journalUsage.FindStringSubmatch(out)
	if m == nil {
		return 0
	}
	value, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	shift := strings.Index("BKMGTPE", m[2])
	if m[2] == "" || shift < 0 {
		shift = 0
	}
	return int64(value * float64(int64(1)<<(10*shift)))
}

var journalRealtime = regexp.MustCompile(`"__REALTIME_TIMESTAMP"\s*:\s*"([0-9]+)"`)

// journalRecordTime is when a JSON record was written, zero where the line
// is not one.
func journalRecordTime(line []byte) time.Time {
	var raw struct {
		Realtime string `json:"__REALTIME_TIMESTAMP"`
	}
	if err := json.Unmarshal(line, &raw); err != nil || raw.Realtime == "" {
		m := journalRealtime.FindSubmatch(line)
		if m == nil {
			return time.Time{}
		}
		raw.Realtime = string(m[1])
	}
	us, err := strconv.ParseInt(raw.Realtime, 10, 64)
	if err != nil || us <= 0 {
		return time.Time{}
	}
	return time.UnixMicro(us).UTC()
}
