package proxysvc

import (
	"strconv"
	"strings"
)

// SiteForName is the enabled nginx site that answers name over TLS on port,
// picked as nginx picks among server_name entries: the exact name first, then
// the longest wildcard in front, then the longest behind. A name no site
// claims goes to the port's default server, which says nothing about a form
// written for another name, so it is nil then, as it is for a site listening
// on another port or matching only by regular expression.
//
// This is the part of tracing a name to its site that the deep scan needs;
// the TLS report's served-by trace answers the whole question.
func SiteForName(vhosts []VHost, name string, port int) *VHost {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	var best *VHost
	bestRank, bestLength := 0, 0
	for i := range vhosts {
		v := &vhosts[i]
		if v.Kind != KindNginx || !v.Enabled || !listensTLSOn(v.Listen, port) {
			continue
		}
		for _, entry := range v.ServerNames {
			rank, length := serverNameMatch(strings.ToLower(entry), name)
			if rank > bestRank || (rank == bestRank && rank > 0 && length > bestLength) {
				best, bestRank, bestLength = v, rank, length
			}
		}
	}
	return best
}

// serverNameMatch ranks one server_name entry against name: 3 exact, 2 a
// leading wildcard (*.example.com, or .example.com, which also takes the bare
// name), 1 a trailing one (www.example.*), 0 no match. length breaks ties the
// way nginx does, the longest wildcard winning.
func serverNameMatch(entry, name string) (rank, length int) {
	switch {
	case entry == "" || strings.HasPrefix(entry, "~"):
		return 0, 0
	case entry == name:
		return 3, len(entry)
	case strings.HasPrefix(entry, "*."):
		if strings.HasSuffix(name, entry[1:]) {
			return 2, len(entry)
		}
	case strings.HasPrefix(entry, "."):
		if name == entry[1:] || strings.HasSuffix(name, entry) {
			return 2, len(entry)
		}
	case strings.HasSuffix(entry, ".*"):
		if strings.HasPrefix(name, entry[:len(entry)-1]) {
			return 1, len(entry)
		}
	}
	return 0, 0
}

// listensTLSOn reports a TCP listen with TLS on port among a site's listen
// values. A quic listen is UDP, and HTTP/3's business.
func listensTLSOn(listens []string, port int) bool {
	for _, value := range listens {
		fields := strings.Fields(value)
		if len(fields) == 0 || hasField(value, "quic") || !listenIsTLS(value) {
			continue
		}
		if listenPort(fields[0]) == port {
			return true
		}
	}
	return false
}

// listenPort is the port in a listen directive's address: 443, *:443,
// 127.0.0.1:443 and [::]:443 are all 443, and an address with no port is 80.
func listenPort(address string) int {
	if strings.HasPrefix(address, "unix:") {
		return 0
	}
	if i := strings.LastIndex(address, ":"); i >= 0 && !strings.HasSuffix(address, "]") {
		address = address[i+1:]
	} else if strings.Contains(address, ".") || strings.HasSuffix(address, "]") || address == "*" {
		return 80
	}
	port, err := strconv.Atoi(address)
	if err != nil {
		// A name without a port: nginx listens on 80.
		return 80
	}
	return port
}
