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
// The dashboard asks this without anybody having told it to, which is worth
// being deliberate about: it is a root-equivalent panel that most people run
// down a tunnel precisely so it touches nothing. So the request is a single
// unauthenticated GET of one small file, it carries nothing about the install
// but a version number in the user agent, it happens every two hours and at
// most every five minutes while pages are being opened, rather than on every
// page load, and JD_UPDATE_CHECK=false turns it off completely — with the
// changelog for the installed version still readable, because that half is
// compiled in and needs no network.
//
// raw.githubusercontent.com rather than the releases API: it is unauthenticated
// and generously rate-limited, where the API is neither, and it answers both
// questions this asks. The changelog on the tracked branch says which versions
// exist. Whether one of them has been released is whether the same file can be
// read at the tag v<version>, which is the one thing a branch cannot say about
// itself: the release workflow tags a merge only once its checks have passed,
// so a version with no tag is one nobody has vouched for yet.
//
// For the project's own repository only a tagged version is offered. Until
// 0.8.6 the changelog on the branch was the whole announcement, so a version
// was offered to every install the moment it was merged, checks or no checks,
// and 0.8.5 sat on offer for days with a failed run and no tag. A fork named
// with JD_UPDATE_REPO keeps the older rule, because a fork that never tags has
// no other way to release, and is told that what it is offered is untagged.

const (
	// DefaultRepo is the project this build is a copy of. A fork changes it
	// with JD_UPDATE_REPO and starts describing its own releases.
	DefaultRepo = "JustDashboard/Just-Dashboard"
	// DefaultRef is the branch releases land on. The manifest at its head names
	// the newest version there is; whether that version has been released is
	// its tag's to say.
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
	// maxTagProbes bounds how many versions one check asks the repository
	// about. The newest is nearly always tagged and the answer is remembered,
	// so the usual cost is one small request once; the bound is for a
	// repository that has stopped tagging altogether.
	maxTagProbes = 8
	// maxManifest is a ceiling on what a redirect or a wrong URL can make this
	// process read into memory. The real file was 300 KB at 0.8.5 and grows by
	// about 4 KB a day, so the ceiling is set where the file will not reach it
	// for years rather than where it reaches it in months: a file one byte over
	// is a check that fails on every install built with this number, with no
	// way to tell them so.
	maxManifest = 8 << 20
	// FieldManifestCap is the same ceiling as it was compiled into every
	// release up to 0.8.5, which read at most this much and then failed to
	// parse what was cut off. Those installs are still out there, and the file
	// they fetch is the one at ManifestPath on the tracked branch, so that file
	// has to stay under this for as long as they are worth reaching. The test
	// beside the changelog fails well before it does not.
	FieldManifestCap = 1 << 20
)

// Check is the answer to "is there a newer version", as of the last time
// anyone asked.
type Check struct {
	// Enabled is false when the operator turned the check off. The UI says so
	// rather than reporting an install that is permanently up to date.
	Enabled   bool      `json:"enabled"`
	CheckedAt time.Time `json:"checkedAt,omitempty"`
	// Latest is the newest version this install is offered: for the project's
	// own repository the newest one with a release tag, for a fork the newest
	// one its branch names. Empty when the check has not succeeded yet, or when
	// nothing released is newer, which is not the same as being up to date.
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	// Tagged says whether Latest has a tag v<Latest> on the repository, which
	// is what makes it a release rather than whatever the branch holds.
	Tagged bool `json:"tagged"`
	// Pending is a version the branch already names, newer than anything
	// offered, that has no tag yet. It is not offered; it is here so the page
	// can say it is on its way rather than say nothing.
	Pending string `json:"pending,omitempty"`
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
	// tagsOnly is true for the project's own repository; see TagsOnly.
	tagsOnly bool

	// baseURL is the raw-content host, overridden only by the tests. There is
	// no configuration for it: pointing an auto-updater at an arbitrary host
	// is a way to install someone else's code, and JD_UPDATE_REPO already
	// covers the legitimate case of running a fork.
	baseURL string
	client  *http.Client

	mu     sync.Mutex
	result Check
	busy   bool
	// tagged remembers the versions found to have a tag. A release tag does
	// not go away, and if one ever did the updater refuses at install time;
	// a missing tag is asked about again, because it is expected to appear.
	tagged map[string]bool

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
	repo = strings.Trim(strings.TrimSpace(repo), "/")
	return &Checker{
		repo:     repo,
		ref:      strings.TrimSpace(ref),
		enabled:  enabled,
		current:  Normalise(current),
		log:      log,
		tagsOnly: TagsOnly(repo),
		baseURL:  "https://raw.githubusercontent.com",
		client:   &http.Client{Timeout: fetchTimeout},
		result:   Check{Enabled: enabled, Releases: []Release{}},
		tagged:   map[string]bool{},
		stop:     make(chan struct{}),
	}
}

// TagsOnly reports whether an install following repo is offered, and installs,
// only versions that have a release tag.
//
// True for the project's own repository, whatever branch is tracked: there the
// tag is made by the release workflow once the merge's checks have passed, so
// it is the only thing that says a version was released. A fork named with
// JD_UPDATE_REPO is the exception, because a fork that never tags has no other
// way to ship, and the page tells its operator when what is offered is
// untagged. GitHub compares owner and repository names without case, so this
// does too.
func TagsOnly(repo string) bool {
	repo = strings.Trim(strings.TrimSpace(repo), "/")
	return repo == "" || strings.EqualFold(repo, DefaultRepo)
}

