package netsec

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// errRuleExists is ufw declining to add a rule it already has.
//
// It says so and exits 0, which read as a successful add — and an edit is an
// add followed by a delete of the line below it, so an edit that changed
// nothing deleted the operator's *next* rule instead. Reported as an error
// because "nothing happened" and "the rule is now what you asked for" are not
// the same answer.
var errRuleExists = errors.New("ufw already has that exact rule")

// ufw is the Debian and Ubuntu answer, and the one this dashboard was written
// against first. It is a front end to iptables with an on/off switch, numbered
// rules and named application profiles — which is to say, all the affordances
// the other backends have to approximate.
type ufwBackend struct{}

func (ufwBackend) Kind() Backend { return BackendUFW }
func (ufwBackend) Detect() bool  { return hostexec.AvailableOnHost("ufw") }

func (ufwBackend) Capabilities() FirewallCapabilities {
	return FirewallCapabilities{
		Editable: true, Toggle: true, DefaultPolicy: true,
		Logging: true, Reset: true, Profiles: true,
	}
}

// ufwNumberedRe matches ufw's numbered status output:
//
//	[ 1] 22/tcp   ALLOW IN  Anywhere   # ssh
var ufwNumberedRe = regexp.MustCompile(`^\[\s*(\d+)\]\s+(.*)$`)

// Status reads the rule list and the policy block.
//
// Two calls, not one. `ufw status numbered verbose` looks like it should work
// and is rejected outright — ufw's own parser accepts exactly one of the two
// words and raises "Invalid syntax" for both. Asking for the pair therefore
// produced an error, no rules, and a firewall the page reported as inactive on
// every host that actually had one. numbered supplies the rules with the
// numbers the delete route needs; verbose supplies the defaults and the
// logging level, which numbered omits.
func (ufwBackend) Status(ctx context.Context) (*FirewallStatus, error) {
	out, err := run(ctx, "ufw", "status", "numbered")
	st := &FirewallStatus{Backend: BackendUFW, Available: true, Rules: []Rule{}, Raw: out}
	if err != nil {
		st.Error = err.Error()
		return st, nil
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if after, ok := strings.CutPrefix(line, "Status:"); ok {
			st.Enabled = strings.TrimSpace(after) == "active"
			continue
		}
		m := ufwNumberedRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		num, _ := strconv.Atoi(m[1])
		st.Rules = append(st.Rules, parseUFWRule(num, m[2]))
	}
	// The verbose block is a second call and a soft failure: rules without a
	// policy line is a worse page than rules with one, but far better than
	// the error the combined call produced.
	if verbose, err := run(ctx, "ufw", "status", "verbose"); err == nil {
		st.Raw = out + "\n" + verbose
		applyUFWVerbose(st, verbose)
	}
	return st, nil
}

// applyUFWVerbose reads the header block ufw prints under `status verbose`.
func applyUFWVerbose(st *FirewallStatus, out string) {
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "Status:"):
			st.Enabled = strings.TrimSpace(strings.TrimPrefix(line, "Status:")) == "active"
		case strings.HasPrefix(line, "Logging:"):
			st.Logging = strings.TrimSpace(strings.TrimPrefix(line, "Logging:"))
		case strings.HasPrefix(line, "Default:"):
			st.Default = strings.TrimSpace(strings.TrimPrefix(line, "Default:"))
			st.Policy = parseDefaultPolicy(st.Default)
		}
	}
}

// parseDefaultPolicy splits ufw's one-line summary of its three defaults:
//
//	deny (incoming), allow (outgoing), disabled (routed)
func parseDefaultPolicy(line string) DefaultPolicy {
	var p DefaultPolicy
	for _, part := range strings.Split(line, ",") {
		part = strings.TrimSpace(part)
		verdict, rest, ok := strings.Cut(part, " ")
		if !ok {
			continue
		}
		switch strings.Trim(strings.TrimSpace(rest), "()") {
		case "incoming":
			p.Incoming = verdict
		case "outgoing":
			p.Outgoing = verdict
		case "routed":
			p.Routed = verdict
		}
	}
	return p
}

