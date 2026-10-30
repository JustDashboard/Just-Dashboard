package term

import (
	"bufio"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"
)

// This file answers one question: what does "open a terminal" mean on a
// machine where the dashboard is a container?
//
// The honest answer is that it has to mean the same thing as `ssh` to the
// server, because that is the only definition an operator can predict. A shell
// that lands in the image instead has none of what makes the box theirs: not
// their tools (nvim, node, a language runtime installed under their home), not
// their account, not their home directory, not the PATH their dotfiles build.
// It looks like a terminal and answers to `ls`, which is worse than not being
// there — it invites you to trust it.
//
// So the session is assembled the way sshd assembles one: pick the account,
// cross into the host's namespaces, become that account, and exec its login
// shell. Every piece below exists to make one of those four true.

// Account is who the shell runs as — the same set of facts sshd reads out of
// /etc/passwd before it hands over a session.
type Account struct {
	Name  string
	UID   int
	GID   int
	Home  string
	Shell string
}

// IsRoot reports whether this account is the superuser, which is the one case
// where becoming somebody else is unnecessary.
func (a Account) IsRoot() bool { return a.UID == 0 }

// resolveAccount decides which account a terminal session belongs to.
//
// A configured name wins and is never silently substituted: an operator who
// writes JD_TERMINAL_USER=deploy and gets a root shell instead has been handed
// the opposite of what they asked for, so an unknown name is an error the
// terminal reports rather than a default it papers over.
//
// With nothing configured, the account is the lowest regular UID on the host —
// on a VPS that is the login the provider created (`ubuntu`, `debian`,
// `admin`), which is the account whose password the operator holds and whose
// home holds their dotfiles. It is the same account `ssh` would land in, which
// is exactly the promise this terminal is trying to keep.
func resolveAccount(configured string) (Account, error) {
	if configured != "" {
		u, err := user.Lookup(configured)
		if err != nil {
			return Account{}, fmt.Errorf("JD_TERMINAL_USER: no such account %q on this host", configured)
		}
		return accountFrom(u.Username, u.Uid, u.Gid, u.HomeDir, loginShellOf(u.Username))
	}
	if a, ok := lowestRegularAccount(); ok {
		return a, nil
	}
	// A server with no regular account at all — a minimal image, or a box
	// administered purely as root. Root is then not a fallback so much as the
	// only correct answer.
	u, err := user.LookupId("0")
	if err != nil {
		return Account{}, fmt.Errorf("no usable account for a terminal session: %w", err)
	}
	return accountFrom(u.Username, u.Uid, u.Gid, u.HomeDir, loginShellOf(u.Username))
}

// resolveLoginAccount is one session's account rather than every session's. A
// nologin shell is refused, not worked around: su would print "This account is
// currently not available" and the terminal would close, and forcing a shell on it
// is exactly what that account's shell is there to prevent.
func resolveLoginAccount(name string) (Account, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return Account{}, fmt.Errorf("%w: %s", ErrNoSuchAccount, name)
	}
	shell := loginShellOf(u.Username)
	if !isLoginShell(shell) {
		return Account{}, fmt.Errorf("%w: %s", ErrAccountCannotLogin, name)
	}
	return accountFrom(u.Username, u.Uid, u.Gid, u.HomeDir, shell)
}

func accountFrom(name, uid, gid, home, shell string) (Account, error) {
	n, err := strconv.Atoi(uid)
	if err != nil {
		return Account{}, fmt.Errorf("account %q has a non-numeric uid %q", name, uid)
	}
	g, err := strconv.Atoi(gid)
	if err != nil {
		return Account{}, fmt.Errorf("account %q has a non-numeric gid %q", name, gid)
	}
	if home == "" {
		home = "/"
	}
	return Account{Name: name, UID: n, GID: g, Home: home, Shell: shell}, nil
}

// passwdPath is the host's account database. The backend container bind-mounts
// the host's /etc at its real name, so this is the server's file and not the
// image's handful of system accounts — the same reason the file manager can
// see the real /home.
var passwdFile = "/etc/passwd"

