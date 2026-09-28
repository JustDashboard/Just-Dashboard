package proxysvc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// What nginx serves, asked of nginx. A certificate replaced on disk reaches
// browsers only once nginx reads its files again, and whether that happened
// is not something the files can say: a certbot deploy hook may have
// reloaded it, or nothing did. A handshake with each site that names the
// certificate is the evidence.

// ServedCertificate is what one enabled nginx site hands a client that asks
// for one of its names.
type ServedCertificate struct {
	Site string `json:"site"`
	// Name is the server name asked for, and Address where it was asked.
	Name    string `json:"name"`
	Address string `json:"address,omitempty"`
	Issuer  string `json:"issuer,omitempty"`
	Serial  string `json:"serial,omitempty"`
	// Staging is a served certificate a staging authority signed.
	Staging bool `json:"staging,omitempty"`
	// Current is the served leaf being the file's own: nginx has read the
	// certificate the file holds now.
	Current bool `json:"current"`
	// Error is why the site could not be asked.
	Error string `json:"error,omitempty"`
}

// ErrCertificateNotListed is a path the certificate inventory does not list:
// nothing is read or dialled for it.
var ErrCertificateNotListed = errors.New("no listed certificate has that path")

// servedRecheckInterval and servedRechecks bound the wait for a reload in
// flight. `nginx -s reload`, and a deploy hook's `systemctl reload nginx`,
// return once the signal is sent; the master reads its files and swaps its
// workers a moment later, and until then the old workers answer.
var servedRecheckInterval = time.Second

const servedRechecks = 3

// ServedCertificates asks each enabled nginx site that names the certificate
// at path, as the inventory lists it, which certificate it serves. With
// settle it asks again, for a few seconds, while a site serves anything but
// the file: the answer to read right after a reload.
func (s *Service) ServedCertificates(ctx context.Context, path string, settle bool) ([]ServedCertificate, error) {
	served, err := s.servedCertificates(ctx, path)
	for attempt := 0; settle && err == nil && servingAnother(served) && attempt < servedRechecks; attempt++ {
		select {
		case <-ctx.Done():
			return served, nil
		case <-time.After(servedRecheckInterval):
		}
		served, err = s.servedCertificates(ctx, path)
	}
	return served, err
}

// ServedLineage is ServedCertificates for the certbot lineage these names
// make up, as certbot matches one to a request.
func (s *Service) ServedLineage(ctx context.Context, domains []string, settle bool) ([]ServedCertificate, error) {
	lineage, _, ok := lineageFor(letsencryptDir, domains)
	if !ok {
		return nil, fmt.Errorf("certbot has no lineage for exactly %s", strings.Join(domains, ", "))
	}
	return s.ServedCertificates(ctx, filepath.Join(letsencryptDir, "live", lineage.Name, "fullchain.pem"), settle)
}

func servingAnother(served []ServedCertificate) bool {
	return slices.ContainsFunc(served, func(site ServedCertificate) bool {
		return site.Error == "" && !site.Current
	})
}

func (s *Service) servedCertificates(ctx context.Context, path string) ([]ServedCertificate, error) {
	vhosts := s.nginxVHosts()
	certs := listCertificates(filepath.Join(letsencryptDir, "live"), importedDir, vhosts, s.tlsStreams())
	index := slices.IndexFunc(certs, func(c Certificate) bool { return c.Path == path })
	if index < 0 {
		return nil, ErrCertificateNotListed
	}
	cert := certs[index]
	if cert.Error != "" {
		return nil, fmt.Errorf("%s could not be read: %s", path, cert.Error)
	}
	sites := []VHost{}
	for _, v := range vhosts {
		// A disabled site serves nothing, whatever file it names.
		if v.Enabled && slices.Contains(cert.UsedBy, v.Name) {
			sites = append(sites, v)
		}
	}
	served := make([]ServedCertificate, len(sites))
	var wg sync.WaitGroup
	for i, site := range sites {
		wg.Add(1)
		go func() {
			defer wg.Done()
			served[i] = certServedBy(ctx, site, cert)
		}()
	}
	wg.Wait()
	return served, nil
}

// certServedBy asks one site, at its first TLS listener, for a name the
// certificate covers.
func certServedBy(ctx context.Context, site VHost, cert Certificate) ServedCertificate {
	out := ServedCertificate{Site: site.Name, Name: serverNameFor(site.ServerNames, cert.Domains)}
	if out.Name == "" {
		out.Error = "the certificate names no host to ask for"
		return out
	}
	network, address, proxyProtocol, err := tlsListen(site.Listen)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Address = address
	leaf, err := handshakeLeaf(ctx, network, address, out.Name, proxyProtocol)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	summary := summarise(leaf, out.Name, "")
	out.Issuer, out.Serial, out.Staging = summary.Issuer, summary.Serial, summary.Staging
	out.Current = summary.Fingerprint == cert.Fingerprint
	return out
}

