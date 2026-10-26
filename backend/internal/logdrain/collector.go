package logdrain

import (
	"context"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

// Target is one deployment environment a drain may read.
type Target struct {
	ProjectID     int64
	Project       string
	EnvironmentID int64
	Environment   string
	// Route names the environment's request record, the ingress route
	// deploy.RouteNameFor gives it; empty when it has no domain.
	Route string
}

// Catalogue lists the environments that exist now.
type Catalogue interface {
	DrainTargets(ctx context.Context) ([]Target, error)
}

// Container is a deployment's container, running now.
type Container struct {
	ID            string
	Name          string
	EnvironmentID int64
	Created       time.Time
}

// Line is one line of a container's output with the instant Docker took it.
type Line struct {
	At     time.Time
	Stream string
	Text   string
}

// ContainerSource reads deployment containers' output through Docker.
type ContainerSource interface {
	DeploymentContainers(ctx context.Context) ([]Container, error)
	Follow(ctx context.Context, id string, since time.Time) (<-chan Line, io.Closer, error)
}

// RequestSource is the ingress's request record: what came after a cursor.
type RequestSource interface {
	After(ctx context.Context, route string, after uint64, limit int) ([]accesslog.Entry, uint64, error)
}

// How often the set of things followed is compared with what drains ask for,
// and how often a followed request record is read.
const (
	reconcileEvery = 10 * time.Second
	// Five seconds is a request record read twelve times a minute per
	// routed environment — each a short read past the cursor, through an
	// exec into the ingress on Docker Caddy — and a delay nobody watching a
	// log service notices.
	requestPollEvery = 5 * time.Second
)

// Manager runs the drains: one shipper per enabled drain, and one reader per
// container and request record some drain asks for. A source nobody asks
// for is not read.
type Manager struct {
	store      *Store
	catalogue  Catalogue
	containers ContainerSource
	requests   RequestSource
	log        *slog.Logger
	client     *http.Client
	host       string
	started    time.Time

	mu        sync.RWMutex
	ctx       context.Context
	shippers  map[int64]*shipper
	followers map[string]*reader
	tailers   map[int64]*reader
	reconcile chan struct{}
}

type reader struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (r *reader) stop() {
	r.cancel()
	<-r.done
}

func NewManager(store *Store, catalogue Catalogue, containers ContainerSource, requests RequestSource, log *slog.Logger) *Manager {
	return &Manager{
		store: store, catalogue: catalogue, containers: containers, requests: requests, log: log,
		client: newClient(), host: hostName(), started: time.Now(),
		shippers: map[int64]*shipper{}, followers: map[string]*reader{}, tailers: map[int64]*reader{},
		reconcile: make(chan struct{}, 1),
	}
}

// Start loads the drains and begins following what they ask for, until ctx
// ends.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	m.ctx = ctx
	m.mu.Unlock()
	if err := m.Reload(ctx); err != nil {
		return err
	}
	go func() {
		ticker := time.NewTicker(reconcileEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				m.shutdown()
				return
			case <-ticker.C:
			case <-m.reconcile:
			}
			m.reconcileSources(ctx)
		}
	}()
	return nil
}

// Reload rebuilds the shippers from the store after a drain was created,
// edited, paused or removed. A drain whose settings changed keeps what it had
// queued; a removed or paused one drops it.
func (m *Manager) Reload(ctx context.Context) error {
	rows, err := m.store.all(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	next := map[int64]*shipper{}
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		current := m.shippers[row.ID]
		if current != nil && sameDrain(current, row) {
			next[row.ID] = current
			delete(m.shippers, row.ID)
			continue
		}
		fresh := newShipper(row, m.store, m.client, m.host, m.log)
		if current != nil {
			fresh.queue, fresh.dropped = current.detach()
			delete(m.shippers, row.ID)
		}
		fresh.start(parent)
		next[row.ID] = fresh
	}
	removed := m.shippers
	m.shippers = next
	m.mu.Unlock()
	for _, gone := range removed {
		gone.detach()
	}
	select {
	case m.reconcile <- struct{}{}:
	default:
	}
	return nil
}

