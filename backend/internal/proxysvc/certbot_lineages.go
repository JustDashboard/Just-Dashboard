package proxysvc

import (
	"bufio"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// renewalConf is one lineage's renewal/<name>.conf: the files it keeps, and
// the authority it renews from.
type renewalConf struct {
	Name      string
	Cert      string
	PrivKey   string
	FullChain string
	// Server is the ACME directory certbot renews from, read from
	// [renewalparams].
	Server string
	// err is why the file could not be read, kept on the lineage so it is
	// still listed: certbot will still try to renew it.
	err error
}

// staging reports a lineage renewed from a test authority, by certbot's own
// test (util.is_staging): the staging URL, or "staging" anywhere in it.
func (c renewalConf) staging() bool {
	return strings.Contains(c.Server, "staging")
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
	sc := bufio.NewScanner(file)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.Trim(line, "[] ")
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.Trim(strings.TrimSpace(value), `"'`)
		switch {
		case section == "" && key == "cert":
			conf.Cert = value
		case section == "" && key == "privkey":
			conf.PrivKey = value
		case section == "" && key == "fullchain":
			conf.FullChain = value
		case section == "renewalparams" && key == "server":
			conf.Server = value
		}
	}
	if err := sc.Err(); err != nil {
		return conf, err
	}
	if conf.Cert == "" && conf.FullChain == "" {
		return conf, fmt.Errorf("%s names no certificate", path)
	}
	return conf, nil
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
	certs := []CertbotCert{}
	if err != nil {
		return certs, err
	}
	for _, conf := range confs {
		cert := CertbotCert{Name: conf.Name, Domains: []string{}, CertPath: conf.FullChain, KeyPath: conf.PrivKey}
		leaf, err := conf.leaf()
		if err != nil {
			cert.Error = err.Error()
			certs = append(certs, cert)
			continue
		}
		summary := summarise(leaf, conf.Name, conf.FullChain)
		cert.Domains = summary.Domains
		cert.Expiry = summary.NotAfter
		cert.DaysLeft = summary.DaysLeft
		cert.Valid = !summary.Expired
		cert.Serial = leaf.SerialNumber.Text(16)
		cert.Staging = conf.staging() || strings.HasPrefix(leaf.Issuer.CommonName, "(STAGING)")
		certs = append(certs, cert)
	}
	return certs, nil
}

// lineageFor finds the lineage certbot would treat as the same certificate as
// a request for these names: exactly the same set, the case certbot resolves
// without a question.
func lineageFor(dir string, domains []string) (renewalConf, bool) {
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
			return conf, true
		}
	}
	return renewalConf{}, false
}

// CertbotSerials is each lineage's current serial, by name — what a job
// compares before and after to tell a certificate certbot replaced from one
// it decided to keep. Its "no action taken" exits 0 either way.
func CertbotSerials() (map[string]string, error) {
	confs, err := readRenewalConfs(letsencryptDir)
	if err != nil {
		return nil, err
	}
	serials := map[string]string{}
	for _, conf := range confs {
		leaf, err := conf.leaf()
		if err != nil {
			return nil, err
		}
		serials[conf.Name] = leaf.SerialNumber.Text(16)
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
