package netsec

import "testing"

// `sshd -T` on OpenSSH 9.9 — Ubuntu 25.04 — prints `permitrootlogin
// without-password`, the deprecated spelling of the value the distributions
// ship as their default. The dropdown has no entry for it, so the control
// rendered empty: a security setting reading as "not set" on a stock host.
func TestPermitRootLoginAliasIsFoldedOntoTheOfferedValue(t *testing.T) {
	def, ok := sshDirectiveFor("permitrootlogin")
	if !ok {
		t.Fatal("no permitrootlogin directive")
	}
	if got := def.canonical("without-password"); got != "prohibit-password" {
		t.Fatalf("canonical = %q, want prohibit-password", got)
	}

	// Every value the page can show has to be one the control offers, or the
	// dropdown is empty again.
	for _, opt := range def.Options {
		if def.canonical(opt) != opt {
			t.Errorf("an offered value was rewritten: %q -> %q", opt, def.canonical(opt))
		}
	}
	offered := map[string]bool{}
	for _, opt := range def.Options {
		offered[opt] = true
	}
	for _, to := range def.Aliases {
		if !offered[to] {
			t.Errorf("alias target %q is not one of the options", to)
		}
	}
}

// A directive with no aliases passes its value through unchanged.
func TestCanonicalLeavesEverythingElseAlone(t *testing.T) {
	def, _ := sshDirectiveFor("passwordauthentication")
	if got := def.canonical("yes"); got != "yes" {
		t.Fatalf("got %q", got)
	}
}
