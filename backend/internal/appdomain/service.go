package appdomain

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/safego"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// Link is what this package asks of the account link: whether it is there,
// and the five signed calls (accountlink/domain.go).
type Link interface {
	Enabled() bool
	SiteURL() string
	Linked(ctx context.Context) (accountlink.State, bool, error)
	GetDomain(ctx context.Context) (accountlink.DomainInfo, error)
	PutDomain(ctx context.Context, handle, address string) (accountlink.Domain, error)
	DeleteDomain(ctx context.Context) error
	AddChallenge(ctx context.Context, value string) (accountlink.ChallengeSet, error)
	RemoveChallenge(ctx context.Context, value string) error
}

// Certificates is the wildcard's lineage, which the Certificates feature
// owns (proxysvc/app_domain.go).
type Certificates interface {
	AppDomainCertificate(ctx context.Context, handle, zone string) proxysvc.AppDomainCertificateState
	IssueAppDomainCertificate(ctx context.Context, order proxysvc.AppDomainOrder, log func(stream, text string)) (proxysvc.DeploymentCertificate, error)
	ServeAppDomainRenewal(ctx context.Context, handle string) error
	DeleteAppDomainCertificate(ctx context.Context, handle string) error
}

// Jobs runs an issuance where its transcript can be read.
type Jobs interface {
	Start(spec jobs.Spec, run jobs.Runner) jobs.Job
}

// Options are what a Service is made from.
type Options struct {
	Store *store.Store
	Link  Link
	Certs Certificates
	Jobs  Jobs
	Audit *audit.Logger
	Log   *slog.Logger
	// Enabled is JD_APP_DOMAINS.
	Enabled bool
	// DataDir is where the hook's credential is written.
	DataDir string
	// BackendURL is where the hook reaches this process: its own listener,
	// on loopback.
	BackendURL string
	// PublicAddress is this server's public IPv4 address, or "" when it has
	// none; the first of proxysvc.PublicAddresses by default.
	PublicAddress func() string
	// WaitTXT waits for the zone's nameservers to answer a challenge value;
	// proxysvc.WaitForAuthoritativeTXT by default.
	WaitTXT func(ctx context.Context, name, value string) error
	Now     func() time.Time
}

// Service keeps this server's app domain.
type Service struct {
	st         *store.Store
	link       Link
	certs      Certificates
	jobs       Jobs
	audit      *audit.Logger
	log        *slog.Logger
	enabled    bool
	dataDir    string
	backendURL string
	address    func() string
	waitTXT    func(ctx context.Context, name, value string) error
	now        func() time.Time

	// How often the address is compared and the site asked; tests shorten
	// them.
	addressEvery, refreshEvery time.Duration
	// refreshAtMost is the shortest gap between two GET /domain asked for a
	// page: the site allows 60 an hour.
	refreshAtMost time.Duration
	// txtWait bounds the hook's wait for the zone's nameservers.
	txtWait time.Duration

	// issueMu is one issuance at a time, whoever asks: the page's job, a
	// deployment's certificate step, a claim.
	issueMu sync.Mutex
	mu      sync.Mutex
	issuing bool
	jobID   string
	// refreshedAt is when a page last had the site asked.
	refreshedAt time.Time
	// claimMu is one automatic claim at a time: the loop, a page and the
	// link's approval can all find the same offer.
	claimMu sync.Mutex
	// kick asks the loop to ask the site now: a link was just approved.
	kick chan struct{}
	// linkEvery is how often the loop looks for a link made or replaced by
	// another process (the terminal's --admin link).
	linkEvery time.Duration
	cancel    context.CancelFunc
	done      <-chan struct{}
}

