package netsec

import (
	"bufio"
	"strconv"
	"strings"
)

// ServicePreset is a port people actually open, with a name and an opinion.
//
// Every panel in this class puts a bare "port" box in front of the operator
// and leaves them to know that Redis is 6379 and that exposing it is the same
// as handing over the machine. The catalogue is the teaching layer for the
// firewall the way GLOSSARY is for Docker: picking "PostgreSQL" fills in 5432
// and says, before the rule is added, that this one belongs behind a private
// source rather than open to the world.
//
// It is deliberately short. A list of four hundred IANA assignments is a
// search box with extra steps; this is the set a single-server operator
// actually opens, plus the set they open by accident and regret.
type ServicePreset struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Port     string `json:"port"`
	Protocol string `json:"protocol"`
	// Detail says what the port is for, in one line, for somebody who does
	// not already know.
	Detail string `json:"detail"`
	// Danger is set on the ports that should almost never face the internet.
	// It is the reason to have a catalogue at all: the UI can warn at the
	// moment of choosing rather than after the fact.
	Danger string `json:"danger,omitempty"`
	// InternetOnly says the danger is strangers on the internet — an open
	// resolver amplifying attacks, a remote desktop brute-forced from
	// everywhere — so a bind only a bridge, a tailnet or a VPN can reach is
	// what the service is for rather than a risk.
	InternetOnly bool `json:"-"`
	// Firewalld is the name firewalld's predefined service for the port goes
	// by, where it ships one: a zone that lists "redis" among its services
	// opens 6379 without naming the port.
	Firewalld string `json:"-"`
}

// ServiceCatalogue is the whole list, ordered as it should be offered:
// the everyday ones first, then the ones that want a source restriction.
var ServiceCatalogue = []ServicePreset{
	{Key: "ssh", Name: "SSH", Port: "22", Protocol: "tcp",
		Detail: "Remote shell. Restrict the source if you can — this is the port the internet knocks on all day."},
	{Key: "http", Name: "HTTP", Port: "80", Protocol: "tcp",
		Detail: "Plain web traffic. Needed for Let's Encrypt's HTTP challenge even on a TLS-only site."},
	{Key: "https", Name: "HTTPS", Port: "443", Protocol: "tcp",
		Detail: "Web traffic over TLS. The one port most servers should have open."},
	{Key: "http3", Name: "HTTP/3 (QUIC)", Port: "443", Protocol: "udp",
		Detail: "HTTP/3 rides UDP 443. Open it alongside TCP 443 if your proxy offers QUIC."},
	{Key: "dns", Name: "DNS", Port: "53", Protocol: "udp",
		Detail:       "Only if this host answers DNS queries for others.",
		Danger:       "An open resolver is used to amplify attacks against other people. Restrict the source.",
		InternetOnly: true, Firewalld: "dns"},
	{Key: "smtp", Name: "SMTP", Port: "25", Protocol: "tcp",
		Detail: "Mail delivery between servers."},
	{Key: "submission", Name: "Mail submission", Port: "587", Protocol: "tcp",
		Detail: "Where mail clients hand outgoing mail to your server."},
	{Key: "imaps", Name: "IMAPS", Port: "993", Protocol: "tcp",
		Detail: "Mail clients reading mail over TLS."},
	{Key: "wireguard", Name: "WireGuard", Port: "51820", Protocol: "udp",
		Detail: "VPN. Opening this is usually how you close everything else."},
	{Key: "tailscale", Name: "Tailscale (direct)", Port: "41641", Protocol: "udp",
		Detail: "Lets Tailscale make direct connections instead of relaying. Optional — it works without."},
	{Key: "postgres", Name: "PostgreSQL", Port: "5432", Protocol: "tcp",
		Detail: "Database. Applications on this machine reach it over loopback without any rule.",
		Danger: "A database open to the internet is scanned and brute-forced within hours. Set a source.", Firewalld: "postgresql"},
	{Key: "mysql", Name: "MySQL / MariaDB", Port: "3306", Protocol: "tcp",
		Detail: "Database. Applications on this machine reach it over loopback without any rule.",
		Danger: "A database open to the internet is scanned and brute-forced within hours. Set a source.", Firewalld: "mysql"},
	{Key: "redis", Name: "Redis", Port: "6379", Protocol: "tcp",
		Detail: "Cache and queue. Ships with no password by default.",
		Danger: "Unauthenticated by default: an exposed Redis is a remote shell, not a data leak. Never open this to the world.", Firewalld: "redis"},
	{Key: "mongodb", Name: "MongoDB", Port: "27017", Protocol: "tcp",
		Detail: "Database.",
		Danger: "Exposed MongoDB instances are the classic ransom target. Set a source.", Firewalld: "mongodb"},
	{Key: "memcached", Name: "Memcached", Port: "11211", Protocol: "tcp",
		Detail: "Cache.",
		Danger: "Unauthenticated, and famous for amplifying attacks against third parties. Keep it on loopback.", Firewalld: "memcache"},
	{Key: "elasticsearch", Name: "Elasticsearch", Port: "9200", Protocol: "tcp",
		Detail: "Search index.",
		Danger: "No authentication in the default configuration. Do not expose.", Firewalld: "elasticsearch"},
	{Key: "docker", Name: "Docker API", Port: "2375", Protocol: "tcp",
		Detail: "The Docker daemon's TCP socket.",
		Danger: "Reaching the Docker API is equivalent to being root on this host. Never open it."},
	{Key: "rdp", Name: "RDP", Port: "3389", Protocol: "tcp",
		Detail:       "Windows remote desktop.",
		Danger:       "Brute-forced constantly. Put it behind a VPN.",
		InternetOnly: true, Firewalld: "rdp"},
	{Key: "vnc", Name: "VNC", Port: "5900", Protocol: "tcp",
		Detail:       "Remote desktop.",
		Danger:       "Weak or absent authentication in most configurations. Put it behind a VPN.",
		InternetOnly: true, Firewalld: "vnc-server"},
	{Key: "ftp", Name: "FTP", Port: "21", Protocol: "tcp",
		Detail: "File transfer.",
		Danger: "Credentials cross the network in plain text. Use SFTP over the SSH port instead.", Firewalld: "ftp"},
}

