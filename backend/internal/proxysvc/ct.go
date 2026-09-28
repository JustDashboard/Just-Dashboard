package proxysvc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"
)

// Certificate Transparency: every publicly trusted certificate is logged
// before a browser accepts it, so the logs list what any authority has
// signed for this host's domains, including certificates this host never
// asked for. crt.sh indexes the logs and answers a domain query in JSON.
//
// Asking it hands the domain names to a third party, which is why the page
// only asks once an administrator switches the monitor on.

const (
	// ctSource is crt.sh's search; a test points a monitor at a fixture.
	ctSource = "https://crt.sh/"
	// ctCacheFor is how long one domain's answer is reused. crt.sh is slow
	// and rate-limits heavy callers; a new certificate is worth hearing about
	// the same day, not the same minute.
	ctCacheFor = 6 * time.Hour
	// ctResponseCap bounds one answer. A domain with more live certificates
	// than fit is refused rather than listed in part: a partial list could
	// leave out exactly the certificate the monitor is for.
	ctResponseCap = 8 << 20
	// ctQueryTimeout bounds one query; crt.sh often takes tens of seconds.
	ctQueryTimeout = 45 * time.Second
	// ctMaxDomains bounds how many registered domains one report asks
	// about, so a host with many sites stays a polite caller.
	ctMaxDomains = 10
	// ctParallel is how many queries run at once.
	ctParallel = 3
)

// CTCertificate is one certificate the logs hold for a domain.
type CTCertificate struct {
	// ID is crt.sh's id; URL opens its page there.
	ID  int64  `json:"id"`
	URL string `json:"url"`
	// Issuer is the issuing CA's common name (R11) and IssuerOrg its
	// organisation (Let's Encrypt), which is what stays the same across an
	// authority's intermediates and so what a certificate is judged by.
	Issuer    string    `json:"issuer"`
	IssuerOrg string    `json:"issuerOrg"`
	Names     []string  `json:"names"`
	Serial    string    `json:"serial"`
	NotBefore time.Time `json:"notBefore"`
	NotAfter  time.Time `json:"notAfter"`
	Logged    time.Time `json:"logged"`
	// Ours is a certificate whose serial is one this host holds: a live
	// certificate, an imported one, or an earlier renewal in certbot's
	// archive.
	Ours bool `json:"ours"`
	// UnexpectedIssuer is a certificate this host does not hold from an
	// authority that signed none of the certificates it does hold.
	UnexpectedIssuer bool `json:"unexpectedIssuer"`
}

// CTDomain is what the logs hold for one registered domain and its
// subdomains, as of Checked.
type CTDomain struct {
	Domain       string          `json:"domain"`
	Certificates []CTCertificate `json:"certificates"`
	Checked      time.Time       `json:"checked,omitzero"`
	Error        string          `json:"error,omitempty"`
}

// CTReport is the monitor's answer for the whole host.
type CTReport struct {
	Source  string     `json:"source"`
	Domains []CTDomain `json:"domains"`
	// Skipped are registered domains past ctMaxDomains, left unasked.
	Skipped []string `json:"skipped"`
	// ExpectedIssuers are the organisations that signed this host's own
	// publicly trusted certificates. Empty, no issuer is called unexpected:
	// there is nothing to compare with.
	ExpectedIssuers []string `json:"expectedIssuers"`
}

// ctEntry is one row of crt.sh's JSON, the fields the monitor reads.
type ctEntry struct {
	ID         int64  `json:"id"`
	IssuerName string `json:"issuer_name"`
	CommonName string `json:"common_name"`
	NameValue  string `json:"name_value"`
	Serial     string `json:"serial_number"`
	NotBefore  string `json:"not_before"`
	NotAfter   string `json:"not_after"`
	Logged     string `json:"entry_timestamp"`
}

type ctCached struct {
	at      time.Time
	entries []ctEntry
}

// CTMonitor asks crt.sh and keeps each domain's answer for ctCacheFor. It
// caches the logs' rows, not the verdicts: a renewal made an hour ago is
// recognised as ours on the next read without asking crt.sh again.
type CTMonitor struct {
	client *http.Client
	source string
	now    func() time.Time

	mu    sync.Mutex
	cache map[string]ctCached
	// inflight shares one query per domain between concurrent readers.
	inflight map[string]*ctCall
}

type ctCall struct {
	done    chan struct{}
	entries []ctEntry
	err     error
}

func NewCTMonitor() *CTMonitor {
	return &CTMonitor{
		client:   &http.Client{Timeout: ctQueryTimeout},
		source:   ctSource,
		now:      time.Now,
		cache:    map[string]ctCached{},
		inflight: map[string]*ctCall{},
	}
}

// ctLocal is what this host holds, to judge the logs' rows against.
type ctLocal struct {
	serials map[string]bool
	issuers map[string]bool
}

