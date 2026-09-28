package logsx

import "strings"

// The auth lens reads who got in, who tried, and who became root: sshd's
// logins, failures and scanners, sudo and su, PAM sessions, logind's new
// sessions and the account tools. On a public address it is mostly an attack
// — a day of this host's auth.log is 47,747 failed passwords and four
// accepted logins — so the failures are named for grouping by address and
// username but stay at info: the event's tone carries them, and a page of
// amber rows would hide the four lines that matter. Only sudo refusing
// someone and sshd giving up on a client rise to warn.
//
// OpenSSH 9.8 moved per-connection logging to sshd-session and 10.0 added
// sshd-auth; matching the program "sshd" alone misses nearly every line.
func init() {
	register(&Lens{
		ID: "auth",
		Events: []string{"ssh_accepted", "ssh_failed", "ssh_invalid_user", "ssh_preauth_closed",
			"ssh_disconnected", "ssh_max_attempts", "ssh_scan", "session_opened", "session_closed",
			"sudo", "sudo_failed", "su", "account", "login", "cron_session"},
		Attrs: []string{"user", "client", "port", "method", "key", "program", "pid", "command",
			"runas", "tty", "pwd"},
		New: func() Reader { return &authReader{names: sysNames{}} },
	})
}

type authReader struct {
	names sysNames
	// sudoUser and sudoLevel are the sudo line just read: sudo cuts a long
	// command into "(command continued)" lines that belong under it.
	sudoUser, sudoLevel string
}

func (r *authReader) Read(l *Line) {
	m := sysEnvelope(l)
	sysSetOrigin(l, m, r.names)
	sudoUser, sudoLevel := r.sudoUser, r.sudoLevel
	r.sudoUser, r.sudoLevel = "", ""
	msg := m.text
	var (
		event  string
		failed bool
	)
	switch p := m.program; {
	case strings.HasPrefix(p, "sshd"), p == "":
		event = authSSH(l, msg)
	case p == "sudo":
		var user string
		var cont bool
		event, user, cont = authSudo(l, msg)
		if cont {
			if sudoUser != "" && user == sudoUser {
				l.Cont = true
				l.SetLevel(sudoLevel)
				r.sudoUser, r.sudoLevel = sudoUser, sudoLevel
			}
			return
		}
		if event != "" {
			r.sudoUser, r.sudoLevel = user, authLevel(event)
		}
	case p == "su":
		event, failed = authSu(l, msg)
	case p == "systemd-logind":
		event = authLogind(l, msg)
	case authAccountTool(p):
		event = authAccount(l, msg)
	default:
		// login, CRON, the user manager's "(systemd)" and polkit each write
		// their own PAM session lines.
		if strings.HasPrefix(msg, "pam_unix(") {
			event = authPAM(l, msg)
		}
	}
	if event == "" {
		return
	}
	l.Event = event
	if failed {
		l.SetLevel("warn")
	} else {
		l.SetLevel(authLevel(event))
	}
}

func authLevel(event string) string {
	switch event {
	case "sudo_failed", "ssh_max_attempts":
		return "warn"
	case "cron_session":
		return "debug"
	}
	return "info"
}

// authAccountTool names the shadow utilities, which log every account and
// group change they make.
func authAccountTool(program string) bool {
	switch program {
	case "useradd", "usermod", "userdel", "groupadd", "groupmod", "groupdel",
		"passwd", "chpasswd", "gpasswd", "chage", "chsh", "chfn":
		return true
	}
	return false
}

