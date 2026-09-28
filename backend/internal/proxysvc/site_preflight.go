package proxysvc

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// The checks nginx -t cannot make.
//
// nginx -t parses the file and opens the certificate and key, and that is all:
// a password file that is missing or empty, a root with nothing in it, an
// application that is not listening and a certificate for other names all
// pass it, and turn into a 500, a 403, a 502 or a browser warning once the
// site is live. Each is answered here before the save instead.
//
// Every input is checked against the same pattern ValidateSpec holds it to,
// field by field rather than the spec as a whole, so a form still being filled
// in gets the answers for the parts it has. A field that fails its pattern is
// skipped: the preview already says what is wrong with it.

// SitePreflightCheck is one finding. Level is ok, info, warning or fail; a
// Blocking one is a site that will not work as saved, and the form holds
// Save until the operator says to save anyway.
type SitePreflightCheck struct {
	ID       string `json:"id"`
	Level    string `json:"level"`
	Title    string `json:"title"`
	Detail   string `json:"detail,omitempty"`
	Blocking bool   `json:"blocking"`
	// Domain is set on the DNS checks, which the form shows by the domains
	// rather than in the list.
	Domain string `json:"domain,omitempty"`
}

// SitePreflightReport is every check for one spec, in a fixed order.
type SitePreflightReport struct {
	Checks []SitePreflightCheck `json:"checks"`
}

const (
	preflightProbeTimeout = 2 * time.Second
	preflightConcurrency  = 4
)

// hostFile is what stat says about a path on the host.
type hostFile struct {
	Kind string // stat's %F: "regular file", "directory", "socket"…
	Mode os.FileMode
	UID  int
	GID  int
}

// statOnHost asks the host, not this container, since nginx reads the host's
// filesystem and /var/www is not shared with the container. One stat call for
// every path; a path it leaves out is one that does not exist. The error is
// only for a stat that could not run at all.
var statOnHost = func(ctx context.Context, paths []string) (map[string]hostFile, error) {
	out := map[string]hostFile{}
	if len(paths) == 0 {
		return out, nil
	}
	// -L follows links, which is what nginx does: certbot's live/ files
	// are links into archive/.
	args := append([]string{"-L", "-c", "%n|%F|%a|%u|%g", "--"}, paths...)
	raw, err := hostexec.CommandOnHost(ctx, "stat", args...).Output()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return nil, err
	}
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	for sc.Scan() {
		// absPathRe keeps | out of every path asked about.
		fields := strings.Split(sc.Text(), "|")
		if len(fields) != 5 {
			continue
		}
		mode, _ := strconv.ParseUint(fields[2], 8, 32)
		uid, _ := strconv.Atoi(fields[3])
		gid, _ := strconv.Atoi(fields[4])
		out[fields[0]] = hostFile{Kind: fields[1], Mode: os.FileMode(mode), UID: uid, GID: gid}
	}
	return out, nil
}

// SitePreflight runs every check that applies to spec. The network ones —
// DNS for each domain and a connection to each of the spec's own upstreams —
// run four at a time.
func (s *Service) SitePreflight(ctx context.Context, spec *SiteSpec) *SitePreflightReport {
	paths := preflightPaths(spec)
	files, statErr := statOnHost(ctx, paths)

	checks := []SitePreflightCheck{}
	if spec.TLS && absPathRe.MatchString(spec.CertPath) && absPathRe.MatchString(spec.KeyPath) {
		checks = append(checks, certificateChecks(spec, files, statErr)...)
	}
	for _, file := range authFiles(spec) {
		checks = append(checks, s.authFileCheck(file, files, statErr))
	}
	checks = append(checks, rootChecks(spec, files, statErr)...)

	probes := []func() SitePreflightCheck{}
	for _, upstream := range specUpstreams(spec) {
		probes = append(probes, func() SitePreflightCheck { return upstreamCheck(ctx, upstream, files, statErr) })
	}
	for _, domain := range spec.Domains {
		if domainRe.MatchString(domain) {
			probes = append(probes, func() SitePreflightCheck { return dnsCheck(ctx, domain) })
		}
	}
	results := make([]SitePreflightCheck, len(probes))
	slots := make(chan struct{}, preflightConcurrency)
	var wg sync.WaitGroup
	for i, probe := range probes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			results[i] = probe()
		}()
	}
	wg.Wait()
	return &SitePreflightReport{Checks: append(checks, results...)}
}