// Transparency reports the logged certificates for every registered domain
// this host's sites and certificates name.
func (m *CTMonitor) Transparency(ctx context.Context, s *Service) (*CTReport, error) {
	certs, err := s.CertificateInventory(ctx)
	if err != nil {
		return nil, err
	}
	vhosts, err := s.ListVHosts(ctx)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, v := range vhosts {
		names = append(names, v.ServerNames...)
	}
	for _, c := range certs {
		names = append(names, c.Domains...)
	}
	domains := RegisteredDomains(names)
	report := &CTReport{Source: m.source, Domains: []CTDomain{}, Skipped: []string{}}
	if len(domains) > ctMaxDomains {
		report.Skipped = domains[ctMaxDomains:]
		domains = domains[:ctMaxDomains]
	}
	local := localCTEvidence(certs)
	report.ExpectedIssuers = slices.Sorted(maps.Keys(local.issuers))

	results := make([]CTDomain, len(domains))
	sem := make(chan struct{}, ctParallel)
	var wg sync.WaitGroup
	for i, domain := range domains {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = m.domain(ctx, domain, local)
		}()
	}
	wg.Wait()
	report.Domains = results
	return report, nil
}

func (m *CTMonitor) domain(ctx context.Context, domain string, local ctLocal) CTDomain {
	out := CTDomain{Domain: domain, Certificates: []CTCertificate{}}
	entries, at, err := m.entries(ctx, domain)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Checked = at
	now := m.now()
	seen := map[string]int{}
	for _, e := range entries {
		cert, ok := ctCertificate(e)
		if !ok || cert.NotAfter.Before(now) {
			continue
		}
		// A precertificate and the certificate issued from it share a
		// serial and are one certificate to the operator.
		key := normalSerial(e.Serial)
		if i, dup := seen[key]; dup {
			if cert.Logged.Before(out.Certificates[i].Logged) {
				out.Certificates[i].Logged = cert.Logged
			}
			continue
		}
		cert.Ours = local.serials[key]
		cert.UnexpectedIssuer = !cert.Ours && len(local.issuers) > 0 && !local.issuers[cert.IssuerOrg]
		seen[key] = len(out.Certificates)
		out.Certificates = append(out.Certificates, cert)
	}
	sort.SliceStable(out.Certificates, func(i, j int) bool {
		return out.Certificates[i].NotBefore.After(out.Certificates[j].NotBefore)
	})
	return out
}

// entries is the logs' rows for a domain and when they were read: from the
// cache while it is fresh, otherwise asked of crt.sh by exactly one caller.
func (m *CTMonitor) entries(ctx context.Context, domain string) ([]ctEntry, time.Time, error) {
	m.mu.Lock()
	if c, ok := m.cache[domain]; ok && m.now().Sub(c.at) < ctCacheFor {
		m.mu.Unlock()
		return c.entries, c.at, nil
	}
	call, running := m.inflight[domain]
	if !running {
		call = &ctCall{done: make(chan struct{})}
		m.inflight[domain] = call
	}
	m.mu.Unlock()

	if !running {
		// Detached from the caller's context so a reader who gives up
		// does not fail the query for the others waiting on it; the
		// client's own timeout still bounds it.
		call.entries, call.err = m.query(context.WithoutCancel(ctx), domain)
		at := m.now()
		m.mu.Lock()
		delete(m.inflight, domain)
		// Failures are not cached: crt.sh's errors are mostly load, and
		// the next read should try again.
		if call.err == nil {
			m.cache[domain] = ctCached{at: at, entries: call.entries}
		}
		m.mu.Unlock()
		close(call.done)
		return call.entries, at, call.err
	}
	select {
	case <-call.done:
		return call.entries, m.now(), call.err
	case <-ctx.Done():
		return nil, time.Time{}, ctx.Err()
	}
}

// query asks for the domain itself and for its subdomains: crt.sh matches
// `%.example.com` against names below example.com only.
func (m *CTMonitor) query(ctx context.Context, domain string) ([]ctEntry, error) {
	out := []ctEntry{}
	ids := map[int64]bool{}
	for _, q := range []string{domain, "%." + domain} {
		rows, err := m.fetch(ctx, q)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if !ids[r.ID] {
				ids[r.ID] = true
				out = append(out, r)
			}
		}
	}
	return out, nil
}

func (m *CTMonitor) fetch(ctx context.Context, q string) ([]ctEntry, error) {
	params := url.Values{}
	params.Set("q", q)
	params.Set("output", "json")
	params.Set("exclude", "expired")
	params.Set("deduplicate", "Y")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.source+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("crt.sh did not answer for %s: %w", q, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("crt.sh answered %s for %s", resp.Status, q)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, ctResponseCap+1))
	if err != nil {
		return nil, fmt.Errorf("reading crt.sh's answer for %s: %w", q, err)
	}
	if len(body) > ctResponseCap {
		return nil, fmt.Errorf("crt.sh lists more live certificates for %s than fit in %d MiB; look it up on crt.sh directly", q, ctResponseCap>>20)
	}
	var rows []ctEntry
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("crt.sh's answer for %s is not the JSON list it returns when it is healthy", q)
	}
	return rows, nil
}

// ctTimeLayout is crt.sh's timestamp: UTC, no zone, sometimes with a
// fraction, which time.Parse accepts without the layout naming it.
const ctTimeLayout = "2006-01-02T15:04:05"

