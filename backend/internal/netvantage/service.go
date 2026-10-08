package netvantage

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

type Service struct {
	db      *sql.DB
	key     ed25519.PrivateKey
	initErr error
	now     func() time.Time
}

func New(st *store.Store, sealer *auth.Sealer) *Service {
	s := &Service{db: st.DB, now: func() time.Time { return time.Now().UTC() }}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		s.initErr = e
		return s
	}
	sealed, e := sealer.Seal(encode(key))
	if e != nil {
		s.initErr = e
		return s
	}
	if _, e = s.db.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES('network.probe.signing-key',?) ON CONFLICT(key) DO NOTHING`, sealed); e != nil {
		s.initErr = e
		return s
	}
	var stored string
	if e = s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='network.probe.signing-key'`).Scan(&stored); e != nil {
		s.initErr = e
		return s
	}
	plain, e := sealer.Open(stored)
	if e == nil {
		s.key, e = decode(plain)
	}
	if e != nil || len(s.key) != ed25519.PrivateKeySize {
		s.initErr = fmt.Errorf("the sealed probe signing identity is unavailable")
	}
	return s
}
func (s *Service) Ready() error {
	if s == nil {
		return fmt.Errorf("the optional controlled-vantage service is unavailable")
	}
	return s.initErr
}
func randomID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func timestamp(n int64) *time.Time {
	if n == 0 {
		return nil
	}
	t := time.UnixMilli(n).UTC()
	return &t
}

const vantageColumns = `id,name,location,placement,scopes,created_at,enrolled_at,last_seen,last_ip,revoked_at`

type scanner interface{ Scan(...any) error }

func readVantage(row scanner) (Vantage, error) {
	var v Vantage
	var scopes string
	var created, enrolled, seen, revoked int64
	e := row.Scan(&v.ID, &v.Name, &v.Location, &v.Placement, &scopes, &created, &enrolled, &seen, &v.LastIP, &revoked)
	if e == nil {
		e = json.Unmarshal([]byte(scopes), &v.Scopes)
	}
	v.CreatedAt = time.UnixMilli(created).UTC()
	v.EnrolledAt, v.LastSeen, v.RevokedAt = timestamp(enrolled), timestamp(seen), timestamp(revoked)
	return v, e
}
func (s *Service) CreateEnrollment(ctx context.Context, input EnrollmentRequest) (Enrollment, error) {
	if e := s.Ready(); e != nil {
		return Enrollment{}, e
	}
	if e := s.expire(ctx); e != nil {
		return Enrollment{}, e
	}
	in, e := ValidateEnrollment(input)
	if e != nil {
		return Enrollment{}, e
	}
	now := s.now()
	v := Vantage{ID: randomID(), Name: in.Name, Location: in.Location, Placement: in.Placement, Scopes: in.Scopes, CreatedAt: now}
	token := randomID() + randomID()
	expires := now.Add(10 * time.Minute)
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Enrollment{}, e
	}
	defer tx.Rollback()
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM network_probe_vantages`).Scan(&count); e != nil {
		return Enrollment{}, e
	}
	if count >= 32 {
		return Enrollment{}, fmt.Errorf("the retained controlled-vantage limit is 32; revoke and wait for retention before enrolling another")
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO network_probe_vantages(id,name,location,placement,scopes,enrollment_hash,enrollment_expires,created_at) VALUES(?,?,?,?,?,?,?,?)`, v.ID, v.Name, v.Location, v.Placement, string(Marshal(v.Scopes)), hash(token), expires.UnixMilli(), now.UnixMilli())
	if e != nil {
		return Enrollment{}, e
	}
	if e = tx.Commit(); e != nil {
		return Enrollment{}, e
	}
	return Enrollment{Vantage: v, Token: token, ExpiresAt: expires, ServerKey: publicKey(s.key)}, nil
}
func (s *Service) Claim(ctx context.Context, c Claim) (Manifest, error) {
	if e := s.Ready(); e != nil {
		return Manifest{}, e
	}
	pub, e := decode(c.PublicKey)
	proof, er := decode(c.Proof)
	if c.ServerKey != publicKey(s.key) || !identityPattern.MatchString(c.ID) || len(c.Token) != 64 || e != nil || er != nil || len(pub) != 32 || !ed25519.Verify(pub, ClaimMessage(c), proof) {
		return Manifest{}, fmt.Errorf("enrollment proof is invalid")
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Manifest{}, e
	}
	defer tx.Rollback()
	var token string
	var expiry, enrolled, revoked int64
	if e = tx.QueryRowContext(ctx, `SELECT enrollment_hash,enrollment_expires,enrolled_at,revoked_at FROM network_probe_vantages WHERE id=?`, c.ID).Scan(&token, &expiry, &enrolled, &revoked); e != nil {
		return Manifest{}, fmt.Errorf("enrollment is unavailable")
	}
	now := s.now()
	if enrolled != 0 || revoked != 0 || expiry <= now.UnixMilli() || subtle.ConstantTimeCompare([]byte(token), []byte(hash(c.Token))) != 1 {
		return Manifest{}, fmt.Errorf("enrollment is used, expired or invalid")
	}
	result, e := tx.ExecContext(ctx, `UPDATE network_probe_vantages SET public_key=?,enrolled_at=?,enrollment_hash=?,enrollment_expires=0 WHERE id=? AND enrolled_at=0 AND revoked_at=0 AND enrollment_hash=?`, c.PublicKey, now.UnixMilli(), "used:"+c.ID, c.ID, token)
	if e != nil {
		return Manifest{}, e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return Manifest{}, fmt.Errorf("enrollment was consumed")
	}
	v, e := readVantage(tx.QueryRowContext(ctx, `SELECT `+vantageColumns+` FROM network_probe_vantages WHERE id=?`, c.ID))
	if e != nil {
		return Manifest{}, e
	}
	if e = tx.Commit(); e != nil {
		return Manifest{}, e
	}
	return Manifest{Vantage: v, ServerKey: publicKey(s.key)}, nil
}

