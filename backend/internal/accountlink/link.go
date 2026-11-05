package accountlink

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/sysinfo"
)

var (
	requestIDPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z-]{7,63}$`)
	userCodePattern  = regexp.MustCompile(`^[A-HJ-NP-Z2-9]{4}-[A-HJ-NP-Z2-9]{4}$`)
	kidPattern       = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]{0,63}$`)
)

// SiteError is a failure to get what a step needed from the site: it could not
// be reached, refused the request, or answered with something that is not
// protocol 1.
type SiteError struct {
	URL string
	Err error
	// Peer is what the other end is called; "site" when unset.
	Peer string
}

func (e *SiteError) Error() string {
	peer := e.Peer
	if peer == "" {
		peer = "site"
	}
	return fmt.Sprintf("the %s at %s: %v", peer, e.URL, e.Err)
}
func (e *SiteError) Unwrap() error { return e.Err }

func (s *Service) peerError(origin string, err error) *SiteError {
	return &SiteError{URL: origin, Err: err, Peer: s.p.Peer}
}

// LinkRequest is what a link is begun with.
type LinkRequest struct {
	Scope Scope
	// By is the dashboard account that asks, or "cli" for the terminal
	// command, which then polls the request itself.
	By string
	// Origin is where to ask, for a profile that chooses it per link; a
	// profile with a configured site ignores it.
	Origin string
	// Endpoint is this dashboard's own address and Token a one-time approval;
	// each is sent only when set.
	Endpoint, Token string
	// HubNode is the peer's machine as the instance found it; it is kept with
	// the request and becomes the link's.
	HubNode *PeerNode
}

// ----------------------------------------------------------------- the flow --

// BeginLink asks the site for a code. The person approves it on the site, and
// the dashboard (or the terminal command, when by is "cli") learns of it by
// polling. Scope is chosen here and kept here.
func (s *Service) BeginLink(ctx context.Context, scope Scope, by string) (Request, error) {
	return s.Begin(ctx, LinkRequest{Scope: scope, By: by})
}

// Begin is BeginLink with everything a request can carry.
func (s *Service) Begin(ctx context.Context, lr LinkRequest) (Request, error) {
	scope, by := lr.Scope, lr.By
	driver := driverDashboard
	if by == "cli" {
		driver = driverCLI
	}
	if !s.enabled {
		return Request{}, ErrDisabled
	}
	if _, ok := ParseScope(string(scope)); !ok {
		return Request{}, ErrBadScope
	}
	if _, linked, err := s.kv.state(ctx); err != nil {
		return Request{}, err
	} else if linked {
		return Request{}, ErrAlreadyLinked
	}
	if req, ok, err := s.kv.request(ctx); err != nil {
		return Request{}, err
	} else if ok {
		switch {
		case !s.now().Before(req.ExpiresAt):
			s.endRequest(ctx, outcomeExpired, "the request was not approved in time")
		case driver == driverCLI && req.Driver == driverCLI:
			// A terminal command that was killed outright leaves its request
			// behind, with nobody polling it. The next terminal command is
			// the only thing that could be, so it starts over.
			if err := s.CancelLink(ctx); err != nil {
				return Request{}, err
			}
		default:
			return Request{}, ErrLinkPending
		}
	}

	origin := s.siteURL
	if s.p.OriginPerLink {
		if origin = lr.Origin; origin == "" {
			return Request{}, errors.New("a link request needs the address to ask")
		}
	}
	id, err := ensureIdentity(ctx, s.kv, s.sealer)
	if errors.Is(err, ErrIdentityUnreadable) {
		// Nothing is linked, so there is nothing a new key would strand.
		id, err = replaceUnreadableIdentity(ctx, s.kv, s.sealer)
	}
	if err != nil {
		return Request{}, err
	}
	c := s.newClientAt(origin, id)
	if s.p.Discover {
		info, err := c.site(ctx)
		if err != nil {
			return Request{}, s.peerError(origin, err)
		}
		if !slices.Contains(info.Protocol, ProtocolVersion) {
			return Request{}, s.peerError(origin, fmt.Errorf("does not speak protocol %d (it lists %v); update this dashboard", ProtocolVersion, info.Protocol))
		}
	}

	d := s.desc(ctx)
	resp, err := c.createRequest(ctx, createRequestBody{
		PublicKey: id.PublicKey(), Scope: scope,
		Server: serverDescription{
			Name: fitText(d.Name, 64), Hostname: fitText(d.Hostname, 64), Version: fitText(s.version, 32),
			OS: fitText(d.OS, 64), Arch: fitText(d.Arch, 16), Endpoint: lr.Endpoint,
		},
		Token: lr.Token,
	})
	if err != nil {
		return Request{}, s.peerError(origin, err)
	}
	req, err := s.checkRequestResponse(origin, resp, scope, by, driver)
	req.ServerName = fitText(d.Name, 64)
	if err != nil {
		return Request{}, s.peerError(origin, err)
	}
	if s.p.OriginPerLink {
		req.URL = origin
	}
	req.HubNode = lr.HubNode
	if err := s.kv.tx(ctx, func(tx *sql.Tx) error {
		if err := s.kv.putTx(ctx, tx, keyRequest, req); err != nil {
			return err
		}
		return s.kv.delTx(ctx, tx, keyOutcome)
	}); err != nil {
		return Request{}, err
	}
	s.nudge()
	return req, nil
}

