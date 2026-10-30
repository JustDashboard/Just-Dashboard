// Package usageping is the optional daily count: one small report to the
// project's site, so that the people who build this can tell how many servers
// run it, and nothing about any of them.
//
// This is a root-equivalent panel that many people run down a tunnel so that it
// touches nothing, so what it may do here is held tightly. It is off until an
// administrator of this server says yes, and an install that never answers
// never sends. The report is the three fields of Report, and Status hands the
// page the same bytes the request carries, so what the page shows is not a
// description of the report but the report.
//
// There is no install ID, on purpose. Each server sends at most one report per
// UTC day, so counting a day's reports counts that day's servers, and with
// nothing to join on nobody can follow one server from one day to the next.
//
// The site necessarily sees the address a request comes from, and a list of
// those would be a list of root-equivalent panels, so its side of this contract
// is to keep none of them. See docs/internal/backend/usage-ping.md.
package usageping

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

const (
	// Path is where a report goes, under JD_ACCOUNT_URL.
	Path = "/api/usage-ping"

	keyChoice = "usage.ping.choice"
	keyLast   = "usage.ping.last"

	// checkEvery is how often the loop asks whether today's report has gone.
	// It is what spreads the reports out without a random delay: each server's
	// first check of a new UTC day falls somewhere in that day's first hour,
	// depending only on when it was started. It also retries a failed report,
	// so a site that is down hears from a server no more than this often.
	checkEvery = time.Hour
	// firstCheck keeps the report out of the boot path.
	firstCheck = time.Minute

	requestTimeout = 15 * time.Second
	// answerTimeout is how long a person who just said yes waits to see whether
	// the first report got through. The next check retries one that did not.
	answerTimeout = 5 * time.Second

	dayWindow  = 24 * time.Hour
	weekWindow = 7 * dayWindow
)

// Choice is what an administrator answered.
type Choice string

const (
	ChoiceUndecided Choice = "undecided"
	ChoiceOn        Choice = "on"
	ChoiceOff       Choice = "off"
)

// ErrDisabled is an answer given while a switch in .env has the feature off.
var ErrDisabled = errors.New("the usage count is switched off in this server's .env")

// Report is everything that is ever sent. Adding a field here is a change to
// what the project has told people it collects, and to the site's receiver,
// which refuses anything else. TestReportIsExactlyThreeFields fails on one.
type Report struct {
	Version      string `json:"version"`
	UsedToday    bool   `json:"usedToday"`
	UsedThisWeek bool   `json:"usedThisWeek"`
}

// Status is what the page draws.
type Status struct {
	Enabled bool `json:"enabled"`
	// OffBy names the switch that has it off, for the page to say which.
	OffBy   string `json:"offBy,omitempty"`
	Choice  Choice `json:"choice"`
	SiteURL string `json:"siteUrl"`
	// NextBody is the body the next report would carry, byte for byte.
	NextBody string `json:"nextBody"`
	// LastBody and LastSentAt are the last report the site accepted.
	LastBody   string     `json:"lastBody,omitempty"`
	LastSentAt *time.Time `json:"lastSentAt,omitempty"`
	// LastError is why the newest attempt failed, if it did.
	LastError string `json:"lastError,omitempty"`
}

// Options are what a Service is made from.
type Options struct {
	Store   *store.Store
	Log     *slog.Logger
	SiteURL string
	Version string
	// OffBy is the name of the setting that switches this off, or empty.
	OffBy string
	// HTTP and Now are for tests.
	HTTP *http.Client
	Now  func() time.Time
}

// Service sends the report. Its state is in the dashboard's store, so it
// survives a restart: that is what keeps a server restarted three times in a
// day from being counted as four.
type Service struct {
	st      *store.Store
	log     *slog.Logger
	siteURL string
	version string
	offBy   string
	http    *http.Client
	now     func() time.Time

	// sendMu lets one attempt at a time decide whether today's report has gone,
	// so an answer pressed during the hourly check cannot send a second one.
	sendMu sync.Mutex

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func New(o Options) *Service {
	s := &Service{
		st: o.Store, log: o.Log, siteURL: o.SiteURL, version: o.Version, offBy: o.OffBy,
		http: o.HTTP, now: o.Now,
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.http == nil {
		s.http = newHTTPClient()
	}
	if s.now == nil {
		s.now = func() time.Time { return time.Now().UTC() }
	}
	return s
}

// newHTTPClient never follows a redirect: the report goes to the origin the
// page names or nowhere. It keeps no connection open for the day between
// reports, and has no cookie jar for a response to leave anything in.
func newHTTPClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	t.DisableKeepAlives = true
	return &http.Client{
		Transport: t,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Start runs the daily check. Off by a switch in .env it starts nothing; while
// nobody has said yes it runs and sends nothing, so saying yes needs no restart.
func (s *Service) Start(ctx context.Context) {
	if s == nil || s.offBy != "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, s.cancel = context.WithCancel(ctx)
	s.done = make(chan struct{})
	go s.run(ctx, s.done)
}

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

func (s *Service) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	timer := time.NewTimer(firstCheck)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		s.sendIfDue(ctx)
		timer.Reset(checkEvery)
	}
}

// SetChoice stores an answer. A yes sends the first report before it returns,
// so the page can say at once that it went, or why it did not; that failing is
// not a failure of the answer, which is stored either way.
func (s *Service) SetChoice(ctx context.Context, on bool) error {
	if s.offBy != "" {
		return ErrDisabled
	}
	choice := ChoiceOff
	if on {
		choice = ChoiceOn
	}
	if err := s.st.SetSetting(ctx, keyChoice, string(choice)); err != nil {
		return err
	}
	if on {
		// Not the request's context alone: a browser navigating away from the
		// answer should not record its own departure as the site being down.
		sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), answerTimeout)
		defer cancel()
		s.sendIfDue(sendCtx)
	}
	return nil
}

