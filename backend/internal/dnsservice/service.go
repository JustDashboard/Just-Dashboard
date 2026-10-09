package dnsservice

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"
)

type Options struct {
	DB      *sql.DB
	Seal    func(string) (string, error)
	Open    func(string) (string, error)
	Runtime ProvisionRuntime
}

type Service struct {
	db      *sql.DB
	seal    func(string) (string, error)
	open    func(string) (string, error)
	active  chan struct{}
	runtime ProvisionRuntime
}

func New(o Options) *Service {
	return &Service{db: o.DB, seal: o.Seal, open: o.Open, active: make(chan struct{}, 4), runtime: o.Runtime}
}

func (s *Service) ready() error {
	if s == nil || s.db == nil || s.seal == nil || s.open == nil {
		return ErrUnavailable
	}
	return nil
}

func (s *Service) acquire(ctx context.Context) error {
	select {
	case s.active <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return errors.New("four native DNS operations are already active")
	}
}

func newID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func validID(id string) bool {
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == 16 && strings.ToLower(id) == id
}

const connectionColumns = `id,name,engine,endpoint,server_name,custom_ca,management,generation,ownership,container_id,created_at,updated_at`

type scanner interface{ Scan(...any) error }

func scanConnection(row scanner) (Connection, error) {
	var c Connection
	var created, updated int64
	err := row.Scan(&c.ID, &c.Name, &c.Engine, &c.Endpoint, &c.ServerName, &c.CustomCA, &c.Management, &c.Generation, &c.Ownership, &c.ContainerID, &created, &updated)
	c.CreatedAt, c.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
	c.HasCredential = true
	return c, err
}

type sealedSettings struct {
	Credential Credential `json:"credential"`
	CA         string     `json:"ca"`
}