func parseUFWRule(num int, body string) Rule {
	r := Rule{Number: num, Raw: strings.TrimSpace(body), Handle: strconv.Itoa(num)}
	if idx := strings.Index(body, "#"); idx >= 0 {
		r.Comment = strings.TrimSpace(body[idx+1:])
		body = body[:idx]
	}
	fields := strings.Fields(body)
	// ufw's layout is: <to> <ACTION> <IN|OUT|FWD> <from>
	for i, f := range fields {
		upper := strings.ToUpper(f)
		if upper == "ALLOW" || upper == "DENY" || upper == "REJECT" || upper == "LIMIT" {
			r.Action = upper
			r.To = strings.Join(fields[:i], " ")
			rest := fields[i+1:]
			// FWD is what `ufw route` rules print, and ufw-docker writes a
			// great many of them. Left unrecognised the word became part of
			// the source address, and a rule with no direction is read as
			// inbound — which is how a host whose only rules were forwarding
			// rules satisfied the "something is allowed in" test that guards
			// the inbound default-deny switch.
			if len(rest) > 0 && (rest[0] == "IN" || rest[0] == "OUT" || rest[0] == "FWD") {
				r.Direction = rest[0]
				rest = rest[1:]
			}
			r.From = strings.Join(rest, " ")
			break
		}
	}
	if r.Action == "" {
		r.Action = "UNKNOWN"
		r.To = strings.TrimSpace(body)
	}
	// ufw appends "(v6)" to the destination of the IPv6 half of a rule. Left
	// in the To field it makes the same rule look like two different ones.
	if trimmed, ok := strings.CutSuffix(r.To, " (v6)"); ok {
		r.IPv6, r.To = true, trimmed
	}
	if trimmed, ok := strings.CutSuffix(r.From, " (v6)"); ok {
		r.IPv6, r.From = true, trimmed
	}
	// A rule with a destination address prints it in front of the port —
	// "10.0.0.5 5432/tcp" — so the port is the last field, not the whole
	// column. Taking the column put the address into the port and everything
	// keyed off the port went quiet: no service name, no warning, and an edit
	// that read the rule back with a port nothing could parse.
	if to, proto, ok := strings.Cut(lastField(r.To), "/"); ok {
		r.Port, r.Protocol = to, proto
	} else if bareUFWPortRe.MatchString(lastField(r.To)) {
		// `ufw allow 6379` is legal and writes a rule for both protocols, so
		// the destination is a bare number with no slash to cut on. Left
		// unparsed the rule carries no port, and everything keyed off the port
		// goes quiet: the catalogue cannot name the service, and the warning
		// in front of "Redis is open to the world" — the reason the catalogue
		// exists — never fires on the one spelling a newcomer is most likely
		// to have typed.
		r.Port = lastField(r.To)
	}
	return r
}

// lastField is the port half of ufw's destination column, which carries an
// address in front of it whenever the rule names one.
func lastField(s string) string {
	if i := strings.LastIndexByte(strings.TrimSpace(s), ' '); i >= 0 {
		return strings.TrimSpace(s)[i+1:]
	}
	return strings.TrimSpace(s)
}

// bareUFWPortRe matches a destination that is only a port, a range or a list.
// Anything else there is an address or an application profile name.
var bareUFWPortRe = regexp.MustCompile(`^\d{1,5}(:\d{1,5})?(,\d{1,5}(:\d{1,5})?)*$`)

