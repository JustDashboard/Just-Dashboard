package netx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrForgotten is a VPN client whose configuration was forgotten: the peer is
// still on the server, but the key that would let anyone show its QR code
// again is gone.
var ErrForgotten = errors.New("this configuration was forgotten")

// VPNStore keeps each VPN client's configuration sealed, so an administrator
// can show its QR code again.
//
// The row is the half of a peer the server never needs: its own private key
// lives only in the client's configuration, which is what is sealed here.
// Forgetting empties the sealed text and keeps the row, so the peer stays
// named and countable while the key it would reveal is unrecoverable; that
// is the point of the button, since a copy of a private key that "might be
// needed" is a copy that can be stolen.
type VPNStore struct {
	db   *sql.DB
	seal func(string) (string, error)
	open func(string) (string, error)
}

func newVPNStore(db *sql.DB, seal func(string) (string, error), open func(string) (string, error)) *VPNStore {
	return &VPNStore{db: db, seal: seal, open: open}
}

// VPNClient is one stored client. Config is filled only by Get.
type VPNClient struct {
	ID        int64
	Iface     string
	PublicKey string
	Name      string
	Kind      string
	Address   string
	// HasConfig is a sealed configuration that has not been forgotten.
	HasConfig bool
	Config    string
	// ClientRoutes is a device's own AllowedIPs, comma separated, as last
	// generated; empty when unknown.
	ClientRoutes string
	CreatedBy    string
	CreatedAt    time.Time
}

// Save stores a client with its configuration sealed and returns its id,
// which is what the peer's `# jd:id` comment carries.
func (v *VPNStore) Save(ctx context.Context, c VPNClient, config string) (int64, error) {
	sealed, err := v.seal(config)
	if err != nil {
		return 0, fmt.Errorf("sealing the client configuration: %w", err)
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	res, err := v.db.ExecContext(ctx,
		`INSERT INTO network_vpn_clients (iface, public_key, name, kind, address, config_sealed, created_by, created_at, client_routes)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Iface, c.PublicKey, c.Name, c.Kind, c.Address, sealed, c.CreatedBy, c.CreatedAt.Unix(), c.ClientRoutes)
	if err != nil {
		return 0, fmt.Errorf("storing the client: %w", err)
	}
	return res.LastInsertId()
}

// Get reads one client with its configuration opened. A forgotten
// configuration is returned as the client with ErrForgotten, so the caller can
// still name the peer in its answer.
func (v *VPNStore) Get(ctx context.Context, iface string, id int64) (VPNClient, error) {
	var (
		c       VPNClient
		sealed  string
		created int64
	)
	err := v.db.QueryRowContext(ctx,
		`SELECT id, iface, public_key, name, kind, address, config_sealed, created_by, created_at, client_routes
		   FROM network_vpn_clients WHERE iface = ? AND id = ?`, iface, id).
		Scan(&c.ID, &c.Iface, &c.PublicKey, &c.Name, &c.Kind, &c.Address, &sealed, &c.CreatedBy, &created, &c.ClientRoutes)
	if errors.Is(err, sql.ErrNoRows) {
		return VPNClient{}, fmt.Errorf("client %d of %s: %w", id, iface, ErrNotFound)
	}
	if err != nil {
		return VPNClient{}, fmt.Errorf("reading the client: %w", err)
	}
	c.CreatedAt = time.Unix(created, 0)
	if sealed == "" {
		return c, ErrForgotten
	}
	c.HasConfig = true
	if c.Config, err = v.open(sealed); err != nil {
		return c, fmt.Errorf("opening the client configuration: %w", err)
	}
	return c, nil
}

// Forget empties a client's sealed configuration.
func (v *VPNStore) Forget(ctx context.Context, iface string, id int64) error {
	res, err := v.db.ExecContext(ctx,
		`UPDATE network_vpn_clients SET config_sealed = '' WHERE iface = ? AND id = ?`, iface, id)
	return vpnAffectedOne(res, err, iface, id)
}

// Update replaces a client's name and, when config is not empty, its sealed
// configuration and recorded routes. A forgotten configuration stays
// forgotten: an edit never brings a private key back.
func (v *VPNStore) Update(ctx context.Context, iface string, id int64, name, config, routes string) error {
	if config == "" {
		res, err := v.db.ExecContext(ctx,
			`UPDATE network_vpn_clients SET name = ? WHERE iface = ? AND id = ?`, name, iface, id)
		return vpnAffectedOne(res, err, iface, id)
	}
	sealed, err := v.seal(config)
	if err != nil {
		return fmt.Errorf("sealing the client configuration: %w", err)
	}
	res, err := v.db.ExecContext(ctx,
		`UPDATE network_vpn_clients SET name = ?, config_sealed = ?, client_routes = ? WHERE iface = ? AND id = ? AND config_sealed <> ''`,
		name, sealed, routes, iface, id)
	return vpnAffectedOne(res, err, iface, id)
}

// Delete removes a client's row.
func (v *VPNStore) Delete(ctx context.Context, iface string, id int64) error {
	res, err := v.db.ExecContext(ctx, `DELETE FROM network_vpn_clients WHERE iface = ? AND id = ?`, iface, id)
	return vpnAffectedOne(res, err, iface, id)
}

func vpnAffectedOne(res sql.Result, err error, iface string, id int64) error {
	if err != nil {
		return fmt.Errorf("updating the client: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("client %d of %s: %w", id, iface, ErrNotFound)
	}
	return nil
}

// DeleteInterface removes every client of a tunnel that was removed.
func (v *VPNStore) DeleteInterface(ctx context.Context, iface string) error {
	if _, err := v.db.ExecContext(ctx, `DELETE FROM network_vpn_clients WHERE iface = ?`, iface); err != nil {
		return fmt.Errorf("removing the tunnel's clients: %w", err)
	}
	return nil
}

// List is a tunnel's clients, oldest first, without their configurations.
func (v *VPNStore) List(ctx context.Context, iface string) ([]VPNClient, error) {
	rows, err := v.db.QueryContext(ctx,
		`SELECT id, iface, public_key, name, kind, address, config_sealed <> '', created_by, created_at, client_routes
		   FROM network_vpn_clients WHERE iface = ? ORDER BY id`, iface)
	if err != nil {
		return nil, fmt.Errorf("listing the clients: %w", err)
	}
	defer rows.Close()
	var out []VPNClient
	for rows.Next() {
		var (
			c       VPNClient
			created int64
		)
		if err := rows.Scan(&c.ID, &c.Iface, &c.PublicKey, &c.Name, &c.Kind, &c.Address, &c.HasConfig, &c.CreatedBy, &created, &c.ClientRoutes); err != nil {
			return nil, fmt.Errorf("reading a client: %w", err)
		}
		c.CreatedAt = time.Unix(created, 0)
		out = append(out, c)
	}
	return out, rows.Err()
}

// byPublicKey indexes a tunnel's clients for the page's join with the file.
func (v *VPNStore) byPublicKey(ctx context.Context, iface string) (map[string]VPNClient, error) {
	list, err := v.List(ctx, iface)
	if err != nil {
		return nil, err
	}
	out := make(map[string]VPNClient, len(list))
	for _, c := range list {
		out[c.PublicKey] = c
	}
	return out, nil
}
