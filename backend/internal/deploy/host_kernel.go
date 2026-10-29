package deploy

import (
	"strconv"
	"strings"
)

// MongoDB 8 and Linux 6.19 to 7.0.13 (MongoDB SERVER-121912).
//
// MongoDB 8 is built with Google's tcmalloc, which keeps its per-CPU caches
// through rseq, and the official image sets GLIBC_TUNABLES=glibc.pthread.rseq=0
// so that glibc leaves rseq to it. The rseq rework in Linux 6.19 broke what
// tcmalloc relied on, and the kernel's fix only arrived in 7.0.14, so on the
// kernels between those mongod refuses to start ("MongoDB cannot start: Linux
// kernel versions 6.19 and newer has a known incompatibility…") and a
// container with a restart policy restarts for ever. MongoDB 8.0.30's own fix
// only takes effect on 7.0.14 and later.
//
// Handing rseq back to glibc (glibc.pthread.rseq=1) is what lets it start:
// tcmalloc then runs without its per-CPU caches, and mongod warns at startup
// that this costs performance. On a GitHub runner with Linux 7.0.0, mongo:8.0
// (8.0.34) refused as shipped and with rseq=0 set explicitly, started with
// rseq=1, and mongo:7.0 and mongo:6.0, built with the older tcmalloc that does
// not use rseq, started unchanged.
const (
	// MongoKernelTicket is MongoDB's ticket for the incompatibility.
	MongoKernelTicket = "SERVER-121912"
	// MongoKernelTunables and MongoKernelTunable are the environment
	// variable and the one tunable in it that let MongoDB 8 start there.
	MongoKernelTunables = "GLIBC_TUNABLES"
	MongoKernelTunable  = "glibc.pthread.rseq=1"
)

const mongoRseqTunable = "glibc.pthread.rseq"

// MongoKernelAffected reports whether a kernel release, as uname -r prints it
// ("7.0.0-1017-azure"), is one MongoDB 8 refuses to start on: 6.19 up to but
// not including 7.0.14. A release that cannot be read is not evidence either
// way and is reported unaffected.
func MongoKernelAffected(release string) bool {
	version, ok := kernelVersion(release)
	if !ok {
		return false
	}
	return !versionBefore(version, [3]int{6, 19, 0}) && versionBefore(version, [3]int{7, 0, 14})
}

// MongoReleaseAffected reports whether a MongoDB release ("8.0", "8.0.34",
// "8") is one built with the allocator the kernels above break: 8 and later.
// An unreadable one is reported unaffected.
func MongoReleaseAffected(release string) bool {
	major, _, _ := strings.Cut(strings.TrimSpace(release), ".")
	n, err := strconv.Atoi(major)
	return err == nil && n >= 8
}

// MongoKernelMitigated reports whether a GLIBC_TUNABLES value hands rseq to
// glibc. glibc reads the colon-separated list in order, so the last setting
// of the tunable is the one that holds.
func MongoKernelMitigated(tunables string) bool {
	on := false
	for _, item := range strings.Split(tunables, ":") {
		name, value, _ := strings.Cut(strings.TrimSpace(item), "=")
		if name == mongoRseqTunable {
			on = value == "1"
		}
	}
	return on
}

// WithMongoKernelTunable is a GLIBC_TUNABLES value with rseq handed to glibc
// and every other tunable in it kept as it was.
func WithMongoKernelTunable(tunables string) string {
	kept := []string{}
	for _, item := range strings.Split(tunables, ":") {
		item = strings.TrimSpace(item)
		name, _, _ := strings.Cut(item, "=")
		if item == "" || name == mongoRseqTunable {
			continue
		}
		kept = append(kept, item)
	}
	return strings.Join(append(kept, MongoKernelTunable), ":")
}

// kernelVersion reads the leading major.minor.patch of a kernel release.
func kernelVersion(release string) ([3]int, bool) {
	release = strings.TrimSpace(release)
	end := strings.IndexFunc(release, func(r rune) bool { return (r < '0' || r > '9') && r != '.' })
	if end >= 0 {
		release = release[:end]
	}
	parts := strings.Split(strings.Trim(release, "."), ".")
	if len(parts) < 2 {
		return [3]int{}, false
	}
	var out [3]int
	for i := 0; i < len(parts) && i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

func versionBefore(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