// preflightPaths is every file and folder the checks ask the host about.
func preflightPaths(spec *SiteSpec) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(p string) {
		if absPathRe.MatchString(p) && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if spec.TLS {
		add(spec.CertPath)
		add(spec.KeyPath)
	}
	for _, file := range authFiles(spec) {
		add(file)
	}
	for _, root := range specRoots(spec) {
		add(root.dir)
		if root.index {
			add(filepath.Join(root.dir, "index.html"))
		}
	}
	for _, upstream := range specUpstreams(spec) {
		if socket, ok := strings.CutPrefix(upstream, "unix:"); ok {
			add(socket)
		}
	}
	return out
}

func notChecked(id, title string, err error) SitePreflightCheck {
	return SitePreflightCheck{
		ID: id, Level: "warning", Title: title,
		Detail: fmt.Sprintf("Not checked: the host could not be asked (%v).", err),
	}
}

func certificateChecks(spec *SiteSpec, files map[string]hostFile, statErr error) []SitePreflightCheck {
	if statErr != nil {
		return []SitePreflightCheck{notChecked("cert", "Certificate and key", statErr)}
	}
	checks := []SitePreflightCheck{}
	certOnHost, keyOnHost := files[spec.CertPath].Kind == "regular file", files[spec.KeyPath].Kind == "regular file"
	if !certOnHost {
		checks = append(checks, SitePreflightCheck{
			ID: "cert", Level: "fail", Blocking: true, Title: "Certificate file missing",
			Detail: spec.CertPath + " is not a file on this server. nginx refuses to start the site without it.",
		})
	}
	if !keyOnHost {
		checks = append(checks, SitePreflightCheck{
			ID: "key", Level: "fail", Blocking: true, Title: "Key file missing",
			Detail: spec.KeyPath + " is not a file on this server. nginx refuses to start the site without it.",
		})
	}
	if !certOnHost || !keyOnHost {
		return checks
	}
	// Both exist on the host; their content is read here, which works for
	// the paths the container shares with the host (/etc, /srv, /opt…).
	certPEM, err := os.ReadFile(spec.CertPath)
	if err != nil {
		return append(checks, SitePreflightCheck{
			ID: "cert", Level: "warning", Title: "Certificate not compared",
			Detail: "Both files exist, but the dashboard cannot read " + spec.CertPath + " to compare them with the domains and each other.",
		})
	}
	var leaf *x509.Certificate
	for rest := certPEM; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			leaf, _ = x509.ParseCertificate(block.Bytes)
			break
		}
	}
	if leaf == nil {
		return append(checks, SitePreflightCheck{
			ID: "cert", Level: "fail", Blocking: true, Title: "Not a certificate",
			Detail: spec.CertPath + " holds no PEM certificate nginx can load.",
		})
	}
	summary := summarise(leaf, certificateName(spec.CertPath), spec.CertPath)

	if keyPEM, err := os.ReadFile(spec.KeyPath); err != nil {
		checks = append(checks, SitePreflightCheck{
			ID: "key", Level: "warning", Title: "Key not compared",
			Detail: "The dashboard cannot read " + spec.KeyPath + " to check that it belongs to the certificate.",
		})
	} else if block, _ := pem.Decode(keyPEM); block == nil {
		checks = append(checks, SitePreflightCheck{
			ID: "key", Level: "fail", Blocking: true, Title: "Not a private key",
			Detail: spec.KeyPath + " holds no PEM private key.",
		})
	} else if err := keyMatchesCertificate(string(keyPEM), leaf); err != nil {
		checks = append(checks, SitePreflightCheck{
			ID: "key", Level: "fail", Blocking: true, Title: "Key does not match the certificate",
			Detail: capitalise(err.Error()) + ". nginx refuses to start the site with this pair.",
		})
	} else {
		checks = append(checks, SitePreflightCheck{ID: "key", Level: "ok", Title: "Key matches the certificate"})
	}

	uncovered := []string{}
	for _, domain := range spec.Domains {
		if !certificateCoversAll(summary.Domains, []string{domain}) {
			uncovered = append(uncovered, domain)
		}
	}
	if len(uncovered) > 0 {
		checks = append(checks, SitePreflightCheck{
			ID: "cert-domains", Level: "fail", Blocking: true, Title: "Certificate does not cover every domain",
			Detail: fmt.Sprintf("It is for %s; browsers warn visitors to %s.",
				strings.Join(summary.Domains, ", "), strings.Join(uncovered, ", ")),
		})
	} else {
		checks = append(checks, SitePreflightCheck{
			ID: "cert-domains", Level: "ok", Title: "Certificate covers every domain",
			Detail: strings.Join(summary.Domains, ", "),
		})
	}

	expiry := SitePreflightCheck{ID: "cert-expiry", Level: "ok", Title: fmt.Sprintf("Certificate valid for %d more days", summary.DaysLeft)}
	switch {
	case summary.Expired:
		expiry = SitePreflightCheck{
			ID: "cert-expiry", Level: "fail", Blocking: true, Title: "Certificate expired",
			Detail: "It expired on " + summary.NotAfter.Format("2 January 2006") + "; browsers refuse it.",
		}
	case summary.Expiring:
		expiry.Level = "warning"
		expiry.Detail = fmt.Sprintf("Inside the %d days in which certbot renews; browsers refuse it once it expires.", expiryWarningDays)
	}
	return append(checks, expiry)
}

