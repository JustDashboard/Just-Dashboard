package netsec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Port, scan, banner and SSH key readings with their coverage and confidence
// stated: what was actually tried, what answered, and how much a self-reported
// greeting can be believed.

// dialFailure classifies a TCP dial error by the kernel's errno where there is
// one, falling back to the error text the resolver produces.
func dialFailure(err error) (state, sentence string) {
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "closed", "Refused: the address answered with a reset. A closed port or a rejecting firewall causes this."
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return "unreachable", "Unreachable: a router or this host reported no route to the address."
	case isTimeout(err):
		return "no reply", "No reply before the deadline. A firewall dropping packets, an offline host or a broken route all look like this."
	case isDNSNotFound(err):
		return "dns", "The name did not resolve."
	}
	return "error", err.Error()
}

func dialedAddress(err error) string {
	var op *net.OpError
	if errors.As(err, &op) && op.Addr != nil {
		return op.Addr.String()
	}
	return ""
}

// scanAddress resolves a scan target once so every port is tried against the
// same address; a name with several addresses would otherwise be scanned
// partly on each.
func scanAddress(ctx context.Context, target string) (netip.Addr, []netip.Addr, error) {
	if a, err := netip.ParseAddr(target); err == nil {
		return a.Unmap(), nil, nil
	}
	addrs, err := lookupResolver.LookupNetIP(ctx, "ip", strings.TrimSuffix(target, "."))
	if err != nil {
		return netip.Addr{}, nil, err
	}
	if len(addrs) == 0 {
		return netip.Addr{}, nil, fmt.Errorf("%s has no addresses", target)
	}
	sort.SliceStable(addrs, func(i, j int) bool { return addrs[i].Unmap().Is4() && !addrs[j].Unmap().Is4() })
	return addrs[0].Unmap(), addrs[1:], nil
}

type scanOutcome struct {
	State    string
	Duration time.Duration
	Detail   string
}

var scanDial = func(ctx context.Context, address string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 2 * time.Second}
	return d.DialContext(ctx, "tcp", address)
}

func scanPortsAt(ctx context.Context, addr netip.Addr, ports []ServicePreset) []scanOutcome {
	out := make([]scanOutcome, len(ports))
	sem := make(chan struct{}, 12)
	var wg sync.WaitGroup
	for i, sp := range ports {
		wg.Add(1)
		go func(i int, sp ServicePreset) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			start := time.Now()
			conn, err := scanDial(ctx, net.JoinHostPort(addr.String(), sp.Port))
			out[i].Duration = time.Since(start)
			if err == nil {
				conn.Close()
				out[i].State = "open"
				return
			}
			state, detail := dialFailure(err)
			if state == "error" || state == "dns" {
				detail = shortDialError(err)
			}
			out[i].State, out[i].Detail = state, detail
		}(i, sp)
	}
	wg.Wait()
	return out
}

func interpretScan(res *ProbeResult, addr netip.Addr, others []netip.Addr, ports []ServicePreset, outcomes []scanOutcome) {
	list := make([]string, 0, len(ports))
	for _, sp := range ports {
		list = append(list, sp.Port)
	}
	res.fact("Scanned address", addr.String(), BasisObserved)
	if len(others) > 0 {
		var names []string
		for _, a := range others {
			names = append(names, a.String())
		}
		res.fact("Other addresses (not scanned)", strings.Join(names, ", "), BasisObserved)
	}
	res.fact("Coverage", fmt.Sprintf("exactly %d TCP ports from the service catalogue: %s", len(ports), strings.Join(list, ", ")), BasisConfigured)
	res.fact("Method", "full TCP connect, 2 s per port, 12 at a time", BasisConfigured)
	res.fact("Not covered", "UDP services and every other TCP port", BasisConfigured)
	res.Limitations = append(res.Limitations,
		"Closed means the address refused the connection and no reply means nothing answered; neither identifies which device decided.",
		"An open port proves only that a TCP handshake completed from this host, not what the service is or who else can reach it.")
	table := ProbeTable{ID: "ports", Title: "Ports tried", Columns: []string{"Port", "Service", "State", "Time", "Note"}}
	counts := map[string]int{}
	for i, sp := range ports {
		o := outcomes[i]
		counts[o.State]++
		note := o.Detail
		if o.State == "open" {
			res.Records = append(res.Records, sp.Port+" "+sp.Name)
			note = sp.Danger
			if sp.Danger != "" {
				res.finding("open-"+sp.Port, "warning", fmt.Sprintf("Port %s (%s) answers", sp.Port, sp.Name), sp.Danger, "Service on "+addr.String())
			}
		} else if o.State == "closed" || o.State == "no reply" {
			note = ""
		}
		table.Rows = append(table.Rows, []string{sp.Port, sp.Name, o.State, o.Duration.Round(time.Millisecond).String(), note})
	}
	res.Tables = append(res.Tables, table)
	res.metric("open", "Open", float64(counts["open"]), "")
	res.metric("closed", "Closed (refused)", float64(counts["closed"]), "")
	res.metric("no_reply", "No reply", float64(counts["no reply"]), "")
	res.OK = true
	res.Summary = fmt.Sprintf("%d open, %d closed, %d without a reply of %d TCP ports on %s.", counts["open"], counts["closed"], counts["no reply"], len(ports), addr)
	switch {
	case len(res.Findings) > 0:
		res.Verdict = ProbeFindings
	case counts["open"] == 0 && counts["closed"] == 0:
		res.Verdict = ProbeUnknown
		res.Summary += " Nothing answered at all, which a host dropping every packet and an offline host both produce."
	default:
		res.Verdict = ProbeOK
	}
}

