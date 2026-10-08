package netipam

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

type Inventory func(context.Context) (Snapshot, error)
type Service struct {
	db        *sql.DB
	initErr   error
	inventory Inventory
	now       func() time.Time
}

func New(st *store.Store, inventory Inventory) *Service {
	s := &Service{db: st.DB, inventory: inventory, now: func() time.Time { return time.Now().UTC() }}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.initErr = s.ReviewInterruptedHandoffs(ctx)
	return s
}
func (s *Service) Ready() error {
	if s == nil {
		return fmt.Errorf("the shared address planner is unavailable")
	}
	return s.initErr
}
func identifier() string {
	raw := make([]byte, 16)
	if _, e := rand.Read(raw); e != nil {
		panic(e)
	}
	return hex.EncodeToString(raw)
}
func timestamp(v int64) *time.Time {
	if v == 0 {
		return nil
	}
	t := time.UnixMilli(v).UTC()
	return &t
}

type scanner interface{ Scan(...any) error }

const poolColumns = `id,name,prefix,allocation_bits,created_at,retired_at`
const reservationColumns = `id,pool_id,prefix,owner,resource,state,acknowledged_unknown,unknown_sources,created_at,updated_at,released_at,started_by,native_id,detail`

func readPool(row scanner) (Pool, error) {
	var p Pool
	var created, retired int64
	e := row.Scan(&p.ID, &p.Name, &p.Prefix, &p.AllocationBits, &created, &retired)
	if e != nil {
		return p, e
	}
	prefix, e := Canonical(p.Prefix)
	if e != nil {
		return p, e
	}
	if p.AllocationBits < prefix.Bits() || p.AllocationBits > prefix.Addr().BitLen() {
		return p, fmt.Errorf("saved pool allocation width is unreadable")
	}
	p.Family = Family(prefix)
	p.CreatedAt = time.UnixMilli(created).UTC()
	p.RetiredAt = timestamp(retired)
	return p, nil
}
func readReservation(row scanner) (Reservation, error) {
	var r Reservation
	var created, updated, released int64
	var unknownJSON string
	e := row.Scan(&r.ID, &r.PoolID, &r.Prefix, &r.Owner, &r.Resource, &r.State, &r.AcknowledgedUnknown, &unknownJSON, &created, &updated, &released, &r.StartedBy, &r.NativeID, &r.Detail)
	if e != nil {
		return r, e
	}
	if e := json.Unmarshal([]byte(unknownJSON), &r.UnknownSources); e != nil {
		return r, e
	}
	p, e := Canonical(r.Prefix)
	if e != nil {
		return r, e
	}
	r.Family = Family(p)
	r.CreatedAt = time.UnixMilli(created).UTC()
	r.UpdatedAt = time.UnixMilli(updated).UTC()
	r.ReleasedAt = timestamp(released)
	return r, nil
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func listReservations(ctx context.Context, q queryer) ([]Reservation, error) {
	rows, e := q.QueryContext(ctx, `SELECT `+reservationColumns+` FROM network_ipam_reservations ORDER BY created_at DESC LIMIT ?`, MaxReservations)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Reservation{}
	for rows.Next() {
		r, e := readReservation(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func listPools(ctx context.Context, q queryer) ([]Pool, error) {
	rows, e := q.QueryContext(ctx, `SELECT `+poolColumns+` FROM network_ipam_pools ORDER BY created_at DESC LIMIT ?`, MaxPools)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Pool{}
	for rows.Next() {
		p, e := readPool(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	if e := s.Ready(); e != nil {
		return Snapshot{}, e
	}
	if s.inventory == nil {
		return Snapshot{CheckedAt: s.now(), FinishedAt: s.now(), Observations: []Observation{}, Coverage: []Coverage{{Source: "native", State: "unknown", Detail: "No native inventory adapter is available", CheckedAt: s.now()}}}, nil
	}
	snapshot, e := s.inventory(ctx)
	if e != nil {
		return Snapshot{}, e
	}
	if len(snapshot.Observations) > MaxObservations {
		snapshot.Observations = snapshot.Observations[:MaxObservations]
		snapshot.Coverage = append(snapshot.Coverage, Coverage{Source: "bounded_inventory", State: "unknown", Detail: "Native prefix inventory exceeded the retained bound", CheckedAt: s.now()})
	}
	if len(snapshot.Coverage) > 128 {
		snapshot.Coverage = snapshot.Coverage[:127]
		snapshot.Coverage = append(snapshot.Coverage, Coverage{Source: "bounded_coverage", State: "unknown", Detail: "Source coverage exceeded the retained bound", CheckedAt: s.now()})
	}
	out := snapshot.Observations[:0]
	for _, v := range snapshot.Observations {
		p, e := netip.ParsePrefix(v.Prefix)
		if e != nil || p.Addr().Is4In6() {
			snapshot.Coverage = append(snapshot.Coverage, Coverage{Source: v.Owner, State: "unreadable", Detail: "An owner returned an unreadable prefix", CheckedAt: s.now()})
			continue
		}
		v.Prefix = p.Masked().String()
		out = append(out, v)
	}
	snapshot.Observations = out
	if len(snapshot.Coverage) > 128 {
		snapshot.Coverage = append(snapshot.Coverage[:127], Coverage{Source: "bounded_coverage", State: "unknown", Detail: "Source coverage exceeded the retained bound", CheckedAt: s.now()})
	}
	return snapshot, nil
}
func (s *Service) View(ctx context.Context) (View, error) {
	snapshot, e := s.Snapshot(ctx)
	if e != nil {
		return View{}, e
	}
	pools, e := listPools(ctx, s.db)
	if e != nil {
		return View{}, e
	}
	reservations, e := listReservations(ctx, s.db)
	if e != nil {
		return View{}, e
	}
	view := View{Pools: pools, Reservations: reservations, Inventory: snapshot, Utilization: []Utilization{}, Limitations: append([]string(nil), limitations...)}
	for _, p := range pools {
		view.Utilization = append(view.Utilization, UtilizationFor(p, reservations, snapshot))
	}
	return view, nil
}
func (s *Service) Preview(ctx context.Context, prefix string) (Preview, error) {
	snapshot, e := s.Snapshot(ctx)
	if e != nil {
		return Preview{}, e
	}
	reserved, e := listReservations(ctx, s.db)
	if e != nil {
		return Preview{}, e
	}
	return PreviewPrefix(prefix, reserved, snapshot, nil)
}
func (s *Service) CreatePool(ctx context.Context, request PoolRequest) (Pool, error) {
	if e := s.Ready(); e != nil {
		return Pool{}, e
	}
	req, e := ValidatePool(request)
	if e != nil {
		return Pool{}, e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Pool{}, e
	}
	defer tx.Rollback()
	pools, e := listPools(ctx, tx)
	if e != nil {
		return Pool{}, e
	}
	if len(pools) >= MaxPools {
		return Pool{}, fmt.Errorf("at most %d retained pools are supported", MaxPools)
	}
	prefix, _ := Canonical(req.Prefix)
	for _, p := range pools {
		existing, _ := Canonical(p.Prefix)
		if p.RetiredAt == nil && existing.Overlaps(prefix) {
			return Pool{}, fmt.Errorf("the pool overlaps shared pool %s (%s)", p.Name, p.Prefix)
		}
	}
	p := Pool{ID: identifier(), Name: req.Name, Prefix: req.Prefix, Family: Family(prefix), AllocationBits: req.AllocationBits, CreatedAt: s.now()}
	if _, e = tx.ExecContext(ctx, `INSERT INTO network_ipam_pools(id,name,prefix,allocation_bits,created_at) VALUES(?,?,?,?,?)`, p.ID, p.Name, p.Prefix, p.AllocationBits, p.CreatedAt.UnixMilli()); e != nil {
		return Pool{}, e
	}
	return p, tx.Commit()
}
func (s *Service) Reserve(ctx context.Context, request ReserveRequest, actor string) (Reservation, error) {
	if !idPattern.MatchString(request.PoolID) {
		return Reservation{}, fmt.Errorf("select an existing shared pool")
	}
	if e := ValidateOwner(request.Owner, request.Resource); e != nil {
		return Reservation{}, e
	}
	snapshot, e := s.Snapshot(ctx)
	if e != nil {
		return Reservation{}, e
	}
	if unknown(snapshot) && !request.AcknowledgeUnknown {
		return Reservation{}, fmt.Errorf("native/provider coverage is incomplete; explicitly acknowledge the planning uncertainty")
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Reservation{}, e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `DELETE FROM network_ipam_reservations WHERE state='released' AND released_at<?`, s.now().Add(-ReleasedRetention).UnixMilli()); e != nil {
		return Reservation{}, e
	}
	pool, e := readPool(tx.QueryRowContext(ctx, `SELECT `+poolColumns+` FROM network_ipam_pools WHERE id=? AND retired_at=0`, request.PoolID))
	if e != nil {
		return Reservation{}, fmt.Errorf("pool is absent, retired or unreadable")
	}
	reservations, e := listReservations(ctx, tx)
	if e != nil {
		return Reservation{}, e
	}
	if len(reservations) >= MaxReservations {
		return Reservation{}, fmt.Errorf("the retained reservation limit is %d", MaxReservations)
	}
	p, _ := Canonical(pool.Prefix)
	reserved, observed := blockers(p, reservations, snapshot, nil)
	var candidate netip.Prefix
	if request.Prefix == "" {
		candidate, e = FirstFit(p, pool.AllocationBits, append(reserved, observed...))
	} else {
		candidate, e = Canonical(request.Prefix)
		if e == nil && (!p.Contains(candidate.Addr()) || candidate.Bits() != pool.AllocationBits || Family(candidate) != pool.Family) {
			e = fmt.Errorf("the exact reservation must fit the pool's /%d allocation", pool.AllocationBits)
		}
	}
	if e != nil {
		return Reservation{}, e
	}
	preview, e := PreviewPrefix(candidate.String(), reservations, snapshot, nil)
	if e != nil {
		return Reservation{}, e
	}
	if len(preview.Conflicts) > 0 {
		return Reservation{}, fmt.Errorf("the reservation overlaps known %s %s (%s)", preview.Conflicts[0].Owner, preview.Conflicts[0].Resource, preview.Conflicts[0].Prefix)
	}
	now := s.now()
	r := Reservation{ID: identifier(), PoolID: pool.ID, Prefix: candidate.String(), Family: Family(candidate), Owner: request.Owner, Resource: request.Resource, State: "reserved", AcknowledgedUnknown: unknown(snapshot), UnknownSources: unknownSources(snapshot), CreatedAt: now, UpdatedAt: now, StartedBy: actor}
	if _, e = tx.ExecContext(ctx, `INSERT INTO network_ipam_reservations(id,pool_id,prefix,owner,resource,state,acknowledged_unknown,unknown_sources,created_at,updated_at,started_by) VALUES(?,?,?,?,?,'reserved',?,?,?,?,?)`, r.ID, r.PoolID, r.Prefix, r.Owner, r.Resource, r.AcknowledgedUnknown, string(marshal(r.UnknownSources)), now.UnixMilli(), now.UnixMilli(), actor); e != nil {
		return Reservation{}, e
	}
	return r, tx.Commit()
}
func (s *Service) Release(ctx context.Context, id string) error {
	if e := s.Ready(); e != nil {
		return e
	}
	result, e := s.db.ExecContext(ctx, `UPDATE network_ipam_reservations SET state='released',released_at=?,updated_at=?,detail='Planning reservation released explicitly; native owner was not changed' WHERE id=? AND state IN ('reserved','observed','review_required')`, s.now().UnixMilli(), s.now().UnixMilli(), id)
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return fmt.Errorf("reservation is absent, already released or has an active handoff")
	}
	return nil
}
func (s *Service) RetirePool(ctx context.Context, id string) error {
	if e := s.Ready(); e != nil {
		return e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var active int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM network_ipam_reservations WHERE pool_id=? AND state<>'released'`, id).Scan(&active); e != nil {
		return e
	}
	if active > 0 {
		return fmt.Errorf("release active planning reservations before retiring the pool")
	}
	result, e := tx.ExecContext(ctx, `UPDATE network_ipam_pools SET retired_at=? WHERE id=? AND retired_at=0`, s.now().UnixMilli(), id)
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return fmt.Errorf("pool is absent or already retired")
	}
	return tx.Commit()
}

// CheckUnselectedReservations prevents an explicit native prefix from borrowing
// held planning space by omitting the reservation identity. Automatic native
// owner allocation has no requested prefix and remains outside this guarantee.
func (s *Service) CheckUnselectedReservations(ctx context.Context, prefixes []string) error {
	if e := s.Ready(); e != nil {
		return e
	}
	reservations, e := listReservations(ctx, s.db)
	if e != nil {
		return e
	}
	for _, value := range prefixes {
		p, e := Canonical(value)
		if e != nil {
			return e
		}
		for _, r := range reservations {
			if r.State == "released" {
				continue
			}
			reserved, e := Canonical(r.Prefix)
			if e != nil {
				return e
			}
			if p.Overlaps(reserved) {
				return fmt.Errorf("the requested prefix overlaps a held planning reservation; select its exact reservation or review the native owner before releasing the plan")
			}
		}
	}
	return nil
}

// BeginHandoff claims only the planning rows. Native validation, ownership,
// authorization and audited mutation remain in the caller's existing owner.
func (s *Service) BeginHandoff(ctx context.Context, ids []string, owner, resource string, prefixes []string) (Handoff, error) {
	if len(ids) == 0 || len(ids) > 16 {
		return Handoff{}, fmt.Errorf("provide one to sixteen active reservation identities")
	}
	if e := ValidateOwner(owner, resource); e != nil {
		return Handoff{}, e
	}
	snapshot, e := s.Snapshot(ctx)
	if e != nil {
		return Handoff{}, e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Handoff{}, e
	}
	defer tx.Rollback()
	reservations, e := listReservations(ctx, tx)
	if e != nil {
		return Handoff{}, e
	}
	selected := map[string]bool{}
	tuple := map[string]bool{}
	for _, value := range prefixes {
		p, e := Canonical(value)
		if e != nil {
			return Handoff{}, e
		}
		tuple[p.String()] = true
	}
	for _, id := range ids {
		if !idPattern.MatchString(id) || selected[id] {
			return Handoff{}, fmt.Errorf("invalid or repeated reservation identity")
		}
		var r *Reservation
		for i := range reservations {
			if reservations[i].ID == id {
				r = &reservations[i]
				break
			}
		}
		if r == nil || r.State != "reserved" || r.Owner != owner || r.Resource != resource || !tuple[r.Prefix] {
			return Handoff{}, fmt.Errorf("reservation is absent, not reserved or belongs to another exact owner/name/prefix")
		}
		if !acknowledgedCoverage(snapshot, r.UnknownSources) {
			return Handoff{}, fmt.Errorf("inventory coverage changed since reservation; return to IPAM to review the uncertainty")
		}
		selected[id] = true
	}
	for _, prefix := range prefixes {
		preview, e := PreviewPrefix(prefix, reservations, snapshot, selected)
		if e != nil {
			return Handoff{}, e
		}
		if len(preview.Conflicts) > 0 {
			return Handoff{}, fmt.Errorf("current overlap with %s %s (%s) prevents this handoff", preview.Conflicts[0].Owner, preview.Conflicts[0].Resource, preview.Conflicts[0].Prefix)
		}
	}
	h := Handoff{ID: identifier(), ReservationIDs: append([]string(nil), ids...), Owner: owner, Resource: resource}
	for _, id := range ids {
		if _, e = tx.ExecContext(ctx, `UPDATE network_ipam_reservations SET state='handing_off',handoff_id=?,updated_at=?,detail='Native owner response has not been recorded; allocation remains held' WHERE id=? AND state='reserved'`, h.ID, s.now().UnixMilli(), id); e != nil {
			return Handoff{}, e
		}
	}
	return h, tx.Commit()
}
func (s *Service) FinishHandoff(ctx context.Context, h Handoff, nativeID string, ownerErr error) error {
	state, detail := "observed", "The native owner returned this identity; reservation remains planning metadata"
	if ownerErr != nil || nativeID == "" {
		state = "review_required"
		detail = "Native outcome is unavailable; allocation remains held and needs explicit owner review"
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, id := range h.ReservationIDs {
		result, e := tx.ExecContext(ctx, `UPDATE network_ipam_reservations SET state=?,native_id=?,detail=?,updated_at=? WHERE id=? AND state='handing_off' AND handoff_id=?`, state, nativeID, detail, s.now().UnixMilli(), id, h.ID)
		if e != nil {
			return e
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return fmt.Errorf("handoff identity changed; reservation remains held")
		}
	}
	return tx.Commit()
}

// ReviewInterruptedHandoffs is called once at backend startup. It neither
// retries native work nor releases a possibly applied allocation.
func (s *Service) ReviewInterruptedHandoffs(ctx context.Context) error {
	_, e := s.db.ExecContext(ctx, `UPDATE network_ipam_reservations SET state='review_required',updated_at=?,detail='Dashboard restarted before recording the native outcome; allocation remains held' WHERE state='handing_off'`, s.now().UnixMilli())
	return e
}

func marshal(value any) []byte { raw, _ := json.Marshal(value); return raw }