var (
	// ErrDisabled is JD_APP_DOMAINS=false, or linking switched off.
	ErrDisabled = errors.New("just-dashboard.com addresses are switched off on this server (JD_APP_DOMAINS=false)")
	// ErrNoAddress is a server with no public IPv4 address of its own.
	ErrNoAddress = errors.New("this server has no public IPv4 address of its own, so the site has nothing to point a name at")
	// ErrInvalidHandle is a handle that breaks the protocol's syntax.
	ErrInvalidHandle = errors.New("a handle is 3 to 20 lowercase letters, digits or dashes, starting and ending with a letter or digit, with no two dashes in a row")
	// ErrNoDomain is an action that needs a handle this server holds.
	ErrNoDomain = errors.New("this server holds no active just-dashboard.com address")
	// ErrNotThisDomain is a challenge for a name this server's handle is not.
	ErrNotThisDomain = errors.New("the challenge is for a name this server's just-dashboard.com address does not cover")
	// ErrUnavailable is a site that does not offer app domains.
	ErrUnavailable = errors.New("just-dashboard.com does not offer addresses at the moment")
)

// InUseError is a handle that deployments still use, so it cannot be
// released or changed.
type InUseError struct{ Uses []Use }

func (e *InUseError) Error() string {
	names := make([]string, 0, len(e.Uses))
	for _, use := range e.Uses {
		names = append(names, use.Hostname+" ("+use.Project+", "+use.Environment+")")
	}
	return "the address is in use by " + strings.Join(names, ", ") + "; remove those names from their deployments first"
}

// New makes the service. It starts nothing.
func New(o Options) *Service {
	s := &Service{
		st: o.Store, link: o.Link, certs: o.Certs, jobs: o.Jobs, audit: o.Audit, log: o.Log,
		enabled: o.Enabled && o.Link != nil && o.Link.Enabled(), dataDir: o.DataDir, backendURL: o.BackendURL,
		address: o.PublicAddress, waitTXT: o.WaitTXT, now: o.Now,
		addressEvery: 15 * time.Minute, refreshEvery: time.Hour, refreshAtMost: time.Minute, txtWait: 120 * time.Second,
		linkEvery: 30 * time.Second, kick: make(chan struct{}, 1),
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.address == nil {
		s.address = func() string { return FirstPublicIPv4(proxysvc.PublicAddresses()) }
	}
	if s.waitTXT == nil {
		s.waitTXT = proxysvc.WaitForAuthoritativeTXT
	}
	if s.now == nil {
		s.now = func() time.Time { return time.Now().UTC() }
	}
	return s
}

// FirstPublicIPv4 is the first globally routable IPv4 address of addresses.
func FirstPublicIPv4(addresses []string) string {
	for _, address := range addresses {
		if ip := net.ParseIP(address); ip != nil && ip.To4() != nil && proxysvc.IsPublicAddress(ip) {
			return address
		}
	}
	return ""
}

// PublicAddress is the address a claim would publish, or "".
func (s *Service) PublicAddress() string { return s.address() }

// Enabled is JD_APP_DOMAINS with the account link permitted.
func (s *Service) Enabled() bool { return s != nil && s.enabled }

// Start runs the background work: the address follow-up at start-up and
// every 15 minutes, and GET /domain hourly.
func (s *Service) Start(ctx context.Context) {
	if !s.Enabled() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, s.cancel = context.WithCancel(ctx)
	s.done = safego.Go(ctx, s.log, "app domain", s.run)
}

// Stop ends the background work and waits for it.
func (s *Service) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.cancel, s.done = nil, nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (s *Service) run(ctx context.Context) {
	if st, err := readState(ctx, s.st.DB); err == nil && st.Handle != "" {
		if err := s.ensureHook(ctx); err != nil {
			s.log.Warn("app domain hook credential", "err", err)
		}
	}
	s.refresh(ctx)
	s.followAddress(ctx)
	seen := s.serverID(ctx)
	addresses := time.NewTicker(s.addressEvery)
	refreshes := time.NewTicker(s.refreshEvery)
	links := time.NewTicker(s.linkEvery)
	defer addresses.Stop()
	defer refreshes.Stop()
	defer links.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-addresses.C:
			s.followAddress(ctx)
		case <-refreshes.C:
			s.refresh(ctx)
		case <-s.kick:
			seen = s.serverID(ctx)
			s.refresh(ctx)
		case <-links.C:
			// A link the terminal made, or replaced, reaches this process
			// here: the site may hold an offer for it.
			if now := s.serverID(ctx); now != seen {
				seen = now
				if now != "" {
					s.refresh(ctx)
				}
			}
		}
	}
}

