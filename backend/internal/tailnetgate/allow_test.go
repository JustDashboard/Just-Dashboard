package tailnetgate

import (
	"slices"
	"testing"
)

func TestCanonicalReadsLoginsAndTags(t *testing.T) {
	for _, fixture := range []struct {
		raw, want string
		tag, ok   bool
	}{
		{"Alice@Example.com", "alice@example.com", false, true},
		{"  bob@github ", "bob@github", false, true},
		{"carol+ops@corp.example.org", "carol+ops@corp.example.org", false, true},
		{"tag:ci", "tag:ci", true, true},
		{"tag:Build-2", "tag:Build-2", true, true},
		{"TAG:ci", "", true, false},
		{"tag:2ci", "", true, false},
		{"tag:", "", true, false},
		{"alice", "", false, false},
		{"@example.com", "", false, false},
		{"alice@", "", false, false},
		{"alice@example.com, bob@example.com", "", false, false},
		{"*@example.com", "", false, false},
		{"", "", false, false},
	} {
		got, tag, err := Canonical(fixture.raw)
		if (err == nil) != fixture.ok || got != fixture.want || (fixture.ok && tag != fixture.tag) {
			t.Errorf("Canonical(%q) = %q tag=%v err=%v", fixture.raw, got, tag, err)
		}
	}
	if _, err := CanonicalList([]string{"alice@example.com", "ALICE@example.com"}); err == nil {
		t.Error("the same login twice in two cases was accepted")
	}
	long := make([]string, MaxAllow+1)
	for i := range long {
		long[i] = "tag:t" + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	if _, err := CanonicalList(long); err == nil {
		t.Error("a list past the limit was accepted")
	}
	if got, err := CanonicalList([]string{"Alice@Example.com", "tag:ci"}); err != nil || !slices.Equal(got, []string{"alice@example.com", "tag:ci"}) {
		t.Errorf("CanonicalList = %v, %v", got, err)
	}
}

// Logins without regard to case, tags exactly, and never the one for the other.
func TestAdmitsComparesLoginsWithoutCaseAndTagsExactly(t *testing.T) {
	allow := []string{"alice@example.com", "tag:ci"}
	alice := Peer{Login: "Alice@EXAMPLE.com", Name: "Alice"}
	if entry, ok := Admits(allow, alice); !ok || entry != "alice@example.com" {
		t.Fatalf("alice = %q %v", entry, ok)
	}
	if _, ok := Admits(allow, Peer{Login: "mallory@example.com"}); ok {
		t.Fatal("a login not on the list was admitted")
	}
	if entry, ok := Admits(allow, Peer{Tags: []string{"tag:build", "tag:ci"}}); !ok || entry != "tag:ci" {
		t.Fatalf("a tagged machine = %q %v", entry, ok)
	}
	if _, ok := Admits(allow, Peer{Tags: []string{"tag:CI"}}); ok {
		t.Fatal("a tag of another case was admitted")
	}
	// A tagged machine carries tailscaled's shared placeholder profile; a
	// list that happened to name it must not admit every tagged machine.
	if _, ok := Admits([]string{"tagged-devices@x"}, Peer{Login: "", Tags: []string{"tag:other"}}); ok {
		t.Fatal("a tagged machine was admitted by a login")
	}
	if _, ok := Admits(nil, alice); ok {
		t.Fatal("an empty list admitted somebody")
	}
}
