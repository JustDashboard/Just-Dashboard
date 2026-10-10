package proxysvc

import (
	"cmp"
	"context"
	"crypto/x509"
	"errors"
	"net"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// The watch list checked on a schedule by the server itself. It used to be
// checked only while an administrator had the Certificates page open, so a
// certificate that expired over a weekend nobody looked was found on Monday
// by a visitor.

// WatchedEndpoint is one watched name and port, and the address to reach it
// at when that is not the name's own ("" for DNS).
type WatchedEndpoint struct {
	ID     int64
	Domain string
	Port   int
	IP     string
	// Kind is what the check asks: WatchTLS, a handshake and its
	// certificate, or WatchTCP, a network probe that only connects.
	Kind      string
	CheckedAt time.Time
}

// Watch kinds.
const (
	WatchTLS = "tls"
	WatchTCP = "tcp"
)

// WatchCheck is what one check of an endpoint found: Cert for a TLS watch,
// carrying a failed handshake's reason in its Error as CheckEndpoint returns
// it, or Probe for a TCP one.
type WatchCheck struct {
	EndpointID int64
	CheckedAt  time.Time
	Cert       *Certificate
	Probe      *ProbeCheck
}

// ProbeCheck is one TCP connection a network probe made, or failed to.
type ProbeCheck struct {
	OK bool `json:"ok"`
	// Address is the address dialled: the watch's own, or the first the
	// name resolved to.
	Address string `json:"address,omitempty"`
	// Ms is how long the connection took to open.
	Ms int64 `json:"ms,omitempty"`
	// State is connected, refused, timeout, unresolvable or error.
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

// CheckTCP opens one TCP connection to the endpoint, at ip when it names
// one, and closes it: whether something accepts connections there, and how
// fast. It sends nothing over the connection.
func CheckTCP(ctx context.Context, domain, ip string, port int) (*ProbeCheck, error) {
	host := domain
	if ip != "" {
		host = ip
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	start := time.Now()
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		check := &ProbeCheck{State: "error", Error: err.Error()}
		var dnsErr *net.DNSError
		switch {
		case errors.As(err, &dnsErr):
			check.State = "unresolvable"
		case isTimeout(err):
			check.State, check.Error = "timeout", "No answer to the connection within 5 seconds."
		case errors.Is(err, syscall.ECONNREFUSED):
			check.State = "refused"
		}
		return check, nil
	}
	check := &ProbeCheck{OK: true, State: "connected", Address: conn.RemoteAddr().String(), Ms: time.Since(start).Milliseconds()}
	conn.Close()
	return check, nil
}

// WatchStore is where the monitor reads its endpoints and keeps what it
// found; the api package keeps it in SQLite.
type WatchStore interface {
	WatchInterval(ctx context.Context) (time.Duration, error)
	WatchedEndpoints(ctx context.Context) ([]WatchedEndpoint, error)
	SaveWatchChecks(ctx context.Context, checks []WatchCheck) error
}

// Bounds on the check interval: under a minute is a handshake storm against
// hosts that are not ours, and over a day misses a certificate that expires
// between two checks with a day's warning.
const (
	DefaultWatchInterval = 5 * time.Minute
	MinWatchInterval     = time.Minute
	MaxWatchInterval     = 24 * time.Hour
)

// TLSMonitor checks watched endpoints whose last check is older than the
// interval. Now and Check are fields so a test can drive it with a fake clock
// and a fake handshake.
type TLSMonitor struct {
	store WatchStore
	Now   func() time.Time
	Check func(ctx context.Context, domain, ip string, port int) (*Certificate, error)
	// Probe is a network probe's check, CheckTCP outside tests.
	Probe func(ctx context.Context, domain, ip string, port int) (*ProbeCheck, error)
	// Tick is how often the loop looks for endpoints that are due, which is
	// finer than any interval so a changed interval takes effect within it.
	Tick time.Duration

	// pass keeps the schedule and an administrator's "check now" from
	// handshaking with the same endpoints twice at once. It is a channel so a
	// caller waiting for it still ends with its own budget.
	pass   chan struct{}
	cancel context.CancelFunc
	done   chan struct{}
}

func NewTLSMonitor(store WatchStore) *TLSMonitor {
	return &TLSMonitor{
		store: store, Now: time.Now, Check: CheckEndpointAt, Probe: CheckTCP, Tick: time.Minute,
		pass: make(chan struct{}, 1),
	}
}

// Start runs the schedule until ctx ends or Stop.
func (m *TLSMonitor) Start(ctx context.Context) {
	ctx, m.cancel = context.WithCancel(ctx)
	m.done = make(chan struct{})
	go func() {
		defer close(m.done)
		ticker := time.NewTicker(m.Tick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			pass, cancel := context.WithTimeout(ctx, 50*time.Second)
			_, _ = m.CheckDue(pass)
			cancel()
		}
	}()
}

// Stop ends the schedule and waits for a pass in flight; it is a no-op for a
// monitor never started.
func (m *TLSMonitor) Stop() {
	if m.cancel == nil {
		return
	}
	m.cancel()
	<-m.done
}

// CheckDue checks the endpoints the interval says are due, stalest first.
func (m *TLSMonitor) CheckDue(ctx context.Context) ([]WatchCheck, error) {
	interval, err := m.store.WatchInterval(ctx)
	if err != nil {
		return nil, err
	}
	endpoints, err := m.store.WatchedEndpoints(ctx)
	if err != nil {
		return nil, err
	}
	due := m.Now().Add(-interval)
	endpoints = slices.DeleteFunc(endpoints, func(e WatchedEndpoint) bool { return e.CheckedAt.After(due) })
	return m.CheckEndpoints(ctx, endpoints)
}

// CheckAll checks every endpoint, stalest first, until ctx ends.
func (m *TLSMonitor) CheckAll(ctx context.Context) ([]WatchCheck, error) {
	endpoints, err := m.store.WatchedEndpoints(ctx)
	if err != nil {
		return nil, err
	}
	return m.CheckEndpoints(ctx, endpoints)
}

// CheckEndpoints checks eight at a time under ctx's budget. A handshake still
// going when the budget ends is abandoned and an endpoint not started is not
// started; both keep what they had, so each row's time says how old its
// answer is, and the stalest go first so a list longer than one budget is
// covered over the next passes rather than the same tail missing every time.
// What was found is saved even when ctx has ended: those handshakes were made.
func (m *TLSMonitor) CheckEndpoints(ctx context.Context, endpoints []WatchedEndpoint) ([]WatchCheck, error) {
	select {
	case m.pass <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-m.pass }()
	queue := slices.Clone(endpoints)
	slices.SortStableFunc(queue, func(a, b WatchedEndpoint) int {
		return cmp.Compare(a.CheckedAt.Unix(), b.CheckedAt.Unix())
	})
	var (
		mu      sync.Mutex
		checked []WatchCheck
		wg      sync.WaitGroup
	)
	sem := make(chan struct{}, 8)
	for _, e := range queue {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			check := WatchCheck{EndpointID: e.ID}
			var err error
			if e.Kind == WatchTCP {
				check.Probe, err = m.Probe(ctx, e.Domain, e.IP, e.Port)
			} else {
				check.Cert, err = m.Check(ctx, e.Domain, e.IP, e.Port)
			}
			if err != nil {
				return
			}
			check.CheckedAt = m.Now().UTC().Truncate(time.Second)
			mu.Lock()
			checked = append(checked, check)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(checked) == 0 {
		return checked, nil
	}
	return checked, m.store.SaveWatchChecks(context.WithoutCancel(ctx), checked)
}

// CheckEndpointAt is CheckEndpoint reaching the name at ip instead of where
// DNS points: the origin behind a CDN, or one server of several behind one
// name, which a check by name alone reaches only when DNS happens to pick it.
// The name is still what the handshake asks for and what the certificate is
// checked against.
func CheckEndpointAt(ctx context.Context, domain, ip string, port int) (*Certificate, error) {
	if ip == "" {
		return CheckEndpoint(ctx, domain, port)
	}
	failed := func(reason string) *Certificate {
		return &Certificate{Name: domain, Domains: []string{domain}, Source: "live", UsedBy: []string{}, Error: reason}
	}
	conn, err := dialTLS(ctx, net.JoinHostPort(ip, strconv.Itoa(port)), domain, startTLSPorts[port], 0, 0)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if isTimeout(err) {
			return failed("No TLS handshake from " + ip + " within 10 seconds."), nil
		}
		return failed(err.Error()), nil
	}
	state := conn.ConnectionState()
	conn.Close()
	if len(state.PeerCertificates) == 0 {
		return failed("The handshake completed without a certificate."), nil
	}
	leaf := state.PeerCertificates[0]
	cert := summarise(leaf, domain, "")
	cert.Source = "live"
	roots, _ := x509.SystemCertPool()
	if _, err := leaf.Verify(x509.VerifyOptions{
		DNSName: domain, Roots: roots, Intermediates: intermediates(state.PeerCertificates),
	}); err != nil {
		cert.Error = err.Error()
	}
	return cert, nil
}

// ParseWatchIP reads the optional address a watched name is reached at.
func ParseWatchIP(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	ip := net.ParseIP(raw)
	if ip == nil {
		return "", errors.New(raw + " is not an IP address")
	}
	return ip.String(), nil
}
