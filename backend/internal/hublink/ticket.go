package hublink

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/tailnetgate"
)

// A hub sign-in ticket (hub-link-protocol.md section 9.4) is four dot-separated
// parts: "v1", the hub key's id, the payload and the signature, the last two in
// base64url without padding. The hub signs a string whose first line has a
// space in it, which no serverId can have, so a ticket signature is never
// taken for a command signature or the other way round.
const (
	ticketVersion   = "v1"
	ticketPrefix    = "jd hub sign-in ticket v1\n"
	ticketMaxLength = 2048
	ticketMaxLife   = 60
	// ticketGrace covers a small clock difference between the hub and this
	// server; with it a ticket is accepted for at most 120 seconds, once.
	ticketGrace = 30
	// mfaMaxAge is how old the hub user's own TOTP code may be when the hub
	// makes the ticket.
	mfaMaxAge  = 600
	maxSafeInt = 1<<53 - 1
)

var (
	base64URLPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	tsLoginPattern   = regexp.MustCompile(`^[\x21-\x40\x5b-\x7e]{1,254}$`)
	tsNodePattern    = regexp.MustCompile(`^[0-9A-Za-z_-]{1,64}$`)
	noncePattern     = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)
	nextPattern      = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,;=:@%/?-]{0,511}$`)
	plainInteger     = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
)

// The refusals of section 9.7, each with its status.
const (
	codeSignInDisabled     = "hub_signin_disabled"
	codeNotLinked          = "hub_not_linked"
	codeBadTicket          = "bad_ticket"
	codeWrongServer        = "wrong_server"
	codeTicketExpired      = "ticket_expired"
	codeMFAStale           = "mfa_stale"
	codeWrongDevice        = "wrong_device"
	codeTicketUsed         = "ticket_used"
	codeNotPermitted       = "not_permitted"
	codeAccountUnavailable = "account_unavailable"
)

// SignInError is a ticket refused at one step of section 9.7. Its message is
// for the person on /hub/enter.
type SignInError struct {
	Status  int
	Code    string
	Message string
}

func (e *SignInError) Error() string { return e.Code + ": " + e.Message }

func refuse(status int, code, message string) *SignInError {
	return &SignInError{Status: status, Code: code, Message: message}
}

var (
	errBadTicket = refuse(http.StatusUnauthorized, codeBadTicket,
		"this sign-in link is not one this server can read; open the server again from Just Hub")
	errWrongServer = refuse(http.StatusUnauthorized, codeWrongServer,
		"this sign-in link was made for another server or by another hub")
	errTicketExpired = refuse(http.StatusUnauthorized, codeTicketExpired,
		"this sign-in link has expired; open the server again from Just Hub, and check this server's clock if it keeps happening")
	errMFAStale = refuse(http.StatusUnauthorized, codeMFAStale,
		"Just Hub did not confirm your authenticator code recently enough; open the server again from Just Hub")
	errWrongDevice = refuse(http.StatusUnauthorized, codeWrongDevice,
		"this sign-in link was made for another device or another tailnet login; open the server from Just Hub on this device")
	errTicketUsed = refuse(http.StatusUnauthorized, codeTicketUsed,
		"this sign-in link has already been used; open the server again from Just Hub, or sign in here")
)

// TicketPayload is the signed payload of a ticket, after section 9.4's rules.
type TicketPayload struct {
	V        int64
	Typ      string
	Hub      string
	ServerID string
	UserID   int64
	HubUser  string
	TSLogin  string
	TSNode   string
	Nonce    string
	Iat      int64
	Exp      int64
	MFAAt    int64
	Next     string
}

// NextPath is where the person lands: the ticket's next, or /dashboard.
func (p TicketPayload) NextPath() string {
	if p.Next == "" {
		return "/dashboard"
	}
	return p.Next
}

// ticketField is one field of section 9.4's table, and how it is read.
type ticketField struct {
	integer  bool
	optional bool
	set      func(*TicketPayload, string, int64)
}

var ticketFields = map[string]ticketField{
	"v":        {integer: true, set: func(p *TicketPayload, _ string, n int64) { p.V = n }},
	"typ":      {set: func(p *TicketPayload, s string, _ int64) { p.Typ = s }},
	"hub":      {set: func(p *TicketPayload, s string, _ int64) { p.Hub = s }},
	"serverId": {set: func(p *TicketPayload, s string, _ int64) { p.ServerID = s }},
	"userId":   {integer: true, set: func(p *TicketPayload, _ string, n int64) { p.UserID = n }},
	"hubUser":  {set: func(p *TicketPayload, s string, _ int64) { p.HubUser = s }},
	"tsLogin":  {set: func(p *TicketPayload, s string, _ int64) { p.TSLogin = s }},
	"tsNode":   {set: func(p *TicketPayload, s string, _ int64) { p.TSNode = s }},
	"nonce":    {set: func(p *TicketPayload, s string, _ int64) { p.Nonce = s }},
	"iat":      {integer: true, set: func(p *TicketPayload, _ string, n int64) { p.Iat = n }},
	"exp":      {integer: true, set: func(p *TicketPayload, _ string, n int64) { p.Exp = n }},
	"mfaAt":    {integer: true, set: func(p *TicketPayload, _ string, n int64) { p.MFAAt = n }},
	"next":     {optional: true, set: func(p *TicketPayload, s string, _ int64) { p.Next = s }},
}

// decodeTicketPayload reads the payload JSON one token at a time, as
// decodeArgs reads command arguments. Decoding into a struct would accept a
// key in another case, a repeated key and a null, which section 9.4 refuses:
// here a key is compared byte for byte, a repeat is refused, every value has
// its one JSON type, integers are plain decimal, and nothing follows the
// object.
func decodeTicketPayload(raw []byte) (TicketPayload, bool) {
	var p TicketPayload
	if !utf8.Valid(raw) {
		return p, false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if t, err := d.Token(); err != nil || t != json.Delim('{') {
		return p, false
	}
	seen := map[string]bool{}
	for d.More() {
		t, err := d.Token()
		if err != nil {
			return p, false
		}
		key, ok := t.(string)
		f, known := ticketFields[key]
		if !ok || !known || seen[key] {
			return p, false
		}
		seen[key] = true
		t, err = d.Token()
		if err != nil {
			return p, false
		}
		switch v := t.(type) {
		case string:
			if f.integer {
				return p, false
			}
			f.set(&p, v, 0)
		case json.Number:
			if !f.integer || !plainInteger.MatchString(v.String()) {
				return p, false
			}
			n, err := strconv.ParseInt(v.String(), 10, 64)
			if err != nil || n > maxSafeInt || n < -maxSafeInt {
				return p, false
			}
			f.set(&p, "", n)
		default:
			// null, true, false, an object or an array: no field is one.
			return p, false
		}
	}
	if t, err := d.Token(); err != nil || t != json.Delim('}') {
		return p, false
	}
	if _, err := d.Token(); err != io.EOF {
		return p, false
	}
	for key, f := range ticketFields {
		if !f.optional && !seen[key] {
			return p, false
		}
	}
	if seen["next"] && (!nextPattern.MatchString(p.Next) || strings.HasPrefix(p.Next, "//")) {
		return p, false
	}
	switch {
	case p.V != 1, p.Typ != "hub.signin", p.UserID < 1, p.MFAAt < 1,
		!hubUserPattern.MatchString(p.HubUser), !tsLoginPattern.MatchString(p.TSLogin),
		!tsNodePattern.MatchString(p.TSNode), !noncePattern.MatchString(p.Nonce),
		!(p.Iat < p.Exp && p.Exp <= p.Iat+ticketMaxLife):
		return p, false
	}
	return p, true
}

// ticketPeek reads the account a ticket names without checking anything, for
// display only. /hub/enter does the same in the browser; the server never acts
// on it.
func ticketPeek(ticket string) (TicketPayload, bool) {
	parts := strings.Split(ticket, ".")
	if len(parts) != 4 {
		return TicketPayload{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return TicketPayload{}, false
	}
	return decodeTicketPayload(raw)
}

// pinnedKey is the hub key a ticket's kid names, if it is pinned and not past
// its end (step 4).
func pinnedKey(keys []accountlink.SiteKey, kid string, now time.Time) (ed25519.PublicKey, bool) {
	for _, k := range keys {
		if k.Kid != kid || (k.NotAfter != 0 && now.Unix() >= k.NotAfter) {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(k.PublicKey)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, false
		}
		return ed25519.PublicKey(raw), true
	}
	return nil, false
}

// ticketCheck is what a ticket is checked against: the link as it is stored.
type ticketCheck struct {
	Keys     []accountlink.SiteKey
	Hub      string
	ServerID string
	Now      time.Time
}

// checkTicket is steps 3 to 9 of section 9.7, in order. It returns the kid once
// step 4 has passed and the payload once step 6 has, so a refusal after them
// can be audited with what is known (section 9.10).
func checkTicket(ticket string, c ticketCheck) (kid string, p *TicketPayload, signed bool, err *SignInError) {
	// Step 3: the shape.
	if len(ticket) > ticketMaxLength {
		return "", nil, false, errBadTicket
	}
	parts := strings.Split(ticket, ".")
	if len(parts) != 4 || parts[0] != ticketVersion || !kidPattern.MatchString(parts[1]) ||
		!base64URLPattern.MatchString(parts[2]) || !base64URLPattern.MatchString(parts[3]) {
		return "", nil, false, errBadTicket
	}
	kid = parts[1]
	// Step 4: a pinned key.
	key, ok := pinnedKey(c.Keys, kid, c.Now)
	if !ok {
		return kid, nil, false, errBadTicket
	}
	// Step 5: the signature, over the exact payload segment, before any JSON
	// is read.
	sig, e := base64.RawURLEncoding.DecodeString(parts[3])
	if e != nil || len(sig) != ed25519.SignatureSize ||
		!ed25519.Verify(key, []byte(ticketPrefix+kid+"\n"+parts[2]), sig) {
		return kid, nil, false, errBadTicket
	}
	// Step 6: the payload's rules.
	raw, e := base64.RawURLEncoding.DecodeString(parts[2])
	if e != nil {
		return kid, nil, true, errBadTicket
	}
	payload, ok := decodeTicketPayload(raw)
	if !ok {
		return kid, nil, true, errBadTicket
	}
	p = &payload
	// Step 7: this hub and this server.
	if p.Hub != c.Hub || p.ServerID != c.ServerID {
		return kid, p, true, errWrongServer
	}
	// Step 8: the time, with the grace either side.
	now := c.Now.Unix()
	if now < p.Iat-ticketGrace || now > p.Exp+ticketGrace {
		return kid, p, true, errTicketExpired
	}
	// Step 9: the hub's own second factor, fresh.
	if p.MFAAt > p.Iat || p.Iat-p.MFAAt > mfaMaxAge {
		return kid, p, true, errMFAStale
	}
	return kid, p, true, nil
}

// DeviceCheck is what step 10 found: "checked", or "skipped" with the reason,
// and what WhoIs said when it named someone else.
type DeviceCheck struct {
	Result string
	Reason string
	Login  string
	Node   string
}

// checkDevice is step 10: the ticket is used by the device that asked the hub
// for it. A loopback client (an SSH tunnel to this server) or a tailscaled that
// cannot be asked skips the check, and the audit entry says so; anything else
// must be the tailnet login and node the ticket names.
func checkDevice(ctx context.Context, whois tailnetgate.WhoIs, client string, p *TicketPayload) (DeviceCheck, *SignInError) {
	addr, err := netip.ParseAddr(strings.TrimSpace(client))
	if err != nil {
		return DeviceCheck{Result: "refused", Reason: "the client address could not be read"}, errWrongDevice
	}
	addr = addr.Unmap()
	if addr.IsLoopback() {
		return DeviceCheck{Result: "skipped", Reason: "loopback"}, nil
	}
	if whois == nil {
		return DeviceCheck{Result: "skipped", Reason: "tailscaled unavailable"}, nil
	}
	peer, err := whois.WhoIs(ctx, addr)
	switch {
	case errors.Is(err, tailnetgate.ErrNotAPeer):
		return DeviceCheck{Result: "refused", Reason: "not on the tailnet"}, errWrongDevice
	case err != nil:
		return DeviceCheck{Result: "skipped", Reason: "tailscaled unavailable"}, nil
	}
	got := DeviceCheck{Login: strings.ToLower(peer.Login), Node: peer.StableID}
	if peer.Login == "" || got.Login != p.TSLogin || peer.StableID == "" || peer.StableID != p.TSNode {
		got.Result = "refused"
		return got, errWrongDevice
	}
	got.Result = "checked"
	return got, nil
}