// PresetFor finds the catalogue entry for a port and protocol, so a rule or a
// listening socket can be named rather than left as a number. An empty
// protocol matches the first entry for the port.
func PresetFor(port, protocol string) (ServicePreset, bool) {
	protocol = strings.ToLower(protocol)
	for _, p := range ServiceCatalogue {
		if p.Port != port {
			continue
		}
		if protocol == "" || protocol == p.Protocol {
			return p, true
		}
	}
	return ServicePreset{}, false
}

// dashboardService is the dashboard's own sockets as the posture judges
// them. Only its Caddy is meant to face the network: the backend and the web
// app behind it serve plain HTTP, without the TLS, headers and rate limits
// Caddy puts in front of them, so one bound where the internet may reach it
// is the admin panel of this server open past its own proxy. It is not in
// the catalogue, which the firewall form offers as ports to open.
var dashboardService = ServicePreset{
	Key: "dashboard", Name: "Just Dashboard",
	Detail: "The dashboard's own backend or web app, which its Caddy proxies from loopback.",
	Danger: "This is the dashboard itself, served without TLS or the proxy's protections in front of it, to anyone who can reach the address. Only the dashboard's Caddy should face the network.",
	// A bind on a tailnet or a bridge is the operator reaching their own
	// panel over their own network; the invariant is about the internet.
	InternetOnly: true,
}

// ServiceOf is what a socket is, as the posture judges it: the dashboard's
// own when it is one of the dashboard's sockets other than Caddy's, and
// otherwise the catalogue's entry for its port and protocol. The protocol
// must match: DNS is flagged on 53/udp, where resolvers amplify, and a TCP
// socket on 53 is not the same service.
func ServiceOf(l ExposedPort) (ServicePreset, bool) {
	if l.Dashboard {
		return dashboardService, true
	}
	if l.Protocol == "" {
		return ServicePreset{}, false
	}
	return PresetFor(strconv.FormatUint(uint64(l.Port), 10), l.Protocol)
}

// AppProfile is a named service bundle the host itself defines — ufw's
// application profiles from /etc/ufw/applications.d ("Nginx Full", "OpenSSH"),
// or firewalld's predefined services ("http", "postgresql").
//
// They are worth surfacing because they are the form the host's own packages
// speak: a rule added as "Nginx Full" keeps meaning what it says if the
// package later adds a port, and reads better in the rule list than 80,443.
type AppProfile struct {
	Name        string   `json:"name"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Ports       []string `json:"ports"`
}

// parseAppList reads the indented names under "Available applications:".
func parseAppList(out string) []string {
	names := []string{}
	started := false
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "Available applications") {
			started = true
			continue
		}
		if !started {
			continue
		}
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// parseAppInfo reads one profile's title, description and ports out of
// `ufw app info`, whose shape is a labelled block followed by a Ports: heading
// with the ports on the next line.
func parseAppInfo(out string) (title, description string, ports []string) {
	ports = []string{}
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "Title:"):
			title = strings.TrimSpace(strings.TrimPrefix(trimmed, "Title:"))
		case strings.HasPrefix(trimmed, "Description:"):
			description = strings.TrimSpace(strings.TrimPrefix(trimmed, "Description:"))
		case trimmed == "Ports:":
			for _, rest := range lines[i+1:] {
				rest = strings.TrimSpace(rest)
				if rest == "" {
					continue
				}
				// ufw's own spelling is "80,443/tcp|137,138/udp": the comma
				// groups a list under one protocol and the pipe separates
				// protocols. Splitting on the comma would turn "80,443/tcp"
				// into a port 80 with no protocol at all.
				for _, p := range strings.Split(rest, "|") {
					if p = strings.TrimSpace(p); p != "" {
						ports = append(ports, p)
					}
				}
				break
			}
		}
	}
	return title, description, ports
}
