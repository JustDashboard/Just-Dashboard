package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// A link fetched onto the server: the dashboard downloads it into a folder
// at the server's own speed, as a job that outlives the request that started
// it, and publishes it the way an upload is published — staged beside its
// destination, nothing under its name until it is whole, no name clobbered
// unless that was asked for, the folder's owner handed the file.
//
// The request is the server's, made as root's dashboard: it carries the link
// and nothing else — no cookie, no header of the dashboard's, no credential
// but one written into the link itself. It can reach whatever the server can
// reach, the loopback and the private network included; SECURITY.md says why
// that is no more than file.write already gives.

const (
	maxRedirects    = 10
	fetchesRunning  = 8
	fetchesPerOwner = 4
	fetchesKept     = 50
	fetchKeptFor    = time.Hour
	// fetchStall is how long a download may send nothing before it is given up.
	fetchStall = 60 * time.Second
	// roomEvery is how often, in bytes, a download checks the disk's room.
	roomEvery = 16 << 20
	// FetchUserAgent is the only header of its own a fetch sends.
	FetchUserAgent = "just-dashboard-fetch"
)

var (
	// ErrFetchScheme refuses a link that is not http or https.
	ErrFetchScheme = errors.New("only http and https links can be fetched")
	// ErrFetchLimit refuses a fetch past the bounds.
	ErrFetchLimit = errors.New("too many links are being fetched; wait for one to finish")
	// ErrFetchNotFound is a fetch the list does not hold.
	ErrFetchNotFound = errors.New("no such fetch")
)

// FetchRequest is a link to fetch into a folder. Name, when given, is used
// instead of the name the response or the link suggests.
type FetchRequest struct {
	Dir       string `json:"path"`
	URL       string `json:"url"`
	Name      string `json:"name,omitempty"`
	Overwrite bool   `json:"overwrite,omitempty"`
}

