package hublink

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/safego"
)

// The fleet fields: what the hub needs to know what each server needs
// (patches, certificates, fixes) and who failed to sign in. They are the hub
// link's own top-level keys of the heartbeat (hub-link-protocol.md 5.4), sent
// beside the snapshot through HeartbeatExtra and never added to the shared
// accountlink.Snapshot: JD builds one snapshot for both links, so a field
// there would also reach just-dashboard.com's account link.
//
// Each is read off the heartbeat's path. A heartbeat takes what the last
// reading left and, when that is older than the field's interval, starts the
// next reading in the background: a package list or a certificate inventory
// can take seconds, and the heartbeat already waits on the machine's own
// reading. A field nobody has read yet, or whose last reading failed, is left
// out, which the hub reads as "not known" (section 5).

// FleetSources are where the fleet fields are read from. A nil source leaves
// its field out of every heartbeat.
type FleetSources struct {
	// OSUpdates is the host's package updates. Nil with no error means this
	// host has no package manager the dashboard reads.
	OSUpdates func(ctx context.Context) (*OSUpdates, error)
	// Certificates is every certificate this server serves.
	Certificates func(ctx context.Context) ([]CertificateFact, error)
	// ImageScans is the latest finished scan of each deployment's live
	// release.
	ImageScans func(ctx context.Context) ([]ImageScanFact, error)
	// SignIns counts the sign-ins of the last day; AuditSignIns reads them
	// from the audit log.
	SignIns func(ctx context.Context, now time.Time) (SignInCounts, error)
}

// OSUpdates is os.updates (5.4).
type OSUpdates struct {
	Pending int `json:"pending"`
	// Security is nil on a package manager that cannot tell a security
	// update from any other (Alpine, Arch), and is then left out.
	Security       *int  `json:"security,omitempty"`
	RebootRequired bool  `json:"rebootRequired"`
	CheckedAt      int64 `json:"checkedAt"`
}

// CertificateFact is one entry of certificates (5.4).
type CertificateFact struct {
	Name     string `json:"name"`
	DaysLeft int    `json:"daysLeft"`
	Issuer   string `json:"issuer,omitempty"`
}

// ImageScanFact is one entry of imageScans (5.4): the findings of the live
// release's latest scan that its owner can do something about.
type ImageScanFact struct {
	Deployment  string `json:"deployment"`
	Environment string `json:"environment"`
	Critical    int    `json:"critical"`
	High        int    `json:"high"`
	ScannedAt   int64  `json:"scannedAt"`
}

// SignInCounts is signins (5.4): counts only, never an account (Decision D6
// of the hub's plan).
type SignInCounts struct {
	Successes int `json:"successes"`
	Failures  int `json:"failures"`
	// LastFailureAt is the newest failure of the last 30 days, left out when
	// there was none.
	LastFailureAt int64 `json:"lastFailureAt,omitempty"`
}

// How often each field is read again, and how soon after a failed reading.
// Packages change when the host's own timer refreshes the index, so every
// six hours is the plan's interval; certificates and scans change by the day;
// sign-ins are counted each minute.
const (
	osUpdatesEvery    = 6 * time.Hour
	certificatesEvery = 10 * time.Minute
	imageScansEvery   = 5 * time.Minute
	signInsEvery      = time.Minute
	readRetry         = 2 * time.Minute
	// readTimeout bounds one background reading.
	readTimeout = 2 * time.Minute
)

// The most of each list a heartbeat carries, and the most the fleet fields
// may take of the 63 KiB body together: beyond it the longer of the two lists
// is halved until they fit, and the snapshot's own lists make room for the
// rest (accountlink's encodeWith).
const (
	maxCertificates = 100
	maxImageScans   = 100
	maxFleetBytes   = 24 << 10
)

// The longest text each field keeps, in characters: a certificate name is at
// most a DNS name, and the rest follow the text rule of 5.3.
const (
	maxCertificateName = 253
	maxIssuer          = 128
	maxDeploymentName  = 128
)

// cached is one field's last reading and the reading under way.
type cached[T any] struct {
	every time.Duration
	read  func(context.Context) (T, error)

	mu      sync.Mutex
	val     T
	ok      bool
	at      time.Time
	failed  bool
	reading bool
}

