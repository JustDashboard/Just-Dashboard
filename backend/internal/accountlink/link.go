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
}

func (e *SiteError) Error() string { return fmt.Sprintf("the site at %s: %v", e.URL, e.Err) }
func (e *SiteError) Unwrap() error { return e.Err }

// ----------------------------------------------------------------- the flow --

// BeginLink asks the site for a code. The person approves it on the site, and
// the dashboard (or the terminal command, when by is "cli") learns of it by
// polling. Scope is chosen here and kept here.
func (s *Service) BeginLink(ctx context.Context, scope Scope, by string) (Request, error) {
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

	id, err := ensureIdentity(ctx, s.st, s.sealer)
	if errors.Is(err, ErrIdentityUnreadable) {
		// Nothing is linked, so there is nothing a new key would strand.
		id, err = replaceUnreadableIdentity(ctx, s.st, s.sealer)
	}
	if err != nil {
		return Request{}, err
	}
	c := s.newClient(id)
	info, err := c.site(ctx)
	if err != nil {
		return Request{}, &SiteError{s.siteURL, err}
	}
	if !slices.Contains(info.Protocol, ProtocolVersion) {
		return Request{}, &SiteError{s.siteURL, fmt.Errorf("does not speak protocol %d (it lists %v); update this dashboard", ProtocolVersion, info.Protocol)}
	}

	d := s.desc(ctx)
	resp, err := c.createRequest(ctx, createRequestBody{
		PublicKey: id.PublicKey(), Scope: scope,
		Server: serverDescription{
			Name: fitText(d.Name, 64), Hostname: fitText(d.Hostname, 64), Version: fitText(s.version, 32),
			OS: fitText(d.OS, 64), Arch: fitText(d.Arch, 16),
		},
	})
	if err != nil {
		return Request{}, &SiteError{s.siteURL, err}
	}
	req, err := s.checkRequestResponse(resp, scope, by, driver)
	req.ServerName = fitText(d.Name, 64)
	if err != nil {
		return Request{}, &SiteError{s.siteURL, err}
	}
	if err := s.kv.tx(ctx, func(tx *sql.Tx) error {
		if err := putTx(ctx, tx, keyRequest, req); err != nil {
			return err
		}
		return delTx(ctx, tx, keyOutcome)
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
func (s *Service) checkRequestResponse(r createRequestResponse, scope Scope, by, driver string) (Request, error) {
	switch {
	case !requestIDPattern.MatchString(r.RequestID):
		return Request{}, errors.New("answered with a request id this dashboard does not accept")
	case !userCodePattern.MatchString(r.UserCode):
		return Request{}, errors.New("answered with a code that is not of the form ABCD-1234")
	case !sameOrigin(s.siteURL, r.ApproveURL):
		return Request{}, errors.New("answered with an approval address on another site")
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
	id, _, err := loadIdentity(ctx, s.st, s.sealer)
	if err != nil || id == nil {
		return PollResult{}, fmt.Errorf("link identity: %w", firstErr(err, ErrIdentityUnreadable))
	}
	resp, err := s.newClient(id).pollRequest(ctx, req.ID)
	switch {
	case err == nil:
	case isCode(err, http.StatusGone, "request_denied"):
		s.endRequest(ctx, outcomeDenied, "the request was declined on the site")
		return PollResult{State: PollDenied, Message: "the request was declined on the site"}, nil
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
		const lost = "the site had already handed over this request's approval, but no link was stored on this server; request a new code"
		s.endRequest(ctx, outcomeFailed, lost)
		return PollResult{State: PollFailed, Message: lost}, nil
	case isCode(err, http.StatusUnauthorized, "unknown_server"):
		s.endRequest(ctx, outcomeFailed, "the site no longer knows this request")
		return PollResult{State: PollFailed, Message: "the site no longer knows this request"}, nil
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
	return PollResult{State: PollPending}, fmt.Errorf("the site answered with the status %q", clip(resp.Status, 32))
}

// completeLink stores what an approval carries and ends the request, in one
// transaction, so a link is either there with its keys or not there at all.
func (s *Service) completeLink(ctx context.Context, req Request, r pollResponse) (State, error) {
	switch {
	case !requestIDPattern.MatchString(r.ServerID):
		return State{}, errors.New("the approval names a server id this dashboard does not accept")
	case r.SiteKey == nil || !kidPattern.MatchString(r.SiteKey.Kid):
		return State{}, errors.New("the approval carries no usable site key")
	case r.Owner == nil || strings.TrimSpace(r.Owner.Label) == "":
		return State{}, errors.New("the approval names no account")
	}
	key := SiteKey{Kid: r.SiteKey.Kid, PublicKey: r.SiteKey.PublicKey}
	if raw, err := base64.StdEncoding.DecodeString(key.PublicKey); err != nil || len(raw) != ed25519.PublicKeySize {
		return State{}, errors.New("the approval's site key is not a 32-byte Ed25519 key")
	}
	heartbeat, pollWait := r.HeartbeatSeconds, r.PollWaitSeconds
	if heartbeat == 0 {
		heartbeat = 30
	}
	if pollWait == 0 {
		pollWait = 25
	}
	st := State{
		URL: s.siteURL, ServerID: r.ServerID, SiteKeys: []SiteKey{key}, Scope: req.Scope,
		LinkedAt: s.now(), LinkedBy: req.By, Owner: fitText(r.Owner.Label, 254), ServerName: req.ServerName,
		HeartbeatSeconds: clampInt(heartbeat, 10, 600), PollWaitSeconds: clampInt(pollWait, 1, 25),
	}
	if err := s.kv.tx(ctx, func(tx *sql.Tx) error {
		if err := putTx(ctx, tx, keyState, st); err != nil {
			return err
		}
		return delTx(ctx, tx, keyRequest, keyOutcome, keyStatus, keyPolicy, keyAttempt)
	}); err != nil {
		return State{}, err
	}
	s.log.Info("linked to a just-dashboard.com account", "owner", st.Owner, "scope", st.Scope)
	if s.audit != nil {
		who, actor, method := req.By, "session", "LINK"
		if req.By == "cli" {
			who, actor, method = "root", "cli", "CLI"
		}
		s.audit.Record(ctx, audit.Entry{
			Username: who, Actor: actor, Action: "account.link", Target: st.Owner, Method: method, Status: 200, Success: true,
			Detail: audit.Detail(map[string]any{"scope": st.Scope, "serverId": st.ServerID, "site": s.siteURL}),
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
		if err := delTx(ctx, tx, keyRequest); err != nil {
			return err
		}
		return putTx(ctx, tx, keyOutcome, Outcome{Result: result, At: s.now(), Message: message})
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
				msg += "; the last attempt to ask the site failed: " + lastErr.Error()
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
	if id, _, e := loadIdentity(ctx, s.st, s.sealer); e == nil && id != nil && s.enabled && st.URL == s.siteURL {
		ctx2, cancel := context.WithTimeout(ctx, requestTimeout)
		e = s.newClient(id).unlink(ctx2, st.ServerID)
		cancel()
		siteNotified = e == nil || isCode(e, http.StatusGone, "")
	}
	if err := s.kv.tx(ctx, func(tx *sql.Tx) error {
		return delTx(ctx, tx, keyState, keyRequest, keyStatus, keyPolicy, keyAttempt, keyOutcome)
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
	if st.URL != s.siteURL {
		return ErrSiteChanged
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
