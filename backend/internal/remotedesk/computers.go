package remotedesk

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrNotFound = errors.New("remote computer not found")
	ErrInvalid  = errors.New("invalid remote computer")
)

// Computer is a desktop the dashboard can open a session to. Its password is
// sealed at rest and never leaves the backend; HasPassword is all a reader
// learns about it.
type Computer struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	OS          string `json:"os"`
	Host        string `json:"host"`
	Protocol    string `json:"protocol"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	Domain      string `json:"domain"`
	HasPassword bool   `json:"hasPassword"`
	Security    string `json:"security"`
	Layout      string `json:"layout"`
	PeerID      string `json:"peerId"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
	LastSession int64  `json:"lastSessionAt"`
	// StreamHostID is the stream bridge's id for this computer once it has
	// been paired with Sunshine on it.
	StreamHostID int `json:"streamHostId"`
	// Gfx asks guacd for RDP's Graphics Pipeline. It is off unless asked
	// for: guacd 1.6 draws a GFX session on a timer rather than on the
	// server's own frame marks, and sends two to three times the data for
	// it; a few servers insist on the pipeline, and they turn this on.
	Gfx bool `json:"gfx"`
}

// ComputerInput is what an administrator saves. A nil Password on an edit
// keeps the sealed one; an empty string clears it.
type ComputerInput struct {
	Name     string  `json:"name"`
	OS       string  `json:"os"`
	Host     string  `json:"host"`
	Protocol string  `json:"protocol"`
	Port     int     `json:"port"`
	Username string  `json:"username"`
	Domain   string  `json:"domain"`
	Password *string `json:"password"`
	Security string  `json:"security"`
	Layout   string  `json:"layout"`
	PeerID   string  `json:"peerId"`
	Gfx      bool    `json:"gfx"`
}

const (
	OSWindows = "windows"
	OSMac     = "macos"
	OSLinux   = "linux"

	ProtocolRDP = "rdp"
	ProtocolVNC = "vnc"
)

// DefaultPort is where each protocol's own server listens unless moved.
func DefaultPort(protocol string) int {
	if protocol == ProtocolVNC {
		return 5900
	}
	return 3389
}

var securityModes = map[string]bool{"": true, "any": true, "nla": true, "tls": true, "rdp": true}

// Layouts are the keyboard maps guacd 1.6 compiles into its RDP client; it
// refuses a connection that names any other.
var Layouts = []string{
	"cs-cz-qwertz", "da-dk-qwerty", "de-ch-qwertz", "de-de-qwertz", "en-gb-qwerty", "en-us-qwerty",
	"es-es-qwerty", "es-latam-qwerty", "failsafe", "fr-be-azerty", "fr-ca-qwerty", "fr-ch-qwertz",
	"fr-fr-azerty", "hu-hu-qwertz", "it-it-qwerty", "ja-jp-qwerty", "no-no-qwerty", "pl-pl-qwerty",
	"pt-br-qwerty", "pt-pt-qwerty", "ro-ro-qwerty", "sv-se-qwerty", "tr-tr-qwerty",
}

var hostname = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,62})(\.[A-Za-z0-9]([A-Za-z0-9-]{0,62}))*\.?$`)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func (in *ComputerInput) normalize() error {
	in.Name = strings.TrimSpace(in.Name)
	in.Host = strings.TrimSpace(in.Host)
	in.Username = strings.TrimSpace(in.Username)
	in.Domain = strings.TrimSpace(in.Domain)
	in.OS = strings.ToLower(strings.TrimSpace(in.OS))
	in.Protocol = strings.ToLower(strings.TrimSpace(in.Protocol))
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 64 {
		return invalid("name must be 1 to 64 characters")
	}
	switch in.OS {
	case OSWindows, OSMac, OSLinux:
	default:
		return invalid("os must be windows, macos or linux")
	}
	switch in.Protocol {
	case ProtocolRDP, ProtocolVNC:
	case "":
		in.Protocol = ProtocolRDP
		if in.OS == OSMac {
			in.Protocol = ProtocolVNC
		}
	default:
		return invalid("protocol must be rdp or vnc")
	}
	if !validHost(in.Host) {
		return invalid("host must be an IP address or a host name")
	}
	if in.Port == 0 {
		in.Port = DefaultPort(in.Protocol)
	}
	if in.Port < 1 || in.Port > 65535 {
		return invalid("port must be between 1 and 65535")
	}
	if len(in.Username) > 256 || len(in.Domain) > 256 {
		return invalid("username and domain must be at most 256 characters")
	}
	if in.Password != nil && len(*in.Password) > 1024 {
		return invalid("password must be at most 1024 characters")
	}
	if in.Protocol != ProtocolRDP {
		in.Security, in.Layout, in.Domain, in.Gfx = "", "", "", false
	}
	if !securityModes[in.Security] {
		return invalid("security must be any, nla, tls or rdp")
	}
	if in.Layout != "" && !knownLayout(in.Layout) {
		return invalid("unknown keyboard layout %q", in.Layout)
	}
	if len(in.PeerID) > 128 {
		return invalid("peer id is too long")
	}
	return nil
}

func validHost(host string) bool {
	return host != "" && len(host) <= 253 && (net.ParseIP(host) != nil || hostname.MatchString(host))
}

func knownLayout(layout string) bool {
	for _, known := range Layouts {
		if known == layout {
			return true
		}
	}
	return false
}

type store struct {
	db   *sql.DB
	seal func(string) (string, error)
	open func(string) (string, error)
	now  func() time.Time
}

const computerColumns = `id, name, os, host, protocol, port, username, domain, password_enc <> '', security, layout, peer_id, created_at, updated_at, last_session_at, stream_host_id, gfx`

type scanner interface{ Scan(...any) error }

func scanComputer(row scanner) (Computer, error) {
	var c Computer
	err := row.Scan(&c.ID, &c.Name, &c.OS, &c.Host, &c.Protocol, &c.Port, &c.Username, &c.Domain,
		&c.HasPassword, &c.Security, &c.Layout, &c.PeerID, &c.CreatedAt, &c.UpdatedAt, &c.LastSession, &c.StreamHostID, &c.Gfx)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

func (s *store) list(ctx context.Context) ([]Computer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+computerColumns+` FROM remote_desktop_computers ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Computer{}
	for rows.Next() {
		c, err := scanComputer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *store) get(ctx context.Context, id string) (Computer, error) {
	return scanComputer(s.db.QueryRowContext(ctx, `SELECT `+computerColumns+` FROM remote_desktop_computers WHERE id = ?`, id))
}

