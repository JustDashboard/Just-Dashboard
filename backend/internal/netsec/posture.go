package netsec

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The security posture of the host, as a verdict rather than as facts.
//
// The rest of this page shows what is configured: a rule list, a jail, a set
// of sshd directives, a table of open ports. Reading all of that and deciding
// whether the machine is in reasonable shape is a skill, and the people who
// most need the answer are exactly the people who do not have it. Every
// competitor in this space either shows the facts and stops (Cockpit, Webmin)
// or sells a "security score" that is a number with no working attached.
//
// This is the same shape as metrics.Assess and dockerx.Diagnose, for the same
// reason: what was measured, what it means, and what to do about it, as three
// separate fields, so the verdict can be argued with rather than merely
// obeyed. Nothing here is a score out of a hundred — a number invites people
// to optimise the number.
//
// Assess is a pure function of everything it judges. The handler gathers the
// inputs; this decides. That is what makes the rules testable without a
// firewall, an sshd or a network.
type Posture struct {
	// Status is the worst level present, or "ok".
	Status    string            `json:"status"`
	Findings  []SecurityFinding `json:"findings"`
	CheckedAt time.Time         `json:"checkedAt"`
	// Checks is how many rules ran, so "nothing to report" can be told apart
	// from "nothing was examined".
	Checks int `json:"checks"`
	// Skipped names the checks that could not run because the thing they
	// examine is not present on this host. A check that silently did not run
	// looks exactly like a check that passed.
	Skipped []string `json:"skipped"`
}

// SecurityFinding is one thing worth telling the operator about how exposed
// they are.
type SecurityFinding struct {
	// ID is stable for the same condition, so a client can keep a dismissal
	// or an expanded row attached across polls.
	ID    string `json:"id"`
	Level string `json:"level"`
	Title string `json:"title"`
	// Detail is what was measured; Advice is what to do about it. Separate
	// because the first is a fact and the second is an opinion.
	Detail string `json:"detail"`
	Advice string `json:"advice,omitempty"`
	// Area groups findings by the panel that can fix them: exposure,
	// firewall, ssh, intrusion, ports, tls, updates.
	Area string `json:"area"`
	// Fix names a remedy the dashboard can carry out itself, which the UI
	// turns into a button. Empty means the fix is somewhere else.
	Fix      string `json:"fix,omitempty"`
	FixLabel string `json:"fixLabel,omitempty"`
}

// ExposedPort is the minimum netsec needs to know about a listening socket.
// Declared here rather than imported from proxysvc so the audit stays a pure
// function with no dependency on how ports are discovered.
type ExposedPort struct {
	Port     uint32
	Protocol string
	Address  string
	Process  string
	Exposed  bool
}

// CertSummary is the minimum netsec needs about a certificate.
type CertSummary struct {
	Name     string
	DaysLeft int
	Expired  bool
}

// AssessInput is everything the verdict is computed from. A nil pointer means
// "this could not be established", which is different from a zero value and
// is reported as a skipped check rather than a pass.
type AssessInput struct {
	Exposure     *Exposure
	Firewall     *FirewallStatus
	Fail2ban     *Fail2banStatus
	SSH          *SSHDConfig
	Listeners    []ExposedPort
	Certificates []CertSummary
	// FailedLogins is how many failed attempts the host recorded inside
	// FailedLoginWindow, and RecentBans how many bans fail2ban issued.
	// FailedLoginsCapped says the sample ran out before the window did, so
	// the count is a floor — which is worth saying rather than quoting a
	// number that stopped counting.
	FailedLogins       int
	FailedLoginsCapped bool
	FailedLoginWindow  time.Duration
	RecentBans         int
	// LoginRecordRead says whether btmp could be read at all. Zero failed
	// attempts and "the tool that counts them is not installed" are the same
	// number and opposite facts — `last` lives in util-linux-extra, which a
	// minimal cloud image does not have — so the count is only quoted when
	// something actually counted.
	LoginRecordRead bool
	// SecurityUpdates is the count of pending updates from the security
	// pocket; RebootRequired is the flag the package manager leaves.
	SecurityUpdates int
	RebootRequired  bool
	// PackageManager names what runs the host, empty when none was found.
	// SecurityFiltering says whether it can tell a security update from any
	// other — Alpine and Arch publish no advisory data, and a count of zero
	// from them means "cannot tell", not "none outstanding". A verdict that
	// reads the two the same way reports a clean bill of health on every
	// Alpine and Arch server, which is the failure this check exists to
	// prevent rather than one to reproduce.
	PackageManager    string
	SecurityFiltering bool
	Now               time.Time
}

