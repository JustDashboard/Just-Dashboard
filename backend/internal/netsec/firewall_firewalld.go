package netsec

import (
	"bufio"
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// firewalld runs Fedora, RHEL and everything descended from it — Rocky, Alma,
// CentOS Stream, Oracle Linux — plus openSUSE. That is most of the server
// installations this dashboard will ever meet outside the Debian family, and
// until now every one of them saw a Security page that could read a rule list
// and change nothing.
//
// Its model is genuinely different from ufw's and the difference is the work.
// There are no rule numbers: a zone holds a set of services, a set of ports
// and a list of "rich rules", and each is removed by handing back the exact
// thing that was added. Numbers are assigned by this dashboard over the listed
// order so that "delete rule 4" means something, and the handle is what
// actually gets removed.
//
// Everything is written --permanent and then reloaded, because a runtime-only
// change is a firewall rule that disappears at the next boot — the same trap
// the fail2ban jail control was rejected for.
type firewalldBackend struct{}

func (firewalldBackend) Kind() Backend { return BackendFirewalld }
func (firewalldBackend) Detect() bool  { return availableOnHost("firewall-cmd") }

func (firewalldBackend) Capabilities() FirewallCapabilities {
	return FirewallCapabilities{
		Editable: true, Toggle: true, DefaultPolicy: true,
		Logging: true, Profiles: true,
		// firewalld has no ufw-style reset. Removing everything a zone holds
		// one call at a time would leave a half-reset firewall behind if any
		// failed; reloading the default zone's shipped definition is one
		// permanent change and one reload (firewall_reset.go).
		Reset: true,
	}
}

var zoneNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// zone reads the active zone, which is where rules are added and removed.
// Everything else in this backend takes it as an argument so the calls are
// explicit about which zone they touch.
func (firewalldBackend) zone(ctx context.Context) string {
	out, err := run(ctx, "firewall-cmd", "--get-default-zone")
	if err != nil {
		return "public"
	}
	name := strings.TrimSpace(out)
	if !zoneNameRe.MatchString(name) {
		return "public"
	}
	return name
}

func (b firewalldBackend) Status(ctx context.Context) (*FirewallStatus, error) {
	st := &FirewallStatus{Backend: BackendFirewalld, Available: true, Rules: []Rule{}}

	// --state exits non-zero when the daemon is stopped, which is a state
	// rather than a failure: a stopped firewalld is exactly what the page
	// needs to show.
	state, _ := run(ctx, "firewall-cmd", "--state")
	st.Enabled = strings.TrimSpace(state) == "running"

	zone := b.zone(ctx)
	st.Zone = zone
	if !st.Enabled {
		st.Default = "firewalld is not running"
		return st, nil
	}

	out, err := run(ctx, "firewall-cmd", "--zone="+zone, "--list-all")
	if err != nil {
		st.Error = err.Error()
		return st, nil
	}
	st.Raw = out
	target, rules := parseFirewalldZone(out)
	for i := range rules {
		rules[i].Zone = zone
	}
	st.Rules = rules
	st.Policy = firewalldPolicy(target)
	st.Default = fmt.Sprintf("%s (zone %s)", target, zone)
	st.Zones = b.activeZones(ctx, zone, target, rules)

	if logged, err := run(ctx, "firewall-cmd", "--get-log-denied"); err == nil {
		st.Logging = strings.TrimSpace(logged)
	}
	return st, nil
}

// FirewallZone is one active firewalld zone: what is bound to it, its
// target and what it admits. Only the default zone's rules are editable from
// here; the others are read so the policy each interface meets is visible.
type FirewallZone struct {
	Name       string   `json:"name"`
	Default    bool     `json:"default"`
	Target     string   `json:"target"`
	Interfaces []string `json:"interfaces"`
	Sources    []string `json:"sources"`
	Rules      []Rule   `json:"rules"`
}

// activeZones reads `--get-active-zones` and each zone's own listing. The
// default zone is always present, bound or not, since unbound interfaces
// land in it.
func (b firewalldBackend) activeZones(ctx context.Context, defaultZone, defaultTarget string, defaultRules []Rule) []FirewallZone {
	zones := []FirewallZone{{Name: defaultZone, Default: true, Target: defaultTarget, Interfaces: []string{}, Sources: []string{}, Rules: defaultRules}}
	out, err := run(ctx, "firewall-cmd", "--get-active-zones")
	if err != nil {
		return zones
	}
	for _, z := range parseActiveZones(out) {
		if z.Name == defaultZone {
			zones[0].Interfaces, zones[0].Sources = z.Interfaces, z.Sources
			continue
		}
		if listing, err := run(ctx, "firewall-cmd", "--zone="+z.Name, "--list-all"); err == nil {
			target, rules := parseFirewalldZone(listing)
			for i := range rules {
				rules[i].Zone = z.Name
			}
			z.Target, z.Rules = target, rules
		}
		zones = append(zones, z)
	}
	return zones
}

// parseActiveZones reads the zone names and their indented bindings:
//
//	public
//	  interfaces: eth0
//	trusted
//	  sources: 10.0.0.0/8
func parseActiveZones(out string) []FirewallZone {
	var zones []FirewallZone
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			name := strings.Fields(trimmed)[0]
			if !zoneNameRe.MatchString(name) {
				continue
			}
			zones = append(zones, FirewallZone{Name: name, Interfaces: []string{}, Sources: []string{}, Rules: []Rule{}})
			continue
		}
		if len(zones) == 0 {
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		z := &zones[len(zones)-1]
		switch strings.TrimSpace(key) {
		case "interfaces":
			z.Interfaces = append(z.Interfaces, strings.Fields(value)...)
		case "sources":
			z.Sources = append(z.Sources, strings.Fields(value)...)
		}
	}
	return zones
}