// serverID is the stored link's server id, or "" with no link.
func (s *Service) serverID(ctx context.Context) string {
	st, ok, err := s.link.Linked(ctx)
	if err != nil || !ok {
		return ""
	}
	return st.ServerID
}

// LinkChanged asks the loop to ask the site now, without waiting for it: a
// link was approved (or ended) in this process. It is the account link's
// OnClear hook's follow-up, after its transaction has committed.
func (s *Service) LinkChanged() {
	if !s.Enabled() {
		return
	}
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// linked reports the account link being stored. The calls themselves say
// when a stored link cannot be used (idle, another site).
func (s *Service) linked(ctx context.Context) bool {
	_, ok, err := s.link.Linked(ctx)
	return err == nil && ok
}

// followAddress sends PUT /domain when the server's public address is no
// longer the one the records point at.
func (s *Service) followAddress(ctx context.Context) {
	st, err := readState(ctx, s.st.DB)
	if err != nil || !st.held() {
		return
	}
	address := s.address()
	if address == "" || address == st.Address {
		return
	}
	domain, err := s.link.PutDomain(ctx, st.Handle, address)
	if err != nil {
		s.noteError(ctx, err)
		return
	}
	_, _ = update(ctx, s.st.DB, func(cur *State) (bool, error) {
		if cur.Handle != st.Handle {
			return false, nil
		}
		s.mirror(cur, domain)
		cur.LastError = ""
		return true, nil
	})
	s.record(ctx, "app.domain.address", st.Hostname, true, map[string]any{"from": st.Address, "to": domain.Address})
}

// refresh asks the site what it holds for this server, and records what
// changed there: a suspension, a release on the site, an unlink.
func (s *Service) refresh(ctx context.Context) {
	if !s.linked(ctx) {
		s.detachIfHeld(ctx, "The account link is not active, so the site no longer publishes this server's records.")
		return
	}
	info, err := s.link.GetDomain(ctx)
	if errors.Is(err, accountlink.ErrNotLinked) {
		s.detachIfHeld(ctx, "The server was removed from its just-dashboard.com account, so the site released its handle.")
		return
	}
	if err != nil {
		s.noteError(ctx, err)
		return
	}
	before, _ := readState(ctx, s.st.DB)
	after, err := update(ctx, s.st.DB, func(cur *State) (bool, error) {
		cur.LastCheckedAt, cur.LastError = s.now().Unix(), ""
		cur.Available = ptr(info.Available)
		cur.AvailableReason = ""
		if info.Reason != nil {
			cur.AvailableReason = *info.Reason
		}
		if info.Zone != nil && *info.Zone != "" {
			cur.Zone = strings.ToLower(*info.Zone)
		}
		cur.Offer = ""
		if info.Offer != nil && info.Domain == nil {
			cur.Offer = *info.Offer
		}
		switch {
		case info.Domain != nil:
			// The site's record is the truth: a suspension, an unsuspension,
			// and a handle this server holds there but had not stored
			// (a restore of an older store, a claim from another process).
			s.mirror(cur, *info.Domain)
		case cur.Handle != "" && cur.Status != StatusDetached:
			cur.Status, cur.UpdatedAt = StatusDetached, s.now().Unix()
			cur.Reason = "The site no longer lists this handle for this server: it was released on the site, or by an administrator there."
		}
		return true, nil
	})
	if err == nil && before.Status != after.Status && after.Handle != "" {
		s.record(ctx, "app.domain."+after.Status, after.Hostname, true, map[string]any{"reason": after.Reason})
	}
	if err == nil {
		s.autoClaim(ctx, after)
	}
}

// autoRetry is how long a handle whose automatic claim failed is left
// before it is tried again: the site allows ten PUT /domain an hour for a
// server and five new handles a day for an account.
const autoRetry = time.Hour

// autoClaim claims the handle the account chose for this server on the site,
// exactly as the Settings card's Claim does, when the feature is on, the
// site offers addresses, the server holds none, and the site named one. A
// person chose it, on the approval page or the server's page on the site;
// an offer reserves nothing, and the claim is the ordinary signed PUT from
// this server's own address.
func (s *Service) autoClaim(ctx context.Context, st State) {
	if !s.Enabled() || st.Offer == "" || st.holdsDomain() || st.Available == nil || !*st.Available {
		return
	}
	if st.OfferTried == st.Offer && st.OfferError != "" && s.now().Sub(time.Unix(st.OfferTriedAt, 0)) < autoRetry {
		return
	}
	if !s.claimMu.TryLock() {
		return
	}
	defer s.claimMu.Unlock()
	offer := st.Offer
	if _, err := update(ctx, s.st.DB, func(cur *State) (bool, error) {
		cur.OfferTried, cur.OfferTriedAt, cur.OfferError = offer, s.now().Unix(), ""
		return true, nil
	}); err != nil {
		return
	}
	view, err := s.Claim(ctx, offer, "system")
	if err != nil {
		_, _ = update(context.WithoutCancel(ctx), s.st.DB, func(cur *State) (bool, error) {
			if cur.OfferTried != offer {
				return false, nil
			}
			cur.OfferError = claimSentence(err)
			return true, nil
		})
		s.record(ctx, "app.domain.claim", offer, false, map[string]any{"automatic": true, "error": claimSentence(err)})
		return
	}
	target := offer
	if view.Domain != nil {
		target = view.Domain.Hostname
	}
	s.record(ctx, "app.domain.claim", target, true, map[string]any{"automatic": true, "address": s.address()})
}

// claimSentence is a failed claim in words for the Settings card.
func claimSentence(err error) string {
	var site *accountlink.DomainError
	if errors.As(err, &site) {
		return site.Message
	}
	msg := err.Error()
	if msg != "" {
		msg = strings.ToUpper(msg[:1]) + msg[1:]
	}
	if !strings.HasSuffix(msg, ".") {
		msg += "."
	}
	return msg
}

// Refresh asks the site now, for the Settings page, at most once a minute.
func (s *Service) Refresh(ctx context.Context) {
	if !s.Enabled() {
		return
	}
	s.mu.Lock()
	if s.now().Sub(s.refreshedAt) < s.refreshAtMost {
		s.mu.Unlock()
		return
	}
	s.refreshedAt = s.now()
	s.mu.Unlock()
	s.refresh(ctx)
}

// mirror copies the site's record of the domain onto the state.
func (s *Service) mirror(cur *State, d accountlink.Domain) {
	cur.Handle, cur.Hostname, cur.Address = d.Handle, strings.ToLower(d.Hostname), d.Address
	cur.Zone = strings.TrimPrefix(cur.Hostname, d.Handle+".")
	cur.Status, cur.Reason = d.Status, ""
	if d.Reason != nil {
		cur.Reason = *d.Reason
	}
	cur.UpdatedAt = d.UpdatedAt
}

func (s *Service) detachIfHeld(ctx context.Context, why string) {
	st, err := update(ctx, s.st.DB, func(cur *State) (bool, error) {
		if cur.Handle == "" || cur.Status == StatusDetached {
			return false, nil
		}
		cur.Status, cur.Reason, cur.UpdatedAt = StatusDetached, why, s.now().Unix()
		return true, nil
	})
	if err == nil && st.Status == StatusDetached && st.Reason == why {
		s.record(ctx, "app.domain.detached", st.Hostname, true, map[string]any{"reason": why})
	}
}

func (s *Service) noteError(ctx context.Context, err error) {
	_, _ = update(ctx, s.st.DB, func(cur *State) (bool, error) {
		msg := err.Error()
		if cur.LastError == msg {
			return false, nil
		}
		cur.LastError = msg
		return true, nil
	})
}

// zone is the app domain zone: the site's word for it once it has said, else
// the account site's own host, which is the zone a stock site uses.
func (s *Service) zone(st State) string {
	if st.Zone != "" {
		return st.Zone
	}
	if s.link == nil {
		return ""
	}
	u, err := url.Parse(s.link.SiteURL())
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if !strings.Contains(host, ".") || net.ParseIP(host) != nil {
		return ""
	}
	return host
}

// Claim takes handle for this server at its public address, or changes this
// server's handle to it. The wildcard is then issued in the background.
func (s *Service) Claim(ctx context.Context, handle, by string) (View, error) {
	if !s.Enabled() {
		return View{}, ErrDisabled
	}
	handle = strings.ToLower(strings.TrimSpace(handle))
	if !accountlink.ValidHandle(handle) {
		return View{}, ErrInvalidHandle
	}
	if !s.linked(ctx) {
		return View{}, accountlink.ErrNotLinked
	}
	address := s.address()
	if address == "" {
		return View{}, ErrNoAddress
	}
	st, err := readState(ctx, s.st.DB)
	if err != nil {
		return View{}, err
	}
	if st.Handle != "" && st.Handle != handle {
		uses, err := s.UsedBy(ctx, st)
		if err != nil {
			return View{}, err
		}
		if len(uses) > 0 {
			return View{}, &InUseError{Uses: uses}
		}
	}
	domain, err := s.link.PutDomain(ctx, handle, address)
	if err != nil {
		return View{}, err
	}
	previous := st.Handle
	st, err = update(ctx, s.st.DB, func(cur *State) (bool, error) {
		s.mirror(cur, domain)
		cur.LastError, cur.CertificateError = "", ""
		// The site clears an offer once the server's domain is active.
		cur.Offer, cur.OfferError = "", ""
		return true, nil
	})
	if err != nil {
		return View{}, err
	}
	if err := s.ensureHook(ctx); err != nil {
		return View{}, fmt.Errorf("the handle is claimed, but the certificate hook's credential could not be written: %w", err)
	}
	if previous != "" && previous != handle && s.certs != nil {
		// The old handle's wildcard covers nothing this server holds now.
		if err := s.certs.DeleteAppDomainCertificate(context.WithoutCancel(ctx), previous); err != nil {
			s.log.Warn("could not remove the previous app domain certificate", "handle", previous, "err", err)
		}
	}
	if st.held() {
		s.startIssue(ctx, IssueOptions{}, by)
	}
	return s.View(ctx)
}

// Release gives the handle back. A handle deployments still use is refused.
func (s *Service) Release(ctx context.Context) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	st, err := readState(ctx, s.st.DB)
	if err != nil || st.Handle == "" {
		return err
	}
	uses, err := s.UsedBy(ctx, st)
	if err != nil {
		return err
	}
	if len(uses) > 0 {
		return &InUseError{Uses: uses}
	}
	// A detached handle is already released on the site; one held is
	// released there first, so the records go before the state does.
	if st.Status != StatusDetached && s.linked(ctx) {
		if err := s.link.DeleteDomain(ctx); err != nil && !errors.Is(err, accountlink.ErrNotLinked) {
			return err
		}
	}
	if _, err := update(ctx, s.st.DB, func(cur *State) (bool, error) {
		*cur = State{Revision: cur.Revision, AutoAssign: cur.AutoAssign, Zone: cur.Zone, Available: cur.Available, AvailableReason: cur.AvailableReason}
		return true, nil
	}); err != nil {
		return err
	}
	if s.certs != nil {
		if err := s.certs.DeleteAppDomainCertificate(context.WithoutCancel(ctx), st.Handle); err != nil {
			s.log.Warn("could not remove the app domain certificate", "handle", st.Handle, "err", err)
		}
	}
	return nil
}

