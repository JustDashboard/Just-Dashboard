package linuxusers

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

// Why a route is closed, as codes the page turns into sentences. Each names the
// one thing an operator would change.
const (
	WhyDisabled        = "disabled"       // the account has expired
	WhyNoShell         = "no_shell"       // its shell refuses a login
	WhyNoSSH           = "no_ssh"         // this host has no SSH server
	WhyNotAllowed      = "not_allowed"    // AllowUsers/DenyUsers/AllowGroups/DenyGroups
	WhyRootLoginOff    = "root_login_off" // PermitRootLogin no
	WhyRootKeysOnly    = "root_keys_only" // PermitRootLogin prohibit-password
	WhyPasswordAuthOff = "password_auth_off"
	WhyKeyAuthOff      = "key_auth_off"
	WhyNoPassword      = "no_password"
	WhyPasswordLocked  = "password_locked"
	WhyEmptyRefused    = "empty_refused" // an empty password with PermitEmptyPasswords no
	WhyNoKeys          = "no_keys"
	WhyLockedNoPAM     = "locked_without_pam" // UsePAM no: sshd itself refuses a locked account
	WhyPasswordDead    = "password_dead"      // its password's age rules have closed it
	WhyKeysUnknown     = "keys_unknown"       // open as far as anyone can tell: a key source this cannot read
)

// SignInRoute is one way in, open or not, and what closes it.
type SignInRoute struct {
	Allowed bool   `json:"allowed"`
	Why     string `json:"why,omitempty"`
}

// SignIn is how SSH would treat this account today.
type SignIn struct {
	Password SignInRoute `json:"password"`
	Key      SignInRoute `json:"key"`
}

// Any reports whether either route is open.
func (s SignIn) Any() bool { return s.Password.Allowed || s.Key.Allowed }

// SSHPolicy is what sshd will do, as it reports it.
type SSHPolicy struct {
	// Installed is false on a host with no SSH server at all.
	Installed bool
	// Known is false when sshd is present but would not report its
	// configuration, in which case only the account's own facts are judged.
	Known        bool
	PasswordAuth bool
	KeyAuth      bool
	PermitEmpty  bool
	UsePAM       bool
	PermitRoot   string
	// AuthorizedKeysFiles is where sshd looks, as it reports it: relative to the
	// home unless absolute, with %h, %u and %U still in it.
	AuthorizedKeysFiles []string
	// KeysCommand is an AuthorizedKeysCommand: keys come from somewhere this cannot
	// count, so "no keys" is never known.
	KeysCommand bool
	AllowUsers  []string
	DenyUsers   []string
	AllowGroups []string
	DenyGroups  []string
}

// sshdCandidates is where a distribution puts the binary. A login shell's PATH
// reaches it, but the dashboard's does not always include sbin.
var sshdCandidates = []string{"/usr/sbin/sshd", "/usr/local/sbin/sshd", "sshd"}

// sshPolicy reads `sshd -T`, resolved for one user when one is named so a Match
// block is in the answer. The listing asks once, without a name, since a Match
// is rare and a process per account is not free on a page that polls.
func (s *Service) sshPolicy(ctx context.Context, username string) SSHPolicy {
	args := []string{"-T"}
	if username != "" {
		// sshd insists on all three connection attributes when -C is used.
		args = append(args, "-C", "user="+username+",host=localhost,addr=127.0.0.1")
	}
	for _, bin := range sshdCandidates {
		out, err := s.run(ctx, "", bin, args...)
		// A socket-activated sshd, Ubuntu's default, has not made this directory
		// until somebody first connects, and -T refuses to answer without it. It is
		// what sshd's own unit would create, so making it changes nothing else.
		if err != nil && strings.Contains(err.Error(), "privilege separation directory") {
			if _, mkErr := s.run(ctx, "", "mkdir", "-p", "-m", "0755", "/run/sshd"); mkErr == nil {
				out, err = s.run(ctx, "", bin, args...)
			}
		}
		if err == nil {
			return parseSSHPolicy(out)
		}
		// Present but not answering — a config it rejects, or a process without
		// the rights to ask — is a different fact from not being there at all.
		if !errors.Is(err, ErrToolMissing) {
			return SSHPolicy{Installed: true}
		}
	}
	return SSHPolicy{}
}

