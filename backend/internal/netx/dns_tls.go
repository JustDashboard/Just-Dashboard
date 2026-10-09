package netx

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// The DNS over TLS setting says what systemd-resolved will insist on; it does
// not say what the servers present. This check opens the dashboard's own TLS
// session to each configured server on its DoT port, verifies the certificate
// against the host's trust store for the configured name, and asks for the
// root's NS records — a question that discloses nothing private — to show the
// server answers DNS over that session. It is independent certificate
// evidence, not proof of the transport resolved used for a given answer.

// DNSTLSCheck is one server's certificate as this host sees it now.
type DNSTLSCheck struct {
	Server  string `json:"server"`
	Scope   string `json:"scope"`
	Address string `json:"address"`
	TLSName string `json:"tlsName"`
	// State is trusted (the chain verifies for the name and the server
	// answered DNS over the session), untrusted (the certificate failed
	// verification), no-answer (trusted, but no DNS reply came back) or
	// unreachable (no TLS session at all).
	State       string     `json:"state"`
	Version     string     `json:"version,omitempty"`
	Subject     string     `json:"subject,omitempty"`
	Issuer      string     `json:"issuer,omitempty"`
	Names       []string   `json:"names,omitempty"`
	NotAfter    *time.Time `json:"notAfter,omitempty"`
	Fingerprint string     `json:"fingerprint,omitempty"`
	ChainLength int        `json:"chainLength,omitempty"`
	LatencyMS   float64    `json:"latencyMs"`
	Error       string     `json:"error,omitempty"`
}

// DNSTLSReport is every check, and the servers no check could be made for.
type DNSTLSReport struct {
	CheckedAt   time.Time           `json:"checkedAt"`
	Checks      []DNSTLSCheck       `json:"checks"`
	Omitted     []LookupDestination `json:"omitted"`
	Limitations []string            `json:"limitations"`
}

// maxDNSTLSChecks bounds how many sessions one press opens.
const maxDNSTLSChecks = 16

type dnsTLSCandidate struct {
	server, scope, address, tlsName string
}

// dnsTLSInventory is every configured or preset server that names a TLS
// identity. Only these can be checked: a caller selects among them and cannot
// name an address of its own.
func dnsTLSInventory(rv ResolvedView, managed ManagedDNS) ([]dnsTLSCandidate, []LookupDestination) {
	out, omitted := []dnsTLSCandidate{}, []LookupDestination{}
	seen := map[string]bool{}
	add := func(entries []string, scope, iface string) {
		for _, entry := range entries {
			if seen[entry] {
				continue
			}
			seen[entry] = true
			sv, err := parseDNSServer(entry)
			if err != nil {
				omitted = append(omitted, LookupDestination{Server: entry, Label: scope, Reason: "unsupported or invalid resolver address"})
				continue
			}
			if sv.tlsName == "" {
				omitted = append(omitted, LookupDestination{Server: entry, Label: scope, Reason: "no server name after #: a DNS-over-TLS certificate cannot be authenticated without one, so opportunistic mode encrypts without checking it"})
				continue
			}
			if sv.addr.IsLinkLocalUnicast() && sv.iface == "" {
				sv.iface = iface
			}
			if sv.addr.IsLinkLocalUnicast() && sv.iface == "" {
				omitted = append(omitted, LookupDestination{Server: entry, Label: scope, Reason: "link-local resolver has no interface scope"})
				continue
			}
			out = append(out, dnsTLSCandidate{server: entry, scope: scope, address: dnsTLSAddress(sv.lookupServer()), tlsName: sv.tlsName})
		}
	}
	if rv.Active {
		add(rv.Global.Servers, "global", "")
		add(rv.Global.Fallback, "global fallback", "")
		for _, l := range rv.Links {
			add(l.Servers, l.Name, l.Name)
		}
	}
	add(managed.Servers, "this page's drop-in", "")
	add(managed.Fallback, "this page's drop-in fallback", "")
	for _, p := range DNSPresets() {
		add(p.TLSServers, p.Name, "")
	}
	return out, omitted
}