func (b ufwBackend) AddRule(ctx context.Context, req RuleRequest) (string, error) {
	args := []string{}
	position := req.Position
	if position == 0 && blocksASource(req) {
		// ufw stops at the first match, so a deny appended after `allow 22`
		// never sees the SSH traffic it was written to refuse — and the rule
		// list shows a block that is doing nothing. A source-only deny or
		// reject, which is what "block this address" writes, goes in front
		// unless the caller chose a place for it.
		position = b.frontPosition(ctx, req.From)
	}
	if position > 0 {
		args = append(args, "insert", strconv.Itoa(position))
	}
	args = append(args, req.Action, req.Direction)
	// An application profile names a *destination* port, and ufw only reads it
	// as one inside a `to` clause. Both other placements are worse than an
	// error: `allow in app OpenSSH` is refused outright with "Need 'to' or
	// 'from' clause", so picking a profile and nothing else never worked at
	// all; and `allow in from 10.0.0.0/8 app OpenSSH` is *accepted* and binds
	// the profile to the source port instead — a rule that admits traffic
	// coming *from* port 22 rather than going to it, which is not what the
	// form said and does not do anything the operator wanted.
	dest := req.To != "" || req.Port != "" || req.App != ""
	if req.From != "" {
		args = append(args, "from", req.From)
	} else if dest {
		// ufw's grammar wants a source before a destination, and "any" is how
		// it spells "unrestricted" in that position.
		args = append(args, "from", "any")
	}
	if dest {
		target := "any"
		if req.To != "" {
			target = req.To
		}
		args = append(args, "to", target)
		if req.App != "" {
			args = append(args, "app", req.App)
		}
		if req.Port != "" {
			args = append(args, "port", req.Port)
		}
		if req.Protocol != "" {
			args = append(args, "proto", req.Protocol)
		}
	}
	if req.Comment != "" {
		args = append(args, "comment", req.Comment)
	}
	out, err := run(ctx, "ufw", args...)
	if err != nil {
		return out, err
	}
	// ufw is idempotent and announces it rather than failing: an add matching
	// a rule already present prints "Skipping adding existing rule" and exits
	// 0. On a dual-stack host it prints one line per family, so the test is
	// that *nothing* was added — a v4 rule accepted while its v6 twin was
	// skipped is a real add and must not be reported as a no-op.
	if !ufwAdded(out) && strings.Contains(out, "Skipping") {
		return out, errRuleExists
	}
	return out, nil
}

// blocksASource reports a rule that refuses an address wherever it knocks:
// inbound, deny or reject, a source and neither a port nor a profile.
func blocksASource(req RuleRequest) bool {
	return req.Direction == "in" && (req.Action == "deny" || req.Action == "reject") &&
		req.Port == "" && req.App == "" && req.From != "" && !strings.EqualFold(req.From, "any")
}

// frontPosition is where a rule that has to run before every allow goes: ahead
// of the first rule of its own family.
//
// Checked against ufw itself with --dry-run: it numbers the IPv6 rules after
// the IPv4 ones and refuses an insert outside the family's own range, so a v6
// block goes in at one past the last v4 rule. It also refuses `insert 1` into
// an empty list — where there is nothing to be shadowed by, so appending is
// right, and 0 says so.
func (b ufwBackend) frontPosition(ctx context.Context, from string) int {
	st, err := b.Status(ctx)
	if err != nil || st.Error != "" {
		return 0
	}
	v4, v6 := 0, 0
	for _, r := range st.Rules {
		if r.IPv6 {
			v6++
		} else {
			v4++
		}
	}
	if strings.Contains(from, ":") {
		if v6 == 0 {
			return 0
		}
		return v4 + 1
	}
	if v4 == 0 {
		return 0
	}
	return 1
}

// ufwAdded reports whether ufw's output claims it wrote something.
func ufwAdded(out string) bool {
	for _, claim := range []string{"Rule added", "Rule inserted", "Rule updated"} {
		if strings.Contains(out, claim) {
			return true
		}
	}
	return false
}

