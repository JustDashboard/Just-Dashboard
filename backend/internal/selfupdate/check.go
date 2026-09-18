package selfupdate

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Asking the repository what the newest version is.
//
// This is the one place the dashboard talks to the internet on its own
// initiative, which is worth being deliberate about: it is a root-equivalent
// panel that most people run down a tunnel precisely so it touches nothing.
// So the request is a single unauthenticated GET of one small file, it carries
// nothing about the install but a version number in the user agent, it happens
// four times a day rather than on every page load, and JD_UPDATE_CHECK=false
// turns it off completely — with the changelog for the installed version
// still readable, because that half is compiled in and needs no network.
//
// raw.githubusercontent.com rather than the releases API, for the reason
// ManifestPath gives: a release becomes a commit rather than a commit plus a
// tag plus a release object, so there is no second step to forget. It is also
// unauthenticated and generously rate-limited, where the API is neither.

const (
	// DefaultRepo is the project this build is a copy of. A fork changes it
	// with JD_UPDATE_REPO and starts describing its own releases.
	DefaultRepo = "JustDashboard/Just-Dashboard"
	// DefaultRef is the branch releases land on. The manifest at its head is
	// the newest published version by definition, which is why nothing here
	// needs to read tags.
	DefaultRef = "main"

	// checkInterval is the background timer, for a dashboard nobody has
	// touched. It is deliberately slow: a wall screen left open for a month
	// should not become a thousand requests, and the answer to "is there a
	// newer version" changes a few times a year.
	//
	// The cadence that actually matters is nudgeInterval below, because the
	// moment an operator wants a current answer is the moment they open the
	// page — and that is a thing this can be told about rather than guessed at
	// on a timer.
	checkInterval = 2 * time.Hour
	// nudgeInterval is the floor on the check a page load triggers.
	//
	// Every reload asks for one, so this is what keeps "check on every load"
	// from meaning "a request per tab per navigation" on a dashboard somebody
	// is clicking around. Five minutes is short enough that an operator who
	// reloads because they are expecting a release gets the answer, and long
	// enough that the twenty pages they open after that cost nothing.
	nudgeInterval = 5 * time.Minute
	// firstCheckDelay keeps the check out of the boot path. A server that
	// cannot reach the internet should not spend its first seconds finding
	// that out, and a crash-looping container should not retry a network call
	// every few seconds.
	firstCheckDelay = 30 * time.Second
	// minForcedInterval is the floor on operator-initiated re-checks, so a
	// held-down refresh button cannot become a request per click.
	minForcedInterval = 20 * time.Second

	fetchTimeout = 15 * time.Second
	// maxManifest is a ceiling on what a redirect or a wrong URL can make this
	// process read into memory. The real file is a few kilobytes.
	maxManifest = 1 << 20
)

// Check is the answer to "is there a newer version", as of the last time
// anyone asked.
type Check struct {
	// Enabled is false when the operator turned the check off. The UI says so
	// rather than reporting an install that is permanently up to date.
	Enabled   bool      `json:"enabled"`
	CheckedAt time.Time `json:"checkedAt,omitempty"`
	// Latest is the newest version the repository advertises. Empty when the
	// check has not succeeded yet, which is not the same as being up to date.
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	// Releases are the ones strictly newer than the installed version, newest
	// first — every intervening one, not just the newest.
	Releases []Release `json:"releases"`
	// Error is why the last attempt failed, in the operator's words. A server
	// with no route to the internet is a normal state for this product and is
	// reported as information, not as a broken dashboard.
	Error  string `json:"error,omitempty"`
	Source string `json:"source,omitempty"`
}

// Checker holds the cached answer and refreshes it on a timer.
type Checker struct {
	repo    string
	ref     string
	enabled bool
	current string
	log     *slog.Logger

	// baseURL is the raw-content host, overridden only by the tests. There is
	// no configuration for it: pointing an auto-updater at an arbitrary host
	// is a way to install someone else's code, and JD_UPDATE_REPO already
	// covers the legitimate case of running a fork.
	baseURL string
	client  *http.Client

	mu     sync.Mutex
	result Check
	busy   bool

	stop chan struct{}
	once sync.Once
}

// NewChecker builds the checker for an install of version `current`.
func NewChecker(current, repo, ref string, enabled bool, log *slog.Logger) *Checker {
	if strings.TrimSpace(repo) == "" {
		repo = DefaultRepo
	}
	if strings.TrimSpace(ref) == "" {
		ref = DefaultRef
	}
	return &Checker{
		repo:    strings.Trim(strings.TrimSpace(repo), "/"),
		ref:     strings.TrimSpace(ref),
		enabled: enabled,
		current: Normalise(current),
		log:     log,
		baseURL: "https://raw.githubusercontent.com",
		client:  &http.Client{Timeout: fetchTimeout},
		result:  Check{Enabled: enabled, Releases: []Release{}},
		stop:    make(chan struct{}),
	}
}