// lowestRegularAccount picks the first human account on the host.
//
// "Regular" is the same test `adduser` and the login utilities use: a UID at
// or above 1000 and below the nobody sentinel, with a shell that actually
// starts a session. Service accounts are created with /usr/sbin/nologin
// precisely so that things like this skip them, and honouring that is what
// keeps the default from landing an operator in `postgres`.
func lowestRegularAccount() (Account, bool) {
	f, err := os.Open(passwdFile)
	if err != nil {
		return Account{}, false
	}
	defer f.Close()

	best := Account{UID: -1}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), ":")
		if len(fields) < 7 {
			continue
		}
		uid, err := strconv.Atoi(fields[2])
		if err != nil || uid < 1000 || uid >= 65534 {
			continue
		}
		if !isLoginShell(fields[6]) {
			continue
		}
		if best.UID == -1 || uid < best.UID {
			gid, err := strconv.Atoi(fields[3])
			if err != nil {
				continue
			}
			home := fields[5]
			if home == "" {
				home = "/"
			}
			best = Account{Name: fields[0], UID: uid, GID: gid, Home: home, Shell: fields[6]}
		}
	}
	if best.UID == -1 {
		return Account{}, false
	}
	return best, true
}

// isLoginShell rejects the shells that exist to refuse a session.
func isLoginShell(shell string) bool {
	switch {
	case shell == "":
		return false
	case strings.HasSuffix(shell, "/nologin"):
		return false
	case strings.HasSuffix(shell, "/false"):
		return false
	case strings.HasSuffix(shell, "/sync"):
		return false
	}
	return true
}

// loginShellOf reads an account's shell straight from the host's passwd file.
//
// os/user does not carry the shell field, and guessing /bin/bash is how an
// operator whose login is zsh or fish ends up in a shell that ignores every
// dotfile they have written.
func loginShellOf(name string) string {
	f, err := os.Open(passwdFile)
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), ":")
		if len(fields) >= 7 && fields[0] == name {
			return fields[6]
		}
	}
	return ""
}

// loginArgv is the command that becomes a session for this account.
//
// `su -` is the whole login sequence in one word, and deliberately not
// reimplemented here: it switches user, resets the environment to that
// account's, exports HOME/USER/LOGNAME/SHELL, changes to the home directory
// and execs the login shell. Reproducing those by hand is how a terminal ends
// up subtly unlike ssh — the right user but the wrong PATH, or the right PATH
// but a shell that never read .bashrc.
//
// The argument vector is literal. Nothing the caller supplies is interpolated
// into a string for a shell to parse, which is what keeps the promise the rest
// of this codebase makes about host commands.
func (a Account) loginArgv(shellOverride string, keepCWD bool) []string {
	shell := shellOverride
	if shell == "" {
		shell = a.Shell
	}
	if shell == "" {
		shell = "/bin/bash"
	}
	// Nobody to become: either the target account is already ours, or we are
	// not root and su would demand a password no one is there to type. Exec
	// the login shell directly — `-l` is the half of "like ssh" that still
	// applies, since it is what makes the shell read the profile. A login
	// *shell* does not change directory (that is su's doing, below), so this
	// form already honours whatever working directory it is started in.
	if a.IsRoot() || os.Geteuid() != 0 {
		return []string{shell, "-l"}
	}
	// Options come before the user: su passes anything *after* the name on to
	// the shell as arguments, so `su - name -s sh` would hand the shell a
	// stray `-s` instead of choosing one.
	if !keepCWD {
		if shellOverride != "" {
			return []string{"su", "-s", shell, "-l", a.Name}
		}
		return []string{"su", "-l", a.Name}
	}
	// "Open a shell *here*" — from a compose stack, a repository, a build
	// context — and `su -l` is the one thing that cannot deliver it: login is
	// defined as chdir-to-home, so it lands in the home directory no matter
	// what directory it was started in. That is why setting tmux's `-c` was
	// not enough on a host where the terminal runs as a regular account: tmux
	// put the pane in the right place and su moved it straight back.
	//
	// So the chdir is moved off su and onto the shell. Without `-l`, su still
	// switches user and still sets HOME, USER, SHELL and LOGNAME for the
	// target account — it simply does not change directory — and the `-l`
	// passed through to the shell after `--` is what reads the profile and
	// gives the same PATH a login would. The result is a login shell in the
	// requested directory, assembled entirely from argv: the path is an
	// argument, never text for a shell to parse.
	return []string{"su", "-s", shell, a.Name, "--", "-l"}
}