// authSSH names an sshd line. The tests run in the order of how often each
// shape occurs on a host under attack, so the failures that are nine lines
// in ten are decided by the first prefix test.
func authSSH(l *Line, msg string) string {
	switch {
	case strings.HasPrefix(msg, "Failed "):
		method, rest, ok := strings.Cut(msg[len("Failed "):], " for ")
		if !ok || !authFrom(l, strings.TrimPrefix(rest, "invalid user ")) {
			return ""
		}
		l.SetAttr("method", method)
		return "ssh_failed"
	case strings.HasPrefix(msg, "pam_unix("):
		return authPAM(l, msg)
	case strings.HasPrefix(msg, "Invalid user "):
		if authFrom(l, msg[len("Invalid user "):]) {
			return "ssh_invalid_user"
		}
		return ""
	case strings.HasPrefix(msg, "Connection closed by "), strings.HasPrefix(msg, "Connection reset by "):
		return authClosed(l, msg[strings.Index(msg, " by ")+len(" by "):])
	case strings.HasPrefix(msg, "Disconnected from "):
		return authClosed(l, msg[len("Disconnected from "):])
	case strings.HasPrefix(msg, "Disconnecting "):
		// "…port 52826: Too many authentication failures [preauth]": the
		// reason goes, and what is left reads like a closed connection.
		rest := msg[len("Disconnecting "):]
		if end := strings.Index(rest, ": "); end >= 0 {
			rest = rest[:end]
		}
		return authClosed(l, rest)
	case strings.HasPrefix(msg, "Received disconnect from "):
		// The client's goodbye, which sshd follows with the "Disconnected"
		// line that closes the attempt; naming both would count it twice.
		authAddrPort(l, msg[len("Received disconnect from "):])
		return ""
	case strings.HasPrefix(msg, "PAM "):
		// "PAM 5 more authentication failures; … rhost=…  user=root" repeats
		// the Failed lines above it as a tally.
		authRhost(l, msg)
		return ""
	case strings.HasPrefix(msg, "error: maximum authentication attempts exceeded for "):
		rest := msg[len("error: maximum authentication attempts exceeded for "):]
		if authFrom(l, strings.TrimPrefix(rest, "invalid user ")) {
			return "ssh_max_attempts"
		}
		return ""
	case strings.HasPrefix(msg, "Accepted "):
		method, rest, ok := strings.Cut(msg[len("Accepted "):], " for ")
		if !ok || !authFrom(l, rest) {
			return ""
		}
		l.SetAttr("method", method)
		// "…ssh2: ED25519 SHA256:q0M5…" — the fingerprint is what an
		// authorized_keys comment can be matched against.
		if at := strings.Index(rest, " SHA256:"); at >= 0 {
			l.SetAttr("key", sysUntil(rest[at+1:], ' '))
		}
		return "ssh_accepted"
	case strings.HasPrefix(msg, "User ") && strings.Contains(msg, " not allowed because "):
		// AllowUsers and DenyUsers turn a real account away the way a
		// missing one is: sshd treats it as invalid from here on.
		if authFrom(l, msg[len("User "):strings.Index(msg, " not allowed because ")]) {
			return "ssh_invalid_user"
		}
		return ""
	}
	if authScan(l, msg) {
		return "ssh_scan"
	}
	return ""
}

// authScan recognises the connections that never reached a username:
// protocol probes, banner grabs and clients sshd dropped for past behaviour.
// They are a large share of a public host's auth.log and none of them is an
// attempt on an account.
func authScan(l *Line, msg string) bool {
	switch {
	case strings.HasPrefix(msg, "banner exchange: Connection from "):
		authAddrPort(l, msg[len("banner exchange: Connection from "):])
	case strings.HasPrefix(msg, "Unable to negotiate with "):
		authAddrPort(l, msg[len("Unable to negotiate with "):])
	case strings.HasPrefix(msg, "ssh_dispatch_run_fatal: Connection from "):
		authAddrPort(l, msg[len("ssh_dispatch_run_fatal: Connection from "):])
	case strings.HasPrefix(msg, "Did not receive identification string from "):
		authAddrPort(l, msg[len("Did not receive identification string from "):])
	case strings.HasPrefix(msg, "Timeout before authentication for "):
		rest := strings.TrimPrefix(msg[len("Timeout before authentication for "):], "connection from ")
		authAddrPort(l, rest)
	case strings.HasPrefix(msg, "drop connection #") && strings.Contains(msg, " penalty: "):
		// OpenSSH 9.8's PerSourcePenalties: "drop connection #0 from
		// [203.0.113.7]:51202 on [198.51.100.1]:22 penalty: failed
		// authentication" refuses an address that has misbehaved before.
		if open := strings.Index(msg, " from ["); open >= 0 {
			rest := msg[open+len(" from ["):]
			if shut := strings.IndexByte(rest, ']'); shut > 0 {
				l.SetAttr("client", sysAddr(rest[:shut]))
				if strings.HasPrefix(rest[shut:], "]:") {
					l.SetAttr("port", sysDigits(rest[shut+2:]))
				}
			}
		}
	case strings.HasPrefix(msg, "error: kex_exchange_identification: "),
		strings.HasPrefix(msg, "error: kex_protocol_error"),
		strings.HasPrefix(msg, "error: Protocol major versions differ"),
		strings.HasPrefix(msg, "padding error"),
		strings.HasPrefix(msg, "fatal: userauth_"),
		strings.HasPrefix(msg, "Bad protocol version identification"):
	default:
		return false
	}
	return true
}

