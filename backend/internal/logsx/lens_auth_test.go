package logsx

import (
	"testing"
	"time"
)

func authSSHD(pid string, more map[string]string) map[string]string {
	attrs := map[string]string{"program": "sshd-session", "pid": pid}
	for k, v := range more {
		attrs[k] = v
	}
	return attrs
}

func TestLensAuthReadsSSH(t *testing.T) {
	sysRead(t, "auth",
		sysWant{
			text:  "2026-09-21T23:33:41.083289+00:00 web-1 sshd-session[2025716]: Accepted password for ubuntu from 100.64.12.7 port 63210 ssh2",
			event: "ssh_accepted", level: "info", at: "2026-09-21T23:33:41.083289Z",
			attrs: authSSHD("2025716", map[string]string{"user": "ubuntu", "client": "100.64.12.7", "port": "63210", "method": "password"}),
		},
		sysWant{
			// The research's publickey example, fingerprint written out.
			text:  "2026-09-21T23:40:02.118731+00:00 web-1 sshd-session[2025802]: Accepted publickey for deploy from 198.51.100.20 port 50122 ssh2: ED25519 SHA256:q0M5HxL6lJ4DfN2Wd0hIuW6xUQY0r3C0yJ4o3m0cP1E",
			event: "ssh_accepted", level: "info", at: "2026-09-21T23:40:02.118731Z",
			attrs: authSSHD("2025802", map[string]string{"user": "deploy", "client": "198.51.100.20", "port": "50122",
				"method": "publickey", "key": "SHA256:q0M5HxL6lJ4DfN2Wd0hIuW6xUQY0r3C0yJ4o3m0cP1E"}),
		},
		sysWant{
			text:  "2026-09-21T23:33:41.085333+00:00 web-1 sshd-session[2025716]: pam_unix(sshd:session): session opened for user ubuntu(uid=1000) by ubuntu(uid=0)",
			event: "session_opened", level: "info", at: "2026-09-21T23:33:41.085333Z",
			attrs: authSSHD("2025716", map[string]string{"user": "ubuntu"}),
		},
		sysWant{
			text:  "2026-09-27T00:21:06.443346+00:00 web-1 sshd-session[3110301]: Invalid user ekala from 203.0.113.42 port 30358",
			event: "ssh_invalid_user", level: "info", at: "2026-09-27T00:21:06.443346Z",
			attrs: authSSHD("3110301", map[string]string{"user": "ekala", "client": "203.0.113.42", "port": "30358"}),
		},
		sysWant{
			text: "2026-09-27T00:21:08.343796+00:00 web-1 sshd-session[3110301]: pam_unix(sshd:auth): check pass; user unknown",
			at:   "2026-09-27T00:21:08.343796Z", attrs: authSSHD("3110301", nil),
		},
		sysWant{
			// The PAM failure repeats the attempt; it names no event but keeps
			// the address, so "only this address" pulls it in.
			text: "2026-09-27T00:21:08.343968+00:00 web-1 sshd-session[3110301]: pam_unix(sshd:auth): authentication failure; logname= uid=0 euid=0 tty=ssh ruser= rhost=203.0.113.42 ",
			at:   "2026-09-27T00:21:08.343968Z", attrs: authSSHD("3110301", map[string]string{"client": "203.0.113.42"}),
		},
		sysWant{
			text:  "2026-09-27T00:21:10.331428+00:00 web-1 sshd-session[3110301]: Failed password for invalid user ekala from 203.0.113.42 port 30358 ssh2",
			event: "ssh_failed", level: "info", at: "2026-09-27T00:21:10.331428Z",
			attrs: authSSHD("3110301", map[string]string{"user": "ekala", "client": "203.0.113.42", "port": "30358", "method": "password"}),
		},
		sysWant{
			text: "2026-09-27T00:21:52.028877+00:00 web-1 sshd-session[3110870]: Received disconnect from 203.0.113.191 port 39706:11: Bye Bye [preauth]",
			at:   "2026-09-27T00:21:52.028877Z", attrs: authSSHD("3110870", map[string]string{"client": "203.0.113.191", "port": "39706"}),
		},
		sysWant{
			text:  "2026-09-27T00:23:26.955440+00:00 web-1 sshd-session[3112372]: Disconnected from invalid user damilare 203.0.113.146 port 47706 [preauth]",
			event: "ssh_preauth_closed", level: "info", at: "2026-09-27T00:23:26.95544Z",
			attrs: authSSHD("3112372", map[string]string{"user": "damilare", "client": "203.0.113.146", "port": "47706"}),
		},
		sysWant{
			text:  "2026-09-27T00:21:02.476539+00:00 web-1 sshd-session[3110074]: Connection closed by invalid user eternum 203.0.113.42 port 19184 [preauth]",
			event: "ssh_preauth_closed", level: "info", at: "2026-09-27T00:21:02.476539Z",
			attrs: authSSHD("3110074", map[string]string{"user": "eternum", "client": "203.0.113.42", "port": "19184"}),
		},
		sysWant{
			text:  "2026-09-27T00:21:31.290670+00:00 web-1 sshd-session[3110500]: Connection closed by authenticating user root 203.0.113.42 port 18384 [preauth]",
			event: "ssh_preauth_closed", level: "info", at: "2026-09-27T00:21:31.29067Z",
			attrs: authSSHD("3110500", map[string]string{"user": "root", "client": "203.0.113.42", "port": "18384"}),
		},
		sysWant{
			text:  "2026-09-27T00:43:28.520719+00:00 web-1 sshd-session[3129119]: Connection reset by invalid user user 203.0.113.197 port 26092 [preauth]",
			event: "ssh_preauth_closed", level: "info", at: "2026-09-27T00:43:28.520719Z",
			attrs: authSSHD("3129119", map[string]string{"user": "user", "client": "203.0.113.197", "port": "26092"}),
		},
		sysWant{
			// An empty username leaves two spaces, and no user.
			text:  "2026-09-20T01:05:15.456184+00:00 web-1 sshd-session[1156468]: Invalid user  from 203.0.113.89 port 59750",
			event: "ssh_invalid_user", level: "info", at: "2026-09-20T01:05:15.456184Z",
			attrs: authSSHD("1156468", map[string]string{"client": "203.0.113.89", "port": "59750"}),
		},
		sysWant{
			text:  "2026-09-27T00:25:01.654921+00:00 web-1 sshd-session[3113695]: Connection closed by 203.0.113.66 port 37418",
			event: "ssh_scan", level: "info", at: "2026-09-27T00:25:01.654921Z",
			attrs: authSSHD("3113695", map[string]string{"client": "203.0.113.66", "port": "37418"}),
		},
		sysWant{
			text:  "2026-09-20T01:18:51.581159+00:00 web-1 sshd-session[1220398]: Connection closed by 203.0.113.69 port 41422 [preauth]",
			event: "ssh_scan", level: "info", at: "2026-09-20T01:18:51.581159Z",
			attrs: authSSHD("1220398", map[string]string{"client": "203.0.113.69", "port": "41422"}),
		},
		sysWant{
			text:  "2026-09-22T22:05:17.614968+00:00 web-1 sshd-session[1423239]: Connection closed by 2001:db8:b000::c2 port 43267",
			event: "ssh_scan", level: "info", at: "2026-09-22T22:05:17.614968Z",
			attrs: authSSHD("1423239", map[string]string{"client": "2001:db8:b000::c2", "port": "43267"}),
		},
		sysWant{
			text:  "2026-09-27T02:03:15.383561+00:00 web-1 sshd-session[3195216]: message repeated 2 times: [ Failed password for root from 203.0.113.197 port 9046 ssh2]",
			event: "ssh_failed", level: "info", at: "2026-09-27T02:03:15.383561Z",
			attrs: authSSHD("3195216", map[string]string{"user": "root", "client": "203.0.113.197", "port": "9046", "method": "password"}),
		},
		sysWant{
			text:  "2026-09-20T13:59:38.425407+00:00 web-1 sshd-session[2956966]: Failed none for invalid user probe from 203.0.113.86 port 30570 ssh2",
			event: "ssh_failed", level: "info", at: "2026-09-20T13:59:38.425407Z",
			attrs: authSSHD("2956966", map[string]string{"user": "probe", "client": "203.0.113.86", "port": "30570", "method": "none"}),
		},
		sysWant{
			text: "2026-09-20T00:55:42.457588+00:00 web-1 sshd-session[1124306]: PAM 4 more authentication failures; logname= uid=0 euid=0 tty=ssh ruser= rhost=203.0.113.152  user=root",
			at:   "2026-09-20T00:55:42.457588Z", attrs: authSSHD("1124306", map[string]string{"client": "203.0.113.152", "user": "root"}),
		},
		sysWant{
			text: "2026-09-27T00:28:00.990469+00:00 web-1 sshd-session[3115986]: PAM service(sshd) ignoring max retries; 6 > 3",
			at:   "2026-09-27T00:28:00.990469Z", attrs: authSSHD("3115986", nil),
		},
		sysWant{
			text:  "2026-09-27T00:29:21.872454+00:00 web-1 sshd-session[3117179]: error: maximum authentication attempts exceeded for invalid user admin from 203.0.113.235 port 57808 ssh2 [preauth]",
			event: "ssh_max_attempts", level: "warn", at: "2026-09-27T00:29:21.872454Z",
			attrs: authSSHD("3117179", map[string]string{"user": "admin", "client": "203.0.113.235", "port": "57808"}),
		},
		sysWant{
			text:  "2026-09-27T00:28:00.989813+00:00 web-1 sshd-session[3115986]: Disconnecting authenticating user root 203.0.113.235 port 44622: Too many authentication failures [preauth]",
			event: "ssh_preauth_closed", level: "info", at: "2026-09-27T00:28:00.989813Z",
			attrs: authSSHD("3115986", map[string]string{"user": "root", "client": "203.0.113.235", "port": "44622"}),
		},
		sysWant{
			text:  "2026-09-22T03:27:21.090479+00:00 web-1 sshd-session[2025895]: Disconnected from user ubuntu 100.64.12.7 port 63210",
			event: "ssh_disconnected", level: "info", at: "2026-09-22T03:27:21.090479Z",
			attrs: authSSHD("2025895", map[string]string{"user": "ubuntu", "client": "100.64.12.7", "port": "63210"}),
		},
		sysWant{
			text:  "2026-09-22T03:27:21.092370+00:00 web-1 sshd-session[2025716]: pam_unix(sshd:session): session closed for user ubuntu",
			event: "session_closed", level: "info", at: "2026-09-22T03:27:21.09237Z",
			attrs: authSSHD("2025716", map[string]string{"user": "ubuntu"}),
		},
		sysWant{
			// Not an attempt, and the word "error" in it is not the verdict.
			text: "2026-09-22T03:27:21.099284+00:00 web-1 sshd-session[2025716]: syslogin_perform_logout: logout() returned an error",
			at:   "2026-09-22T03:27:21.099284Z", level: "error", attrs: authSSHD("2025716", nil),
		},
	)
}