// Thresholds. Each is a claim about what is bad, and a claim deserves one
// place to be read and argued with.
const (
	// Failed logins inside the assessed window that stop being background
	// noise and start being a campaign. An internet-facing host with password
	// authentication on will pass the first within a day.
	//
	// Both are counts over a window, and both were previously compared
	// against the length of a 500-record listing of the whole of btmp — so
	// the warning was unreachable and the notice was permanent on any host
	// that had ever accumulated 200 attempts.
	failedLoginNoticeCount  = 200
	failedLoginWarningCount = 2000
	// A certificate this close to expiry, on a host where nothing has renewed
	// it, is an outage with a date on it.
	certWarningDays  = 14
	certCriticalDays = 3
	// Pending security updates worth interrupting somebody for.
	securityUpdateWarningCount = 10
)

// Assess grades the host, worst first.
func Assess(in AssessInput) *Posture {
	if in.Now.IsZero() {
		in.Now = time.Now()
	}
	p := &Posture{Findings: []SecurityFinding{}, CheckedAt: in.Now.UTC(), Skipped: []string{}}

	p.add(assessExposure(in))
	p.add(assessFirewall(in))
	p.add(assessSSH(in))
	p.add(assessIntrusion(in))
	p.add(assessPorts(in))
	p.add(assessCertificates(in))
	p.add(assessUpdates(in))

	p.Checks = 7
	if in.Firewall == nil || !in.Firewall.Available {
		p.Skipped = append(p.Skipped, "firewall")
	}
	if in.SSH == nil || !in.SSH.Available {
		p.Skipped = append(p.Skipped, "ssh")
	}
	if in.Fail2ban == nil || !in.Fail2ban.Available {
		p.Skipped = append(p.Skipped, "fail2ban")
	}
	if in.PackageManager == "" || !in.SecurityFiltering {
		p.Skipped = append(p.Skipped, "security updates")
	}
	if !in.LoginRecordRead {
		p.Skipped = append(p.Skipped, "failed logins")
	}

	// Worst first, then by area so a page of warnings does not reshuffle
	// itself between polls.
	sort.SliceStable(p.Findings, func(i, j int) bool {
		if levelRank(p.Findings[i].Level) != levelRank(p.Findings[j].Level) {
			return levelRank(p.Findings[i].Level) > levelRank(p.Findings[j].Level)
		}
		return p.Findings[i].ID < p.Findings[j].ID
	})
	p.Status = "ok"
	for _, f := range p.Findings {
		if levelRank(f.Level) > levelRank(p.Status) {
			p.Status = f.Level
		}
	}
	return p
}

func (p *Posture) add(findings []SecurityFinding) {
	p.Findings = append(p.Findings, findings...)
}

func levelRank(level string) int {
	switch level {
	case "critical":
		return 3
	case "warning":
		return 2
	case "notice":
		return 1
	}
	return 0
}

func assessExposure(in AssessInput) []SecurityFinding {
	if in.Exposure == nil {
		return nil
	}
	switch in.Exposure.Grade {
	case "open":
		return []SecurityFinding{{
			ID: "exposure.open", Level: "critical", Area: "exposure",
			Title:  "The dashboard admits every address on the internet",
			Detail: "The allowlist contains a default route, so the network layer refuses nobody.",
			Advice: "Narrow JD_ALLOWED_CIDRS to the addresses you actually use, or put the panel on Tailscale. This is a root-equivalent control panel; the login page should not be something a scanner can find.",
		}}
	case "public":
		return []SecurityFinding{{
			ID: "exposure.public", Level: "warning", Area: "exposure",
			Title:  "The dashboard is reachable from public addresses",
			Detail: "Allowlist: " + strings.Join(in.Exposure.Allowlist, ", "),
			Advice: in.Exposure.Recommendation,
		}}
	}
	return nil
}