// authFrom reads "<user> from <address> port <n>", the tail every sshd
// verdict line shares. The user is whatever the client typed, spaces and all,
// so the address is found from the right. Nothing is recorded unless the
// address is one.
func authFrom(l *Line, s string) bool {
	at := strings.LastIndex(s, " from ")
	if at < 0 {
		return false
	}
	addr, rest, _ := strings.Cut(s[at+len(" from "):], " ")
	if sysAddr(addr) == "" {
		return false
	}
	l.SetAttr("user", s[:at])
	l.SetAttr("client", addr)
	if strings.HasPrefix(rest, "port ") {
		l.SetAttr("port", sysDigits(rest[len("port "):]))
	}
	return true
}

// authClosed names the line that ends a connection: "invalid user x
// 203.0.113.7 port 5 [preauth]" ended an attempt on an account, "user ubuntu
// 203.0.113.7 port 5" a session, and a bare address a connection that never
// named anyone.
func authClosed(l *Line, rest string) string {
	rest = strings.TrimSuffix(rest, " [preauth]")
	port := strings.LastIndex(rest, " port ")
	if port < 0 {
		return ""
	}
	head := rest[:port]
	space := strings.LastIndexByte(head, ' ')
	addr := sysAddr(head[space+1:])
	if addr == "" {
		return ""
	}
	who := ""
	if space >= 0 {
		who = head[:space]
	}
	var event, user string
	switch {
	case strings.HasPrefix(who, "invalid user "), strings.HasPrefix(who, "authenticating user "):
		event, user = "ssh_preauth_closed", who[strings.Index(who, "user ")+len("user "):]
	case strings.HasPrefix(who, "user "):
		event, user = "ssh_disconnected", who[len("user "):]
	case who == "":
		event = "ssh_scan"
	default:
		return ""
	}
	l.SetAttr("user", user)
	l.SetAttr("client", addr)
	l.SetAttr("port", sysDigits(rest[port+len(" port "):]))
	return event
}

// authAddrPort reads "<address> port <n>…" or a bare "<address>…".
func authAddrPort(l *Line, s string) {
	addr, rest, _ := strings.Cut(s, " ")
	addr = strings.TrimSuffix(addr, ",")
	if sysAddr(addr) == "" {
		return
	}
	l.SetAttr("client", addr)
	if strings.HasPrefix(rest, "port ") {
		l.SetAttr("port", sysDigits(rest[len("port "):]))
	}
}

// authRhost reads the address and user off a PAM failure, "… ruser=
// rhost=203.0.113.7  user=root". They carry no event — the Failed line beside
// them is the attempt — but they let "only this address" pull in the whole
// conversation.
func authRhost(l *Line, msg string) {
	at := strings.Index(msg, " rhost=")
	if at < 0 {
		return
	}
	host, rest, _ := strings.Cut(msg[at+len(" rhost="):], " ")
	l.SetAttr("client", sysAddr(host))
	if user := strings.Index(rest, "user="); user >= 0 {
		l.SetAttr("user", strings.TrimSpace(rest[user+len("user="):]))
	}
}

// authPAM reads pam_unix's "pam_unix(<service>:<type>): …". Sessions are
// named for every service; cron's are their own event because a host opens
// and closes one every few minutes and they would drown the logins.
func authPAM(l *Line, msg string) string {
	service, rest, ok := strings.Cut(msg[len("pam_unix("):], ":")
	if !ok {
		return ""
	}
	kind, rest, ok := strings.Cut(rest, "): ")
	if !ok {
		return ""
	}
	switch kind {
	case "session":
		var event string
		switch {
		case strings.HasPrefix(rest, "session opened for user "):
			event, rest = "session_opened", rest[len("session opened for user "):]
		case strings.HasPrefix(rest, "session closed for user "):
			event, rest = "session_closed", rest[len("session closed for user "):]
		default:
			return ""
		}
		// "root(uid=0) by root(uid=0)": the user is the session's owner.
		l.SetAttr("user", sysUntil(rest, '('))
		if service == "cron" {
			return "cron_session"
		}
		return event
	case "auth":
		authRhost(l, rest)
	case "chauthtok":
		if strings.HasPrefix(rest, "password changed for ") {
			l.SetAttr("user", rest[len("password changed for "):])
			return "account"
		}
	}
	return ""
}

