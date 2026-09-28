package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/portalloc"
)

// Live request metrics come from nginx's stub_status module, which is built
// into every nginx this dashboard supports and answers with five counters and
// three gauges. The module needs a location to answer on, so switching the
// metrics on writes one server of the dashboard's own into conf.d, listening
// on loopback only, and switching them off removes it again.

const (
	// statusFileName is the dashboard's status server in conf.d. The jd-
	// prefix and the marker on its first line are what the site listing
	// skips as the dashboard's own plumbing rather than a site.
	statusFileName = "jd-status.conf"
	statusOwned    = "Just Dashboard owned"
	statusMarker   = "# " + statusOwned + ": live request metrics for the proxy overview."
	statusLocation = "/jd-status"

	// statusPortPreferred is where the port search starts. It is below the
	// kernel's ephemeral range, which a listener must stay out of: an
	// outgoing connection holding the port at the moment nginx reloads
	// would keep the status server from binding at all.
	statusPortPreferred = 19081
	statusPortMinimum   = 1024

	// StatusInterval is how often the sampler reads the counters, and
	// statusWindow how much of that it keeps: an hour of five-second
	// readings is 720.
	StatusInterval = 5 * time.Second
	statusWindow   = time.Hour
	// statusVerifyFor is how long a switch-on waits for the reloaded nginx
	// to answer. A reload is a signal nginx acts on after the command has
	// returned, and a large configuration takes the master a while to read.
	statusVerifyFor = 10 * time.Second
)

var (
	// ErrStatusUnsupported is a proxy directory with no conf.d, the one
	// directory nginx includes inside http{} on every layout the dashboard
	// knows.
	ErrStatusUnsupported = errors.New("live metrics need a conf.d directory in the nginx configuration directory")
	// ErrStatusForeign is a file at the status server's path that the
	// dashboard did not write. It is somebody's configuration, and switching
	// the metrics on or off is not a reason to replace or remove it.
	ErrStatusForeign = errors.New("conf.d/" + statusFileName + " was not written by the dashboard, so it was left alone: move it aside to switch live metrics from here")
	// ErrStatusNotIncluded is a conf.d that nginx does not read, so a status
	// server written there would never answer.
	ErrStatusNotIncluded = errors.New("nginx does not read conf.d/*.conf, so a status server there would never answer; nothing was changed. Include conf.d/*.conf inside the http block of nginx.conf to use live metrics")
	// ErrStatusReload is a reload nginx refused or could not be sent.
	ErrStatusReload = errors.New("nginx did not reload")
	// ErrStatusNoAnswer is a reloaded nginx that still did not answer on the
	// status server's address.
	ErrStatusNoAnswer = errors.New("the status server did not answer")
)

// statusFailure is one of the errors above with the sentence that says what
// happened this time, which is what the operator reads.
type statusFailure struct {
	kind    error
	message string
}

func (e *statusFailure) Error() string { return e.message }
func (e *statusFailure) Unwrap() error { return e.kind }

// StubStatus is one reading of nginx's stub_status page. Accepts, Handled and
// Requests count up from the moment nginx started, and survive a reload;
// Active, Reading, Writing and Waiting are the connections open right now.
type StubStatus struct {
	Active   int64 `json:"active"`
	Accepts  int64 `json:"accepts"`
	Handled  int64 `json:"handled"`
	Requests int64 `json:"requests"`
	Reading  int64 `json:"reading"`
	Writing  int64 `json:"writing"`
	Waiting  int64 `json:"waiting"`
}

var (
	stubActiveRe   = regexp.MustCompile(`(?m)^Active connections:\s*(\d+)\s*$`)
	stubCountersRe = regexp.MustCompile(`(?m)^server accepts handled requests\s*\n\s*(\d+)\s+(\d+)\s+(\d+)\s*$`)
	stubStatesRe   = regexp.MustCompile(`(?m)^Reading:\s*(\d+)\s+Writing:\s*(\d+)\s+Waiting:\s*(\d+)\s*$`)
)