// SetAutoAssign is whether new deployments get the address by default.
func (s *Service) SetAutoAssign(ctx context.Context, on bool) (View, error) {
	if !s.Enabled() {
		return View{}, ErrDisabled
	}
	if _, err := update(ctx, s.st.DB, func(cur *State) (bool, error) {
		if cur.AutoAssign == on {
			return false, nil
		}
		cur.AutoAssign = on
		return true, nil
	}); err != nil {
		return View{}, err
	}
	return s.View(ctx)
}

// View is the Settings page's answer.
func (s *Service) View(ctx context.Context) (View, error) {
	st, err := readState(ctx, s.st.DB)
	if err != nil {
		return View{}, err
	}
	v := View{Enabled: s.Enabled(), AutoAssign: st.AutoAssign, UsedBy: []Use{}, Detached: []Use{}}
	v.Linked = s.link != nil && s.linked(ctx)
	v.PublicAddress = strPtr(s.address())
	if zone := s.zone(st); zone != "" {
		v.Zone = &zone
	}
	switch {
	case !v.Enabled:
		v.Reason = strPtr(ErrDisabled.Error() + ".")
	case !v.Linked:
		v.Reason = strPtr("This server is not linked to a just-dashboard.com account. Link it first; the address is held through the link.")
	case st.Available == nil && st.LastError != "":
		v.Reason = strPtr("The site could not be asked: " + st.LastError)
	case st.Available == nil:
		v.Reason = strPtr("The site has not been asked yet.")
	default:
		v.Available = st.Available
		if !*st.Available {
			v.Reason = strPtr(st.AvailableReason)
			v.Zone = nil
		} else if st.LastError != "" {
			v.Reason = strPtr(st.LastError)
		}
	}
	if st.Handle != "" {
		v.Domain = &DomainView{
			Handle: st.Handle, Hostname: st.Hostname, Wildcard: "*." + st.Hostname, Address: st.Address,
			Status: st.Status, Reason: strPtr(st.Reason), UpdatedAt: st.UpdatedAt,
		}
		v.Certificate = s.certificate(ctx, st)
		if v.UsedBy, err = s.UsedBy(ctx, st); err != nil {
			return View{}, err
		}
	}
	if v.Detached, err = s.DetachedNames(ctx, st); err != nil {
		return View{}, err
	}
	if v.Enabled && st.Offer != "" && !st.holdsDomain() {
		v.Offer = &OfferView{Handle: st.Offer, Hostname: st.Offer + "." + s.zone(st)}
		if st.OfferTried == st.Offer {
			v.Offer.Error = strPtr(st.OfferError)
		}
	}
	return v, nil
}

