package netx

import "database/sql"

// VPNStore keeps each VPN client's configuration sealed, so an administrator
// can show its QR code again.
type VPNStore struct {
	db   *sql.DB
	seal func(string) (string, error)
	open func(string) (string, error)
}

func newVPNStore(db *sql.DB, seal func(string) (string, error), open func(string) (string, error)) *VPNStore {
	return &VPNStore{db: db, seal: seal, open: open}
}
