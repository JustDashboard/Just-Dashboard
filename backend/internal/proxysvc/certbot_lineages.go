package proxysvc

import (
	"bufio"
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// certbot's lineages, read from its files rather than asked of certbot.
//
// `certbot certificates` takes certbot's global lock, so while any certbot
// ran — an issuance from this page, or the host's own timer — it failed with
// "Another instance of Certbot is already running" and the page said certbot
// managed nothing and nothing renewed it. Holding that lock for a status read
// was also a way to make the timer's own run fail. The renewal configuration
// and the certificate it points at are plain files, readable at any time.

// letsencryptDir is certbot's configuration directory. A variable rather than
// a constant for the same reason as importedDir: a test running as an
// ordinary user points it at a directory of its own.
var letsencryptDir = "/etc/letsencrypt"

// renewalConf is one lineage's renewal/<name>.conf: the files it keeps, the
// authority it renews from, and how certbot proves control and deploys the
// result when it does — the [renewalparams] certbot restores for each
// renewal (renewal.py's reconstitute).
type renewalConf struct {
	Name      string
	Cert      string
	PrivKey   string
	FullChain string
	// Server is the ACME directory certbot renews from, read from
	// [renewalparams].
	Server string
	// Authenticator and Installer are the plugins the renewal runs, as
	// certbot names them: "nginx", "webroot", "dns-cloudflare". An
	// installer of None is none.
	Authenticator string
	Installer     string
	// Webroots are the folders the webroot plugin writes challenges into,
	// from [[webroot_map]] and webroot_path, each once.
	Webroots []string
	// Credentials is the file a DNS plugin reads its token from:
	// <plugin>_credentials, the plugin's name with its dashes as underscores.
	Credentials string
	// ManualAuthHook is what answers a manual challenge; without one the
	// manual plugin cannot run unattended.
	ManualAuthHook string
	// PreHook runs before the renewal (the standard way to stop whatever
	// holds port 80 for standalone), and DeployHook after a renewed
	// certificate is saved: --deploy-hook, stored as renew_hook.
	PreHook    string
	DeployHook string
	// HTTP01Port is where standalone listens; zero is certbot's default, 80.
	HTTP01Port int
	// err is why the file could not be read, kept on the lineage so it is
	// still listed: certbot will still try to renew it.
	err error
}

// staging reports a lineage renewed from a test authority, by certbot's own
// test (util.is_staging): the staging URL, or "staging" anywhere in it.
func (c renewalConf) staging() bool {
	return strings.Contains(c.Server, "staging")
}

// testCertificate reports a lineage whose certificate, leaf, browsers refuse
// as a test one: renewed from a staging authority, or signed by one whatever
// the configuration says now.
func (c renewalConf) testCertificate(leaf *x509.Certificate) bool {
	return c.staging() || stagingIssuer(leaf)
}

// readRenewalConf reads the configobj file certbot writes: key = value lines,
// top-level keys before the first [section], # comments.
func readRenewalConf(path string) (renewalConf, error) {
	conf := renewalConf{Name: strings.TrimSuffix(filepath.Base(path), ".conf")}
	file, err := os.Open(path)
	if err != nil {
		return conf, err
	}
	defer file.Close()
	section := ""
	params := map[string]string{}
	sc := bufio.NewScanner(file)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			// [renewalparams], and [[webroot_map]] nested inside it.
			section = strings.Trim(line, "[] ")
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.Trim(strings.TrimSpace(value), `"'`)
		switch section {
		case "":
			switch key {
			case "cert":
				conf.Cert = value
			case "privkey":
				conf.PrivKey = value
			case "fullchain":
				conf.FullChain = value
			}
		case "renewalparams":
			params[key] = value
		case "webroot_map":
			conf.Webroots = appendOnce(conf.Webroots, value)
		}
	}
	if err := sc.Err(); err != nil {
		return conf, err
	}
	// configobj writes None for an option that was never set.
	param := func(key string) string {
		if v := params[key]; v != "None" {
			return v
		}
		return ""
	}
	conf.Server = param("server")
	conf.Authenticator = param("authenticator")
	conf.Installer = param("installer")
	conf.ManualAuthHook = param("manual_auth_hook")
	conf.PreHook = param("pre_hook")
	conf.DeployHook = param("renew_hook")
	conf.HTTP01Port, _ = strconv.Atoi(param("http01_port"))
	if conf.Authenticator != "" {
		conf.Credentials = param(strings.ReplaceAll(conf.Authenticator, "-", "_") + "_credentials")
	}
	// A list of one is written with a trailing comma, so configobj reads
	// it back as a list: "/var/www/html,".
	for _, root := range strings.Split(param("webroot_path"), ",") {
		if root = strings.Trim(strings.TrimSpace(root), `"'`); root != "" {
			conf.Webroots = appendOnce(conf.Webroots, root)
		}
	}
	if conf.Cert == "" && conf.FullChain == "" {
		return conf, fmt.Errorf("%s names no certificate", path)
	}
	return conf, nil
}

