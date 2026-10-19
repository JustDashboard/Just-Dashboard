package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	// Only the list fills this in; a board is opened for its scene.
	Preview json.RawMessage `json:"preview,omitempty"`
}

type boardDetail struct {
	boardSummary
	Scene json.RawMessage `json:"scene"`
	// The images stored apart from the scene (boards_files.go).
	FileIDs []string `json:"fileIds"`
}

// boardSaved is a save's answer.
type boardSaved struct {
	boardSummary
	FileIDs []string `json:"fileIds"`
	// Only a merge save answers it: the elements the saver does not hold as
	// they are now, so a tab that missed a live frame still converges.
	RemoteElements json.RawMessage `json:"remoteElements,omitempty"`
}

func (s *Server) mountBoardRoutes(r chi.Router) {
	r.Route("/boards", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleBoardList))
		// A static segment wins over {id} in chi, so "library" is never a board.
		r.Method(http.MethodGet, "/library", s.handle(s.handleBoardLibraryGet))
		r.Method(http.MethodGet, "/{id}", s.handle(s.handleBoardGet))
		r.Method(http.MethodGet, "/{id}/files/{fileId}", s.handle(s.handleBoardFileGet))
		r.Method(http.MethodGet, "/{id}/live", s.handle(s.handleBoardLive))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapServiceControl))
			r.Method(http.MethodPost, "/", s.handle(s.handleBoardCreate))
			r.Method(http.MethodPut, "/library", s.handle(s.handleBoardLibraryPut))
			r.Method(http.MethodPost, "/library/fetch", s.handle(s.handleBoardLibraryFetch))
			r.Method(http.MethodPost, "/diagram", s.handle(s.handleBoardDiagram))
			r.Method(http.MethodPut, "/{id}", s.handle(s.handleBoardPut))
			r.Method(http.MethodPut, "/{id}/files/{fileId}", s.handle(s.handleBoardFilePut))
		})
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{id}", s.handle(s.handleBoardDelete))
		})
	})
}