// get returns the last good reading, and starts the next when it is due.
// spawn runs the reading; it is `go` outside tests.
func (c *cached[T]) get(now time.Time, spawn func(func())) (T, bool) {
	c.mu.Lock()
	// A clock that went back counts as due, rather than holding the reading
	// back by however far it went.
	due := c.at.IsZero() || now.Before(c.at) || now.Sub(c.at) >= c.every ||
		(c.failed && now.Sub(c.at) >= readRetry)
	start := due && !c.reading
	if start {
		c.reading = true
	}
	c.mu.Unlock()
	if start {
		spawn(func() {
			ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
			defer cancel()
			// This goroutine is nobody's loop, and the sources parse what the
			// host prints: a panic is a failed reading, retried like any other,
			// rather than the end of the process (runtime-boundaries.md).
			var v T
			err := safego.Try(nil, "hub link fleet reading", func() error {
				var err error
				v, err = c.read(ctx)
				return err
			})
			c.mu.Lock()
			defer c.mu.Unlock()
			// Timed from when it was asked for, on the link's own clock.
			c.reading, c.at, c.failed = false, now, err != nil
			if err != nil {
				// What is not known now is not sent, rather than an older
				// answer that reads as current.
				var zero T
				c.val, c.ok = zero, false
				return
			}
			c.val, c.ok = v, true
		})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.val, c.ok
}

// fleet holds the fleet fields' readings.
type fleet struct {
	spawn        func(func())
	osUpdates    *cached[*OSUpdates]
	certificates *cached[[]CertificateFact]
	imageScans   *cached[[]ImageScanFact]
	signIns      *cached[SignInCounts]
}

func newFleet(src *FleetSources, now func() time.Time) *fleet {
	if src == nil {
		return nil
	}
	f := &fleet{spawn: func(fn func()) { go fn() }}
	if src.OSUpdates != nil {
		f.osUpdates = &cached[*OSUpdates]{every: osUpdatesEvery, read: src.OSUpdates}
	}
	if src.Certificates != nil {
		f.certificates = &cached[[]CertificateFact]{every: certificatesEvery, read: src.Certificates}
	}
	if src.ImageScans != nil {
		f.imageScans = &cached[[]ImageScanFact]{every: imageScansEvery, read: src.ImageScans}
	}
	if src.SignIns != nil {
		f.signIns = &cached[SignInCounts]{every: signInsEvery, read: func(ctx context.Context) (SignInCounts, error) {
			return src.SignIns(ctx, now())
		}}
	}
	return f
}

// extra is the fleet fields as the heartbeat carries them at scope: os and
// signins at every scope, certificates and imageScans from operate up.
func (f *fleet) extra(now time.Time, scope accountlink.Scope) (map[string]json.RawMessage, error) {
	if f == nil {
		return nil, nil
	}
	out := map[string]json.RawMessage{}
	put := func(key string, v any) error {
		raw, err := json.Marshal(v)
		if err == nil {
			out[key] = raw
		}
		return err
	}
	if f.osUpdates != nil {
		if u, ok := f.osUpdates.get(now, f.spawn); ok && u != nil {
			if err := put("os", map[string]any{"updates": u}); err != nil {
				return nil, err
			}
		}
	}
	if f.signIns != nil {
		if c, ok := f.signIns.get(now, f.spawn); ok {
			if err := put("signins", c); err != nil {
				return nil, err
			}
		}
	}
	if scope != accountlink.ScopeOperate && scope != accountlink.ScopeMaintain {
		return out, nil
	}
	var certs []CertificateFact
	var scans []ImageScanFact
	var haveCerts, haveScans bool
	if f.certificates != nil {
		certs, haveCerts = f.certificates.get(now, f.spawn)
	}
	if f.imageScans != nil {
		scans, haveScans = f.imageScans.get(now, f.spawn)
	}
	// A list that was read is a list, empty for none, and never null.
	if haveCerts && certs == nil {
		certs = []CertificateFact{}
	}
	if haveScans && scans == nil {
		scans = []ImageScanFact{}
	}
	certs, scans = fitFleetLists(certs, scans)
	if haveCerts {
		if err := put("certificates", certs); err != nil {
			return nil, err
		}
	}
	if haveScans {
		if err := put("imageScans", scans); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// fitFleetLists cuts the two lists to their counts and then halves the longer
// until both encode inside maxFleetBytes. They are sorted worst first, so
// what goes is what needs the least attention.
func fitFleetLists(certs []CertificateFact, scans []ImageScanFact) ([]CertificateFact, []ImageScanFact) {
	if certs != nil {
		certs = slices.Clone(certs[:min(len(certs), maxCertificates)])
	}
	if scans != nil {
		scans = slices.Clone(scans[:min(len(scans), maxImageScans)])
	}
	for {
		a, _ := json.Marshal(certs)
		b, _ := json.Marshal(scans)
		if len(a)+len(b) <= maxFleetBytes || (len(certs) == 0 && len(scans) == 0) {
			return certs, scans
		}
		if len(a) >= len(b) {
			certs = certs[:len(certs)/2]
		} else {
			scans = scans[:len(scans)/2]
		}
	}
}

// fleetText keeps a value to the text rule of 5.3 (no control, format or
// separator characters, valid UTF-8) and to max characters; "" means the
// value cannot be sent.
func fleetText(s string, max int) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max])
	}
	return s
}