func appendOnce(list []string, value string) []string {
	if slices.Contains(list, value) {
		return list
	}
	return append(list, value)
}

// readRenewalConfs is every lineage certbot renews, by name. A missing
// directory is certbot never having issued anything, which is not an error.
func readRenewalConfs(dir string) ([]renewalConf, error) {
	entries, err := os.ReadDir(filepath.Join(dir, "renewal"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("certbot's renewal directory could not be read: %w", err)
	}
	var confs []renewalConf
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".conf") {
			continue
		}
		conf, err := readRenewalConf(filepath.Join(dir, "renewal", e.Name()))
		conf.err = err
		confs = append(confs, conf)
	}
	sort.Slice(confs, func(i, j int) bool { return confs[i].Name < confs[j].Name })
	return confs, nil
}

// leaf is the lineage's own certificate: cert.pem, or the first block of the
// full chain where the configuration names only that.
func (c renewalConf) leaf() (*x509.Certificate, error) {
	if c.err != nil {
		return nil, c.err
	}
	path := c.Cert
	if path == "" {
		path = c.FullChain
	}
	return readLeaf(path)
}

// readCertbotLineages is what `certbot certificates` printed, without asking
// certbot. A lineage whose certificate cannot be read is listed with the
// reason, since it is still one certbot will try to renew.
func readCertbotLineages(dir string) ([]CertbotCert, error) {
	confs, err := readRenewalConfs(dir)
	if err != nil {
		return []CertbotCert{}, err
	}
	return lineagesFrom(confs), nil
}

func lineagesFrom(confs []renewalConf) []CertbotCert {
	certs := []CertbotCert{}
	for _, conf := range confs {
		cert := CertbotCert{
			Name: conf.Name, Domains: []string{}, CertPath: conf.FullChain, KeyPath: conf.PrivKey,
			Authenticator: pluginName(conf.Authenticator), Installer: pluginName(conf.Installer),
			Webroots: conf.Webroots, DeployHook: conf.DeployHook != "",
		}
		if provider, ok := dnsProviderForPlugin(cert.Authenticator); ok {
			cert.DNSProvider = provider.Name
		}
		leaf, err := conf.leaf()
		if err != nil {
			cert.Error = err.Error()
			certs = append(certs, cert)
			continue
		}
		summary := summarise(leaf, conf.Name, conf.FullChain)
		cert.Domains = summary.Domains
		cert.NotBefore = summary.NotBefore
		cert.Expiry = summary.NotAfter
		cert.DaysLeft = summary.DaysLeft
		cert.Valid = !summary.Expired
		cert.Serial = leaf.SerialNumber.Text(16)
		cert.Staging = conf.testCertificate(leaf)
		certs = append(certs, cert)
	}
	return certs
}