// ParseStubStatus reads the page ngx_http_stub_status_module writes:
//
//	Active connections: 291
//	server accepts handled requests
//	 16630948 16630948 31070465
//	Reading: 6 Writing: 179 Waiting: 106
//
// Every line has to be there. Whatever else answers on the port — another
// server that took it, an error page — is refused rather than read as zeros.
func ParseStubStatus(text string) (StubStatus, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	active := stubActiveRe.FindStringSubmatch(text)
	counters := stubCountersRe.FindStringSubmatch(text)
	states := stubStatesRe.FindStringSubmatch(text)
	if active == nil || counters == nil || states == nil {
		return StubStatus{}, errors.New("the answer is not nginx's stub_status page")
	}
	var fields []int64
	for _, digits := range append(append(active[1:], counters[1:]...), states[1:]...) {
		n, err := strconv.ParseInt(digits, 10, 64)
		if err != nil {
			return StubStatus{}, fmt.Errorf("stub_status counter %q: %w", digits, err)
		}
		fields = append(fields, n)
	}
	return StubStatus{
		Active: fields[0], Accepts: fields[1], Handled: fields[2], Requests: fields[3],
		Reading: fields[4], Writing: fields[5], Waiting: fields[6],
	}, nil
}

// RenderStatusServer is the status server the dashboard writes to conf.d.
//
// Loopback only, and a second fence inside it: the location refuses anything
// but 127.0.0.1 even if an edit by hand widens the listen. It logs nothing, or
// every five seconds would be a line in the access log for as long as the
// metrics are on; keep-alive is off, so no connection of the sampler's lingers
// between readings to be counted as a visitor's; and every other path is a
// 404 rather than nginx's welcome page.
func RenderStatusServer(port int) string {
	return fmt.Sprintf(`%s
# Written and removed by the Live traffic switch on the proxy overview.
server {
    listen 127.0.0.1:%d;
    access_log off;
    keepalive_timeout 0;

    location = %s {
        stub_status;
        allow 127.0.0.1;
        deny all;
    }

    location / {
        return 404;
    }
}
`, statusMarker, port, statusLocation)
}

var statusListenRe = regexp.MustCompile(`(?m)^\s*listen\s+127\.0\.0\.1:(\d+)\s*;`)

// StatusServer is what is at the status server's path on disk.
type StatusServer struct {
	Path string
	// Present is any file there; Owned is one carrying the dashboard's
	// marker on its first line.
	Present bool
	Owned   bool
	// Port is the loopback port the file listens on, or 0 when it names
	// none this can read.
	Port int
}

// Endpoint is the address the sampler reads, or empty when there is none.
func (st StatusServer) Endpoint() string {
	if !st.Owned || st.Port == 0 {
		return ""
	}
	return statusEndpoint(st.Port)
}

func statusEndpoint(port int) string {
	return "http://127.0.0.1:" + strconv.Itoa(port) + statusLocation
}

func (s *Service) statusFile() string {
	return filepath.Join(s.nginxDir, "conf.d", statusFileName)
}

// StatusServer reads the status server's file as it is now. The file is the
// switch's state: an operator who removes it by hand has switched the metrics
// off, and one who changes its port has moved them.
func (s *Service) StatusServer() StatusServer {
	st := StatusServer{Path: s.statusFile()}
	content, present := readIfPresent(st.Path)
	st.Present = present
	st.Owned, st.Port = parseStatusFile(content)
	return st
}

// StatusSupported says whether the metrics can be switched on here, and why
// not when they cannot.
func (s *Service) StatusSupported() (bool, string) {
	info, err := os.Stat(filepath.Dir(s.statusFile()))
	if err != nil || !info.IsDir() {
		return false, fmt.Sprintf("%s has no conf.d directory, which is where live metrics put nginx's status server", s.nginxDir)
	}
	return true, ""
}

func parseStatusFile(content string) (owned bool, port int) {
	first, _, _ := strings.Cut(content, "\n")
	if !strings.Contains(first, statusOwned) {
		return false, 0
	}
	if m := statusListenRe.FindStringSubmatch(content); m != nil {
		if p, err := strconv.Atoi(m[1]); err == nil && p > 0 && p < 65536 {
			port = p
		}
	}
	return true, port
}

// StatusChange is what switching the status server did.
type StatusChange struct {
	// Changed is false when the switch was already where it was asked to be.
	Changed  bool
	Port     int
	Reloaded bool
	// Validation is the configuration test the change ran, when it ran one.
	Validation *ValidationResult
}

