package accountlink

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

var (
	ErrDisabled      = errors.New("linking to a just-dashboard.com account is switched off by JD_ACCOUNT_LINK=false")
	ErrAlreadyLinked = errors.New("this server is already linked to a just-dashboard.com account")
	ErrLinkPending   = errors.New("a link request is already waiting for approval")
	ErrNotLinked     = errors.New("this server is not linked to a just-dashboard.com account")
	ErrBadScope      = errors.New("scope must be observe, operate or maintain")
	ErrNoHost        = errors.New("this process cannot read the server's state")
	// ErrSiteChanged is a link made to one site while JD_ACCOUNT_URL now names
	// another. The link's keys and its server id mean something only to the
	// site it was made with, so nothing is sent to the new one on its account.
	ErrSiteChanged = errors.New("this server is linked to a different site than JD_ACCOUNT_URL now names; set it back or unlink")
)

// Options are what a Service is made from. Everything but the store and the
// sealer has a default, which is what the terminal commands use: they link and
// unlink and never run the connector, so they need no Host.
type Options struct {
	Store  *store.Store
	Sealer *auth.Sealer
	Audit  *audit.Logger
	Log    *slog.Logger
	// SiteURL is JD_ACCOUNT_URL after ParseSiteURL.
	SiteURL string
	// Enabled is JD_ACCOUNT_LINK. Off, nothing is ever sent and linking is
	// refused; an existing link is left in place but idle.
	Enabled bool
	Host    Host
	Version string
	// Describe names this server to the site when a link is requested.
	Describe func(context.Context) ServerDescription
	// HTTP and Now are for tests.
	HTTP *http.Client
	Now  func() time.Time
	// Location is the clock the update window is read on; the server's own
	// time zone by default.
	Location *time.Location
}

// ServerDescription is what a link request tells the site about this machine.
type ServerDescription struct {
	Name, Hostname, OS, Arch string
}

// Service is the connector: the link flow, the heartbeat, the command loop and
// the update policy. Its state is in the dashboard's store, so a link begun at
// the terminal is picked up by the running dashboard without a restart.
type Service struct {
	st      *store.Store
	sealer  *auth.Sealer
	audit   *audit.Logger
	log     *slog.Logger
	kv      kv
	siteURL string
	enabled bool
	host    Host
	version string
	desc    func(context.Context) ServerDescription
	http    *http.Client
	now     func() time.Time
	loc     *time.Location

	// How often the loops look at the stored state while nothing is linked,
	// and how often a running link checks that it still is one. Tests shorten
	// them.
	idle  time.Duration
	watch time.Duration
	// pollEvery overrides the interval the site asked a pending request be
	// polled at; zero means use the site's.
	pollEvery time.Duration
	// The bounds of the wait after a failed exchange.
	backoffMin, backoffMax time.Duration
	// First and later evaluations of the update policy. Once a minute, because
	// a window can be shorter than any longer cadence: one that is checked
	// hourly at a fixed minute can miss a half-hour window every day.
	policyFirst, policyEvery time.Duration

	wake chan struct{}
	kick chan struct{}

	hbMu    sync.Mutex
	hbTimes []time.Time
	// hbPerMinute is the site's limit on heartbeats for one server.
	hbPerMinute int

	mu           sync.Mutex
	lastSnapshot []byte
	linkedCancel context.CancelFunc
	cancel       context.CancelFunc
	done         chan struct{}
	interrupted  sync.Once
}

