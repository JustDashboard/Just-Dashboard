package deploy

import "testing"

func TestMongoKernelAffected(t *testing.T) {
	for release, want := range map[string]bool{
		// The GitHub runner image whose kernel MongoDB 8.0 refused.
		"7.0.0-1017-azure": true,
		"6.19.0":           true,
		"6.19.4-arch1-1":   true,
		"7.0.13":           true,
		"7.0":              true,
		// The kernel side of the fix, and everything after it.
		"7.0.14":            false,
		"7.0.21-generic":    false,
		"7.1.0":             false,
		"8.0.2":             false,
		"6.18.9":            false,
		"6.8.0-139-generic": false,
		"5.15.0-122":        false,
		"":                  false,
		"unknown":           false,
		"7":                 false,
	} {
		if got := MongoKernelAffected(release); got != want {
			t.Errorf("MongoKernelAffected(%q) = %t, want %t", release, got, want)
		}
	}
}

func TestMongoReleaseAffected(t *testing.T) {
	for release, want := range map[string]bool{
		"8.0": true, "8.0.34": true, "8": true, "8.2.1": true, "9.0": true,
		"7.0": false, "7.0.45": false, "6.0": false, "4.4": false, "": false, "latest": false,
	} {
		if got := MongoReleaseAffected(release); got != want {
			t.Errorf("MongoReleaseAffected(%q) = %t, want %t", release, got, want)
		}
	}
}

func TestMongoKernelTunables(t *testing.T) {
	for tunables, want := range map[string]bool{
		"glibc.pthread.rseq=1":                          true,
		"glibc.malloc.arena_max=2:glibc.pthread.rseq=1": true,
		// What the official 8.0 image sets, and what CI showed refused.
		"glibc.pthread.rseq=0":                      false,
		"glibc.pthread.rseq=1:glibc.pthread.rseq=0": false,
		"glibc.pthread.rseq=0:glibc.pthread.rseq=1": true,
		"": false,
	} {
		if got := MongoKernelMitigated(tunables); got != want {
			t.Errorf("MongoKernelMitigated(%q) = %t, want %t", tunables, got, want)
		}
	}
	for current, want := range map[string]string{
		"":                     "glibc.pthread.rseq=1",
		"glibc.pthread.rseq=0": "glibc.pthread.rseq=1",
		"glibc.malloc.arena_max=2:glibc.pthread.rseq=0": "glibc.malloc.arena_max=2:glibc.pthread.rseq=1",
		"glibc.malloc.arena_max=2":                      "glibc.malloc.arena_max=2:glibc.pthread.rseq=1",
	} {
		got := WithMongoKernelTunable(current)
		if got != want || !MongoKernelMitigated(got) {
			t.Errorf("WithMongoKernelTunable(%q) = %q, want %q", current, got, want)
		}
	}
}