func ctCertificate(e ctEntry) (CTCertificate, bool) {
	notBefore, err1 := time.Parse(ctTimeLayout, e.NotBefore)
	notAfter, err2 := time.Parse(ctTimeLayout, e.NotAfter)
	if err1 != nil || err2 != nil || e.Serial == "" {
		return CTCertificate{}, false
	}
	logged, _ := time.Parse(ctTimeLayout, e.Logged)
	names := []string{}
	for n := range strings.SplitSeq(e.NameValue, "\n") {
		if n = strings.ToLower(strings.TrimSpace(n)); n != "" && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	if len(names) == 0 && e.CommonName != "" {
		names = []string{strings.ToLower(e.CommonName)}
	}
	issuerCN := dnAttribute(e.IssuerName, "CN")
	issuerOrg := dnAttribute(e.IssuerName, "O")
	if issuerOrg == "" {
		issuerOrg = e.IssuerName
	}
	if issuerCN == "" {
		issuerCN = issuerOrg
	}
	serial := ""
	if n, ok := new(big.Int).SetString(normalSerial(e.Serial), 16); ok {
		serial = colonHex(n.Bytes())
	}
	return CTCertificate{
		ID:        e.ID,
		URL:       ctSource + "?id=" + strconv.FormatInt(e.ID, 10),
		Issuer:    issuerCN,
		IssuerOrg: issuerOrg,
		Names:     names,
		Serial:    serial,
		NotBefore: notBefore.UTC(),
		NotAfter:  notAfter.UTC(),
		Logged:    logged.UTC(),
	}, true
}

// normalSerial is a serial in one spelling whichever way it was written:
// crt.sh's lowercase hex with a leading zero byte kept, or the colon form
// this package prints.
func normalSerial(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, ":", ""))
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return "0"
	}
	return s
}

// dnAttribute reads one attribute from a distinguished name as crt.sh
// prints it: `C=US, O="DigiCert, Inc.", CN=…`, quoting a value that holds a
// comma.
func dnAttribute(dn, key string) string {
	for len(dn) > 0 {
		dn = strings.TrimLeft(dn, " ")
		eq := strings.IndexByte(dn, '=')
		if eq < 0 {
			return ""
		}
		name := strings.TrimSpace(dn[:eq])
		rest := dn[eq+1:]
		var value string
		if strings.HasPrefix(rest, `"`) {
			end := strings.IndexByte(rest[1:], '"')
			if end < 0 {
				return ""
			}
			value, rest = rest[1:1+end], rest[2+end:]
			if i := strings.IndexByte(rest, ','); i >= 0 {
				rest = rest[i+1:]
			} else {
				rest = ""
			}
		} else if i := strings.IndexByte(rest, ','); i >= 0 {
			value, rest = rest[:i], rest[i+1:]
		} else {
			value, rest = rest, ""
		}
		if strings.EqualFold(name, key) {
			return strings.TrimSpace(value)
		}
		dn = rest
	}
	return ""
}

// localCTEvidence gathers the serials this host holds and the organisations
// that signed its publicly trusted certificates. certbot's archive keeps
// every earlier renewal, which the logs still list until it expires.
func localCTEvidence(certs []Certificate) ctLocal {
	local := ctLocal{serials: map[string]bool{}, issuers: map[string]bool{}}
	paths := []string{}
	for _, c := range certs {
		if c.Error == "" && !c.SelfSigned && !c.LocalCA {
			paths = append(paths, c.Path)
		}
	}
	archived, _ := filepath.Glob(filepath.Join(letsencryptDir, "archive", "*", "cert*.pem"))
	paths = append(paths, archived...)
	for _, p := range paths {
		leaf, err := readLeaf(p)
		if err != nil || leaf.Issuer.String() == leaf.Subject.String() {
			continue
		}
		local.serials[normalSerial(leaf.SerialNumber.Text(16))] = true
		// A test certificate is never logged publicly and its staging
		// authority is not one the logs' certificates should match.
		if stagingIssuer(leaf) {
			continue
		}
		org := strings.Join(leaf.Issuer.Organization, ", ")
		if org == "" {
			org = leaf.Issuer.String()
		}
		local.issuers[org] = true
	}
	return local
}

// RegisteredDomains reduces host names to the domains they are registered
// under (app.example.co.uk → example.co.uk), sorted and once each. A name
// under no public suffix — localhost, an internal .lan, an address, a
// catch-all — is left out: no public authority can sign for it.
func RegisteredDomains(names []string) []string {
	set := map[string]bool{}
	for _, n := range names {
		n = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(n)), ".")
		n = strings.TrimPrefix(n, "*.")
		if n == "" || strings.ContainsAny(n, "~*/: ") || !strings.Contains(n, ".") || net.ParseIP(n) != nil {
			continue
		}
		suffix, icann := publicsuffix.PublicSuffix(n)
		if !icann && !strings.Contains(suffix, ".") {
			continue
		}
		registered, err := publicsuffix.EffectiveTLDPlusOne(n)
		if err != nil {
			continue
		}
		set[registered] = true
	}
	return slices.Sorted(maps.Keys(set))
}
