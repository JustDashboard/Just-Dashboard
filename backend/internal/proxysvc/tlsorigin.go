package proxysvc

import (
	"fmt"
	"net"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Origin traces a scanned name back to this host's nginx: the site whose
// server block takes the name, the ssl_certificate file it names, and whether
// the certificate that answered is that file. A certificate renewed on disk
// is not served until nginx is reloaded, and the scan is the only place that
// difference shows.
type Origin struct {
	// State is "current" (the answer is the site's file), "stale" (the file
	// is a later renewal of what answered, so a reload would serve it),
	// "other" (another certificate on this host answered), "foreign" (no
	// certificate on this host answered: another machine or a CDN did),
	// "unserved" (no site names the domain) or "unknown" (the site's file
	// cannot be read or is not in its own file).
	State string `json:"state"`
	// Site is the nginx site that takes the name; for "unserved" it is the
	// one that answers names nobody claims on the port, when there is one.
	Site string `json:"site,omitempty"`
	// Match is how Site was chosen: "exact", "wildcard", "regex" or
	// "default". "default" with DefaultFlag false means the first site
	// loaded for the port, which is nginx's rule when none is marked.
	Match       string `json:"match,omitempty"`
	DefaultFlag bool   `json:"defaultFlag,omitempty"`
	// File is the certificate the site's ssl_certificate names, as the
	// certificate list has it, so a link to it opens the same entry.
	File *Certificate `json:"file,omitempty"`
	// Served is the certificate on this host that answered, when it is not
	// File.
	Served *Certificate `json:"served,omitempty"`
	Detail string       `json:"detail"`
}

// TraceOrigin picks the server block nginx would give domain on port the way
// nginx does — exact name, then the longest leading wildcard, then the
// longest trailing one, then the first regex, then the port's default — and
// compares the file it names with live, the leaf the handshake got.
//
// It answers only for nginx: a name another engine claims, a host with no
// nginx sites, or an address, which server_name never matches, gives nil
// rather than a verdict about files that are not the ones serving.
func TraceOrigin(vhosts []VHost, certs []Certificate, domain string, port int, live *Certificate) *Origin {
	if live == nil || net.ParseIP(domain) != nil {
		return nil
	}
	if port == 0 {
		port = 443
	}
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	candidates := []VHost{}
	for _, v := range vhosts {
		if v.Kind != KindNginx {
			for _, name := range v.ServerNames {
				if strings.EqualFold(strings.TrimSuffix(name, "."), domain) {
					return nil
				}
			}
			continue
		}
		if v.Enabled && listensOn(v.Listen, port) {
			candidates = append(candidates, v)
		}
	}
	if len(candidates) == 0 {
		hasNginx := slices.ContainsFunc(vhosts, func(v VHost) bool { return v.Kind == KindNginx })
		if !hasNginx {
			return nil
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Name < candidates[j].Name })

	servedBy := func(fingerprint string) *Certificate {
		for i := range certs {
			if certs[i].Fingerprint != "" && certs[i].Fingerprint == fingerprint {
				return &certs[i]
			}
		}
		return nil
	}
	served := servedBy(live.Fingerprint)

	site, match := serverFor(candidates, domain)
	if site == nil {
		o := &Origin{State: "unserved", Served: served}
		if d, flagged := defaultFor(candidates, port); d != nil {
			o.Site, o.Match, o.DefaultFlag = d.Name, "default", flagged
		}
		switch {
		case o.Site != "" && served != nil:
			o.Detail = fmt.Sprintf("No site names %s, so %s answered it as the port's default server, with its own certificate.", domain, o.Site)
		case served != nil:
			o.Detail = fmt.Sprintf("No site names %s; a certificate on this host answered it.", domain)
		default:
			o.Detail = fmt.Sprintf("No site on this host names %s, and the certificate that answered is not on this host either.", domain)
		}
		return o
	}

	o := &Origin{Site: site.Name, Match: match}
	if match == "default" {
		_, o.DefaultFlag = defaultFor(candidates, port)
	}
	for i := range certs {
		if slices.Contains(certs[i].UsedBy, site.Name) {
			o.File = &certs[i]
			break
		}
	}
	switch {
	case o.File == nil:
		o.State = "unknown"
		o.Detail = fmt.Sprintf("%s names no ssl_certificate in its own file, so the file behind the answer cannot be traced; it may come from an include.", site.Name)
	case o.File.Error != "":
		o.State = "unknown"
		o.Detail = fmt.Sprintf("%s names %s, which could not be read: %s.", site.Name, o.File.Path, o.File.Error)
	case o.File.Fingerprint == live.Fingerprint:
		o.State = "current"
		o.Detail = fmt.Sprintf("nginx is serving the certificate in %s.", o.File.Path)
	case o.File.NotBefore.After(live.NotBefore) && sameNames(o.File.Domains, live.Domains):
		// A later certificate for the same names is a renewal, and the
		// older one still answering means nginx has not read the new file.
		o.State = "stale"
		o.Detail = fmt.Sprintf("%s was renewed on disk, but nginx still serves the certificate it loaded before; a reload serves the new one.", o.File.Path)
	case served != nil:
		o.State, o.Served = "other", served
		o.Detail = fmt.Sprintf("%s names %s, but nginx answered with %s: another server block took the name, or the site's file was changed without a reload.", site.Name, o.File.Path, served.Path)
	default:
		o.State = "foreign"
		o.Detail = "The certificate that answered is not on this host: the name resolves to another machine, or a CDN in front of this one answered."
	}
	return o
}

// serverFor is nginx's server_name precedence among the port's sites.
func serverFor(sites []VHost, domain string) (*VHost, string) {
	var leading, trailing *VHost
	leadingLen, trailingLen := 0, 0
	for i := range sites {
		for _, raw := range sites[i].ServerNames {
			name := strings.ToLower(strings.TrimSuffix(raw, "."))
			switch {
			case name == domain:
				return &sites[i], "exact"
			case strings.HasPrefix(name, "*."):
				if strings.HasSuffix(domain, name[1:]) && len(name) > leadingLen {
					leading, leadingLen = &sites[i], len(name)
				}
			case strings.HasPrefix(name, "."):
				// ".example.com" is example.com and *.example.com at once.
				if (domain == name[1:] || strings.HasSuffix(domain, name)) && len(name) > leadingLen {
					leading, leadingLen = &sites[i], len(name)
				}
			case strings.HasSuffix(name, ".*"):
				if strings.HasPrefix(domain, name[:len(name)-1]) && len(name) > trailingLen {
					trailing, trailingLen = &sites[i], len(name)
				}
			}
		}
	}
	if leading != nil {
		return leading, "wildcard"
	}
	if trailing != nil {
		return trailing, "wildcard"
	}
	for i := range sites {
		for _, raw := range sites[i].ServerNames {
			if !strings.HasPrefix(raw, "~") {
				continue
			}
			// PCRE and RE2 agree on the patterns server_name is written
			// with; one Go cannot compile is left out rather than guessed.
			if re, err := regexp.Compile(raw[1:]); err == nil && re.MatchString(domain) {
				return &sites[i], "regex"
			}
		}
	}
	return nil, ""
}

// defaultFor is the site that answers a name no server_name claims: the one
// marked default_server on the port, else the first loaded. Sites load in
// name order from their directory, which is the order sorted by the caller.
func defaultFor(sites []VHost, port int) (*VHost, bool) {
	for i := range sites {
		for _, l := range sites[i].Listen {
			fields := strings.Fields(l)
			if len(fields) > 0 && listenPortNumber(fields[0]) == port &&
				(slices.Contains(fields[1:], "default_server") || slices.Contains(fields[1:], "default")) {
				return &sites[i], true
			}
		}
	}
	if len(sites) > 0 {
		return &sites[0], false
	}
	return nil, false
}

// listensOn reports a site with a listen on port. A file with no listen at
// all is nginx's own default, port 80.
func listensOn(listen []string, port int) bool {
	if len(listen) == 0 {
		return port == 80
	}
	for _, l := range listen {
		if fields := strings.Fields(l); len(fields) > 0 && listenPortNumber(fields[0]) == port {
			return true
		}
	}
	return false
}

// listenPortNumber reads the port of a listen address: "443", "*:443",
// "[::]:443", "10.0.0.1:443", or a bare address, which is port 80. A unix
// socket is no port.
func listenPortNumber(addr string) int {
	if strings.HasPrefix(addr, "unix:") {
		return 0
	}
	if n, err := strconv.Atoi(addr); err == nil {
		return n
	}
	if strings.HasPrefix(addr, "[") {
		_, after, ok := strings.Cut(addr, "]:")
		if !ok {
			return 80
		}
		addr = after
	} else if i := strings.LastIndex(addr, ":"); i >= 0 {
		addr = addr[i+1:]
	}
	if n, err := strconv.Atoi(addr); err == nil {
		return n
	}
	return 80
}

// sameNames reports two certificates for the same set of names, which is
// what a renewal keeps.
func sameNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	for i := range x {
		if !strings.EqualFold(x[i], y[i]) {
			return false
		}
	}
	return true
}