// URL is the file this checker reads, shown in the UI so an operator can see
// exactly what their server is asking for and from where.
func (c *Checker) URL() string {
	return fmt.Sprintf("%s/%s/%s/%s", c.baseURL, c.repo, c.ref, ManifestPath)
}

func (c *Checker) Repo() string { return c.repo }
func (c *Checker) Ref() string  { return c.ref }

// Start runs the check on its own timer for as long as ctx lives.
//
// It is started at boot rather than on the first request for the same reason
// the metrics recorder is: the operator who most needs to be told about a
// security release is the one who has not opened the dashboard in a month, and
// a check that only runs when somebody looks tells them nothing they could not
// have found out by looking.
func (c *Checker) Start(ctx context.Context) {
	if !c.enabled {
		return
	}
	go func() {
		timer := time.NewTimer(firstCheckDelay)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-c.stop:
				return
			case <-timer.C:
			}
			if _, err := c.Refresh(ctx); err != nil {
				// Logged at debug: a server behind a firewall would otherwise
				// write a warning four times a day about a feature its
				// operator deliberately cannot use.
				c.log.Debug("update check failed", "err", err, "url", c.URL())
			}
			timer.Reset(checkInterval)
		}
	}()
}

func (c *Checker) Stop() { c.once.Do(func() { close(c.stop) }) }

// Result is the cached answer, which is what every request reads. It never
// blocks and never makes a request.
func (c *Checker) Result() Check {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.result
	out.Releases = append([]Release{}, c.result.Releases...)
	return out
}

// Nudge starts a check when the cached answer is older than maxAge, and
// returns the cached answer either way.
//
// It never waits, and that is the whole point of it. This runs on the status
// request, which the update panel polls every couple of seconds while an
// upgrade is running; a version of this that blocked on a network call would
// turn one unreachable registry into a dashboard whose every page load hangs
// for fifteen seconds. The operator asking explicitly gets Refresh instead,
// where waiting is what they asked for.
//
// maxAge is what separates the two callers: a background poll passes
// checkInterval and almost never triggers anything, while a page load passes
// nudgeInterval and is how a freshly opened dashboard has a current answer
// within a second or two of being opened.
func (c *Checker) Nudge(maxAge time.Duration) Check {
	c.mu.Lock()
	stale := c.enabled && !c.busy &&
		(c.result.CheckedAt.IsZero() || time.Since(c.result.CheckedAt) >= maxAge)
	c.mu.Unlock()
	if stale {
		// Deliberately not the request's context: the answer outlives the
		// request that triggered it, and cancelling it when the browser
		// navigates away would leave the cache stale forever on a dashboard
		// nobody keeps open.
		go func() { _, _ = c.Refresh(context.Background()) }()
	}
	return c.Result()
}

// Refresh asks now, subject to the floor on how often that can happen.
func (c *Checker) Refresh(ctx context.Context) (Check, error) {
	if !c.enabled {
		return c.Result(), nil
	}
	c.mu.Lock()
	if c.busy || (!c.result.CheckedAt.IsZero() && time.Since(c.result.CheckedAt) < minForcedInterval) {
		c.mu.Unlock()
		return c.Result(), nil
	}
	c.busy = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.busy = false
		c.mu.Unlock()
	}()

	res := Check{
		Enabled:   true,
		CheckedAt: time.Now().UTC(),
		Source:    c.URL(),
		Releases:  []Release{},
	}
	manifest, err := c.fetch(ctx)
	if err != nil {
		// The previous good answer is kept rather than blanked. An install
		// that knew about 0.6 yesterday still needs 0.6 today, and a dropped
		// tunnel should not make the banner disappear and reappear.
		c.mu.Lock()
		prev := c.result
		c.result = Check{
			Enabled: true, CheckedAt: res.CheckedAt, Source: res.Source,
			Latest: prev.Latest, Available: prev.Available, Releases: prev.Releases,
			Error: err.Error(),
		}
		if c.result.Releases == nil {
			c.result.Releases = []Release{}
		}
		out := c.result
		c.mu.Unlock()
		return out, err
	}

	res.Latest = manifest.Latest
	res.Releases = manifest.Since(c.current)
	res.Available = len(res.Releases) > 0

	c.mu.Lock()
	c.result = res
	c.mu.Unlock()
	return res, nil
}

func (c *Checker) fetch(ctx context.Context) (Manifest, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL(), nil)
	if err != nil {
		return Manifest{}, err
	}
	// Identifies the product and the version asking, and nothing else — no
	// host name, no install id, no account. There is no telemetry here and
	// this is the line that has to keep being true.
	req.Header.Set("User-Agent", "just-dashboard/"+c.current)
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return Manifest{}, fmt.Errorf("could not reach %s: %w", hostOf(c.baseURL), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Manifest{}, fmt.Errorf("%s returned %s for %s/%s", hostOf(c.baseURL), resp.Status, c.repo, c.ref)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxManifest))
	if err != nil {
		return Manifest{}, err
	}
	m, err := ParseManifest(b)
	if err != nil {
		return Manifest{}, fmt.Errorf("the published changelog could not be read: %w", err)
	}
	return m, nil
}

func hostOf(base string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}
