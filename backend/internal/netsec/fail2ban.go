package netsec

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

type Jail struct {
	Name         string   `json:"name"`
	Currently    int      `json:"currentlyFailed"`
	TotalFailed  int      `json:"totalFailed"`
	CurrentlyBan int      `json:"currentlyBanned"`
	TotalBanned  int      `json:"totalBanned"`
	BannedIPs    []string `json:"bannedIps"`
	FileList     []string `json:"fileList"`
}

type Fail2banStatus struct {
	Available bool   `json:"available"`
	Running   bool   `json:"running"`
	Jails     []Jail `json:"jails"`
	Error     string `json:"error,omitempty"`
}

func (s *Service) Fail2banAvailable() bool {
	return hostexec.AvailableOnHost("fail2ban-client")
}

// Fail2banStatus queries fail2ban-client. Its output is a fixed-shape text
// report rather than JSON, so it is parsed by the labels it prints.
func (s *Service) Fail2banStatus(ctx context.Context) (*Fail2banStatus, error) {
	st := &Fail2banStatus{Jails: []Jail{}}
	if !s.Fail2banAvailable() {
		return st, nil
	}
	st.Available = true
	out, err := run(ctx, "fail2ban-client", "status")
	if err != nil {
		st.Error = err.Error()
		return st, nil
	}
	st.Running = true
	names := parseJailList(out)
	for _, name := range names {
		jail, err := s.jailStatus(ctx, name)
		if err != nil {
			continue
		}
		st.Jails = append(st.Jails, *jail)
	}
	sort.Slice(st.Jails, func(i, j int) bool { return st.Jails[i].Name < st.Jails[j].Name })
	return st, nil
}

func parseJailList(out string) []string {
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "Jail list:") {
			continue
		}
		_, after, _ := strings.Cut(line, "Jail list:")
		names := []string{}
		for _, n := range strings.Split(after, ",") {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
		return names
	}
	return nil
}

var jailNameRe = regexp.MustCompile(`^[A-Za-z0-9._\-]{1,64}$`)

func (s *Service) jailStatus(ctx context.Context, name string) (*Jail, error) {
	if !jailNameRe.MatchString(name) {
		return nil, fmt.Errorf("invalid jail name %q", name)
	}
	out, err := run(ctx, "fail2ban-client", "status", name)
	if err != nil {
		return nil, err
	}
	j := &Jail{Name: name, BannedIPs: []string{}, FileList: []string{}}
	for _, line := range strings.Split(out, "\n") {
		label, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch {
		case strings.Contains(label, "Currently failed"):
			j.Currently = atoi(value)
		case strings.Contains(label, "Total failed"):
			j.TotalFailed = atoi(value)
		case strings.Contains(label, "Currently banned"):
			j.CurrentlyBan = atoi(value)
		case strings.Contains(label, "Total banned"):
			j.TotalBanned = atoi(value)
		case strings.Contains(label, "Banned IP list"):
			j.BannedIPs = strings.Fields(value)
		case strings.Contains(label, "File list"):
			j.FileList = strings.Fields(value)
		}
	}
	return j, nil
}

func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

// Unban releases one address from a jail. Both arguments are validated: the
// jail name against a name pattern and the address by actually parsing it as
// an IP, so neither can carry anything but what it claims to be.
func (s *Service) Unban(ctx context.Context, jail, ip string) (string, error) {
	if !jailNameRe.MatchString(jail) {
		return "", fmt.Errorf("invalid jail name %q", jail)
	}
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("%q is not a valid IP address", ip)
	}
	return run(ctx, "fail2ban-client", "set", jail, "unbanip", ip)
}

// Ban adds an address to a jail, which is to say it installs a firewall drop
// for it.
//
// callerIP is why this takes an argument the unban does not: a ban is a deny
// rule wearing a different name, and banning the address you are connected
// from severs the session exactly the way an inbound deny would. The firewall
// page has refused that since it shipped; this route reached the same outcome
// from a different button with nothing in the way. Narrow like every other
// guard here — only the certain case, the caller's own address.
func (s *Service) Ban(ctx context.Context, jail, ip, callerIP string) (string, error) {
	if !jailNameRe.MatchString(jail) {
		return "", fmt.Errorf("invalid jail name %q", jail)
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return "", fmt.Errorf("%q is not a valid IP address", ip)
	}
	if caller := net.ParseIP(callerIP); caller != nil && caller.Equal(parsed) {
		return "", fmt.Errorf("%w: %s is the address you are connected from, and a ban installs a firewall drop for it",
			ErrLockout, ip)
	}
	return run(ctx, "fail2ban-client", "set", jail, "banip", ip)
}

