package netsec

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// BackendNFTOwned is the dashboard's own nftables table. It is offered where
// neither ufw nor firewalld is present, and it manages that one table: rules
// in any other table — Docker's, the gateway's, hand-written ones — keep
// enforcing, are never adopted, and an accept here cannot override their
// drops.
const BackendNFTOwned Backend = "nftables"

// OwnedFirewall is the network module's owned table, seen from here without
// importing it; the API layer adapts one to the other.
type OwnedFirewall interface {
	Available() bool
	Read(ctx context.Context) (OwnedTable, error)
	Change(ctx context.Context, change OwnedChange) error
}

// OwnedTable is the owned table as saved, with whether the kernel holds it
// and which other tables exist.
type OwnedTable struct {
	Enabled  bool
	Incoming string
	Runtime  string
	Rules    []OwnedRule
	Foreign  []string
	Raw      string
}

// OwnedRule is one rule of the owned table.
type OwnedRule struct {
	ID        int
	Action    string
	Protocol  string
	Ports     string
	Source    string
	Interface string
	Comment   string
}

// OwnedChange is one edit: add (Rule at Position), delete (ID), enable,
// disable, policy or reset.
type OwnedChange struct {
	Op       string
	Rule     *OwnedRule
	Position int
	ID       int
	Policy   string
}

// UseOwnedFirewall offers the network module's table as a backend.
func (s *Service) UseOwnedFirewall(owned OwnedFirewall) { s.owned = owned }

type nftOwnedBackend struct{ owner OwnedFirewall }

func (nftOwnedBackend) Kind() Backend  { return BackendNFTOwned }
func (b nftOwnedBackend) Detect() bool { return b.owner.Available() }

func (nftOwnedBackend) Capabilities() FirewallCapabilities {
	return FirewallCapabilities{Editable: true, Toggle: true, DefaultPolicy: true, Reset: true}
}

// Activity reports the table active when it is switched on and loaded.
func (b nftOwnedBackend) Activity(ctx context.Context) (string, string) {
	t, err := b.owner.Read(ctx)
	switch {
	case err != nil:
		return "unknown", "The owned table could not be read: " + err.Error()
	case t.Enabled && t.Runtime == "present":
		return "active", "The dashboard's nftables table is switched on and loaded."
	case t.Enabled:
		return "unknown", "The dashboard's nftables table is switched on but the kernel does not hold it; the boot unit restores it."
	}
	return "inactive", "nftables is available; the dashboard's own table is switched off."
}

func (b nftOwnedBackend) Status(ctx context.Context) (*FirewallStatus, error) {
	t, err := b.owner.Read(ctx)
	if err != nil {
		return nil, err
	}
	incoming := "allow"
	if t.Incoming == "drop" {
		incoming = "deny"
	}
	st := &FirewallStatus{
		Backend: BackendNFTOwned, Available: true, Enabled: t.Enabled && t.Runtime == "present",
		Default: "policy " + map[bool]string{true: "drop", false: "accept"}[t.Incoming == "drop"] + " (table inet jd_firewall)",
		Policy:  DefaultPolicy{Incoming: incoming}, Rules: []Rule{}, Raw: t.Raw, Foreign: append([]string{}, t.Foreign...),
	}
	if t.Enabled && t.Runtime != "present" {
		st.Error = "The owned table is switched on but not loaded; the boot unit or the next change restores it."
	}
	for i, r := range t.Rules {
		rule := Rule{Number: i + 1, Action: ownedAction(r.Action), Direction: "IN", From: firstNonBlank(r.Source, "Anywhere"),
			Port: strings.ReplaceAll(r.Ports, "-", ":"), Protocol: r.Protocol, Interface: r.Interface, Comment: r.Comment,
			Handle: strconv.Itoa(r.ID), BothFamilies: r.Source == "", IPv6: strings.Contains(r.Source, ":")}
		rule.To = "Anywhere"
		if r.Ports != "" {
			rule.To = rule.Port
			if r.Protocol != "" {
				rule.To += "/" + r.Protocol
			}
		}
		rule.Raw = strings.TrimSpace(fmt.Sprintf("%s %s from %s %s", rule.Action, rule.To, rule.From, map[bool]string{true: "on " + r.Interface}[r.Interface != ""]))
		st.Rules = append(st.Rules, rule)
	}
	return st, nil
}