func sameDrain(current *shipper, row *drainRow) bool {
	a, b := current.drain, row.Drain
	if a.Kind != b.Kind || a.Endpoint != b.Endpoint || a.Enabled != b.Enabled || mustJSON(a.Streams) != mustJSON(b.Streams) ||
		mustJSON(a.Projects) != mustJSON(b.Projects) || mustJSON(current.headers) != mustJSON(row.headers) {
		return false
	}
	current.drain.Name = b.Name
	return true
}

// Queued is how many records wait in memory for each drain.
func (m *Manager) Queued() map[int64]int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := map[int64]int{}
	for id, shipper := range m.shippers {
		out[id] = shipper.queued()
	}
	return out
}

// Test sends one record through a drain's own settings and says what the
// destination answered. It is not counted as shipped.
func (m *Manager) Test(ctx context.Context, id int64) error {
	row, err := m.store.get(ctx, id)
	if err != nil {
		return err
	}
	record := Record{
		At: time.Now().UTC(), Project: "just-dashboard", Environment: "test", Stream: StreamOutput, IOStream: "stdout",
		Message: "Test record from Just Dashboard: the drain " + row.Name + " delivers here.",
	}
	return deliverBatch(ctx, m.client, row.Kind, row.Endpoint, row.headers, []Record{record}, m.host)
}

// wanted is what the drains ask to be read: per stream, every project
// (nil) or the projects named.
func (m *Manager) wanted() (output, requests map[int64]bool, allOutput, allRequests bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	output, requests = map[int64]bool{}, map[int64]bool{}
	for _, shipper := range m.shippers {
		for _, stream := range shipper.drain.Streams {
			all := len(shipper.drain.Projects) == 0
			for _, id := range shipper.drain.Projects {
				if stream == StreamOutput {
					output[id] = true
				} else {
					requests[id] = true
				}
			}
			if stream == StreamOutput && all {
				allOutput = true
			}
			if stream == StreamRequests && all {
				allRequests = true
			}
		}
	}
	return output, requests, allOutput, allRequests
}

// reconcileSources starts a reader for every container and request record a
// drain asks for, and stops the readers nobody asks for any more.
func (m *Manager) reconcileSources(ctx context.Context) {
	output, requests, allOutput, allRequests := m.wanted()
	targets := map[int64]Target{}
	if len(output) > 0 || len(requests) > 0 || allOutput || allRequests {
		list, err := m.catalogue.DrainTargets(ctx)
		if err != nil {
			if m.log != nil {
				m.log.Warn("log drains could not list deployments", "err", err)
			}
			return
		}
		for _, target := range list {
			targets[target.EnvironmentID] = target
		}
	}

	wantedContainers := map[string]Container{}
	if len(output) > 0 || allOutput {
		containers, err := m.containers.DeploymentContainers(ctx)
		if err != nil {
			if m.log != nil {
				m.log.Warn("log drains could not list deployment containers", "err", err)
			}
		}
		for _, container := range containers {
			target, ok := targets[container.EnvironmentID]
			if ok && (allOutput || output[target.ProjectID]) {
				wantedContainers[container.ID] = container
			}
		}
	}
	wantedRoutes := map[int64]Target{}
	if m.requests != nil {
		for id, target := range targets {
			if target.Route != "" && (allRequests || requests[target.ProjectID]) {
				wantedRoutes[id] = target
			}
		}
	}

	m.mu.Lock()
	var stopping []*reader
	for id, follower := range m.followers {
		if _, ok := wantedContainers[id]; !ok {
			stopping = append(stopping, follower)
			delete(m.followers, id)
		}
	}
	for id, tailer := range m.tailers {
		if _, ok := wantedRoutes[id]; !ok {
			stopping = append(stopping, tailer)
			delete(m.tailers, id)
		}
	}
	parent := m.ctx
	if parent == nil {
		parent = ctx
	}
	for id, container := range wantedContainers {
		if _, ok := m.followers[id]; !ok {
			m.followers[id] = m.follow(parent, container, targets[container.EnvironmentID])
		}
	}
	for id, target := range wantedRoutes {
		if _, ok := m.tailers[id]; !ok {
			m.tailers[id] = m.tail(parent, target)
		}
	}
	m.mu.Unlock()
	for _, r := range stopping {
		r.stop()
	}
}

