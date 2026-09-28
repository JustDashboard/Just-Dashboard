package proxysvc

import (
	"bytes"
	"context"
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ocsp"
)

// Revocation is whether the leaf's issuer still stands behind it, as its
// OCSP responder or CRL says, and where that answer came from.
//
// Status is "good", "revoked", "unknown" (the issuer's answer does not know
// the certificate) or "unchecked" (no answer could be had or trusted; Detail
// says why). An answer is believed only once its signature checks against
// the issuing certificate, so a responder or CRL the server's operator could
// forge is never what the report goes by.
type Revocation struct {
	Status string `json:"status"`
	// Method is "stapled" (the OCSP answer the server attached), "ocsp" or
	// "crl".
	Method     string     `json:"method,omitempty"`
	Source     string     `json:"source,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	ThisUpdate *time.Time `json:"thisUpdate,omitempty"`
	NextUpdate *time.Time `json:"nextUpdate,omitempty"`
	Detail     string     `json:"detail,omitempty"`
	// Cached is an answer reused from an earlier scan: it holds until the
	// issuer's own NextUpdate, when a fresh one would say nothing new.
	Cached bool `json:"cached,omitempty"`
}

// maxCRLBytes bounds a CRL download. The largest public ones run to a few
// megabytes; anything past this is a server trying to hold the scan.
const maxCRLBytes = 20 << 20

// maxOCSPBytes bounds an OCSP answer, which is a single signed response.
const maxOCSPBytes = 64 << 10

// errPrivateRevocationSource is an OCSP or CRL URL that resolved to this
// machine or its private network. Those URLs are written by whoever issued
// the scanned certificate, so following one inward would let any site
// scanned make this server send requests to the services behind it.
var errPrivateRevocationSource = errors.New("not requested: an address on this machine or its private network")

// revocationCache keeps each certificate's answer until the issuer's
// NextUpdate. Keyed by the issuer's key and the serial, since a serial is
// only unique under its issuer.
var revocationCache = struct {
	sync.Mutex
	entries map[string]Revocation
}{entries: map[string]Revocation{}}

// maxRevocationCache bounds the cache; a dashboard scans a handful of names.
const maxRevocationCache = 512

func revocationKey(leaf, issuer *x509.Certificate) string {
	sum := sha256.Sum256(issuer.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:]) + "/" + leaf.SerialNumber.Text(16)
}

func cachedRevocation(key string, now time.Time) (Revocation, bool) {
	revocationCache.Lock()
	defer revocationCache.Unlock()
	entry, ok := revocationCache.entries[key]
	if !ok || entry.NextUpdate == nil || !now.Before(*entry.NextUpdate) {
		delete(revocationCache.entries, key)
		return Revocation{}, false
	}
	entry.Cached = true
	return entry, true
}

// storeRevocation keeps an answer that says when it will next change. One
// without a NextUpdate means the issuer always has something newer, so it is
// asked again next time.
func storeRevocation(key string, answer Revocation, now time.Time) {
	if answer.NextUpdate == nil || !now.Before(*answer.NextUpdate) ||
		(answer.Status != "good" && answer.Status != "revoked") {
		return
	}
	revocationCache.Lock()
	defer revocationCache.Unlock()
	if len(revocationCache.entries) >= maxRevocationCache {
		for k, e := range revocationCache.entries {
			if !now.Before(*e.NextUpdate) {
				delete(revocationCache.entries, k)
			}
		}
	}
	if len(revocationCache.entries) >= maxRevocationCache {
		for k := range revocationCache.entries {
			delete(revocationCache.entries, k)
			break
		}
	}
	revocationCache.entries[key] = answer
}

// CheckRevocation asks whether leaf is revoked: the stapled OCSP answer
// first, since it is what browsers read, then each OCSP responder the leaf
// names, then each CRL. The first answer whose signature checks is the one
// reported; failures on the way are kept for Detail when none does.
func CheckRevocation(ctx context.Context, leaf, issuer *x509.Certificate, stapled []byte) *Revocation {
	if issuer == nil {
		return &Revocation{Status: "unchecked",
			Detail: "The issuing certificate was neither sent nor found in the trust store, so no revocation answer could be verified."}
	}
	now := time.Now()
	if len(stapled) > 0 {
		answer, err := readOCSP(stapled, leaf, issuer, now)
		if err == nil {
			answer.Method = "stapled"
			return answer
		}
	}
	key := revocationKey(leaf, issuer)
	if answer, ok := cachedRevocation(key, now); ok {
		return &answer
	}
	if len(leaf.OCSPServer) == 0 && len(leaf.CRLDistributionPoints) == 0 {
		return &Revocation{Status: "unchecked",
			Detail: "The certificate names no OCSP responder and no CRL, so there is nowhere to ask."}
	}

	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	client := revocationClient()
	var failures []string
	for _, source := range leaf.OCSPServer {
		answer, err := askOCSP(ctx, client, source, leaf, issuer, now)
		if err != nil {
			failures = append(failures, "OCSP "+source+": "+err.Error())
			continue
		}
		answer.Method, answer.Source = "ocsp", source
		storeRevocation(key, *answer, now)
		return answer
	}
	for _, source := range leaf.CRLDistributionPoints {
		answer, err := askCRL(ctx, client, source, leaf, issuer, now)
		if err != nil {
			failures = append(failures, "CRL "+source+": "+err.Error())
			continue
		}
		answer.Method, answer.Source = "crl", source
		storeRevocation(key, *answer, now)
		return answer
	}
	return &Revocation{Status: "unchecked", Detail: strings.Join(failures, "; ")}
}

// revocationClient reaches public addresses only, checked on the address
// dialled after DNS, and follows no redirects: OCSP and CRL URLs are the
// issuer's to write, and a redirect is somebody else's.
func revocationClient() *http.Client {
	dialer := &net.Dialer{Timeout: 8 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil || !IsPublicAddress(net.ParseIP(host)) {
			return errPrivateRevocationSource
		}
		return nil
	}}
	return &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			DialContext:       dialer.DialContext,
			DisableKeepAlives: true,
		},
	}
}

// revocationURL accepts the plain http URLs OCSP and CRLs are published at.
// ldap:// distribution points exist and are not spoken here.
func revocationURL(source string) (*url.URL, error) {
	u, err := url.Parse(source)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%s URLs are not fetched", u.Scheme)
	}
	return u, nil
}

func fetchLimited(client *http.Client, req *http.Request, limit int64) ([]byte, error) {
	req.Header.Set("User-Agent", scanUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New(requestError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("answered %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("larger than %d MB, not read", limit>>20)
	}
	return body, nil
}

func askOCSP(ctx context.Context, client *http.Client, source string, leaf, issuer *x509.Certificate, now time.Time) (*Revocation, error) {
	u, err := revocationURL(source)
	if err != nil {
		return nil, err
	}
	// SHA-1 is what every responder accepts for the CertID; it names the
	// certificate here and signs nothing.
	body, err := ocsp.CreateRequest(leaf, issuer, &ocsp.RequestOptions{Hash: crypto.SHA1})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/ocsp-request")
	raw, err := fetchLimited(client, req, maxOCSPBytes)
	if err != nil {
		return nil, err
	}
	return readOCSP(raw, leaf, issuer, now)
}

// readOCSP believes an OCSP answer only when it is signed by the issuer or a
// responder the issuer delegated to, is about this certificate, and has not
// passed its NextUpdate.
func readOCSP(raw []byte, leaf, issuer *x509.Certificate, now time.Time) (*Revocation, error) {
	resp, err := ocsp.ParseResponseForCert(raw, leaf, issuer)
	if err != nil {
		return nil, err
	}
	if !resp.NextUpdate.IsZero() && now.After(resp.NextUpdate) {
		return nil, fmt.Errorf("the answer expired at %s", resp.NextUpdate.UTC().Format(time.RFC3339))
	}
	out := &Revocation{ThisUpdate: timeRef(resp.ThisUpdate), NextUpdate: timeRef(resp.NextUpdate)}
	switch resp.Status {
	case ocsp.Good:
		out.Status = "good"
	case ocsp.Revoked:
		out.Status = "revoked"
		out.RevokedAt = timeRef(resp.RevokedAt)
		out.Reason = revocationReason(resp.RevocationReason)
	default:
		out.Status = "unknown"
		out.Detail = "The responder does not know this certificate."
	}
	return out, nil
}

func askCRL(ctx context.Context, client *http.Client, source string, leaf, issuer *x509.Certificate, now time.Time) (*Revocation, error) {
	u, err := revocationURL(source)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	raw, err := fetchLimited(client, req, maxCRLBytes)
	if err != nil {
		return nil, err
	}
	return readCRL(raw, leaf, issuer, now)
}

// readCRL believes a CRL only when the issuer signed it and it is current.
func readCRL(raw []byte, leaf, issuer *x509.Certificate, now time.Time) (*Revocation, error) {
	list, err := x509.ParseRevocationList(raw)
	if err != nil {
		return nil, err
	}
	if err := list.CheckSignatureFrom(issuer); err != nil {
		return nil, fmt.Errorf("not signed by the issuer: %w", err)
	}
	if !list.NextUpdate.IsZero() && now.After(list.NextUpdate) {
		return nil, fmt.Errorf("the list expired at %s", list.NextUpdate.UTC().Format(time.RFC3339))
	}
	out := &Revocation{Status: "good", ThisUpdate: timeRef(list.ThisUpdate), NextUpdate: timeRef(list.NextUpdate)}
	for _, entry := range list.RevokedCertificateEntries {
		if entry.SerialNumber.Cmp(leaf.SerialNumber) == 0 {
			out.Status = "revoked"
			out.RevokedAt = timeRef(entry.RevocationTime)
			out.Reason = revocationReason(entry.ReasonCode)
			break
		}
	}
	return out, nil
}

func timeRef(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

// revocationReason names RFC 5280's CRLReason codes.
func revocationReason(code int) string {
	switch code {
	case ocsp.KeyCompromise:
		return "key compromise"
	case ocsp.CACompromise:
		return "CA compromise"
	case ocsp.AffiliationChanged:
		return "affiliation changed"
	case ocsp.Superseded:
		return "superseded"
	case ocsp.CessationOfOperation:
		return "cessation of operation"
	case ocsp.CertificateHold:
		return "certificate hold"
	case ocsp.PrivilegeWithdrawn:
		return "privilege withdrawn"
	case ocsp.AACompromise:
		return "AA compromise"
	}
	return ""
}