// Authenticate commits the monotonic sequence before decoding any probe
// content. A restart cannot reopen an accepted request, even if its response
// was lost. Machine signatures cannot authenticate a human feature route.
func (s *Service) Authenticate(ctx context.Context, method, path string, body []byte, signature Signature, ip string) error {
	if e := s.Ready(); e != nil {
		return e
	}
	if signature.ServerKey != publicKey(s.key) || !identityPattern.MatchString(signature.ID) || signature.Sequence < 1 || signature.Sequence > 1<<53 || len(body) > MaxBody || len(signature.Value) > 128 {
		return fmt.Errorf("machine request identity is invalid")
	}
	if delta := s.now().Unix() - signature.Timestamp; delta > 60 || delta < -60 {
		return fmt.Errorf("machine request timestamp is outside the 60-second window")
	}
	var raw string
	if e := s.db.QueryRowContext(ctx, `SELECT public_key FROM network_probe_vantages WHERE id=? AND enrolled_at>0 AND revoked_at=0`, signature.ID).Scan(&raw); e != nil {
		return fmt.Errorf("machine identity is unavailable")
	}
	pub, e := decode(raw)
	sig, er := decode(signature.Value)
	if e != nil || er != nil || len(pub) != 32 || !ed25519.Verify(pub, RequestMessage(method, path, body, signature), sig) {
		return fmt.Errorf("machine request signature is invalid")
	}
	result, e := s.db.ExecContext(ctx, `UPDATE network_probe_vantages SET sequence=?,last_seen=?,last_ip=? WHERE id=? AND sequence<? AND revoked_at=0 AND enrolled_at>0`, signature.Sequence, s.now().UnixMilli(), ip, signature.ID, signature.Sequence)
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return fmt.Errorf("machine request was replayed or the identity was revoked")
	}
	return nil
}
func (s *Service) Vantages(ctx context.Context) ([]Vantage, error) {
	if e := s.Ready(); e != nil {
		return nil, e
	}
	if e := s.expire(ctx); e != nil {
		return nil, e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT `+vantageColumns+` FROM network_probe_vantages ORDER BY created_at DESC LIMIT 32`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Vantage{}
	for rows.Next() {
		v, e := readVantage(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) Revoke(ctx context.Context, id string) error {
	if e := s.Ready(); e != nil {
		return e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	res, e := tx.ExecContext(ctx, `UPDATE network_probe_vantages SET revoked_at=? WHERE id=? AND revoked_at=0`, s.now().UnixMilli(), id)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return fmt.Errorf("vantage is absent or already revoked")
	}
	_, e = tx.ExecContext(ctx, `UPDATE network_probe_checks SET status='cancelled',completed_at=? WHERE vantage_id=? AND status IN ('queued','running')`, s.now().UnixMilli(), id)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Service) expire(ctx context.Context) error {
	now := s.now()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `UPDATE network_probe_checks SET status='expired',completed_at=? WHERE expires_at<=? AND status IN ('queued','running')`, now.UnixMilli(), now.UnixMilli()); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM network_probe_checks WHERE created_at<? AND status NOT IN ('queued','running')`, now.Add(-Retention).UnixMilli()); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM network_probe_vantages WHERE (revoked_at>0 AND revoked_at<? OR enrolled_at=0 AND enrollment_expires<?) AND NOT EXISTS(SELECT 1 FROM network_probe_checks WHERE vantage_id=network_probe_vantages.id)`, now.Add(-Retention).UnixMilli(), now.Add(-Retention).UnixMilli()); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Service) CreateCheck(ctx context.Context, r Request, actor string) (Check, error) {
	if e := s.Ready(); e != nil {
		return Check{}, e
	}
	if e := s.expire(ctx); e != nil {
		return Check{}, e
	}
	if !identityPattern.MatchString(r.VantageID) {
		return Check{}, fmt.Errorf("select an enrolled controlled vantage")
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Check{}, e
	}
	defer tx.Rollback()
	v, e := readVantage(tx.QueryRowContext(ctx, `SELECT `+vantageColumns+` FROM network_probe_vantages WHERE id=? AND enrolled_at>0 AND revoked_at=0`, r.VantageID))
	if e != nil {
		return Check{}, fmt.Errorf("vantage is not enrolled or was revoked")
	}
	if _, e = scopeFor(v.Scopes, r); e != nil {
		return Check{}, e
	}
	var total, active int
	if e = tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(vantage_id=? AND status IN ('queued','running')),0) FROM network_probe_checks`, r.VantageID).Scan(&total, &active); e != nil {
		return Check{}, e
	}
	if total >= 256 || active > 0 {
		return Check{}, fmt.Errorf("one outstanding check per vantage and at most 256 retained checks are permitted")
	}
	now := s.now()
	check := Check{ID: randomID(), VantageID: r.VantageID, Request: r, Status: "queued", CreatedAt: now, ExpiresAt: now.Add(2 * time.Minute), StartedBy: actor}
	_, e = tx.ExecContext(ctx, `INSERT INTO network_probe_checks(id,vantage_id,request,nonce,status,created_at,expires_at,started_by) VALUES(?,?,?,?,'queued',?,?,?)`, check.ID, r.VantageID, string(Marshal(r)), randomID(), now.UnixMilli(), check.ExpiresAt.UnixMilli(), actor)
	if e != nil {
		return Check{}, e
	}
	if e = tx.Commit(); e != nil {
		return Check{}, e
	}
	return check, nil
}
func (s *Service) Poll(ctx context.Context, id string) (*SignedJob, error) {
	if e := s.Ready(); e != nil {
		return nil, e
	}
	if e := s.expire(ctx); e != nil {
		return nil, e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	v, e := readVantage(tx.QueryRowContext(ctx, `SELECT `+vantageColumns+` FROM network_probe_vantages WHERE id=? AND enrolled_at>0 AND revoked_at=0`, id))
	if e != nil {
		return nil, fmt.Errorf("vantage is unavailable")
	}
	var jobID, nonce, raw string
	var expires int64
	e = tx.QueryRowContext(ctx, `SELECT id,nonce,request,expires_at FROM network_probe_checks WHERE vantage_id=? AND status='queued' ORDER BY created_at LIMIT 1`, id).Scan(&jobID, &nonce, &raw, &expires)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var r Request
	if e = json.Unmarshal([]byte(raw), &r); e != nil {
		return nil, e
	}
	scope, e := scopeFor(v.Scopes, r)
	if e != nil {
		return nil, e
	}
	now := s.now()
	deadline := now.Add(45 * time.Second)
	if time.UnixMilli(expires).Before(deadline) {
		deadline = time.UnixMilli(expires).UTC()
	}
	res, e := tx.ExecContext(ctx, `UPDATE network_probe_checks SET status='running',leased_at=?,expires_at=? WHERE id=? AND status='queued'`, now.UnixMilli(), deadline.UnixMilli(), jobID)
	if e != nil {
		return nil, e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return nil, fmt.Errorf("check was already leased")
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	job := Job{Version: 1, ID: jobID, VantageID: id, Nonce: nonce, Request: r, Scope: scope, IssuedAt: now, ExpiresAt: deadline}
	return &SignedJob{Job: job, Signature: encode(ed25519.Sign(s.key, Marshal(job)))}, nil
}

const checkColumns = `id,vantage_id,request,status,created_at,expires_at,leased_at,completed_at,result,started_by`

func readCheck(row scanner) (Check, error) {
	var c Check
	var request, result string
	var created, expires, leased, complete int64
	e := row.Scan(&c.ID, &c.VantageID, &request, &c.Status, &created, &expires, &leased, &complete, &result, &c.StartedBy)
	if e == nil {
		e = json.Unmarshal([]byte(request), &c.Request)
	}
	if e == nil && result != "" {
		e = json.Unmarshal([]byte(result), &c.Result)
	}
	c.CreatedAt, c.ExpiresAt = time.UnixMilli(created).UTC(), time.UnixMilli(expires).UTC()
	c.LeasedAt, c.CompletedAt = timestamp(leased), timestamp(complete)
	return c, e
}
func (s *Service) Checks(ctx context.Context) ([]Check, error) {
	if e := s.Ready(); e != nil {
		return nil, e
	}
	if e := s.expire(ctx); e != nil {
		return nil, e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT `+checkColumns+` FROM network_probe_checks ORDER BY created_at DESC LIMIT 256`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Check{}
	for rows.Next() {
		c, e := readCheck(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Service) Complete(ctx context.Context, id string, result Result) error {
	if e := s.Ready(); e != nil {
		return e
	}
	if result.VantageID != id || result.Request.VantageID != id || !identityPattern.MatchString(result.CheckID) {
		return fmt.Errorf("result identity does not match the authenticated vantage")
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var nonce string
	c, e := readCheck(tx.QueryRowContext(ctx, `SELECT `+checkColumns+` FROM network_probe_checks WHERE id=? AND vantage_id=?`, result.CheckID, id))
	if e != nil {
		return fmt.Errorf("the leased check is unavailable")
	}
	if e = tx.QueryRowContext(ctx, `SELECT nonce FROM network_probe_checks WHERE id=?`, c.ID).Scan(&nonce); e != nil {
		return e
	}
	if c.Status != "running" || !c.ExpiresAt.After(s.now()) || c.LeasedAt == nil || result.Nonce != nonce || string(Marshal(c.Request)) != string(Marshal(result.Request)) {
		return fmt.Errorf("result is stale, replayed or belongs to another job tuple")
	}
	v, e := readVantage(tx.QueryRowContext(ctx, `SELECT `+vantageColumns+` FROM network_probe_vantages WHERE id=? AND revoked_at=0`, id))
	if e != nil {
		return fmt.Errorf("vantage was revoked")
	}
	scope, e := scopeFor(v.Scopes, c.Request)
	if e != nil {
		return e
	}
	if e = validateResult(result, scope, *c.LeasedAt, c.ExpiresAt, s.now()); e != nil {
		return e
	}
	status := "completed"
	for _, stage := range result.Stages {
		if stage.State == "failed" || stage.State == "unavailable" || stage.State == "refused" {
			status = "failed"
		}
	}
	res, e := tx.ExecContext(ctx, `UPDATE network_probe_checks SET result=?,status=?,completed_at=? WHERE id=? AND status='running' AND result=''`, string(Marshal(result)), status, s.now().UnixMilli(), c.ID)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return fmt.Errorf("result was already accepted")
	}
	return tx.Commit()
}
func (s *Service) Cancel(ctx context.Context, id string) error {
	if e := s.Ready(); e != nil {
		return e
	}
	result, e := s.db.ExecContext(ctx, `UPDATE network_probe_checks SET status='cancelled',completed_at=? WHERE id=? AND status IN ('queued','running')`, s.now().UnixMilli(), id)
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return fmt.Errorf("check is absent or terminal")
	}
	return nil
}
func ValidateResultTime(start, end, issued, expiry, now time.Time) bool {
	return !start.Before(issued.Add(-5*time.Second)) && !end.Before(start) && !end.After(expiry) && !end.After(now.Add(5*time.Second)) && end.Sub(start) <= 30*time.Second
}
func validateResult(r Result, s Scope, issued, expiry, now time.Time) error {
	if r.Target != s.Target || len(Marshal(r)) > MaxBody || len(r.Addresses) > 8 || len(r.Stages) != 3 || len(r.Limitations) > 12 || !ValidateResultTime(r.StartedAt, r.EndedAt, issued, expiry, now) {
		return fmt.Errorf("result scope, bounds or timestamps are invalid")
	}
	for i, stage := range r.Stages {
		if stage.Name != []string{"dns", "tcp", "tls"}[i] || len(stage.Detail) > 2048 || !ValidateResultTime(stage.StartedAt, stage.EndedAt, r.StartedAt, r.EndedAt, now) || stage.DurationMS < 0 || stage.DurationMS > 30000 {
			return fmt.Errorf("result stage is invalid")
		}
		switch stage.Basis {
		case "measured", "observed", "unknown":
		default:
			return fmt.Errorf("result evidence basis is invalid")
		}
		allowed := []map[string]bool{
			{"resolved": true, "failed": true, "unavailable": true, "refused": true, "not_applicable": true},
			{"connected": true, "failed": true, "unavailable": true, "refused": true, "skipped": true},
			{"verified": true, "failed": true, "unavailable": true, "refused": true, "skipped": true, "not_requested": true},
		}
		if !allowed[i][stage.State] {
			return fmt.Errorf("result stage state is invalid")
		}
	}
	if r.Address != "" && !approvedAddress(s, r.Address, r.Request.Family) {
		return fmt.Errorf("measured destination is outside the enrolled scope")
	}
	for _, address := range r.Addresses {
		ip, err := netip.ParseAddr(address)
		if err != nil || ip.Zone() != "" || ip.Unmap().Is4() != (r.Request.Family == "inet") {
			return fmt.Errorf("DNS evidence contains an invalid selected-family address")
		}
	}
	if r.SourceAddress != "" {
		ip, err := netip.ParseAddr(r.SourceAddress)
		if err != nil || ip.IsUnspecified() || ip.IsMulticast() || ip.Zone() != "" || ip.Unmap().Is4() != (r.Request.Family == "inet") {
			return fmt.Errorf("measured source address is invalid")
		}
	}
	if r.Stages[1].State == "connected" && (r.Address == "" || r.SourceAddress == "" || r.Stages[1].Basis != "measured") {
		return fmt.Errorf("connected TCP evidence needs an actual bounded tuple")
	}
	if r.Stages[1].State == "connected" && r.Stages[0].State != "resolved" && r.Stages[0].State != "not_applicable" {
		return fmt.Errorf("connected TCP evidence cannot follow refused or failed DNS")
	}
	if !r.Request.TLS && r.Stages[2].State != "not_requested" {
		return fmt.Errorf("TLS evidence was not requested for this job")
	}
	if r.Stages[2].State == "verified" && (!r.Request.TLS || r.Stages[1].State != "connected" || r.Stages[2].Basis != "measured" || r.Certificate == nil) {
		return fmt.Errorf("verified TLS evidence needs its measured TCP connection and certificate")
	}
	for _, lim := range r.Limitations {
		if len(lim) > 1024 {
			return fmt.Errorf("result limitation exceeds bounds")
		}
	}
	if r.Certificate != nil {
		fingerprint, err := hex.DecodeString(r.Certificate.SHA256)
		if err != nil || len(fingerprint) != 32 {
			return fmt.Errorf("certificate fingerprint is invalid")
		}
	}
	if r.Certificate != nil && (len(r.Certificate.Subject) > 256 || len(r.Certificate.Issuer) > 256 || len(r.Certificate.SHA256) != 64) {
		return fmt.Errorf("certificate evidence exceeds bounds")
	}
	if r.Certificate != nil && r.Stages[2].State != "verified" {
		return fmt.Errorf("certificate verification evidence needs a verified TLS stage")
	}
	return nil
}
func Compare(left, right Check) string {
	if left.Result == nil || right.Result == nil {
		return "A retained agent result is missing; measurement comparison is unavailable."
	}
	if left.Result.Target != right.Result.Target || left.Request.Port != right.Request.Port || left.Request.TLS != right.Request.TLS {
		return "These checks concern different service tuples and cannot establish a path change."
	}
	return "Compare each DNS, TCP and TLS stage by declared vantage, address family and collection time. Different sources/families are different paths; geography and provider causes remain unverified."
}