func (s *Service) connection(ctx context.Context, id string) (Connection, ConnectionRequest, error) {
	if err := s.ready(); err != nil {
		return Connection{}, ConnectionRequest{}, err
	}
	if !validID(id) {
		return Connection{}, ConnectionRequest{}, ErrNotFound
	}
	c, err := scanConnection(s.db.QueryRowContext(ctx, `SELECT `+connectionColumns+` FROM network_dns_services WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return c, ConnectionRequest{}, err
	}
	if c.Ownership == "provisioned" {
		p, e := scanProvision(s.db.QueryRowContext(ctx, `SELECT `+provisionColumns+` FROM network_dns_service_provisions WHERE connection_id=?`, c.ID))
		if e != nil || p.State != "verified" || p.Resources.ContainerID != c.ContainerID {
			return c, ConnectionRequest{}, ErrConflict
		}
		if s.runtime == nil {
			return c, ConnectionRequest{}, ErrUnavailable
		}
		ownerCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = s.runtime.Verify(ownerCtx, p.spec(), p.Resources)
		cancel()
		if err != nil {
			return c, ConnectionRequest{}, ErrUnavailable
		}
	}
	var sealed string
	if err = s.db.QueryRowContext(ctx, `SELECT secret_enc FROM network_dns_services WHERE id=? AND generation=?`, id, c.Generation).Scan(&sealed); err != nil {
		return c, ConnectionRequest{}, ErrConflict
	}
	plain, err := s.open(sealed)
	if err != nil {
		return c, ConnectionRequest{}, errors.New("native DNS credential cannot be opened with this install's key")
	}
	var settings sealedSettings
	if json.Unmarshal([]byte(plain), &settings) != nil {
		return c, ConnectionRequest{}, errors.New("sealed native DNS settings are unreadable")
	}
	c.CustomCA = settings.CA != ""
	req := ConnectionRequest{Name: c.Name, Engine: c.Engine, Endpoint: c.Endpoint, ServerName: c.ServerName, CA: settings.CA, Management: c.Management, Credential: settings.Credential}
	req, err = ValidateConnection(req)
	return c, req, err
}

func (s *Service) List(ctx context.Context) ([]Connection, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+connectionColumns+` FROM network_dns_services ORDER BY created_at,id LIMIT 32`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Connection{}
	for rows.Next() {
		c, e := scanConnection(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Service) Connect(ctx context.Context, req ConnectionRequest) (View, error) {
	if err := s.ready(); err != nil {
		return View{}, err
	}
	req, err := ValidateConnection(req)
	if err != nil {
		return View{}, err
	}
	if err = s.acquire(ctx); err != nil {
		return View{}, err
	}
	defer func() { <-s.active }()
	snapshot, err := inspectNative(ctx, req)
	if err != nil {
		return View{}, err
	}
	c, err := s.saveConnection(ctx, req, "connected", "")
	return View{Connection: c, Snapshot: snapshot, State: "available"}, err
}

func (s *Service) saveConnection(ctx context.Context, req ConnectionRequest, ownership, container string) (Connection, error) {
	settings, _ := json.Marshal(sealedSettings{req.Credential, req.CA})
	sealed, err := s.seal(string(settings))
	if err != nil {
		return Connection{}, errors.New("native DNS credential cannot be sealed")
	}
	id, err := newID()
	if err != nil {
		return Connection{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Connection{}, err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM network_dns_services`).Scan(&count); err != nil {
		return Connection{}, err
	}
	if count >= 32 {
		return Connection{}, errors.New("at most 32 native DNS service connections are retained")
	}
	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx, `INSERT INTO network_dns_services(id,name,engine,endpoint,server_name,custom_ca,secret_enc,management,generation,ownership,container_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,1,?,?,?,?)`, id, req.Name, req.Engine, req.Endpoint, req.ServerName, req.CA != "", sealed, req.Management, ownership, container, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return Connection{}, err
	}
	if err = tx.Commit(); err != nil {
		return Connection{}, err
	}
	return Connection{ID: id, Name: req.Name, Engine: req.Engine, Endpoint: req.Endpoint, ServerName: req.ServerName, CustomCA: req.CA != "", Management: req.Management, HasCredential: true, Generation: 1, Ownership: ownership, ContainerID: container, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Service) Inspect(ctx context.Context, id string) (View, error) {
	c, req, err := s.connection(ctx, id)
	if err != nil {
		return View{}, err
	}
	if err = s.acquire(ctx); err != nil {
		return View{}, err
	}
	defer func() { <-s.active }()
	snapshot, err := inspectNative(ctx, req)
	view := View{Connection: c, Snapshot: snapshot, State: "available"}
	if err != nil {
		view.State, view.Error = "unavailable", err.Error()
	}
	return view, nil
}

func (s *Service) Update(ctx context.Context, id string, req ConnectionRequest) (View, error) {
	current, _, err := s.connection(ctx, id)
	if err != nil {
		return View{}, err
	}
	if req.Endpoint != current.Endpoint || req.Engine != current.Engine {
		return View{}, errors.New("the native endpoint and engine are fixed; create a separate connection to change their identity")
	}
	req, err = ValidateConnection(req)
	if err != nil {
		return View{}, err
	}
	if err = s.acquire(ctx); err != nil {
		return View{}, err
	}
	defer func() { <-s.active }()
	snapshot, err := inspectNative(ctx, req)
	if err != nil {
		return View{}, err
	}
	settings, _ := json.Marshal(sealedSettings{req.Credential, req.CA})
	sealed, err := s.seal(string(settings))
	if err != nil {
		return View{}, errors.New("native DNS credential cannot be sealed")
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE network_dns_services SET name=?,server_name=?,custom_ca=?,secret_enc=?,management=?,generation=generation+1,updated_at=? WHERE id=? AND generation=? AND NOT EXISTS(SELECT 1 FROM network_dns_service_changes WHERE connection_id=? AND state='applying')`, req.Name, req.ServerName, req.CA != "", sealed, req.Management, now.UnixMilli(), id, current.Generation, id)
	if err != nil {
		return View{}, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return View{}, ErrConflict
	}
	current.Name, current.ServerName, current.Management, current.CustomCA = req.Name, req.ServerName, req.Management, req.CA != ""
	current.Generation++
	current.UpdatedAt = now
	return View{Connection: current, Snapshot: snapshot, State: "available"}, nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	c, _, err := s.connection(ctx, id)
	if err != nil {
		return err
	}
	if c.Ownership != "connected" {
		return errors.New("remove this owned DNS provision through its resource review")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM network_dns_service_changes WHERE connection_id=? AND state='applying'`, id).Scan(&active); err != nil {
		return err
	}
	if active != 0 {
		return ErrConflict
	}
	deleted, err := tx.ExecContext(ctx, `DELETE FROM network_dns_services WHERE id=? AND generation=?`, id, c.Generation)
	if err != nil {
		return err
	}
	if n, _ := deleted.RowsAffected(); n != 1 {
		return ErrConflict
	}
	return tx.Commit()
}

func (s *Service) Preview(ctx context.Context, id string, req ChangeRequest) (Change, error) {
	c, native, err := s.connection(ctx, id)
	if err != nil {
		return Change{}, err
	}
	if !c.Management {
		return Change{}, ErrReadOnly
	}
	if err = validateChange(req, c.Engine); err != nil {
		return Change{}, err
	}
	if err = s.acquire(ctx); err != nil {
		return Change{}, err
	}
	defer func() { <-s.active }()
	before, err := inspectNative(ctx, native)
	if err != nil {
		return Change{}, err
	}
	if before.ProtectionTemporary {
		return Change{}, errors.New("native temporary protection is active; review its timer in the native console before staging a change")
	}
	if req.Action == "zone_create" {
		if before.ZoneEvidence.State != "native_authority_configuration" {
			return Change{}, errors.New("complete native zone ownership is required before creating a zone")
		}
		for _, zone := range before.Zones {
			if strings.EqualFold(zone.Name, req.Zone) {
				return Change{}, errors.New("the native zone already exists; no replacement was staged")
			}
		}
	}
	before.Queries = []Query{}
	planID, err := newID()
	if err != nil {
		return Change{}, err
	}
	now := time.Now().UTC()
	change := Change{ID: planID, ConnectionID: id, Generation: c.Generation, Request: req, Before: before, State: "planned", CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	requestJSON, _ := json.Marshal(req)
	beforeJSON, _ := json.Marshal(before)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Change{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM network_dns_service_changes WHERE (state='planned' AND expires_at<?) OR (state NOT IN ('planned','applying') AND ended_at<?)`, now.UnixMilli(), now.Add(-7*24*time.Hour).UnixMilli()); err != nil {
		return Change{}, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM network_dns_service_changes`).Scan(&count); err != nil {
		return Change{}, err
	}
	if count >= 128 {
		return Change{}, errors.New("at most 128 reviewed native DNS changes are retained")
	}
	inserted, err := tx.ExecContext(ctx, `INSERT INTO network_dns_service_changes(id,connection_id,generation,request_json,before_json,state,created_at,expires_at) SELECT ?,?,?,?,?, 'planned',?,? WHERE EXISTS(SELECT 1 FROM network_dns_services WHERE id=? AND generation=? AND management=1)`, planID, id, c.Generation, string(requestJSON), string(beforeJSON), now.UnixMilli(), change.ExpiresAt.UnixMilli(), id, c.Generation)
	if err != nil {
		return Change{}, err
	}
	if count, _ := inserted.RowsAffected(); count != 1 {
		return Change{}, ErrConflict
	}
	if err = tx.Commit(); err != nil {
		return Change{}, err
	}
	return change, nil
}

func (s *Service) Change(ctx context.Context, id string) (Change, error) {
	if err := s.ready(); err != nil {
		return Change{}, err
	}
	if !validID(id) {
		return Change{}, ErrNotFound
	}
	return readChange(s.db.QueryRowContext(ctx, `SELECT id,connection_id,generation,request_json,before_json,after_json,state,created_at,expires_at,ended_at,error FROM network_dns_service_changes WHERE id=?`, id))
}

func readChange(row scanner) (Change, error) {
	var c Change
	var request, before, after string
	var created, expires, ended int64
	err := row.Scan(&c.ID, &c.ConnectionID, &c.Generation, &request, &before, &after, &c.State, &created, &expires, &ended, &c.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	if json.Unmarshal([]byte(request), &c.Request) != nil || json.Unmarshal([]byte(before), &c.Before) != nil || (after != "" && json.Unmarshal([]byte(after), &c.After) != nil) {
		return c, errors.New("retained native DNS change is unreadable")
	}
	c.CreatedAt, c.ExpiresAt = time.UnixMilli(created).UTC(), time.UnixMilli(expires).UTC()
	if ended != 0 {
		at := time.UnixMilli(ended).UTC()
		c.EndedAt = &at
	}
	return c, nil
}

func (s *Service) Apply(ctx context.Context, id string) (Change, error) {
	if err := s.ready(); err != nil {
		return Change{}, err
	}
	if !validID(id) {
		return Change{}, ErrNotFound
	}
	if err := s.acquire(ctx); err != nil {
		return Change{}, err
	}
	defer func() { <-s.active }()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Change{}, err
	}
	defer tx.Rollback()
	plan, err := readChange(tx.QueryRowContext(ctx, `SELECT id,connection_id,generation,request_json,before_json,after_json,state,created_at,expires_at,ended_at,error FROM network_dns_service_changes WHERE id=?`, id))
	if err != nil {
		return Change{}, err
	}
	if plan.State != "planned" || !plan.ExpiresAt.After(time.Now()) {
		return plan, ErrConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE network_dns_service_changes SET state='applying' WHERE id=? AND state='planned' AND EXISTS(SELECT 1 FROM network_dns_services WHERE id=? AND generation=? AND management=1) AND NOT EXISTS(SELECT 1 FROM network_dns_service_changes WHERE connection_id=? AND state='applying')`, id, plan.ConnectionID, plan.Generation, plan.ConnectionID)
	if err != nil {
		return plan, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return plan, ErrConflict
	}
	if err = tx.Commit(); err != nil {
		return plan, err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()
	_, native, err := s.connection(ctx, plan.ConnectionID)
	var current *Snapshot
	if err == nil {
		current, err = inspectNative(ctx, native)
		if err == nil && (plan.Before == nil || current.PolicyFingerprint != plan.Before.PolicyFingerprint) {
			err = ErrConflict
		}
	}
	if err != nil {
		plan.State, plan.Error = "refused", err.Error()
		return s.finishChange(plan)
	}
	needsRestart := native.Engine == PiHole && plan.Request.Action == "upstreams" && !changeMatches(native.Engine, plan.Request, current)
	if needsRestart && current.Process == nil {
		plan.State, plan.Error = "refused", "Native FTL process identity is unavailable; restart completion cannot be verified."
		return s.finishChange(plan)
	}
	mutationAt := time.Now().UTC()
	applyErr := applyNative(ctx, native, plan.Request)
	after, readErr := inspectNative(ctx, native)
	if applyErr == nil && needsRestart {
		// A successful FTL config PATCH can precede its asynchronous exit. Only
		// read again: repeating the mutation could hide an uncertain restart.
		for readErr != nil || !ftlRestarted(current, after, mutationAt) {
			select {
			case <-ctx.Done():
				readErr = errors.New("native FTL restart completion was not established")
				goto readback
			case <-time.After(250 * time.Millisecond):
			}
			_, fresh, connectionErr := s.connection(ctx, plan.ConnectionID)
			if connectionErr != nil {
				readErr = connectionErr
				continue
			}
			after, readErr = inspectNative(ctx, fresh)
		}
	}
readback:
	if after != nil {
		after.Queries = []Query{}
	}
	plan.After = after
	plan.State = "verified"
	if applyErr != nil {
		plan.State, plan.Error = "needs_review", applyErr.Error()
	} else if readErr != nil {
		plan.State, plan.Error = "needs_review", "Native readback is unavailable; the operation was not repeated."
	} else if !changeMatches(native.Engine, plan.Request, after) {
		plan.State, plan.Error = "needs_review", "Native readback does not match the reviewed intent; no automatic retry or foreign-policy restore was attempted."
	}
	return s.finishChange(plan)
}

func ftlRestarted(before, after *Snapshot, mutationAt time.Time) bool {
	if before == nil || after == nil || before.Process == nil || after.Process == nil {
		return false
	}
	// Container PID namespaces may reuse the same PID after Docker restarts.
	// Native uptime must establish a later boot, beyond request timing jitter.
	return after.Process.StartedAt.After(mutationAt) && after.Process.StartedAt.After(before.Process.StartedBefore.Add(time.Second))
}

func (s *Service) finishChange(plan Change) (Change, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	now := time.Now().UTC()
	plan.EndedAt = &now
	after := ""
	if plan.After != nil {
		body, _ := json.Marshal(plan.After)
		after = string(body)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE network_dns_service_changes SET state=?,after_json=?,ended_at=?,error=? WHERE id=? AND state='applying'`, plan.State, after, now.UnixMilli(), plan.Error, plan.ID)
	if err != nil {
		return plan, errors.New("native DNS operation ended but its result could not be retained; the connection remains fenced for review")
	}
	return plan, nil
}

func changeMatches(engine Engine, req ChangeRequest, after *Snapshot) bool {
	if after == nil {
		return false
	}
	switch req.Action {
	case "protection":
		return req.Protection != nil && after.Protection == *req.Protection && !after.ProtectionTemporary
	case "access":
		return reflect.DeepEqual(req.AllowedClients, after.AllowedClients) && ((len(req.DeniedClients) == 0 && len(after.DeniedClients) == 0) || reflect.DeepEqual(req.DeniedClients, after.DeniedClients))
	case "upstreams":
		if len(req.Upstreams) != len(after.Upstreams) {
			return false
		}
		for i, endpoint := range req.Upstreams {
			wanted, ok := canonicalUpstream(engine, endpoint)
			actual, actualOK := canonicalUpstream(engine, after.Upstreams[i])
			if !ok || !actualOK || wanted != actual {
				return false
			}
		}
		return engine != Technitium || after.UpstreamProtocol == "Udp"
	case "zone_create":
		for _, zone := range after.Zones {
			if strings.EqualFold(zone.Name, req.Zone) && zone.Type == "Primary" && !zone.Disabled {
				return true
			}
		}
	}
	return false
}

func (s *Service) Reconcile(ctx context.Context) error {
	if err := s.ready(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE network_dns_service_changes SET state='interrupted',ended_at=?,error='The predecessor ended during a native DNS change. No native operation was replayed; inspect the engine before staging again.' WHERE state='applying'`, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	return s.reconcileProvisions(ctx)
}

func (s *Service) Close() error {
	if s == nil || s.runtime == nil {
		return nil
	}
	return s.runtime.Close()
}
