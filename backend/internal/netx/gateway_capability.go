package netx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// gatewaySysRoot and gatewayClassNet are where the gateway reads kernel
// state it does not need a command for. Variables so tests point them at a
// temporary directory.
var (
	gatewaySysRoot  = "/proc/sys"
	gatewayClassNet = "/sys/class/net"
)

// Capability says whether the gateway can be written on this host, and if not
// why not, in a sentence the page shows where the controls would be. It is
// declared per firewall the way netsec's FirewallCapabilities is: a missing
// control that explains itself is information, and one that is merely absent
// is a bug report.
type Capability struct {
	Writable bool   `json:"writable"`
	Reason   string `json:"reason,omitempty"`
	// Firewall is what filters this host's traffic: ufw, firewalld, iptables,
	// nftables (a ruleset of the operator's own) or none.
	Firewall string `json:"firewall"`
	// Docker is whether Docker's chains are present, which is why admission
	// reaches DOCKER-USER.
	Docker bool `json:"docker"`
	// Blocker names the chain that makes the gateway read-only, with the rule
	// that would admit the dashboard's translated connections there.
	Blocker *CapabilityBlocker `json:"blocker,omitempty"`

	// missing is nft itself being absent; mutations answer it as an install
	// hand-off rather than a read-only host.
	missing bool
}

// CapabilityBlocker is a base chain at the forward hook that drops by default.
type CapabilityBlocker struct {
	Family string `json:"family"`
	Table  string `json:"table"`
	Chain  string `json:"chain"`
	// Rule is what to add to that chain so connections the gateway
	// translated are accepted by it.
	Rule string `json:"rule"`
}

// admitMarkRule is the accept an operator adds to a foreign forward chain.
const admitMarkRule = "ct mark and " + connMask + " == " + connMark + " accept"

// iptablesCompat are the tables iptables-nft creates. Their forward chain is
// named FORWARD, and it is the one the admission rule is inserted into; a
// chain by any other name in one of these tables is somebody's own nftables
// ruleset that happens to be called filter.
var iptablesCompat = map[string]bool{"filter": true, "nat": true, "raw": true, "mangle": true, "security": true}

// nftListing is `nft -t -j list ruleset` (terse: no set elements, which on a
// host with a blocklist loaded are most of the output) read for the two kinds of object that
// matter here. Everything else it prints is left unparsed.
type nftListing struct {
	Nftables []struct {
		Table *struct {
			Family string `json:"family"`
			Name   string `json:"name"`
		} `json:"table"`
		Chain *struct {
			Family string `json:"family"`
			Table  string `json:"table"`
			Name   string `json:"name"`
			Hook   string `json:"hook"`
			Policy string `json:"policy"`
		} `json:"chain"`
	} `json:"nftables"`
}

// GatewayCapability reads what is filtering this host and says whether the
// gateway's translations can be admitted through it.
//
// The gateway's table never accepts: an accept in one nftables table cannot
// override a drop in another, so a forward is let through by the connection
// mark the iptables chains accept (§4 of the plan). That works wherever the
// host's filtering is iptables — ufw, Docker, plain iptables — and nowhere the
// filtering is a table of its own that drops forwarded traffic, because the
// accept would have to be written into that table.
func (s *Service) GatewayCapability(ctx context.Context) Capability {
	c := Capability{Firewall: "none"}
	out, err := run(ctx, "nft", "-t", "-j", "list", "ruleset")
	if err != nil {
		var missing *UnavailableError
		if errors.As(err, &missing) {
			c.Reason = "nftables is not installed on this host; the gateway needs the nft command."
			c.missing = true
			return c
		}
		c.Reason = fmt.Sprintf("the host's nftables ruleset could not be read (%v), so whether the gateway can be admitted is not known.", err)
		return c
	}
	var listing nftListing
	if err := json.Unmarshal([]byte(out), &listing); err != nil {
		c.Reason = "nft printed its ruleset in a form this dashboard could not read, so whether the gateway can be admitted is not known."
		return c
	}

	firewalld := false
	if state, ferr := run(ctx, "firewall-cmd", "--state"); ferr == nil && strings.TrimSpace(state) == "running" {
		firewalld = true
	}
	var blocker *CapabilityBlocker
	hasIPTables := false
	for _, o := range listing.Nftables {
		if o.Table != nil && o.Table.Family == "inet" && o.Table.Name == "firewalld" {
			firewalld = true
		}
		ch := o.Chain
		if ch == nil {
			continue
		}
		if (ch.Family == "ip" || ch.Family == "ip6") && iptablesCompat[ch.Table] {
			hasIPTables = true
		}
		if ch.Hook != "forward" || ch.Policy != "drop" || blocker != nil {
			continue
		}
		if ch.Family == "inet" && ch.Table == gatewayTable {
			continue
		}
		if (ch.Family == "ip" || ch.Family == "ip6") && iptablesCompat[ch.Table] && ch.Name == "FORWARD" {
			continue
		}
		blocker = &CapabilityBlocker{Family: ch.Family, Table: ch.Table, Chain: ch.Name, Rule: admitMarkRule}
	}

	c.Docker = dockerPresent(ctx)
	switch {
	case firewalld:
		c.Firewall = "firewalld"
		c.Reason = "firewalld filters forwarded traffic in its own nftables table, and an accept in another table cannot override its drop, so this dashboard cannot let a port forward or NAT through it. Open the forward in a firewalld zone instead (the Firewall page lists them)."
		return c
	case blocker != nil:
		c.Firewall = "nftables"
		c.Blocker = blocker
		c.Reason = fmt.Sprintf("The %s table %q drops forwarded traffic by default in its chain %q, and an accept in the gateway's table cannot override that. Add this rule to that chain to let connections the gateway translates through: %s",
			blocker.Family, blocker.Table, blocker.Chain, blocker.Rule)
		return c
	}
	c.Writable = true
	switch {
	case ufwActive(ctx):
		c.Firewall = "ufw"
	case hasIPTables:
		c.Firewall = "iptables"
	}
	return c
}

// ufwActive reports whether ufw is managing the host's filtering.
func ufwActive(ctx context.Context) bool {
	out, err := run(ctx, "ufw", "status")
	return err == nil && strings.Contains(out, "Status: active")
}

// dockerPresent reports whether Docker's chains exist: DOCKER-USER is where
// admission goes, and docker0 is the fallback for a host whose iptables
// cannot be listed.
func dockerPresent(ctx context.Context) bool {
	if _, err := run(ctx, "iptables", "-S", "DOCKER-USER"); err == nil {
		return true
	}
	_, err := os.Stat(filepath.Join(gatewayClassNet, "docker0"))
	return err == nil
}

// requireWritable is the guard in front of every gateway mutation that adds
// or changes a translation or a drop.
func (s *Service) requireWritable(ctx context.Context) (Capability, error) {
	c := s.GatewayCapability(ctx)
	switch {
	case c.Writable:
		return c, nil
	case c.missing:
		return c, &UnavailableError{Tool: "nft", Package: "nftables"}
	}
	return c, &ReadOnlyError{Reason: c.Reason}
}