// chooseStatusPort finds a loopback port nothing holds. The backend shares
// the host's network namespace with nginx, so a port it can bind here is one
// nginx can bind there.
func chooseStatusPort() (int, error) {
	return portalloc.Select(statusPortPreferred, statusPortMinimum, nil, func(port int) error {
		return portalloc.Available("127.0.0.1", "tcp", port)
	})
}

// EnableStatusServer writes the status server, proves nginx loads it, reloads
// nginx and waits for verify to read the counters from it. Any step that fails
// puts the host back as it was — the file, and after a reload the running
// nginx too — so the switch is either on and answering or off.
//
// A status file the dashboard already wrote keeps its port. When it is in
// place and answering, nothing is written or reloaded.
func (s *Service) EnableStatusServer(ctx context.Context, verify func(ctx context.Context, endpoint string) error) (*StatusChange, error) {
	if !hostexec.Available("nginx") {
		return nil, ErrNoProxy
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ok, reason := s.StatusSupported(); !ok {
		return nil, &statusFailure{ErrStatusUnsupported, reason}
	}
	path := s.statusFile()
	original, existed := readIfPresent(path)
	change := &StatusChange{}
	if existed {
		owned, port := parseStatusFile(original)
		if !owned {
			return nil, ErrStatusForeign
		}
		change.Port = port
	}
	if change.Port == 0 {
		port, err := chooseStatusPort()
		if err != nil {
			return nil, fmt.Errorf("no loopback port is free for the status server: %w", err)
		}
		change.Port = port
	}
	content := RenderStatusServer(change.Port)
	endpoint := statusEndpoint(change.Port)
	if existed && original == content {
		quick, cancel := context.WithTimeout(ctx, 2*time.Second)
		answered := verify(quick, endpoint) == nil
		cancel()
		if answered {
			return change, nil
		}
	}

	if err := writeAtomic(path, content); err != nil {
		return nil, err
	}
	undo := func() { restoreConfig(path, original, existed) }
	started := time.Now()
	change.Validation = runValidator(ctx, "nginx", "-t")
	if !change.Validation.Valid {
		undo()
		return change, ErrInvalidConf
	}
	loaded, err := s.nginxLoads(ctx, path)
	if err != nil {
		undo()
		return change, err
	}
	if !loaded {
		undo()
		return change, ErrStatusNotIncluded
	}
	if out, err := reloadNginx(ctx); err != nil {
		undo()
		return change, &statusFailure{ErrStatusReload,
			"nginx did not reload, so the status server was not added — is nginx running? It said: " + out}
	}
	change.Reloaded = true
	waiting, cancel := context.WithTimeout(ctx, statusVerifyFor)
	err = verify(waiting, endpoint)
	cancel()
	if err != nil {
		// nginx acts on a reload after the command returns, and a listen it
		// cannot bind — a port taken since it was chosen, a policy such as
		// SELinux that limits which ports nginx may use — is written to its
		// error log while the old configuration goes on serving. A dashboard
		// that is not on the host's network cannot reach a bound one either.
		// The file comes out again and nginx is reloaded without it, so the
		// host is where it was.
		undo()
		if back := runValidator(ctx, "nginx", "-t"); back.Valid {
			_, _ = reloadNginx(ctx)
		}
		return change, &statusFailure{ErrStatusNoAnswer, fmt.Sprintf(
			"nginx reloaded, but the status server did not answer within %s: %v. It was taken out again and nginx reloaded without it. If nginx's error log shows it could not bind 127.0.0.1:%d, another program holds the port or a policy such as SELinux limits the ports nginx may use; if it did, this dashboard is not on the host's network.",
			statusVerifyFor, err, change.Port)}
	}
	s.remember(KindNginx, started, change.Validation)
	s.recordChange(ctx, Change{Path: path, Action: ChangeWrite,
		Before: []byte(original), BeforeExisted: existed, After: []byte(content)})
	change.Changed = true
	return change, nil
}

// DisableStatusServer removes the status server and reloads nginx. The file
// comes out only if the configuration still tests clean without it, the rule
// every change here keeps; a reload that fails after that leaves the file
// removed, since nginx stops serving the status server at its next reload
// or restart whatever happens now.
func (s *Service) DisableStatusServer(ctx context.Context) (*StatusChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.statusFile()
	original, existed := readIfPresent(path)
	change := &StatusChange{}
	if !existed {
		return change, nil
	}
	owned, port := parseStatusFile(original)
	if !owned {
		return nil, ErrStatusForeign
	}
	change.Port = port
	if err := os.Remove(path); err != nil {
		return nil, err
	}
	started := time.Now()
	change.Validation = runValidator(ctx, "nginx", "-t")
	if !change.Validation.Valid {
		restoreConfig(path, original, true)
		return change, ErrInvalidConf
	}
	s.remember(KindNginx, started, change.Validation)
	s.recordChange(ctx, Change{Path: path, Action: ChangeDelete,
		Before: []byte(original), BeforeExisted: true})
	change.Changed = true
	if out, err := reloadNginx(ctx); err != nil {
		return change, &statusFailure{ErrStatusReload,
			"The status server's file was removed, but nginx did not reload, so it answers until nginx next reloads or restarts. nginx said: " + out}
	}
	change.Reloaded = true
	return change, nil
}

// nginxLoads is whether nginx reads path, from its own account of the files
// it loads. conf.d is included inside http{} on every stock layout, but
// nginx.conf is the operator's, and `nginx -t` passes over a file it never
// reads. Must be called with s.mu held.
func (s *Service) nginxLoads(ctx context.Context, path string) (bool, error) {
	files, err := s.dumpNginx(ctx)
	if err != nil {
		return false, err
	}
	want := resolvedFile(path)
	for _, f := range files {
		if resolvedFile(f.Path) == want {
			return true, nil
		}
	}
	return false, nil
}

func reloadNginx(ctx context.Context) (string, error) {
	out, err := hostexec.Command(ctx, "nginx", "-s", "reload").CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil && text == "" {
		text = err.Error()
	}
	return text, err
}

// StatusSample is one reading as the overview draws it. The sampler's own
// request is left out of every figure: it is the connection and the request
// that produced the page, and on a quiet host it would be the only traffic.
type StatusSample struct {
	Seq     int64     `json:"seq"`
	At      time.Time `json:"at"`
	Active  int64     `json:"active"`
	Reading int64     `json:"reading"`
	Writing int64     `json:"writing"`
	Waiting int64     `json:"waiting"`
	// Requests is how many requests a second nginx answered since the
	// reading before. It is null for the first reading, after nginx
	// restarted (its counters start again from zero) and after a gap of
	// more than a few intervals, where one average would stand for minutes.
	Requests *float64 `json:"requests"`
	// Dropped is the connections nginx accepted and could not handle since
	// the reading before: every worker was at its worker_connections limit.
	Dropped int64 `json:"dropped"`

	// served is the request count behind Requests, kept for the window's
	// total whether or not a rate was drawn for it.
	served int64
}

// StatusReport is what GET /proxy/metrics answers.
type StatusReport struct {
	// Supported is false when the metrics cannot be switched on here, with
	// Reason saying why.
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
	// Enabled is the dashboard's status file being in place; Foreign is a
	// file at its path the dashboard did not write.
	Enabled  bool   `json:"enabled"`
	Foreign  bool   `json:"foreign,omitempty"`
	Path     string `json:"path"`
	Endpoint string `json:"endpoint,omitempty"`
	// Interval is the seconds between readings.
	Interval int `json:"interval"`
	// Epoch names the series. It changes when the series starts again — the
	// metrics switched off and on, the port moved, the dashboard restarted —
	// and a client holding another epoch's readings drops them.
	Epoch int64 `json:"epoch"`
	// Samples are the readings after the one the caller already has, or the
	// whole last hour.
	Samples []StatusSample `json:"samples"`
	// Current is the newest reading, whatever the caller already has.
	Current *StatusSample `json:"current,omitempty"`
	// Totals are nginx's own counters at the newest reading, since it
	// started, the sampler's requests included.
	Totals *StubStatus `json:"totals,omitempty"`
	// HourRequests and HourDropped add up the last hour's readings.
	HourRequests int64 `json:"hourRequests"`
	HourDropped  int64 `json:"hourDropped"`
	// Error is why the last reading failed, and FailingSince when the
	// readings began to fail. Both are empty while nginx answers.
	Error        string     `json:"error,omitempty"`
	FailingSince *time.Time `json:"failingSince,omitempty"`
}

// StatusSampler reads the status server every StatusInterval and keeps the
// last hour. It reads only the address in the dashboard's own status file, so
// no caller can aim it anywhere, and it holds the readings in memory: an hour
// of them is what the overview draws, and a restart of the dashboard starts
// the hour again.
type StatusSampler struct {
	svc      *Service
	client   *http.Client
	interval time.Duration
	window   time.Duration
	now      func() time.Time

	// pollMu keeps one reading in flight at a time, so readings land in the
	// order nginx produced them and a delta is never taken backwards.
	pollMu sync.Mutex

	mu       sync.Mutex
	endpoint string
	epoch    int64
	seq      int64
	samples  []StatusSample
	previous *statusReading
	failure  string
	failing  time.Time

	stop context.CancelFunc
	done chan struct{}
}

type statusReading struct {
	status StubStatus
	at     time.Time
}

func NewStatusSampler(svc *Service) *StatusSampler {
	return &StatusSampler{
		svc: svc,
		client: &http.Client{
			Timeout: 3 * time.Second,
			Transport: &http.Transport{
				// The sampler's connection closes after every reading, or
				// it would sit in nginx's Waiting count between them.
				DisableKeepAlives: true,
				Proxy:             nil,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		interval: StatusInterval,
		window:   statusWindow,
		now:      time.Now,
	}
}

// Start reads at once and then every interval, until ctx ends or Stop.
func (m *StatusSampler) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	m.stop, m.done = cancel, make(chan struct{})
	go func() {
		defer close(m.done)
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		for {
			pass, cancelPass := context.WithTimeout(ctx, m.interval)
			_ = m.Poll(pass)
			cancelPass()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// Stop ends the loop and waits for it. A sampler never started stops at once.
func (m *StatusSampler) Stop() {
	if m.stop == nil {
		return
	}
	m.stop()
	<-m.done
}

// Poll takes one reading from the address the status file names, or forgets
// the series when there is no file to read.
func (m *StatusSampler) Poll(ctx context.Context) error {
	endpoint := m.svc.StatusServer().Endpoint()
	if endpoint == "" {
		m.mu.Lock()
		m.restart("")
		m.mu.Unlock()
		return nil
	}
	return m.read(ctx, endpoint)
}

// Enable switches the status server on and takes its first reading as the
// proof it answers.
func (m *StatusSampler) Enable(ctx context.Context) (*StatusChange, error) {
	return m.svc.EnableStatusServer(ctx, m.await)
}

// Disable switches the status server off and forgets its readings.
func (m *StatusSampler) Disable(ctx context.Context) (*StatusChange, error) {
	change, err := m.svc.DisableStatusServer(ctx)
	if err == nil || errors.Is(err, ErrStatusReload) {
		m.mu.Lock()
		m.restart("")
		m.mu.Unlock()
	}
	return change, err
}

// await reads endpoint until it answers or ctx ends. Its readings go into
// the series like any other, so the sampler's own requests stay counted.
func (m *StatusSampler) await(ctx context.Context, endpoint string) error {
	for {
		err := m.read(ctx, endpoint)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// restart begins a new series for endpoint. Must be called with m.mu held.
func (m *StatusSampler) restart(endpoint string) {
	if endpoint == m.endpoint && (endpoint != "" || len(m.samples) == 0) {
		return
	}
	m.endpoint = endpoint
	m.samples = nil
	m.previous = nil
	m.failure, m.failing = "", time.Time{}
	// Milliseconds of the wall clock, moved on by one when two series start
	// within the same millisecond, so a client can never mistake a new
	// series for the one it holds.
	epoch := m.now().UnixMilli()
	if epoch <= m.epoch {
		epoch = m.epoch + 1
	}
	m.epoch = epoch
}

func (m *StatusSampler) read(ctx context.Context, endpoint string) error {
	m.pollMu.Lock()
	defer m.pollMu.Unlock()
	at := m.now()
	status, err := m.fetch(ctx, endpoint)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.restart(endpoint)
	if err != nil {
		if m.failure == "" {
			m.failing = at
		}
		m.failure = err.Error()
		return err
	}
	m.failure, m.failing = "", time.Time{}
	// The reading's own connection is open and being written to while
	// nginx counts, so it is one of the active and one of the writing.
	sample := StatusSample{
		At:      at,
		Active:  max(0, status.Active-1),
		Reading: status.Reading,
		Writing: max(0, status.Writing-1),
		Waiting: status.Waiting,
	}
	if prev := m.previous; prev != nil && !restarted(prev.status, status) {
		// One of the requests since the last reading is this one.
		sample.served = max(0, status.Requests-prev.status.Requests-1)
		sample.Dropped = max(0, (status.Accepts-status.Handled)-(prev.status.Accepts-prev.status.Handled))
		if gap := at.Sub(prev.at); gap > 0 && gap <= 4*m.interval {
			rate := float64(sample.served) / gap.Seconds()
			sample.Requests = &rate
		}
	}
	m.previous = &statusReading{status: status, at: at}
	m.seq++
	sample.Seq = m.seq
	m.samples = append(m.samples, sample)
	m.trim(at)
	return nil
}

// restarted is nginx having started again between two readings: its counters
// begin from zero, and a delta across that would be negative.
func restarted(prev, next StubStatus) bool {
	return next.Accepts < prev.Accepts || next.Handled < prev.Handled || next.Requests < prev.Requests
}

// trim drops what has aged out of the window. Must be called with m.mu held.
func (m *StatusSampler) trim(now time.Time) {
	cut := 0
	for cut < len(m.samples) && now.Sub(m.samples[cut].At) > m.window {
		cut++
	}
	if cut > 0 {
		m.samples = append([]StatusSample(nil), m.samples[cut:]...)
	}
}

func (m *StatusSampler) fetch(ctx context.Context, endpoint string) (StubStatus, error) {
	address := strings.TrimSuffix(strings.TrimPrefix(endpoint, "http://"), statusLocation)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return StubStatus{}, err
	}
	response, err := m.client.Do(request)
	if err != nil {
		var timeout net.Error
		switch {
		case errors.Is(err, syscall.ECONNREFUSED):
			return StubStatus{}, fmt.Errorf("nothing is listening on %s", address)
		case errors.As(err, &timeout) && timeout.Timeout(), errors.Is(err, context.DeadlineExceeded):
			return StubStatus{}, fmt.Errorf("%s did not answer in time", address)
		}
		return StubStatus{}, fmt.Errorf("%s: %w", address, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return StubStatus{}, fmt.Errorf("%s: %w", address, err)
	}
	if response.StatusCode != http.StatusOK {
		return StubStatus{}, fmt.Errorf("%s%s answered %d, not nginx's status page", address, statusLocation, response.StatusCode)
	}
	status, err := ParseStubStatus(string(body))
	if err != nil {
		return StubStatus{}, fmt.Errorf("%s%s: %w", address, statusLocation, err)
	}
	return status, nil
}

// Report is the metrics as they stand. A caller holding epoch's readings up
// to seq after is sent only the newer ones; any other caller the whole hour.
func (m *StatusSampler) Report(epoch, after int64) StatusReport {
	server := m.svc.StatusServer()
	report := StatusReport{
		Path:     server.Path,
		Enabled:  server.Present && server.Owned,
		Foreign:  server.Present && !server.Owned,
		Endpoint: server.Endpoint(),
		Interval: int(m.interval / time.Second),
		Samples:  []StatusSample{},
	}
	report.Supported, report.Reason = m.svc.StatusSupported()

	m.mu.Lock()
	defer m.mu.Unlock()
	report.Epoch = m.epoch
	// Readings of another address are not readings of this one: the file
	// changed since the last poll, and the next starts the series again.
	if report.Endpoint == "" || report.Endpoint != m.endpoint {
		return report
	}
	if m.failure != "" {
		since := m.failing
		report.Error, report.FailingSince = m.failure, &since
	}
	for _, sample := range m.samples {
		report.HourRequests += sample.served
		report.HourDropped += sample.Dropped
		if epoch != m.epoch || sample.Seq > after {
			report.Samples = append(report.Samples, sample)
		}
	}
	if n := len(m.samples); n > 0 {
		current := m.samples[n-1]
		report.Current = &current
	}
	if m.previous != nil {
		totals := m.previous.status
		report.Totals = &totals
	}
	return report
}
