package dnsservice

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (s *Service) provisionOwner(ctx context.Context) (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	if _, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO network_dns_service_settings(key,value) VALUES('owner',?)`, id); err != nil {
		return "", err
	}
	var owner string
	if err = s.db.QueryRowContext(ctx, `SELECT value FROM network_dns_service_settings WHERE key='owner'`).Scan(&owner); err != nil {
		return "", err
	}
	if !validID(owner) {
		return "", errors.New("native DNS resource owner identity is unreadable")
	}
	return owner, nil
}

func (s *Service) PreviewProvision(ctx context.Context, req ProvisionRequest) (Provision, error) {
	if err := s.ready(); err != nil {
		return Provision{}, err
	}
	if s.runtime == nil {
		return Provision{}, ErrUnavailable
	}
	req, err := validateProvision(req)
	if err != nil {
		return Provision{}, err
	}
	if err = s.acquire(ctx); err != nil {
		return Provision{}, err
	}
	defer func() { <-s.active }()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	imageID, err := s.runtime.Image(ctx, req.Engine)
	if err != nil {
		return Provision{}, err
	}
	owner, err := s.provisionOwner(ctx)
	if err != nil {
		return Provision{}, err
	}
	id, err := newID()
	if err != nil {
		return Provision{}, err
	}
	secret, err := s.seal(req.Password)
	if err != nil {
		return Provision{}, errors.New("native bootstrap password cannot be sealed")
	}
	req.Password = ""
	now := time.Now().UTC()
	p := Provision{ID: id, Request: req, Image: pinnedImages[req.Engine], ImageID: imageID, Owner: owner, Resources: provisionIntent(id), State: "planned", CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute), Limitations: []string{"Creates an owned bridge, two named persistent volumes and one bounded native-engine container; management and classic DNS publish only on 127.0.0.1.", "The host resolver and existing DNS services are unchanged. Native engine maintenance traffic remains native behavior; no filter-list subscription or plugin is installed.", "Bootstrap credentials are sealed and seeded privately. A failed or interrupted setup is never replayed; cleanup touches only matching owned resources.", "A verified provision persists with its native hashed credentials and an unless-stopped restart policy. Removing it also removes its two owned data volumes."}}
	metadata, _ := json.Marshal(p)
	resources, _ := json.Marshal(p.Resources)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Provision{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM network_dns_service_provisions WHERE state='planned' AND expires_at<?`, now.UnixMilli()); err != nil {
		return Provision{}, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM network_dns_service_provisions WHERE state NOT IN ('removed','failed')`).Scan(&count); err != nil {
		return Provision{}, err
	}
	if count >= 16 {
		return Provision{}, errors.New("at most 16 native DNS provisions are retained")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO network_dns_service_provisions(id,request_json,secret_enc,resources_json,state,created_at,expires_at) VALUES(?,?,?,?,'planned',?,?)`, id, string(metadata), secret, string(resources), now.UnixMilli(), p.ExpiresAt.UnixMilli()); err != nil {
		return Provision{}, err
	}
	return p, tx.Commit()
}

