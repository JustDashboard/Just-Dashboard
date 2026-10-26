// Package logdrain ships what deployments say — their containers' output
// and the requests the ingress served them — to a log service the operator
// already runs: anything that takes OpenTelemetry logs over OTLP/HTTP, a
// Grafana Loki, or an HTTP endpoint that takes JSON lines (Vector, Fluent
// Bit, Logstash, Axiom). Nothing is read for a drain that the dashboard does
// not already read for its own pages; a drain is where a copy goes.
package logdrain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// The kinds of destination: the wire format a drain speaks.
const (
	KindOTLP = "otlp"
	KindLoki = "loki"
	KindHTTP = "http"
)

// The two things a deployment says.
const (
	StreamOutput   = "output"
	StreamRequests = "requests"
)

var (
	ErrNotFound  = errors.New("log drain not found")
	ErrNameTaken = errors.New("a log drain with that name already exists")
	ErrInvalid   = errors.New("invalid log drain")
)

// Header is one request header a drain sends, usually its credential. The
// value is sealed; only the name is ever read back.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Drain is a destination as the page reads it: no header value, ever.
type Drain struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Endpoint string   `json:"endpoint"`
	Enabled  bool     `json:"enabled"`
	Streams  []string `json:"streams"`
	// Projects narrows the drain to these projects; empty sends every
	// project, including the ones created after the drain.
	Projects    []int64   `json:"projects"`
	HeaderNames []string  `json:"headerNames"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	Status      Status    `json:"status"`
}

// Status is what a drain has done since it was created: what it shipped and
// dropped, and how its last delivery went.
type Status struct {
	Shipped       int64      `json:"shipped"`
	Dropped       int64      `json:"dropped"`
	Batches       int64      `json:"batches"`
	LastSuccessAt *time.Time `json:"lastSuccessAt,omitempty"`
	LastError     string     `json:"lastError,omitempty"`
	LastErrorAt   *time.Time `json:"lastErrorAt,omitempty"`
	// Queued is what waits in memory for the next delivery; it is not
	// persisted, and a restart loses it.
	Queued int `json:"queued"`
}

// Write is a create or an update. A nil Headers on update keeps the sealed
// ones; an empty list removes them.
type Write struct {
	Name     string    `json:"name"`
	Kind     string    `json:"kind"`
	Endpoint string    `json:"endpoint"`
	Enabled  bool      `json:"enabled"`
	Streams  []string  `json:"streams"`
	Projects []int64   `json:"projects"`
	Headers  *[]Header `json:"headers,omitempty"`
}

// sealedConfig is what config_enc holds.
type sealedConfig struct {
	Headers []Header `json:"headers"`
}

// Store keeps drains in SQLite with their headers sealed.
type Store struct {
	db     *sql.DB
	sealer *auth.Sealer
	now    func() time.Time
}

func NewStore(st *store.Store, sealer *auth.Sealer) *Store {
	return &Store{db: st.DB, sealer: sealer, now: time.Now}
}

var (
	nameRe       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)
	headerNameRe = regexp.MustCompile(`^[A-Za-z0-9!#$%&'*+.^_` + "`" + `|~-]{1,64}$`)
)

// reservedHeaders are the ones the encoders set themselves or that would let
// a drain rewrite where its request goes.
var reservedHeaders = []string{"content-type", "content-encoding", "content-length", "host", "transfer-encoding", "connection"}

// normalizeEndpoint checks an endpoint the way every credentialed outbound
// call in the dashboard is checked: a header goes over https, or plain http
// to this machine. A drain without headers may use plain http anywhere — a
// collector on the local network usually has no TLS and no credential — and
// the page says that its records then cross the network unencrypted.
func normalizeEndpoint(raw string, hasHeaders bool) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", fmt.Errorf("%w: the endpoint must be an http or https URL without credentials", ErrInvalid)
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		host := strings.Trim(parsed.Hostname(), "[]")
		ip := net.ParseIP(host)
		loopback := ip != nil && ip.IsLoopback()
		if hasHeaders && !loopback {
			return "", fmt.Errorf("%w: headers are never sent over plain http except to this machine; use https", ErrInvalid)
		}
	default:
		return "", fmt.Errorf("%w: the endpoint must be an http or https URL", ErrInvalid)
	}
	if len(raw) > 2048 {
		return "", fmt.Errorf("%w: the endpoint is too long", ErrInvalid)
	}
	return parsed.String(), nil
}