func New(o Options) *Service {
	s := &Service{
		st: o.Store, sealer: o.Sealer, audit: o.Audit, log: o.Log, kv: kv{o.Store},
		siteURL: o.SiteURL, enabled: o.Enabled, host: o.Host, version: o.Version,
		desc: o.Describe, http: o.HTTP, now: o.Now, loc: o.Location,
		idle: 15 * time.Second, watch: 5 * time.Second,
		backoffMin: time.Second, backoffMax: 5 * time.Minute,
		policyFirst: time.Minute, policyEvery: time.Minute,
		wake: make(chan struct{}, 1), kick: make(chan struct{}, 1),
		hbPerMinute: maxHeartbeatsPerMinute,
	}
	if s.siteURL == "" {
		s.siteURL = DefaultSiteURL
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.http == nil {
		s.http = NewHTTPClient()
	}
	if s.now == nil {
		s.now = func() time.Time { return time.Now().UTC() }
	}
	if s.loc == nil {
		s.loc = time.Local
	}
	if s.desc == nil {
		s.desc = describeHost
	}
	if s.version == "" {
		s.version = "unknown"
	}
	return s
}

// Enabled reports JD_ACCOUNT_LINK.
func (s *Service) Enabled() bool { return s != nil && s.enabled }

// SiteURL is the origin this service talks to.
func (s *Service) SiteURL() string { return s.siteURL }

func (s *Service) newClient(id *Identity) *Client {
	return &Client{
		origin: s.siteURL, http: s.http, id: id, next: s.kv.nextSequence,
		now: s.now, version: s.version,
	}
}

// ---------------------------------------------------------------- lifecycle --

// Start runs the connector in the background until ctx ends or Stop is called.
// With linking switched off it runs nothing at all and opens no connection.
func (s *Service) Start(ctx context.Context) {
	if s == nil || !s.enabled || s.host == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, s.cancel = context.WithCancel(ctx)
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		s.run(ctx)
	}()
}

// Stop ends the connector and waits for it.
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

func (s *Service) nudge() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) kickHeartbeat() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// pause waits for d, for a nudge, or for the context.
func (s *Service) pause(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	case <-s.wake:
	}
}

// sleep waits for d or for the context, and nothing else.
func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// run is the top loop. It looks at the stored state each time round, which is
// how a link made by the terminal command starts the heartbeat and an unlink
// stops it, without anything being restarted.
func (s *Service) run(ctx context.Context) {
	for ctx.Err() == nil {
		st, linked, err := s.kv.state(ctx)
		switch {
		case err != nil:
			s.log.Warn("account link state is unreadable", "err", err)
			s.pause(ctx, s.idle)
		case linked && st.URL != s.siteURL:
			s.setHeartbeatError(ctx, fmt.Sprintf("this server is linked to %s but JD_ACCOUNT_URL is now %s; set it back or unlink", st.URL, s.siteURL))
			s.pause(ctx, s.idle)
		case linked:
			s.runLinked(ctx, st)
		default:
			s.runUnlinked(ctx)
		}
	}
}

// runUnlinked polls a link request this process owns, and otherwise waits for
// something to change.
func (s *Service) runUnlinked(ctx context.Context) {
	req, ok, err := s.kv.request(ctx)
	if err != nil || !ok {
		s.pause(ctx, s.idle)
		return
	}
	if !s.now().Before(req.ExpiresAt) {
		s.endRequest(ctx, outcomeExpired, "the request was not approved in time")
		return
	}
	if req.Driver != driverDashboard {
		s.pause(ctx, s.idle)
		return
	}
	res, err := s.PollLink(ctx)
	if err != nil {
		s.log.Debug("link request poll failed", "err", err)
	}
	if err == nil && res.State != PollPending {
		return
	}
	wait := time.Duration(clampInt(req.PollSeconds, 2, 30)) * time.Second
	if s.pollEvery > 0 {
		wait = s.pollEvery
	}
	s.pause(ctx, wait)
}

func (s *Service) runLinked(ctx context.Context, st State) {
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.linkedCancel = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		s.linkedCancel = nil
		s.mu.Unlock()
	}()

	var wg sync.WaitGroup
	for _, loop := range []func(context.Context, State){s.heartbeatLoop, s.commandLoop, s.policyLoop, s.watchLoop} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A loop that returns on its own means the link is over or has
			// changed; the others have no reason to go on.
			defer cancel()
			loop(ctx, st)
		}()
	}
	wg.Wait()
}

