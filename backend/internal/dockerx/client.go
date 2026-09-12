// Package dockerx talks to the Docker Engine API over its unix socket through
// the official Go SDK. Nothing here shells out to the docker CLI: the socket
// is the interface, and shelling out would both lose typed errors and widen
// the command-injection surface.
//
// Access to /var/run/docker.sock is root-equivalent — a caller who can create
// a container can bind-mount the host root and escape. Every route that
// reaches this package therefore sits behind authentication, a capability
// check and the audit trail, and the destructive ones behind a typed
// confirmation as well.
package dockerx

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	dtypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/system"
	"github.com/docker/docker/client"
)

var ErrUnavailable = errors.New("docker is not available on this host")

type Client struct {
	mu   sync.RWMutex
	cli  *client.Client
	host string
	err  error

	// Disk usage is the one Docker query that walks every layer and volume on
	// disk; on a modest server it takes seconds. It is cached because the
	// volume list needs it only to answer "how big, and is anything using
	// it" — figures that do not move between two page refreshes.
	duMu   sync.Mutex
	duVal  *dtypes.DiskUsage
	duAt   time.Time
	duBusy bool

	// How much memory and how many CPUs this host has, cached forever.
	//
	// Needed to read a container's own numbers honestly. Docker reports a
	// container with no memory limit as having a limit equal to the machine's
	// RAM, which a dashboard then shows as "97 MB / 62.7 GB" — a budget that
	// does not exist, next to a percentage of it that means nothing. The only
	// way to tell "limited to all of it" from "not limited" is to know what
	// all of it is. Neither number changes while the process runs.
	hostMu     sync.Mutex
	hostMemory int64
	hostCPUs   int
}

// HostCapacity is the memory and CPU this server has, as Docker sees it.
//
// Read once and kept: it is what makes "no limit" and "0.4% of this server"
// sayable instead of showing host RAM as though a container had asked for it.
func (c *Client) HostCapacity(ctx context.Context) (memory int64, cpus int) {
	c.hostMu.Lock()
	memory, cpus = c.hostMemory, c.hostCPUs
	c.hostMu.Unlock()
	if memory > 0 {
		return memory, cpus
	}
	info, err := c.Info(ctx)
	if err != nil {
		return 0, 0
	}
	c.hostMu.Lock()
	c.hostMemory, c.hostCPUs = info.MemTotal, info.NCPU
	memory, cpus = c.hostMemory, c.hostCPUs
	c.hostMu.Unlock()
	return memory, cpus
}

// diskUsageTTL is how long a cached disk-usage reading stays authoritative.
const diskUsageTTL = 60 * time.Second

// diskUsage returns Docker's disk accounting, refreshing it at most once per
// TTL. A stale reading is served immediately while a refresh runs in the
// background, so only the very first caller after startup waits for the walk.
func (c *Client) diskUsage(ctx context.Context) *dtypes.DiskUsage {
	cli, err := c.api()
	if err != nil {
		return nil
	}

	c.duMu.Lock()
	val, at, busy := c.duVal, c.duAt, c.duBusy
	fresh := val != nil && time.Since(at) < diskUsageTTL
	if fresh {
		c.duMu.Unlock()
		return val
	}
	if val != nil {
		// Stale but usable: hand it back now and refresh out of band.
		if !busy {
			c.duBusy = true
			go func() {
				bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
				defer cancel()
				du, err := cli.DiskUsage(bg, dtypes.DiskUsageOptions{})
				c.duMu.Lock()
				if err == nil {
					c.duVal, c.duAt = &du, time.Now()
				}
				c.duBusy = false
				c.duMu.Unlock()
			}()
		}
		c.duMu.Unlock()
		return val
	}
	c.duMu.Unlock()

	// Nothing cached at all, so this caller has to wait for the real thing.
	du, err := cli.DiskUsage(ctx, dtypes.DiskUsageOptions{})
	if err != nil {
		return nil
	}
	c.duMu.Lock()
	c.duVal, c.duAt = &du, time.Now()
	c.duMu.Unlock()
	return &du
}

// forgetDiskUsage drops the cached reading so the next caller pays for a fresh
// walk of the layer store.
//
// Every prune calls this, and the reason is the whole complaint it fixes: the
// cache serves a stale value *immediately* and refreshes behind the request,
// so without it the page that just reclaimed forty gigabytes redraws with the
// figure it had before — which is indistinguishable from a button that did
// nothing, and was exactly how a working prune came to look broken.
func (c *Client) forgetDiskUsage() {
	c.duMu.Lock()
	c.duVal, c.duAt = nil, time.Time{}
	c.duMu.Unlock()
}

// WarmCaches primes the expensive queries in the background so the first
// operator to open a page does not pay for them.
func (c *Client) WarmCaches() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		c.diskUsage(ctx)
	}()
}

// New never fails hard: a host without Docker should still serve the rest of
// the dashboard. The connection error is retained and surfaced per request.
func New(host string) *Client {
	c := &Client{host: host}
	cli, err := client.NewClientWithOpts(
		client.WithHost(host),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		c.err = err
		return c
	}
	c.cli = cli
	return c
}

func (c *Client) api() (*client.Client, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.cli == nil {
		if c.err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUnavailable, c.err)
		}
		return nil, ErrUnavailable
	}
	return c.cli, nil
}

// Close tolerates a nil client so a host with no Docker can shut the dashboard
// down. Every other module is optional at shutdown; this one was not, and a
// degraded install panicked on the way out.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cli != nil {
		return c.cli.Close()
	}
	return nil
}

type Availability struct {
	Available     bool   `json:"available"`
	Host          string `json:"host"`
	Error         string `json:"error,omitempty"`
	ServerVersion string `json:"serverVersion,omitempty"`
	APIVersion    string `json:"apiVersion,omitempty"`
}

// Ping is what the UI calls before rendering the Docker section, so an
// unreachable daemon reads as "not configured" rather than a wall of errors.
func (c *Client) Ping(ctx context.Context) Availability {
	a := Availability{Host: c.host}
	cli, err := c.api()
	if err != nil {
		a.Error = err.Error()
		return a
	}
	p, err := cli.Ping(ctx)
	if err != nil {
		a.Error = err.Error()
		return a
	}
	a.Available = true
	a.APIVersion = p.APIVersion
	if info, err := cli.ServerVersion(ctx); err == nil {
		a.ServerVersion = info.Version
	}
	return a
}

func (c *Client) Info(ctx context.Context) (system.Info, error) {
	cli, err := c.api()
	if err != nil {
		return system.Info{}, err
	}
	return cli.Info(ctx)
}
