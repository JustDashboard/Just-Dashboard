package netx

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

const maxDNSEvidenceArtifact = 192 << 10

type SavedDNSEvidence struct {
	ID        string                  `json:"id"`
	Request   DNSInvestigationRequest `json:"request"`
	Status    string                  `json:"status"`
	StartedAt time.Time               `json:"startedAt"`
	EndedAt   *time.Time              `json:"endedAt,omitempty"`
	Result    *DNSInvestigation       `json:"result,omitempty"`
}

// ReconcileDNSEvidence is startup-only. A predecessor's lost answer is never
// reconstructed by issuing its private question again.
func (s *Service) ReconcileDNSEvidence(ctx context.Context) error {
	if s.db == nil {
		return ErrUnavailable
	}
	_, err := s.db.ExecContext(ctx, `UPDATE network_dns_evidence SET status='interrupted',ended_at=? WHERE status='running'`, time.Now().UnixMilli())
	return err
}

func pruneDNSEvidence(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM network_dns_evidence WHERE status!='running' AND (started_at<? OR id IN (SELECT id FROM network_dns_evidence WHERE status!='running' ORDER BY started_at DESC,id DESC LIMIT -1 OFFSET 128))`, time.Now().Add(-7*24*time.Hour).UnixMilli())
	return err
}

func (s *Service) CreateDNSEvidence(ctx context.Context, req DNSInvestigationRequest, actor string, execute TrafficExecutor) (SavedDNSEvidence, error) {
	req, err := ValidateDNSInvestigation(req)
	if err != nil {
		return SavedDNSEvidence{}, err
	}
	if s.db == nil {
		return SavedDNSEvidence{}, ErrUnavailable
	}
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return SavedDNSEvidence{}, err
	}
	record := SavedDNSEvidence{ID: hex.EncodeToString(entropy[:]), Request: req, Status: "running", StartedAt: time.Now().UTC()}
	raw, _ := json.Marshal(req)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return record, err
	}
	defer tx.Rollback()
	if err = pruneDNSEvidence(ctx, tx); err != nil {
		return record, err
	}
	var running int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM network_dns_evidence WHERE status='running'`).Scan(&running); err != nil {
		return record, err
	}
	if running >= 4 {
		return record, fmt.Errorf("at most four DNS investigations may be running")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO network_dns_evidence(id,request_json,actor,started_at) VALUES(?,?,?,?)`, record.ID, string(raw), actor, record.StartedAt.UnixMilli()); err != nil {
		return record, err
	}
	if err = tx.Commit(); err != nil {
		return record, err
	}
	result, probeErr := s.InvestigateDNS(ctx, req, execute)
	if result != nil {
		result.ID = record.ID
		record.Result = result
	}
	record.Status = "completed"
	if probeErr != nil {
		record.Status = "failed"
	}
	if ctx.Err() != nil {
		record.Status = "interrupted"
	}
	ended := time.Now().UTC()
	record.EndedAt = &ended
	artifact, _ := json.Marshal(result)
	if len(artifact) > maxDNSEvidenceArtifact {
		record.Status = "failed"
		record.Result = nil
		artifact = []byte("null")
		probeErr = fmt.Errorf("native DNS evidence exceeds the retained artifact bound")
	}
	// Record cancellation and request loss with an independent bounded context;
	// this finishes only persistence, never a second query.
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	final, err := s.db.BeginTx(finish, nil)
	if err != nil {
		return record, fmt.Errorf("DNS investigation finished but saving its evidence failed; it was not retried: %w", err)
	}
	defer final.Rollback()
	res, err := final.ExecContext(finish, `UPDATE network_dns_evidence SET status=?,ended_at=?,artifact_json=? WHERE id=? AND status='running'`, record.Status, ended.UnixMilli(), string(artifact), record.ID)
	if err != nil {
		return record, fmt.Errorf("DNS investigation finished but saving its evidence failed; it was not retried: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return record, fmt.Errorf("DNS investigation evidence could not be finalized")
	}
	if err = pruneDNSEvidence(finish, final); err != nil {
		return record, err
	}
	if err = final.Commit(); err != nil {
		return record, err
	}
	if probeErr != nil {
		return record, probeErr
	}
	return record, nil
}

func (s *Service) ListDNSEvidence(ctx context.Context) ([]SavedDNSEvidence, error) {
	if s.db == nil {
		return nil, ErrUnavailable
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = pruneDNSEvidence(ctx, tx); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,request_json,status,started_at,ended_at FROM network_dns_evidence ORDER BY started_at DESC,id DESC LIMIT 132`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SavedDNSEvidence{}
	for rows.Next() {
		r, err := scanDNSEvidence(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type dnsRowScanner interface{ Scan(...any) error }

func scanDNSEvidence(row dnsRowScanner, body bool) (SavedDNSEvidence, error) {
	var r SavedDNSEvidence
	var request, artifact string
	var started, ended int64
	args := []any{&r.ID, &request, &r.Status, &started, &ended}
	if body {
		args = append(args, &artifact)
	}
	if err := row.Scan(args...); err != nil {
		if err == sql.ErrNoRows {
			return r, ErrNotFound
		}
		return r, err
	}
	if len(request) > 4096 || json.Unmarshal([]byte(request), &r.Request) != nil {
		return r, fmt.Errorf("saved DNS scope is unreadable")
	}
	r.StartedAt = time.UnixMilli(started).UTC()
	if ended != 0 {
		at := time.UnixMilli(ended).UTC()
		r.EndedAt = &at
	}
	if body && artifact != "" {
		if len(artifact) > maxDNSEvidenceArtifact || json.Unmarshal([]byte(artifact), &r.Result) != nil {
			return r, fmt.Errorf("saved DNS evidence is unreadable")
		}
	}
	return r, nil
}
func validDNSEvidenceID(id string) bool {
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == 16 && len(id) == 32
}

func (s *Service) DNSEvidence(ctx context.Context, id string) (SavedDNSEvidence, error) {
	if !validDNSEvidenceID(id) {
		return SavedDNSEvidence{}, ErrNotFound
	}
	if _, err := s.ListDNSEvidence(ctx); err != nil {
		return SavedDNSEvidence{}, err
	}
	return scanDNSEvidence(s.db.QueryRowContext(ctx, `SELECT id,request_json,status,started_at,ended_at,artifact_json FROM network_dns_evidence WHERE id=?`, id), true)
}
func (s *Service) DeleteDNSEvidence(ctx context.Context, id string) error {
	if !validDNSEvidenceID(id) {
		return ErrNotFound
	}
	if s.db == nil {
		return ErrUnavailable
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM network_dns_evidence WHERE id=? AND status!='running'`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
