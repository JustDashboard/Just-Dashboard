package proxysvc

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// What the certificates and the configuration say together: a key other
// accounts can read, one key behind unrelated certificates, a weak key or a
// SHA-1 signature, a lineage whose names no longer point here, a server block
// whose certificate does not cover its names, and a certificate paired with
// a key that is not its own. The server blocks come from `nginx -T`, includes
// followed, so a certificate named in a snippet or at http level counts;
// directive arguments arrive unquoted from the tokenizer.

// CertificateFinding is one hygiene finding, worded for the page.
type CertificateFinding struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Level  string `json:"level"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Advice string `json:"advice"`
	// Certificate is a listed file the finding is about, which opens its
	// sheet.
	Certificate string `json:"certificate,omitempty"`
	// Lineage is a certbot lineage the finding suggests deleting.
	Lineage string `json:"lineage,omitempty"`
	// Names are server names no certificate the block serves covers.
	Names []string `json:"names,omitempty"`
}

// CertificateHygiene is every finding, and where the server blocks were read.
type CertificateHygiene struct {
	Findings []CertificateFinding `json:"findings"`
	// Config is "nginx -T", or "site files" when nginx could not print its
	// configuration; ConfigNote then says what that leaves out.
	Config     string `json:"config"`
	ConfigNote string `json:"configNote,omitempty"`
}

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

type hygieneScan struct {
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

func (s *Service) scanHygiene(ctx context.Context) *hygieneScan {
	scan := &hygieneScan{vhosts: s.nginxVHosts(), listed: map[string]Certificate{}, leaves: map[string]*x509.Certificate{}}
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

func (scan *hygieneScan) leaf(path string) *x509.Certificate {
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
func (scan *hygieneScan) listedPath(path string) string {
	if c, ok := scan.listed[resolvedPath(path)]; ok {
		return c.Path
	}
	return ""
}

func (scan *hygieneScan) certName(path string) string {
	if c, ok := scan.listed[resolvedPath(path)]; ok {
		return c.Name
	}
	return certificateName(path)
}

// usedBy is the sites whose server blocks name each certificate, by
// resolved path.
func (scan *hygieneScan) usedBy() map[string][]string {
	out := map[string][]string{}
	for _, server := range scan.servers {
		for _, pair := range server.pairs {
			resolved := resolvedPath(pair.cert)
			if !slices.Contains(out[resolved], server.site) {
				out[resolved] = append(out[resolved], server.site)
			}
		}
	}
	return out
}

// pairs is every certificate and key nginx pairs, and for a listed
// certificate no block names, the key certbot or its import keeps with it.
func (scan *hygieneScan) pairs() []certKeyPair {
	seen := map[certKeyPair]bool{}
	out := []certKeyPair{}
	add := func(pair certKeyPair) {
		if pair.cert == "" || pair.key == "" {
			return
		}
		id := certKeyPair{resolvedPath(pair.cert), resolvedPath(pair.key)}
		if !seen[id] {
			seen[id] = true
			out = append(out, pair)
		}
	}
	named := map[string]bool{}
	for _, server := range scan.servers {
		for _, pair := range server.pairs {
			add(pair)
			named[resolvedPath(pair.cert)] = true
		}
	}
	for _, c := range scan.certs {
		if c.Error != "" || named[resolvedPath(c.Path)] {
			continue
		}
		if key, _ := certificateKeyPath(c, nil); key != "" {
			add(certKeyPair{cert: c.Path, key: key})
		}
	}
	return out
}

// CertificateHygiene runs every check. Resolving certbot's names is the only
// thing it asks the network, and only of this host's resolver.
func (s *Service) CertificateHygiene(ctx context.Context) *CertificateHygiene {
	scan := s.scanHygiene(ctx)
	used := scan.usedBy()
	pairs := scan.pairs()
	findings := []CertificateFinding{}
	findings = append(findings, scan.keyExposure(pairs)...)
	findings = append(findings, scan.keyMismatch(pairs, used)...)
	findings = append(findings, scan.keyReuse()...)
	findings = append(findings, scan.weakCertificates(used)...)
	findings = append(findings, scan.uncoveredNames()...)
	findings = append(findings, scan.staleLineages(ctx, used)...)
	rank := map[string]int{"critical": 0, "warning": 1, "notice": 2}
	sort.SliceStable(findings, func(i, j int) bool { return rank[findings[i].Level] < rank[findings[j].Level] })
	return &CertificateHygiene{Findings: findings, Config: scan.config, ConfigNote: scan.configNote}
}

// keyExposure reports a key whose mode lets another account read it, where
// the directories above it let that account reach it too: certbot's
// archive directory is 0700, so a 0644 key inside it is exposed to nobody.
func (scan *hygieneScan) keyExposure(pairs []certKeyPair) []CertificateFinding {
	certsOf := map[string][]string{}
	keys := []string{}
	for _, pair := range pairs {
		resolved := resolvedPath(pair.key)
		if _, ok := certsOf[resolved]; !ok {
			keys = append(keys, pair.key)
		}
		certsOf[resolved] = append(certsOf[resolved], pair.cert)
	}
	out := []CertificateFinding{}
	for _, key := range keys {
		resolved := resolvedPath(key)
		info, err := os.Stat(resolved)
		if err != nil {
			continue
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			continue
		}
		perm := info.Mode().Perm()
		world := perm&0o004 != 0 && reachable(resolved, func(mode os.FileMode, _ uint32) bool { return mode&0o001 != 0 })
		group := perm&0o040 != 0 && reachable(resolved, func(mode os.FileMode, gid uint32) bool {
			return mode&0o001 != 0 || (mode&0o010 != 0 && gid == st.Gid)
		})
		if !world && !group {
			continue
		}
		_, groupName := fileOwner(info)
		cert := certsOf[resolved][0]
		finding := CertificateFinding{
			ID: "cert.key-readable." + key, Kind: "key-readable",
			Certificate: scan.listedPath(cert),
			Detail:      fmt.Sprintf("%s is mode %04o and belongs to %s's certificate.", key, perm, scan.certName(cert)),
		}
		if world {
			finding.Level = "critical"
			finding.Title = fmt.Sprintf("Every account on this host can read %s's key", scan.certName(cert))
			finding.Advice = "Anyone who can run a process here can copy the key and impersonate the site until the certificate expires. Run chmod 600 on it, then replace the certificate if an untrusted account may already have read it."
		} else {
			finding.Level = "warning"
			finding.Title = fmt.Sprintf("Group %s can read %s's key", groupName, scan.certName(cert))
			finding.Advice = fmt.Sprintf("Every member of %s can copy the key. If the group exists to share keys on purpose, such as Debian's ssl-cert, keep it that small; otherwise run chmod 600 on it. nginx reads keys as root, so it does not need group access.", groupName)
		}
		out = append(out, finding)
	}
	return out
}

// reachable reports every directory above path letting through the account
// open describes.
func reachable(path string, open func(mode os.FileMode, gid uint32) bool) bool {
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		info, err := os.Stat(dir)
		if err != nil {
			return false
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !open(info.Mode().Perm(), st.Gid) {
			return false
		}
		if dir == filepath.Dir(dir) {
			return true
		}
	}
}

// keyMismatch reports a certificate paired with a key that is not its own.
// A key that cannot be read or parsed is left alone: nothing is known.
func (scan *hygieneScan) keyMismatch(pairs []certKeyPair, used map[string][]string) []CertificateFinding {
	out := []CertificateFinding{}
	for _, pair := range pairs {
		leaf := scan.leaf(pair.cert)
		if leaf == nil {
			continue
		}
		public, err := readKeyPublic(pair.key)
		if err != nil {
			continue
		}
		eq, ok := public.(interface{ Equal(crypto.PublicKey) bool })
		if !ok || eq.Equal(leaf.PublicKey) {
			continue
		}
		finding := CertificateFinding{
			ID: "cert.key-mismatch." + pair.cert + "|" + pair.key, Kind: "key-mismatch",
			Title:       fmt.Sprintf("%s is paired with a key that is not its own", scan.certName(pair.cert)),
			Certificate: scan.listedPath(pair.cert),
		}
		if sites := used[resolvedPath(pair.cert)]; len(sites) > 0 {
			finding.Level = "critical"
			finding.Detail = fmt.Sprintf("%s names %s with %s, whose public half is another certificate's.", strings.Join(sites, ", "), pair.cert, pair.key)
			finding.Advice = "nginx refuses the pair with \"key values mismatch\", so its configuration test fails and no reload goes through. Point ssl_certificate_key at this certificate's own key, or replace the certificate."
		} else {
			finding.Level = "warning"
			finding.Detail = fmt.Sprintf("%s is kept beside %s, whose public half is another certificate's.", pair.key, pair.cert)
			finding.Advice = "A site pointed at this certificate fails nginx's configuration test. Import the pair again, or delete it if nothing needs it."
		}
		out = append(out, finding)
	}
	return out
}

func readKeyPublic(path string) (crypto.PublicKey, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxKeyFileBytes))
	if err != nil {
		return nil, err
	}
	return privateKeyPublic(raw)
}

// keyReuse reports one key behind certificates that share no name. A
// renewal that keeps its key, or a certificate that grew a name, shares
// names with the old one and is not reported.
func (scan *hygieneScan) keyReuse() []CertificateFinding {
	groups := map[[32]byte][]Certificate{}
	order := [][32]byte{}
	seen := map[string]bool{}
	for _, c := range scan.certs {
		resolved := resolvedPath(c.Path)
		if c.Error != "" || c.Expired || seen[resolved] {
			continue
		}
		seen[resolved] = true
		leaf := scan.leaf(c.Path)
		if leaf == nil {
			continue
		}
		spki := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
		if _, ok := groups[spki]; !ok {
			order = append(order, spki)
		}
		groups[spki] = append(groups[spki], c)
	}
	out := []CertificateFinding{}
	for _, spki := range order {
		group := groups[spki]
		if !anyUnrelated(group) {
			continue
		}
		names := make([]string, len(group))
		for i, c := range group {
			names[i] = fmt.Sprintf("%s (%s)", c.Name, strings.Join(c.Domains, ", "))
		}
		out = append(out, CertificateFinding{
			ID: fmt.Sprintf("cert.key-reused.%x", spki[:8]), Kind: "key-reused", Level: "warning",
			Title:       fmt.Sprintf("%d certificates for unrelated names share one key", len(group)),
			Detail:      "The same key signs for " + strings.Join(names, "; ") + ".",
			Advice:      "Whoever gets the key from one of them can impersonate every one, and revoking one for a compromised key leaves the others open. Issue each with its own key: certbot does unless --reuse-key is set.",
			Certificate: group[0].Path,
		})
	}
	return out
}

func anyUnrelated(group []Certificate) bool {
	for i := range group {
		for j := i + 1; j < len(group); j++ {
			if !slices.ContainsFunc(group[i].Domains, func(d string) bool {
				return slices.ContainsFunc(group[j].Domains, func(e string) bool { return strings.EqualFold(d, e) })
			}) {
				return true
			}
		}
	}
	return false
}

// weakCertificates reports an RSA key under 2048 bits and a SHA-1 signature
// on the leaf or an intermediate the file carries; a root signs itself, and
// no client checks that signature.
func (scan *hygieneScan) weakCertificates(used map[string][]string) []CertificateFinding {
	out := []CertificateFinding{}
	for _, c := range scan.certs {
		if c.Error != "" || c.Expired {
			continue
		}
		raw, err := os.ReadFile(c.Path)
		if err != nil {
			continue
		}
		chain, err := decodeChain(raw)
		if err != nil || len(chain) == 0 {
			continue
		}
		reasons := []string{}
		if key, ok := chain[0].PublicKey.(*rsa.PublicKey); ok && key.N.BitLen() < 2048 {
			reasons = append(reasons, fmt.Sprintf("its key is RSA %d-bit", key.N.BitLen()))
		}
		for i, cert := range chain {
			if cert.Issuer.String() == cert.Subject.String() {
				continue
			}
			switch cert.SignatureAlgorithm {
			case x509.SHA1WithRSA, x509.ECDSAWithSHA1, x509.DSAWithSHA1:
				which := "the certificate"
				if i > 0 {
					which = "the intermediate " + cert.Subject.CommonName
				}
				reasons = append(reasons, which+" is signed with SHA-1")
			}
		}
		if len(reasons) == 0 {
			continue
		}
		finding := CertificateFinding{
			ID: "cert.weak." + c.Path, Kind: "weak", Level: "warning",
			Title:       fmt.Sprintf("%s is weaker than browsers accept", c.Name),
			Detail:      capitalise(strings.Join(reasons, ", and ")) + ".",
			Advice:      "Browsers refuse RSA keys under 2048 bits and SHA-1 signatures. Issue or import a replacement with an ECDSA or RSA 2048+ key from an authority that signs with SHA-256.",
			Certificate: c.Path,
		}
		if sites := used[resolvedPath(c.Path)]; len(sites) > 0 {
			finding.Level = "critical"
			finding.Detail += " Served by " + strings.Join(sites, ", ") + "."
		}
		out = append(out, finding)
	}
	return out
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

// uncoveredNames reports a TLS server block answering names its
// certificate does not cover: the browser refuses those names.
func (scan *hygieneScan) uncoveredNames() []CertificateFinding {
	out := []CertificateFinding{}
	for _, server := range scan.servers {
		if server.stream || !server.tls || server.variable || len(server.pairs) == 0 {
			continue
		}
		domains, first := scan.pairDomains(server.pairs)
		if domains == nil {
			continue
		}
		missing := []string{}
		for _, name := range serverNamesOf(server.names) {
			if !covers(domains, name) {
				missing = append(missing, name)
			}
		}
		if len(missing) == 0 {
			continue
		}
		out = append(out, CertificateFinding{
			ID: fmt.Sprintf("cert.uncovered.%s:%d", server.file, server.line), Kind: "uncovered", Level: "critical",
			Title:       fmt.Sprintf("%s serves %s without a certificate for %s", server.site, scan.certName(first), pluralWord(len(missing), "it", "them")),
			Detail:      fmt.Sprintf("The server block at %s:%d answers %s, which %s does not cover (it covers %s).", server.file, server.line, strings.Join(missing, ", "), scan.certName(first), strings.Join(domains, ", ")),
			Advice:      "Browsers refuse the site under those names. Issue a certificate that covers them and point the block at it, or take them out of server_name.",
			Certificate: scan.listedPath(first),
			Names:       missing,
		})
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
func (scan *hygieneScan) pairDomains(pairs []certKeyPair) ([]string, string) {
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

// nameState is how one name resolves against this host.
type nameState int

const (
	nameUnknown nameState = iota
	nameHere
	nameGone
	nameElsewhere
)

const lineageLookups = 8

// staleLineages reports a certbot lineage none of whose names resolves to
// this host any more. A name behind Cloudflare, or a host with no public
// address of its own to compare with, is not judged: either may be fine.
func (scan *hygieneScan) staleLineages(ctx context.Context, used map[string][]string) []CertificateFinding {
	type lookup struct {
		name  string
		state nameState
		addrs []string
	}
	lineages := []Certificate{}
	names := []string{}
	for _, c := range scan.certs {
		if c.Source != "certbot" || c.Error != "" {
			continue
		}
		plain := slices.DeleteFunc(slices.Clone(c.Domains), func(d string) bool { return strings.HasPrefix(d, "*.") })
		if len(plain) == 0 {
			continue
		}
		lineages = append(lineages, c)
		for _, name := range plain {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	host := hostAddresses()
	results := make([]lookup, len(names))
	sem := make(chan struct{}, lineageLookups)
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			state, addrs := resolveAgainst(ctx, name, host)
			results[i] = lookup{name, state, addrs}
		}()
	}
	wg.Wait()
	byName := map[string]lookup{}
	for _, r := range results {
		byName[r.name] = r
	}

	out := []CertificateFinding{}
	for _, c := range lineages {
		parts := []string{}
		stale := true
		for _, d := range c.Domains {
			if strings.HasPrefix(d, "*.") {
				continue
			}
			r := byName[d]
			switch r.state {
			case nameGone:
				parts = append(parts, d+" does not resolve")
			case nameElsewhere:
				parts = append(parts, fmt.Sprintf("%s resolves to %s", d, strings.Join(r.addrs, ", ")))
			default:
				stale = false
			}
		}
		if !stale {
			continue
		}
		detail := capitalise(strings.Join(parts, "; ")) + "."
		if len(host) > 0 {
			detail += fmt.Sprintf(" This host is %s.", strings.Join(host, ", "))
		}
		advice := "certbot keeps renewing it, and an HTTP challenge for names that point elsewhere fails. If no other machine takes its copy, delete it."
		if sites := used[resolvedPath(c.Path)]; len(sites) > 0 {
			advice += fmt.Sprintf(" %s still %s it: take it out of %s first.", strings.Join(sites, ", "), pluralWord(len(sites), "names", "name"), pluralWord(len(sites), "that site", "those sites"))
		}
		out = append(out, CertificateFinding{
			ID: "cert.stale." + c.Name, Kind: "stale", Level: "notice",
			Title:       fmt.Sprintf("%s's names no longer point here", c.Name),
			Detail:      detail,
			Advice:      advice,
			Certificate: c.Path,
			Lineage:     c.Name,
		})
	}
	return out
}

func resolveAgainst(ctx context.Context, name string, host []string) (nameState, []string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, name)
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return nameGone, nil
		}
		return nameUnknown, nil
	}
	addrs := make([]string, 0, len(ips))
	for _, ip := range ips {
		addrs = append(addrs, ip.IP.String())
	}
	sort.Strings(addrs)
	pointsHere, behindProxy := compareAddresses(addrs, host)
	switch {
	case pointsHere:
		return nameHere, addrs
	case behindProxy || len(host) == 0:
		return nameUnknown, addrs
	}
	return nameElsewhere, addrs
}

// CertificateCoverage maps each server name to the certificate its block
// serves, or else one listed that could, and lists what no block names.
func (s *Service) CertificateCoverage(ctx context.Context) *CertificateCoverage {
	scan := s.scanHygiene(ctx)
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
