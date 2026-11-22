package accountlink

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// The app domain endpoints: app-domains-protocol.md in the site's repository,
// https://github.com/JustDashboard/frontend/blob/main/docs/app-domains-protocol.md.
//
// They are new paths of the link protocol, signed like the heartbeat with the
// same identity and the same sequence counter, and only ever sent on an active
// account link. Every number is taken from the persisted counter, committed
// before the request is built (kv.nextSequence), and a request the site
// refuses with replayed_sequence is sent again with a fresh number
// (Client.call). That is how the heartbeat, the command poll and these calls
// share one counter while they run at the same time: two of them can still
// reach the site in the opposite order to the numbers they took, and the one
// refused for it has not been acted on, so sending it again is safe. Nothing
// here serialises them further; serialising would make a claim wait behind a
// long poll that holds its connection for most of a minute.

// DomainInfo is GET /domain's answer: whether the site offers app domains, and
// this server's, if it holds one.
type DomainInfo struct {
	Available bool `json:"available"`
	// Reason says, when Available is false, why, in the site's words.
	Reason *string `json:"reason"`
	// Zone is nil when the site offers nothing.
	Zone       *string `json:"zone"`
	MaxHandles int     `json:"maxHandles"`
	Domain     *Domain `json:"domain"`
	// Offer is the handle the account chose for this server, when the link
	// was approved or later on the site, that the server has not claimed:
	// nil when there is none. It is never set beside Domain.
	Offer *string `json:"offer"`
}

// Domain is a handle this server holds on the site.
type Domain struct {
	Handle   string `json:"handle"`
	Hostname string `json:"hostname"`
	Address  string `json:"address"`
	// Status is "active" or "suspended".
	Status string `json:"status"`
	// Reason is a suspension's stated reason.
	Reason    *string `json:"reason"`
	CreatedAt int64   `json:"createdAt"`
	UpdatedAt int64   `json:"updatedAt"`
}

// putDomainBody and challengeBody are the request bodies, in the field order
// the protocol's test vectors are signed over.
type putDomainBody struct {
	Handle  string `json:"handle"`
	Address string `json:"address"`
}

type challengeBody struct {
	Value string `json:"value"`
}

// ChallengeSet is the TXT values live at a server's _acme-challenge name.
type ChallengeSet struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// The statuses a v1 site gives a domain.
const (
	DomainActive    = "active"
	DomainSuspended = "suspended"
)

// DomainError is the site refusing an app domain call, with a sentence for
// the operator. Code is the protocol's (section 5), or the link protocol's
// own for a refusal of the signature or the clock.
type DomainError struct {
	Status  int
	Code    string
	Message string
	err     error
}

func (e *DomainError) Error() string { return e.Message }
func (e *DomainError) Unwrap() error { return e.err }

// ErrDomainsNotAccountLink is a domain call on an instance of the engine that
// is not the account link: only just-dashboard.com hands out names.
var ErrDomainsNotAccountLink = errors.New("app domains are reached only through the just-dashboard.com account link")

