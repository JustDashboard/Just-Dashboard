package api

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfcfg"
)

// portReservation is a port the free-port search passed over although
// nothing listens on it now: something has claimed it, or binding it would
// meet a policy already written for it.
type portReservation struct {
	Port   int    `json:"port"`
	Source string `json:"source"`
	Detail string `json:"detail"`
}

// portSource is one owner the search consulted, and whether it could. A
// source that was not read is said, so a port is never called free on the
// strength of an owner nobody asked.
type portSource struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// Source states.
const (
	portSourceChecked     = "checked"
	portSourceUnavailable = "unavailable"
	portSourceNotSupplied = "not_supplied"
)

type reservationCheck func(port int) (portReservation, bool)

// portPolicy is every owner's claim on a port beyond a socket bound now,
// consulted in the order the sources are listed.
type portPolicy struct {
	checks  map[string]reservationCheck
	sources []portSource
}

func (p portPolicy) reserved(port int) (portReservation, bool) {
	for _, source := range p.sources {
		if check := p.checks[source.Key]; check != nil {
			if r, ok := check(port); ok {
				return r, true
			}
		}
	}
	return portReservation{}, false
}

// portLease is a deployment's claim on a loopback port between choosing it
// and its container binding it.
type portLease struct {
	Address     string
	Port        int
	Protocol    string
	Environment string
	ExpiresAt   time.Time
}

func leaseCheck(leases []portLease, protocol, address string) reservationCheck {
	return func(port int) (portReservation, bool) {
		for _, l := range leases {
			if l.Port == port && l.Protocol == protocol && bindingOverlaps(l.Address, address) {
				return portReservation{Port: port, Source: "deployments",
					Detail: fmt.Sprintf("leased to %s on %s until %s, while its release starts", l.Environment, l.Address, l.ExpiresAt.UTC().Format(time.RFC3339))}, true
			}
		}
		return portReservation{}, false
	}
}

var tailnetRanges = []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("fd7a:115c:a1e0::/48")}

// previewCheck keeps the tailnet preview range clear wherever tailscaled
// would serve it: on the tailnet address, which a wildcard bind covers too.
func previewCheck(protocol, address string) reservationCheck {
	applies := protocol == "tcp"
	if ip, err := netip.ParseAddr(address); err == nil && !ip.IsUnspecified() {
		applies = applies && (tailnetRanges[0].Contains(ip.Unmap()) || tailnetRanges[1].Contains(ip))
	}
	return func(port int) (portReservation, bool) {
		if applies && port >= selfcfg.TailnetPortMin && port <= selfcfg.TailnetPortMax {
			return portReservation{Port: port, Source: "previews",
				Detail: fmt.Sprintf("reserved for pull request previews' tailnet addresses (%d–%d)", selfcfg.TailnetPortMin, selfcfg.TailnetPortMax)}, true
		}
		return portReservation{}, false
	}
}

// ephemeralCheck passes over the kernel's ephemeral range: a port in it is
// handed to outgoing connections, so a service that restarts can find it
// taken by one.
func ephemeralCheck(span proxysvc.PortRange) reservationCheck {
	return func(port int) (portReservation, bool) {
		if uint32(port) >= span.Low && uint32(port) <= span.High {
			return portReservation{Port: port, Source: "ephemeral",
				Detail: fmt.Sprintf("inside the kernel's ephemeral range (%d–%d), which outgoing connections are given ports from", span.Low, span.High)}, true
		}
		return portReservation{}, false
	}
}

// firewallCheck passes over a port an enabled firewall already has an
// inbound rule for: whatever binds it is admitted from where that rule
// says, or refused by it, the moment it starts.
func firewallCheck(status *netsec.FirewallStatus, protocol string) reservationCheck {
	return func(port int) (portReservation, bool) {
		for _, rule := range status.Rules {
			direction := strings.ToLower(rule.Direction)
			if direction != "" && direction != "in" && direction != "input" {
				continue
			}
			if rule.Protocol != "" && !strings.EqualFold(rule.Protocol, protocol) && !strings.EqualFold(rule.Protocol, "all") {
				continue
			}
			if !portRuleMatches(rule.Port, strconv.Itoa(port)) {
				continue
			}
			action := strings.ToLower(rule.Action)
			from := strings.TrimSpace(rule.From)
			if from == "" || strings.EqualFold(from, "anywhere") || from == "0.0.0.0/0" || from == "::/0" {
				from = "anywhere"
			}
			verb := "admits it from " + from + ", so whatever binds it is reachable from there at once"
			if strings.Contains(action, "deny") || strings.Contains(action, "reject") || strings.Contains(action, "drop") {
				verb = "refuses it from " + from + ", so a service bound to it is not reachable from there"
			}
			return portReservation{Port: port, Source: "firewall", Detail: fmt.Sprintf("%s rule %d %s", status.Backend, rule.Number, verb)}, true
		}
		return portReservation{}, false
	}
}

// gatewayCheck passes over a port the dashboard's gateway forwards: an
// inbound connection to it is translated to the forward's target, so a
// local service bound to it would not receive it.
func gatewayCheck(forwards []netx.ForwardView, protocol string) reservationCheck {
	return func(port int) (portReservation, bool) {
		for _, f := range forwards {
			if !f.Enabled || (f.Protocol != protocol && f.Protocol != "both") || !forwardCovers(f.Ports, port) {
				continue
			}
			return portReservation{Port: port, Source: "gateway",
				Detail: fmt.Sprintf("the gateway forward %q translates it on %s to %s:%s", f.Name, f.Interface, f.Target, f.TargetPort)}, true
		}
		return portReservation{}, false
	}
}

