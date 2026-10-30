package linuxusers

import (
	"testing"
)

func TestSignInOnThisKindOfCloudHost(t *testing.T) {
	// A stock cloud image after a provider set root's password: sshd allows root
	// with a password, ubuntu has "!" and no keys, and an account made with no way
	// in sits beside it. The page used to say root could not be reached at all and
	// that ubuntu could.
	policy := parseSSHPolicy("permitrootlogin yes\npasswordauthentication yes\npubkeyauthentication yes\nusepam yes\n")
	root := User{Username: "root", UID: 0, Shell: "/bin/bash", Password: PasswordSet}
	ubuntu := User{Username: "ubuntu", UID: 1000, Shell: "/bin/bash", Password: PasswordNone, Locked: true}
	zoro := User{Username: "zoro", UID: 1001, Shell: "/bin/bash", Password: PasswordNone, Locked: true}

	if got := evaluateSignIn(root, policy); !got.Password.Allowed || got.Key.Allowed || got.Key.Why != WhyNoKeys {
		t.Fatalf("root = %+v", got)
	}
	for _, u := range []User{ubuntu, zoro} {
		got := evaluateSignIn(u, policy)
		if got.Any() {
			t.Fatalf("%s can sign in: %+v", u.Username, got)
		}
		if got.Password.Why != WhyNoPassword || got.Key.Why != WhyNoKeys {
			t.Fatalf("%s = %+v", u.Username, got)
		}
	}
}

func TestSignInMatrix(t *testing.T) {
	person := func(mutate func(*User)) User {
		u := User{Username: "ion", UID: 1000, Shell: "/bin/bash", Password: PasswordSet, SSHKeys: 1, Groups: []string{"ion", "dev"}}
		if mutate != nil {
			mutate(&u)
		}
		return u
	}
	policy := func(mutate func(*SSHPolicy)) SSHPolicy {
		p := defaultPolicy()
		if mutate != nil {
			mutate(&p)
		}
		return p
	}
	cases := []struct {
		name        string
		user        User
		policy      SSHPolicy
		password    bool
		passwordWhy string
		key         bool
		keyWhy      string
	}{
		{"both open", person(nil), policy(nil), true, "", true, ""},
		{"no ssh server", person(nil), SSHPolicy{}, false, WhyNoSSH, false, WhyNoSSH},
		{"expired", person(func(u *User) { u.Disabled = true }), policy(nil), false, WhyDisabled, false, WhyDisabled},
		{"nologin", person(func(u *User) { u.Shell = "/usr/sbin/nologin" }), policy(nil), false, WhyNoShell, false, WhyNoShell},
		{"false", person(func(u *User) { u.Shell = "/bin/false" }), policy(nil), false, WhyNoShell, false, WhyNoShell},
		{"password auth off", person(nil), policy(func(p *SSHPolicy) { p.PasswordAuth = false }), false, WhyPasswordAuthOff, true, ""},
		{"key auth off", person(nil), policy(func(p *SSHPolicy) { p.KeyAuth = false }), true, "", false, WhyKeyAuthOff},
		{"locked password", person(func(u *User) { u.Password = PasswordLocked; u.Locked = true }), policy(nil), false, WhyPasswordLocked, true, ""},
		{"no password", person(func(u *User) { u.Password = PasswordNone; u.Locked = true }), policy(nil), false, WhyNoPassword, true, ""},
		{"no keys", person(func(u *User) { u.SSHKeys = 0 }), policy(nil), true, "", false, WhyNoKeys},
		{"empty refused", person(func(u *User) { u.Password = PasswordEmpty }), policy(nil), false, WhyEmptyRefused, true, ""},
		{"empty permitted", person(func(u *User) { u.Password = PasswordEmpty }), policy(func(p *SSHPolicy) { p.PermitEmpty = true }), true, "", true, ""},
		{"locked without PAM", person(func(u *User) { u.Password = PasswordNone; u.Locked = true }), policy(func(p *SSHPolicy) { p.UsePAM = false }), false, WhyNoPassword, false, WhyLockedNoPAM},
		{"denied by name", person(nil), policy(func(p *SSHPolicy) { p.DenyUsers = []string{"ion"} }), false, WhyNotAllowed, false, WhyNotAllowed},
		{"denied by pattern", person(nil), policy(func(p *SSHPolicy) { p.DenyUsers = []string{"i*"} }), false, WhyNotAllowed, false, WhyNotAllowed},
		{"not on the allow list", person(nil), policy(func(p *SSHPolicy) { p.AllowUsers = []string{"deploy"} }), false, WhyNotAllowed, false, WhyNotAllowed},
		{"on the allow list from an address", person(nil), policy(func(p *SSHPolicy) { p.AllowUsers = []string{"ion@10.0.0.*"} }), true, "", true, ""},
		{"not in an allowed group", person(nil), policy(func(p *SSHPolicy) { p.AllowGroups = []string{"ops"} }), false, WhyNotAllowed, false, WhyNotAllowed},
		{"in an allowed group", person(nil), policy(func(p *SSHPolicy) { p.AllowGroups = []string{"dev"} }), true, "", true, ""},
		{"in a denied group", person(nil), policy(func(p *SSHPolicy) { p.DenyGroups = []string{"dev"} }), false, WhyNotAllowed, false, WhyNotAllowed},
		{"deny beats allow", person(nil), policy(func(p *SSHPolicy) { p.DenyUsers = []string{"ion"}; p.AllowUsers = []string{"ion"} }), false, WhyNotAllowed, false, WhyNotAllowed},
		{"sshd will not say", person(nil), SSHPolicy{Installed: true}, true, "", true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := evaluateSignIn(c.user, c.policy)
			if got.Password.Allowed != c.password || got.Password.Why != c.passwordWhy {
				t.Errorf("password = %+v, want %v %q", got.Password, c.password, c.passwordWhy)
			}
			if got.Key.Allowed != c.key || got.Key.Why != c.keyWhy {
				t.Errorf("key = %+v, want %v %q", got.Key, c.key, c.keyWhy)
			}
		})
	}
}