// route hands a record to every drain that asks for it.
func (m *Manager) route(record Record) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, shipper := range m.shippers {
		if shipper.wants(record.Stream, record.ProjectID) {
			shipper.offer(record)
		}
	}
}

// follow reads one container's output from the moment it was created, or
// from when the dashboard started if that is later — a drain carries what
// happens while it exists, not a container's history — and picks up where
// it stopped when Docker ends the stream.
func (m *Manager) follow(parent context.Context, container Container, target Target) *reader {
	ctx, cancel := context.WithCancel(parent)
	r := &reader{cancel: cancel, done: make(chan struct{})}
	since := m.started
	if container.Created.After(since) {
		since = container.Created
	}
	go func() {
		defer close(r.done)
		for ctx.Err() == nil {
			lines, closer, err := m.containers.Follow(ctx, container.ID, since)
			if err == nil {
				for line := range lines {
					if !line.At.After(since) && !line.At.IsZero() {
						continue
					}
					if !line.At.IsZero() {
						since = line.At
					}
					m.route(Record{
						At: line.At, ProjectID: target.ProjectID, Project: target.Project, Environment: target.Environment,
						Stream: StreamOutput, ContainerID: shortID(container.ID), ContainerName: container.Name,
						IOStream: line.Stream, Message: line.Text,
					})
				}
				_ = closer.Close()
			}
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}()
	return r
}

// tail reads an environment's request record from now on.
func (m *Manager) tail(parent context.Context, target Target) *reader {
	ctx, cancel := context.WithCancel(parent)
	r := &reader{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		cursor := accesslog.FromNow
		ticker := time.NewTicker(requestPollEvery)
		defer ticker.Stop()
		for {
			entries, next, err := m.requests.After(ctx, target.Route, cursor, 2000)
			if err == nil {
				cursor = next
				for _, entry := range entries {
					m.route(Record{
						At: entry.At(), ProjectID: target.ProjectID, Project: target.Project, Environment: target.Environment,
						Stream: StreamRequests, Request: requestOf(entry),
					})
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return r
}

func requestOf(entry accesslog.Entry) *Request {
	duration, timed := entry.Duration()
	return &Request{
		Method: entry.Method, Path: entry.Path, Query: entry.Query, Host: entry.Host, Proto: entry.Proto,
		// To the microsecond, which is what the ingress measures; the rest
		// is a float's noise in every format that prints it.
		Status: entry.Status, Size: entry.Size, DurationMs: math.Round(duration*1000) / 1000, Timed: timed,
		RemoteIP: entry.RemoteIP, UserAgent: entry.UserAgent, Referer: entry.Referer,
	}
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func (m *Manager) shutdown() {
	m.mu.Lock()
	readers := make([]*reader, 0, len(m.followers)+len(m.tailers))
	for _, r := range m.followers {
		readers = append(readers, r)
	}
	for _, r := range m.tailers {
		readers = append(readers, r)
	}
	m.followers, m.tailers = map[string]*reader{}, map[int64]*reader{}
	shippers := m.shippers
	m.shippers = map[int64]*shipper{}
	m.mu.Unlock()
	for _, r := range readers {
		r.stop()
	}
	for _, s := range shippers {
		s.close()
	}
}