// watchLoop ends a running link when the stored state stops being it: an unlink
// from the terminal, a re-link, or the site removing the server.
func (s *Service) watchLoop(ctx context.Context, st State) {
	for {
		sleep(ctx, s.watch)
		if ctx.Err() != nil {
			return
		}
		if _, ok := s.currentLink(ctx, st); !ok {
			return
		}
	}
}

// currentLink is the stored state if it is still the link st describes.
func (s *Service) currentLink(ctx context.Context, st State) (State, bool) {
	cur, ok, err := s.kv.state(ctx)
	if err != nil {
		// A read that failed says nothing about the link; keep going.
		return st, true
	}
	if !ok || cur.ServerID != st.ServerID || cur.URL != s.siteURL {
		return State{}, false
	}
	return cur, true
}

// cancelLinked stops the loops of a running link from inside this process.
func (s *Service) cancelLinked() {
	s.mu.Lock()
	cancel := s.linkedCancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// --------------------------------------------------------------- the loops --

type backoff struct {
	min, max, cur time.Duration
}

func (b *backoff) next() time.Duration {
	if b.cur == 0 {
		b.cur = b.min
	} else {
		b.cur = min(b.cur*2, b.max)
	}
	return jitter(b.cur)
}

func (b *backoff) reset() { b.cur = 0 }

func jitter(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
}

func (s *Service) heartbeatLoop(ctx context.Context, st State) {
	bo := backoff{min: s.backoffMin, max: s.backoffMax}
	for ctx.Err() == nil {
		cur, ok := s.currentLink(ctx, st)
		if !ok {
			return
		}
		wait := time.Duration(clampInt(cur.HeartbeatSeconds, 10, 600)) * time.Second
		if err := s.sendHeartbeat(ctx, cur, true); err != nil {
			if s.linkEnded(ctx, cur, err) {
				return
			}
			wait = bo.next()
		} else {
			bo.reset()
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
		case <-t.C:
		case <-s.kick:
		}
		t.Stop()
	}
}

func (s *Service) commandLoop(ctx context.Context, st State) {
	s.interrupted.Do(func() { s.closeInterrupted(ctx, st) })
	bo := backoff{min: s.backoffMin, max: s.backoffMax}
	for ctx.Err() == nil {
		cur, ok := s.currentLink(ctx, st)
		if !ok {
			return
		}
		began := time.Now()
		resp, err := s.pollCommands(ctx, cur)
		if err != nil {
			if ctx.Err() != nil || s.linkEnded(ctx, cur, err) {
				return
			}
			s.setPollError(ctx, describeError(err))
			sleep(ctx, bo.next())
			continue
		}
		bo.reset()
		s.setPollError(ctx, "")
		for _, cmd := range resp.Commands {
			if ctx.Err() != nil {
				return
			}
			// Run against the state as it is now: a scope narrowed since the
			// poll began applies to this command.
			now, ok := s.currentLink(ctx, st)
			if !ok {
				return
			}
			s.process(ctx, now, cmd)
		}
		// A site that answers an empty poll at once would otherwise be asked
		// again as fast as the network allows.
		if len(resp.Commands) == 0 && time.Since(began) < time.Second {
			sleep(ctx, time.Second)
		}
	}
}

func (s *Service) pollCommands(ctx context.Context, st State) (commandsResponse, error) {
	id, _, err := loadIdentity(ctx, s.st, s.sealer)
	if err != nil || id == nil {
		return commandsResponse{}, fmt.Errorf("link identity: %w", firstErr(err, ErrIdentityUnreadable))
	}
	return s.newClient(id).commands(ctx, st.ServerID, clampInt(st.PollWaitSeconds, 1, 25))
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// closeInterrupted settles the commands a previous process was running when it
// stopped, which will never finish.
func (s *Service) closeInterrupted(ctx context.Context, st State) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT id FROM account_link_commands WHERE status = 'running'`)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	const why = "the dashboard restarted before this command's result was sent; its effect, if any, shows in the next snapshot"
	for _, id := range ids {
		s.finishRecord(ctx, id, resultFailed, why, s.now())
		s.report(ctx, st, id, CommandResult{Status: resultFailed, StartedAt: s.now().Unix(), FinishedAt: s.now().Unix(), Error: why})
	}
}

func (s *Service) policyLoop(ctx context.Context, st State) {
	wait := s.policyFirst
	for {
		sleep(ctx, wait)
		if ctx.Err() != nil {
			return
		}
		wait = s.policyEvery
		if cur, ok := s.currentLink(ctx, st); ok {
			s.evaluatePolicy(ctx, cur)
		}
	}
}

// evaluatePolicy installs an update when the policy says one is due. A version
// is tried once: an install that failed is not started again every hour, and a
// new policy or a newer version is what lets it try again.
func (s *Service) evaluatePolicy(ctx context.Context, st State) {
	p, ok := s.policy(ctx)
	if !ok || p.Auto == AutoOff || !st.Scope.Includes(ScopeMaintain) {
		return
	}
	// Outside the window there is nothing to decide, and this runs every
	// minute: the machine is not asked anything until the window is open.
	if !p.Window.contains(s.now().In(s.loc)) {
		return
	}
	status, err := s.host.UpdateStatus(ctx)
	if err != nil {
		s.log.Debug("update policy could not read the update state", "err", err)
		return
	}
	version, due := updateDue(p, status, s.now().In(s.loc))
	if !due {
		return
	}
	var tried string
	_, _ = s.kv.get(ctx, keyAttempt, &tried)
	if tried == version {
		return
	}
	if err := s.kv.put(ctx, keyAttempt, version); err != nil {
		return
	}
	actor := "account:" + st.Owner + " (update policy)"
	_, err = s.host.InstallUpdate(ctx, actor, version)
	if s.audit != nil {
		detail := map[string]any{"from": status.Version, "to": version, "policy": p.Auto}
		if err != nil {
			detail["error"] = clip(err.Error(), 300)
		}
		s.audit.Record(ctx, audit.Entry{
			Username: "account:" + st.Owner, Actor: "system", Action: "account.update.auto", Target: version,
			Method: "LINK", Status: map[bool]int{true: 200, false: 500}[err == nil], Success: err == nil,
			Detail: audit.Detail(detail),
		})
	}
	if err != nil {
		s.log.Warn("automatic update did not start", "version", version, "err", err)
	}
}

// ------------------------------------------------------------ the heartbeat --

// maxHeartbeatsPerMinute is the site's limit for one server.
const maxHeartbeatsPerMinute = 4

// ErrTooSoon is a heartbeat refused locally because the site would refuse it.
var ErrTooSoon = errors.New("heartbeats are limited to four a minute; try again shortly")

// sendHeartbeat reads the machine, redacts it to the scope, sends it and acts
// on the answer. patient waits for the rate window; an impatient caller (a
// button in a browser) gives up instead.
func (s *Service) sendHeartbeat(ctx context.Context, st State, patient bool) error {
	if s.host == nil {
		return ErrNoHost
	}
	if st.URL != s.siteURL {
		return ErrSiteChanged
	}
	if patient {
		s.hbMu.Lock()
	} else if !s.hbMu.TryLock() {
		return nil
	}
	defer s.hbMu.Unlock()

	if wait := s.heartbeatWait(); wait > 0 {
		if !patient {
			return ErrTooSoon
		}
		sleep(ctx, wait)
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	id, _, err := loadIdentity(ctx, s.st, s.sealer)
	if err != nil || id == nil {
		err = fmt.Errorf("link identity: %w", firstErr(err, ErrIdentityUnreadable))
		s.setHeartbeatError(ctx, describeError(err))
		return err
	}
	snap, err := s.host.Collect(ctx)
	if err != nil {
		s.log.Debug("snapshot is incomplete", "err", err)
	}
	// The scope may have been narrowed while this waited for the rate window or
	// read the machine, which can take most of a minute. What is sent is cut
	// to the scope as it is now, and says so.
	cur, still := s.currentLink(ctx, st)
	if !still {
		return ErrNotLinked
	}
	st = cur
	snap.Protocol = ProtocolVersion
	snap.SentAt = s.now().Unix()
	snap.Server.Scope = st.Scope
	if p, ok := s.policy(ctx); ok && p.Auto != AutoOff {
		snap.Server.UpdatePolicy = &p
	}
	body, err := snap.Redact(st.Scope).encode()
	if err != nil {
		return err
	}
	s.hbTimes = append(s.hbTimes, time.Now())
	resp, err := s.newClient(id).heartbeat(ctx, st.ServerID, body)
	if err != nil {
		s.setHeartbeatError(ctx, describeError(err))
		return err
	}
	s.mu.Lock()
	s.lastSnapshot = body
	s.mu.Unlock()
	s.noteHeartbeat(ctx, st, resp)
	return nil
}

// heartbeatWait is how long to hold off to stay inside the site's rate limit.
// It is pacing, so it runs on the real clock rather than the protocol's.
func (s *Service) heartbeatWait() time.Duration {
	now := time.Now()
	cutoff := now.Add(-61 * time.Second)
	kept := s.hbTimes[:0]
	for _, t := range s.hbTimes {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	s.hbTimes = kept
	if len(kept) < s.hbPerMinute {
		return 0
	}
	return kept[len(kept)-s.hbPerMinute].Add(61 * time.Second).Sub(now)
}

// noteHeartbeat records a good exchange and takes what the site announced in
// it: a successor key, and how often it wants to hear from this server.
func (s *Service) noteHeartbeat(ctx context.Context, st State, resp heartbeatResponse) {
	now := s.now()
	// Read and written back inside one transaction, and only if the link is
	// still this one: a link ended while this was in flight has nothing left
	// to record on, and a scope changed meanwhile must not be put back.
	_, current, err := s.kv.updateState(ctx, st.ServerID, func(cur *State) bool {
		changed := false
		if hb := clampInt(resp.HeartbeatSeconds, 10, 600); resp.HeartbeatSeconds != 0 && hb != cur.HeartbeatSeconds {
			cur.HeartbeatSeconds, changed = hb, true
		}
		if keys, rotated := mergeSiteKeys(cur.SiteKeys, resp.SiteKeys, now); rotated {
			cur.SiteKeys, changed = keys, true
		}
		return changed
	})
	if err != nil {
		s.log.Warn("could not store what the site announced in a heartbeat", "err", err)
	}
	if current {
		s.setStatus(ctx, func(rs *runtimeStatus) { rs.LastHeartbeatAt, rs.HeartbeatError = &now, "" })
	}
}

// mergeSiteKeys applies a rotation announcement. A key the site lists is added
// or has its end date updated, and a pinned key past its end date is dropped.
// It never leaves the set empty: a dashboard with no key could not verify
// anything, and the link would have to be made again.
func mergeSiteKeys(pinned []SiteKey, announced []wireSiteKey, now time.Time) ([]SiteKey, bool) {
	out := append([]SiteKey(nil), pinned...)
	changed := false
	for _, a := range announced {
		k := SiteKey{Kid: a.Kid, PublicKey: a.PublicKey}
		if a.NotAfter != nil {
			k.NotAfter = *a.NotAfter
		}
		if _, err := k.key(); err != nil || k.Kid == "" || len(k.Kid) > 64 {
			continue
		}
		found := false
		for i := range out {
			if out[i].Kid != k.Kid {
				continue
			}
			found = true
			// A pinned key's material never changes; only its end date may.
			if out[i].PublicKey == k.PublicKey && out[i].NotAfter != k.NotAfter {
				out[i].NotAfter, changed = k.NotAfter, true
			}
		}
		if !found {
			out, changed = append(out, k), true
		}
	}
	live := out[:0:0]
	for _, k := range out {
		if k.usable(now) {
			live = append(live, k)
		}
	}
	if len(live) > 0 && len(live) != len(out) {
		return live, true
	}
	return out, changed
}

// linkEnded handles the site saying the server was removed there: this
// dashboard forgets its link and everything stops. It reports whether that is
// what happened.
func (s *Service) linkEnded(ctx context.Context, st State, err error) bool {
	if !isCode(err, http.StatusGone, "") {
		return false
	}
	s.clearLink(ctx, st, "the server was removed on the site")
	return true
}

// clearLink forgets a link that is over. It forgets the link and what the site
// set (the update policy), and keeps the identity, so linking again shows the
// same fingerprint.
func (s *Service) clearLink(ctx context.Context, st State, why string) {
	ctx = context.WithoutCancel(ctx)
	// Two loops can hear the same 410 together; only the first has a link to
	// clear, and only that link, not a newer one.
	cur, ok, err := s.kv.state(ctx)
	if err != nil || !ok || cur.ServerID != st.ServerID {
		return
	}
	if err := s.kv.tx(ctx, func(tx *sql.Tx) error {
		return delTx(ctx, tx, keyState, keyRequest, keyStatus, keyPolicy, keyAttempt)
	}); err != nil {
		s.log.Warn("could not clear the account link", "err", err)
		return
	}
	s.cancelLinked()
	s.log.Info("account link cleared", "reason", why)
	if s.audit != nil {
		s.audit.Record(ctx, audit.Entry{
			Username: "system", Actor: "system", Action: "account.unlink", Target: st.Owner,
			Method: "LINK", Status: 200, Success: true,
			Detail: audit.Detail(map[string]any{"reason": why, "serverId": st.ServerID}),
		})
	}
}

// ------------------------------------------------------------------ status --

// setStatus changes the small record of how the last exchanges went, and
// writes it only when it changed.
func (s *Service) setStatus(ctx context.Context, change func(*runtimeStatus)) {
	before := s.kv.status(ctx)
	after := before
	change(&after)
	b1, _ := json.Marshal(before)
	b2, _ := json.Marshal(after)
	if string(b1) == string(b2) {
		return
	}
	if err := s.kv.put(context.WithoutCancel(ctx), keyStatus, after); err != nil {
		s.log.Debug("could not store the account link status", "err", err)
	}
}

// An exchange cut short because the connector is stopping, or the link is being
// ended, failed for no reason the page should report.
func (s *Service) setHeartbeatError(ctx context.Context, msg string) {
	if msg != "" && ctx.Err() != nil {
		return
	}
	s.setStatus(ctx, func(rs *runtimeStatus) { rs.HeartbeatError = msg })
}

func (s *Service) setPollError(ctx context.Context, msg string) {
	if msg != "" && ctx.Err() != nil {
		return
	}
	s.setStatus(ctx, func(rs *runtimeStatus) { rs.PollError = msg })
}

// describeError says in a sentence why an exchange failed, for the person
// reading the Settings page.
func describeError(err error) string {
	var api *APIError
	if errors.As(err, &api) {
		switch api.Code {
		case "stale_time":
			return "This server's clock and the site's differ by more than 60 seconds; correct the time on this server."
		case "bad_signature", "unknown_server":
			return "The site does not accept this server's signature. If the server was removed from the account, unlink and link it again."
		case "rate_limited", "poll_in_flight":
			return "The site is limiting how often this server may call it; it will try again."
		}
		return api.Error()
	}
	return clip(err.Error(), 300)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
