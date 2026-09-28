package proxysvc

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Every address a name has, handshaken with one by one.
//
// A dialer takes the first record that answers, so a scan of a name with an
// A and an AAAA grades one of them and says nothing of the other. The other
// is where a stale record hides: an AAAA left pointing at the old server,
// still serving last year's certificate to every visitor on IPv6. Asking
// each address with the name in SNI is the only way to see it from here.

// AddressScan is one of a name's addresses and what it served.
type AddressScan struct {
	// Address is ip:port, and Family "IPv4" or "IPv6".
	Address string `json:"address"`
	Family  string `json:"family"`
	// Kind is whose address it is, as TLSScan.AddressKind.
	Kind      string `json:"kind"`
	Reachable bool   `json:"reachable"`
	Error     string `json:"error,omitempty"`
	// LegacyOnly is a server that took only the offer of every version.
	LegacyOnly  bool      `json:"legacyOnly,omitempty"`
	Negotiated  string    `json:"negotiated,omitempty"`
	Subject     string    `json:"subject,omitempty"`
	Issuer      string    `json:"issuer,omitempty"`
	NotAfter    time.Time `json:"notAfter,omitzero"`
	Fingerprint string    `json:"fingerprint,omitempty"`
}

type addressesResult struct {
	addresses []AddressScan
	err       error
}

// addressScanConcurrency bounds the handshakes in flight: a name with many
// records is usually a CDN's, and eight at once finishes within the scan's
// budget without looking like a flood to it.
const addressScanConcurrency = 8

// scanAddresses resolves domain and handshakes with each address on port,
// naming domain in SNI. lookup is the resolver, so a test can give a name
// addresses it does not have.
func scanAddresses(ctx context.Context, domain string, port int, starttls string, lookup lookupFunc) addressesResult {
	lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	ips, err := lookup(lookupCtx, domain)
	cancel()
	if err != nil {
		return addressesResult{err: err}
	}
	local, public := localAddresses(), hostAddresses()
	out := make([]AddressScan, len(ips))
	slots := make(chan struct{}, addressScanConcurrency)
	var wg sync.WaitGroup
	for i, ip := range ips {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			out[i] = scanAddress(ctx, ip.IP, domain, port, starttls, local, public)
		}()
	}
	wg.Wait()
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Family != out[j].Family {
			return out[i].Family == "IPv4"
		}
		return out[i].Address < out[j].Address
	})
	return addressesResult{addresses: out}
}

func scanAddress(ctx context.Context, ip net.IP, domain string, port int, starttls string, local, public []string) AddressScan {
	addr := net.JoinHostPort(ip.String(), strconv.Itoa(port))
	result := AddressScan{Address: addr, Family: "IPv6", Kind: whereConnected(addr, nil, local, public)}
	if ip.To4() != nil {
		result.Family = "IPv4"
	}
	conn, legacy, err := handshake(ctx, addr, domain, starttls)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	state := conn.ConnectionState()
	conn.Close()
	result.Reachable, result.LegacyOnly = true, legacy
	result.Negotiated = tls.VersionName(state.Version)
	if len(state.PeerCertificates) > 0 {
		leaf := state.PeerCertificates[0]
		result.Subject = leaf.Subject.CommonName
		if result.Subject == "" && len(leaf.DNSNames) > 0 {
			result.Subject = leaf.DNSNames[0]
		}
		result.Issuer = leaf.Issuer.CommonName
		result.NotAfter = leaf.NotAfter.UTC()
		sum := sha256.Sum256(leaf.Raw)
		result.Fingerprint = colonHex(sum[:])
	}
	return result
}

// collectAddresses waits for the address scan, when one was started, and
// records it with its finding.
func collectAddresses(scan *TLSScan, addresses chan addressesResult) {
	if addresses == nil {
		return
	}
	result := <-addresses
	if result.err != nil {
		scan.AddressesError = result.err.Error()
		return
	}
	scan.Addresses = result.addresses
	if finding, ok := addressMismatch(scan.Domain, result.addresses); ok {
		scan.Findings = append(scan.Findings, finding)
	}
}

// addressMismatch is the finding for addresses that answered with different
// certificates. It is a warning and caps nothing: the grade is of the answer
// the scan got, and which answer a visitor gets depends on their network.
func addressMismatch(domain string, addresses []AddressScan) (ScanFinding, bool) {
	groups := map[string][]AddressScan{}
	order := []string{}
	for _, a := range addresses {
		if !a.Reachable || a.Fingerprint == "" {
			continue
		}
		if _, seen := groups[a.Fingerprint]; !seen {
			order = append(order, a.Fingerprint)
		}
		groups[a.Fingerprint] = append(groups[a.Fingerprint], a)
	}
	if len(order) < 2 {
		return ScanFinding{}, false
	}
	parts := make([]string, 0, len(order))
	for _, fp := range order {
		group := groups[fp]
		names := make([]string, 0, len(group))
		for _, a := range group {
			names = append(names, a.Address)
		}
		first := group[0]
		parts = append(parts, strings.Join(names, ", ")+" serve "+first.Subject+
			" from "+first.Issuer+", valid until "+first.NotAfter.Format("2 Jan 2006"))
	}
	return ScanFinding{
		ID:    "tls.address-mismatch",
		Level: "warning",
		Title: "Addresses serve different certificates",
		Detail: domain + " has " + strconv.Itoa(len(order)) + " certificates depending on the address a visitor reaches: " +
			strings.Join(parts, "; ") + ".",
		Advice: "Usually one record is stale — an AAAA or a second A still pointing at an old server, or a machine that missed a renewal or a reload. Point every A and AAAA record at servers that hold the current certificate, or bring the other one up to date.",
	}, true
}
