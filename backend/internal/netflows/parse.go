package netflows

import (
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

var ownersRE = regexp.MustCompile(`\("([^"]{1,128})",pid=([0-9]+),fd=([0-9]+)\)`)

// SS uses -O: every native row and its optional TCP_INFO fields stay together.
// Missing fields remain nil, including fields ss suppresses when zero.
func parseSS(out string, cap int) Source {
	s := Source{Status: "observed", Values: []Socket{}}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) < 6 || (f[0] != "tcp" && f[0] != "udp") {
			s.Malformed++
			continue
		}
		if f[1] == "LISTEN" {
			continue
		}
		if len(s.Values) >= cap {
			s.Truncated = true
			break
		}
		local, lp, valid := endpoint(f[4])
		if !valid {
			s.Malformed++
			continue
		}
		remote, rp, valid := endpoint(f[5])
		if !valid {
			s.Malformed++
			continue
		}
		sk := Socket{Protocol: f[0], State: f[1], LocalAddress: local, LocalPort: lp, RemoteAddress: remote, RemotePort: rp, Owner: Owner{Status: "unknown", Reason: "No revalidated descriptor owner is available."}}
		sk.LocalEndpoint, sk.RemoteEndpoint = clip(f[4], 256), clip(f[5], 256)
		if rp == 0 {
			sk.RemoteAddress = ""
		}
		if sk.Protocol == "udp" && (sk.RemoteAddress == "" || rp == 0) {
			s.UnconnectedUDP++
		}
		for _, tok := range f[6:] {
			key, value, ok := strings.Cut(tok, ":")
			if !ok {
				continue
			}
			switch key {
			case "ino":
				if _, err := strconv.ParseUint(value, 10, 64); err == nil && value != "0" {
					sk.Inode = value
				}
			case "sk":
				if cookie, err := strconv.ParseUint(value, 16, 64); err == nil && cookie != 0 {
					sk.Cookie = strconv.FormatUint(cookie, 16)
				}
			case "bytes_sent", "bytes_received", "retrans", "lost":
				if sk.Protocol != "tcp" {
					continue
				}
				if key == "retrans" {
					_, value, ok = strings.Cut(value, "/")
					if !ok {
						continue
					}
				}
				n, err := strconv.ParseUint(value, 10, 64)
				if err != nil {
					s.Malformed++
					continue
				}
				switch key {
				case "bytes_sent":
					sk.Tx = &n
				case "bytes_received":
					sk.Rx = &n
				case "retrans":
					sk.Retrans = &n
				case "lost":
					sk.Lost = &n
				}
			}
		}
		matches := ownersRE.FindAllStringSubmatch(line, 5)
		if sk.Cookie == "" || sk.Inode == "" {
			s.IdentityUnavailable++
		}
		if len(matches) > 4 {
			sk.Owner.Status, sk.Owner.Reason = "shared", "More than four descriptor owners exceed attribution coverage."
		} else {
			for _, match := range matches {
				pid, _ := strconv.Atoi(match[2])
				fd, _ := strconv.Atoi(match[3])
				sk.owners = append(sk.owners, socketOwner{Name: match[1], PID: pid, FD: fd})
			}
		}
		s.Values = append(s.Values, sk)
	}
	s.Sockets = len(s.Values)
	if s.Truncated || s.Malformed > 0 || s.IdentityUnavailable > 0 {
		s.Status = "partial"
	}
	return s
}

func endpoint(s string) (string, int, bool) {
	host, port, ok := strings.Cut(s, "]:")
	if ok {
		host = strings.TrimPrefix(host, "[")
	} else {
		i := strings.LastIndexByte(s, ':')
		if i < 0 {
			return "", 0, false
		}
		host, port = s[:i], s[i+1:]
	}
	if host == "*" {
		host = ""
	} else {
		// ss also displays device scopes on IPv4 addresses. Peers remain
		// namespace-scoped evidence; this is not a route lookup.
		host, _, _ = strings.Cut(host, "%")
		addr, err := netip.ParseAddr(host)
		if err != nil {
			return "", 0, false
		}
		host = addr.Unmap().String()
	}
	if port == "*" {
		return host, 0, true
	}
	p, err := strconv.Atoi(port)
	return host, p, err == nil && p >= 0 && p <= 65535
}