// fitText makes a string acceptable to the site's field limits, which count
// characters: printable, non-empty and no longer than max.
func fitText(s string, max int) string {
	s = strings.TrimSpace(clip(s, max*4))
	if s == "" {
		return "unknown"
	}
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
	}
	return s
}

// checkRequestResponse refuses a site answer this dashboard should not act on
// or show a person.
func (s *Service) checkRequestResponse(origin string, r createRequestResponse, scope Scope, by, driver string) (Request, error) {
	switch {
	case !requestIDPattern.MatchString(r.RequestID):
		return Request{}, errors.New("answered with a request id this dashboard does not accept")
	case !userCodePattern.MatchString(r.UserCode):
		return Request{}, errors.New("answered with a code that is not of the form ABCD-1234")
	case !sameOrigin(origin, r.ApproveURL):
		return Request{}, errors.New("answered with an approval address on another " + s.p.Peer)
	}
	expires := time.Unix(r.ExpiresAt, 0).UTC()
	if !expires.After(s.now()) || expires.After(s.now().Add(time.Hour)) {
		return Request{}, errors.New("answered with an expiry that is not within the hour")
	}
	return Request{
		ID: r.RequestID, UserCode: r.UserCode, ApproveURL: r.ApproveURL, ExpiresAt: expires,
		Scope: scope, PollSeconds: clampInt(r.PollSeconds, 2, 30), By: by, Driver: driver,
	}, nil
}

// The states PollLink reports.
const (
	PollPending = "pending"
	PollLinked  = "linked"
	PollDenied  = "denied"
	PollExpired = "expired"
	PollFailed  = "failed"
)

// PollResult is where a link request stands after asking the site.
type PollResult struct {
	State   string
	Message string
	// Link is set once State is PollLinked.
	Link State
}

