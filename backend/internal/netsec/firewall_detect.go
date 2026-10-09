package netsec

import (
	"context"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// availableOnHost reports whether the host has a tool; a variable so tests
// can describe a host other than the one they run on.
var availableOnHost = hostexec.AvailableOnHost

// BackendDetection is one firewall this host could be run by. A binary on
// disk is not a firewall in charge: Debian images ship ufw inactive beside a
// running firewalld, and an iptables binary exists everywhere. Active is
// active, inactive, unknown (the tool could not be asked) or not_checked
// (a higher-priority firewall is already in charge).
type BackendDetection struct {
	Backend   Backend `json:"backend"`
	Installed bool    `json:"installed"`
	Active    string  `json:"active"`
	Selected  bool    `json:"selected"`
	Reason    string  `json:"reason"`
}

// activity is how a backend reports whether it is the one enforcing now.
type activity interface {
	Activity(ctx context.Context) (state, detail string)
}

// candidates are the backends in preference order, the owned nftables table
// before raw iptables where the network module offers it.
func (s *Service) candidates() []fwBackend {
	list := []fwBackend{ufwBackend{}, firewalldBackend{}}
	if s.owned != nil {
		list = append(list, nftOwnedBackend{owner: s.owned})
	}
	return append(list, iptablesBackend{})
}

// selectBackend chooses the firewall in charge: the first active front end,
// then the first installed one that can be switched on, then the owned
// table, then raw iptables for reading. Two active front ends are a conflict
// the status reports and every write refuses.
func (s *Service) selectBackend(ctx context.Context) (fwBackend, []BackendDetection) {
	list := s.candidates()
	detection := make([]BackendDetection, len(list))
	decided := ""
	for i, b := range list {
		d := BackendDetection{Backend: b.Kind(), Active: "unknown"}
		frontEnd := b.Kind() == BackendUFW || b.Kind() == BackendFirewalld
		switch d.Installed = b.Detect(); {
		case !d.Installed:
			d.Active, d.Reason = "inactive", string(b.Kind())+" is not installed on this host."
		case decided != "" && !frontEnd:
			// Both front ends are always asked, so a conflict is seen; past
			// an active one the rest cannot be in charge and are not probed.
			d.Active, d.Reason = "not_checked", decided+" is in charge, so this was not asked."
		default:
			if a, ok := b.(activity); ok {
				d.Active, d.Reason = a.Activity(ctx)
			}
			if d.Active == "active" && decided == "" {
				decided = string(b.Kind())
			}
		}
		detection[i] = d
	}
	pick := -1
	activeFrontEnds := 0
	for i, d := range detection {
		if (d.Backend == BackendUFW || d.Backend == BackendFirewalld) && d.Active == "active" {
			activeFrontEnds++
			if pick < 0 {
				pick = i
			}
		}
	}
	if pick < 0 {
		for i, d := range detection {
			if d.Backend == BackendNFTOwned && d.Active == "active" {
				pick = i
				break
			}
		}
	}
	if pick < 0 {
		for i, d := range detection {
			if d.Installed {
				pick = i
				break
			}
		}
	}
	if pick < 0 {
		return nil, detection
	}
	detection[pick].Selected = true
	if activeFrontEnds > 1 {
		for i := range detection {
			if detection[i].Active == "active" && (detection[i].Backend == BackendUFW || detection[i].Backend == BackendFirewalld) {
				detection[i].Reason = "Both ufw and firewalld are running; they program the same kernel tables, so an edit through one leaves the other's rules in force."
			}
		}
	}
	return list[pick], detection
}

// conflicted reports two front ends enforcing at once.
func conflicted(detection []BackendDetection) bool {
	active := 0
	for _, d := range detection {
		if d.Active == "active" && (d.Backend == BackendUFW || d.Backend == BackendFirewalld) {
			active++
		}
	}
	return active > 1
}

// capabilitiesFor is a backend's capabilities, withdrawn while another front
// end also enforces: writes would describe a firewall that is not the whole
// story.
func capabilitiesFor(b fwBackend, detection []BackendDetection) FirewallCapabilities {
	caps := b.Capabilities()
	if conflicted(detection) {
		return FirewallCapabilities{Profiles: caps.Profiles, ReadOnlyReason: "Both ufw and firewalld are active on this host. They program the same kernel tables, so a change made through one would leave the other's rules in force. Stop one of them, then manage the other from here."}
	}
	return caps
}

// writable resolves the backend for a write and refuses where the host's
// firewall cannot honestly be changed from here.
func (s *Service) writable(ctx context.Context, need func(FirewallCapabilities) bool) (fwBackend, error) {
	b, detection := s.selectBackend(ctx)
	if b == nil {
		return nil, ErrNoFirewall
	}
	caps := capabilitiesFor(b, detection)
	if !need(caps) {
		reason := caps.ReadOnlyReason
		if reason == "" {
			reason = string(b.Kind()) + " cannot make this change"
		}
		return nil, readOnly(reason)
	}
	return b, nil
}

func readOnly(reason string) error {
	return &readOnlyError{reason: reason}
}

type readOnlyError struct{ reason string }

func (e *readOnlyError) Error() string { return ErrReadOnly.Error() + ": " + e.reason }
func (e *readOnlyError) Unwrap() error { return ErrReadOnly }

// Activity reads ufw's own status line.
func (ufwBackend) Activity(ctx context.Context) (string, string) {
	out, err := run(ctx, "ufw", "status")
	for _, line := range strings.Split(out, "\n") {
		if state, ok := strings.CutPrefix(strings.TrimSpace(line), "Status:"); ok {
			if strings.TrimSpace(state) == "active" {
				return "active", "ufw reports itself active."
			}
			return "inactive", "ufw is installed and inactive."
		}
	}
	if err != nil {
		return "unknown", "ufw could not be asked: " + err.Error()
	}
	return "unknown", "ufw printed no status line."
}

// Activity reads firewalld's state, which it prints even while it exits
// non-zero for a stopped daemon.
func (firewalldBackend) Activity(ctx context.Context) (string, string) {
	out, err := run(ctx, "firewall-cmd", "--state")
	switch strings.TrimSpace(out) {
	case "running":
		return "active", "firewalld reports itself running."
	case "not running":
		return "inactive", "firewalld is installed and stopped."
	}
	if err != nil && strings.Contains(err.Error(), "not running") {
		return "inactive", "firewalld is installed and stopped."
	}
	if err != nil {
		return "unknown", "firewalld could not be asked: " + err.Error()
	}
	return "unknown", "firewalld printed an unrecognised state."
}

// Activity reports raw iptables as enforcing when it holds a rule or a
// non-accept policy in a filter chain.
func (iptablesBackend) Activity(ctx context.Context) (string, string) {
	out, err := run(ctx, "iptables", "-S")
	if err != nil {
		return "unknown", "iptables could not be read: " + err.Error()
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "-P" && fields[2] != "ACCEPT" {
			return "active", "iptables holds a " + fields[2] + " policy on " + fields[1] + "."
		}
		if len(fields) >= 1 && fields[0] == "-A" {
			return "active", "iptables holds rules no front end manages."
		}
	}
	return "inactive", "iptables holds no rules and accepts by default."
}