func assessFirewall(in AssessInput) []SecurityFinding {
	fw := in.Firewall
	if fw == nil || !fw.Available {
		return []SecurityFinding{{
			ID: "firewall.absent", Level: "warning", Area: "firewall",
			Title:  "No host firewall",
			Detail: "None of ufw, firewalld or iptables answered on this host.",
			// Named per family rather than "install ufw", which is the wrong
			// package on more than half the distributions this now runs on
			// and reads as advice from somebody who assumed Debian.
			Advice: "Install ufw on Debian and Ubuntu, or firewalld on Fedora, RHEL and openSUSE. Without one, every port anything on this machine opens is reachable from wherever the machine is reachable — including the ones a container publishes by accident.",
		}}
	}
	out := []SecurityFinding{}
	if !fw.Enabled {
		out = append(out, SecurityFinding{
			ID: "firewall.disabled", Level: "critical", Area: "firewall",
			Title:  "The firewall is installed but switched off",
			Detail: fmt.Sprintf("%s reports itself inactive with %d rule(s) configured.", fw.Backend, len(fw.Rules)),
			Advice: "Rules that are not being enforced are worse than no rules, because the page looks configured. Check that the rules admit the port you are reading this on, then enable it.",
			// Only offered where the dashboard can actually do it. A button
			// that always returns "not supported on this host" is worse than
			// no button, because it looks like the fix is one click away.
			Fix:      fixIf(fw.Capabilities.Toggle, "firewall.enable"),
			FixLabel: fixIf(fw.Capabilities.Toggle, "Enable firewall"),
		})
	}
	if fw.Enabled && fw.Policy.Incoming == "allow" {
		out = append(out, SecurityFinding{
			ID: "firewall.default-allow", Level: "warning", Area: "firewall",
			Title:  "Inbound traffic is allowed by default",
			Detail: "The default incoming policy is allow, so the deny rules are a blocklist rather than a fence.",
			Advice: "Set the inbound default to deny and add allow rules for what should be reachable. A blocklist can only ever refuse what somebody thought of.",
		})
	}
	if fw.Available && !fw.Capabilities.Editable {
		out = append(out, SecurityFinding{
			ID: "firewall.read-only", Level: "notice", Area: "firewall",
			Title:  "This firewall can be read but not changed from here",
			Detail: fmt.Sprintf("%s is in charge of this host.", fw.Backend),
			Advice: fw.Capabilities.ReadOnlyReason,
		})
	}
	if fw.Enabled && strings.HasPrefix(strings.ToLower(fw.Logging), "off") {
		out = append(out, SecurityFinding{
			ID: "firewall.logging-off", Level: "notice", Area: "firewall",
			Title:  "The firewall is not logging",
			Detail: string(fw.Backend) + " logging is off, so refused connections leave no record.",
			Advice: "Turn logging on at low. It costs almost nothing and it is the only way to answer what was being attempted after the fact.",
		})
	}
	for _, r := range fw.Rules {
		if r.Danger == "" || r.IPv6 {
			continue
		}
		out = append(out, SecurityFinding{
			ID: "firewall.dangerous-rule." + strconv.Itoa(r.Number), Level: "critical", Area: "firewall",
			Title:  "A rule opens " + ruleLabel(r) + " to everyone",
			Detail: r.Raw,
			Advice: r.Danger,
		})
	}
	return out
}

func ruleLabel(r Rule) string {
	if r.Service != "" {
		return r.Service + " (" + r.To + ")"
	}
	return r.To
}