func TestPermitRootLoginDecidesRootsRoutes(t *testing.T) {
	root := User{Username: "root", UID: 0, Shell: "/bin/bash", Password: PasswordSet, SSHKeys: 1}
	cases := []struct {
		permit      string
		password    bool
		passwordWhy string
		key         bool
		keyWhy      string
	}{
		{"yes", true, "", true, ""},
		{"prohibit-password", false, WhyRootKeysOnly, true, ""},
		{"without-password", false, WhyRootKeysOnly, true, ""},
		{"forced-commands-only", false, WhyRootKeysOnly, true, ""},
		{"no", false, WhyRootLoginOff, false, WhyRootLoginOff},
	}
	for _, c := range cases {
		p := defaultPolicy()
		p.PermitRoot = c.permit
		got := evaluateSignIn(root, p)
		if got.Password.Allowed != c.password || got.Password.Why != c.passwordWhy ||
			got.Key.Allowed != c.key || got.Key.Why != c.keyWhy {
			t.Errorf("%s: %+v", c.permit, got)
		}
	}
	// PermitRootLogin says nothing about anyone else.
	p := defaultPolicy()
	p.PermitRoot = "no"
	if got := evaluateSignIn(User{Username: "ion", UID: 1000, Shell: "/bin/bash", Password: PasswordSet}, p); !got.Password.Allowed {
		t.Fatalf("ion = %+v", got)
	}
}

func TestParseSSHPolicyReadsTheDumpAndAccumulatesLists(t *testing.T) {
	p := parseSSHPolicy(`port 22
permitrootlogin without-password
passwordauthentication no
pubkeyauthentication yes
permitemptypasswords no
usepam no
allowusers ion deploy@10.0.0.1
allowusers mira
denyusers bad
allowgroups ops
denygroups contractors temps
`)
	if !p.Installed || !p.Known || p.PasswordAuth || !p.KeyAuth || p.PermitEmpty || p.UsePAM {
		t.Fatalf("p = %+v", p)
	}
	if p.PermitRoot != "without-password" {
		t.Fatalf("permit = %q", p.PermitRoot)
	}
	if len(p.AllowUsers) != 3 || p.AllowUsers[2] != "mira" {
		t.Fatalf("allow = %v", p.AllowUsers)
	}
	if len(p.DenyGroups) != 2 {
		t.Fatalf("deny groups = %v", p.DenyGroups)
	}
}
