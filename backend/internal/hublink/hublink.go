// Package hublink links this dashboard to a Just Hub on its own tailnet.
//
// A hub is a separate app that joins the tailnet as its own machine and shows
// many dashboards in one place. The link is the account link's protocol
// pointed at a different peer: the signed strings, the canonical JSON, the
// fingerprint and the command checks are the same bytes, and they are the
// same code, because this package runs a second instance of the accountlink
// engine with the hub's profile. What is the hub's own lives here: the rule
// that a hub must be one machine on this server's tailnet and is only ever
// reached over it, the fleet and notices its heartbeat replies carry, the
// status the dashboard's pages read, and the discovery answer.
//
// The contract is docs/internal/backend/hub-link-protocol.md; how this
// implements it is docs/internal/backend/hub-link.md.
package hublink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
	"github.com/Wayy01/Just-Dashboard/backend/internal/tailnetgate"
)

const (
	// ProtocolVersion is the hub link protocol this build speaks.
	ProtocolVersion = 1
	// BasePath is where the hub answers the link API.
	BasePath = "/api/hub/v1"
)

var (
	// A hub key's id has no dot, so a sign-in ticket splits on dots without
	// ambiguity.
	kidPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z_-]{0,63}$`)
	// The hub's name, which becomes the audit actor hub:<name>.
	hubNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	// A one-time token from the hub: jht_ and 32 random bytes in base64url.
	tokenPattern = regexp.MustCompile(`^jht_[A-Za-z0-9_-]{43}$`)
)

// profile is the hub link's instance of the engine. Every part of it differs
// from the account link's, so the two never share a key, a table row, a
// counter, a budget or an audit name.
var profile = accountlink.Profile{
	Name: "hub link", Title: "Just Hub",
	Peer: "hub", PeerAt: "in the hub",
	KeyPrefix: "hub.link", CommandsTable: "hub_link_commands",
	BasePath: BasePath, Agent: "hub-link",
	ActorPrefix: "hub:", AuditPrefix: "hub",
	HeartbeatsPerMinute: 12, DefaultHeartbeatSeconds: 15,
	KidPattern: kidPattern, RotationKidPattern: kidPattern, OwnerPattern: hubNamePattern,
	OriginPerLink: true, ReplyView: true,
}

var (
	ErrDisabled      = errors.New("connecting to Just Hub is switched off by JD_HUB_LINK=false")
	ErrAlreadyLinked = errors.New("this server is already connected to a hub; disconnect it first")
	ErrLinkPending   = errors.New("a request to connect to a hub is already waiting for approval")
	ErrNotLinked     = errors.New("this server is not connected to a hub")
	ErrBadScope      = accountlink.ErrBadScope
	ErrBadToken      = errors.New("the token must be the one the hub printed: jht_ followed by 43 characters")
)

// UnreachableError is the hub not giving a step what it needed: it could not
// be reached, refused the request, or answered with something that is not
// protocol 1. The message says which.
type UnreachableError struct {
	URL string
	Err error
}

func (e *UnreachableError) Error() string { return fmt.Sprintf("the hub at %s: %v", e.URL, e.Err) }
func (e *UnreachableError) Unwrap() error { return e.Err }

// Options are what a Service is made from. The terminal commands leave Host
// out: they connect and disconnect and never run the connector.
type Options struct {
	Store  *store.Store
	Sealer *auth.Sealer
	Audit  *audit.Logger
	Log    *slog.Logger
	// Enabled is JD_HUB_LINK. Off, nothing is ever sent and connecting is
	// refused; an existing link stays stored and idle.
	Enabled bool
	// TestOrigin is JD_HUB_LINK_TEST_ORIGIN after ParseTestOrigin. Tests only.
	TestOrigin string
	Host       accountlink.Host
	Version    string
	// Describe names this server to the hub; the host's own names by default.
	Describe func(context.Context) accountlink.ServerDescription
	// Endpoint is the address a browser opens this dashboard at, sent with a
	// request so the hub's approval screen can show it.
	Endpoint func(context.Context) string
	// Tailnet reads this server's tailnet; the host's tailscale by default.
	Tailnet Tailnet
	// WhoIs asks tailscaled who holds an address; its local API by default.
	// It confirms a hub's name when a link is begun.
	WhoIs tailnetgate.WhoIs
	// HTTP and Now are for tests.
	HTTP     *http.Client
	Now      func() time.Time
	Location *time.Location
}

// Service is the hub link: the accountlink engine with the hub's profile, and
// the hub's own rules around it.
type Service struct {
	engine     *accountlink.Service
	st         *store.Store
	log        *slog.Logger
	enabled    bool
	testOrigin string
	tailnet    Tailnet
	whois      tailnetgate.WhoIs
	dial       *dialer
	endpoint   func(context.Context) string
	now        func() time.Time

	mu sync.Mutex
	// admitted is the rule's decision on each stored link, made once per
	// link per process: when the connector starts with it.
	admitted map[string]error
}

func New(o Options) *Service {
	s := &Service{
		st: o.Store, log: o.Log, enabled: o.Enabled, testOrigin: o.TestOrigin,
		tailnet: o.Tailnet, whois: o.WhoIs, endpoint: o.Endpoint, now: o.Now,
		admitted: map[string]error{},
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.tailnet == nil {
		s.tailnet = hostTailnet{}
	}
	if s.whois == nil {
		s.whois = tailnetgate.NewLocalAPI(hostexec.HostPath)
	}
	if s.now == nil {
		s.now = func() time.Time { return time.Now().UTC() }
	}
	s.dial = newDialer(s.tailnet, s.testOrigin)
	client := o.HTTP
	if client == nil {
		client = newHTTPClient(s.dial)
	}
	p := profile
	s.engine = accountlink.New(accountlink.Options{
		Store: o.Store, Sealer: o.Sealer, Audit: o.Audit, Log: s.log, Enabled: o.Enabled,
		Host: o.Host, Version: o.Version, Describe: o.Describe, HTTP: client, Now: s.now, Location: o.Location,
		Profile: &p,
		Hooks: accountlink.Hooks{
			Admit:   s.admit,
			OnReply: s.onReply,
			OnClear: s.onClear,
			// Hub sign-in adds its grants to the heartbeat here, through
			// HeartbeatExtra, once it exists. Until then the heartbeat is
			// the snapshot alone and the hub reads that as sign-in off.
		},
	})
	return s
}

// Enabled reports JD_HUB_LINK.
func (s *Service) Enabled() bool { return s != nil && s.enabled }

// TestOrigin is JD_HUB_LINK_TEST_ORIGIN, when it is set.
func (s *Service) TestOrigin() string { return s.testOrigin }

// Engine is the accountlink instance underneath, for tests that need to
// shorten its waits.
func (s *Service) Engine() *accountlink.Service { return s.engine }

// Start runs the connector until ctx ends or Stop is called. Switched off, or
// without a Host, it runs nothing and opens no connection.
func (s *Service) Start(ctx context.Context) {
	if s == nil {
		return
	}
	s.engine.Start(ctx)
}

// Stop ends the connector and waits for it.
func (s *Service) Stop() {
	if s == nil {
		return
	}
	s.engine.Stop()
}

// admit is the engine's hook for a stored link: the URL rule as it applies at
// start, decided once per link.
func (s *Service) admit(ctx context.Context, st accountlink.State) error {
	key := st.ServerID + " " + st.URL
	s.mu.Lock()
	err, decided := s.admitted[key]
	s.mu.Unlock()
	if decided {
		return err
	}
	err = s.admitStored(ctx, st.URL)
	if err != nil {
		s.log.Warn("the hub link is idle: its address no longer passes this server's tailnet rule", "hub", st.URL, "err", err)
	}
	s.mu.Lock()
	s.admitted[key] = err
	s.mu.Unlock()
	return err
}

// translate gives the engine's errors this package's names.
func translate(err error) error {
	var site *accountlink.SiteError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, accountlink.ErrDisabled):
		return ErrDisabled
	case errors.Is(err, accountlink.ErrAlreadyLinked):
		return ErrAlreadyLinked
	case errors.Is(err, accountlink.ErrLinkPending):
		return ErrLinkPending
	case errors.Is(err, accountlink.ErrNotLinked):
		return ErrNotLinked
	case errors.As(err, &site):
		var api *accountlink.APIError
		if errors.As(site.Err, &api) && api.Status == http.StatusBadRequest && api.Code == "unsupported_protocol" {
			return &UnreachableError{URL: site.URL, Err: fmt.Errorf("it does not speak hub link protocol %d; update this dashboard or the hub so they speak the same one", ProtocolVersion)}
		}
		return &UnreachableError{URL: site.URL, Err: site.Err}
	}
	return err
}

// ConnectRequest is what a link to a hub is begun with.
type ConnectRequest struct {
	// URL is the hub as a person typed it; the rule reads and checks it.
	URL   string
	Scope accountlink.Scope
	// By is the account that pressed Connect, or "cli".
	By string
	// Token is the hub's one-time approval. Only the terminal sends one.
	Token string
}

// Connect applies the URL rule and asks the hub for a code. The hub owner
// approves it in the hub, or the token has approved it in advance; either way
// the dashboard (or the terminal command, for "cli") learns of it by polling.
func (s *Service) Connect(ctx context.Context, r ConnectRequest) (accountlink.Request, error) {
	if !s.enabled {
		return accountlink.Request{}, ErrDisabled
	}
	if _, ok := accountlink.ParseScope(string(r.Scope)); !ok {
		return accountlink.Request{}, ErrBadScope
	}
	if r.Token != "" && !tokenPattern.MatchString(r.Token) {
		return accountlink.Request{}, ErrBadToken
	}
	// One hub per server. Said before the address is looked at, which asks
	// the host's tailnet.
	if _, linked, err := s.engine.Linked(ctx); err != nil {
		return accountlink.Request{}, err
	} else if linked {
		return accountlink.Request{}, ErrAlreadyLinked
	}
	origin, err := s.checkURL(ctx, r.URL)
	if err != nil {
		return accountlink.Request{}, err
	}
	endpoint := ""
	if s.endpoint != nil {
		if e := strings.TrimSpace(s.endpoint(ctx)); len(e) <= 255 {
			endpoint = e
		}
	}
	req, err := s.engine.Begin(ctx, accountlink.LinkRequest{
		Scope: r.Scope, By: r.By, Origin: origin, Endpoint: endpoint, Token: r.Token,
	})
	return req, translate(err)
}

// Poll asks the hub whether the pending request has been decided.
func (s *Service) Poll(ctx context.Context) (accountlink.PollResult, error) {
	res, err := s.engine.PollLink(ctx)
	return res, translate(err)
}

// Await polls the pending request until it is decided or ctx ends, which is
// the terminal command's whole job. If ctx ends first the request is
// forgotten.
func (s *Service) Await(ctx context.Context) (accountlink.PollResult, error) {
	res, err := s.engine.AwaitLink(ctx)
	return res, translate(err)
}

// Cancel forgets a pending request. The hub is not told; it expires there.
func (s *Service) Cancel(ctx context.Context) error { return s.engine.CancelLink(ctx) }

// SetScope narrows or widens what the hub may see and do. The next heartbeat
// tells the hub.
func (s *Service) SetScope(ctx context.Context, scope accountlink.Scope) error {
	return translate(s.engine.SetScope(ctx, scope))
}

// Disconnect tells the hub (one attempt) and forgets the link, whether or not
// the hub heard. The identity and the sequence are kept.
func (s *Service) Disconnect(ctx context.Context) (hubNotified bool, err error) {
	notified, err := s.engine.Unlink(ctx)
	return notified, translate(err)
}

// Sync sends a heartbeat now. A press that would break the hub's pace is
// skipped; the result is on the status either way.
func (s *Service) Sync(ctx context.Context) error { return translate(s.engine.SyncNow(ctx)) }

// Linked is the stored link, which holds nothing secret.
func (s *Service) Linked(ctx context.Context) (accountlink.State, bool, error) {
	return s.engine.Linked(ctx)
}

// Pending is the request waiting for approval, if one is and has not expired.
func (s *Service) Pending(ctx context.Context) (accountlink.Request, bool, error) {
	return s.engine.Pending(ctx)
}