// CheckDNSTLS checks the named servers, or every configured one that names a
// TLS identity when servers is empty. Presets are checked only when named.
func (s *Service) CheckDNSTLS(ctx context.Context, servers []string) (*DNSTLSReport, error) {
	if len(servers) > maxDNSTLSChecks {
		return nil, fmt.Errorf("select at most %d servers", maxDNSTLSChecks)
	}
	rv := s.readResolved(ctx, false)
	inventory, omitted := dnsTLSInventory(rv, readManagedDNS(s.paths.Resolved))
	report := &DNSTLSReport{CheckedAt: time.Now().UTC(), Checks: []DNSTLSCheck{}, Omitted: []LookupDestination{}, Limitations: []string{
		"These are the dashboard's own TLS sessions from this host to each server, verified against this host's trust store for the configured name. They show what the server presents now, not which transport systemd-resolved used for a particular answer.",
		"The DNS question asked over each session is for the root's NS records, which carries no private name.",
	}}
	presets := map[string]bool{}
	for _, p := range DNSPresets() {
		for _, entry := range p.TLSServers {
			presets[entry] = true
		}
	}
	var chosen []dnsTLSCandidate
	if len(servers) == 0 {
		for _, c := range inventory {
			if !presets[c.server] || c.scope != presetName(c.server) {
				chosen = append(chosen, c)
			}
		}
		for _, o := range omitted {
			if !presets[o.Server] {
				report.Omitted = append(report.Omitted, o)
			}
		}
		if len(chosen) > maxDNSTLSChecks {
			for _, c := range chosen[maxDNSTLSChecks:] {
				report.Omitted = append(report.Omitted, LookupDestination{Server: c.server, Label: c.scope, Reason: fmt.Sprintf("more than %d servers name a TLS identity; select this one to check it", maxDNSTLSChecks)})
			}
			chosen = chosen[:maxDNSTLSChecks]
		}
	} else {
		by := map[string]dnsTLSCandidate{}
		for _, c := range inventory {
			by[c.server] = c
		}
		reasons := map[string]LookupDestination{}
		for _, o := range omitted {
			reasons[o.Server] = o
		}
		seen := map[string]bool{}
		for _, server := range servers {
			server = strings.TrimSpace(server)
			if seen[server] {
				continue
			}
			seen[server] = true
			if c, ok := by[server]; ok {
				chosen = append(chosen, c)
				continue
			}
			if o, ok := reasons[server]; ok {
				report.Omitted = append(report.Omitted, o)
				continue
			}
			return nil, fmt.Errorf("%s is not a configured or preset DNS-over-TLS server", server)
		}
	}
	report.Checks = make([]DNSTLSCheck, len(chosen))
	var wg sync.WaitGroup
	for i, c := range chosen {
		wg.Add(1)
		go func() {
			defer wg.Done()
			report.Checks[i] = checkDNSTLS(ctx, c)
		}()
	}
	wg.Wait()
	sort.SliceStable(report.Checks, func(i, j int) bool { return report.Checks[i].Scope < report.Checks[j].Scope })
	return report, nil
}

func presetName(entry string) string {
	for _, p := range DNSPresets() {
		for _, e := range p.TLSServers {
			if e == entry {
				return p.Name
			}
		}
	}
	return ""
}

// checkDNSTLS opens one session and asks one question over it.
func checkDNSTLS(ctx context.Context, c dnsTLSCandidate) DNSTLSCheck {
	out := DNSTLSCheck{Server: c.server, Scope: c.scope, Address: c.address, TLSName: c.tlsName}
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	start := time.Now()
	defer func() { out.LatencyMS = millis(time.Since(start)) }()
	raw, err := dnsTLSDial(ctx, "tcp", c.address)
	if err != nil {
		out.State, out.Error = "unreachable", lookupError(err)
		return out
	}
	conn := tls.Client(raw, &tls.Config{ServerName: c.tlsName, RootCAs: dnsTLSRoots, MinVersion: tls.VersionTLS12})
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := conn.HandshakeContext(ctx); err != nil {
		out.State, out.Error = dnsTLSFailure(err, c.tlsName)
		return out
	}
	state := conn.ConnectionState()
	out.Version = tls.VersionName(state.Version)
	if len(state.PeerCertificates) > 0 {
		leaf := state.PeerCertificates[0]
		out.Subject, out.Issuer = leaf.Subject.String(), leaf.Issuer.String()
		names := leaf.DNSNames
		if len(names) > 8 {
			names = names[:8]
		}
		out.Names = append([]string{}, names...)
		notAfter := leaf.NotAfter.UTC()
		out.NotAfter = &notAfter
		sum := sha256.Sum256(leaf.Raw)
		out.Fingerprint = hex.EncodeToString(sum[:])
	}
	if len(state.VerifiedChains) > 0 {
		out.ChainLength = len(state.VerifiedChains[0])
	}
	if err := askRootNS(conn); err != nil {
		out.State, out.Error = "no-answer", lookupError(err)
		return out
	}
	out.State = "trusted"
	return out
}

func dnsTLSFailure(err error, name string) (string, string) {
	var unknown x509.UnknownAuthorityError
	var host x509.HostnameError
	var invalid x509.CertificateInvalidError
	var verify *tls.CertificateVerificationError
	switch {
	case errors.As(err, &host):
		return "untrusted", "the certificate is not valid for " + name
	case errors.As(err, &unknown):
		return "untrusted", "the certificate is signed by an authority this host does not trust"
	case errors.As(err, &invalid):
		return "untrusted", "the certificate is invalid: " + invalid.Error()
	case errors.As(err, &verify):
		return "untrusted", verify.Error()
	}
	return "unreachable", lookupError(err)
}

// askRootNS asks ". NS" over an open session and checks that a response to
// that question comes back.
func askRootNS(conn net.Conn) error {
	builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 0x4a44, RecursionDesired: true})
	question := dnsmessage.Question{Name: dnsmessage.MustNewName("."), Type: dnsmessage.TypeNS, Class: dnsmessage.ClassINET}
	if err := builder.StartQuestions(); err != nil {
		return err
	}
	if err := builder.Question(question); err != nil {
		return err
	}
	packet, err := builder.Finish()
	if err != nil {
		return err
	}
	framed := binary.BigEndian.AppendUint16(nil, uint16(len(packet)))
	if _, err := conn.Write(append(framed, packet...)); err != nil {
		return err
	}
	var size [2]byte
	if _, err := io.ReadFull(conn, size[:]); err != nil {
		return err
	}
	response := make([]byte, binary.BigEndian.Uint16(size[:]))
	if _, err := io.ReadFull(conn, response); err != nil {
		return err
	}
	var parser dnsmessage.Parser
	header, err := parser.Start(response)
	if err != nil {
		return err
	}
	questions, err := parser.AllQuestions()
	if err != nil || header.ID != 0x4a44 || !header.Response || len(questions) != 1 || questions[0].Type != dnsmessage.TypeNS {
		return errors.New("the server answered a different question")
	}
	return nil
}