type bannerID struct {
	Protocol, Product, Version, Confidence, Basis string
	Details                                       []ProbeFact
}

var (
	sshIdent   = regexp.MustCompile(`^SSH-(\d+\.\d+)-([^\s\r\n]+)(?: ([^\r\n]*))?`)
	rfbIdent   = regexp.MustCompile(`^RFB (\d{3})\.(\d{3})\n`)
	versionTok = regexp.MustCompile(`\b(\d+(?:\.\d+)+[a-z0-9]*)\b`)
)

var bannerProducts = []struct{ match, name string }{
	{"postfix", "Postfix"}, {"exim", "Exim"}, {"sendmail", "Sendmail"}, {"microsoft esmtp", "Microsoft Exchange"},
	{"opensmtpd", "OpenSMTPD"}, {"haraka", "Haraka"}, {"dovecot", "Dovecot"}, {"cyrus", "Cyrus"},
	{"courier", "Courier"}, {"vsftpd", "vsftpd"}, {"proftpd", "ProFTPD"}, {"pure-ftpd", "Pure-FTPd"},
	{"filezilla", "FileZilla Server"}, {"zimbra", "Zimbra"}, {"qmail", "qmail"}, {"mailcow", "mailcow"},
}

func bannerProduct(text string) (string, string) {
	lower := strings.ToLower(text)
	for _, p := range bannerProducts {
		if i := strings.Index(lower, p.match); i >= 0 {
			version := ""
			if m := versionTok.FindStringSubmatch(text[i:]); m != nil {
				version = m[1]
			}
			return p.name, version
		}
	}
	return "", ""
}

// identifyBanner reads a greeting by the protocol's own grammar first. The
// protocol is high confidence only when the bytes follow that protocol's
// defined greeting; a product named inside free text is self-reported; a
// guess from the port number is low.
func identifyBanner(port int, raw []byte) bannerID {
	text := string(raw)
	if m := sshIdent.FindStringSubmatch(text); m != nil {
		id := bannerID{Protocol: "SSH " + m[1], Confidence: "high", Basis: "RFC 4253 identification string"}
		software := m[2]
		if name, version, ok := strings.Cut(software, "_"); ok {
			id.Product, id.Version = name, version
		} else {
			id.Product = software
		}
		if m[3] != "" {
			id.Details = append(id.Details, ProbeFact{Label: "Comment", Value: m[3], Basis: BasisSelfReported})
		}
		return id
	}
	if m := rfbIdent.FindStringSubmatch(text); m != nil {
		major, _ := strconv.Atoi(m[1])
		minor, _ := strconv.Atoi(m[2])
		return bannerID{Protocol: fmt.Sprintf("VNC (RFB %d.%d)", major, minor), Confidence: "high", Basis: "RFB protocol version handshake"}
	}
	if id, ok := mysqlHandshake(raw); ok {
		return id
	}
	switch {
	case strings.HasPrefix(text, "+OK"):
		id := bannerID{Protocol: "POP3", Confidence: "high", Basis: "POP3 positive greeting (+OK)"}
		id.Product, id.Version = bannerProduct(text)
		return id
	case strings.HasPrefix(text, "* OK") || strings.HasPrefix(text, "* PREAUTH") || strings.HasPrefix(text, "* BYE"):
		id := bannerID{Protocol: "IMAP", Confidence: "high", Basis: "IMAP untagged greeting"}
		id.Product, id.Version = bannerProduct(text)
		if i := strings.Index(text, "[CAPABILITY "); i >= 0 {
			if j := strings.Index(text[i:], "]"); j > 0 {
				id.Details = append(id.Details, ProbeFact{Label: "Capabilities", Value: text[i+12 : i+j], Basis: BasisSelfReported})
			}
		}
		return id
	case strings.HasPrefix(text, "220"):
		upper := strings.ToUpper(text)
		id := bannerID{Confidence: "medium", Basis: "220 greeting shared by SMTP and FTP"}
		switch {
		case strings.Contains(upper, "SMTP"):
			id.Protocol, id.Confidence, id.Basis = "SMTP", "high", "220 greeting naming SMTP/ESMTP"
		case strings.Contains(upper, "FTP"):
			id.Protocol, id.Confidence, id.Basis = "FTP", "high", "220 greeting naming FTP"
		case port == 25 || port == 587 || port == 2525:
			id.Protocol = "SMTP (by port)"
		case port == 21:
			id.Protocol = "FTP (by port)"
		default:
			id.Protocol, id.Confidence = "SMTP or FTP", "low"
		}
		id.Product, id.Version = bannerProduct(text)
		return id
	}
	if preset, ok := PresetFor(strconv.Itoa(port), "tcp"); ok {
		return bannerID{Protocol: preset.Name + " (by port only)", Confidence: "low", Basis: "port number in the service catalogue; the greeting matched no known grammar"}
	}
	return bannerID{Protocol: "unidentified", Confidence: "none", Basis: "the greeting matched no known grammar"}
}

