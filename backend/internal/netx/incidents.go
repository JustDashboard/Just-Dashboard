package netx

import (
	"context"
	"database/sql"
	"strconv"
	"time"
)

// Incident is one attention finding over time, as the Overview's readings saw
// it. History exists only for the moments the Overview was read — by a page,
// or by the server's own five-minute schedule: between two reads nothing was
// observed, and a finding that came and went unread is not in it.
type Incident struct {
	ID         int64      `json:"id"`
	FindingID  string     `json:"findingId"`
	Source     string     `json:"source"`
	Level      string     `json:"level"`
	Title      string     `json:"title"`
	Detail     string     `json:"detail"`
	Href       string     `json:"href"`
	OpenedAt   time.Time  `json:"openedAt"`
	LastSeenAt time.Time  `json:"lastSeenAt"`
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
	// UnobservedSince is when the reading this incident is judged from began
	// failing. An unobserved incident stays open: its absence is unknown.
	UnobservedSince *time.Time `json:"unobservedSince,omitempty"`
	// Related are the other incidents that began within the correlation
	// window of this one — a carrier lost and the errors and drops beside it.
	Related []int64 `json:"related"`
}

// incidentWindow is how close two openings must be to be read as one event.
const incidentWindow = 2 * time.Minute

// The bounds of what is kept: a month of resolved incidents, at most this many.
const (
	incidentRetention = 30 * 24 * time.Hour
	incidentKeep      = 1000
)

// RecordFindings folds one Overview reading into the incident history. A
// finding seen again extends its incident; a new one opens an incident,
// correlated with any other opened within the window; an open incident whose
// finding is gone resolves only when its own reading succeeded, and is marked
// unobserved when it failed.
func (s *Service) RecordFindings(ctx context.Context, now time.Time, findings []Finding, observations []Observation) error {
	if s.db == nil {
		return nil
	}
	s.incidentMu.Lock()
	defer s.incidentMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	type openIncident struct {
		id         int64
		source     string
		unobserved int64
	}
	open := map[string]openIncident{}
	rows, err := tx.QueryContext(ctx, `SELECT id, finding_id, source, unobserved_since FROM network_incidents WHERE resolved_at = 0`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var finding string
		var inc openIncident
		if err := rows.Scan(&inc.id, &finding, &inc.source, &inc.unobserved); err != nil {
			rows.Close()
			return err
		}
		open[finding] = inc
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	at := now.Unix()
	seen := map[string]bool{}
	for _, f := range findings {
		seen[f.ID] = true
		if inc, ok := open[f.ID]; ok {
			if _, err := tx.ExecContext(ctx, `UPDATE network_incidents SET level=?, title=?, detail=?, href=?, last_seen_at=?, unobserved_since=0 WHERE id=?`,
				f.Level, f.Title, f.Detail, f.Href, at, inc.id); err != nil {
				return err
			}
			continue
		}
		var correlation string
		err := tx.QueryRowContext(ctx, `SELECT correlation FROM network_incidents WHERE opened_at >= ? AND correlation != '' ORDER BY opened_at, id LIMIT 1`,
			now.Add(-incidentWindow).Unix()).Scan(&correlation)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO network_incidents(finding_id, source, level, title, detail, href, opened_at, last_seen_at, correlation) VALUES(?,?,?,?,?,?,?,?,?)`,
			f.ID, f.Source, f.Level, f.Title, f.Detail, f.Href, at, at, correlation)
		if err != nil {
			return err
		}
		if correlation == "" {
			id, err := res.LastInsertId()
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE network_incidents SET correlation=? WHERE id=?`, strconv.FormatInt(id, 10), id); err != nil {
				return err
			}
		}
	}
	for finding, inc := range open {
		if seen[finding] {
			continue
		}
		if ObservationFailed(observations, inc.source) {
			if inc.unobserved == 0 {
				if _, err := tx.ExecContext(ctx, `UPDATE network_incidents SET unobserved_since=? WHERE id=?`, at, inc.id); err != nil {
					return err
				}
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE network_incidents SET resolved_at=? WHERE id=?`, at, inc.id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM network_incidents WHERE resolved_at > 0 AND (resolved_at < ? OR id NOT IN (SELECT id FROM network_incidents ORDER BY opened_at DESC, id DESC LIMIT ?))`,
		now.Add(-incidentRetention).Unix(), incidentKeep); err != nil {
		return err
	}
	return tx.Commit()
}

// Incidents is the history, newest first, bounded to limit.
func (s *Service) Incidents(ctx context.Context, limit int) ([]Incident, error) {
	out := []Incident{}
	if s.db == nil {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, finding_id, source, level, title, detail, href, opened_at, last_seen_at, resolved_at, unobserved_since, correlation
		FROM network_incidents ORDER BY opened_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := map[string][]int64{}
	correlations := []string{}
	for rows.Next() {
		var inc Incident
		var opened, last, resolved, unobserved int64
		var correlation string
		if err := rows.Scan(&inc.ID, &inc.FindingID, &inc.Source, &inc.Level, &inc.Title, &inc.Detail, &inc.Href,
			&opened, &last, &resolved, &unobserved, &correlation); err != nil {
			return nil, err
		}
		inc.OpenedAt, inc.LastSeenAt = time.Unix(opened, 0).UTC(), time.Unix(last, 0).UTC()
		if resolved > 0 {
			t := time.Unix(resolved, 0).UTC()
			inc.ResolvedAt = &t
		}
		if unobserved > 0 {
			t := time.Unix(unobserved, 0).UTC()
			inc.UnobservedSince = &t
		}
		groups[correlation] = append(groups[correlation], inc.ID)
		correlations = append(correlations, correlation)
		out = append(out, inc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Related = []int64{}
		for _, id := range groups[correlations[i]] {
			if id != out[i].ID {
				out[i].Related = append(out[i].Related, id)
			}
		}
	}
	return out, nil
}