// A public host's scanners: every one stays at info, whatever word sshd put
// in front of it — "error:" and "fatal:" included.
func TestLensAuthKeepsScannersQuiet(t *testing.T) {
	sysRead(t, "auth",
		sysWant{
			text:  "2026-09-27T01:47:43.061493+00:00 web-1 sshd-session[3182567]: error: kex_exchange_identification: read: Connection reset by peer",
			event: "ssh_scan", level: "info", at: "2026-09-27T01:47:43.061493Z", attrs: authSSHD("3182567", nil),
		},
		sysWant{
			text:  "2026-09-27T01:04:39.414271+00:00 web-1 sshd-session[3147038]: banner exchange: Connection from 192.0.2.169 port 49643: invalid format",
			event: "ssh_scan", level: "info", at: "2026-09-27T01:04:39.414271Z",
			attrs: authSSHD("3147038", map[string]string{"client": "192.0.2.169", "port": "49643"}),
		},
		sysWant{
			text:  "2026-09-27T01:06:14.197729+00:00 web-1 sshd-session[3148279]: Unable to negotiate with 192.0.2.92 port 48001: no matching host key type found. Their offer: ssh-rsa,ssh-dss [preauth]",
			event: "ssh_scan", level: "info", at: "2026-09-27T01:06:14.197729Z",
			attrs: authSSHD("3148279", map[string]string{"client": "192.0.2.92", "port": "48001"}),
		},
		sysWant{
			text:  "2026-09-24T13:49:06.844320+00:00 web-1 sshd-session[1333543]: error: kex_protocol_error: type 20 seq 2 [preauth]",
			event: "ssh_scan", level: "info", at: "2026-09-24T13:49:06.84432Z", attrs: authSSHD("1333543", nil),
		},
		sysWant{
			text:  "2026-09-27T04:14:12.290575+00:00 web-1 sshd-session[3303387]: error: Protocol major versions differ: 2 vs. 1",
			event: "ssh_scan", level: "info", at: "2026-09-27T04:14:12.290575Z", attrs: authSSHD("3303387", nil),
		},
		sysWant{
			text:  "2026-09-22T17:24:15.522730+00:00 web-1 sshd-session[598086]: padding error: need 436 block 8 mod 4 [preauth]",
			event: "ssh_scan", level: "info", at: "2026-09-22T17:24:15.52273Z", attrs: authSSHD("598086", nil),
		},
		sysWant{
			text:  "2026-09-22T17:24:15.522929+00:00 web-1 sshd-session[598086]: ssh_dispatch_run_fatal: Connection from 192.0.2.207 port 57268: message authentication code incorrect [preauth]",
			event: "ssh_scan", level: "info", at: "2026-09-22T17:24:15.522929Z",
			attrs: authSSHD("598086", map[string]string{"client": "192.0.2.207", "port": "57268"}),
		},
		sysWant{
			text:  "2026-09-27T02:30:53.034124+00:00 web-1 sshd-session[3217928]: fatal: userauth_pubkey: parse publickey packet: incomplete message [preauth]",
			event: "ssh_scan", level: "info", at: "2026-09-27T02:30:53.034124Z", attrs: authSSHD("3217928", nil),
		},
		sysWant{
			text:  "2026-09-20T01:06:03.433763+00:00 web-1 sshd[2450808]: Timeout before authentication for connection from 192.0.2.69 to 198.51.100.87, pid = 1152348",
			event: "ssh_scan", level: "info", at: "2026-09-20T01:06:03.433763Z",
			attrs: map[string]string{"program": "sshd", "pid": "2450808", "client": "192.0.2.69"},
		},
		sysWant{
			text:  "2026-09-27T07:22:22.456816+00:00 web-1 sshd[2450808]: drop connection #1 from [192.0.2.114]:34666 on [198.51.100.87]:22 penalty: exceeded LoginGraceTime",
			event: "ssh_scan", level: "info", at: "2026-09-27T07:22:22.456816Z",
			attrs: map[string]string{"program": "sshd", "pid": "2450808", "client": "192.0.2.114", "port": "34666"},
		},
		sysWant{
			// The daemon running out of room is not a scanner; it keeps the
			// error its text states.
			text: "2026-09-21T03:59:33.524808+00:00 web-1 sshd[2450808]: error: beginning MaxStartups throttling",
			at:   "2026-09-21T03:59:33.524808Z", level: "error", attrs: map[string]string{"program": "sshd", "pid": "2450808"},
		},
	)
}

