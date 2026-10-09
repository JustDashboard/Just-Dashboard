package netsec

import (
	"context"
	"slices"
	"strconv"
	"strings"
)

// DockerDNAT is one translation Docker wrote for a published port, as its
// nat table's DOCKER chain lists it.
type DockerDNAT struct {
	Protocol string `json:"protocol"`
	// HostIP is the -d match: the address the port is published on, empty
	// for every address.
	HostIP string `json:"hostIp,omitempty"`
	// FirstPort and LastPort are the --dport match, equal for one port.
	FirstPort int `json:"firstPort"`
	LastPort  int `json:"lastPort"`
	// ExceptInterface is the "! -i" match: traffic from the container's own
	// bridge is not translated, which is how hairpin traffic is told apart.
	ExceptInterface string `json:"exceptInterface,omitempty"`
	// To is the --to-destination: the container address and port.
	To  string `json:"to"`
	Raw string `json:"raw"`
}

// Covers says whether the translation applies to a host address and port.
func (d DockerDNAT) Covers(protocol, hostIP string, port int) bool {
	if d.Protocol != protocol || port < d.FirstPort || port > d.LastPort {
		return false
	}
	return d.HostIP == "" || strings.TrimSuffix(strings.TrimSuffix(d.HostIP, "/32"), "/128") == hostIP
}

// DockerChains is Docker's iptables chains for one address family, read as
// text. Each part carries its own error: an unreadable nat table and an
// empty one are different answers.
type DockerChains struct {
	Family        string       `json:"family"`
	NAT           []DockerDNAT `json:"nat"`
	NATError      string       `json:"natError,omitempty"`
	User          []string     `json:"user"`
	UserError     string       `json:"userError,omitempty"`
	ForwardPolicy string       `json:"forwardPolicy,omitempty"`
	Forward       []string     `json:"forward"`
	ForwardError  string       `json:"forwardError,omitempty"`
	// Filter is the filter table's DOCKER chain: the per-container accepts
	// FORWARD reaches through Docker's own jump.
	Filter      []string `json:"filter"`
	FilterError string   `json:"filterError,omitempty"`
}

// dockerJumps are the chains Docker's FORWARD jumps lead to, across Engine
// versions: DOCKER-FORWARD since 28, DOCKER and its isolation stages before.
var dockerJumps = []string{"DOCKER-FORWARD", "DOCKER", "DOCKER-ISOLATION-STAGE-1"}

// ForwardOrder reads FORWARD's jumps in order: the chains Docker's own
// accepts are reached through, whether they come before any ufw chain, and
// the foreign chains consulted ahead of them, which are not evaluated.
func (c DockerChains) ForwardOrder() (dockerFirst bool, ahead []string) {
	for _, rule := range c.Forward {
		target := jumpTarget(rule)
		switch {
		case target == "":
		case target == "DOCKER-USER":
		case slices.Contains(dockerJumps, target):
			return true, ahead
		case strings.HasPrefix(target, "ufw-") || strings.HasPrefix(target, "ufw6-"):
			return false, ahead
		default:
			ahead = append(ahead, target)
		}
	}
	return false, ahead
}

// DockerAccept is Docker's filter rule admitting forwarded traffic to one
// container address and port, the decisive accept of a published port.
func (c DockerChains) DockerAccept(protocol, address string, port int) string {
	destination := "-d " + address + "/"
	for _, rule := range c.Filter {
		if strings.Contains(rule, destination) && strings.Contains(rule, "-p "+protocol+" ") &&
			strings.Contains(rule, "--dport "+strconv.Itoa(port)+" ") && strings.HasSuffix(rule, "-j ACCEPT") {
			return rule
		}
	}
	return ""
}

func jumpTarget(rule string) string {
	fields := strings.Fields(rule)
	for i, f := range fields {
		if (f == "-j" || f == "-g") && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}

// ReadDockerChains lists the DOCKER nat chain, DOCKER-USER and FORWARD of one
// family. It reads only: `-S` prints rules and never changes them.
func ReadDockerChains(ctx context.Context, family string) DockerChains {
	tool := "iptables"
	if family == "inet6" {
		tool = "ip6tables"
	}
	out := DockerChains{Family: family, NAT: []DockerDNAT{}, User: []string{}, Forward: []string{}, Filter: []string{}}
	if text, err := run(ctx, tool, "-t", "nat", "-S", "DOCKER"); err != nil {
		out.NATError = err.Error()
	} else {
		out.NAT = ParseDockerNAT(text)
	}
	if text, err := run(ctx, tool, "-S", "DOCKER-USER"); err != nil {
		out.UserError = err.Error()
	} else {
		_, out.User = ParseChainRules(text, "DOCKER-USER")
	}
	if text, err := run(ctx, tool, "-S", "FORWARD"); err != nil {
		out.ForwardError = err.Error()
	} else {
		out.ForwardPolicy, out.Forward = ParseChainRules(text, "FORWARD")
	}
	if text, err := run(ctx, tool, "-S", "DOCKER"); err != nil {
		out.FilterError = err.Error()
	} else {
		_, out.Filter = ParseChainRules(text, "DOCKER")
	}
	return out
}

// ParseChainRules reads `iptables -S CHAIN`: the policy of a built-in chain
// and the rules appended to it, in order.
func ParseChainRules(text, chain string) (policy string, rules []string) {
	rules = []string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "-P "+chain+" "):
			policy = strings.TrimSpace(strings.TrimPrefix(line, "-P "+chain+" "))
		case strings.HasPrefix(line, "-A "+chain+" "):
			rules = append(rules, line)
		}
	}
	return policy, rules
}

// ParseDockerNAT reads the DNAT rules of Docker's nat DOCKER chain. A rule
// in another shape (a RETURN for a bridge, a rule added by hand) is left
// out rather than guessed at.
func ParseDockerNAT(text string) []DockerDNAT {
	out := []DockerDNAT{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "-A DOCKER ") || !strings.Contains(line, "-j DNAT") {
			continue
		}
		fields := strings.Fields(line)
		rule := DockerDNAT{Raw: line}
		for i := 2; i < len(fields); i++ {
			next := func() string {
				if i+1 < len(fields) {
					i++
					return fields[i]
				}
				return ""
			}
			switch fields[i] {
			case "-d":
				rule.HostIP = next()
			case "!":
				if i+1 < len(fields) && fields[i+1] == "-i" {
					i++
					rule.ExceptInterface = next()
				}
			case "-p":
				rule.Protocol = strings.ToLower(next())
			case "--dport":
				first, last, ranged := strings.Cut(next(), ":")
				rule.FirstPort, _ = strconv.Atoi(first)
				rule.LastPort = rule.FirstPort
				if ranged {
					rule.LastPort, _ = strconv.Atoi(last)
				}
			case "--to-destination":
				rule.To = next()
			}
		}
		if rule.Protocol != "" && rule.FirstPort > 0 && rule.To != "" {
			out = append(out, rule)
		}
	}
	return out
}