// PollLink asks the site whether the pending request has been decided. An error
// means the site could not be asked this time and the request stands; a result
// other than PollPending means the request is over.
func (s *Service) PollLink(ctx context.Context) (PollResult, error) {
	s.pollMu.Lock()
	defer s.pollMu.Unlock()
	if st, linked, err := s.kv.state(ctx); err != nil {
		return PollResult{}, err
	} else if linked {
		return PollResult{State: PollLinked, Link: st}, nil
	}
	req, ok, err := s.kv.request(ctx)
	if err != nil {
		return PollResult{}, err
	}
	if !ok {
		return PollResult{State: PollFailed, Message: "there is no link request"}, nil
	}
	if !s.now().Before(req.ExpiresAt) {
		s.endRequest(ctx, outcomeExpired, "the request was not approved in time")
		return PollResult{State: PollExpired, Message: "the request was not approved in time"}, nil
	}
	id, _, err := loadIdentity(ctx, s.kv, s.sealer)
	if err != nil || id == nil {
		return PollResult{}, fmt.Errorf("link identity: %w", firstErr(err, ErrIdentityUnreadable))
	}
	// The instance's own check, before anything is sent: an approval is
	// stored only from a poll it let through.
	if s.hooks.BeforePoll != nil {
		if req, err = s.hooks.BeforePoll(ctx, req); err != nil {
			return s.pollRefused(ctx, err)
		}
	}
	resp, err := s.newClientAt(s.originFor(req.URL), id).pollRequest(ctx, req.ID)
	declined := "the request was declined " + s.p.PeerAt
	var ended *RequestEndedError
	switch {
	case err == nil:
	case isCode(err, http.StatusGone, "request_denied"):
		s.endRequest(ctx, outcomeDenied, declined)
		return PollResult{State: PollDenied, Message: declined}, nil
	case isCode(err, http.StatusGone, "request_expired"):
		s.endRequest(ctx, outcomeExpired, "the request was not approved in time")
		return PollResult{State: PollExpired, Message: "the request was not approved in time"}, nil
	case isCode(err, http.StatusGone, "request_consumed"):
		// Another process collected the approval; if it is stored, it is ours.
		if st, linked, e := s.kv.state(ctx); e == nil && linked {
			return PollResult{State: PollLinked, Link: st}, nil
		}
		// The site handed this request's approval out once and it was not kept
		// here: a poll answered but the link could not be stored, or the
		// process stopped between the two. Nothing else is to blame, and the
		// only remedy is a new code.
		lost := "the " + s.p.Peer + " had already handed over this request's approval, but no link was stored on this server; request a new code"
		s.endRequest(ctx, outcomeFailed, lost)
		return PollResult{State: PollFailed, Message: lost}, nil
	case isCode(err, http.StatusUnauthorized, "unknown_server"):
		forgot := "the " + s.p.Peer + " no longer knows this request"
		s.endRequest(ctx, outcomeFailed, forgot)
		return PollResult{State: PollFailed, Message: forgot}, nil
	case errors.As(err, &ended):
		// The transport's own check refused to dial.
		return s.pollRefused(ctx, err)
	default:
		return PollResult{State: PollPending}, err
	}
	switch resp.Status {
	case "pending":
		return PollResult{State: PollPending}, nil
	case "approved":
		st, err := s.completeLink(ctx, req, resp)
		if err != nil {
			s.endRequest(ctx, outcomeFailed, err.Error())
			return PollResult{State: PollFailed, Message: err.Error()}, nil
		}
		return PollResult{State: PollLinked, Link: st}, nil
	}
	return PollResult{State: PollPending}, fmt.Errorf("the %s answered with the status %q", s.p.Peer, clip(resp.Status, 32))
}

// pollRefused is the instance's check saying no to a poll: the request ends as
// failed when it says so, and otherwise this poll counts as a failed one.
func (s *Service) pollRefused(ctx context.Context, err error) (PollResult, error) {
	var ended *RequestEndedError
	if errors.As(err, &ended) {
		s.endRequest(ctx, outcomeFailed, ended.Message)
		return PollResult{State: PollFailed, Message: ended.Message}, nil
	}
	return PollResult{State: PollPending}, err
}