func TestLensAuthReadsSudoSuAndAccounts(t *testing.T) {
	sysInZone(t)
	sudo := func(more map[string]string) map[string]string {
		attrs := map[string]string{"program": "sudo"}
		for k, v := range more {
			attrs[k] = v
		}
		return attrs
	}
	sysRead(t, "auth",
		sysWant{
			text:  "2026-09-17T02:37:48.976605+00:00 web-1 sudo:     root : TTY=pts/4 ; PWD=/ ; USER=root ; COMMAND=/usr/bin/systemctl kill --signal=SIGUSR1 nordvpnd.service",
			event: "sudo", level: "info", at: "2026-09-17T02:37:48.976605Z",
			attrs: sudo(map[string]string{"user": "root", "tty": "pts/4", "pwd": "/", "runas": "root",
				"command": "/usr/bin/systemctl kill --signal=SIGUSR1 nordvpnd.service"}),
		},
		sysWant{
			// Non-interactive: no TTY.
			text:  "2026-09-20T03:16:40.973808+00:00 web-1 sudo:   ubuntu : PWD=/home/ubuntu/Just-Dashboard ; USER=root ; COMMAND=/usr/bin/ss -tlnp",
			event: "sudo", level: "info", at: "2026-09-20T03:16:40.973808Z",
			attrs: sudo(map[string]string{"user": "ubuntu", "pwd": "/home/ubuntu/Just-Dashboard", "runas": "root",
				"command": "/usr/bin/ss -tlnp"}),
		},
		sysWant{
			// sudo's split of a long command folds under the line it continues.
			text: "2026-09-20T03:16:40.973830+00:00 web-1 sudo:   ubuntu : (command continued) /var/lib/just-dashboard/",
			at:   "2026-09-20T03:16:40.97383Z", level: "info", cont: true, attrs: sudo(nil),
		},
		sysWant{
			text:  "2026-09-20T03:16:13.171168+00:00 web-1 sudo: pam_unix(sudo:session): session opened for user root(uid=0) by (uid=1000)",
			event: "session_opened", level: "info", at: "2026-09-20T03:16:13.171168Z",
			attrs: sudo(map[string]string{"user": "root"}),
		},
		sysWant{
			// Not following its sudo line, a continuation stands alone.
			text: "2026-09-20T03:16:13.174390+00:00 web-1 sudo:   ubuntu : (command continued) --no-pager",
			at:   "2026-09-20T03:16:13.17439Z", attrs: sudo(nil),
		},
		sysWant{
			// The research's refusal shapes, in the BSD envelope older hosts
			// write: local time, not UTC.
			text:  "Sep  7 03:12:01 web-1 sudo:      bob : 3 incorrect password attempts ; TTY=pts/1 ; PWD=/home/bob ; USER=root ; COMMAND=/usr/bin/apt update",
			event: "sudo_failed", level: "warn", at: sysBSDWant(time.September, 7, 3, 12, 1),
			attrs: sudo(map[string]string{"user": "bob", "tty": "pts/1", "pwd": "/home/bob", "runas": "root", "command": "/usr/bin/apt update"}),
		},
		sysWant{
			text:  "Sep  7 03:13:40 web-1 sudo:      eve : user NOT in sudoers ; TTY=pts/2 ; PWD=/home/eve ; USER=root ; COMMAND=/bin/bash",
			event: "sudo_failed", level: "warn", at: sysBSDWant(time.September, 7, 3, 13, 40),
			attrs: sudo(map[string]string{"user": "eve", "tty": "pts/2", "pwd": "/home/eve", "runas": "root", "command": "/bin/bash"}),
		},
		sysWant{
			text:  "2026-09-25T18:28:10.032355+00:00 web-1 su[1249353]: (to ubuntu) root on pts/4",
			event: "su", level: "info", at: "2026-09-25T18:28:10.032355Z",
			attrs: map[string]string{"program": "su", "pid": "1249353", "user": "root", "runas": "ubuntu", "tty": "pts/4"},
		},
		sysWant{
			text:  "Sep  7 03:14:22 web-1 su[4102]: FAILED SU (to root) bob on pts/1",
			event: "su", level: "warn", at: sysBSDWant(time.September, 7, 3, 14, 22),
			attrs: map[string]string{"program": "su", "pid": "4102", "user": "bob", "runas": "root", "tty": "pts/1"},
		},
		sysWant{
			text:  "2026-09-20T03:15:26.557615+00:00 web-1 su[1664510]: pam_unix(su-l:session): session opened for user ubuntu(uid=1000) by (uid=0)",
			event: "session_opened", level: "info", at: "2026-09-20T03:15:26.557615Z",
			attrs: map[string]string{"program": "su", "pid": "1664510", "user": "ubuntu"},
		},
		sysWant{
			text:  "2026-09-25T18:28:10.043282+00:00 web-1 systemd-logind[917]: New session c503 of user ubuntu.",
			event: "login", level: "info", at: "2026-09-25T18:28:10.043282Z",
			attrs: map[string]string{"program": "systemd-logind", "pid": "917", "user": "ubuntu"},
		},
		sysWant{
			text: "2026-09-20T02:06:51.252241+00:00 web-1 systemd-logind[917]: Removed session c178.",
			at:   "2026-09-20T02:06:51.252241Z", attrs: map[string]string{"program": "systemd-logind", "pid": "917"},
		},
		sysWant{
			text:  "2026-09-20T00:55:01.236375+00:00 web-1 CRON[1123363]: pam_unix(cron:session): session opened for user root(uid=0) by root(uid=0)",
			event: "cron_session", level: "debug", at: "2026-09-20T00:55:01.236375Z",
			attrs: map[string]string{"program": "CRON", "pid": "1123363", "user": "root"},
		},
		sysWant{
			text:  "2026-09-25T12:31:42.299948+00:00 web-1 groupadd[3513615]: new group: name=ssl-cert, GID=111",
			event: "account", level: "info", at: "2026-09-25T12:31:42.299948Z",
			attrs: map[string]string{"program": "groupadd", "pid": "3513615"},
		},
		sysWant{
			text: "2026-09-25T12:31:42.296392+00:00 web-1 groupadd[3513615]: group added to /etc/group: name=ssl-cert, GID=111",
			at:   "2026-09-25T12:31:42.296392Z", attrs: map[string]string{"program": "groupadd", "pid": "3513615"},
		},
		sysWant{
			text:  "2026-09-25T12:31:44.927448+00:00 web-1 useradd[3514538]: new user: name=postgres, UID=108, GID=112, home=/var/lib/postgresql, shell=/bin/bash, from=none",
			event: "account", level: "info", at: "2026-09-25T12:31:44.927448Z",
			attrs: map[string]string{"program": "useradd", "pid": "3514538", "user": "postgres"},
		},
		sysWant{
			text:  "2026-09-25T12:31:45.096972+00:00 web-1 usermod[3514570]: add 'postgres' to group 'ssl-cert'",
			event: "account", level: "info", at: "2026-09-25T12:31:45.096972Z",
			attrs: map[string]string{"program": "usermod", "pid": "3514570", "user": "postgres"},
		},
		sysWant{
			text: "2026-09-25T12:31:45.097071+00:00 web-1 usermod[3514570]: add 'postgres' to shadow group 'ssl-cert'",
			at:   "2026-09-25T12:31:45.097071Z", attrs: map[string]string{"program": "usermod", "pid": "3514570"},
		},
		sysWant{
			text:  "2026-09-25T12:31:44.947429+00:00 web-1 chfn[3514543]: changed user 'postgres' information",
			event: "account", level: "info", at: "2026-09-25T12:31:44.947429Z",
			attrs: map[string]string{"program": "chfn", "pid": "3514543", "user": "postgres"},
		},
		sysWant{
			text:  "Sep  7 03:20:05 web-1 passwd[4410]: pam_unix(passwd:chauthtok): password changed for bob",
			event: "account", level: "info", at: sysBSDWant(time.September, 7, 3, 20, 5),
			attrs: map[string]string{"program": "passwd", "pid": "4410", "user": "bob"},
		},
	)
}