func normalizeWrite(w *Write, existingHeaders []Header) ([]Header, error) {
	w.Name = strings.TrimSpace(w.Name)
	if !nameRe.MatchString(w.Name) {
		return nil, fmt.Errorf("%w: a name is letters, digits, spaces, dots, dashes and underscores, up to 64", ErrInvalid)
	}
	if w.Kind != KindOTLP && w.Kind != KindLoki && w.Kind != KindHTTP {
		return nil, fmt.Errorf("%w: the kind is otlp, loki or http", ErrInvalid)
	}
	streams := []string{}
	for _, stream := range w.Streams {
		if stream != StreamOutput && stream != StreamRequests {
			return nil, fmt.Errorf("%w: a stream is output or requests", ErrInvalid)
		}
		if !slices.Contains(streams, stream) {
			streams = append(streams, stream)
		}
	}
	if len(streams) == 0 {
		return nil, fmt.Errorf("%w: choose container output, requests or both", ErrInvalid)
	}
	slices.Sort(streams)
	w.Streams = streams
	projects := []int64{}
	for _, id := range w.Projects {
		if id <= 0 {
			return nil, fmt.Errorf("%w: a project id is positive", ErrInvalid)
		}
		if !slices.Contains(projects, id) {
			projects = append(projects, id)
		}
	}
	slices.Sort(projects)
	w.Projects = projects
	headers := existingHeaders
	if w.Headers != nil {
		headers = []Header{}
		for _, header := range *w.Headers {
			header.Name = strings.TrimSpace(header.Name)
			if header.Name == "" && header.Value == "" {
				continue
			}
			if !headerNameRe.MatchString(header.Name) || slices.Contains(reservedHeaders, strings.ToLower(header.Name)) {
				return nil, fmt.Errorf("%w: %q is not a header the drain may set", ErrInvalid, header.Name)
			}
			if header.Value == "" || len(header.Value) > 8192 || strings.ContainsAny(header.Value, "\r\n\x00") {
				return nil, fmt.Errorf("%w: the value of %s is empty, too long or has a line break", ErrInvalid, header.Name)
			}
			for _, existing := range headers {
				if strings.EqualFold(existing.Name, header.Name) {
					return nil, fmt.Errorf("%w: %s is set twice", ErrInvalid, header.Name)
				}
			}
			headers = append(headers, header)
		}
		if len(headers) > 16 {
			return nil, fmt.Errorf("%w: at most 16 headers", ErrInvalid)
		}
	}
	endpoint, err := normalizeEndpoint(w.Endpoint, len(headers) > 0)
	if err != nil {
		return nil, err
	}
	w.Endpoint = endpoint
	return headers, nil
}

func (s *Store) seal(headers []Header) (string, error) {
	encoded, err := json.Marshal(sealedConfig{Headers: headers})
	if err != nil {
		return "", err
	}
	return s.sealer.Seal(string(encoded))
}

func (s *Store) open(sealed string) ([]Header, error) {
	if sealed == "" {
		return []Header{}, nil
	}
	plain, err := s.sealer.Open(sealed)
	if err != nil {
		return nil, errors.New("the drain's headers cannot be decrypted")
	}
	var config sealedConfig
	if err := json.Unmarshal([]byte(plain), &config); err != nil {
		return nil, err
	}
	if config.Headers == nil {
		config.Headers = []Header{}
	}
	return config.Headers, nil
}

const drainColumns = `d.id, d.name, d.kind, d.endpoint, d.enabled, d.streams_json, d.projects_json, d.config_enc, d.created_at, d.updated_at,
	COALESCE(st.shipped, 0), COALESCE(st.dropped, 0), COALESCE(st.batches, 0), COALESCE(st.last_success_at, 0), COALESCE(st.last_error, ''), COALESCE(st.last_error_at, 0)`

const drainFrom = ` FROM deploy_log_drains d LEFT JOIN deploy_log_drain_status st ON st.drain_id = d.id`

// drainRow is a drain as stored, its headers opened.
type drainRow struct {
	Drain
	headers []Header
}

