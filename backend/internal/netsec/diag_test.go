package netsec

import (
	"strings"
	"testing"
)

// A probe target becomes an argv element of a command run on the host. It
// never reaches a shell, but a target beginning with a dash would still be
// read as an option by the tool itself.
func TestValidTarget(t *testing.T) {
	for _, ok := range []string{"example.com", "example.com.", "sub.example.co.uk", "192.0.2.1", "2001:db8::1", "host1"} {
		if !ValidTarget(ok) {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{
		"", "-i", "--help", "example.com; rm -rf /", "a b", "exa mple", "$(id)",
		strings.Repeat("a", 254),
	} {
		if ValidTarget(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestLookupRejectsUnknownRecordTypes(t *testing.T) {
	s := New()
	if _, err := s.Lookup(t.Context(), "example.com", "ANY"); err == nil {
		t.Error("accepted an unknown record type")
	}
	// PTR takes an address, and a hostname there produces a lookup that can
	// only ever fail.
	if _, err := s.Lookup(t.Context(), "example.com", "PTR"); err == nil {
		t.Error("accepted a hostname for a PTR lookup")
	}
}

func TestPortCheckRejectsBadPorts(t *testing.T) {
	s := New()
	for _, port := range []int{0, -1, 65536} {
		if _, err := s.PortCheck(t.Context(), "example.com", port); err == nil {
			t.Errorf("accepted port %d", port)
		}
	}
}

func TestDescribeDialError(t *testing.T) {
	if !strings.Contains(describeDialError(errString("connection refused")), "refused") {
		t.Error("a refusal should be described as one")
	}
	if !strings.Contains(describeDialError(errString("i/o timeout")), "firewall") {
		t.Error("a timeout is what a drop looks like, and should say so")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