// journal:ssh.service and the whole journal hand the lens a bare message with
// the program already in Attrs, and a priority that the lens overrules for a
// scanner sshd logged at LOG_ERR.
func TestLensAuthReadsTheJournal(t *testing.T) {
	got := sysReadJournal(t, "auth",
		sysEntry{"Failed password for root from 203.0.113.186 port 62760 ssh2", 6,
			map[string]string{"program": "sshd-session", "pid": "1122396", "unit": "ssh.service"}},
		sysEntry{"error: kex_exchange_identification: read: Connection reset by peer", 3,
			map[string]string{"program": "sshd-session", "pid": "3182567", "unit": "ssh.service"}},
		sysEntry{"  ubuntu : PWD=/home/ubuntu/Just-Dashboard ; USER=root ; COMMAND=/usr/bin/ss -tlnp", 5,
			map[string]string{"program": "sudo"}},
	)
	sysCheck(t, got, []sysWant{
		{text: "failed", event: "ssh_failed", level: "info", attrs: map[string]string{"program": "sshd-session", "pid": "1122396",
			"unit": "ssh.service", "user": "root", "client": "203.0.113.186", "port": "62760", "method": "password"}},
		{text: "kex", event: "ssh_scan", level: "info", attrs: map[string]string{"program": "sshd-session", "pid": "3182567", "unit": "ssh.service"}},
		{text: "sudo", event: "sudo", level: "info", attrs: map[string]string{"program": "sudo", "user": "ubuntu",
			"pwd": "/home/ubuntu/Just-Dashboard", "runas": "root", "command": "/usr/bin/ss -tlnp"}},
	})
}