// Fetch is one link being fetched, or fetched a little while ago.
type Fetch struct {
	ID string `json:"id"`
	// URL is the link as it may be shown and logged: no query, no fragment,
	// no credentials.
	URL       string    `json:"url"`
	Dir       string    `json:"dir"`
	Name      string    `json:"name,omitempty"`
	Size      int64     `json:"size"`
	Received  int64     `json:"received"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	Redirects int       `json:"redirects"`
	Started   time.Time `json:"started"`
	Finished  time.Time `json:"finished,omitzero"`
	Owner     string    `json:"-"`
}

type fetchJob struct {
	mu       sync.Mutex
	f        Fetch
	received atomic.Int64
	stalled  atomic.Bool
	cancel   context.CancelFunc
	done     chan struct{}
}

func (j *fetchJob) snapshot() Fetch {
	j.mu.Lock()
	defer j.mu.Unlock()
	f := j.f
	f.Received = j.received.Load()
	return f
}

// Fetches runs and remembers the links being fetched.
type Fetches struct {
	svc    *Service
	client *http.Client
	ctx    context.Context
	stop   context.CancelFunc
	wg     sync.WaitGroup

	mu   sync.Mutex
	jobs map[string]*fetchJob
}

// NewFetches fetches into folders through svc. Its downloads end with Close.
func NewFetches(svc *Service) *Fetches {
	ctx, stop := context.WithCancel(context.Background())
	f := &Fetches{svc: svc, ctx: ctx, stop: stop, jobs: map[string]*fetchJob{}}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		// The bytes as the server sends them: no Accept-Encoding of ours, and
		// a .tar.gz served gzip-encoded is not unpacked on the way.
		DisableCompression: true,
		ForceAttemptHTTP2:  true,
		MaxIdleConns:       4,
		IdleConnTimeout:    30 * time.Second,
	}
	f.client = &http.Client{
		Transport: transport,
		// No cookie jar: nothing a fetched site sets is kept or sent back.
		Jar: nil,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return fmt.Errorf("the link redirected more than %d times", maxRedirects)
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("%w: the link redirected to %s", ErrFetchScheme, req.URL.Scheme+":")
			}
			// Nothing but the user agent follows a redirect.
			for key := range req.Header {
				if key != "User-Agent" {
					req.Header.Del(key)
				}
			}
			return nil
		},
	}
	return f
}

// Close cancels every running fetch and waits for each to clean up.
func (f *Fetches) Close() {
	f.stop()
	f.wg.Wait()
}

// ShownURL is a link as it may be shown and logged: no query, fragment or
// credentials, which is where a signed download link keeps its secret.
func ShownURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.RawQuery, u.Fragment, u.User = "", "", nil
	u.ForceQuery = false
	return u.String()
}

// Start checks the link and the folder and begins the download. What can be
// refused at once is refused here; what goes wrong later is the fetch's
// status.
func (f *Fetches) Start(owner string, req FetchRequest) (Fetch, error) {
	u, err := url.Parse(strings.TrimSpace(req.URL))
	if err != nil {
		return Fetch{}, fmt.Errorf("%w: that is not a link", fs.ErrInvalid)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Fetch{}, ErrFetchScheme
	}
	if u.Host == "" {
		return Fetch{}, fmt.Errorf("%w: that is not a link", fs.ErrInvalid)
	}
	var name string
	if req.Name != "" {
		if name, err = SafeName(req.Name); err != nil {
			return Fetch{}, err
		}
	}
	dir, err := f.svc.Resolve(req.Dir)
	if err != nil {
		return Fetch{}, err
	}
	if st, err := f.svc.Stat(dir); err != nil {
		return Fetch{}, err
	} else if !st.IsDir {
		return Fetch{}, ErrNotDir
	}
	id, _, err := newStageName()
	if err != nil {
		return Fetch{}, err
	}

	f.mu.Lock()
	f.prune()
	running, mine := 0, 0
	for _, j := range f.jobs {
		if s := j.snapshot(); s.Status == "running" {
			running++
			if s.Owner == owner {
				mine++
			}
		}
	}
	if running >= fetchesRunning || mine >= fetchesPerOwner {
		f.mu.Unlock()
		return Fetch{}, ErrFetchLimit
	}
	ctx, cancel := context.WithCancel(f.ctx)
	job := &fetchJob{
		f: Fetch{
			ID: id, URL: ShownURL(u.String()), Dir: dir, Name: name, Size: -1,
			Status: "running", Started: time.Now().UTC(), Owner: owner,
		},
		cancel: cancel,
		done:   make(chan struct{}),
	}
	f.jobs[id] = job
	f.wg.Add(1)
	f.mu.Unlock()

	go func() {
		defer f.wg.Done()
		defer close(job.done)
		defer cancel()
		err := f.run(ctx, job, u, req.Overwrite)
		job.mu.Lock()
		job.f.Finished = time.Now().UTC()
		switch {
		case err == nil:
			job.f.Status = "done"
		case ctx.Err() != nil && f.ctx.Err() == nil && !errors.Is(err, errStalled):
			job.f.Status, job.f.Error = "cancelled", ""
		case f.ctx.Err() != nil:
			job.f.Status, job.f.Error = "failed", "The dashboard stopped while this was being fetched."
		default:
			job.f.Status, job.f.Error = "failed", fetchSentence(err)
		}
		job.mu.Unlock()
	}()
	return job.snapshot(), nil
}

var errStalled = errors.New("the server stopped sending")

func (f *Fetches) run(ctx context.Context, job *fetchJob, u *url.URL, overwrite bool) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", FetchUserAgent)
	redirects := 0
	client := *f.client
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := f.client.CheckRedirect(req, via); err != nil {
			return err
		}
		redirects = len(via)
		job.mu.Lock()
		job.f.Redirects = redirects
		job.mu.Unlock()
		return nil
	}
	resp, err := client.Do(request)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return urlErr.Err
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("the server answered %s", resp.Status)
	}

	job.mu.Lock()
	name := job.f.Name
	job.mu.Unlock()
	if name == "" {
		if name, err = responseName(resp); err != nil {
			return err
		}
	}
	size := resp.ContentLength
	job.mu.Lock()
	job.f.Name, job.f.Size = name, size
	dir := job.f.Dir
	job.mu.Unlock()

	// A stalled server is given up on: every read resets the clock.
	stall := time.AfterFunc(fetchStall, func() { job.cancelStalled() })
	defer stall.Stop()
	body := &progressReader{r: resp.Body, n: &job.received, onRead: func() { stall.Reset(fetchStall) }}
	published, err := f.svc.fetchInto(dir, name, size, overwrite, body)
	if published != "" {
		job.mu.Lock()
		job.f.Name = published
		job.mu.Unlock()
	}
	if err != nil && job.stalled.Load() {
		return errStalled
	}
	return err
}

func (j *fetchJob) cancelStalled() {
	j.stalled.Store(true)
	j.cancel()
}

type progressReader struct {
	r      io.Reader
	n      *atomic.Int64
	onRead func()
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.n.Add(int64(n))
		p.onRead()
	}
	return n, err
}

// responseName is the name a response suggests: its Content-Disposition, or
// the last part of the link it finally came from.
func responseName(resp *http.Response) (string, error) {
	if disposition := resp.Header.Get("Content-Disposition"); disposition != "" {
		if _, params, err := mime.ParseMediaType(disposition); err == nil && params["filename"] != "" {
			return SafeName(params["filename"])
		}
	}
	last := path.Base(resp.Request.URL.Path)
	if unescaped, err := url.PathUnescape(last); err == nil {
		last = unescaped
	}
	if last == "/" || last == "." || last == "" {
		return "download", nil
	}
	return SafeName(last)
}

// fetchInto stages body beside dir/name and publishes it there. Without
// overwrite a taken name becomes "name (2).ext", the uploads' keep-both, so
// nothing already there is replaced. It returns the name it was published
// under.
func (s *Service) fetchInto(dir, name string, size int64, overwrite bool, body io.Reader) (string, error) {
	if s.run != nil {
		var published string
		var err error
		s.run(func() { published, err = s.inPlace().fetchInto(dir, name, size, overwrite, body) })
		return published, err
	}
	var t *uploadTarget
	var err error
	chosen := name
	for n := 2; ; n++ {
		t, err = s.openUploadTarget(filepath.Join(dir, chosen), overwrite, nil)
		if err == nil || overwrite || !errors.Is(err, fs.ErrExist) || n > 100 {
			break
		}
		chosen = numbered(name, n)
	}
	if err != nil {
		return "", err
	}
	defer t.Close()
	if size > 0 {
		if err := checkRoom(t.directory, size); err != nil {
			return "", err
		}
	}
	_, stage, err := newStageName()
	if err != nil {
		return "", err
	}
	tmp, err := t.parent.OpenFile(stage, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return "", err
	}
	defer tmp.Close()
	defer t.parent.Remove(stage)

	var written int64
	buf := make([]byte, 1<<20)
	nextCheck := int64(roomEvery)
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			if size < 0 && written+int64(n) >= nextCheck {
				if err := checkRoom(tmp, roomEvery); err != nil {
					return "", fmt.Errorf("stopped at %s before the disk filled: %w", HumanBytes(written), err)
				}
				nextCheck += roomEvery
			}
			if _, err := tmp.Write(buf[:n]); err != nil {
				return "", err
			}
			written += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	if size >= 0 && written != size {
		return "", fmt.Errorf("the connection ended after %s of %s", HumanBytes(written), HumanBytes(size))
	}
	if err := t.seal(tmp); err != nil {
		return "", err
	}
	if err := s.publishStaged(t, stage, overwrite, nil); err != nil {
		return "", err
	}
	return t.name, nil
}

// numbered is "name (n).ext", the keep-both name the upload review uses.
func numbered(name string, n int) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	if strings.HasSuffix(strings.ToLower(base), ".tar") {
		ext = base[len(base)-4:] + ext
		base = base[:len(base)-4]
	}
	if base == "" {
		base, ext = name, ""
	}
	return fmt.Sprintf("%s (%d)%s", base, n, ext)
}

// fetchSentence is a failed fetch's error the way the page shows it.
func fetchSentence(err error) string {
	switch {
	case errors.Is(err, errStalled):
		return "The server stopped sending for a minute; the fetch was given up."
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "The connection ended before the whole file arrived."
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "The link's host could not be found."
	}
	msg := err.Error()
	if msg == "" {
		return "The fetch failed."
	}
	return strings.ToUpper(msg[:1]) + msg[1:] + "."
}

// List is every fetch held: running ones and those finished in the last hour.
func (f *Fetches) List() []Fetch {
	f.mu.Lock()
	f.prune()
	out := make([]Fetch, 0, len(f.jobs))
	for _, j := range f.jobs {
		out = append(out, j.snapshot())
	}
	f.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out
}

// Get is one fetch.
func (f *Fetches) Get(id string) (Fetch, error) {
	f.mu.Lock()
	j := f.jobs[id]
	f.mu.Unlock()
	if j == nil {
		return Fetch{}, ErrFetchNotFound
	}
	return j.snapshot(), nil
}

// Cancel stops a running fetch and waits for it to clean up, or forgets a
// finished one.
func (f *Fetches) Cancel(id string) (Fetch, error) {
	f.mu.Lock()
	j := f.jobs[id]
	f.mu.Unlock()
	if j == nil {
		return Fetch{}, ErrFetchNotFound
	}
	if j.snapshot().Status == "running" {
		j.cancel()
		<-j.done
		return j.snapshot(), nil
	}
	f.mu.Lock()
	delete(f.jobs, id)
	f.mu.Unlock()
	return j.snapshot(), nil
}

// prune drops finished fetches past their hour, and the oldest finished ones
// past the bound. It runs with f.mu held.
func (f *Fetches) prune() {
	cutoff := time.Now().Add(-fetchKeptFor)
	var finished []Fetch
	for id, j := range f.jobs {
		s := j.snapshot()
		if s.Status == "running" {
			continue
		}
		if s.Finished.Before(cutoff) {
			delete(f.jobs, id)
			continue
		}
		finished = append(finished, s)
	}
	if len(finished) > fetchesKept {
		sort.Slice(finished, func(i, j int) bool { return finished[i].Finished.Before(finished[j].Finished) })
		for _, s := range finished[:len(finished)-fetchesKept] {
			delete(f.jobs, s.ID)
		}
	}
}