// Everything below is jail control rather than jail reporting.
//
// A dashboard that can only unban one address at a time is a viewer with a
// button. The jail's own parameters — how many failures, in what window, for
// how long — are the knobs that decide whether fail2ban is doing anything at
// all, and they live in a file under /etc/fail2ban that is a different shape
// on every distribution. fail2ban-client can read and set them on the running
// server, which is both easier to get right and honest about what is in force
// now rather than what a file says.

// JailConfig is a jail's working parameters.
type JailConfig struct {
	Name string `json:"name"`
	// BanTime, FindTime are seconds. MaxRetry is a count. Together they are
	// the whole policy: this many failures inside this window earns this long
	// a ban.
	BanTime  int      `json:"banTime"`
	FindTime int      `json:"findTime"`
	MaxRetry int      `json:"maxRetry"`
	IgnoreIP []string `json:"ignoreIp"`
	Actions  []string `json:"actions"`
	Error    string   `json:"error,omitempty"`
}

func (s *Service) JailConfig(ctx context.Context, jail string) (*JailConfig, error) {
	if !jailNameRe.MatchString(jail) {
		return nil, fmt.Errorf("invalid jail name %q", jail)
	}
	cfg := &JailConfig{Name: jail, IgnoreIP: []string{}, Actions: []string{}}
	get := func(param string) string {
		out, err := run(ctx, "fail2ban-client", "get", jail, param)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(out)
	}
	cfg.BanTime = atoi(get("bantime"))
	cfg.FindTime = atoi(get("findtime"))
	cfg.MaxRetry = atoi(get("maxretry"))
	cfg.IgnoreIP = parseClientList(get("ignoreip"))
	// One comma-separated line under a heading, which the address-list
	// parser read as prose whenever a jail had more than one action.
	cfg.Actions = parseActionList(get("actions"))
	return cfg, nil
}

// parseClientList reads a multi-valued answer out of fail2ban-client, which
// prints three different things depending on the version and on whether there
// is anything to print.
//
// Current fail2ban — 1.x, which is Debian 12, Ubuntu 24.04 and everything else
// shipping today — draws a tree under a heading:
//
//	These IP addresses/networks are ignored:
//	|- 127.0.0.0/8
//	`- 10.0.0.0/8
//
// with none, it answers in a sentence — `No IP address/network is ignored` —
// and for a single-valued list it prints a heading and bare lines. Older 0.x
// builds print a Python list, `['127.0.0.1/8', '::1']`, and older ones still a
// bare space-separated line. Reading the current format with the old parser
// turned the allowlist into eleven entries reading "These", "IP",
// "addresses/networks" — and an empty one into five entries beginning "No".
func parseClientList(out string) []string {
	out = strings.TrimSpace(out)
	items := []string{}
	if out == "" {
		return items
	}
	if strings.HasPrefix(out, "[") && strings.HasSuffix(out, "]") {
		inner := strings.TrimSuffix(strings.TrimPrefix(out, "["), "]")
		for _, part := range strings.Split(inner, ",") {
			part = strings.Trim(strings.TrimSpace(part), `'"`)
			if part != "" {
				items = append(items, part)
			}
		}
		return items
	}

	// A tree is unambiguous: take the branches and nothing else.
	if branches := treeBranches(out); len(branches) > 0 {
		return branches
	}

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		// A heading names what follows and is not one of the values.
		if line == "" || strings.HasSuffix(line, ":") {
			continue
		}
		fields := strings.Fields(line)
		// The one remaining ambiguity is a bare line: an old build's
		// space-separated values, or the modern sentence saying there are
		// none. Every value here is an address, a network or a path; prose is
		// not, so a multi-word line counts only when every word looks like one.
		if len(fields) > 1 && !allAddressLike(fields) {
			continue
		}
		items = append(items, fields...)
	}
	return items
}