// pluginName is a plugin as certbot's current releases name it. Before 1.0 a
// third-party plugin was stored with its package in front of the name:
// "certbot-dns-cloudflare:dns-cloudflare".
func pluginName(name string) string {
	if _, after, ok := strings.Cut(name, ":"); ok {
		return after
	}
	return name
}

func dnsProviderForPlugin(plugin string) (DNSProvider, bool) {
	for _, p := range dnsProviders {
		if p.Plugin == plugin {
			return p, true
		}
	}
	return DNSProvider{}, false
}

// builtinAuthenticators ship inside certbot itself. `certbot plugins` hides
// manual and null from its listing, so their absence from it says nothing.
var builtinAuthenticators = map[string]bool{"manual": true, "null": true, "standalone": true, "webroot": true}

// renewalProblems is, for each lineage, what will make its next renewal fail,
// found before the run that finds out: each one is a certain failure in
// certbot's own code, not a guess.
//
// A webroot folder that no longer exists is not one. certbot's webroot plugin
// creates every missing folder on the challenge's path, so the renewal still
// passes wherever a site serves that folder; whether one does is not
// something a missing folder says.
//
// An installer plugin that went missing is not one either: a renewal runs
// as certonly, which picks the installer only if it can, and renews without
// deploying — which the page reads as nginx not being reloaded.
func (s *Service) renewalProblems(ctx context.Context, rt *certbotRuntime, confs []renewalConf) map[string][]string {
	problems := map[string][]string{}
	add := func(name, format string, args ...any) {
		problems[name] = append(problems[name], fmt.Sprintf(format, args...))
	}
	var credentials []string
	var standalone []renewalConf
	for _, conf := range confs {
		if conf.err != nil {
			continue
		}
		auth := pluginName(conf.Authenticator)
		switch {
		case auth == "":
			add(conf.Name, "Its renewal configuration names no authenticator, so certbot skips it and the run fails.")
		case rt != nil && rt.authenticators != nil && !builtinAuthenticators[auth] && !rt.authenticators[auth]:
			add(conf.Name, "certbot renews it with the %s plugin, which %s does not have.", auth, rt.where())
		case auth == "manual" && conf.ManualAuthHook == "":
			add(conf.Name, "It was issued by hand with the manual plugin, which renews unattended only with an auth hook, and it has none.")
		case auth == "standalone" && conf.PreHook == "":
			standalone = append(standalone, conf)
		}
		if conf.Credentials != "" && filepath.IsAbs(conf.Credentials) {
			credentials = append(credentials, conf.Credentials)
		}
	}
	if len(credentials) > 0 && rt != nil {
		missing := rt.missingFiles(ctx, credentials)
		for _, conf := range confs {
			if conf.Credentials != "" && missing[conf.Credentials] {
				add(conf.Name, "certbot reads the %s credentials from %s, which is gone.", credentialsOwner(conf), conf.Credentials)
			}
		}
	}
	// A pre hook in the renewal-hooks directory runs before every renewal,
	// and stopping whatever holds port 80 is what one is usually for.
	if len(standalone) > 0 && len(executablesIn(filepath.Join(letsencryptDir, "renewal-hooks", "pre"), "")) == 0 {
		listeners, err := ListListeners(ctx)
		if err == nil {
			for _, conf := range standalone {
				port := conf.HTTP01Port
				if port == 0 {
					port = 80
				}
				if holder := tcpHolder(listeners, port); holder != "" {
					add(conf.Name, "certbot's standalone server needs port %d, which %s holds, and no pre hook frees it before the renewal.", port, holder)
				}
			}
		}
	}
	return problems
}

func credentialsOwner(conf renewalConf) string {
	if provider, ok := dnsProviderForPlugin(pluginName(conf.Authenticator)); ok {
		return provider.Name
	}
	return pluginName(conf.Authenticator)
}