// authFiles is every password file the site and its paths name.
func authFiles(spec *SiteSpec) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, file := range append([]string{spec.BasicAuthFile}, locationAuthFiles(spec)...) {
		if absPathRe.MatchString(file) && !seen[file] {
			seen[file] = true
			out = append(out, file)
		}
	}
	return out
}

func locationAuthFiles(spec *SiteSpec) []string {
	out := []string{}
	for _, loc := range spec.Locations {
		out = append(out, loc.BasicAuthFile)
	}
	return out
}

// authFileCheck: nginx reads a password file on each request, in a worker, so
// a file that is missing, empty or unreadable by the worker is not caught by
// nginx -t — every visitor gets a 500 or a password prompt nothing passes.
func (s *Service) authFileCheck(path string, files map[string]hostFile, statErr error) SitePreflightCheck {
	id := "auth:" + path
	if statErr != nil {
		return notChecked(id, "Password file", statErr)
	}
	file, ok := files[path]
	if !ok || file.Kind != "regular file" {
		return SitePreflightCheck{
			ID: id, Level: "fail", Blocking: true, Title: "Password file missing",
			Detail: path + " is not a file on this server; nginx answers every request with a 500.",
		}
	}
	users, err := readAuthUsers(path)
	if err != nil {
		return SitePreflightCheck{
			ID: id, Level: "warning", Title: "Password file not read",
			Detail: path + " exists, but the dashboard cannot read it to count who is in it.",
		}
	}
	if len(users) == 0 {
		return SitePreflightCheck{
			ID: id, Level: "fail", Blocking: true, Title: "Password file has no users",
			Detail: path + " is empty, so nobody gets past the password prompt.",
		}
	}
	gid, known := s.nginxWorkerGID()
	switch {
	case file.Mode&0o004 != 0, known && file.GID == gid && file.Mode&0o040 != 0:
		return SitePreflightCheck{
			ID: id, Level: "ok", Title: "Password file ready",
			Detail: fmt.Sprintf("%s: %d %s, readable by nginx's workers.", path, len(users), pluralWord(len(users), "user", "users")),
		}
	case known && file.UID == 0:
		return SitePreflightCheck{
			ID: id, Level: "fail", Blocking: true, Title: "nginx cannot read the password file",
			Detail: fmt.Sprintf("%s is mode %03o, owned by root and group %d; nginx's workers (group %d) cannot read it, so every request gets a 500.",
				path, file.Mode.Perm(), file.GID, gid),
		}
	default:
		// Owned by an account other than root, which may be the worker's
		// own: the group and mode alone cannot settle it.
		return SitePreflightCheck{
			ID: id, Level: "warning", Title: "Password file may not be readable",
			Detail: fmt.Sprintf("%s is mode %03o and not readable by everyone; nginx's workers can read it only if they run as its owner or its group.",
				path, file.Mode.Perm()),
		}
	}
}