// URL is the file this checker reads, shown in the UI so an operator can see
// exactly what their server is asking for and from where.
func (c *Checker) URL() string {
	return fmt.Sprintf("%s/%s/%s/%s", c.baseURL, c.repo, c.ref, ManifestPath)
}

// TagURL is the same file at the tag v<version>. It is there exactly when the
// tag is, which is the cheapest way to ask this host whether a version was
// released. The full ref is spelled out so a branch that happens to be called
// v0.9 cannot answer for the tag.
func (c *Checker) TagURL(version string) string {
	return fmt.Sprintf("%s/%s/refs/tags/v%s/%s", c.baseURL, c.repo, Normalise(version), ManifestPath)
}

func (c *Checker) Repo() string { return c.repo }
func (c *Checker) Ref() string  { return c.ref }

// TagsOnly reports whether this install is offered tagged versions only.
func (c *Checker) TagsOnly() bool { return c.tagsOnly }

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
				// write a warning every two hours about a feature its
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
		return c.failed(res, err)
	}

	res.Releases, res.Tagged, res.Pending, err = c.released(ctx, manifest.Since(c.current))
	if err != nil {
		return c.failed(res, err)
	}
	res.Available = len(res.Releases) > 0
	if res.Available {
		res.Latest = res.Releases[0].Version
	} else if !c.tagsOnly {
		res.Latest = manifest.Latest
	}
	if unread := manifest.NewerUnread(c.current); !res.Available && unread != "" {
		// Nothing this build can read is newer, but something is. "Up to date"
		// would be the one wrong answer.
		res.Error = fmt.Sprintf("release %s is out, but this version cannot read its notes and so cannot install it from here; "+
			"update from the server's shell instead", unread)
	}

	c.mu.Lock()
	c.result = res
	c.mu.Unlock()
	return res, nil
}

// failed records a check that could not be finished.
//
// The previous good answer is kept rather than blanked. An install that knew
// about 0.6 yesterday still needs 0.6 today, and a dropped tunnel should not
// make the banner disappear and reappear.
func (c *Checker) failed(res Check, err error) (Check, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prev := c.result
	c.result = Check{
		Enabled: true, CheckedAt: res.CheckedAt, Source: res.Source,
		Latest: prev.Latest, Available: prev.Available, Releases: prev.Releases,
		Tagged: prev.Tagged, Pending: prev.Pending,
		Error: err.Error(),
	}
	if c.result.Releases == nil {
		c.result.Releases = []Release{}
	}
	out := c.result
	out.Releases = append([]Release{}, c.result.Releases...)
	return out, err
}

// released narrows the versions newer than this install, newest first, to the
// ones it may be offered.
//
// For the project's own repository that is the newest version with a tag and
// everything older: the versions above it are on the branch and not released,
// and the newest of them is returned as pending. A probe that cannot be
// answered fails the check, since guessing either way is wrong: offering would
// ignore the gate, and withholding would hide a release behind a dropped
// connection. For a fork every version is offered, and tagged only says
// whether the newest has a tag; not finding out is then reported as untagged,
// which is what the updater falls back to treating it as.
func (c *Checker) released(ctx context.Context, newer []Release) (offered []Release, tagged bool, pending string, err error) {
	if len(newer) == 0 {
		return []Release{}, false, "", nil
	}
	if !c.tagsOnly {
		tagged, _ = c.hasTag(ctx, newer[0].Version)
		return newer, tagged, "", nil
	}
	for i, release := range newer {
		if i >= maxTagProbes {
			break
		}
		found, err := c.hasTag(ctx, release.Version)
		if err != nil {
			return nil, false, "", err
		}
		if found {
			if i > 0 {
				pending = newer[0].Version
			}
			return newer[i:], true, pending, nil
		}
	}
	return []Release{}, false, newer[0].Version, nil
}

// hasTag asks the repository whether v<version> exists.
func (c *Checker) hasTag(ctx context.Context, version string) (bool, error) {
	version = Normalise(version)
	c.mu.Lock()
	known := c.tagged[version]
	c.mu.Unlock()
	if known {
		return true, nil
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	// HEAD: the answer is the status, and the body is the whole changelog.
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, c.TagURL(version), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", "just-dashboard/"+c.current)
	resp, err := c.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("could not reach %s to ask whether %s has been released: %w", hostOf(c.baseURL), version, err)
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		c.mu.Lock()
		c.tagged[version] = true
		c.mu.Unlock()
		return true, nil
	case http.StatusNotFound:
		return false, nil
	}
	return false, fmt.Errorf("%s returned %s when asked whether %s has been released", hostOf(c.baseURL), resp.Status, version)
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
	// One byte past the ceiling, so a file that is too long is told apart from
	// one that is exactly long enough, and is reported as that rather than as
	// JSON that stops in the middle of a word.
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxManifest+1))
	if err != nil {
		return Manifest{}, err
	}
	if len(b) > maxManifest {
		return Manifest{}, fmt.Errorf("the published changelog is larger than the %d MiB this version reads", maxManifest>>20)
	}
	m, err := ParsePublishedManifest(b)
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
