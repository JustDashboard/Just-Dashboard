package netsec

import (
	"fmt"
	"net"
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
	// Unknowns are the layers of exposure no check could see: provider
	// policy, and nftables tables no adapter models.
	Unknowns []PostureUnknown `json:"unknowns"`
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
	// Process names the socket's holder: docker-proxy's is a port Docker
	// publishes, which a firewall's inbound default does not hold.
	Process string
	// Published is a port Docker publishes whatever holds it: docker-proxy
	// where this account cannot see the holder, dockerd reserving it, or
	// no socket at all where the userland proxy is off.
	Published bool
	// Exposed is any bind but loopback. How far it reaches — every
	// interface, a public address, a tailnet — is read from Address against
	// AssessInput.Network, by the same HostNetwork.Place the ports page's
	// listing is graded with, so the two cannot disagree.
	Exposed bool
	// Dashboard marks one of the dashboard's own sockets other than its
	// Caddy's: the backend and the web app, which are meant to answer on
	// loopback alone.
	Dashboard bool
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
	Exposure  *Exposure
	Firewall  *FirewallStatus
	Fail2ban  *Fail2banStatus
	SSH       *SSHDConfig
	Listeners []ExposedPort
	// Network places each listener's address on its interface, which is
	// what tells a bridge address from the uplink's. The zero value judges
	// every private address as the uplink's.
	Network      HostNetwork
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
	// Policy is the nftables ruleset's filtering chains, nil where it was not
	// read. PublicAddress is a public address on one of this host's
	// interfaces, empty for none, once PublicAddressRead says it was looked
	// for.
	Policy            *PolicyCoverage
	PublicAddress     string
	PublicAddressRead bool
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
	p := &Posture{Findings: []SecurityFinding{}, CheckedAt: in.Now.UTC(), Skipped: []string{}, Unknowns: assessUnknowns(in)}

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
	// One finding per protocol and port, from its widest bind: a database on
	// 0.0.0.0 and on :: is one exposure, and the finding's ID promises one.
	type exposure struct {
		listener ExposedPort
		preset   ServicePreset
		reach    bindReach
	}
	widest := map[string]exposure{}
	order := []string{}
	for _, l := range in.Listeners {
		preset, reach, ok := in.Network.dangerAt(l)
		if !ok {
			continue
		}
		id := fmt.Sprintf("ports.exposed.%s.%d", l.Protocol, l.Port)
		if preset.Key == dashboardService.Key {
			id = fmt.Sprintf("ports.self.%s.%d", l.Protocol, l.Port)
		}
		prev, seen := widest[id]
		if !seen {
			order = append(order, id)
		}
		if !seen || reach.class.rank() > prev.reach.class.rank() {
			widest[id] = exposure{l, preset, reach}
		}
	}

	out := []SecurityFinding{}
	for _, id := range order {
		e := widest[id]
		l, port := e.listener, strconv.FormatUint(uint64(e.listener.Port), 10)
		detail := fmt.Sprintf("%s/%d is bound to %s", strings.ToUpper(l.Protocol), l.Port, addressLabel(l.Address))
		if e.reach.iface != "" {
			detail += " on " + e.reach.iface
		}
		if l.Process != "" {
			detail += " by " + l.Process
		}
		grade := portLevel(l, e.reach, in.Network, in.Firewall)
		switch {
		case grade.InboundDefault != "":
			detail += ", though the firewall's inbound default is " + grade.InboundDefault
		case grade.PastFirewall == PastFirewallDocker:
			detail += ", published by Docker past the firewall's inbound default"
		case grade.PastFirewall == PastFirewallRule:
			detail += fmt.Sprintf(", and firewall rule %d admits it from anywhere", grade.FirewallRule)
		}
		out = append(out, SecurityFinding{
			ID: id, Level: grade.Level, Area: "ports",
			Title:  e.preset.Name + " is listening on " + e.reach.where,
			Detail: detail,
			Advice: e.reach.advice(e.preset.Danger, l.Address, port),
		})
	}
	return out
}

// PortGrade is the posture's judgement of one listening socket, which the
// ports page colours the socket by and the proxy overview levels its
// finding by, so neither can call critical what the posture calls a warning.
type PortGrade struct {
	// Level is "critical" or "warning", or empty where the posture raises
	// nothing: a port with no danger in the catalogue, a loopback bind, or a
	// danger only strangers pose on a bind the internet cannot reach.
	Level string
	// InboundDefault is the firewall's inbound default when it is what holds
	// Level to a warning, and empty otherwise.
	InboundDefault string
	// PastFirewall says why a firewall refusing inbound by default does not
	// hold the socket: Docker publishes it, or a rule admits it. Empty when
	// the default holds it, or when there is no such default to hold it.
	PastFirewall PastFirewall
	// FirewallRule is the number of the rule that admits it, for
	// PastFirewallRule.
	FirewallRule int
}