func (s *store) create(ctx context.Context, in ComputerInput) (Computer, error) {
	if err := in.normalize(); err != nil {
		return Computer{}, err
	}
	id, err := newID()
	if err != nil {
		return Computer{}, err
	}
	sealed := ""
	if in.Password != nil && *in.Password != "" {
		if sealed, err = s.seal(*in.Password); err != nil {
			return Computer{}, err
		}
	}
	now := s.now().Unix()
	_, err = s.db.ExecContext(ctx, `INSERT INTO remote_desktop_computers
		(id, name, os, host, protocol, port, username, domain, password_enc, security, layout, peer_id, gfx, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, in.Name, in.OS, in.Host, in.Protocol, in.Port, in.Username, in.Domain, sealed, in.Security, in.Layout, in.PeerID, in.Gfx, now, now)
	if err != nil {
		return Computer{}, err
	}
	return s.get(ctx, id)
}

func (s *store) update(ctx context.Context, id string, in ComputerInput) (Computer, error) {
	if err := in.normalize(); err != nil {
		return Computer{}, err
	}
	now := s.now().Unix()
	// The bridge's pairing is with the computer at its old address; a
	// computer moved to another one is paired again.
	res, err := s.db.ExecContext(ctx, `UPDATE remote_desktop_computers SET
		name = ?, os = ?, host = ?, protocol = ?, port = ?, username = ?, domain = ?, security = ?, layout = ?, peer_id = ?, gfx = ?, updated_at = ?,
		stream_host_id = CASE WHEN host = ? THEN stream_host_id ELSE 0 END
		WHERE id = ?`,
		in.Name, in.OS, in.Host, in.Protocol, in.Port, in.Username, in.Domain, in.Security, in.Layout, in.PeerID, in.Gfx, now, in.Host, id)
	if err := affectedOne(res, err); err != nil {
		return Computer{}, err
	}
	if in.Password != nil {
		sealed := ""
		if *in.Password != "" {
			if sealed, err = s.seal(*in.Password); err != nil {
				return Computer{}, err
			}
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE remote_desktop_computers SET password_enc = ? WHERE id = ?`, sealed, id); err != nil {
			return Computer{}, err
		}
	}
	return s.get(ctx, id)
}

func (s *store) setStreamHost(ctx context.Context, id string, hostID int) error {
	return affectedOne(s.db.ExecContext(ctx, `UPDATE remote_desktop_computers SET stream_host_id = ? WHERE id = ?`, hostID, id))
}

// forgetStreamHosts is for a bridge that was removed with its pairings.
func (s *store) forgetStreamHosts(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE remote_desktop_computers SET stream_host_id = 0`)
	return err
}

func (s *store) delete(ctx context.Context, id string) error {
	return affectedOne(s.db.ExecContext(ctx, `DELETE FROM remote_desktop_computers WHERE id = ?`, id))
}

func (s *store) password(ctx context.Context, id string) (string, error) {
	var sealed string
	err := s.db.QueryRowContext(ctx, `SELECT password_enc FROM remote_desktop_computers WHERE id = ?`, id).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return s.open(sealed)
}

func affectedOne(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SessionRecord is one remote session as the history keeps it.
type SessionRecord struct {
	ID         int64  `json:"id"`
	ComputerID string `json:"computerId"`
	Actor      string `json:"actor"`
	StartedAt  int64  `json:"startedAt"`
	EndedAt    int64  `json:"endedAt"`
	Outcome    string `json:"outcome"`
}

func (s *store) openSession(ctx context.Context, computer, actor string) (int64, error) {
	now := s.now().Unix()
	res, err := s.db.ExecContext(ctx, `INSERT INTO remote_desktop_sessions (computer_id, actor, started_at) VALUES (?, ?, ?)`, computer, actor, now)
	if err != nil {
		return 0, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE remote_desktop_computers SET last_session_at = ? WHERE id = ?`, now, computer); err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *store) closeSession(ctx context.Context, id int64, outcome string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE remote_desktop_sessions SET ended_at = ?, outcome = ? WHERE id = ?`, s.now().Unix(), outcome, id)
	return err
}

// interruptSessions closes what a restart left open, so the history never
// shows a session as still running when the process that carried it is gone.
func (s *store) interruptSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE remote_desktop_sessions SET ended_at = started_at, outcome = 'interrupted' WHERE ended_at = 0`)
	return err
}

func (s *store) sessions(ctx context.Context, since int64, limit int) ([]SessionRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, computer_id, actor, started_at, ended_at, outcome
		FROM remote_desktop_sessions WHERE started_at >= ? ORDER BY started_at DESC, id DESC LIMIT ?`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionRecord{}
	for rows.Next() {
		var r SessionRecord
		if err := rows.Scan(&r.ID, &r.ComputerID, &r.Actor, &r.StartedAt, &r.EndedAt, &r.Outcome); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func newID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func ValidID(id string) bool {
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == 16 && strings.ToLower(id) == id
}
