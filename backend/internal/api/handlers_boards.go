package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

const emptyBoardScene = `{"elements":[],"appState":{},"files":{}}`
const maxBoardSceneBytes = 16 << 20

type boardSummary struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type boardDetail struct {
	boardSummary
	Scene json.RawMessage `json:"scene"`
}

func (s *Server) mountBoardRoutes(r chi.Router) {
	r.Route("/boards", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleBoardList))
		r.Method(http.MethodGet, "/{id}", s.handle(s.handleBoardGet))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapServiceControl))
			r.Method(http.MethodPost, "/", s.handle(s.handleBoardCreate))
			r.Method(http.MethodPut, "/{id}", s.handle(s.handleBoardPut))
		})
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{id}", s.handle(s.handleBoardDelete))
		})
	})
}

func (s *Server) handleBoardList(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.Store.DB.QueryContext(r.Context(),
		`SELECT id, name, revision, created_at, updated_at FROM boards ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return httpx.Internal(err)
	}
	defer rows.Close()
	boards := []boardSummary{}
	for rows.Next() {
		var b boardSummary
		var created, updated int64
		if err := rows.Scan(&b.ID, &b.Name, &b.Revision, &created, &updated); err != nil {
			return httpx.Internal(err)
		}
		b.CreatedAt = time.Unix(created, 0).UTC()
		b.UpdatedAt = time.Unix(updated, 0).UTC()
		boards = append(boards, b)
	}
	if err := rows.Err(); err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, boards)
	return nil
}

func (s *Server) handleBoardGet(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var b boardDetail
	var created, updated int64
	var scene string
	err = s.Store.DB.QueryRowContext(r.Context(),
		`SELECT id, name, scene, revision, created_at, updated_at FROM boards WHERE id = ?`, id,
	).Scan(&b.ID, &b.Name, &scene, &b.Revision, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return httpx.Err(http.StatusNotFound, "board_not_found", "board not found")
	}
	if err != nil {
		return httpx.Internal(err)
	}
	b.CreatedAt = time.Unix(created, 0).UTC()
	b.UpdatedAt = time.Unix(updated, 0).UTC()
	b.Scene = json.RawMessage(scene)
	httpx.JSON(w, http.StatusOK, b)
	return nil
}

func boardName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 100 || strings.ContainsRune(name, 0) {
		return "", httpx.BadRequest("board name must be between 1 and 100 characters")
	}
	return name, nil
}

func (s *Server) handleBoardCreate(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name string `json:"name"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	name, err := boardName(req.Name)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	result, err := s.Store.DB.ExecContext(r.Context(),
		`INSERT INTO boards(name, scene, created_at, updated_at) VALUES(?,?,?,?)`,
		name, emptyBoardScene, now, now)
	if err != nil {
		return httpx.Internal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "board.create", name, nil)
	httpx.JSON(w, http.StatusCreated, boardDetail{
		boardSummary: boardSummary{ID: id, Name: name, Revision: 1,
			CreatedAt: time.Unix(now, 0).UTC(), UpdatedAt: time.Unix(now, 0).UTC()},
		Scene: json.RawMessage(emptyBoardScene),
	})
	return nil
}

func decodeBoardPut(w http.ResponseWriter, r *http.Request, dst any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return httpx.Err(http.StatusUnsupportedMediaType, "json_content_type_required", "request body must use application/json")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBoardSceneBytes+(1<<20)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return httpx.BadRequest("malformed board: %v", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return httpx.BadRequest("board request must contain one JSON object")
	}
	return nil
}

func validBoardScene(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || len(raw) > maxBoardSceneBytes || trimmed[0] != '{' {
		return false
	}
	var scene struct {
		Elements json.RawMessage `json:"elements"`
		AppState json.RawMessage `json:"appState"`
		Files    json.RawMessage `json:"files"`
	}
	if json.Unmarshal(raw, &scene) != nil {
		return false
	}
	return len(scene.Elements) > 0 && bytes.TrimSpace(scene.Elements)[0] == '[' &&
		len(scene.AppState) > 0 && bytes.TrimSpace(scene.AppState)[0] == '{' &&
		len(scene.Files) > 0 && bytes.TrimSpace(scene.Files)[0] == '{'
}

func (s *Server) handleBoardPut(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req struct {
		Name     string          `json:"name"`
		Scene    json.RawMessage `json:"scene"`
		Revision int64           `json:"revision"`
	}
	if err := decodeBoardPut(w, r, &req); err != nil {
		return err
	}
	name, err := boardName(req.Name)
	if err != nil {
		return err
	}
	if req.Revision < 1 || !validBoardScene(req.Scene) {
		return httpx.BadRequest("board revision and scene are required; scene must contain elements, appState, and files")
	}
	now := time.Now().Unix()
	result, err := s.Store.DB.ExecContext(r.Context(),
		`UPDATE boards SET name = ?, scene = ?, revision = revision + 1, updated_at = ?
		 WHERE id = ? AND revision = ?`, name, string(req.Scene), now, id, req.Revision)
	if err != nil {
		return httpx.Internal(err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return httpx.Internal(err)
	}
	if changed == 0 {
		var exists int
		if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT 1 FROM boards WHERE id = ?`, id).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
			return httpx.Err(http.StatusNotFound, "board_not_found", "board not found")
		} else if err != nil {
			return httpx.Internal(err)
		}
		return httpx.Err(http.StatusConflict, "board_conflict", "this board changed in another tab; reload it before saving")
	}
	var created int64
	if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT created_at FROM boards WHERE id = ?`, id).Scan(&created); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "board.save", name, nil)
	httpx.JSON(w, http.StatusOK, boardSummary{ID: id, Name: name, Revision: req.Revision + 1,
		CreatedAt: time.Unix(created, 0).UTC(), UpdatedAt: time.Unix(now, 0).UTC()})
	return nil
}

func (s *Server) handleBoardDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var name string
	if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT name FROM boards WHERE id = ?`, id).Scan(&name); errors.Is(err, sql.ErrNoRows) {
		return httpx.Err(http.StatusNotFound, "board_not_found", "board not found")
	} else if err != nil {
		return httpx.Internal(err)
	}
	if err := httpx.RequireTypedConfirmation(w, r, name); err != nil {
		return err
	}
	if _, err := s.Store.DB.ExecContext(r.Context(), `DELETE FROM boards WHERE id = ?`, id); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "board.delete", name, nil)
	httpx.NoContent(w)
	return nil
}