// firewalldPolicy translates a zone target into the three directions.
//
// The one worth spelling out is "default", which is firewalld's own word for
// "reject anything not explicitly allowed" — reading it as "no policy set"
// would report the safest configuration as the most permissive one. firewalld
// does not filter outgoing traffic in any zone, so saying so is more useful
// than leaving the field blank and letting the UI imply a restriction.
func firewalldPolicy(target string) DefaultPolicy {
	p := DefaultPolicy{Outgoing: "allow", Routed: "disabled"}
	switch strings.ToUpper(strings.Trim(target, "%")) {
	case "ACCEPT":
		p.Incoming = "allow"
	case "DROP":
		p.Incoming = "deny"
	case "REJECT", "DEFAULT", "":
		p.Incoming = "reject"
	default:
		p.Incoming = strings.ToLower(target)
	}
	return p
}

// parseFirewalldZone reads `firewall-cmd --list-all`, whose shape is a set of
// "key: values" lines with the rich rules indented underneath their own.
func parseFirewalldZone(out string) (target string, rules []Rule) {
	rules = []Rule{}
	target = "default"
	inRich := false
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// A rich rule is indented under the "rich rules:" heading and is the
		// only multi-line section, so the indent is what ends it.
		if inRich {
			if strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "  ") {
				if r, ok := parseRichRule(trimmed); ok {
					rules = append(rules, r)
				}
				continue
			}
			inRich = false
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "target":
			target = value
		case "services":
			for _, svc := range strings.Fields(value) {
				rules = append(rules, Rule{
					Action: "ALLOW", Direction: "IN", From: "Anywhere",
					To: svc, Service: svc, Handle: "service:" + svc,
					Raw: "service " + svc, BothFamilies: true,
				})
			}
		case "ports":
			for _, port := range strings.Fields(value) {
				r := Rule{
					Action: "ALLOW", Direction: "IN", From: "Anywhere",
					To: port, Handle: "port:" + port, Raw: "port " + port, BothFamilies: true,
				}
				r.Port, r.Protocol, _ = strings.Cut(port, "/")
				rules = append(rules, r)
			}
		case "rich rules":
			inRich = true
			if value != "" {
				if r, ok := parseRichRule(value); ok {
					rules = append(rules, r)
				}
			}
		}
	}
	return target, rules
}

var richAttrRe = regexp.MustCompile(`(\w[\w-]*)="([^"]*)"`)

// parseRichRule reads one of firewalld's rich rules:
//
//	rule family="ipv4" source address="10.0.0.0/8" port port="5432" protocol="tcp" accept
//
// Attributes are read as key/value pairs rather than by position, because
// firewalld emits them in a canonical order that is not the order they were
// written in and has changed between releases.
func parseRichRule(raw string) (Rule, bool) {
	if !strings.HasPrefix(raw, "rule") {
		return Rule{}, false
	}
	r := Rule{Direction: "IN", Raw: raw, Handle: "rich:" + raw}
	attrs := map[string]string{}
	for _, m := range richAttrRe.FindAllStringSubmatch(raw, -1) {
		// A rich rule repeats a key inside its own element — `port port="80"`
		// — so the first occurrence of each is the one that means something
		// at the top level.
		if _, seen := attrs[m[1]]; !seen {
			attrs[m[1]] = m[2]
		}
	}
	switch attrs["family"] {
	case "ipv6":
		r.IPv6 = true
	case "":
		r.BothFamilies = true
	}
	r.From = "Anywhere"
	if addr := attrs["address"]; addr != "" {
		r.From = addr
	}
	r.Port = attrs["port"]
	r.Protocol = attrs["protocol"]
	switch {
	case r.Port != "" && r.Protocol != "":
		r.To = r.Port + "/" + r.Protocol
	case attrs["name"] != "":
		r.To = attrs["name"]
		r.Service = attrs["name"]
	default:
		r.To = "any"
	}
	switch {
	case strings.Contains(raw, " accept"):
		r.Action = "ALLOW"
		if strings.Contains(raw, "limit value=") {
			r.Action = "LIMIT"
		}
	case strings.Contains(raw, " reject"):
		r.Action = "REJECT"
	case strings.Contains(raw, " drop"):
		r.Action = "DENY"
	default:
		r.Action = "UNKNOWN"
	}
	return r, true
}

