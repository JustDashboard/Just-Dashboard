package cftunnel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// The connection is one row (proxy_cloudflare_tunnel): which tunnel, how it
// was connected, and its token sealed with the dashboard's key. The token is
// never logged, never returned by a route, and leaves the store only to be
// handed to the connector's environment.

// Connection is the tunnel the dashboard runs a connector for.
type Connection struct {
	// Mode is "token", a token pasted from Cloudflare's dashboard, or
	// "api", a tunnel the dashboard created with the saved credentials.
	Mode       string    `json:"mode"`
	TunnelID   string    `json:"tunnelId"`
	TunnelName string    `json:"tunnelName,omitempty"`
	AccountID  string    `json:"accountId"`
	CreatedBy  string    `json:"createdBy,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// Hostname is one deployment domain marked to be served through the tunnel,
// as the live release has it.
type Hostname struct {
	Hostname      string `json:"hostname"`
	ProjectID     int64  `json:"projectId"`
	EnvironmentID int64  `json:"environmentId"`
	Project       string `json:"project"`
}

// HostnameStatus is a routed hostname and its record at Cloudflare.
type HostnameStatus struct {
	Hostname
	// Routed says the tunnel's ingress rules hold it.
	Routed bool `json:"routed"`
	// DNS is created, present, updated, error, or manual when the
	// dashboard has no credentials to make it.
	DNS     string     `json:"dns"`
	Error   string     `json:"error,omitempty"`
	Checked *time.Time `json:"checkedAt,omitempty"`
}

// APIStatus says whether the dashboard can configure the tunnel itself.
type APIStatus struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// Status is the tunnel as the Proxy page reads it.
type Status struct {
	Connected  bool           `json:"connected"`
	Connection *Connection    `json:"connection,omitempty"`
	Connector  ConnectorState `json:"connector"`
	// Ready is cloudflared holding at least one connection to the edge.
	Ready            bool       `json:"ready"`
	ReadyConnections int        `json:"readyConnections"`
	CheckedAt        *time.Time `json:"checkedAt,omitempty"`
	// Error is why it is not ready, in cloudflared's own words when it said
	// so, otherwise the dashboard's.
	Error     string           `json:"error,omitempty"`
	API       APIStatus        `json:"api"`
	Origin    *Origin          `json:"origin,omitempty"`
	Hostnames []HostnameStatus `json:"hostnames"`
	// Routes are the ingress rules last set at Cloudflare.
	Routes    []IngressRule `json:"routes,omitempty"`
	SyncedAt  *time.Time    `json:"syncedAt,omitempty"`
	SyncError string        `json:"syncError,omitempty"`
}

// Audit records what the manager did on nobody's request.
type Audit func(ctx context.Context, action string, detail map[string]any, success bool)

// Options are the manager's joins to the rest of the dashboard.
type Options struct {
	DB        *sql.DB
	Sealer    *auth.Sealer
	Connector Connector
	// API is the Cloudflare API with the credentials saved under
	// Certificates, or an error saying there are none.
	API func() (API, error)
	// Origin is where the connector reaches this host's proxy, joining the
	// proxy to the tunnel's network as it does.
	Origin func(ctx context.Context, network string) (Origin, error)
	// Hostnames are the deployment domains marked for the tunnel.
	Hostnames func(ctx context.Context) ([]Hostname, error)
	Audit     Audit
	Log       *slog.Logger
	Now       func() time.Time
	Network   string
}

// Manager keeps the connector, the tunnel's routes and their records in
// agreement with the connection and the deployments routed through it.
type Manager struct {
	opts     Options
	mu       sync.Mutex
	status   Status
	dns      map[string]HostnameStatus
	interval time.Duration
	// pass serializes the passes, a route's and the loop's.
	pass sync.Mutex
	// readyAttempts is how many times, half a second apart, a connector
	// just started is asked whether it reached the edge.
	readyAttempts int
}

func NewManager(opts Options) *Manager {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Network == "" {
		opts.Network = TunnelNetwork
	}
	return &Manager{opts: opts, dns: map[string]HostnameStatus{}, interval: 30 * time.Second,
		status: Status{Hostnames: []HostnameStatus{}}, readyAttempts: 10}
}

// WithInterval sets how often the pass runs.
func (m *Manager) WithInterval(interval time.Duration) *Manager {
	if interval > 0 {
		m.interval = interval
	}
	return m
}

// ErrNotConnected answers for a host with no tunnel connected.
var ErrNotConnected = errors.New("no Cloudflare Tunnel is connected")

func (m *Manager) load(ctx context.Context) (*Connection, string, error) {
	var c Connection
	var sealed string
	var created, updated int64
	err := m.opts.DB.QueryRowContext(ctx, `SELECT mode, tunnel_id, tunnel_name, account_id, token_enc, created_by, created_at, updated_at
		FROM proxy_cloudflare_tunnel WHERE id = 1`).Scan(&c.Mode, &c.TunnelID, &c.TunnelName, &c.AccountID, &sealed, &c.CreatedBy, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	c.CreatedAt, c.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	token, err := m.opts.Sealer.Open(sealed)
	if err != nil {
		return &c, "", errors.New("the tunnel's token could not be unsealed with this dashboard's key; connect it again")
	}
	return &c, token, nil
}

func (m *Manager) save(ctx context.Context, c Connection, token string) error {
	sealed, err := m.opts.Sealer.Seal(strings.TrimSpace(token))
	if err != nil {
		return err
	}
	now := m.opts.Now().UTC().Unix()
	_, err = m.opts.DB.ExecContext(ctx, `INSERT INTO proxy_cloudflare_tunnel(id, mode, tunnel_id, tunnel_name, account_id, token_enc, applied_digest, created_by, created_at, updated_at)
		VALUES(1, ?, ?, ?, ?, ?, '', ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET mode = excluded.mode, tunnel_id = excluded.tunnel_id, tunnel_name = excluded.tunnel_name,
		  account_id = excluded.account_id, token_enc = excluded.token_enc, applied_digest = '', created_by = excluded.created_by,
		  updated_at = excluded.updated_at`,
		c.Mode, c.TunnelID, c.TunnelName, c.AccountID, sealed, c.CreatedBy, now, now)
	return err
}

// Connect keeps a token pasted from Cloudflare's dashboard and starts the
// connector with it. The token is read for its account and tunnel, so the
// tunnel's routes can be set with the saved credentials when there are any.
func (m *Manager) Connect(ctx context.Context, token, actor string) (Status, error) {
	fields, err := ParseToken(token)
	if err != nil {
		return m.Status(ctx), err
	}
	connection := Connection{Mode: "token", TunnelID: fields.TunnelID, AccountID: fields.AccountID, CreatedBy: actor}
	if api, err := m.opts.API(); err == nil {
		if tunnel, err := api.GetTunnel(ctx, fields.AccountID, fields.TunnelID); err == nil {
			connection.TunnelName = tunnel.Name
		}
	}
	if err := m.save(ctx, connection, token); err != nil {
		return m.Status(ctx), err
	}
	m.forgetDNS()
	return m.Sync(ctx), nil
}

// Create makes a tunnel through Cloudflare's API with the saved credentials,
// in the given account or the only one they reach, and connects it.
func (m *Manager) Create(ctx context.Context, name, accountID, actor string) (Status, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 || strings.ContainsAny(name, "\x00\r\n/") {
		return m.Status(ctx), errors.New("a tunnel is named in up to 64 characters")
	}
	api, err := m.opts.API()
	if err != nil {
		return m.Status(ctx), err
	}
	if accountID == "" {
		accounts, err := api.Accounts(ctx)
		if err != nil {
			return m.Status(ctx), err
		}
		switch len(accounts) {
		case 0:
			return m.Status(ctx), errors.New("the saved Cloudflare credentials reach no account; the token needs Account › Cloudflare Tunnel › Edit")
		case 1:
			accountID = accounts[0].ID
		default:
			names := make([]string, 0, len(accounts))
			for _, account := range accounts {
				names = append(names, account.Name+" ("+account.ID+")")
			}
			return m.Status(ctx), fmt.Errorf("the saved credentials reach %d accounts: name one of %s", len(accounts), strings.Join(names, ", "))
		}
	}
	tunnel, err := api.CreateTunnel(ctx, accountID, name)
	if err != nil {
		return m.Status(ctx), err
	}
	token, err := api.TunnelToken(ctx, accountID, tunnel.ID)
	if err != nil {
		return m.Status(ctx), err
	}
	connection := Connection{Mode: "api", TunnelID: tunnel.ID, TunnelName: tunnel.Name, AccountID: accountID, CreatedBy: actor}
	if err := m.save(ctx, connection, token); err != nil {
		return m.Status(ctx), err
	}
	m.forgetDNS()
	return m.Sync(ctx), nil
}

// Disconnect removes the connector and forgets the token. The tunnel stays
// at Cloudflare, with its records, for its owner to remove there: a tunnel
// another server also runs a connector for is not this dashboard's to delete.
func (m *Manager) Disconnect(ctx context.Context) error {
	m.pass.Lock()
	defer m.pass.Unlock()
	if err := m.opts.Connector.Remove(ctx); err != nil {
		return err
	}
	if _, err := m.opts.DB.ExecContext(ctx, `DELETE FROM proxy_cloudflare_tunnel WHERE id = 1`); err != nil {
		return err
	}
	m.forgetDNS()
	m.mu.Lock()
	m.status = Status{Hostnames: []HostnameStatus{}}
	m.mu.Unlock()
	return nil
}

func (m *Manager) forgetDNS() {
	m.mu.Lock()
	m.dns = map[string]HostnameStatus{}
	m.mu.Unlock()
}

// Start runs the pass until the context ends.
func (m *Manager) Start(ctx context.Context) {
	if m == nil || m.opts.DB == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		for {
			pass, cancel := context.WithTimeout(ctx, 2*time.Minute)
			m.Sync(pass)
			cancel()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// Status is the last pass's reading with the connector read again now, so a
// container stopped by hand reads as stopped at once.
func (m *Manager) Status(ctx context.Context) Status {
	m.mu.Lock()
	status := m.status
	status.Hostnames = append([]HostnameStatus(nil), m.status.Hostnames...)
	m.mu.Unlock()
	connection, _, err := m.load(ctx)
	if err != nil || connection == nil {
		return Status{Hostnames: []HostnameStatus{}, API: m.apiStatus(nil)}
	}
	status.Connected, status.Connection = true, connection
	if state, err := m.opts.Connector.Inspect(ctx); err == nil {
		status.Connector = state
		if state.State != "running" {
			status.Ready, status.ReadyConnections = false, 0
		}
	}
	return status
}

func (m *Manager) apiStatus(connection *Connection) APIStatus {
	if _, err := m.opts.API(); err != nil {
		return APIStatus{Reason: err.Error()}
	}
	if connection != nil && connection.AccountID == "" {
		return APIStatus{Reason: "the tunnel's account is not known"}
	}
	return APIStatus{Available: true}
}

// Sync is one pass: the connector made again if its token or origin
// changed and started if it stopped, its readiness read, and — with the
// saved credentials — the tunnel's routes set to the hostnames marked for it
// and each hostname's record made.
func (m *Manager) Sync(ctx context.Context) Status { return m.sync(ctx, nil) }

// Route is a pass that also routes extra — a release's names as it goes live,
// before they are among the live ones — and answers what stood in the way.
func (m *Manager) Route(ctx context.Context, extra []string) error {
	status := m.sync(ctx, extra)
	if !status.Connected {
		return ErrNotConnected
	}
	if status.SyncError != "" {
		return errors.New(status.SyncError)
	}
	var problems []string
	for _, entry := range status.Hostnames {
		name := entry.Hostname.Hostname
		if slices.Contains(extra, name) && (!entry.Routed || entry.DNS == "error") {
			reason := entry.Error
			if reason == "" {
				reason = "it is not among the tunnel's routes"
			}
			problems = append(problems, name+": "+reason)
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (m *Manager) sync(ctx context.Context, extra []string) Status {
	m.pass.Lock()
	defer m.pass.Unlock()
	now := m.opts.Now().UTC()
	status := Status{Hostnames: []HostnameStatus{}}
	connection, token, err := m.load(ctx)
	if err != nil {
		status.SyncError = err.Error()
		return m.publish(status)
	}
	status.API = m.apiStatus(connection)
	if connection == nil {
		return m.publish(status)
	}
	status.Connected, status.Connection = true, connection
	origin, originErr := m.opts.Origin(ctx, m.opts.Network)
	if originErr == nil {
		status.Origin = &origin
	}
	state, err := m.opts.Connector.Inspect(ctx)
	if err != nil {
		status.Error = err.Error()
		return m.publish(status)
	}
	started := false
	if token != "" && !state.Foreign && (state.State == "missing" || state.TokenDigest != TokenDigest(token) ||
		(originErr == nil && state.Origin != origin.Service) || state.State == "exited" || state.State == "created" || state.State == "dead") {
		before := state.State
		if err := m.opts.Connector.Run(ctx, token, origin); err != nil {
			status.Error = err.Error()
			m.audit(ctx, "proxy.tunnel.connector", map[string]any{"tunnelId": connection.TunnelID, "from": before, "error": err.Error()}, false)
			return m.publish(status)
		}
		m.audit(ctx, "proxy.tunnel.connector", map[string]any{"tunnelId": connection.TunnelID, "from": before}, true)
		started = true
		if state, err = m.opts.Connector.Inspect(ctx); err != nil {
			status.Error = err.Error()
			return m.publish(status)
		}
	}
	if state.Foreign {
		status.Error = "a container named " + ConnectorName + " that this dashboard did not make is in the way"
	}
	status.Connector = state
	// A connector that has just started takes a moment to reach the edge;
	// one that was already running is read once.
	attempts := 1
	if started {
		attempts = m.readyAttempts
	}
	for attempt := 0; attempt < attempts && state.State == "running"; attempt++ {
		ready, err := m.opts.Connector.Ready(ctx, state)
		if err == nil {
			status.ReadyConnections = ready.ReadyConnections
			status.Ready = ready.ReadyConnections > 0
		}
		if status.Ready || ctx.Err() != nil || attempt == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}
	}
	checked := now
	status.CheckedAt = &checked
	if !status.Ready && status.Error == "" {
		if complaint := m.opts.Connector.LastError(ctx); complaint != "" {
			status.Error = complaint
		} else if state.State != "running" {
			status.Error = "The connector is " + state.State + "."
		} else {
			status.Error = "The connector holds no connection to Cloudflare's edge yet."
		}
	}
	m.route(ctx, &status, connection, origin, originErr, extra)
	synced := m.opts.Now().UTC()
	status.SyncedAt = &synced
	return m.publish(status)
}

// route sets the tunnel's ingress rules and each hostname's record.
func (m *Manager) route(ctx context.Context, status *Status, connection *Connection, origin Origin, originErr error, extra []string) {
	hostnames, err := m.opts.Hostnames(ctx)
	if err != nil {
		status.SyncError = "the deployments routed through the tunnel could not be read: " + err.Error()
		return
	}
	for _, name := range extra {
		known := false
		for _, h := range hostnames {
			known = known || h.Hostname == name
		}
		if !known {
			hostnames = append(hostnames, Hostname{Hostname: name})
		}
	}
	names := make([]string, 0, len(hostnames))
	for _, h := range hostnames {
		names = append(names, h.Hostname)
	}
	api, apiErr := m.opts.API()
	if originErr != nil {
		status.SyncError = originErr.Error()
	}
	var applied Configuration
	if originErr == nil {
		configuration, err := Render(names, origin)
		if err != nil {
			status.SyncError = err.Error()
		} else if apiErr == nil {
			var digest string
			_ = m.opts.DB.QueryRowContext(ctx, `SELECT applied_digest FROM proxy_cloudflare_tunnel WHERE id = 1`).Scan(&digest)
			if digest != configuration.Digest() {
				if err := api.PutConfiguration(ctx, connection.AccountID, connection.TunnelID, configuration); err != nil {
					status.SyncError = err.Error()
					m.audit(ctx, "proxy.tunnel.routes", map[string]any{"tunnelId": connection.TunnelID, "hostnames": configuration.Hostnames(), "error": err.Error()}, false)
				} else {
					_, _ = m.opts.DB.ExecContext(ctx, `UPDATE proxy_cloudflare_tunnel SET applied_digest = ? WHERE id = 1`, configuration.Digest())
					m.audit(ctx, "proxy.tunnel.routes", map[string]any{"tunnelId": connection.TunnelID, "hostnames": configuration.Hostnames()}, true)
					applied = configuration
				}
			} else {
				applied = configuration
			}
		}
		if applied.Ingress == nil && apiErr != nil {
			// Shown, not applied: what to add in Cloudflare's dashboard.
			status.Routes = configuration.Ingress
		}
	}
	if applied.Ingress != nil {
		status.Routes = applied.Ingress
	}
	routed := applied.Hostnames()
	m.mu.Lock()
	known := m.dns
	m.mu.Unlock()
	next := map[string]HostnameStatus{}
	for _, h := range hostnames {
		entry := HostnameStatus{Hostname: h, Routed: slices.Contains(routed, h.Hostname)}
		previous, seen := known[h.Hostname]
		switch {
		case apiErr != nil:
			entry.DNS, entry.Error = "manual", "Without saved Cloudflare credentials the record is yours to create: a proxied CNAME to "+TunnelTarget(connection.TunnelID)+"."
		case seen && previous.DNS != "error" && previous.Checked != nil && m.opts.Now().Sub(*previous.Checked) < 30*time.Minute:
			entry.DNS, entry.Error, entry.Checked = previous.DNS, previous.Error, previous.Checked
		default:
			checked := m.opts.Now().UTC()
			entry.Checked = &checked
			zone, err := api.ZoneFor(ctx, h.Hostname)
			if err == nil {
				var outcome DNSOutcome
				outcome, err = api.EnsureCNAME(ctx, zone.ID, h.Hostname, connection.TunnelID)
				entry.DNS = string(outcome)
				if err == nil && outcome != DNSPresent {
					m.audit(ctx, "proxy.tunnel.dns", map[string]any{"hostname": h.Hostname, "outcome": string(outcome), "target": TunnelTarget(connection.TunnelID)}, true)
				}
			}
			if err != nil {
				entry.DNS, entry.Error = "error", err.Error()
				if !seen || previous.Error != err.Error() {
					m.audit(ctx, "proxy.tunnel.dns", map[string]any{"hostname": h.Hostname, "error": err.Error()}, false)
				}
			}
		}
		next[h.Hostname] = entry
		status.Hostnames = append(status.Hostnames, entry)
	}
	m.mu.Lock()
	m.dns = next
	m.mu.Unlock()
}

func (m *Manager) publish(status Status) Status {
	if status.Hostnames == nil {
		status.Hostnames = []HostnameStatus{}
	}
	m.mu.Lock()
	m.status = status
	m.mu.Unlock()
	return status
}

func (m *Manager) audit(ctx context.Context, action string, detail map[string]any, success bool) {
	if m.opts.Audit != nil {
		m.opts.Audit(ctx, action, detail, success)
	}
}

// Routes says whether a hostname is routed through the tunnel, for the
// deployment pages: which tunnel, and whether a connector is running for it.
func (m *Manager) Connected(ctx context.Context) (*Connection, bool) {
	connection, _, err := m.load(ctx)
	if err != nil || connection == nil {
		return nil, false
	}
	m.mu.Lock()
	ready := m.status.Ready
	m.mu.Unlock()
	return connection, ready
}