type siteRoot struct {
	id, dir string
	// index is whether an index.html is expected in the folder: the site's
	// own root, and a folder serving a single-page app.
	index, spa bool
}

func specRoots(spec *SiteSpec) []siteRoot {
	out := []siteRoot{}
	if spec.Kind == "static" && absPathRe.MatchString(spec.Root) {
		out = append(out, siteRoot{id: "root", dir: spec.Root, index: true, spa: spec.SPA})
	}
	if spec.Kind == "php" && absPathRe.MatchString(spec.Root) {
		out = append(out, siteRoot{id: "root", dir: spec.Root})
	}
	if spec.Kind == "redirect" {
		return out
	}
	for _, loc := range spec.Locations {
		if loc.Upstream != "" || !absPathRe.MatchString(loc.Root) {
			continue
		}
		// With RootMode "root" the path is appended to the folder, so its
		// index.html is somewhere below it; only the folder is checked.
		index := loc.SPA && loc.RootMode != "root"
		out = append(out, siteRoot{id: "root:" + loc.Path, dir: loc.Root, index: index, spa: loc.SPA})
	}
	return out
}

func rootChecks(spec *SiteSpec, files map[string]hostFile, statErr error) []SitePreflightCheck {
	checks := []SitePreflightCheck{}
	for _, root := range specRoots(spec) {
		if statErr != nil {
			checks = append(checks, notChecked(root.id, "Folder "+root.dir, statErr))
			continue
		}
		if files[root.dir].Kind != "directory" {
			checks = append(checks, SitePreflightCheck{
				ID: root.id, Level: "fail", Blocking: true, Title: "Folder missing",
				Detail: root.dir + " is not a folder on this server; nginx answers 404 for everything under it.",
			})
			continue
		}
		if !root.index {
			checks = append(checks, SitePreflightCheck{ID: root.id, Level: "ok", Title: "Folder " + root.dir + " exists"})
			continue
		}
		index := filepath.Join(root.dir, "index.html")
		switch {
		case files[index].Kind == "regular file":
			checks = append(checks, SitePreflightCheck{ID: root.id, Level: "ok", Title: root.dir + " has an index.html"})
		case root.spa:
			checks = append(checks, SitePreflightCheck{
				ID: root.id, Level: "fail", Blocking: true, Title: "No index.html",
				Detail: root.dir + " has no index.html, which a single-page app answers every path with; each one gets an error.",
			})
		default:
			checks = append(checks, SitePreflightCheck{
				ID: root.id, Level: "warning", Title: "No index.html",
				Detail: root.dir + " has no index.html, so its front page is a 403 or 404 unless the files have another index.",
			})
		}
	}
	return checks
}

// specUpstreams is every address the spec itself forwards to — never anything
// else, since this is a probe the caller chooses the target of.
func specUpstreams(spec *SiteSpec) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(u string) {
		if validUpstream(u) == nil && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	if spec.Kind == "proxy" {
		if spec.Pool == nil {
			add(spec.Upstream)
		} else {
			scheme := "http"
			if spec.Pool.Scheme == "https" {
				scheme = "https"
			}
			for _, server := range spec.Pool.Servers {
				if server.Down || validPoolAddress(server.Address) != nil {
					continue
				}
				if strings.HasPrefix(server.Address, "unix:") {
					add(server.Address)
				} else {
					add(scheme + "://" + server.Address + "/")
				}
			}
		}
	}
	// The socket is checked like an application's: it has to exist.
	if spec.Kind == "php" && absPathRe.MatchString(spec.PHPSocket) {
		add("unix:" + spec.PHPSocket)
	}
	if spec.Kind != "redirect" {
		for _, loc := range spec.Locations {
			if loc.Upstream != "" {
				add(loc.Upstream)
			}
		}
	}
	return out
}