func BenchmarkLensAuth(b *testing.B) {
	benchmarkSysLens(b, "auth", []string{
		"2026-09-27T00:21:06.443346+00:00 web-1 sshd-session[3110301]: Invalid user ekala from 203.0.113.42 port 30358",
		"2026-09-27T00:21:08.343796+00:00 web-1 sshd-session[3110301]: pam_unix(sshd:auth): check pass; user unknown",
		"2026-09-27T00:21:08.343968+00:00 web-1 sshd-session[3110301]: pam_unix(sshd:auth): authentication failure; logname= uid=0 euid=0 tty=ssh ruser= rhost=203.0.113.42 ",
		"2026-09-27T00:21:10.331428+00:00 web-1 sshd-session[3110301]: Failed password for invalid user ekala from 203.0.113.42 port 30358 ssh2",
		"2026-09-27T00:21:52.028877+00:00 web-1 sshd-session[3110870]: Received disconnect from 203.0.113.191 port 39706:11: Bye Bye [preauth]",
		"2026-09-27T00:23:26.955440+00:00 web-1 sshd-session[3112372]: Disconnected from invalid user damilare 203.0.113.146 port 47706 [preauth]",
		"2026-09-27T00:21:31.290670+00:00 web-1 sshd-session[3110500]: Connection closed by authenticating user root 203.0.113.42 port 18384 [preauth]",
		"2026-09-20T00:55:01.236375+00:00 web-1 CRON[1123363]: pam_unix(cron:session): session opened for user root(uid=0) by root(uid=0)",
	})
}