// Status reads where this server stands.
func (s *Service) Status(ctx context.Context) (Status, error) {
	choice, err := s.choice(ctx)
	if err != nil {
		return Status{}, err
	}
	last, err := s.last(ctx)
	if err != nil {
		return Status{}, err
	}
	body, err := s.body(ctx)
	if err != nil {
		return Status{}, err
	}
	st := Status{
		Enabled: s.offBy == "", OffBy: s.offBy, Choice: choice, SiteURL: s.siteURL,
		NextBody: string(body), LastBody: last.Body, LastError: last.Error,
	}
	if last.At > 0 {
		at := time.Unix(last.At, 0).UTC()
		st.LastSentAt = &at
	}
	return st, nil
}

// lastAttempt is what is remembered between reports.
type lastAttempt struct {
	// Day is the UTC date of the last report the site accepted.
	Day   string `json:"day,omitempty"`
	At    int64  `json:"at,omitempty"`
	Body  string `json:"body,omitempty"`
	Error string `json:"error,omitempty"`
}

func (s *Service) choice(ctx context.Context) (Choice, error) {
	v, _, err := s.st.Setting(ctx, keyChoice)
	if err != nil {
		return ChoiceUndecided, err
	}
	switch Choice(v) {
	case ChoiceOn:
		return ChoiceOn, nil
	case ChoiceOff:
		return ChoiceOff, nil
	}
	// Unset, or a value this build cannot read: either way, nobody said yes.
	return ChoiceUndecided, nil
}

func (s *Service) last(ctx context.Context) (lastAttempt, error) {
	var last lastAttempt
	v, ok, err := s.st.Setting(ctx, keyLast)
	if err != nil || !ok {
		return last, err
	}
	if err := json.Unmarshal([]byte(v), &last); err != nil {
		// Forgetting it can at worst send one extra report today.
		s.log.Warn("the usage count's last report could not be read", "err", err)
		return lastAttempt{}, nil
	}
	return last, nil
}

func (s *Service) storeLast(ctx context.Context, last lastAttempt) {
	raw, err := json.Marshal(last)
	if err == nil {
		err = s.st.SetSetting(ctx, keyLast, string(raw))
	}
	if err != nil {
		s.log.Warn("the usage count's last report could not be stored", "err", err)
	}
}

// sendIfDue sends today's report if the answer is yes and it has not gone yet.
func (s *Service) sendIfDue(ctx context.Context) {
	if s.offBy != "" {
		return
	}
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	choice, err := s.choice(ctx)
	if err != nil || choice != ChoiceOn {
		return
	}
	last, err := s.last(ctx)
	if err != nil {
		s.log.Debug("usage count skipped", "err", err)
		return
	}
	now := s.now().UTC()
	today := now.Format(time.DateOnly)
	if last.Day == today {
		return
	}
	body, err := s.body(ctx)
	if err != nil {
		s.log.Debug("usage count skipped", "err", err)
		return
	}
	if err := s.send(ctx, body); err != nil {
		// Debug, like a failed update check: a server behind a firewall would
		// otherwise warn every hour about something its operator chose.
		s.log.Debug("usage count failed", "err", err)
		last.Error = err.Error()
		s.storeLast(context.WithoutCancel(ctx), last)
		return
	}
	s.storeLast(context.WithoutCancel(ctx), lastAttempt{Day: today, At: now.Unix(), Body: string(body)})
}

func (s *Service) send(ctx context.Context, body []byte) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.siteURL+Path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// No version here: the body has it, and the body is what the page shows.
	req.Header.Set("User-Agent", "just-dashboard")
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("the site answered %s", resp.Status)
	}
	return nil
}

// body builds the report. Status and the request both come through here.
func (s *Service) body(ctx context.Context) ([]byte, error) {
	used, err := s.lastUsed(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	return json.Marshal(Report{
		Version:      s.version,
		UsedToday:    !used.IsZero() && now.Sub(used) < dayWindow,
		UsedThisWeek: !used.IsZero() && now.Sub(used) < weekWindow,
	})
}

// lastUsed is the last time anybody was signed in here.
//
// The sessions table alone would miss most of it: signing out or an expired
// session deletes the row, so somebody who signed in, worked and signed out
// would read as nobody having been here. The audit log keeps those moments.
// Only a successful sign-in counts, so a bot guessing passwords at a panel left
// open to the internet does not make it look used.
func (s *Service) lastUsed(ctx context.Context) (time.Time, error) {
	var seen, signedIn sql.NullInt64
	if err := s.st.DB.QueryRowContext(ctx, `SELECT MAX(last_seen_at) FROM sessions`).Scan(&seen); err != nil {
		return time.Time{}, err
	}
	err := s.st.DB.QueryRowContext(ctx,
		`SELECT ts FROM audit_log WHERE action IN ('auth.login', 'auth.logout') AND success = 1 ORDER BY ts DESC LIMIT 1`,
	).Scan(&signedIn)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, err
	}
	latest := max(seen.Int64, signedIn.Int64)
	if latest == 0 {
		return time.Time{}, nil
	}
	return time.Unix(latest, 0), nil
}