func forwardCovers(ports string, port int) bool {
	first, last, ranged := strings.Cut(ports, "-")
	low, err := strconv.Atoi(strings.TrimSpace(first))
	if err != nil {
		return false
	}
	if !ranged {
		return port == low
	}
	high, err := strconv.Atoi(strings.TrimSpace(last))
	return err == nil && port >= low && port <= high
}

// freePortPolicy reads every owner of a future claim at once, each bounded
// like the ports page's owner sources. Provider reservations have no
// adapter, which the sources say rather than leave out.
func (s *Server) freePortPolicy(ctx context.Context, protocol, address string) portPolicy {
	ctx, cancel := context.WithTimeout(ctx, ownerSourceTimeout)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	policy := portPolicy{checks: map[string]reservationCheck{}}
	add := func(source portSource, check reservationCheck) {
		mu.Lock()
		defer mu.Unlock()
		policy.sources = append(policy.sources, source)
		if check != nil {
			policy.checks[source.Key] = check
		}
	}
	run := func(read func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			read()
		}()
	}
	run(func() {
		leases, err := s.deploymentPortLeases(ctx)
		if err != nil {
			add(portSource{Key: "deployments", Label: "Deployment port leases", State: portSourceUnavailable, Detail: err.Error()}, nil)
			return
		}
		add(portSource{Key: "deployments", Label: "Deployment port leases", State: portSourceChecked, Detail: fmt.Sprintf("%d active", len(leases))}, leaseCheck(leases, protocol, address))
	})
	run(func() {
		if s.modules.netsec == nil {
			add(portSource{Key: "firewall", Label: "Firewall rules", State: portSourceUnavailable, Detail: "no firewall owner"}, nil)
			return
		}
		status, err := s.modules.netsec.Status(ctx)
		switch {
		case err != nil:
			add(portSource{Key: "firewall", Label: "Firewall rules", State: portSourceUnavailable, Detail: err.Error()}, nil)
		case status == nil || !status.Available:
			add(portSource{Key: "firewall", Label: "Firewall rules", State: portSourceUnavailable, Detail: "no firewall this dashboard can read"}, nil)
		case status.Error != "":
			add(portSource{Key: "firewall", Label: "Firewall rules", State: portSourceUnavailable, Detail: status.Error}, nil)
		case !status.Enabled:
			add(portSource{Key: "firewall", Label: "Firewall rules", State: portSourceChecked, Detail: string(status.Backend) + " is not enabled; no rule applies"}, nil)
		default:
			add(portSource{Key: "firewall", Label: "Firewall rules", State: portSourceChecked, Detail: fmt.Sprintf("%s, %d rules", status.Backend, len(status.Rules))}, firewallCheck(status, protocol))
		}
	})
	run(func() {
		if s.modules.network == nil {
			add(portSource{Key: "gateway", Label: "Gateway forwards", State: portSourceUnavailable, Detail: "no network owner"}, nil)
			return
		}
		view, err := s.modules.network.Gateway(ctx)
		if err != nil || view == nil {
			detail := "the gateway could not be read"
			if err != nil {
				detail = err.Error()
			}
			add(portSource{Key: "gateway", Label: "Gateway forwards", State: portSourceUnavailable, Detail: detail}, nil)
			return
		}
		add(portSource{Key: "gateway", Label: "Gateway forwards", State: portSourceChecked, Detail: fmt.Sprintf("%d configured", len(view.Forwards))}, gatewayCheck(view.Forwards, protocol))
	})
	wg.Wait()
	add(portSource{Key: "previews", Label: "Preview tailnet range", State: portSourceChecked, Detail: fmt.Sprintf("%d–%d", selfcfg.TailnetPortMin, selfcfg.TailnetPortMax)}, previewCheck(protocol, address))
	if span, err := proxysvc.EphemeralPorts(); err == nil {
		add(portSource{Key: "ephemeral", Label: "Kernel ephemeral range", State: portSourceChecked, Detail: fmt.Sprintf("%d–%d", span.Low, span.High)}, ephemeralCheck(span))
	} else {
		add(portSource{Key: "ephemeral", Label: "Kernel ephemeral range", State: portSourceUnavailable, Detail: err.Error()}, nil)
	}
	add(portSource{Key: "provider", Label: "Provider reservations", State: portSourceNotSupplied,
		Detail: "No provider adapter is configured, so ports a provider reserves or its firewall refuses are unknown."}, nil)
	order := map[string]int{"deployments": 0, "previews": 1, "ephemeral": 2, "firewall": 3, "gateway": 4, "provider": 5}
	sort.SliceStable(policy.sources, func(i, j int) bool { return order[policy.sources[i].Key] < order[policy.sources[j].Key] })
	return policy
}

// deploymentPortLeases are the unexpired candidate-port leases deployments
// hold while a release starts.
func (s *Server) deploymentPortLeases(ctx context.Context) ([]portLease, error) {
	if s.Store == nil {
		return nil, fmt.Errorf("the store is unavailable")
	}
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT address, port, protocol, environment_id, expires_at
		FROM deploy_port_leases WHERE expires_at >= ? ORDER BY port LIMIT 1024`, time.Now().UTC().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []portLease{}
	ids := []int64{}
	environments := []int64{}
	for rows.Next() {
		var l portLease
		var environment, expires int64
		if err := rows.Scan(&l.Address, &l.Port, &l.Protocol, &environment, &expires); err != nil {
			return nil, err
		}
		l.ExpiresAt = time.Unix(expires, 0)
		out = append(out, l)
		environments = append(environments, environment)
		ids = append(ids, environment)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	names, _ := s.deploymentEnvironments(ctx, ids)
	for i := range out {
		out[i].Environment = names[environments[i]]
		if out[i].Environment == "" {
			out[i].Environment = fmt.Sprintf("deployment environment %d", environments[i])
		}
	}
	return out, nil
}
