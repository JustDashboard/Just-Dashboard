package metrics

import (
	"context"
	"database/sql"
	"strings"
)

// A container's id is sixty-four characters, and every sample of a container
// used to carry it, in the row and again in the index that finds a release's
// samples: about half of the largest table in the store was the same few
// strings, written every interval. An id is now written once, in
// metric_container_ids, and a sample holds the number of that row
// (container_ref) with container_id left empty.
//
// Nothing was rewritten to get there. A sample recorded before still spells
// its id out and is read as it always was; it leaves with the retention like
// any other, so after JD_METRICS_RETENTION the table holds only the short
// form. Whatever reads a sample's identity reads both spellings through the
// three fragments below, so a container whose samples straddle the upgrade is
// one series with the numbers it had.

const (
	// containerIdentityJoin makes a sample's written-down id available as i;
	// the sample table must be aliased s.
	containerIdentityJoin = ` LEFT JOIN metric_container_ids i ON i.id = s.container_ref `
	// containerIdentity is the id a sample was taken from, however the row
	// spells it: empty for a row from before ids were recorded at all.
	containerIdentity = `COALESCE(i.container_id, s.container_id)`
	// containerIDCacheLimit bounds the ids the recorder remembers the numbers
	// of. Every redeploy brings new ids; past the limit it forgets them all
	// and asks the store again.
	containerIDCacheLimit = 4096
)

// containerIdentityIn is the condition that picks the samples taken from a set
// of containers, in either spelling. marks are numbered parameters ("?3"),
// which is what lets each be named twice. The two halves are served by the
// index on (container_id, ts) and the one on (container_ref, ts);
// TestIdentityReadsUseAnIndexForBothSpellings holds that.
func containerIdentityIn(marks []string) string {
	list := strings.Join(marks, ",")
	return `(s.container_id IN (` + list + `) OR s.container_ref IN (
		SELECT id FROM metric_container_ids WHERE container_id IN (` + list + `)))`
}

// containerRef is the number a container's id is written down under, writing
// it down first when the id is new. It runs inside the transaction that
// stores the sample, so a number is never handed out for an id the store does
// not hold; resolved collects what this transaction found, for the caller to
// remember once it has committed. The caller holds idsMu.
func (r *Recorder) containerRef(ctx context.Context, tx *sql.Tx, id string, resolved map[string]int64) (any, error) {
	if id == "" {
		return nil, nil
	}
	if ref, ok := r.ids[id]; ok {
		return ref, nil
	}
	if ref, ok := resolved[id]; ok {
		return ref, nil
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO metric_container_ids(container_id) VALUES (?) ON CONFLICT(container_id) DO NOTHING`, id); err != nil {
		return nil, err
	}
	var ref int64
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM metric_container_ids WHERE container_id = ?`, id).Scan(&ref); err != nil {
		return nil, err
	}
	resolved[id] = ref
	return ref, nil
}

// rememberContainerRefs keeps what a committed transaction resolved. The
// caller holds idsMu.
func (r *Recorder) rememberContainerRefs(resolved map[string]int64) {
	if r.ids == nil || len(r.ids)+len(resolved) > containerIDCacheLimit {
		r.ids = map[string]int64{}
	}
	for id, ref := range resolved {
		r.ids[id] = ref
	}
}

// pruneContainerIDs forgets the ids no sample refers to any longer, which is
// every container replaced more than a retention ago. What the recorder
// remembers goes with them, under the lock a write holds, so a number is
// never used after its row has gone.
func (r *Recorder) pruneContainerIDs(ctx context.Context) error {
	r.idsMu.Lock()
	defer r.idsMu.Unlock()
	r.ids = nil
	_, err := r.samples().ExecContext(ctx, `
		DELETE FROM metric_container_ids
		 WHERE NOT EXISTS (SELECT 1 FROM metric_container_samples s
		                    WHERE s.container_ref = metric_container_ids.id)`)
	return err
}