// Names is what a deployment page needs to place a hostname.
func (s *Service) Names(ctx context.Context) Names {
	out := Names{Enabled: s.Enabled()}
	st, err := readState(ctx, s.st.DB)
	if err != nil {
		return out
	}
	out.AutoAssign = st.AutoAssign
	out.Zone = strPtr(s.zone(st))
	out.Status = strPtr(st.Status)
	if s.Enabled() && st.held() {
		out.Base = strPtr(st.base())
	}
	return out
}

// Active is the base of every app name, <handle>.<zone>, while the domain is
// active, and whether new deployments get it by default.
func (s *Service) Active(ctx context.Context) (base string, autoAssign, ok bool) {
	if !s.Enabled() {
		return "", false, false
	}
	st, err := readState(ctx, s.st.DB)
	if err != nil || !st.held() {
		return "", false, false
	}
	return st.base(), st.AutoAssign, true
}

// NameState is how a hostname stands against the app domain.
type NameState int

const (
	// NotAppDomain is a name outside the zone.
	NotAppDomain NameState = iota
	// Held is a name under the handle this server holds and may use.
	Held
	// Detached is a name under the zone that this server's handle does
	// not cover now: released, suspended, detached, renamed, or never ours.
	Detached
)

// Classify places hostname.
func (s *Service) Classify(ctx context.Context, hostname string) NameState {
	if s == nil || s.st == nil {
		return NotAppDomain
	}
	hostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hostname), "."))
	st, err := readState(ctx, s.st.DB)
	if err != nil {
		return NotAppDomain
	}
	zone := s.zone(st)
	if zone == "" || (hostname != zone && !strings.HasSuffix(hostname, "."+zone)) {
		return NotAppDomain
	}
	if s.Enabled() && st.held() {
		if base := st.base(); hostname == base || singleLabelUnder(hostname, base) {
			return Held
		}
	}
	return Detached
}

