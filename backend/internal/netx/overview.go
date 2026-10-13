package netx

import (
	"context"
	"encoding/json"
	"sort"
)

// DefaultRoute is one way off this machine: the device a default route leaves
// through and the gateway it hands traffic to.
type DefaultRoute struct {
	Family  string `json:"family"`
	Device  string `json:"device"`
	Gateway string `json:"gateway,omitempty"`
	Metric  int    `json:"metric,omitempty"`
}

// DefaultRoutes reads the main table's default routes in both families, lowest
// metric first, which is the order the kernel prefers them in.
func DefaultRoutes(ctx context.Context) []DefaultRoute {
	var out []DefaultRoute
	for _, family := range []string{"inet", "inet6"} {
		args := []string{"-j", "route", "show", "default"}
		if family == "inet6" {
			args = []string{"-j", "-6", "route", "show", "default"}
		}
		raw, err := run(ctx, "ip", args...)
		if err != nil {
			continue
		}
		var routes []struct {
			Dev     string `json:"dev"`
			Gateway string `json:"gateway"`
			Metric  int    `json:"metric"`
		}
		if json.Unmarshal([]byte(raw), &routes) != nil {
			continue
		}
		for _, r := range routes {
			if r.Dev != "" {
				out = append(out, DefaultRoute{Family: family, Device: r.Dev, Gateway: r.Gateway, Metric: r.Metric})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Family != out[j].Family {
			return out[i].Family == "inet"
		}
		return out[i].Metric < out[j].Metric
	})
	return out
}

// ForwardingSwitches is whether this machine routes packets that are not its
// own, as two plain switches for the Overview; the Routing page reads the
// fuller ForwardingView with what needs each.
type ForwardingSwitches struct {
	IPv4 bool `json:"ipv4"`
	IPv6 bool `json:"ipv6"`
}

// CurrentForwarding reads the two forwarding switches.
func CurrentForwarding() ForwardingSwitches {
	v4, _ := readSysctl(sysctlForwardV4)
	v6, _ := readSysctl(sysctlForwardV6)
	return ForwardingSwitches{IPv4: v4 == "1", IPv6: v6 == "1"}
}

// Finding is one thing on the Overview's attention list: what was measured,
// how bad it is, the page that fixes it, and the reading it was judged from
// (Source), so an incident is never closed by a reading that failed.
type Finding struct {
	ID     string `json:"id"`
	Level  string `json:"level"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Href   string `json:"href"`
	Source string `json:"source"`
}

// Observation is whether one of the readings the Overview is judged from
// arrived. A failed reading is reported as itself: the findings that depend
// on it are not judged, rather than judged against an empty answer.
type Observation struct {
	Source string `json:"source"`
	Label  string `json:"label"`
	// State is ok or failed.
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	Href  string `json:"href"`
}

// ObservationFailed reports whether the named reading failed.
func ObservationFailed(observations []Observation, source string) bool {
	for _, o := range observations {
		if o.Source == source && o.State == "failed" {
			return true
		}
	}
	return false
}

// CurrentConntrack is the connection-tracking table's fullness, for the
// Overview's attention list and the gateway's reading.
func CurrentConntrack() Conntrack { return readConntrack() }