// SortCertificates puts the soonest to expire first, and keeps only what can
// be sent: a name, and a certificate that was read.
func SortCertificates(in []CertificateFact) []CertificateFact {
	out := make([]CertificateFact, 0, len(in))
	for _, c := range in {
		c.Name, c.Issuer = fleetText(c.Name, maxCertificateName), fleetText(c.Issuer, maxIssuer)
		if c.Name == "" {
			continue
		}
		out = append(out, c)
	}
	slices.SortStableFunc(out, func(a, b CertificateFact) int {
		return cmp.Or(cmp.Compare(a.DaysLeft, b.DaysLeft), strings.Compare(a.Name, b.Name))
	})
	return out
}

// SortImageScans puts the worst first: the most critical findings, then the
// most high ones, then by name.
func SortImageScans(in []ImageScanFact) []ImageScanFact {
	out := make([]ImageScanFact, 0, len(in))
	for _, s := range in {
		s.Deployment = fleetText(s.Deployment, maxDeploymentName)
		s.Environment = fleetText(s.Environment, maxDeploymentName)
		if s.Deployment == "" || s.Environment == "" || s.ScannedAt < 1 {
			continue
		}
		out = append(out, s)
	}
	slices.SortStableFunc(out, func(a, b ImageScanFact) int {
		return cmp.Or(cmp.Compare(b.Critical, a.Critical), cmp.Compare(b.High, a.High),
			strings.Compare(a.Deployment, b.Deployment), strings.Compare(a.Environment, b.Environment))
	})
	return out
}

// signInWindow is how far back signins counts, and lastFailureWindow how far
// back its last failure is looked for: the audit log is indexed by time only,
// so each read stays a range of that index.
const (
	signInWindow      = 24 * time.Hour
	lastFailureWindow = 30 * 24 * time.Hour
)

// AuditSignIns counts the last day's sign-ins from the audit log (5.4).
//
// A success is a completed sign-in. auth.login is written as soon as the
// password is right, even while a second factor is still owed, so a password
// or passkey sign-in counts only when its detail says nothing was owed; the
// code that completes one that owed a code is its own auth.2fa.verify. One
// that had to enrol an authenticator completes with auth.2fa.enable, which is
// not counted: Settings writes the same entry when a signed-in person adds
// one. A hub sign-in (hub.signin) completes on its own. A failure is a
// refused password, code or passkey: auth.login, auth.2fa.verify or
// auth.passkey.login with success 0. A refused hub sign-in is the hub's own
// to count, and is not one here.
func AuditSignIns(db *sql.DB) func(ctx context.Context, now time.Time) (SignInCounts, error) {
	return func(ctx context.Context, now time.Time) (SignInCounts, error) {
		var c SignInCounts
		var last sql.NullInt64
		err := db.QueryRowContext(ctx, `
			SELECT
			  COALESCE(SUM(CASE WHEN ts >= ? AND success = 1 AND (
			      (action IN ('auth.login', 'auth.passkey.login')
			        AND COALESCE(json_extract(CASE WHEN json_valid(detail) THEN detail END, '$.needsTotp'), 0) = 0
			        AND COALESCE(json_extract(CASE WHEN json_valid(detail) THEN detail END, '$.needsEnrollment'), 0) = 0)
			      OR action IN ('auth.2fa.verify', 'hub.signin')) THEN 1 ELSE 0 END), 0),
			  COALESCE(SUM(CASE WHEN ts >= ? AND success = 0
			      AND action IN ('auth.login', 'auth.2fa.verify', 'auth.passkey.login') THEN 1 ELSE 0 END), 0),
			  MAX(CASE WHEN success = 0
			      AND action IN ('auth.login', 'auth.2fa.verify', 'auth.passkey.login') THEN ts END)
			FROM audit_log WHERE ts >= ? AND ts <= ?`,
			now.Add(-signInWindow).Unix(), now.Add(-signInWindow).Unix(),
			now.Add(-lastFailureWindow).Unix(), now.Unix()).Scan(&c.Successes, &c.Failures, &last)
		if err != nil {
			return SignInCounts{}, err
		}
		if last.Valid && last.Int64 > 0 {
			c.LastFailureAt = last.Int64
		}
		return c, nil
	}
}