// serverNameFor is the name to ask a site for: the first of its server names
// the certificate covers, since a name it does not cover may belong to
// another server block; otherwise the certificate's own first name.
func serverNameFor(serverNames, domains []string) string {
	for _, name := range serverNames {
		// ".example.com" is nginx's shorthand for the name and its subdomains.
		name = strings.ToLower(strings.TrimPrefix(name, "."))
		if plainHostname(name) && covers(domains, name) {
			return name
		}
	}
	for _, domain := range domains {
		if plainHostname(domain) {
			return strings.ToLower(domain)
		}
	}
	// Only wildcards: any name one label under the first stands for them all.
	for _, domain := range domains {
		if strings.HasPrefix(domain, "*.") {
			return "jd-check" + strings.ToLower(domain[1:])
		}
	}
	return ""
}

func plainHostname(name string) bool {
	return !strings.HasPrefix(name, "*.") && certDomainRe.MatchString(name)
}

// covers reports a name the certificate's names match, as a browser matches
// them: exactly, or one label under a wildcard.
func covers(domains []string, name string) bool {
	for _, domain := range domains {
		domain = strings.ToLower(domain)
		if domain == name {
			return true
		}
		if suffix, ok := strings.CutPrefix(domain, "*"); ok {
			if head, found := strings.CutSuffix(name, suffix); found && head != "" && !strings.Contains(head, ".") {
				return true
			}
		}
	}
	return false
}

// tlsListen is where to reach a site's first TLS listener from this host:
// a wildcard address is reached on loopback, a named one only when it is
// this host's own. The dashboard shares the host's network, so what answers
// there is the nginx the site runs in.
func tlsListen(listens []string) (network, address string, proxyProtocol bool, err error) {
	for _, listen := range listens {
		fields := strings.Fields(listen)
		if len(fields) == 0 || !slices.Contains(fields[1:], "ssl") {
			continue
		}
		proxyProtocol = slices.Contains(fields[1:], "proxy_protocol")
		spec := fields[0]
		if socket, ok := strings.CutPrefix(spec, "unix:"); ok {
			return "unix", socket, proxyProtocol, nil
		}
		host, port := "", ""
		if strings.Trim(spec, "0123456789") == "" {
			port = spec
		} else if h, p, splitErr := net.SplitHostPort(spec); splitErr == nil {
			host, port = h, p
		} else {
			// An address alone listens on port 80, as nginx reads it.
			host, port = strings.Trim(spec, "[]"), "80"
		}
		switch host {
		case "", "*", "0.0.0.0", "localhost":
			host = "127.0.0.1"
		case "::":
			host = "::1"
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return "", "", false, fmt.Errorf("the site listens on %s, a name rather than an address", spec)
		}
		if !ip.IsLoopback() && !hostAddress(ip) {
			return "", "", false, fmt.Errorf("the site listens on %s, which is not an address of this host", host)
		}
		return "tcp", net.JoinHostPort(host, port), proxyProtocol, nil
	}
	return "", "", false, errors.New("the site has no listen directive with ssl")
}

func hostAddress(ip net.IP) bool {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, address := range addresses {
		if network, ok := address.(*net.IPNet); ok && network.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// handshakeLeaf is the leaf a TLS listener presents for name. The
// certificate is inspected, not trusted: a test one is exactly what this
// looks for.
func handshakeLeaf(ctx context.Context, network, address, name string, proxyProtocol bool) (*x509.Certificate, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if proxyProtocol {
		// Such a listener reads a PROXY header before anything else; this
		// one says the connection's addresses are unknown, which nginx takes.
		if _, err := io.WriteString(conn, "PROXY UNKNOWN\r\n"); err != nil {
			return nil, err
		}
	}
	client := tls.Client(conn, &tls.Config{ServerName: name, InsecureSkipVerify: true, MinVersion: tls.VersionTLS10})
	if err := client.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	peers := client.ConnectionState().PeerCertificates
	if len(peers) == 0 {
		return nil, errors.New("no certificate was presented")
	}
	return peers[0], nil
}

// ReplacementServed says what nginx serves once certbot has replaced a test
// certificate on disk, from what each site that names it answered.
func ReplacementServed(served []ServedCertificate) string {
	parts := []string{"The real certificate replaced the test one on disk."}
	if len(served) == 0 {
		return parts[0] + " No enabled nginx site names it."
	}
	var current, test, other, failed []string
	for _, site := range served {
		switch {
		case site.Error != "":
			failed = append(failed, fmt.Sprintf("What %s serves could not be checked: %s.", site.Site, site.Error))
		case site.Current:
			current = append(current, site.Site)
		case site.Staging:
			test = append(test, site.Site)
		default:
			other = append(other, fmt.Sprintf("%s answers %s with another certificate, from %s.", site.Site, site.Name, site.Issuer))
		}
	}
	if len(current) > 0 {
		parts = append(parts, fmt.Sprintf("nginx already serves it for %s.", strings.Join(current, ", ")))
	}
	if len(test) > 0 {
		verb := "still serve"
		if len(test) == 1 {
			verb = "still serves"
		}
		parts = append(parts, fmt.Sprintf("%s %s the test one until nginx reloads.", strings.Join(test, ", "), verb))
	}
	parts = append(parts, other...)
	parts = append(parts, failed...)
	return strings.Join(parts, " ")
}