func parseSSHPolicy(dump string) SSHPolicy {
	p := SSHPolicy{Installed: true, Known: true, PasswordAuth: true, KeyAuth: true, UsePAM: true,
		PermitRoot: "prohibit-password"}
	for _, line := range strings.Split(dump, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(key) {
		case "passwordauthentication":
			p.PasswordAuth = value == "yes"
		case "pubkeyauthentication":
			p.KeyAuth = value == "yes"
		case "permitemptypasswords":
			p.PermitEmpty = value == "yes"
		case "usepam":
			p.UsePAM = value == "yes"
		case "permitrootlogin":
			p.PermitRoot = value
		case "authorizedkeysfile":
			p.AuthorizedKeysFiles = strings.Fields(value)
		case "authorizedkeyscommand":
			p.KeysCommand = value != "" && value != "none"
		case "allowusers":
			p.AllowUsers = append(p.AllowUsers, strings.Fields(value)...)
		case "denyusers":
			p.DenyUsers = append(p.DenyUsers, strings.Fields(value)...)
		case "allowgroups":
			p.AllowGroups = append(p.AllowGroups, strings.Fields(value)...)
		case "denygroups":
			p.DenyGroups = append(p.DenyGroups, strings.Fields(value)...)
		}
	}
	return p
}

// admits applies sshd's own order: DenyUsers, AllowUsers, DenyGroups,
// AllowGroups, and the first that says no is the answer.
func (p SSHPolicy) admits(username string, groups []string) bool {
	if matchesUser(p.DenyUsers, username) {
		return false
	}
	if len(p.AllowUsers) > 0 && !matchesUser(p.AllowUsers, username) {
		return false
	}
	if matchesAnyGroup(p.DenyGroups, groups) {
		return false
	}
	if len(p.AllowGroups) > 0 && !matchesAnyGroup(p.AllowGroups, groups) {
		return false
	}
	return true
}

// matchesUser reads sshd's patterns, where `user@host` narrows by origin. A rule
// that depends on where somebody connects from is taken as applying: the page
// says whether the account can get in at all, not from every address.
func matchesUser(patterns []string, username string) bool {
	for _, pattern := range patterns {
		name, _, _ := strings.Cut(pattern, "@")
		if ok, err := filepath.Match(name, username); err == nil && ok {
			return true
		}
	}
	return false
}

func matchesAnyGroup(patterns, groups []string) bool {
	for _, pattern := range patterns {
		for _, group := range groups {
			if ok, err := filepath.Match(pattern, group); err == nil && ok {
				return true
			}
		}
	}
	return false
}

// evaluateSignIn is the whole of the answer, and pure so a test can hand it any
// combination. Each route reports the first thing closing it, in the order an
// operator would have to deal with them.
func evaluateSignIn(u User, policy SSHPolicy) SignIn {
	if blocked := commonBlock(u, policy); blocked != "" {
		return SignIn{Password: SignInRoute{Why: blocked}, Key: SignInRoute{Why: blocked}}
	}
	return SignIn{Password: passwordRoute(u, policy), Key: keyRoute(u, policy)}
}

func commonBlock(u User, policy SSHPolicy) string {
	switch {
	case u.Disabled:
		return WhyDisabled
	case u.PasswordDead:
		return WhyPasswordDead
	case !isLoginShell(u.Shell):
		return WhyNoShell
	case !policy.Installed:
		return WhyNoSSH
	case policy.Known && !policy.admits(u.Username, u.Groups):
		return WhyNotAllowed
	case policy.Known && u.UID == 0 && policy.PermitRoot == "no":
		return WhyRootLoginOff
	}
	return ""
}

func passwordRoute(u User, policy SSHPolicy) SignInRoute {
	if policy.Known {
		if u.UID == 0 && policy.PermitRoot != "yes" {
			return SignInRoute{Why: WhyRootKeysOnly}
		}
		if !policy.PasswordAuth {
			return SignInRoute{Why: WhyPasswordAuthOff}
		}
	}
	switch u.Password {
	case PasswordSet:
		return SignInRoute{Allowed: true}
	case PasswordLocked:
		return SignInRoute{Why: WhyPasswordLocked}
	case PasswordEmpty:
		if policy.Known && !policy.PermitEmpty {
			return SignInRoute{Why: WhyEmptyRefused}
		}
		return SignInRoute{Allowed: true}
	}
	return SignInRoute{Why: WhyNoPassword}
}

func keyRoute(u User, policy SSHPolicy) SignInRoute {
	if policy.Known && !policy.KeyAuth {
		return SignInRoute{Why: WhyKeyAuthOff}
	}
	if u.SSHKeys == 0 {
		if u.KeysUnknown {
			return SignInRoute{Allowed: true, Why: WhyKeysUnknown}
		}
		return SignInRoute{Why: WhyNoKeys}
	}
	// Without PAM, sshd reads "!" in the password field as a locked account and
	// refuses every method. With PAM, which is the usual case, keys still work.
	if policy.Known && !policy.UsePAM && u.Locked {
		return SignInRoute{Why: WhyLockedNoPAM}
	}
	return SignInRoute{Allowed: true}
}
