package logsx

import (
	"bytes"
	"io"
	"os"
	"time"
)

// Readings ask about the last hour on every page load, and without this a
// "last hour" of a two-gigabyte log parsed two gigabytes to discard all but
// the end. A live file is written in time order, so the line where the window
// opens can be found by bisecting on the stamps of a few sampled lines, and
// the scan can start there.
//
// It is a shortcut, never a filter: the window is still applied line by line
// after it, and anything that makes the file's order doubtful — a probe with
// no stamp, stamps that run backwards — sends the scan back to the start.

const (
	sinceProbe   = 64 << 10
	sinceProbes  = 16
	sinceBackoff = 1 << 20
	// sinceMinSize is where bisecting starts paying for itself; below it the
	// whole file is cheaper to read than to reason about.
	sinceMinSize = 2 << 20
	// sinceSkew is how far out of order a stamp may be before the file stops
	// counting as time-ordered. Access logs stamp a request when it arrived
	// and write it when it finished, so a slow request lands after faster
	// ones that came in later.
	sinceSkew = time.Minute
)

// sinceOffset answers where to start reading a live file for a window opening
// at since, and how many lines precede that point so line numbers stay true.
// It answers 0 whenever the file does not look time-ordered.
func sinceOffset(file *os.File, since time.Time, f *Filter) (int64, int) {
	info, err := file.Stat()
	if err != nil || info.Size() < sinceMinSize {
		return 0, 0
	}
	size := info.Size()
	type probe struct {
		at    int64
		stamp time.Time
	}
	buf := make([]byte, sinceProbe)
	read := func(at int64) (time.Time, bool) {
		n, err := file.ReadAt(buf, at)
		if err != nil && err != io.EOF {
			return time.Time{}, false
		}
		return firstStamp(buf[:n], at > 0, f)
	}
	first, ok := read(0)
	if !ok || !first.Before(since) {
		return 0, 0
	}
	probes := []probe{{0, first}}
	lo, hi := int64(0), size
	for range sinceProbes - 1 {
		if hi-lo <= sinceProbe {
			break
		}
		mid := lo + (hi-lo)/2
		stamp, ok := read(mid)
		if !ok {
			return 0, 0
		}
		probes = append(probes, probe{mid, stamp})
		if stamp.Before(since) {
			lo = mid
		} else {
			hi = mid
		}
	}
	// Bisection only ever looked at the probes it needed; checked in offset
	// order, they have to agree that the file runs forwards in time.
	for i := range probes {
		for j := range probes {
			if probes[j].at > probes[i].at && probes[j].stamp.Before(probes[i].stamp.Add(-sinceSkew)) {
				return 0, 0
			}
		}
	}
	start := lo - sinceBackoff
	if start <= 0 {
		return 0, 0
	}
	return lineStart(file, start)
}

// firstStamp reads the first stamped line in a probe, through a fresh stream
// so a lens that knows the format's stamp — nginx's error log, Redis — finds
// it where the generic parser would not.
func firstStamp(chunk []byte, partial bool, f *Filter) (time.Time, bool) {
	if partial {
		// The probe landed mid-line; the first whole line starts after the
		// first newline.
		i := bytes.IndexByte(chunk, '\n')
		if i < 0 {
			return time.Time{}, false
		}
		chunk = chunk[i+1:]
	}
	st := f.Stream("")
	for len(chunk) > 0 {
		i := bytes.IndexByte(chunk, '\n')
		if i < 0 {
			// A line cut off by the end of the probe is not a line.
			break
		}
		l := ParseLine(string(chunk[:i]), "")
		st.Read(&l)
		if l.Timestamp != nil {
			return *l.Timestamp, true
		}
		chunk = chunk[i+1:]
	}
	return time.Time{}, false
}

// lineStart moves an offset forward to the start of the next line and counts
// the lines before it. Counting newlines reads the skipped prefix, but at the
// speed of a byte search rather than of parsing — a small price for "line
// 48211" still meaning line 48211.
func lineStart(file *os.File, at int64) (int64, int) {
	buf := make([]byte, 1<<20)
	lines := 0
	for pos := int64(0); pos < at; {
		n, err := file.ReadAt(buf[:min(int64(len(buf)), at-pos)], pos)
		lines += bytes.Count(buf[:n], []byte{'\n'})
		pos += int64(n)
		if err != nil || n == 0 {
			return 0, 0
		}
	}
	// The byte at `at` may be mid-line; the scan starts after the next newline.
	n, _ := file.ReadAt(buf[:sinceProbe], at)
	i := bytes.IndexByte(buf[:n], '\n')
	if i < 0 {
		return 0, 0
	}
	return at + int64(i) + 1, lines + 1
}