// mysqlHandshake reads the server greeting packet: a 3-byte length, sequence
// zero, protocol version 10 and a NUL-terminated server version.
func mysqlHandshake(raw []byte) (bannerID, bool) {
	if len(raw) < 6 || raw[3] != 0 || raw[4] != 10 {
		return bannerID{}, false
	}
	length := int(raw[0]) | int(raw[1])<<8 | int(raw[2])<<16
	if length < 2 || length > 1<<16 {
		return bannerID{}, false
	}
	end := bytes.IndexByte(raw[5:], 0)
	if end <= 0 {
		return bannerID{}, false
	}
	version := string(raw[5 : 5+end])
	for _, r := range version {
		if r < 0x20 || r > 0x7e {
			return bannerID{}, false
		}
	}
	product := "MySQL"
	if strings.Contains(strings.ToLower(version), "mariadb") {
		product = "MariaDB"
	}
	return bannerID{Protocol: "MySQL protocol 10", Product: product, Version: version, Confidence: "high", Basis: "MySQL server handshake packet"}, true
}

func describeBanner(res *ProbeResult, id bannerID) {
	res.fact("Protocol", id.Protocol, BasisInferred)
	res.fact("Identification confidence", id.Confidence+" — "+id.Basis, BasisInferred)
	if id.Product != "" {
		product := id.Product
		if id.Version != "" {
			product += " " + id.Version
		}
		res.fact("Software (self-reported)", product, BasisSelfReported)
		res.Records = append(res.Records, product)
	}
	res.Facts = append(res.Facts, id.Details...)
	res.Records = append(res.Records, id.Protocol)
	res.Limitations = append(res.Limitations, "A banner is whatever the service chooses to send; software names and versions can be hidden, changed or forged, and do not show patch level.")
}

// SSHTrustedKey is one fingerprint an operator saved for a host.
type SSHTrustedKey struct {
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
}

// SSHTrust is the saved set for one host and port.
type SSHTrust struct {
	Target  string          `json:"target"`
	Keys    []SSHTrustedKey `json:"keys"`
	Source  string          `json:"source"`
	SavedAt time.Time       `json:"savedAt"`
	SavedBy string          `json:"savedBy"`
}