func assessSSH(in AssessInput) []SecurityFinding {
	cfg := in.SSH
	if cfg == nil || !cfg.Available {
		return nil
	}
	value := func(key string) string {
		for _, s := range cfg.Settings {
			if s.Key == key {
				return strings.ToLower(strings.TrimSpace(s.Value))
			}
		}
		return ""
	}
	out := []SecurityFinding{}
	if value("permitrootlogin") == "yes" {
		out = append(out, SecurityFinding{
			ID: "ssh.root-login", Level: "critical", Area: "ssh",
			Title:  "Root may log in over SSH with a password",
			Detail: "PermitRootLogin is yes.",
			Advice: "Set it to prohibit-password. Every bot that finds an SSH port tries root first, and root is the one account that does not need to escalate afterwards.",
			Fix:    "ssh.permitrootlogin=prohibit-password", FixLabel: "Set to prohibit-password",
		})
	}
	if value("permitemptypasswords") == "yes" {
		out = append(out, SecurityFinding{
			ID: "ssh.empty-passwords", Level: "critical", Area: "ssh",
			Title:  "Accounts with no password may log in",
			Detail: "PermitEmptyPasswords is yes.",
			Advice: "Set it to no. There is no configuration in which this is what you meant.",
			Fix:    "ssh.permitemptypasswords=no", FixLabel: "Turn off",
		})
	}
	if value("passwordauthentication") == "yes" {
		level, detail := "notice", "PasswordAuthentication is yes."
		if len(cfg.KeyedAccounts) > 0 {
			level = "warning"
			detail = fmt.Sprintf("PasswordAuthentication is yes, and %d account(s) already have an authorized key.",
				len(cfg.KeyedAccounts))
		}
		if in.FailedLogins >= failedLoginWarningCount {
			level = "warning"
		}
		advice := "Turn it off and use keys. With passwords on, this server's security is whatever its weakest password is."
		if len(cfg.KeyedAccounts) == 0 {
			advice = "Add an SSH key to an account first — with no key anywhere on this host, turning passwords off would lock everyone out. The Users page manages authorized keys."
		}
		out = append(out, SecurityFinding{
			ID: "ssh.password-auth", Level: level, Area: "ssh",
			Title: "SSH accepts passwords", Detail: detail, Advice: advice,
			Fix:      fixIf(len(cfg.KeyedAccounts) > 0, "ssh.passwordauthentication=no"),
			FixLabel: fixIf(len(cfg.KeyedAccounts) > 0, "Turn off passwords"),
		})
	}
	if n, err := strconv.Atoi(value("maxauthtries")); err == nil && n > 6 {
		out = append(out, SecurityFinding{
			ID: "ssh.max-auth-tries", Level: "notice", Area: "ssh",
			Title:  "SSH allows many guesses per connection",
			Detail: fmt.Sprintf("MaxAuthTries is %d.", n),
			Advice: "Lower it to 3. Each connection currently gets that many attempts before it is dropped, which multiplies whatever rate limit sits in front of it.",
		})
	}
	return out
}

func fixIf(cond bool, value string) string {
	if cond {
		return value
	}
	return ""
}