// upstreamCheck opens a connection and sends a HEAD, each within two seconds.
// Nothing listening is a warning, not a hold on the save: a site is often
// saved before its application is started, and nginx answers 502 until then.
func upstreamCheck(ctx context.Context, upstream string, files map[string]hostFile, statErr error) SitePreflightCheck {
	id := "upstream:" + upstream
	if socket, ok := strings.CutPrefix(upstream, "unix:"); ok {
		if statErr != nil {
			return notChecked(id, upstream, statErr)
		}
		if files[socket].Kind != "socket" {
			return SitePreflightCheck{
				ID: id, Level: "warning", Title: "Nothing listens at " + upstream,
				Detail: socket + " is not a socket on this server; nginx answers 502 until the application creates it.",
			}
		}
		return SitePreflightCheck{ID: id, Level: "ok", Title: upstream + " exists", Detail: "A socket; no request is sent over it."}
	}
	target, err := url.Parse(upstream)
	if err != nil {
		return SitePreflightCheck{ID: id, Level: "warning", Title: upstream, Detail: "Not an address that can be tried."}
	}
	port := target.Port()
	if port == "" {
		port = "80"
		if target.Scheme == "https" {
			port = "443"
		}
	}
	address := net.JoinHostPort(target.Hostname(), port)
	started := time.Now()
	dialer := &net.Dialer{Timeout: preflightProbeTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return SitePreflightCheck{
			ID: id, Level: "warning", Title: "Nothing accepted a connection at " + address,
			Detail: fmt.Sprintf("%v. nginx answers 502 until the application is listening there.", err),
		}
	}
	connected := time.Since(started)
	conn.Close()

	client := &http.Client{
		Timeout: preflightProbeTimeout,
		// No Proxy: the upstream is asked directly, the way nginx asks it.
		Transport: &http.Transport{
			// Looked at, not trusted: whether nginx checks the
			// certificate is the site's own setting.
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
			DisableKeepAlives: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, upstream, nil)
	if err != nil {
		return SitePreflightCheck{ID: id, Level: "warning", Title: upstream, Detail: err.Error()}
	}
	started = time.Now()
	res, err := client.Do(req)
	if err != nil {
		return SitePreflightCheck{
			ID: id, Level: "warning", Title: address + " accepts connections but did not answer",
			Detail: fmt.Sprintf("Connected in %d ms; a HEAD request then failed: %v.", connected.Milliseconds(), err),
		}
	}
	res.Body.Close()
	check := SitePreflightCheck{
		ID: id, Level: "ok", Title: fmt.Sprintf("%s answers %s", address, res.Status),
		Detail: fmt.Sprintf("HEAD %s in %d ms.", upstream, time.Since(started).Milliseconds()),
	}
	if res.StatusCode >= 500 {
		check.Level = "warning"
	}
	return check
}

// dnsCheck is CheckDomainDNS as a check. A name that does not point here is
// never a hold on the save: DNS is often changed after the site is ready, and
// behind a CDN or provider NAT the comparison cannot be made at all.
func dnsCheck(ctx context.Context, domain string) SitePreflightCheck {
	id := "dns:" + domain
	if strings.HasPrefix(domain, "*.") {
		return SitePreflightCheck{ID: id, Domain: domain, Level: "info", Title: "Wildcard", Detail: "A wildcard name is not looked up."}
	}
	check := CheckDomainDNS(ctx, domain)
	out := SitePreflightCheck{ID: id, Domain: domain, Detail: check.Summary}
	switch {
	case check.Error != "":
		out.Level, out.Title = "warning", "Does not resolve"
	case check.PointsHere:
		out.Level, out.Title = "ok", "Points here"
	case check.BehindProxy:
		out.Level, out.Title = "info", "Behind a proxy"
	case !check.HostAddressesKnown:
		out.Level, out.Title = "info", "Cannot compare"
	default:
		out.Level, out.Title = "warning", "Points elsewhere"
	}
	return out
}