// Zone is the app domain zone, or "" when none is known.
func (s *Service) Zone(ctx context.Context) string {
	if s == nil || s.st == nil {
		return ""
	}
	st, err := readState(ctx, s.st.DB)
	if err != nil {
		return ""
	}
	return s.zone(st)
}

func singleLabelUnder(hostname, base string) bool {
	prefix, ok := strings.CutSuffix(hostname, "."+base)
	return ok && prefix != "" && !strings.Contains(prefix, ".")
}

// record writes an audit entry for what the service did on its own.
func (s *Service) record(ctx context.Context, action, target string, ok bool, detail map[string]any) {
	if s.audit == nil {
		return
	}
	s.audit.Record(context.WithoutCancel(ctx), audit.Entry{
		Username: "system", Actor: "system", Action: action, Target: target,
		Method: "APPDOMAIN", Status: 200, Success: ok, Detail: audit.Detail(detail),
	})
}

// usesQuery lists the configured deployment domains (release 0 is the
// desired configuration) whose name ends in a suffix, on environments and
// projects that are not archived.
const usesQuery = `
	SELECT DISTINCT p.id, p.name, e.id, e.name, lower(d.resource_id)
	  FROM deploy_dependencies d
	  JOIN deploy_environments e ON e.id = d.environment_id
	  JOIN deploy_projects p ON p.id = e.project_id
	 WHERE d.kind = 'domain' AND d.release_id = 0 AND p.archived_at = 0 AND e.archived_at = 0
	   AND (lower(d.resource_id) = ? OR lower(d.resource_id) LIKE ?)
	 ORDER BY p.name, e.name, lower(d.resource_id)`

