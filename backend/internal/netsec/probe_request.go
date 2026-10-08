package netsec

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// ProbeRequest is the closed vocabulary shared by quick and retained diagnostics.
// It never carries a command, shell fragment, capture filter or filesystem path.
type ProbeRequest struct {
	Tool   string `json:"tool"`
	Target string `json:"target"`
	Port   int    `json:"port,omitempty"`
	Record string `json:"record,omitempty"`
	Option string `json:"option,omitempty"`
}

// ValidateProbeRequest performs the existing tools' validation before work is
// queued. Normalising unused fields makes saved comparisons about the same test.
func ValidateProbeRequest(req ProbeRequest) (ProbeRequest, error) {
	req.Target = strings.TrimSpace(req.Target)
	req.Record = strings.ToUpper(strings.TrimSpace(req.Record))
	rawOption := strings.TrimSpace(req.Option)
	req.Option = strings.ToLower(rawOption)
	needsTarget, usesPort := true, false
	switch req.Tool {
	case "ping", "traceroute", "scan", "whois", "dnsauth", "mtu":
	case "dns":
		if req.Record == "" {
			req.Record = "A"
		}
		if !dnsTypes[req.Record] {
			return req, fmt.Errorf("record type must be one of A, AAAA, MX, TXT, NS, CNAME or PTR")
		}
		if req.Record == "PTR" && net.ParseIP(req.Target) == nil {
			return req, fmt.Errorf("a PTR lookup takes an IP address")
		}
	case "port", "banner":
		usesPort = true
	case "ssh":
		usesPort = true
		if req.Port == 0 {
			req.Port = 22
		}
	case "http", "tls", "tlssurvey", "httpsec", "siteaudit":
		usesPort = true
		if req.Port == 0 {
			req.Port = 443
		}
	case "starttls":
		usesPort = true
		if req.Option == "" {
			req.Option = "smtp"
		}
		if _, ok := starttlsProtocols[req.Option]; !ok {
			return req, fmt.Errorf("protocol must be one of smtp, imap, pop3 or ftp")
		}
		if req.Port == 0 {
			req.Port = starttlsDefaultPort[req.Option]
		}
		if req.Port == 465 || req.Port == 993 || req.Port == 995 {
			return req, fmt.Errorf("port %d is implicit TLS; use the TLS tool", req.Port)
		}
	case "dnsbl", "asn":
		if net.ParseIP(req.Target) == nil {
			return req, fmt.Errorf("this lookup takes an IP address, not a name")
		}
	case "mx":
		if net.ParseIP(req.Target) != nil {
			return req, fmt.Errorf("a mail check takes a domain, not an IP address")
		}
	case "route":
		a, err := netip.ParseAddr(req.Target)
		if err != nil || a.Zone() != "" {
			return req, fmt.Errorf("route lookup takes an IPv4 or IPv6 address without a zone")
		}
		req.Target = a.Unmap().String()
	case "listeners", "egress", "neigh", "capabilities":
		needsTarget = false
		req.Target = ""
	case "capture":
		needsTarget = false
		if req.Option == "" {
			req.Option = "all"
		}
		if req.Option != "all" && req.Option != "tcp" && req.Option != "udp" && req.Option != "icmp" && req.Option != "icmp6" {
			return req, fmt.Errorf("protocol must be all, tcp, udp, icmp or icmp6")
		}
		if _, err := liveLANInterface(req.Target); err != nil {
			return req, err
		}
	case "wol":
		needsTarget = false
		if _, err := magicPacket(req.Target); err != nil {
			return req, err
		}
		// Interface names are case-sensitive, unlike the protocol choices.
		req.Option = rawOption
		iface, err := liveLANInterface(req.Option)
		if err != nil {
			return req, err
		}
		if iface.Flags&net.FlagBroadcast == 0 || iface.Flags&net.FlagLoopback != 0 || len(iface.HardwareAddr) != 6 {
			return req, fmt.Errorf("Wake-on-LAN needs a broadcast-capable Ethernet interface, bridge or VLAN")
		}
	default:
		return req, fmt.Errorf("unknown network diagnostic tool %q", req.Tool)
	}
	if needsTarget && !ValidTarget(req.Target) {
		return req, fmt.Errorf("target must be a hostname or IP address")
	}
	if usesPort && (req.Port < 1 || req.Port > 65535) {
		return req, fmt.Errorf("port must be between 1 and 65535")
	}
	if !usesPort {
		req.Port = 0
	}
	if req.Tool != "dns" {
		req.Record = ""
	}
	if req.Tool != "starttls" && req.Tool != "capture" && req.Tool != "wol" {
		req.Option = ""
	}
	return req, nil
}