func ownedAction(action string) string {
	switch action {
	case "accept":
		return "ALLOW"
	case "drop":
		return "DENY"
	case "reject":
		return "REJECT"
	}
	return "UNKNOWN"
}

func (b nftOwnedBackend) AddRule(ctx context.Context, req RuleRequest) (string, error) {
	if req.Direction != "in" {
		return "", fmt.Errorf("the dashboard's nftables table filters inbound traffic only")
	}
	if req.App != "" {
		return "", fmt.Errorf("the dashboard's nftables table has no application profiles; name the ports")
	}
	if req.To != "" && !strings.EqualFold(req.To, "any") {
		return "", fmt.Errorf("the dashboard's nftables table matches source, port, protocol and interface, not a destination address")
	}
	if req.Action == "limit" {
		return "", fmt.Errorf("the dashboard's nftables table has no rate-limited allow; use the Protection page's limits")
	}
	rule := &OwnedRule{Action: req.Action, Protocol: req.Protocol, Ports: req.Port, Source: req.From, Comment: req.Comment}
	if err := b.owner.Change(ctx, OwnedChange{Op: "add", Rule: rule, Position: req.Position}); err != nil {
		return "", err
	}
	return "rule added to table inet jd_firewall", nil
}

func (b nftOwnedBackend) DeleteRule(ctx context.Context, number int) (string, error) {
	st, err := b.Status(ctx)
	if err != nil {
		return "", err
	}
	for _, r := range st.Rules {
		if r.Number == number {
			return b.removeHandle(ctx, r.Handle)
		}
	}
	return "", fmt.Errorf("no rule %d in the owned table", number)
}

// removeHandle deletes by the rule's own id, which survives renumbering.
func (b nftOwnedBackend) removeHandle(ctx context.Context, handle string) (string, error) {
	id, err := strconv.Atoi(handle)
	if err != nil || id < 1 {
		return "", fmt.Errorf("this rule cannot be removed from the dashboard")
	}
	if err := b.owner.Change(ctx, OwnedChange{Op: "delete", ID: id}); err != nil {
		return "", err
	}
	return "rule removed from table inet jd_firewall", nil
}

func (b nftOwnedBackend) SetEnabled(ctx context.Context, enabled bool) (string, error) {
	op := "disable"
	if enabled {
		op = "enable"
	}
	if err := b.owner.Change(ctx, OwnedChange{Op: op}); err != nil {
		return "", err
	}
	return "table inet jd_firewall " + op + "d", nil
}

func (b nftOwnedBackend) SetDefaultPolicy(ctx context.Context, direction, policy string) (string, error) {
	if direction != "incoming" {
		return "", fmt.Errorf("the dashboard's nftables table sets the inbound default only")
	}
	if err := b.owner.Change(ctx, OwnedChange{Op: "policy", Policy: policy}); err != nil {
		return "", err
	}
	return "inbound default set to " + policy, nil
}

func (nftOwnedBackend) SetLogging(context.Context, string) (string, error) {
	return "", readOnly("the dashboard's nftables table does not log; its rules count matches instead")
}

func (b nftOwnedBackend) Reset(ctx context.Context) (string, error) {
	if err := b.owner.Change(ctx, OwnedChange{Op: "reset"}); err != nil {
		return "", err
	}
	return "table inet jd_firewall reset and switched off", nil
}

func (nftOwnedBackend) Profiles(context.Context) ([]AppProfile, error) { return []AppProfile{}, nil }
