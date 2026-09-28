package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// mountProxyFindingRoutes keeps the overview's snoozed findings. Reading
// them is everyone's, since a finding hidden for one account is hidden for
// all of them; snoozing one is the operator's, and taking a snooze back sits
// behind destructive like every other removal.
func (s *Server) mountProxyFindingRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/snoozes", s.handle(s.handleFindingSnoozes))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPut, "/snoozes", s.handle(s.handleFindingSnooze))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/snoozes", s.handle(s.handleFindingUnsnooze))
		})
	})
}

type findingSnooze struct {
	FindingID   string `json:"findingId"`
	Fingerprint string `json:"fingerprint"`
	// Until is absent for a snooze that lasts until the finding changes.
	Until     *time.Time `json:"until,omitempty"`
	Note      string     `json:"note,omitempty"`
	Actor     string     `json:"actor"`
	CreatedAt time.Time  `json:"createdAt"`
}

// findingSnoozeFor is how long a snooze lasts, by the name the overview's
// buttons send.
var findingSnoozeFor = map[string]time.Duration{
	"day":    24 * time.Hour,
	"week":   7 * 24 * time.Hour,
	"change": 0,
}

func (s *Server) handleFindingSnoozes(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.Store.DB.QueryContext(r.Context(),
		`SELECT finding_id, fingerprint, until, note, actor, created_at FROM proxy_finding_snoozes
		 WHERE until = 0 OR until > ? ORDER BY created_at DESC`, time.Now().Unix())
	if err != nil {
		return httpx.Internal(err)
	}
	defer rows.Close()
	out := []findingSnooze{}
	for rows.Next() {
		var sn findingSnooze
		var until, created int64
		if err := rows.Scan(&sn.FindingID, &sn.Fingerprint, &until, &sn.Note, &sn.Actor, &created); err != nil {
			return httpx.Internal(err)
		}
		if until > 0 {
			t := time.Unix(until, 0).UTC()
			sn.Until = &t
		}
		sn.CreatedAt = time.Unix(created, 0).UTC()
		out = append(out, sn)
	}
	if err := rows.Err(); err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

type findingSnoozeRequest struct {
	FindingID   string `json:"findingId"`
	Fingerprint string `json:"fingerprint"`
	// Level is the finding's as the overview judged it; a critical one is
	// put aside for a day at most.
	Level string `json:"level"`
	For   string `json:"for"`
	Note  string `json:"note"`
}

func (s *Server) handleFindingSnooze(w http.ResponseWriter, r *http.Request) error {
	var in findingSnoozeRequest
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	in.Note = strings.TrimSpace(in.Note)
	if in.FindingID == "" || len(in.FindingID) > 1024 || in.Fingerprint == "" || len(in.Fingerprint) > 4096 {
		return httpx.BadRequest("a snooze names a finding and its fingerprint")
	}
	if len(in.Note) > 500 {
		return httpx.BadRequest("keep the note under 500 characters")
	}
	span, ok := findingSnoozeFor[in.For]
	if !ok {
		return httpx.BadRequest("snooze for a day, a week, or until it changes")
	}
	switch in.Level {
	case "critical":
		if in.For != "day" {
			return httpx.BadRequest("a critical finding snoozes for a day at most")
		}
	case "warning", "notice":
	default:
		return httpx.BadRequest("unknown finding level %q", in.Level)
	}
	now := time.Now()
	var until int64
	if span > 0 {
		until = now.Add(span).Unix()
	}
	actor := httpx.MustPrincipal(r).Username()
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return httpx.Internal(err)
	}
	defer func() { _ = tx.Rollback() }()
	// Expired rows are nothing the overview reads again.
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM proxy_finding_snoozes WHERE until > 0 AND until <= ?`, now.Unix()); err != nil {
		return httpx.Internal(err)
	}
	if _, err := tx.ExecContext(r.Context(),
		`INSERT INTO proxy_finding_snoozes (finding_id, fingerprint, until, note, actor, created_at) VALUES (?,?,?,?,?,?)
		 ON CONFLICT(finding_id) DO UPDATE SET fingerprint=excluded.fingerprint, until=excluded.until,
		 note=excluded.note, actor=excluded.actor, created_at=excluded.created_at`,
		in.FindingID, in.Fingerprint, until, in.Note, actor, now.Unix()); err != nil {
		return httpx.Internal(err)
	}
	if err := tx.Commit(); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "proxy.findings.snooze", in.FindingID, map[string]any{"level": in.Level, "for": in.For, "note": in.Note})
	out := findingSnooze{FindingID: in.FindingID, Fingerprint: in.Fingerprint, Note: in.Note, Actor: actor, CreatedAt: time.Unix(now.Unix(), 0).UTC()}
	if until > 0 {
		t := time.Unix(until, 0).UTC()
		out.Until = &t
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// handleFindingUnsnooze names the finding in the query: an id carries the
// file and line it is about, slashes and all, which a path segment cannot.
func (s *Server) handleFindingUnsnooze(w http.ResponseWriter, r *http.Request) error {
	id := r.URL.Query().Get("id")
	if id == "" || len(id) > 1024 {
		return httpx.BadRequest("name the finding to show again")
	}
	res, err := s.Store.DB.ExecContext(r.Context(), `DELETE FROM proxy_finding_snoozes WHERE finding_id=?`, id)
	if err != nil {
		return httpx.Internal(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.Err(http.StatusNotFound, "not_found", "That finding is not snoozed.")
	}
	httpx.SetAudit(r, "proxy.findings.unsnooze", id, nil)
	httpx.NoContent(w)
	return nil
}
