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