// treeBranches pulls the values out of fail2ban's `|-` / “ `- “ drawing.
func treeBranches(out string) []string {
	items := []string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"|- ", "`- ", "|-\t", "`-\t"} {
			if rest, ok := strings.CutPrefix(line, prefix); ok {
				if rest = strings.TrimSpace(rest); rest != "" {
					items = append(items, rest)
				}
				break
			}
		}
	}
	return items
}

// allAddressLike reports whether every token could be an address, a network or
// a path — which is to say, whether the line is data rather than a sentence.
func allAddressLike(fields []string) bool {
	for _, f := range fields {
		if !strings.ContainsAny(f, "./:") {
			return false
		}
	}
	return true
}

// jailParams is the closed set of parameters this dashboard will set, with
// the bounds each is allowed. Closed for the same reason the sshd list is:
// every entry is a command this code is willing to run against the running
// intrusion-prevention service.
var jailParams = map[string]struct{ min, max int }{
	"bantime":  {60, 31536000},
	"findtime": {30, 604800},
	"maxretry": {1, 100},
}

// SetJailParam changes one parameter. It is the single-value shorthand for
// SetJailParams, which is where the work is.
func (s *Service) SetJailParam(ctx context.Context, jail, param string, value int) (*JailParamResult, error) {
	return s.SetJailParams(ctx, jail, map[string]int{strings.ToLower(strings.TrimSpace(param)): value})
}

// There is deliberately no start/stop for a jail. fail2ban-client's status
// lists only the jails that are running, so a jail stopped from here would
// vanish from every listing with nothing left to start it again — a control
// that can only be used once is a trap, not a feature.

// IgnoreIP adds or removes an address from a jail's allowlist. This is the
// control an operator reaches for the moment they ban themselves, so it is
// worth having on the page rather than in a file.
func (s *Service) IgnoreIP(ctx context.Context, jail, ip string, add bool) (string, error) {
	if !jailNameRe.MatchString(jail) {
		return "", fmt.Errorf("invalid jail name %q", jail)
	}
	// A CIDR is as valid here as a single address, and is what somebody
	// allowlisting their office wants.
	if net.ParseIP(ip) == nil {
		if _, _, err := net.ParseCIDR(ip); err != nil {
			return "", fmt.Errorf("%q is not a valid IP address or CIDR", ip)
		}
	}
	verb := "addignoreip"
	if !add {
		verb = "delignoreip"
	}
	out, err := run(ctx, "fail2ban-client", "set", jail, verb, ip)
	if err != nil {
		return out, err
	}
	// Written to disk as well, for the reason SetJailParams is: addignoreip
	// changes the running server and nothing else, so the allowlist somebody
	// adds after banning themselves is gone at the next restart — silently,
	// with the page still showing it. The list is read back rather than
	// computed here, because fail2ban is the one that knows what it now holds.
	if cfg, cerr := s.JailConfig(ctx, jail); cerr == nil {
		if _, perr := s.PersistJailValue(jail, "ignoreip", strings.Join(cfg.IgnoreIP, " ")); perr != nil {
			return out, fmt.Errorf("applied to the running fail2ban, but not written to disk, so it will be lost on restart: %w", perr)
		}
	}
	return out, nil
}

// UnbanAll releases every address a jail currently holds.
//
// One call per address rather than `unbanip --all`, which only exists from
// fail2ban 0.10 and fails with a syntax error on the older builds still
// shipping in long-term releases. The list comes from our own parse of the
// jail's status, so nothing free-form reaches the client.
func (s *Service) UnbanAll(ctx context.Context, jail string) (int, error) {
	if !jailNameRe.MatchString(jail) {
		return 0, fmt.Errorf("invalid jail name %q", jail)
	}
	status, err := s.jailStatus(ctx, jail)
	if err != nil {
		return 0, err
	}
	unbanned := 0
	for _, ip := range status.BannedIPs {
		if _, err := s.Unban(ctx, jail, ip); err == nil {
			unbanned++
		}
	}
	return unbanned, nil
}