// authSudo reads "  ubuntu : TTY=pts/4 ; PWD=/ ; USER=root ; COMMAND=…".
// sudo writes no pid, omits TTY for a non-interactive run, and puts the
// reason it refused — "3 incorrect password attempts", "user NOT in sudoers"
// — ahead of the fields, which is the only difference between a refusal and
// a run. COMMAND is last and taken whole: a command can hold " ; " itself.
func authSudo(l *Line, msg string) (event, user string, cont bool) {
	if strings.HasPrefix(msg, "pam_unix(") {
		return authPAM(l, msg), "", false
	}
	user, rest, ok := strings.Cut(strings.TrimLeft(msg, " "), " : ")
	if !ok || user == "" {
		return "", "", false
	}
	if strings.HasPrefix(rest, "(command continued)") {
		return "", user, true
	}
	command := strings.Index(rest, "COMMAND=")
	if command < 0 {
		return "", "", false
	}
	event = "sudo"
	for fields := rest[:command]; fields != ""; {
		var field string
		field, fields, _ = strings.Cut(fields, " ; ")
		field = strings.TrimSpace(field)
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			if field != "" {
				event = "sudo_failed"
			}
			continue
		}
		switch key {
		case "TTY":
			l.SetAttr("tty", value)
		case "PWD":
			l.SetAttr("pwd", value)
		case "USER":
			l.SetAttr("runas", value)
		}
	}
	l.SetAttr("user", user)
	l.SetAttr("command", rest[command+len("COMMAND="):])
	return event, user, false
}

// authSu reads util-linux su's "(to ubuntu) root on pts/0" and its
// "FAILED SU (to root) bob on pts/1": user is who ran su, runas whom they
// became.
func authSu(l *Line, msg string) (event string, failed bool) {
	if strings.HasPrefix(msg, "pam_unix(") {
		return authPAM(l, msg), false
	}
	if strings.HasPrefix(msg, "FAILED SU ") {
		msg, failed = msg[len("FAILED SU "):], true
	}
	if !strings.HasPrefix(msg, "(to ") {
		return "", false
	}
	target, rest, ok := strings.Cut(msg[len("(to "):], ") ")
	if !ok {
		return "", false
	}
	user, tty, _ := strings.Cut(rest, " on ")
	l.SetAttr("user", user)
	l.SetAttr("runas", target)
	if tty != "none" {
		l.SetAttr("tty", tty)
	}
	return "su", failed
}

// authLogind reads "New session 11110 of user ubuntu.", the moment a login
// became a session on the host.
func authLogind(l *Line, msg string) string {
	if !strings.HasPrefix(msg, "New session ") {
		return ""
	}
	at := strings.Index(msg, " of user ")
	if at < 0 {
		return ""
	}
	l.SetAttr("user", strings.TrimSuffix(msg[at+len(" of user "):], "."))
	return "login"
}

// authAccount reads the shadow utilities' own lines. groupadd writes each
// group three times (the group file, gshadow, then "new group") and usermod
// each membership twice (group, then shadow group); one line of each is the
// change, so a new account does not read as three.
func authAccount(l *Line, msg string) string {
	switch {
	case strings.HasPrefix(msg, "new user: name="):
		l.SetAttr("user", sysUntil(msg[len("new user: name="):], ','))
	case strings.HasPrefix(msg, "new group: name="):
	case strings.HasPrefix(msg, "add '"), strings.HasPrefix(msg, "delete '"),
		strings.HasPrefix(msg, "delete user '"), strings.HasPrefix(msg, "change user '"),
		strings.HasPrefix(msg, "changed user '"):
		if strings.Contains(msg, " shadow group ") {
			return ""
		}
		name := msg[strings.IndexByte(msg, '\'')+1:]
		if end := strings.IndexByte(name, '\''); end > 0 {
			l.SetAttr("user", name[:end])
		}
	case strings.HasPrefix(msg, "pam_unix("):
		return authPAM(l, msg)
	default:
		return ""
	}
	return "account"
}