var (
	handleRe    = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,18})[a-z0-9]$`)
	challengeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
)

// ValidHandle applies the protocol's handle syntax (section 3) to a handle
// already lowercased: 3 to 20 of a-z, 0-9 and -, a letter or digit at each end,
// and no "--".
func ValidHandle(handle string) bool {
	return handleRe.MatchString(handle) && !strings.Contains(handle, "--")
}

// ValidChallenge is the shape of a DNS-01 value certbot hands a hook: 43
// base64url characters, the SHA-256 of the key authorization.
func ValidChallenge(value string) bool { return challengeRe.MatchString(value) }

// domainLink is the stored link and the client a domain call is sent with,
// once the link is active and may be used.
func (s *Service) domainLink(ctx context.Context) (State, *Client, error) {
	if s.p.KeyPrefix != AccountProfile.KeyPrefix {
		return State{}, nil, ErrDomainsNotAccountLink
	}
	if !s.enabled {
		return State{}, nil, ErrDisabled
	}
	st, linked, err := s.kv.state(ctx)
	if err != nil {
		return State{}, nil, err
	}
	if !linked {
		return State{}, nil, ErrNotLinked
	}
	if err := s.admit(ctx, st); err != nil {
		return State{}, nil, err
	}
	id, _, err := loadIdentity(ctx, s.kv, s.sealer)
	if err != nil || id == nil {
		return State{}, nil, fmt.Errorf("link identity: %w", firstErr(err, ErrIdentityUnreadable))
	}
	return st, s.linkClient(st, id), nil
}

// domainCall sends one signed domain request and reads the answer. A 410 is
// the server removed on the site, handled as every other signed request
// handles it: the link is cleared, and the call reports not linked.
func (s *Service) domainCall(ctx context.Context, method, path string, body any, out any) error {
	st, c, err := s.domainLink(ctx)
	if err != nil {
		return err
	}
	var raw json.RawMessage
	var target any
	if out != nil {
		target = &raw
	}
	_, err = c.call(ctx, method, c.base+path, body, st.ServerID, 0, target)
	if err != nil {
		if s.linkEnded(ctx, st, err) {
			return ErrNotLinked
		}
		return domainError(err)
	}
	if out == nil {
		return nil
	}
	if err := decodeStrict(raw, out); err != nil {
		return fmt.Errorf("the site's response is not what the app domain protocol describes: %w", err)
	}
	return nil
}

// GetDomain asks the site whether it offers app domains and which this server
// holds (protocol 4.1).
func (s *Service) GetDomain(ctx context.Context) (DomainInfo, error) {
	var out DomainInfo
	var raw json.RawMessage
	if err := s.domainCall(ctx, http.MethodGet, "/domain", nil, &raw); err != nil {
		return out, err
	}
	if err := requireKeys(raw, "available", "reason", "zone", "maxHandles", "domain", "offer"); err != nil {
		return out, err
	}
	if err := decodeStrict(raw, &out); err != nil {
		return out, fmt.Errorf("the site's response is not what the app domain protocol describes: %w", err)
	}
	if out.Domain != nil {
		var wrapper struct {
			Domain json.RawMessage `json:"domain"`
		}
		_ = json.Unmarshal(raw, &wrapper)
		if err := checkDomain(wrapper.Domain, out.Domain); err != nil {
			return out, err
		}
	}
	if out.Offer != nil && (!ValidHandle(*out.Offer) || out.Domain != nil) {
		return out, errors.New("the site's response is not what the app domain protocol describes: the offer is not a handle, or is given beside a domain")
	}
	if out.Available && (out.Zone == nil || *out.Zone == "") {
		return out, errors.New("the site's response is not what the app domain protocol describes: available without a zone")
	}
	return out, nil
}

// PutDomain claims handle at address, changes this server's handle to it, or
// moves its records to address (protocol 4.2).
func (s *Service) PutDomain(ctx context.Context, handle, address string) (Domain, error) {
	var raw json.RawMessage
	if err := s.domainCall(ctx, http.MethodPut, "/domain", putDomainBody{handle, address}, &raw); err != nil {
		return Domain{}, err
	}
	var out struct {
		Domain json.RawMessage `json:"domain"`
	}
	if err := decodeStrict(raw, &out); err != nil || len(bytes.TrimSpace(out.Domain)) == 0 {
		return Domain{}, fmt.Errorf("the site's response is not what the app domain protocol describes: %v", firstErr(err, errors.New("domain is missing")))
	}
	var d Domain
	if err := decodeStrict(out.Domain, &d); err != nil {
		return Domain{}, fmt.Errorf("the site's response is not what the app domain protocol describes: %w", err)
	}
	if err := checkDomain(out.Domain, &d); err != nil {
		return Domain{}, err
	}
	return d, nil
}

// DeleteDomain releases this server's handle (protocol 4.3). It is not an
// error when there was nothing to release.
func (s *Service) DeleteDomain(ctx context.Context) error {
	return s.domainCall(ctx, http.MethodDelete, "/domain", nil, nil)
}

// AddChallenge publishes one DNS-01 value at this server's _acme-challenge
// name (protocol 4.4).
func (s *Service) AddChallenge(ctx context.Context, value string) (ChallengeSet, error) {
	if !ValidChallenge(value) {
		return ChallengeSet{}, &DomainError{Status: http.StatusBadRequest, Code: "invalid_challenge", Message: "A DNS challenge value is 43 base64url characters; certbot gave something else."}
	}
	var raw json.RawMessage
	if err := s.domainCall(ctx, http.MethodPost, "/domain/challenges", challengeBody{value}, &raw); err != nil {
		return ChallengeSet{}, err
	}
	var out ChallengeSet
	if err := requireKeys(raw, "name", "values"); err != nil {
		return out, err
	}
	if err := decodeStrict(raw, &out); err != nil {
		return out, fmt.Errorf("the site's response is not what the app domain protocol describes: %w", err)
	}
	return out, nil
}

// RemoveChallenge takes one value down again (protocol 4.5). Removing a value
// that is not live is not an error.
func (s *Service) RemoveChallenge(ctx context.Context, value string) error {
	if !ValidChallenge(value) {
		return &DomainError{Status: http.StatusBadRequest, Code: "invalid_challenge", Message: "A DNS challenge value is 43 base64url characters; certbot gave something else."}
	}
	return s.domainCall(ctx, http.MethodPost, "/domain/challenges/remove", challengeBody{value}, nil)
}

// requireKeys checks that every field the protocol lists is in the answer,
// null or not: a v1 site never leaves one out, and a field that is missing is
// an answer from something else.
func requireKeys(raw json.RawMessage, keys ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("the site's response is not what the app domain protocol describes: %w", err)
	}
	for _, key := range keys {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("the site's response is not what the app domain protocol describes: %s is missing", key)
		}
	}
	return nil
}

func checkDomain(raw json.RawMessage, d *Domain) error {
	if err := requireKeys(raw, "handle", "hostname", "address", "status", "reason", "createdAt", "updatedAt"); err != nil {
		return err
	}
	if !ValidHandle(d.Handle) || d.Hostname == "" || !strings.HasPrefix(d.Hostname, d.Handle+".") {
		return errors.New("the site's response is not what the app domain protocol describes: the domain's handle or hostname is malformed")
	}
	if d.Status != DomainActive && d.Status != DomainSuspended {
		return fmt.Errorf("the site's response is not what the app domain protocol describes: status %q", d.Status)
	}
	return nil
}

// domainError turns the site's refusal into a sentence for the operator. The
// site's own words follow ours where they carry the detail (the address it
// saw, the day a quarantined handle frees up), quoted, never read as ours.
func domainError(err error) error {
	var api *APIError
	if !errors.As(err, &api) {
		return err
	}
	said := ""
	if api.Message != "" {
		said = ` The site said: "` + api.Message + `"`
	}
	msg := ""
	switch api.Code {
	case "invalid_handle":
		msg = "That handle is not allowed: use 3 to 20 lowercase letters, digits or dashes, starting and ending with a letter or digit, with no two dashes in a row."
	case "invalid_address":
		msg = "The site does not accept this server's address: it must be a public IPv4 address." + said
	case "invalid_challenge":
		msg = "The site refused the DNS challenge value: it is not 43 base64url characters."
	case "limit_reached":
		msg = "Your just-dashboard.com account already holds as many handles as it may. Release one on another server first." + said
	case "account_suspended":
		msg = "Your just-dashboard.com account is suspended, so it holds no app domains." + said
	case "domain_suspended":
		msg = "This server's app domain is suspended by the site's administrators, and only they can change it." + said
	case "no_domain":
		msg = "This server holds no active app domain on the site, so there is nothing to prove a certificate for."
	case "handle_reserved":
		msg = "That handle is reserved, or the zone already uses the name for something else. Choose another." + said
	case "handle_taken":
		msg = "Another server already holds that handle. Choose another."
	case "handle_quarantined":
		msg = "That handle was released by another account less than 30 days ago and is held for it until then." + said
	case "address_mismatch":
		msg = "The site saw this server's request come from a different address than the one it would publish. App domains need the server's outbound and inbound IPv4 address to be the same." + said
	case "too_many_challenges":
		msg = "Four DNS challenge values are already live for this server. They expire within an hour; try again after that."
	case "rate_limited":
		msg = "The site is limiting how often this server may change its app domain. Try again later."
	case "dns_failed":
		msg = "The site could not update its DNS records just now. Nothing changed; try again." + said
	case "domains_unavailable":
		msg = "The site does not offer app domains at the moment." + said
	case "stale_time":
		msg = "This server's clock and the site's differ by more than 60 seconds; correct the time on this server."
	case "bad_signature", "unknown_server":
		msg = "The site does not accept this server's signature. If the server was removed from the account, unlink and link it again."
	default:
		msg = api.Error()
	}
	return &DomainError{Status: api.Status, Code: api.Code, Message: msg, err: err}
}