// PastFirewall is how a socket's traffic gets by a firewall's inbound default.
type PastFirewall string

const (
	// PastFirewallDocker is a port Docker publishes, with NAT rules that
	// forward the traffic before the input chain the inbound default
	// belongs to is reached, so the default never sees it.
	PastFirewallDocker PastFirewall = "docker"
	// PastFirewallRule is a port a rule admits from anywhere, which a
	// connection meets before it falls through to the default.
	PastFirewallRule PastFirewall = "rule"
)

// GradePort levels one socket by the rules Assess levels a port's finding
// by. A port bound several times is one finding, at the highest of its
// sockets' levels.
func GradePort(l ExposedPort, network HostNetwork, firewall *FirewallStatus) PortGrade {
	_, reach, ok := network.dangerAt(l)
	if !ok {
		return PortGrade{}
	}
	return portLevel(l, reach, network, firewall)
}

// dangerAt is the catalogue's entry for a socket and how far its address
// reaches, when the posture raises a finding for it.
func (n HostNetwork) dangerAt(l ExposedPort) (ServicePreset, bindReach, bool) {
	if !l.Exposed {
		return ServicePreset{}, bindReach{}, false
	}
	preset, ok := ServiceOf(l)
	if !ok || preset.Danger == "" {
		return ServicePreset{}, bindReach{}, false
	}
	reach := n.reachOf(l.Address)
	if !reach.matters(preset) {
		return ServicePreset{}, bindReach{}, false
	}
	return preset, reach, true
}

// portLevel is a dangerous port's level at a reach. A socket only one
// network can reach is worth knowing about, not an emergency: a database for
// the containers on a bridge, or for the operator's own devices on a
// tailnet, is often the design. A firewall refusing inbound by default may
// be refusing it anyway, and saying so is the difference between a finding
// and a false alarm — but only where the default is what the socket's
// traffic meets. A port Docker publishes is forwarded before it, and a port
// a rule admits from anywhere is let in before it; crediting the default
// there drew amber a database the internet can reach. One whose default
// could not be read is not counted on.
func portLevel(l ExposedPort, reach bindReach, network HostNetwork, firewall *FirewallStatus) PortGrade {
	grade := PortGrade{Level: "warning"}
	if reach.class.InternetFacing() {
		grade.Level = "critical"
	}
	if firewall == nil || !firewall.Enabled || firewall.Policy.Incoming == "" || firewall.Policy.Incoming == "allow" {
		return grade
	}
	if l.Process == "docker-proxy" || l.Published {
		grade.PastFirewall = PastFirewallDocker
		return grade
	}
	if rule, ok := admittingRule(firewall, network, l); ok {
		grade.PastFirewall, grade.FirewallRule = PastFirewallRule, rule
		return grade
	}
	return PortGrade{Level: "warning", InboundDefault: firewall.Policy.Incoming}
}

// admittingRule is the number of the inbound rule that lets a connection
// from anywhere reach the socket's port before the inbound default refuses
// it. ufw and iptables stop at the first rule that matches, so a rule
// refusing the port from anywhere ends the search.
//
// A rule limited to one destination admits the socket when that is the
// socket's own address, or one the internet reaches — a public address, or
// the private uplink a provider maps one onto. One for a tailnet's or a
// bridge's address, like one limited to an interface, still leaves the
// internet to the default, which is how a database on every interface is
// kept to a tailnet.
func admittingRule(firewall *FirewallStatus, network HostNetwork, l ExposedPort) (int, bool) {
	port := strconv.FormatUint(uint64(l.Port), 10)
	for _, r := range firewall.Rules {
		if !inboundRule(r, firewall.Backend) || !isAnywhere(r.From) || !protocolCovers(r.Protocol, l.Protocol) {
			continue
		}
		destination, ports, ok := ruleTarget(r, firewall.Backend)
		if !ok || ports != "" && !portInSpec(port, ports) {
			continue
		}
		toSocket := destination == "" || destinationHolds(destination, l.Address)
		switch strings.ToUpper(r.Action) {
		case "ALLOW", "LIMIT", "ACCEPT":
			if toSocket || network.internetReaches(destination) {
				return r.Number, true
			}
		case "DENY", "REJECT", "DROP":
			// iptables' listing leaves out the interface a rule is limited
			// to, so its refusals end nothing.
			if toSocket && firewall.Backend != BackendIPTables {
				return 0, false
			}
		}
	}
	return 0, false
}