func (s *Store) scan(row interface{ Scan(...any) error }) (*drainRow, error) {
	var out drainRow
	var enabled int
	var streams, projects, sealed string
	var created, updated, lastSuccess, lastErrorAt int64
	if err := row.Scan(&out.ID, &out.Name, &out.Kind, &out.Endpoint, &enabled, &streams, &projects, &sealed, &created, &updated,
		&out.Status.Shipped, &out.Status.Dropped, &out.Status.Batches, &lastSuccess, &out.Status.LastError, &lastErrorAt); err != nil {
		return nil, err
	}
	out.Enabled = enabled == 1
	_ = json.Unmarshal([]byte(streams), &out.Streams)
	_ = json.Unmarshal([]byte(projects), &out.Projects)
	if out.Projects == nil {
		out.Projects = []int64{}
	}
	out.CreatedAt, out.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	if lastSuccess > 0 {
		at := time.Unix(0, lastSuccess).UTC()
		out.Status.LastSuccessAt = &at
	}
	if lastErrorAt > 0 {
		at := time.Unix(0, lastErrorAt).UTC()
		out.Status.LastErrorAt = &at
	}
	headers, err := s.open(sealed)
	if err != nil {
		// A drain whose key changed is still listed, so it can be fixed or
		// removed; it ships nothing until its headers are entered again.
		headers = nil
		out.Status.LastError = err.Error()
	}
	out.headers = headers
	out.HeaderNames = []string{}
	for _, header := range headers {
		out.HeaderNames = append(out.HeaderNames, header.Name)
	}
	return &out, nil
}

func (s *Store) all(ctx context.Context) ([]*drainRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+drainColumns+drainFrom+` ORDER BY d.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*drainRow{}
	for rows.Next() {
		row, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) get(ctx context.Context, id int64) (*drainRow, error) {
	row, err := s.scan(s.db.QueryRowContext(ctx, `SELECT `+drainColumns+drainFrom+` WHERE d.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return row, err
}

// List is every drain as the page reads it.
func (s *Store) List(ctx context.Context) ([]Drain, error) {
	rows, err := s.all(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Drain, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Drain)
	}
	return out, nil
}

func (s *Store) Get(ctx context.Context, id int64) (*Drain, error) {
	row, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &row.Drain, nil
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}

func (s *Store) Create(ctx context.Context, w Write) (*Drain, error) {
	headers, err := normalizeWrite(&w, []Header{})
	if err != nil {
		return nil, err
	}
	sealed, err := s.seal(headers)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC().Unix()
	result, err := s.db.ExecContext(ctx, `INSERT INTO deploy_log_drains(name, kind, endpoint, enabled, streams_json, projects_json, config_enc, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, w.Name, w.Kind, w.Endpoint, boolInt(w.Enabled), mustJSON(w.Streams), mustJSON(w.Projects), sealed, now, now)
	if isUnique(err) {
		return nil, ErrNameTaken
	}
	if err != nil {
		return nil, err
	}
	id, _ := result.LastInsertId()
	return s.Get(ctx, id)
}

func (s *Store) Update(ctx context.Context, id int64, w Write) (*Drain, error) {
	current, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	headers, err := normalizeWrite(&w, current.headers)
	if err != nil {
		return nil, err
	}
	sealed, err := s.seal(headers)
	if err != nil {
		return nil, err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE deploy_log_drains SET name=?, kind=?, endpoint=?, enabled=?, streams_json=?, projects_json=?, config_enc=?, updated_at=? WHERE id=?`,
		w.Name, w.Kind, w.Endpoint, boolInt(w.Enabled), mustJSON(w.Streams), mustJSON(w.Projects), sealed, s.now().UTC().Unix(), id)
	if isUnique(err) {
		return nil, ErrNameTaken
	}
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *Store) SetEnabled(ctx context.Context, id int64, enabled bool) (*Drain, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE deploy_log_drains SET enabled=?, updated_at=? WHERE id=?`, boolInt(enabled), s.now().UTC().Unix(), id)
	if err != nil {
		return nil, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.Get(ctx, id)
}

func (s *Store) Delete(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM deploy_log_drains WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// recordDelivery folds one delivery's outcome into the drain's status.
func (s *Store) recordDelivery(ctx context.Context, id int64, shipped, dropped int64, failure string, at time.Time) {
	ns := at.UTC().UnixNano()
	if failure == "" {
		_, _ = s.db.ExecContext(ctx, `INSERT INTO deploy_log_drain_status(drain_id, shipped, dropped, batches, last_success_at)
			VALUES(?,?,?,1,?) ON CONFLICT(drain_id) DO UPDATE SET shipped=shipped+excluded.shipped, dropped=dropped+excluded.dropped,
			batches=batches+1, last_success_at=excluded.last_success_at`, id, shipped, dropped, ns)
		return
	}
	_, _ = s.db.ExecContext(ctx, `INSERT INTO deploy_log_drain_status(drain_id, dropped, last_error, last_error_at)
		VALUES(?,?,?,?) ON CONFLICT(drain_id) DO UPDATE SET dropped=dropped+excluded.dropped, last_error=excluded.last_error,
		last_error_at=excluded.last_error_at`, id, dropped, clip(failure, 400), ns)
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func mustJSON(v any) string {
	encoded, _ := json.Marshal(v)
	return string(encoded)
}

func clip(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
}