var (
	sshKeyTypes = map[string]bool{
		"ssh-ed25519": true, "ssh-rsa": true, "ecdsa-sha2-nistp256": true, "ecdsa-sha2-nistp384": true,
		"ecdsa-sha2-nistp521": true, "sk-ssh-ed25519@openssh.com": true, "sk-ecdsa-sha2-nistp256@openssh.com": true, "ssh-dss": true,
	}
	sshFingerprintRe = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`)
)

// ValidateSSHTrust normalises a trust request. Fingerprints are SHA-256 in
// the form ssh-keygen -l prints, so a value copied from a console compares.
func ValidateSSHTrust(target string, port int, keys []SSHTrustedKey) (string, []SSHTrustedKey, error) {
	if !ValidTarget(target) {
		return "", nil, fmt.Errorf("target must be a hostname or IP address")
	}
	if port == 0 {
		port = 22
	}
	if port < 1 || port > 65535 {
		return "", nil, fmt.Errorf("port must be between 1 and 65535")
	}
	if len(keys) == 0 || len(keys) > 8 {
		return "", nil, fmt.Errorf("save between one and eight host key fingerprints")
	}
	seen := map[string]bool{}
	out := make([]SSHTrustedKey, 0, len(keys))
	for _, k := range keys {
		k.Type, k.Fingerprint = strings.TrimSpace(k.Type), strings.TrimSpace(k.Fingerprint)
		if !sshKeyTypes[k.Type] {
			return "", nil, fmt.Errorf("unsupported host key type %q", k.Type)
		}
		if !sshFingerprintRe.MatchString(k.Fingerprint) {
			return "", nil, fmt.Errorf("fingerprints look like SHA256: followed by 43 base64 characters")
		}
		if seen[k.Type] {
			return "", nil, fmt.Errorf("save one fingerprint per key type")
		}
		seen[k.Type] = true
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return SSHTrustTarget(target, port), out, nil
}

// SSHTrustTarget is the canonical key a trust entry is saved under.
func SSHTrustTarget(target string, port int) string {
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(target), "."))
	if a, err := netip.ParseAddr(host); err == nil {
		host = a.Unmap().String()
	}
	if port == 0 {
		port = 22
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// ObservedSSHKeys reads the keys an SSH scan recorded.
func ObservedSSHKeys(res *ProbeResult) []SSHTrustedKey {
	var out []SSHTrustedKey
	if res == nil {
		return out
	}
	for _, record := range res.Records {
		keyType, fp, ok := strings.Cut(record, " ")
		if ok && sshKeyTypes[keyType] && sshFingerprintRe.MatchString(fp) {
			out = append(out, SSHTrustedKey{Type: keyType, Fingerprint: fp})
		}
	}
	return out
}

// CompareSSHKeys sets the scan's verdict from a saved trust entry. Without
// one, a scan reads keys and authenticates nothing.
func CompareSSHKeys(res *ProbeResult, trust *SSHTrust) {
	if res == nil || res.Tool != "ssh" {
		return
	}
	observed := ObservedSSHKeys(res)
	res.Limitations = append(res.Limitations, "A match shows the server presents the key saved earlier; it is only as trustworthy as the way that fingerprint was first obtained. Check a first fingerprint against the server's console or provider records.")
	if len(observed) == 0 {
		return
	}
	if trust == nil || len(trust.Keys) == 0 {
		res.fact("Saved trust", "none for "+res.Target, BasisUnknown)
		res.Verdict = ProbeUnknown
		res.Summary = fmt.Sprintf("Read %d host key(s). None is saved as trusted for this host and port, so this does not authenticate the server.", len(observed))
		return
	}
	saved := map[string]string{}
	for _, k := range trust.Keys {
		saved[k.Type] = k.Fingerprint
	}
	res.fact("Saved trust", fmt.Sprintf("%d fingerprint(s) %s on %s by %s", len(trust.Keys), map[string]string{"observed": "trusted from a scan", "entered": "entered by hand"}[trust.Source], trust.SavedAt.UTC().Format("2006-01-02 15:04 UTC"), trust.SavedBy), BasisConfigured)
	table := ProbeTable{ID: "trust", Title: "Host keys against saved trust", Columns: []string{"Key type", "Offered fingerprint", "Saved fingerprint", "Comparison"}}
	matched, changed := 0, 0
	offered := map[string]bool{}
	for _, k := range observed {
		offered[k.Type] = true
		want, ok := saved[k.Type]
		state := "no saved key of this type"
		switch {
		case ok && want == k.Fingerprint:
			state, matched = "matches", matched+1
		case ok:
			state, changed = "DIFFERS", changed+1
			res.finding("key-changed-"+k.Type, "critical", "The "+k.Type+" host key differs from the saved fingerprint",
				"Offered "+k.Fingerprint+"; saved "+want+". Either the server's key was replaced (reinstall, rotation) or something is intercepting the connection. Verify out of band before trusting the new key.", "Server administrator")
		}
		table.Rows = append(table.Rows, []string{k.Type, k.Fingerprint, nonEmptyOr(want, "—"), state})
	}
	for _, k := range trust.Keys {
		if !offered[k.Type] {
			table.Rows = append(table.Rows, []string{k.Type, "not offered", k.Fingerprint, "saved but not offered"})
		}
	}
	res.Tables = append(res.Tables, table)
	switch {
	case changed > 0:
		res.Verdict = ProbeFindings
		res.Summary = fmt.Sprintf("%d host key(s) differ from the fingerprints saved for this server.", changed)
	case matched > 0:
		res.Verdict = ProbeOK
		res.Summary = fmt.Sprintf("%d offered host key(s) match the saved fingerprints.", matched)
	default:
		res.Verdict = ProbeUnknown
		res.Summary = "None of the offered key types has a saved fingerprint to compare with."
	}
}