func assessIntrusion(in AssessInput) []SecurityFinding {
	out := []SecurityFinding{}
	sshExposed := in.SSH != nil && in.SSH.Available
	if (in.Fail2ban == nil || !in.Fail2ban.Available) && sshExposed {
		out = append(out, SecurityFinding{
			ID: "intrusion.absent", Level: "notice", Area: "intrusion",
			Title:  "Nothing is blocking repeated failures",
			Detail: "fail2ban is not installed on this host.",
			Advice: "Install fail2ban and enable its sshd jail. It turns an endless brute-force into a few attempts and a ban, which is most of what a firewall cannot do on a port that has to stay open.",
		})
	} else if in.Fail2ban != nil && in.Fail2ban.Available && !in.Fail2ban.Running {
		out = append(out, SecurityFinding{
			ID: "intrusion.stopped", Level: "warning", Area: "intrusion",
			Title:  "fail2ban is installed but not running",
			Detail: in.Fail2ban.Error,
			Advice: "Start the fail2ban service. Installed and stopped is the state that looks protected and is not.",
		})
	} else if in.Fail2ban != nil && in.Fail2ban.Running && len(in.Fail2ban.Jails) == 0 {
		out = append(out, SecurityFinding{
			ID: "intrusion.no-jails", Level: "warning", Area: "intrusion",
			Title:  "fail2ban is running with no jails",
			Detail: "The service is up but has nothing configured to watch.",
			Advice: "Enable at least the sshd jail. A running fail2ban with no jails bans nobody.",
		})
	}
	if !in.LoginRecordRead {
		return append(out, SecurityFinding{
			ID: "intrusion.no-record", Level: "notice", Area: "intrusion",
			Title:  "Failed logins cannot be counted on this host",
			Detail: "The host's btmp record could not be read — `last` and `lastb` come from util-linux-extra, which a minimal image often leaves out.",
			Advice: "Install util-linux-extra to see who has been trying. Until then this check has no answer, which is not the same as a quiet server.",
		})
	}
	switch {
	case in.FailedLogins >= failedLoginWarningCount:
		out = append(out, SecurityFinding{
			ID: "intrusion.failed-logins", Level: "warning", Area: "intrusion",
			Title: "Sustained login attempts against this host",
			Detail: fmt.Sprintf("%s failed attempts %s, %d bans issued.",
				countLabel(in.FailedLogins, in.FailedLoginsCapped), windowLabel(in.FailedLoginWindow), in.RecentBans),
			Advice: "This is what a public SSH port looks like. Confirm password authentication is off, and that fail2ban's sshd jail is actually banning — the failure count matters much less once neither passwords nor unlimited attempts are available.",
		})
	case in.FailedLogins >= failedLoginNoticeCount:
		out = append(out, SecurityFinding{
			ID: "intrusion.failed-logins", Level: "notice", Area: "intrusion",
			Title: "Background brute-force traffic",
			Detail: fmt.Sprintf("%s failed attempts %s.",
				countLabel(in.FailedLogins, in.FailedLoginsCapped), windowLabel(in.FailedLoginWindow)),
			Advice: "Normal for anything with a public SSH port. Worth knowing rather than worth acting on, provided keys are the only way in.",
		})
	}
	return out
}

