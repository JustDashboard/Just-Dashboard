package linuxusers

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// directProcess is what starting a process without hostexec looks like. The word
// boundary is what lets hostexec.Command through.
var directProcess = regexp.MustCompile(`\b(exec\.Command|exec\.CommandContext|os\.StartProcess|syscall\.Exec)\(`)

// Invariant 4: host commands go through hostexec. This package imports os/exec
// only for the error type it reads a missing tool from.
func TestNothingHereStartsAProcessOfItsOwn(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if call := directProcess.Find(src); call != nil {
			t.Errorf("%s calls %s; run the tool through hostexec (run.go)", name, call)
		}
	}
}