// completeLink stores what an approval carries and ends the request, in one
// transaction, so a link is either there with its keys or not there at all.
func (s *Service) completeLink(ctx context.Context, req Request, r pollResponse) (State, error) {
	kids := s.p.KidPattern
	if kids == nil {
		kids = kidPattern
	}
	switch {
	case !requestIDPattern.MatchString(r.ServerID):
		return State{}, errors.New("the approval names a server id this dashboard does not accept")
	case r.SiteKey == nil || !kids.MatchString(r.SiteKey.Kid):
		return State{}, errors.New("the approval carries no usable " + s.p.Peer + " key")
	case r.Owner == nil || strings.TrimSpace(r.Owner.Label) == "":
		return State{}, errors.New("the approval names no account")
	case s.p.OwnerPattern != nil && !s.p.OwnerPattern.MatchString(r.Owner.Label):
		return State{}, errors.New("the approval names the " + s.p.Peer + " with a label this dashboard does not accept")
	}
	key := SiteKey{Kid: r.SiteKey.Kid, PublicKey: r.SiteKey.PublicKey}
	if raw, err := base64.StdEncoding.DecodeString(key.PublicKey); err != nil || len(raw) != ed25519.PublicKeySize {
		return State{}, errors.New("the approval's " + s.p.Peer + " key is not a 32-byte Ed25519 key")
	}
	heartbeat, pollWait := r.HeartbeatSeconds, r.PollWaitSeconds
	if heartbeat == 0 {
		heartbeat = s.p.DefaultHeartbeatSeconds
	}
	if pollWait == 0 {
		pollWait = 25
	}
	origin := s.originFor(req.URL)
	st := State{
		URL: origin, ServerID: r.ServerID, SiteKeys: []SiteKey{key}, Scope: req.Scope,
		LinkedAt: s.now(), LinkedBy: req.By, Owner: fitText(r.Owner.Label, 254), ServerName: req.ServerName,
		HeartbeatSeconds: clampInt(heartbeat, 10, 600), PollWaitSeconds: clampInt(pollWait, 1, 25),
		HubNode: req.HubNode,
	}
	if err := s.kv.tx(ctx, func(tx *sql.Tx) error {
		if err := s.kv.putTx(ctx, tx, keyState, st); err != nil {
			return err
		}
		if err := s.kv.delTx(ctx, tx, keyRequest, keyOutcome, keyStatus, keyPolicy, keyAttempt); err != nil {
			return err
		}
		return s.onClear(ctx, tx)
	}); err != nil {
		return State{}, err
	}
	s.log.Info("linked to "+s.p.Title, "owner", st.Owner, "scope", st.Scope)
	if s.audit != nil {
		who, actor, method := req.By, "session", "LINK"
		if req.By == "cli" {
			who, actor, method = "root", "cli", "CLI"
		}
		detail := map[string]any{"scope": st.Scope, "serverId": st.ServerID, s.p.Peer: origin}
		if st.HubNode != nil {
			detail["hubNode"] = st.HubNode
		}
		s.audit.Record(ctx, audit.Entry{
			Username: who, Actor: actor, Action: s.p.AuditPrefix + ".link", Target: st.Owner, Method: method, Status: 200, Success: true,
			Detail: audit.Detail(detail),
		})
	}
	s.nudge()
	return st, nil
}

// endRequest closes the pending request and remembers why it ended, so the
// Settings page can say so.
func (s *Service) endRequest(ctx context.Context, result, message string) {
	ctx = context.WithoutCancel(ctx)
	err := s.kv.tx(ctx, func(tx *sql.Tx) error {
		if err := s.kv.delTx(ctx, tx, keyRequest); err != nil {
			return err
		}
		return s.kv.putTx(ctx, tx, keyOutcome, Outcome{Result: result, At: s.now(), Message: message})
	})
	if err != nil {
		s.log.Warn("could not close the link request", "err", err)
	}
}

// Linked is the stored link, which holds nothing secret.
func (s *Service) Linked(ctx context.Context) (State, bool, error) { return s.kv.state(ctx) }

// Pending is the request waiting for approval, if one is and it has not
// expired.
func (s *Service) Pending(ctx context.Context) (Request, bool, error) {
	req, ok, err := s.kv.request(ctx)
	if err != nil || !ok || !s.now().Before(req.ExpiresAt) {
		return Request{}, false, err
	}
	return req, true, nil
}

// CancelLink forgets a pending request. The site is not told; the request
// expires there on its own.
func (s *Service) CancelLink(ctx context.Context) error {
	return s.kv.del(context.WithoutCancel(ctx), keyRequest)
}

// AwaitLink polls the pending request until it is decided or ctx ends, which is
// the terminal command's whole job. If ctx ends first the request is cancelled.
func (s *Service) AwaitLink(ctx context.Context) (PollResult, error) {
	var lastErr error
	for {
		res, err := s.PollLink(ctx)
		if err == nil && res.State != PollPending {
			return res, nil
		}
		if err != nil {
			lastErr = err
		}
		wait := 5 * time.Second
		if req, ok, e := s.kv.request(ctx); e == nil && ok {
			wait = time.Duration(clampInt(req.PollSeconds, 2, 30)) * time.Second
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			_ = s.CancelLink(ctx)
			msg := "no decision was made in time"
			if lastErr != nil {
				msg += "; the last attempt to ask the " + s.p.Peer + " failed: " + lastErr.Error()
			}
			return PollResult{State: PollExpired, Message: msg}, nil
		case <-t.C:
		}
	}
}