// inboundRule is a rule on traffic coming in to this host: ufw's and
// firewalld's IN, and for iptables the INPUT chain the default belongs to.
// Rules in the chains INPUT jumps to are not followed.
func inboundRule(r Rule, backend Backend) bool {
	if backend == BackendIPTables {
		return r.Direction == "INPUT"
	}
	return r.Direction == "" || strings.EqualFold(r.Direction, "IN")
}

func protocolCovers(ruleProtocol, protocol string) bool {
	return ruleProtocol == "" || strings.EqualFold(ruleProtocol, "all") || strings.EqualFold(ruleProtocol, protocol)
}

// ruleTarget reads what a rule is about: the destination address it names,
// empty for any, and its ports, empty for every port. ok is false for a rule
// whose target cannot be read from its listing: a ufw application profile,
// a rule limited to an interface, and an iptables rule that names no port,
// whose other matches — a state, an input interface — are not in its fields.
func ruleTarget(r Rule, backend Backend) (destination, ports string, ok bool) {
	switch backend {
	case BackendIPTables:
		ports = iptablesPort(r.Raw)
		return anywhereAsEmpty(r.To), ports, ports != ""
	case BackendFirewalld:
		// A zone's ports and services, and the rich rules read here, name
		// no destination.
		if r.Port != "" {
			return "", r.Port, true
		}
		if r.Service != "" {
			ports := []string{}
			for _, preset := range ServiceCatalogue {
				if preset.Firewalld == r.Service {
					ports = append(ports, preset.Port)
				}
			}
			return "", strings.Join(ports, ","), len(ports) > 0
		}
		return "", "", isAnywhere(r.To)
	}
	to := strings.TrimSpace(r.To)
	// A rule scoped to one device does not open the port everywhere.
	if r.Interface != "" || strings.Contains(to, " on ") {
		return "", "", false
	}
	if r.Port != "" {
		// ufw prints a destination address in front of the port.
		if i := strings.LastIndexByte(to, ' '); i >= 0 {
			return anywhereAsEmpty(to[:i]), r.Port, true
		}
		return "", r.Port, true
	}
	if isAnywhere(to) {
		return "", "", true
	}
	return to, "", isAddress(to)
}

func anywhereAsEmpty(address string) string {
	if isAnywhere(address) {
		return ""
	}
	return strings.TrimSpace(address)
}

func isAddress(s string) bool {
	if _, _, err := net.ParseCIDR(s); err == nil {
		return true
	}
	return net.ParseIP(s) != nil
}

// destinationHolds says a rule's destination address or network holds a
// socket's address. A socket on every interface is held by no one address.
func destinationHolds(destination, address string) bool {
	ip := net.ParseIP(address)
	if ip == nil || ip.IsUnspecified() {
		return false
	}
	if _, cidr, err := net.ParseCIDR(destination); err == nil {
		return cidr.Contains(ip)
	}
	return ip.Equal(net.ParseIP(destination))
}

// internetReaches says the internet reaches a rule's destination: a public
// address, or a private one a provider may map a public address onto.
func (n HostNetwork) internetReaches(destination string) bool {
	if destination == "" {
		return true
	}
	address, _, _ := strings.Cut(destination, "/")
	if net.ParseIP(address) == nil {
		return false
	}
	reach := n.reachOf(address)
	return reach.class.InternetFacing() || reach.forwarded
}

// portInSpec says a port is in a rule's port field: one port, a list, or a
// range written with a colon (ufw, iptables) or a dash (firewalld).
func portInSpec(port, spec string) bool {
	n, err := strconv.Atoi(port)
	if err != nil {
		return false
	}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		lo, hi, isRange := strings.Cut(part, ":")
		if !isRange {
			lo, hi, isRange = strings.Cut(part, "-")
		}
		if !isRange {
			if part == port {
				return true
			}
			continue
		}
		from, err1 := strconv.Atoi(strings.TrimSpace(lo))
		to, err2 := strconv.Atoi(strings.TrimSpace(hi))
		if err1 == nil && err2 == nil && from <= n && n <= to {
			return true
		}
	}
	return false
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