// DeleteRule removes a numbered rule and, where ufw made one, its IPv6 twin.
//
// `ufw delete <n>` removes exactly one numbered entry, and on a dual-stack host
// ufw wrote the rule into both tables — `ufw allow 80` produces two lines. The
// page folds the "(v6)" duplicates away so eight rules do not read as sixteen,
// which is right for reading and was catastrophic for deleting: closing a port
// removed the IPv4 line, left the IPv6 line in place and hid it, so the rule
// list showed a closed port that was still open to every IPv6 client on the
// internet.
//
// So the rule is read before it is deleted, and its twin is looked up again
// afterwards by the fields the two share — the numbering has shifted by then,
// which is why the second delete cannot be arithmetic either. A rule with a v4
// source has no twin and nothing further happens.
func (b ufwBackend) DeleteRule(ctx context.Context, number int) (string, error) {
	// ufw prompts before deleting; --force answers it. The rule number comes
	// from our own parsed listing, never from free text.
	target, known := b.ruleAt(ctx, number)
	out, err := run(ctx, "ufw", "--force", "delete", strconv.Itoa(number))
	if err != nil {
		return out, err
	}
	if !known || target.IPv6 {
		return out, nil
	}
	twin, found := b.findRule(ctx, target, true)
	if !found {
		return out, nil
	}
	more, err := run(ctx, "ufw", "--force", "delete", strconv.Itoa(twin))
	if err != nil {
		return out, fmt.Errorf("the rule was removed from the IPv4 table, but its IPv6 copy could not be: %w", err)
	}
	return strings.TrimSpace(out + "\n" + more), nil
}

// ruleAt reads one numbered rule out of the current listing.
func (b ufwBackend) ruleAt(ctx context.Context, number int) (Rule, bool) {
	st, err := b.Status(ctx)
	if err != nil {
		return Rule{}, false
	}
	for _, r := range st.Rules {
		if r.Number == number {
			return r, true
		}
	}
	return Rule{}, false
}

// findRule locates a rule by what it says rather than by where it sits, which
// is the only stable identity ufw offers once a delete has renumbered the list.
func (b ufwBackend) findRule(ctx context.Context, want Rule, ipv6 bool) (int, bool) {
	st, err := b.Status(ctx)
	if err != nil {
		return 0, false
	}
	for _, r := range st.Rules {
		if r.IPv6 == ipv6 && sameRule(r, want) {
			return r.Number, true
		}
	}
	return 0, false
}

func (ufwBackend) SetEnabled(ctx context.Context, enabled bool) (string, error) {
	if enabled {
		return run(ctx, "ufw", "--force", "enable")
	}
	return run(ctx, "ufw", "--force", "disable")
}

func (ufwBackend) SetDefaultPolicy(ctx context.Context, direction, policy string) (string, error) {
	// ufw's argument order is policy first, then direction.
	return run(ctx, "ufw", "default", policy, direction)
}

func (ufwBackend) SetLogging(ctx context.Context, level string) (string, error) {
	return run(ctx, "ufw", "logging", level)
}

// Reset also disables the firewall, which is why it is behind the same typed
// confirmation as the disable toggle rather than treated as a tidy-up.
func (ufwBackend) Reset(ctx context.Context) (string, error) {
	return run(ctx, "ufw", "--force", "reset")
}

// Profiles expands `ufw app list`, which gives only names, with `ufw app info`
// per entry. The names come from ufw's own listing and are checked against a
// pattern before being passed back to it — the file they come from is writable
// by root, and root is not the same as this code.
func (ufwBackend) Profiles(ctx context.Context) ([]AppProfile, error) {
	out, err := run(ctx, "ufw", "app", "list")
	if err != nil {
		return nil, err
	}
	profiles := []AppProfile{}
	for _, name := range parseAppList(out) {
		if !appNameRe.MatchString(name) {
			continue
		}
		p := AppProfile{Name: name, Ports: []string{}}
		if info, err := run(ctx, "ufw", "app", "info", name); err == nil {
			p.Title, p.Description, p.Ports = parseAppInfo(info)
		}
		profiles = append(profiles, p)
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	return profiles, nil
}