// ------------------------------------------------------ changing a live link --

// SetScope narrows or widens what the site may see and do. The next heartbeat
// tells the site.
func (s *Service) SetScope(ctx context.Context, scope Scope) error {
	if !s.enabled {
		return ErrDisabled
	}
	if _, ok := ParseScope(string(scope)); !ok {
		return ErrBadScope
	}
	st, linked, err := s.kv.state(ctx)
	if err != nil {
		return err
	}
	if !linked {
		return ErrNotLinked
	}
	// Written back inside one transaction, so a heartbeat storing a rotated
	// key at the same moment cannot put the old scope back.
	if _, ok, err := s.kv.updateState(ctx, st.ServerID, func(cur *State) bool {
		cur.Scope = scope
		return true
	}); err != nil {
		return err
	} else if !ok {
		return ErrNotLinked
	}
	s.kickHeartbeat()
	return nil
}

// Unlink tells the site and forgets the link. A site that cannot be reached
// does not keep the link alive: the dashboard's own state is what matters, and
// the owner can remove the server on the site. The result says whether the
// site was told.
func (s *Service) Unlink(ctx context.Context) (siteNotified bool, err error) {
	st, linked, err := s.kv.state(ctx)
	if err != nil {
		return false, err
	}
	if !linked {
		return false, ErrNotLinked
	}
	// Only the site the link was made with is told: a URL that has changed
	// since names somebody who never heard of this server.
	if id, _, e := loadIdentity(ctx, s.kv, s.sealer); e == nil && id != nil && s.enabled && s.admit(ctx, st) == nil {
		ctx2, cancel := context.WithTimeout(ctx, requestTimeout)
		e = s.linkClient(st, id).unlink(ctx2, st.ServerID)
		cancel()
		siteNotified = e == nil || isCode(e, http.StatusGone, "")
	}
	if err := s.kv.tx(ctx, func(tx *sql.Tx) error {
		if err := s.kv.delTx(ctx, tx, keyState, keyRequest, keyStatus, keyPolicy, keyAttempt, keyOutcome); err != nil {
			return err
		}
		return s.onClear(ctx, tx)
	}); err != nil {
		return siteNotified, err
	}
	s.cancelLinked()
	s.nudge()
	return siteNotified, nil
}

// SyncNow sends a heartbeat now and returns once it has been tried; the result
// is on the status either way.
func (s *Service) SyncNow(ctx context.Context) error {
	if !s.enabled {
		return ErrDisabled
	}
	st, linked, err := s.kv.state(ctx)
	if err != nil {
		return err
	}
	if !linked {
		return ErrNotLinked
	}
	if err := s.admit(ctx, st); err != nil {
		return err
	}
	if err := s.sendHeartbeat(ctx, st, false); err != nil && !errors.Is(err, ErrTooSoon) {
		// The failure is already on the status as lastError.
		s.log.Debug("manual heartbeat failed", "err", err)
	}
	return nil
}

// describeHost names this machine for the site. The container shares the host's
// UTS namespace through host networking, so its hostname is the server's.
func describeHost(ctx context.Context) ServerDescription {
	d := ServerDescription{Arch: runtime.GOARCH}
	d.Hostname, _ = os.Hostname()
	d.Name = d.Hostname
	if info, err := sysinfo.NewCollector().Host(ctx); err == nil {
		if info.Hostname != "" {
			d.Hostname, d.Name = info.Hostname, info.Hostname
		}
		if info.Platform != "" {
			d.OS = strings.ToUpper(info.Platform[:1]) + info.Platform[1:]
			if info.PlatformVersion != "" {
				d.OS += " " + info.PlatformVersion
			}
		}
	}
	return d
}