func (b firewalldBackend) AddRule(ctx context.Context, req RuleRequest) (string, error) {
	if req.Direction == "out" {
		// firewalld filters egress only through direct rules or policies,
		// neither of which is a zone rule. Saying so beats writing something
		// that looks like an outbound rule and filters nothing.
		return "", fmt.Errorf("firewalld zones do not filter outbound traffic; use a policy object instead")
	}
	zone := b.zone(ctx)
	// A plain allow with no source restriction is a port or a service, which
	// is the shape firewalld prefers and the shape it can list back cleanly.
	if req.Action == "allow" && req.From == "" && req.To == "" {
		args := []string{}
		if req.App != "" {
			args = append(args, "--add-service="+req.App)
		} else {
			proto := req.Protocol
			if proto == "" {
				// firewalld has no protocol-less port. tcp is the
				// overwhelmingly common intent and is what every example
				// assumes.
				proto = "tcp"
			}
			for _, port := range firewalldPorts(req.Port) {
				args = append(args, "--add-port="+port+"/"+proto)
			}
		}
		if len(args) == 0 {
			return "", fmt.Errorf("a rule needs a port or a service")
		}
		// One call, so a list of ports is one change and one reload rather
		// than a partially applied rule if the third of five is refused.
		call := append([]string{"--permanent", "--zone=" + zone}, args...)
		if _, err := run(ctx, "firewall-cmd", call...); err != nil {
			return "", err
		}
		return run(ctx, "firewall-cmd", "--reload")
	}

	rules, err := buildRichRules(req)
	if err != nil {
		return "", err
	}
	call := []string{"--permanent", "--zone=" + zone}
	for _, rule := range rules {
		call = append(call, "--add-rich-rule="+rule)
	}
	if _, err := run(ctx, "firewall-cmd", call...); err != nil {
		return "", err
	}
	return run(ctx, "firewall-cmd", "--reload")
}

// firewalldPorts renders a validated port field in firewalld's own spelling.
//
// Two differences, both of which firewalld answers with a flat INVALID_PORT:
// a range is written with a hyphen, not the colon ufw and iptables use; and
// there is no multiport, so a list is a rule each. Validation upstream accepts
// both forms deliberately — the operator should not have to know which tool is
// underneath — so the translation belongs here, where the tool is known.
func firewalldPorts(port string) []string {
	if port == "" {
		return nil
	}
	out := []string{}
	for _, part := range strings.Split(port, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, strings.ReplaceAll(part, ":", "-"))
	}
	return out
}

