package proxysvc

import (
	"context"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Certificate coverage joins the configured server names to their certificates.
// Includes are followed through nginx -T, and directive arguments arrive
// unquoted from the tokenizer.

// CoveredName is one server name and the certificate that covers it.
type CoveredName struct {
	Name string `json:"name"`
	Site string `json:"site"`
	File string `json:"file"`
	Line int    `json:"line"`
	// TLS is a server block for the name that listens with ssl or quic.
	TLS bool `json:"tls"`
	// State is "served" (the block's certificate covers it), "wrong" (it
	// does not), "unknown" (the block's certificate is a variable or was
	// not found), "available" (no TLS block, but a listed certificate
	// covers it) or "uncovered" (nothing here does).
	State           string `json:"state"`
	Certificate     string `json:"certificate,omitempty"`
	CertificateName string `json:"certificateName,omitempty"`
}

// UnusedCertificate is a listed certificate no server block names.
type UnusedCertificate struct {
	Path    string   `json:"path"`
	Name    string   `json:"name"`
	Source  string   `json:"source"`
	Domains []string `json:"domains"`
	Expired bool     `json:"expired"`
	// DisabledSites are disabled sites whose file names it: enabling one
	// puts it back in use.
	DisabledSites []string `json:"disabledSites,omitempty"`
}

// CertificateCoverage maps every server name to its certificate, and lists
// the certificates nothing names.
type CertificateCoverage struct {
	Names []CoveredName `json:"names"`
	// Unused is null when the configuration was read from the site files
	// alone: a snippet or a stream naming a certificate was not read, so
	// nothing can be called unused.
	Unused     []UnusedCertificate `json:"unused"`
	Config     string              `json:"config"`
	ConfigNote string              `json:"configNote,omitempty"`
}

// configServer is one server block of the loaded configuration.
type configServer struct {
	site string
	file string
	line int
	// stream is a server in the stream context, which has no names here.
	stream bool
	names  []string
	tls    bool
	pairs  []certKeyPair
	// variable is a certificate or key chosen per request, which cannot be
	// read ahead of one.
	variable bool
}

// certKeyPair is an ssl_certificate and the ssl_certificate_key nginx pairs
// with it, by position.
type certKeyPair struct{ cert, key string }

type coverageScan struct {
	servers []configServer
	vhosts  []VHost
	// certs is what the inventory lists for nginx, without Caddy's release
	// copies, which the evidence prune looks after.
	certs []Certificate
	// listed is certs by resolved path.
	listed     map[string]Certificate
	config     string
	configNote string
	leaves     map[string]*x509.Certificate
}

func (s *Service) scanCoverage(ctx context.Context) *coverageScan {
	scan := &coverageScan{vhosts: s.nginxVHosts(), listed: map[string]Certificate{}, leaves: map[string]*x509.Certificate{}}
	all := listCertificates(filepath.Join(letsencryptDir, "live"), importedDir, scan.vhosts, s.tlsStreams())
	scan.certs, _ = splitCaddyEvidence(all)
	for _, c := range scan.certs {
		scan.listed[resolvedPath(c.Path)] = c
	}
	tree, config, note := s.configTree(ctx, scan.vhosts)
	scan.config, scan.configNote = config, note
	scan.servers = s.configServers(tree)
	return scan
}

// configTree is the configuration nginx loads. When `nginx -T` fails — a
// configuration that does not pass its test, which a mismatched key is
// enough for — the enabled site files are read on their own instead.
func (s *Service) configTree(ctx context.Context, vhosts []VHost) ([]Directive, string, string) {
	files, err := s.EffectiveConfig(ctx)
	if err == nil {
		tree, treeErr := NginxTree(files)
		if treeErr == nil {
			return tree, "nginx -T", ""
		}
		err = treeErr
	}
	tree := []Directive{}
	for _, v := range vhosts {
		if !v.Enabled {
			continue
		}
		content, readErr := os.ReadFile(v.Path)
		if readErr != nil {
			continue
		}
		directives, parseErr := ParseNginxFile(v.Path, string(content), []string{"http"})
		if parseErr != nil {
			continue
		}
		tree = append(tree, Directive{Name: "http", File: v.Path, Block: directives})
	}
	return tree, "site files", fmt.Sprintf("nginx could not print its configuration (%v), so each enabled site file was read on its own: an include inside one and every stream were not read.", err)
}

// configServers is every server block under http and stream. A block
// without its own ssl_certificate inherits the one its context sets.
func (s *Service) configServers(tree []Directive) []configServer {
	out := []configServer{}
	for _, top := range tree {
		if (top.Name != "http" && top.Name != "stream") || top.Block == nil {
			continue
		}
		inheritedCerts, inheritedKeys := directiveValues(top.Block, "ssl_certificate"), directiveValues(top.Block, "ssl_certificate_key")
		for _, d := range top.Block {
			if d.Name != "server" || d.Block == nil {
				continue
			}
			server := configServer{site: s.siteOf(d.File), file: d.File, line: d.Line, stream: top.Name == "stream"}
			for _, c := range d.Block {
				switch c.Name {
				case "server_name":
					if !server.stream {
						server.names = append(server.names, c.Args...)
					}
				case "listen":
					if len(c.Args) > 1 && (slices.Contains(c.Args[1:], "ssl") || slices.Contains(c.Args[1:], "quic")) {
						server.tls = true
					}
				}
			}
			certs, keys := directiveValues(d.Block, "ssl_certificate"), directiveValues(d.Block, "ssl_certificate_key")
			if len(certs) == 0 {
				certs, keys = inheritedCerts, inheritedKeys
			}
			for i, cert := range certs {
				key := ""
				if i < len(keys) {
					key = keys[i]
				}
				if perRequest(cert) || perRequest(key) {
					server.variable = true
					continue
				}
				server.pairs = append(server.pairs, certKeyPair{cert: s.nginxConfPath(cert), key: s.nginxConfPath(key)})
			}
			out = append(out, server)
		}
	}
	return out
}

func directiveValues(block []Directive, name string) []string {
	out := []string{}
	for _, d := range block {
		if d.Name == name && len(d.Args) > 0 {
			out = append(out, d.Args[0])
		}
	}
	return out
}

// perRequest is a path nginx works out per handshake: a variable, a
// "data:" value, or a key held by an OpenSSL engine.
func perRequest(path string) bool {
	return strings.Contains(path, "$") || strings.HasPrefix(path, "data:") || strings.HasPrefix(path, "engine:")
}

// nginxConfPath is a configuration path as nginx opens it: a relative one is
// relative to the configuration's directory.
func (s *Service) nginxConfPath(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(s.nginxDir, path)
}

// siteOf names the site a server block's file is, as the site list names it.
func (s *Service) siteOf(file string) string {
	rel, err := filepath.Rel(s.nginxDir, filepath.Clean(file))
	if err != nil || strings.HasPrefix(rel, "..") {
		return file
	}
	switch filepath.Dir(rel) {
	case "sites-enabled", "sites-available", "conf.d":
		return filepath.Base(rel)
	}
	return rel
}

func resolvedPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

func (scan *coverageScan) leaf(path string) *x509.Certificate {
	resolved := resolvedPath(path)
	if leaf, ok := scan.leaves[resolved]; ok {
		return leaf
	}
	leaf, err := readLeaf(path)
	if err != nil {
		leaf = nil
	}
	scan.leaves[resolved] = leaf
	return leaf
}

// listedPath is the inventory's path for a file, which the page opens a
// sheet by; "" when the inventory does not list it.
func (scan *coverageScan) listedPath(path string) string {
	if c, ok := scan.listed[resolvedPath(path)]; ok {
		return c.Path
	}
	return ""
}

func (scan *coverageScan) certName(path string) string {
	if c, ok := scan.listed[resolvedPath(path)]; ok {
		return c.Name
	}
	return certificateName(path)
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// serverNamesOf is the names a block answers that a certificate can cover:
// no catch-all "_", no regular expression, no "www.example.*" suffix
// wildcard, no address and no single-label name. ".example.com" is nginx's
// shorthand for the name and every name under it.
func serverNamesOf(names []string) []string {
	out := []string{}
	add := func(name string) {
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	for _, name := range names {
		name = strings.ToLower(name)
		if name == "" || name == "_" || strings.HasPrefix(name, "~") || strings.HasSuffix(name, "*") ||
			strings.Contains(name, "$") || net.ParseIP(name) != nil || !strings.Contains(strings.Trim(name, "."), ".") {
			continue
		}
		if base, ok := strings.CutPrefix(name, "."); ok {
			add(base)
			add("*." + base)
			continue
		}
		add(name)
	}
	return out
}

func pluralWord(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// pairDomains is the names the block's certificates cover together, nil
// when none of them could be read.
func (scan *coverageScan) pairDomains(pairs []certKeyPair) ([]string, string) {
	var domains []string
	first := ""
	for _, pair := range pairs {
		leaf := scan.leaf(pair.cert)
		if leaf == nil {
			continue
		}
		if first == "" {
			first = pair.cert
		}
		names := leaf.DNSNames
		if len(names) == 0 && leaf.Subject.CommonName != "" {
			names = []string{leaf.Subject.CommonName}
		}
		domains = append(domains, names...)
	}
	return domains, first
}

// CertificateCoverage maps each server name to the certificate its block
// serves, or else one listed that could, and lists what no block names.
func (s *Service) CertificateCoverage(ctx context.Context) *CertificateCoverage {
	scan := s.scanCoverage(ctx)
	coverage := &CertificateCoverage{Names: []CoveredName{}, Config: scan.config, ConfigNote: scan.configNote}

	usable := []Certificate{}
	for _, c := range scan.certs {
		if c.Error == "" && !c.Expired && !c.Staging {
			usable = append(usable, c)
		}
	}
	tlsNames := map[string]bool{}
	plain := []CoveredName{}
	for _, server := range scan.servers {
		if server.stream {
			continue
		}
		domains, first := scan.pairDomains(server.pairs)
		site := server.site
		for _, name := range serverNamesOf(server.names) {
			row := CoveredName{Name: name, Site: site, File: server.file, Line: server.line, TLS: server.tls}
			if !server.tls {
				if i := slices.IndexFunc(usable, func(c Certificate) bool { return covers(c.Domains, name) }); i >= 0 {
					row.State, row.Certificate, row.CertificateName = "available", usable[i].Path, usable[i].Name
				} else {
					row.State = "uncovered"
				}
				plain = append(plain, row)
				continue
			}
			tlsNames[name] = true
			switch {
			case server.variable || domains == nil:
				row.State = "unknown"
			case covers(domains, name):
				row.State = "served"
			default:
				row.State = "wrong"
			}
			if first != "" {
				row.Certificate, row.CertificateName = scan.listedPath(first), scan.certName(first)
				if row.Certificate == "" {
					row.Certificate = first
				}
			}
			coverage.Names = append(coverage.Names, row)
		}
	}
	// A name that also has a TLS block is read there: its plain block is
	// the redirect to it.
	for _, row := range plain {
		if !tlsNames[row.Name] && !slices.ContainsFunc(coverage.Names, func(r CoveredName) bool { return !r.TLS && r.Name == row.Name }) {
			coverage.Names = append(coverage.Names, row)
		}
	}
	sort.SliceStable(coverage.Names, func(i, j int) bool { return coverage.Names[i].Name < coverage.Names[j].Name })

	if scan.config != "nginx -T" {
		return coverage
	}
	named := map[string]bool{}
	for _, server := range scan.servers {
		for _, pair := range server.pairs {
			named[resolvedPath(pair.cert)] = true
		}
	}
	caddyfile := ""
	if b, err := os.ReadFile(s.caddyFile); err == nil {
		caddyfile = string(b)
	}
	enabled := map[string]bool{}
	for _, v := range scan.vhosts {
		enabled[v.Name] = v.Enabled
	}
	coverage.Unused = []UnusedCertificate{}
	for _, c := range scan.certs {
		if named[resolvedPath(c.Path)] || caddyfileNames(caddyfile, c) {
			continue
		}
		unused := UnusedCertificate{Path: c.Path, Name: c.Name, Source: c.Source, Domains: c.Domains, Expired: c.Expired}
		for _, site := range c.UsedBy {
			if !enabled[site] {
				unused.DisabledSites = append(unused.DisabledSites, site)
			}
		}
		coverage.Unused = append(coverage.Unused, unused)
	}
	return coverage
}

// caddyfileNames reports a Caddyfile tls directive reading the certificate:
// by its path, or for a certbot lineage by any file of its live directory.
func caddyfileNames(caddyfile string, c Certificate) bool {
	if caddyfile == "" {
		return false
	}
	if strings.Contains(caddyfile, c.Path) {
		return true
	}
	return c.Source == "certbot" && strings.Contains(caddyfile, filepath.Dir(c.Path)+"/")
}
