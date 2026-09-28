package proxysvc

import (
	"regexp"
	"testing"
)

func TestNginxIdent(t *testing.T) {
	// Three site names that fold to the same characters must still name
	// three zones: a duplicate zone name is an emergency at the next reload.
	seen := map[string]string{}
	for _, name := range []string{"a-b", "a_b", "a.b"} {
		ident := NginxIdent(name)
		if other, ok := seen[ident]; ok {
			t.Fatalf("%q and %q share %q", name, other, ident)
		}
		seen[ident] = name
	}

	valid := regexp.MustCompile(`^jd_[a-z0-9_]+$`)
	for name, want := range map[string]string{
		// Nothing folded: the name reads as itself.
		"app":     "jd_app",
		"app2024": "jd_app2024",
		// Pinned so a stored zone name never changes under a site.
		"a-b":             "jd_a_b_2a89df",
		"app.example.com": "jd_app_example_com_dd46c9",
		"App":             "jd__pp_81f51a",
	} {
		got := NginxIdent(name)
		if got != want {
			t.Errorf("NginxIdent(%q) = %q, want %q", name, got, want)
		}
		if !valid.MatchString(got) {
			t.Errorf("NginxIdent(%q) = %q is not a name nginx takes everywhere", name, got)
		}
	}
}