func (s *Server) handleBoardList(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.Store.DB.QueryContext(r.Context(),
		`SELECT id, name, revision, preview, created_at, updated_at FROM boards ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return httpx.Internal(err)
	}
	defer rows.Close()
	boards := []boardSummary{}
	for rows.Next() {
		var b boardSummary
		var preview string
		var created, updated int64
		if err := rows.Scan(&b.ID, &b.Name, &b.Revision, &preview, &created, &updated); err != nil {
			return httpx.Internal(err)
		}
		b.Preview = json.RawMessage(preview)
		b.CreatedAt = time.Unix(created, 0).UTC()
		b.UpdatedAt = time.Unix(updated, 0).UTC()
		boards = append(boards, b)
	}
	if err := rows.Err(); err != nil {
		return httpx.Internal(err)
	}
	// Close before the backfill below reads scenes: the connection pool may be
	// a single connection, and this cursor would hold it.
	if err := rows.Close(); err != nil {
		return httpx.Internal(err)
	}
	for i := range boards {
		if len(boards[i].Preview) > 0 {
			continue
		}
		preview, err := s.backfillBoardPreview(r.Context(), boards[i].ID, boards[i].Revision)
		if err != nil {
			return err
		}
		boards[i].Preview = preview
	}
	httpx.JSON(w, http.StatusOK, boards)
	return nil
}

// backfillBoardPreview derives the preview of a board saved before previews
// existed. It is not an edit: revision and updated_at stay as they were, so an
// editor open on the board does not meet a conflict on its next save. The
// revision in the WHERE keeps a save that lands meanwhile, which stores its own
// preview, from being overwritten by one of the scene it replaced.
func (s *Server) backfillBoardPreview(ctx context.Context, id, revision int64) (json.RawMessage, error) {
	var scene string
	err := s.Store.DB.QueryRowContext(ctx,
		`SELECT scene FROM boards WHERE id = ? AND revision = ?`, id, revision).Scan(&scene)
	if errors.Is(err, sql.ErrNoRows) {
		return json.RawMessage(emptyBoardPreview), nil
	}
	if err != nil {
		return nil, httpx.Internal(err)
	}
	preview := boardPreviewJSON(json.RawMessage(scene))
	if _, err := s.Store.DB.ExecContext(ctx,
		`UPDATE boards SET preview = ? WHERE id = ? AND revision = ?`, preview, id, revision); err != nil {
		return nil, httpx.Internal(err)
	}
	return json.RawMessage(preview), nil
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
	if b.FileIDs, err = boardFileIDs(r.Context(), s.Store.DB, id); err != nil {
		return err
	}
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
		`INSERT INTO boards(name, scene, preview, created_at, updated_at) VALUES(?,?,?,?,?)`,
		name, emptyBoardScene, emptyBoardPreview, now, now)
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
		Scene: json.RawMessage(emptyBoardScene), FileIDs: []string{},
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
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return errBoardTooLarge()
		}
		return httpx.BadRequest("malformed board: %v", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return httpx.BadRequest("board request must contain one JSON object")
	}
	return nil
}

// The shape error blamed a missing field for a scene that was only too big,
// which sent the operator looking for a bug instead of at their images.
func errBoardTooLarge() error {
	return httpx.Err(http.StatusRequestEntityTooLarge, "board_too_large",
		"this board is larger than 16 MiB; remove or shrink some images before saving")
}

func validBoardScene(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
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

// handleBoardPut saves a board. Everything that reads the stored board and
// writes it back happens in one write transaction, so two saves cannot read
// the same revision, and a merge cannot lose what a save between its read and
// its write added.
func (s *Server) handleBoardPut(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req struct {
		Name     string          `json:"name"`
		Scene    json.RawMessage `json:"scene"`
		Revision int64           `json:"revision"`
		Merge    bool            `json:"merge"`
	}
	if err := decodeBoardPut(w, r, &req); err != nil {
		return err
	}
	name, err := boardName(req.Name)
	if err != nil {
		return err
	}
	if len(req.Scene) > maxBoardSceneBytes {
		return errBoardTooLarge()
	}
	if req.Revision < 1 || !validBoardScene(req.Scene) {
		return httpx.BadRequest("board revision and scene are required; scene must contain elements, appState, and files")
	}
	var top map[string]json.RawMessage
	var files map[string]json.RawMessage
	if json.Unmarshal(req.Scene, &top) != nil || json.Unmarshal(top["files"], &files) != nil {
		return httpx.BadRequest("malformed board scene")
	}
	incoming, err := decodeSceneElements(top["elements"])
	if err != nil {
		return httpx.BadRequest("malformed board scene: %v", err)
	}
	if req.Merge {
		for _, e := range incoming {
			if !e.ok || e.meta.ID == "" {
				return httpx.BadRequest("every element of a merged scene must be an object with an id")
			}
		}
	}

	ctx := r.Context()
	now := time.Now()
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return httpx.Internal(err)
	}
	defer tx.Rollback()
	var storedScene string
	var revision, created int64
	err = tx.QueryRowContext(ctx, `SELECT CASE WHEN ? THEN scene ELSE '' END, revision, created_at FROM boards WHERE id = ?`,
		req.Merge, id).Scan(&storedScene, &revision, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return errBoardNotFound()
	}
	if err != nil {
		return httpx.Internal(err)
	}
	if !req.Merge && revision != req.Revision {
		return httpx.Err(http.StatusConflict, "board_conflict", "this board changed in another tab; reload it before saving")
	}

	elements := pruneTombstones(incoming, now)
	var remote []json.RawMessage
	if req.Merge {
		merged, storedFiles, err := mergeWithStored(storedScene, incoming)
		if err != nil {
			return httpx.Internal(err)
		}
		elements = pruneTombstones(merged, now)
		orderByIndex(elements)
		remote = differingFrom(elements, incoming)
		// A picture a drawing from before images moved out of scenes still
		// carries inline stays while an element names it.
		refs := fileRefs(elements)
		for fileID, entry := range storedFiles {
			if _, held := files[fileID]; !held && refs[fileID] {
				files[fileID] = entry
			}
		}
	}
	kept, err := s.extractSceneFiles(ctx, tx, id, files, now)
	if err != nil {
		return err
	}

	scene := req.Scene
	if req.Merge || len(elements) != len(incoming) || len(kept) != len(files) {
		if top["elements"], err = elementsJSON(elements); err != nil {
			return httpx.Internal(err)
		}
		if top["files"], err = json.Marshal(kept); err != nil {
			return httpx.Internal(err)
		}
		if scene, err = json.Marshal(top); err != nil {
			return httpx.Internal(err)
		}
		if len(scene) > maxBoardSceneBytes {
			return errBoardTooLarge()
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE boards SET name = ?, scene = ?, preview = ?, revision = revision + 1, updated_at = ? WHERE id = ?`,
		name, string(scene), boardPreviewJSON(scene), now.Unix(), id); err != nil {
		return httpx.Internal(err)
	}
	fileIDs, err := collectBoardFiles(ctx, tx, id, fileRefs(elements), now)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "board.save", name, nil)
	saved := boardSaved{
		boardSummary: boardSummary{ID: id, Name: name, Revision: revision + 1,
			CreatedAt: time.Unix(created, 0).UTC(), UpdatedAt: time.Unix(now.Unix(), 0).UTC()},
		FileIDs: fileIDs,
	}
	if req.Merge {
		if saved.RemoteElements, err = json.Marshal(remote); err != nil {
			return httpx.Internal(err)
		}
	}
	httpx.JSON(w, http.StatusOK, saved)
	return nil
}

// mergeWithStored reconciles an incoming element set with the stored scene's,
// and answers the stored scene's inline files for the caller to carry over.
func mergeWithStored(storedScene string, incoming []sceneElement) ([]sceneElement, map[string]json.RawMessage, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(storedScene), &top); err != nil {
		return nil, nil, fmt.Errorf("read stored scene: %w", err)
	}
	stored, err := decodeSceneElements(top["elements"])
	if err != nil {
		return nil, nil, fmt.Errorf("read stored elements: %w", err)
	}
	var files map[string]json.RawMessage
	_ = json.Unmarshal(top["files"], &files) // a stored scene without readable files has none to carry over
	return mergeElements(stored, incoming), files, nil
}

func (s *Server) handleBoardDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return httpx.Internal(err)
	}
	defer tx.Rollback()
	var name string
	if err := tx.QueryRowContext(r.Context(), `SELECT name FROM boards WHERE id = ?`, id).Scan(&name); errors.Is(err, sql.ErrNoRows) {
		return errBoardNotFound()
	} else if err != nil {
		return httpx.Internal(err)
	}
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM board_files WHERE board_id = ?`, id); err != nil {
		return httpx.Internal(err)
	}
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM boards WHERE id = ?`, id); err != nil {
		return httpx.Internal(err)
	}
	if err := tx.Commit(); err != nil {
		return httpx.Internal(err)
	}
	s.boards.hub.closeRoom(id)
	httpx.SetAudit(r, "board.delete", name, nil)
	httpx.NoContent(w)
	return nil
}
