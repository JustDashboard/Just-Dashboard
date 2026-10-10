package netvantage

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"
)

func approvedAddress(scope Scope, value, family string) bool {
	ip, e := netip.ParseAddr(value)
	if e != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() || ip.Unmap().Is4() != (family == "inet") {
		return false
	}
	for _, a := range scope.Addresses {
		if a == ip.Unmap().String() {
			return true
		}
	}
	return false
}
func ValidateJob(s SignedJob, manifest Manifest, now time.Time) error {
	if e := VerifyJob(s, manifest.ServerKey); e != nil {
		return e
	}
	j := s.Job
	if j.Version != 1 || j.VantageID != manifest.Vantage.ID || j.Request.VantageID != j.VantageID || !identityPattern.MatchString(j.ID) || !identityPattern.MatchString(j.Nonce) || j.ExpiresAt.Sub(j.IssuedAt) > 45*time.Second || !j.ExpiresAt.After(j.IssuedAt) || !j.ExpiresAt.After(now) || j.IssuedAt.After(now.Add(5*time.Second)) {
		return fmt.Errorf("the signed job identity or lease is invalid")
	}
	scope, e := scopeFor(manifest.Vantage.Scopes, j.Request)
	if e != nil {
		return e
	}
	if string(Marshal(scope)) != string(Marshal(j.Scope)) {
		return fmt.Errorf("the job changes the locally enrolled target scope")
	}
	return nil
}

// Probe uses the agent's own native resolver and a rootless Go dialer. It
// never invokes a program, enumerates ranges, follows redirects or disables
// certificate verification. One approved literal address is pinned for TCP
// and TLS; an unapproved DNS answer stops before any connection is attempted.
func Probe(ctx context.Context, job Job) *Result {
	return probe(ctx, job, net.DefaultResolver, nil)
}

func probe(ctx context.Context, job Job, resolver *net.Resolver, roots *x509.CertPool) *Result {
	ctx, cancel := context.WithDeadline(ctx, minTime(time.Now().Add(25*time.Second), job.ExpiresAt))
	defer cancel()
	now := time.Now().UTC()
	r := &Result{CheckID: job.ID, VantageID: job.VantageID, Nonce: job.Nonce, Request: job.Request, Target: job.Scope.Target, StartedAt: now, Addresses: []string{}, Stages: []Stage{}, Limitations: []string{"These are signed agent-reported measurements from the enrolled source at this time; signatures authenticate the agent, not its geographic placement.", "DNS uses this agent's native resolver for an absolute selected-family query; NSS/search/encrypted upstream policy is not traced.", "One TCP handshake and optional verified TLS handshake do not establish HTTP, authentication, UDP, global availability or a provider failure cause."}}
	defer func() { r.EndedAt = time.Now().UTC() }()
	stage := func(name, basis, state, detail string, start time.Time) {
		end := time.Now().UTC()
		r.Stages = append(r.Stages, Stage{Name: name, Basis: basis, State: state, Detail: bounded(detail, 2048), StartedAt: start, EndedAt: end, DurationMS: end.Sub(start).Milliseconds()})
	}
	start := time.Now().UTC()
	literal, e := netip.ParseAddr(job.Scope.Target)
	if e == nil {
		if approvedAddress(job.Scope, literal.Unmap().String(), job.Request.Family) {
			r.Addresses = []string{literal.Unmap().String()}
			r.Address = r.Addresses[0]
			stage("dns", "observed", "not_applicable", "The scoped target is a literal address; no DNS query was sent.", start)
		} else {
			stage("dns", "unknown", "refused", "The literal address is outside the enrolled family/address scope.", start)
		}
	} else {
		dnsctx, cancelDNS := context.WithTimeout(ctx, 5*time.Second)
		family := "ip4"
		if job.Request.Family == "inet6" {
			family = "ip6"
		}
		values, err := resolver.LookupIP(dnsctx, family, strings.TrimSuffix(job.Scope.Target, ".")+".")
		cancelDNS()
		if err != nil {
			stage("dns", "measured", "failed", err.Error(), start)
		} else {
			forbidden := false
			for _, ip := range values {
				a, ok := netip.AddrFromSlice(ip)
				if !ok {
					continue
				}
				value := a.Unmap().String()
				if len(r.Addresses) < 8 {
					r.Addresses = append(r.Addresses, value)
				}
				if !approvedAddress(job.Scope, value, job.Request.Family) {
					forbidden = true
				}
			}
			if forbidden {
				stage("dns", "measured", "refused", "The native DNS answer includes an address outside the exact enrolled allowlist; no TCP connection was sent.", start)
			} else if len(r.Addresses) == 0 {
				stage("dns", "measured", "failed", "The native resolver returned no selected-family address.", start)
			} else {
				r.Address = r.Addresses[0]
				stage("dns", "measured", "resolved", "The native selected-family DNS answer was pinned to one enrolled literal address.", start)
			}
		}
	}
	var connection net.Conn
	start = time.Now().UTC()
	if r.Address == "" {
		stage("tcp", "unknown", "skipped", "No approved selected destination exists.", start)
	} else {
		dialctx, cancelDial := context.WithTimeout(ctx, 6*time.Second)
		network := "tcp4"
		if job.Request.Family == "inet6" {
			network = "tcp6"
		}
		connection, e = (&net.Dialer{}).DialContext(dialctx, network, net.JoinHostPort(r.Address, fmt.Sprint(job.Request.Port)))
		cancelDial()
		if e != nil {
			stage("tcp", "measured", "failed", e.Error(), start)
		} else {
			defer connection.Close()
			source, _, _ := net.SplitHostPort(connection.LocalAddr().String())
			r.SourceAddress = source
			stage("tcp", "measured", "connected", "TCP connected from this enrolled source to this pinned address at this time.", start)
		}
	}
	start = time.Now().UTC()
	if !job.Request.TLS {
		stage("tls", "unknown", "not_requested", "A TLS handshake was not requested.", start)
	} else if connection == nil {
		stage("tls", "unknown", "skipped", "TCP did not connect; TLS was not attempted.", start)
	} else {
		tlsctx, cancelTLS := context.WithTimeout(ctx, 6*time.Second)
		secured := tls.Client(connection, &tls.Config{ServerName: job.Scope.Target, MinVersion: tls.VersionTLS12, RootCAs: roots})
		e = secured.HandshakeContext(tlsctx)
		cancelTLS()
		if e != nil {
			stage("tls", "measured", "failed", e.Error(), start)
		} else {
			peer := secured.ConnectionState().PeerCertificates[0]
			sum := sha256.Sum256(peer.Raw)
			r.Certificate = &Certificate{SHA256: hex.EncodeToString(sum[:]), Subject: bounded(peer.Subject.String(), 256), Issuer: bounded(peer.Issuer.String(), 256), ExpiresAt: peer.NotAfter}
			stage("tls", "measured", "verified", "TLS handshake and native trust/hostname verification succeeded on the same TCP connection.", start)
		}
	}
	return r
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func bounded(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