func (s *Service) uses(ctx context.Context, suffix string, keep func(string) bool) ([]Use, error) {
	out := []Use{}
	if suffix == "" {
		return out, nil
	}
	rows, err := s.st.DB.QueryContext(ctx, usesQuery, suffix, "%."+suffix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var u Use
		if err := rows.Scan(&u.ProjectID, &u.Project, &u.EnvironmentID, &u.Environment, &u.Hostname); err != nil {
			return nil, err
		}
		if keep == nil || keep(u.Hostname) {
			out = append(out, u)
		}
	}
	return out, rows.Err()
}

// UsedBy lists every deployment domain under the state's handle, across
// projects and environments.
func (s *Service) UsedBy(ctx context.Context, st State) ([]Use, error) {
	return s.uses(ctx, st.base(), nil)
}

// DetachedNames lists the deployment domains under the zone that the state's
// handle does not cover now.
func (s *Service) DetachedNames(ctx context.Context, st State) ([]Use, error) {
	zone := s.zone(st)
	if zone == "" {
		return []Use{}, nil
	}
	held := s.Enabled() && st.held()
	base := st.base()
	return s.uses(ctx, zone, func(hostname string) bool {
		return !held || (hostname != base && !singleLabelUnder(hostname, base))
	})
}

// UsedByAny lists every deployment name under the zone, held or not: what
// stops resolving when the server is unlinked.
func (s *Service) UsedByAny(ctx context.Context) ([]Use, error) {
	st, err := readState(ctx, s.st.DB)
	if err != nil {
		return nil, err
	}
	if st.Handle == "" {
		return []Use{}, nil
	}
	return s.UsedBy(ctx, st)
}

// state is readState over the service's store.
func (s *Service) state(ctx context.Context) (State, error) { return readState(ctx, s.st.DB) }