func scanProvision(row scanner) (Provision, error) {
	var p Provision
	var metadata, resources string
	var created, expires, ended int64
	err := row.Scan(&metadata, &p.State, &resources, &p.ConnectionID, &created, &expires, &ended, &p.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	state, conn, problem := p.State, p.ConnectionID, p.Error
	if json.Unmarshal([]byte(metadata), &p) != nil || json.Unmarshal([]byte(resources), &p.Resources) != nil || !validID(p.ID) || !validID(p.Owner) || !validNativeDigest(p.ImageID) || !validDockerID(p.Resources.ContainerID) || !validDockerID(p.Resources.NetworkID) || p.Image != pinnedImages[p.Request.Engine] || p.Resources.NetworkName != provisionIntent(p.ID).NetworkName || p.Resources.ContainerName != provisionIntent(p.ID).ContainerName {
		return Provision{}, errors.New("retained native DNS provision identity is unreadable")
	}
	intent := provisionIntent(p.ID)
	if len(p.Resources.Volumes) != 2 || p.Resources.Volumes[0] != intent.Volumes[0] || p.Resources.Volumes[1] != intent.Volumes[1] {
		return Provision{}, errors.New("retained native DNS volume ownership is unreadable")
	}
	p.State, p.ConnectionID, p.Error = state, conn, problem
	p.Request.Password = ""
	p.CreatedAt, p.ExpiresAt = time.UnixMilli(created).UTC(), time.UnixMilli(expires).UTC()
	if ended != 0 {
		v := time.UnixMilli(ended).UTC()
		p.EndedAt = &v
	}
	return p, nil
}

func validDockerID(value string) bool {
	if value == "" {
		return true
	}
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == 32 && strings.ToLower(value) == value
}
func validNativeDigest(value string) bool {
	return strings.HasPrefix(value, "sha256:") && len(value) == 71 && validDockerID(strings.TrimPrefix(value, "sha256:"))
}

const provisionColumns = `request_json,state,resources_json,connection_id,created_at,expires_at,ended_at,error`

func (s *Service) Provision(ctx context.Context, id string) (Provision, error) {
	if err := s.ready(); err != nil {
		return Provision{}, err
	}
	if !validID(id) {
		return Provision{}, ErrNotFound
	}
	p, err := scanProvision(s.db.QueryRowContext(ctx, `SELECT `+provisionColumns+` FROM network_dns_service_provisions WHERE id=?`, id))
	if err == nil && p.ID != id {
		return Provision{}, errors.New("retained native DNS plan identity differs from its row")
	}
	return p, err
}
func (s *Service) Provisions(ctx context.Context) ([]Provision, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+provisionColumns+` FROM network_dns_service_provisions ORDER BY created_at DESC LIMIT 64`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Provision{}
	for rows.Next() {
		p, e := scanProvision(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) journalProvision(p *Provision, r ProvisionResources) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	encoded, _ := json.Marshal(r)
	result, err := s.db.ExecContext(ctx, `UPDATE network_dns_service_provisions SET resources_json=? WHERE id=? AND state='applying'`, string(encoded), p.ID)
	if err != nil {
		return errors.New("owned DNS resource journal could not be persisted")
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrConflict
	}
	p.Resources = r
	return nil
}

func (s *Service) ApplyProvision(ctx context.Context, id string) (Provision, error) {
	p, err := s.Provision(ctx, id)
	if err != nil {
		return p, err
	}
	if s.runtime == nil {
		return p, ErrUnavailable
	}
	if err = s.acquire(ctx); err != nil {
		return p, err
	}
	defer func() { <-s.active }()
	claimed, err := s.db.ExecContext(ctx, `UPDATE network_dns_service_provisions SET state='applying' WHERE id=? AND state='planned' AND expires_at>? AND NOT EXISTS(SELECT 1 FROM network_dns_service_provisions WHERE state IN ('applying','removing'))`, id, time.Now().UnixMilli())
	if err != nil {
		return p, err
	}
	if n, _ := claimed.RowsAffected(); n != 1 {
		return p, ErrConflict
	}
	p.State = "applying"
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 120*time.Second)
	defer cancel()
	var secret string
	if err = s.db.QueryRowContext(work, `SELECT secret_enc FROM network_dns_service_provisions WHERE id=? AND state='applying'`, id).Scan(&secret); err == nil {
		p.Request.Password, err = s.open(secret)
	}
	if err != nil {
		return s.failProvision(p, errors.New("sealed bootstrap password could not be opened"))
	}
	if _, err = validateProvision(p.Request); err != nil {
		return s.failProvision(p, err)
	}
	resources, err := s.runtime.Prepare(work, p.spec(), p.Resources, func(r ProvisionResources) error { return s.journalProvision(&p, r) })
	p.Resources = resources
	if err != nil {
		return s.failProvision(p, err)
	}
	p.Resources.Phase = "starting"
	if err = s.journalProvision(&p, p.Resources); err != nil {
		return s.failProvision(p, err)
	}
	if err = s.runtime.Start(work, p.spec(), p.Resources); err != nil {
		return s.failProvision(p, err)
	}
	p.Resources.Phase = "native_bootstrap"
	if err = s.journalProvision(&p, p.Resources); err != nil {
		return s.failProvision(p, err)
	}
	connection, snapshot, err := s.bootstrapOwned(work, p)
	if err != nil {
		return s.failProvision(p, err)
	}
	if err = s.runtime.RemoveBootstrapSecret(work, p.spec(), p.Resources); err != nil {
		return s.failProvision(p, err)
	}
	if err = s.runtime.Verify(work, p.spec(), p.Resources); err != nil {
		return s.failProvision(p, err)
	}
	if err = s.runtime.Activate(work, p.spec(), p.Resources); err != nil {
		return s.failProvision(p, err)
	}
	p.Resources.Phase = "verified"
	if err = s.finishProvision(work, &p, connection); err != nil {
		return s.failProvision(p, err)
	}
	_ = snapshot
	p.Request.Password = ""
	return p, nil
}

func (s *Service) bootstrapOwned(ctx context.Context, p Provision) (ConnectionRequest, *Snapshot, error) {
	req := ConnectionRequest{Name: p.Request.Name, Engine: p.Request.Engine, Endpoint: p.endpoint(), Management: p.Request.Management}
	if req.Engine == AdGuard {
		req.Credential = Credential{Username: p.Request.Username, Password: p.Request.Password}
	} else if req.Engine == PiHole {
		req.Credential = Credential{Password: p.Request.Password}
	} else {
		bootstrap := req
		bootstrap.Credential = Credential{Token: "bootstrap-not-used"}
		c, err := newNativeClient(bootstrap)
		if err != nil {
			return req, nil, err
		}
		defer c.close()
		c.engine = ""
		for {
			if err = s.runtime.Verify(ctx, p.spec(), p.Resources); err != nil {
				return req, nil, err
			}
			// The pinned console is larger than the native API body limit.
			// A HEAD readiness probe avoids downloading that unrelated HTML.
			if c.request(ctx, http.MethodHead, "/", nil, nil) == nil {
				break
			}
			select {
			case <-ctx.Done():
				return req, nil, errors.New("native Technitium bootstrap did not become ready")
			case <-time.After(250 * time.Millisecond):
			}
		}
		var created struct {
			Status string `json:"status"`
			Token  string `json:"token"`
		}
		if err = c.request(ctx, http.MethodPost, "/api/user/createToken", url.Values{"user": {"admin"}, "pass": {p.Request.Password}, "tokenName": {"Just Dashboard " + p.ID}}, &created); err != nil || created.Status != "ok" {
			return req, nil, errors.New("native Technitium credential bootstrap failed; it was not replayed")
		}
		req.Credential = Credential{Token: created.Token}
		if err = validateCredential(Technitium, req.Credential); err != nil {
			return req, nil, errors.New("native Technitium returned an unusable API token")
		}
	}
	for {
		if err := s.runtime.Verify(ctx, p.spec(), p.Resources); err != nil {
			return req, nil, err
		}
		snapshot, err := inspectNative(ctx, req)
		if err == nil {
			version := strings.TrimPrefix(snapshot.Version, "v")
			expected := map[Engine]string{AdGuard: "0.107.71", PiHole: "6.7.1", Technitium: "15.6.0"}[req.Engine]
			if version != expected && !(req.Engine == Technitium && version == "15.6") {
				return req, nil, errors.New("owned native engine version differs from its reviewed image contract")
			}
			if !changeMatches(req.Engine, ChangeRequest{Action: "upstreams", Upstreams: p.Request.Upstreams}, snapshot) || !snapshot.Protection {
				return req, nil, errors.New("owned native DNS policy readback differs from the reviewed bootstrap")
			}
			return req, snapshot, nil
		}
		select {
		case <-ctx.Done():
			return req, nil, errors.New("owned native DNS startup or authenticated readback did not complete")
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (s *Service) finishProvision(ctx context.Context, p *Provision, req ConnectionRequest) error {
	settings, _ := json.Marshal(sealedSettings{req.Credential, req.CA})
	secret, err := s.seal(string(settings))
	if err != nil {
		return errors.New("native provision credential could not be sealed")
	}
	id, err := newID()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	resources, _ := json.Marshal(p.Resources)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM network_dns_services`).Scan(&count); err != nil {
		return err
	}
	if count >= 32 {
		return errors.New("native DNS connection capacity changed before provision completion")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO network_dns_services(id,name,engine,endpoint,server_name,secret_enc,management,generation,ownership,container_id,created_at,updated_at) VALUES(?,?,?,?,'',?,?,1,'provisioned',?,?,?)`, id, req.Name, req.Engine, req.Endpoint, secret, req.Management, p.Resources.ContainerID, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return err
	}
	updated, err := tx.ExecContext(ctx, `UPDATE network_dns_service_provisions SET state='verified',connection_id=?,resources_json=?,secret_enc='',ended_at=?,error='' WHERE id=? AND state='applying'`, id, string(resources), now.UnixMilli(), p.ID)
	if err != nil {
		return err
	}
	if n, _ := updated.RowsAffected(); n != 1 {
		return ErrConflict
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	p.State, p.ConnectionID, p.EndedAt = "verified", id, &now
	return nil
}

func (s *Service) failProvision(p Provision, cause error) (Provision, error) {
	return s.failProvisionWithin(context.Background(), p, cause)
}

func (s *Service) failProvisionWithin(parent context.Context, p Provision, cause error) (Provision, error) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	retained, retainedErr := s.Provision(ctx, p.ID)
	if retainedErr != nil {
		p.State = "needs_review"
		p.Request.Password = ""
		p.Error = "Provision completion state is unreadable; no uncertain resource was removed."
		return p, errors.New(p.Error)
	}
	if retained.State == "verified" {
		return retained, nil
	}
	p.State = "failed"
	p.Error = cause.Error()
	if err := s.runtime.Destroy(ctx, p.spec(), p.Resources); err != nil {
		p.State = "needs_review"
		p.Error += "; owned cleanup needs review: " + err.Error()
	} else {
		p.Resources.Phase = "cleaned"
	}
	if p.Request.Password != "" {
		p.Error = strings.ReplaceAll(p.Error, p.Request.Password, "[redacted]")
	}
	p.Request.Password = ""
	now := time.Now().UTC()
	p.EndedAt = &now
	resources, _ := json.Marshal(p.Resources)
	updated, err := s.db.ExecContext(ctx, `UPDATE network_dns_service_provisions SET state=?,resources_json=?,ended_at=?,error=?,secret_enc='' WHERE id=? AND state IN ('applying','interrupted')`, p.State, string(resources), now.UnixMilli(), p.Error, p.ID)
	if err != nil {
		return p, errors.New("native provision failed; its durable cleanup result could not be persisted")
	}
	if n, _ := updated.RowsAffected(); n != 1 {
		return p, ErrConflict
	}
	return p, nil
}

func (s *Service) RemoveProvision(ctx context.Context, id string) (Provision, error) {
	p, err := s.Provision(ctx, id)
	if err != nil {
		return p, err
	}
	if s.runtime == nil {
		return p, ErrUnavailable
	}
	if err = s.acquire(ctx); err != nil {
		return p, err
	}
	defer func() { <-s.active }()
	claimed, err := s.db.ExecContext(ctx, `UPDATE network_dns_service_provisions SET state='removing' WHERE id=? AND state IN ('verified','failed','needs_review','interrupted','planned') AND NOT EXISTS(SELECT 1 FROM network_dns_service_changes WHERE connection_id=? AND state='applying') AND NOT EXISTS(SELECT 1 FROM network_dns_service_provisions WHERE state IN ('applying','removing'))`, id, p.ConnectionID)
	if err != nil {
		return p, err
	}
	if n, _ := claimed.RowsAffected(); n != 1 {
		return p, ErrConflict
	}
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 40*time.Second)
	defer cancel()
	if err = s.runtime.Destroy(work, p.spec(), p.Resources); err != nil {
		s.db.ExecContext(work, `UPDATE network_dns_service_provisions SET state='needs_review',error=? WHERE id=? AND state='removing'`, err.Error(), id)
		p.State, p.Error = "needs_review", err.Error()
		return p, nil
	}
	tx, err := s.db.BeginTx(work, nil)
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	if p.ConnectionID != "" {
		if _, err = tx.ExecContext(work, `DELETE FROM network_dns_services WHERE id=? AND ownership='provisioned' AND container_id=?`, p.ConnectionID, p.Resources.ContainerID); err != nil {
			return p, err
		}
	}
	now := time.Now().UTC()
	p.Resources.Phase = "removed"
	resources, _ := json.Marshal(p.Resources)
	if _, err = tx.ExecContext(work, `UPDATE network_dns_service_provisions SET state='removed',resources_json=?,ended_at=?,error='',secret_enc='' WHERE id=? AND state='removing'`, string(resources), now.UnixMilli(), id); err != nil {
		return p, err
	}
	if err = tx.Commit(); err != nil {
		return p, err
	}
	p.State, p.Error, p.EndedAt = "removed", "", &now
	return p, nil
}

func (s *Service) reconcileProvisions(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE network_dns_service_provisions SET state='interrupted',error='The predecessor ended during owned setup or removal. Native bootstrap was not replayed.' WHERE state IN ('applying','removing')`); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM network_dns_service_provisions WHERE state IN ('applying','removing','interrupted') LIMIT 16`)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var problems []error
	for _, id := range ids {
		if e := ctx.Err(); e != nil {
			problems = append(problems, e)
			break
		}
		p, e := s.Provision(ctx, id)
		if e != nil {
			problems = append(problems, e)
			continue
		}
		if _, e = s.db.ExecContext(ctx, `UPDATE network_dns_service_provisions SET state='interrupted',error='The predecessor ended during owned setup or removal. Native bootstrap was not replayed.' WHERE id=? AND state IN ('applying','removing','interrupted')`, id); e != nil {
			problems = append(problems, e)
			continue
		}
		if s.runtime == nil {
			problems = append(problems, fmt.Errorf("owned native DNS cleanup unavailable for %s", id))
			continue
		}
		if _, e = s.failProvisionWithin(ctx, p, errors.New("predecessor interrupted; no native operation was replayed")); e != nil {
			problems = append(problems, e)
		}
	}
	return errors.Join(problems...)
}
