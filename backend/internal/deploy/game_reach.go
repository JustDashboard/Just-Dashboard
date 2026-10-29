package deploy

import (
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
)

// GameReach is where a game server's players connect and whether anything
// between them and it lets them: the port and protocol it is published on,
// the address it is bound to, and what the host firewall does with it. It is
// read from the live release's own snapshot, not the desired plan, so it
// describes what is running.
type GameReach struct {
	Port int `json:"port"`
	// Protocol is tcp, udp, or tcp+udp for one number published on both.
	Protocol string `json:"protocol"`
	Bind     string `json:"bind"`
	// Public is a bind on every interface. Loopback is where every game
	// starts: only this server can connect until the operator chooses
	// otherwise under Runtime.
	Public bool `json:"public"`
	// Firewall is "admits" (an allow rule matches), "drops" (the firewall is
	// on, drops by default and nothing matches), "off", "open" (it accepts
	// incoming traffic by default) or "unknown", with Reason saying why it
	// could not be read.
	Firewall string `json:"firewall"`
	Reason   string `json:"reason,omitempty"`
	// Editable is whether the firewall can be changed from the dashboard,
	// which is what the page's Allow action needs.
	Editable bool `json:"editable,omitempty"`
}

// ReleaseRuntimePlan is the runtime plan a release was frozen with, read
// from its verified snapshot.
func ReleaseRuntimePlan(release *ReleaseWithArtifacts) (RuntimePlanConfig, error) {
	snapshot, err := decodeReleaseRuntimeSnapshot(release)
	if err != nil {
		return RuntimePlanConfig{}, err
	}
	return snapshot.Plan, nil
}

// DescribeGameReach answers for a game release's fixed port. A release with
// none (a game deployed some other way) has no reach to describe.
func DescribeGameReach(plan RuntimePlanConfig, firewall *netsec.FirewallStatus, firewallErr error) (GameReach, bool) {
	if plan.HostPort == 0 || plan.HostNetwork {
		return GameReach{}, false
	}
	protocols := plan.publishedProtocols()
	reach := GameReach{Port: plan.HostPort, Protocol: strings.Join(protocols, "+"), Bind: plan.BindAddress, Public: publicRuntimeBind(plan)}
	if reach.Bind == "" {
		reach.Bind = "127.0.0.1"
	}
	switch {
	case firewallErr != nil || firewall == nil || !firewall.Available:
		reach.Firewall, reach.Reason = "unknown", "The firewall could not be read."
		if firewallErr != nil {
			reach.Reason = firewallErr.Error()
		}
		if firewall != nil && firewall.Error != "" {
			reach.Reason = firewall.Error
		}
	case !firewall.Enabled:
		reach.Firewall = "off"
	case strings.HasPrefix(strings.ToLower(firewall.Policy.Incoming), "allow"):
		reach.Firewall = "open"
	case firewallAllowsPorts(firewall.Rules, portObservations(reach.Port, protocols)):
		reach.Firewall = "admits"
	default:
		reach.Firewall = "drops"
	}
	if firewall != nil {
		reach.Editable = firewall.Capabilities.Editable
	}
	return reach, true
}

func portObservations(port int, protocols []string) []PortObservation {
	observations := make([]PortObservation, 0, len(protocols))
	for _, protocol := range protocols {
		observations = append(observations, PortObservation{Port: port, Protocol: protocol})
	}
	return observations
}

// PublicRuntimeBind is whether a plan publishes on every interface.
func PublicRuntimeBind(plan RuntimePlanConfig) bool {
	return publicRuntimeBind(plan)
}
