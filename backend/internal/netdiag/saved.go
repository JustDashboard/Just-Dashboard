package netdiag

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
)

// Operator-saved references the diagnostics compare against: trusted SSH
// host keys and Wake-on-LAN devices. Both live in the existing settings table
// as bounded JSON, so no schema change is needed.

const (
	sshTrustKey    = "network.diagnostics.ssh_trust"
	wakeDevicesKey = "network.diagnostics.wol_devices"
	MaxSSHTrust    = 128
	MaxWakeDevices = 64
)

var (
	ErrTooMany     = errors.New("the saved list is full")
	ErrTrustExists = errors.New("fingerprints are already saved for this host; replacing them must be confirmed")
)

func (s *Store) readJSON(ctx context.Context, key string, into any) error {
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(data), into)
}

func (s *Store) writeJSON(ctx context.Context, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, string(data))
	return err
}

func (s *Service) SSHTrust(ctx context.Context) ([]netsec.SSHTrust, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return nil, err
	}
	return s.sshTrust(ctx)
}

func (s *Service) sshTrust(ctx context.Context) ([]netsec.SSHTrust, error) {
	entries := []netsec.SSHTrust{}
	if err := s.store.readJSON(ctx, sshTrustKey, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// SSHTrustFor returns the saved entry for a canonical host:port, or nil.
func (s *Service) SSHTrustFor(ctx context.Context, target string) (*netsec.SSHTrust, error) {
	entries, err := s.SSHTrust(ctx)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if entries[i].Target == target {
			return &entries[i], nil
		}
	}
	return nil, nil
}

// SaveSSHTrust saves the entry for its target. Overwriting existing trust
// erases the fingerprint later scans rely on, so it needs replace.
func (s *Service) SaveSSHTrust(ctx context.Context, entry netsec.SSHTrust, replace bool) (netsec.SSHTrust, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return netsec.SSHTrust{}, err
	}
	entries, err := s.sshTrust(ctx)
	if err != nil {
		return netsec.SSHTrust{}, err
	}
	entry.SavedAt = s.now()
	replaced := false
	for i := range entries {
		if entries[i].Target == entry.Target {
			if !replace {
				return netsec.SSHTrust{}, ErrTrustExists
			}
			entries[i], replaced = entry, true
		}
	}
	if !replaced {
		if len(entries) >= MaxSSHTrust {
			return netsec.SSHTrust{}, fmt.Errorf("%w: at most %d hosts can have saved fingerprints", ErrTooMany, MaxSSHTrust)
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Target < entries[j].Target })
	return entry, s.store.writeJSON(ctx, sshTrustKey, entries)
}

func (s *Service) ForgetSSHTrust(ctx context.Context, target string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return err
	}
	entries, err := s.sshTrust(ctx)
	if err != nil {
		return err
	}
	kept := entries[:0]
	for _, e := range entries {
		if e.Target != target {
			kept = append(kept, e)
		}
	}
	if len(kept) == len(entries) {
		return ErrNotFound
	}
	return s.store.writeJSON(ctx, sshTrustKey, kept)
}

// WakeDevice is a saved Wake-on-LAN target with its optional verification.
type WakeDevice struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	MAC       string    `json:"mac"`
	Interface string    `json:"interface"`
	Verify    string    `json:"verify,omitempty"`
	Port      int       `json:"port,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	CreatedBy string    `json:"createdBy"`
}

func ValidateWakeDevice(d WakeDevice) (WakeDevice, error) {
	var err error
	if d.Name, err = validName(d.Name); err != nil {
		return d, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if d.MAC, err = netsec.ValidWakeMAC(d.MAC); err != nil {
		return d, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	d.Interface = strings.TrimSpace(d.Interface)
	if err := netsec.ValidInterfaceName(d.Interface); err != nil {
		return d, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	d.Verify = strings.TrimSpace(d.Verify)
	switch {
	case d.Verify == "" && d.Port != 0:
		return d, fmt.Errorf("%w: a verification port needs a verification address", ErrInvalid)
	case d.Verify != "":
		if d.Verify, err = netsec.ValidWakeVerification(d.Verify, d.Port); err != nil {
			return d, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
	}
	return d, nil
}

func (s *Service) WakeDevices(ctx context.Context) ([]WakeDevice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return nil, err
	}
	return s.wakeDevices(ctx)
}

func (s *Service) wakeDevices(ctx context.Context) ([]WakeDevice, error) {
	devices := []WakeDevice{}
	if err := s.store.readJSON(ctx, wakeDevicesKey, &devices); err != nil {
		return nil, err
	}
	return devices, nil
}

// SaveWakeDevice creates a device (empty ID) or replaces one by ID.
func (s *Service) SaveWakeDevice(ctx context.Context, d WakeDevice, actor string) (WakeDevice, error) {
	d, err := ValidateWakeDevice(d)
	if err != nil {
		return d, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return d, err
	}
	devices, err := s.wakeDevices(ctx)
	if err != nil {
		return d, err
	}
	now := s.now()
	d.UpdatedAt = now
	if d.ID == "" {
		if len(devices) >= MaxWakeDevices {
			return d, fmt.Errorf("%w: at most %d devices can be saved", ErrTooMany, MaxWakeDevices)
		}
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			return d, err
		}
		d.ID, d.CreatedAt, d.CreatedBy = hex.EncodeToString(random[:]), now, actor
		devices = append(devices, d)
	} else {
		found := false
		for i := range devices {
			if devices[i].ID == d.ID {
				d.CreatedAt, d.CreatedBy = devices[i].CreatedAt, devices[i].CreatedBy
				devices[i], found = d, true
			}
		}
		if !found {
			return d, ErrNotFound
		}
	}
	sort.SliceStable(devices, func(i, j int) bool { return strings.ToLower(devices[i].Name) < strings.ToLower(devices[j].Name) })
	return d, s.store.writeJSON(ctx, wakeDevicesKey, devices)
}

func (s *Service) DeleteWakeDevice(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return err
	}
	devices, err := s.wakeDevices(ctx)
	if err != nil {
		return err
	}
	kept := devices[:0]
	for _, d := range devices {
		if d.ID != id {
			kept = append(kept, d)
		}
	}
	if len(kept) == len(devices) {
		return ErrNotFound
	}
	return s.store.writeJSON(ctx, wakeDevicesKey, kept)
}
