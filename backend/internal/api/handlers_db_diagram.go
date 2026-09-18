package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// The saved arrangement of a connection's schema diagram.
//
// A diagram somebody has spent ten minutes arranging is worth exactly as much
// as its next visit, and until this existed the positions lived in component
// state and were gone the moment the tab was left. They are kept here, per
// connection and schema, beside the saved queries that outlive a page for the
// same reason. The server treats the document as opaque beyond two checks — it
// is a JSON object, and it is under a size cap — because every field in it is
// a decision about a picture, and the diagram is the only thing that decodes
// it. A schema-aware validator here would be a second copy of the diagram's
// own format, drifting from the first.

// maxDiagramLayoutBytes bounds one saved document. A hundred and twenty tables
// with a position, a note and a colour each is a few tens of kilobytes; the
// cap is an order of magnitude above that so it is never met by honest use.
const maxDiagramLayoutBytes = 512 << 10

type diagramLayoutResponse struct {
	// Layout is the document exactly as it was saved, or JSON null when the
	// diagram has never been arranged.
	Layout    json.RawMessage `json:"layout"`
	UpdatedAt *time.Time      `json:"updatedAt,omitempty"`
}

type diagramLayoutRequest struct {
	Layout json.RawMessage `json:"layout"`
}

func (s *Server) handleDBDiagramGet(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	if _, _, err := s.dbConnRow(r.Context(), id); err != nil {
		return err
	}
	var (
		raw     string
		updated int64
	)
	err = s.Store.DB.QueryRowContext(r.Context(),
		`SELECT layout, updated_at FROM db_diagram_layouts WHERE connection_id = ? AND schema_name = ?`,
		id, r.URL.Query().Get("schema")).Scan(&raw, &updated)
	if err == sql.ErrNoRows {
		httpx.JSON(w, http.StatusOK, diagramLayoutResponse{Layout: json.RawMessage("null")})
		return nil
	}
	if err != nil {
		return httpx.Internal(err)
	}
	at := time.Unix(updated, 0).UTC()
	httpx.JSON(w, http.StatusOK, diagramLayoutResponse{Layout: json.RawMessage(raw), UpdatedAt: &at})
	return nil
}

func (s *Server) handleDBDiagramPut(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	if _, _, err := s.dbConnRow(r.Context(), id); err != nil {
		return err
	}
	var req diagramLayoutRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	layout := bytes.TrimSpace(req.Layout)
	// The decoder already proved the body is JSON; what it did not check is
	// the shape of this one field, and a saved `null` or `[]` would come back
	// on the next visit as a document the diagram cannot read.
	if len(layout) == 0 || layout[0] != '{' {
		return httpx.BadRequest("layout must be a JSON object")
	}
	if len(layout) > maxDiagramLayoutBytes {
		return httpx.BadRequest("layout is larger than %d KiB", maxDiagramLayoutBytes>>10)
	}
	schema := r.URL.Query().Get("schema")
	now := time.Now().Unix()
	if _, err := s.Store.DB.ExecContext(r.Context(),
		`INSERT INTO db_diagram_layouts(connection_id, schema_name, layout, updated_at) VALUES(?,?,?,?)
		 ON CONFLICT(connection_id, schema_name) DO UPDATE SET layout = excluded.layout, updated_at = excluded.updated_at`,
		id, schema, string(layout), now); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "database.diagram.save", diagramTarget(schema), nil)
	at := time.Unix(now, 0).UTC()
	httpx.JSON(w, http.StatusOK, diagramLayoutResponse{Layout: layout, UpdatedAt: &at})
	return nil
}

func (s *Server) handleDBDiagramDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	if _, _, err := s.dbConnRow(r.Context(), id); err != nil {
		return err
	}
	schema := r.URL.Query().Get("schema")
	if _, err := s.Store.DB.ExecContext(r.Context(),
		`DELETE FROM db_diagram_layouts WHERE connection_id = ? AND schema_name = ?`, id, schema); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "database.diagram.reset", diagramTarget(schema), nil)
	httpx.NoContent(w)
	return nil
}

// diagramTarget names the schema in the audit entry, where an empty string
// would read as a missing target rather than as the engine's default schema.
func diagramTarget(schema string) string {
	if schema == "" {
		return "default schema"
	}
	return schema
}