// buildRichRules renders the one form firewalld has for "this source, this
// port, this verdict" — one rule per port, because firewalld has no multiport
// and answers a comma with INVALID_PORT.
//
// The whole string is a single argv element, so the quotes inside it are
// firewalld's own syntax rather than shell quoting — nothing here is passed
// through a shell. Every value has already been validated by normaliseRule.
func buildRichRules(req RuleRequest) ([]string, error) {
	ports := firewalldPorts(req.Port)
	if len(ports) == 0 {
		ports = []string{""}
	}
	rules := make([]string, 0, len(ports))
	for _, port := range ports {
		rule, err := buildRichRule(req, port)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

func buildRichRule(req RuleRequest, port string) (string, error) {
	parts := []string{"rule"}
	if req.From != "" && !strings.EqualFold(req.From, "any") {
		family := "ipv4"
		if strings.Contains(req.From, ":") {
			family = "ipv6"
		}
		parts = append(parts, fmt.Sprintf("family=%q", family),
			fmt.Sprintf("source address=%q", req.From))
	}
	switch {
	case req.App != "":
		parts = append(parts, fmt.Sprintf("service name=%q", req.App))
	case port != "":
		proto := req.Protocol
		if proto == "" {
			proto = "tcp"
		}
		parts = append(parts, fmt.Sprintf("port port=%q", port),
			fmt.Sprintf("protocol=%q", proto))
	}
	switch req.Action {
	case "allow":
		parts = append(parts, "accept")
	case "limit":
		// firewalld's rate limit attaches to the verdict rather than being a
		// verdict of its own, which is why ufw's fourth action has no direct
		// equivalent and is expressed as a rate-limited accept.
		parts = append(parts, "accept", `limit value="10/m"`)
	case "deny":
		parts = append(parts, "drop")
	case "reject":
		parts = append(parts, "reject")
	default:
		return "", fmt.Errorf("unsupported action %q", req.Action)
	}
	return strings.Join(parts, " "), nil
}

// DeleteRule resolves the dashboard's positional number back to the handle
// firewalld needs, by listing again. The listing is the only source of these
// handles, so a stale number removes nothing rather than removing the wrong
// thing — the failure mode worth having.
func (b firewalldBackend) DeleteRule(ctx context.Context, number int) (string, error) {
	st, err := b.Status(ctx)
	if err != nil {
		return "", err
	}
	var handle string
	for i, r := range st.Rules {
		if i+1 == number {
			handle = r.Handle
			break
		}
	}
	if handle == "" {
		return "", fmt.Errorf("no rule %d in this zone", number)
	}
	return b.removeHandle(ctx, handle)
}

// removeHandle deletes by the thing firewalld was given rather than by a
// position, which is what makes an edit safe: the handle is resolved before
// anything is added, so it still names the rule it named.
func (b firewalldBackend) removeHandle(ctx context.Context, handle string) (string, error) {
	kind, value, _ := strings.Cut(handle, ":")
	var arg string
	switch kind {
	case "service":
		arg = "--remove-service=" + value
	case "port":
		arg = "--remove-port=" + value
	case "rich":
		arg = "--remove-rich-rule=" + value
	default:
		return "", fmt.Errorf("this rule cannot be removed from the dashboard")
	}
	if _, err := run(ctx, "firewall-cmd", "--permanent", "--zone="+b.zone(ctx), arg); err != nil {
		return "", err
	}
	return run(ctx, "firewall-cmd", "--reload")
}

// SetEnabled drives the service rather than the firewall: firewalld has no
// on/off of its own, and stopping the daemon is what "off" means for it.
func (firewalldBackend) SetEnabled(ctx context.Context, enabled bool) (string, error) {
	verb := "stop"
	if enabled {
		verb = "start"
	}
	out, err := run(ctx, "systemctl", verb, "firewalld")
	if err != nil {
		return out, err
	}
	// Enabling the unit as well, so the answer survives a reboot. A firewall
	// that is on until the next restart is the trap this backend writes
	// everything --permanent to avoid.
	enableVerb := "disable"
	if enabled {
		enableVerb = "enable"
	}
	run(ctx, "systemctl", enableVerb, "firewalld")
	return out, nil
}

func (b firewalldBackend) SetDefaultPolicy(ctx context.Context, direction, policy string) (string, error) {
	if direction != "incoming" {
		return "", fmt.Errorf("firewalld zones set a policy for inbound traffic only")
	}
	target := "default"
	switch policy {
	case "allow":
		target = "ACCEPT"
	case "deny":
		target = "DROP"
	case "reject":
		target = "%%REJECT%%"
	}
	if _, err := run(ctx, "firewall-cmd", "--permanent", "--zone="+b.zone(ctx), "--set-target="+target); err != nil {
		return "", err
	}
	return run(ctx, "firewall-cmd", "--reload")
}

// SetLogging maps our five levels onto firewalld's three. It logs denials
// globally rather than per zone, so this is a machine-wide setting and the
// mapping is deliberately coarse.
func (firewalldBackend) SetLogging(ctx context.Context, level string) (string, error) {
	value := "all"
	switch level {
	case "off":
		value = "off"
	case "low", "on":
		value = "unicast"
	}
	return run(ctx, "firewall-cmd", "--set-log-denied="+value)
}

// Profiles are firewalld's predefined services. It ships a few hundred, and
// unlike ufw's profiles they carry no ports in the listing — resolving each
// would be one subprocess per service, which is a second of latency to
// populate a dropdown. The names are what the operator picks by, so the names
// are what is returned.
func (firewalldBackend) Profiles(ctx context.Context) ([]AppProfile, error) {
	out, err := run(ctx, "firewall-cmd", "--get-services")
	if err != nil {
		return nil, err
	}
	profiles := []AppProfile{}
	for _, name := range strings.Fields(out) {
		if !appNameRe.MatchString(name) {
			continue
		}
		profiles = append(profiles, AppProfile{Name: name, Ports: []string{}})
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	return profiles, nil
}