// countLabel says "at least" where the sample ran out, because a floor quoted
// as a total is the kind of number people go on to reason from.
func countLabel(n int, capped bool) string {
	if capped {
		return "at least " + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// windowLabel names the period the count covers. An unset window means the
// caller counted the whole record, which is what the old figure did and is
// worth admitting rather than dressing up as a period.
func windowLabel(window time.Duration) string {
	switch {
	case window == 0:
		return "in the host's whole record"
	case window >= 48*time.Hour:
		return fmt.Sprintf("in the last %d days", int(window.Hours()/24))
	default:
		return fmt.Sprintf("in the last %d hours", int(window.Hours()))
	}
}

func assessPorts(in AssessInput) []SecurityFinding {
	out := []SecurityFinding{}
	for _, l := range in.Listeners {
		if !l.Exposed {
			continue
		}
		preset, ok := PresetFor(strconv.FormatUint(uint64(l.Port), 10), l.Protocol)
		if !ok || preset.Danger == "" {
			continue
		}
		detail := fmt.Sprintf("%s/%d is bound to %s", strings.ToUpper(l.Protocol), l.Port, addressLabel(l.Address))
		if l.Process != "" {
			detail += " by " + l.Process
		}
		// The firewall may be refusing it anyway, and saying so is the
		// difference between a finding and a false alarm.
		level := "critical"
		if in.Firewall != nil && in.Firewall.Enabled && in.Firewall.Policy.Incoming != "allow" {
			level = "warning"
			detail += ", though the firewall's inbound default is " + in.Firewall.Policy.Incoming
		}
		out = append(out, SecurityFinding{
			ID: fmt.Sprintf("ports.exposed.%s.%d", l.Protocol, l.Port), Level: level, Area: "ports",
			Title:  preset.Name + " is listening on every interface",
			Detail: detail,
			Advice: preset.Danger + " Bind it to 127.0.0.1 instead — for a container, publish it as 127.0.0.1:" + strconv.FormatUint(uint64(l.Port), 10) + ": rather than " + strconv.FormatUint(uint64(l.Port), 10) + ":.",
		})
	}
	return out
}

func addressLabel(addr string) string {
	if addr == "" || addr == "0.0.0.0" || addr == "::" || addr == "*" {
		return "every interface"
	}
	return addr
}

func assessCertificates(in AssessInput) []SecurityFinding {
	out := []SecurityFinding{}
	for _, c := range in.Certificates {
		switch {
		case c.Expired:
			out = append(out, SecurityFinding{
				ID: "tls.expired." + c.Name, Level: "critical", Area: "tls",
				Title:  "Certificate for " + c.Name + " has expired",
				Detail: fmt.Sprintf("%d days past its expiry.", -c.DaysLeft),
				Advice: "Browsers are refusing this site now. Renew it, and check why the automatic renewal did not run — an expired Let's Encrypt certificate almost always means the renewal timer stopped, not that it was forgotten.",
			})
		case c.DaysLeft <= certCriticalDays:
			out = append(out, SecurityFinding{
				ID: "tls.expiring." + c.Name, Level: "critical", Area: "tls",
				Title:  "Certificate for " + c.Name + " expires in " + strconv.Itoa(c.DaysLeft) + " days",
				Detail: "Renewal has not happened and the window is nearly closed.",
				Advice: "Renew it now. certbot renews at 30 days left; being inside three means renewal has been failing for weeks.",
			})
		case c.DaysLeft <= certWarningDays:
			out = append(out, SecurityFinding{
				ID: "tls.expiring." + c.Name, Level: "warning", Area: "tls",
				Title:  "Certificate for " + c.Name + " expires in " + strconv.Itoa(c.DaysLeft) + " days",
				Detail: "Past the point where automatic renewal should already have run.",
				Advice: "Check the renewal timer. certbot attempts renewal from 30 days out, so anything still unrenewed at 14 is not renewing on its own.",
			})
		}
	}
	return out
}

func assessUpdates(in AssessInput) []SecurityFinding {
	out := []SecurityFinding{}
	// A manager with no advisory data cannot be quoted a count of zero. Said
	// plainly rather than left silent: silence here reads as "checked, and
	// nothing outstanding", which on Alpine and Arch is a claim nothing on
	// this host is in a position to make.
	if in.PackageManager != "" && !in.SecurityFiltering {
		out = append(out, SecurityFinding{
			ID: "updates.unknown", Level: "notice", Area: "updates",
			Title:  "Security updates cannot be counted on this host",
			Detail: in.PackageManager + " publishes no advisory data, so pending updates cannot be separated into security fixes and everything else.",
			Advice: "Treat the whole update list as the security list, and keep it short. The Updates page shows it.",
		})
	}
	if in.SecurityUpdates >= securityUpdateWarningCount {
		out = append(out, SecurityFinding{
			ID: "updates.security", Level: "warning", Area: "updates",
			Title:  strconv.Itoa(in.SecurityUpdates) + " security updates are waiting",
			Detail: "Packages from the security pocket have not been applied.",
			Advice: "Apply them from the Updates page. Published security fixes are the exploits everybody already has.",
		})
	} else if in.SecurityUpdates > 0 {
		out = append(out, SecurityFinding{
			ID: "updates.security", Level: "notice", Area: "updates",
			Title:  strconv.Itoa(in.SecurityUpdates) + " security updates are waiting",
			Detail: "Packages from the security pocket have not been applied.",
			Advice: "Apply them from the Updates page.",
		})
	}
	if in.RebootRequired {
		out = append(out, SecurityFinding{
			ID: "updates.reboot", Level: "notice", Area: "updates",
			Title:  "A restart is needed for updates already installed",
			Detail: "The package manager left its reboot-required flag.",
			Advice: "Until the machine restarts, the running kernel and libraries are the old ones — the patch is on disk and not in memory.",
		})
	}
	return out
}