// tcpHolder names what listens on a TCP port, or "" when nothing does.
func tcpHolder(listeners []Listener, port int) string {
	for _, l := range listeners {
		if l.Protocol == "tcp" && int(l.Port) == port {
			if l.Process != "" {
				return l.Process
			}
			return "another process"
		}
	}
	return ""
}

// executablesIn is what certbot runs from a hooks directory — every
// executable file whose name does not end in "~" (hooks.list_hooks) — other
// than except.
func executablesIn(dir, except string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.Name() == except || strings.HasSuffix(e.Name(), "~") {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, e.Name()))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		names = append(names, e.Name())
	}
	return names
}

// missingFiles is which of paths do not exist where certbot runs: on the host
// for the host's certbot, whose paths need not be this process's. stat takes
// them all at once; a path it cannot reach for any other reason, permission
// included, is not called missing.
func (rt *certbotRuntime) missingFiles(ctx context.Context, paths []string) map[string]bool {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := rt.on(ctx, "stat", append([]string{"-L", "--format=%n", "--"}, paths...)...)
	cmd.Env = append(cmd.Environ(), "LC_ALL=C")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	_ = cmd.Run()
	missing := map[string]bool{}
	for _, path := range paths {
		if strings.Contains(stderr.String(), "'"+path+"': No such file or directory") {
			missing[path] = true
		}
	}
	return missing
}

// written is when certbot last saved the lineage's certificate: the
// version its live link points at, which certbot writes whole on each
// renewal. A failed run before it no longer describes the lineage.
func (c renewalConf) written() (time.Time, bool) {
	path := c.Cert
	if path == "" {
		path = c.FullChain
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

// lineageFor finds the lineage certbot would treat as the same certificate as
// a request for these names: exactly the same set, the case certbot resolves
// without a question.
func lineageFor(dir string, domains []string) (renewalConf, *x509.Certificate, bool) {
	want := map[string]bool{}
	for _, d := range domains {
		want[strings.ToLower(d)] = true
	}
	confs, _ := readRenewalConfs(dir)
	for _, conf := range confs {
		leaf, err := conf.leaf()
		if err != nil {
			continue
		}
		names := leaf.DNSNames
		if len(names) == 0 && leaf.Subject.CommonName != "" {
			names = []string{leaf.Subject.CommonName}
		}
		have := map[string]bool{}
		for _, n := range names {
			have[strings.ToLower(n)] = true
		}
		if len(have) != len(want) {
			continue
		}
		same := true
		for n := range want {
			same = same && have[n]
		}
		if same {
			return conf, leaf, true
		}
	}
	return renewalConf{}, nil, false
}

// CertbotSerials is each lineage's current serial, by name — what a job
// compares before and after to tell a certificate certbot replaced from one
// it decided to keep (its "no action taken" exits 0 either way), and which
// lineages a run renewed. A lineage whose certificate cannot be read has no
// serial to compare and is left out.
func CertbotSerials() (map[string]string, error) {
	confs, err := readRenewalConfs(letsencryptDir)
	if err != nil {
		return nil, err
	}
	serials := map[string]string{}
	for _, conf := range confs {
		if leaf, err := conf.leaf(); err == nil {
			serials[conf.Name] = leaf.SerialNumber.Text(16)
		}
	}
	return serials, nil
}

// readLeaf is the first certificate in a PEM file.
func readLeaf(path string) (*x509.Certificate, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("not a PEM certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

// UseCertificateDirsForTest points certbot's directory and the import
// directory at a test's own and forgets which certbot was found, for handler
// tests that run as an ordinary user with a certbot of their own on PATH.
// Production never calls this.
func UseCertificateDirsForTest(letsencrypt, imported string) (restore func()) {
	previousLetsencrypt, previousImported := letsencryptDir, importedDir
	letsencryptDir, importedDir = letsencrypt, imported
	forgetCertbotRuntime()
	return func() {
		letsencryptDir, importedDir = previousLetsencrypt, previousImported
		forgetCertbotRuntime()
	}
}
