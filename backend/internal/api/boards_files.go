package api

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// A board's pictures are stored apart from its scene (board_files), so saving
// a drawing does not send every image again and the scene stays the small
// part of a board. The upload route takes the bytes as they are; a scene
// saved with images embedded, as every scene was before this, has them moved
// out by the save itself.

const (
	maxBoardFileBytes = 8 << 20
	maxBoardFiles     = 2000
	maxBoardFileTotal = 512 << 20
	// A file uploaded by one tab is not yet in the scene another tab saves; the
	// grace keeps the collection from deleting it in between.
	boardFileGrace = time.Hour
)

var boardFileID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// boardFileTypes are the types Excalidraw inserts as images. Anything else
// would be served back from the dashboard's own origin.
var boardFileTypes = map[string]bool{
	"image/svg+xml": true, "image/png": true, "image/jpeg": true, "image/gif": true,
	"image/webp": true, "image/bmp": true, "image/x-icon": true, "image/avif": true,
}

var errBoardFilesFull = httpx.Err(http.StatusRequestEntityTooLarge, "board_files_too_large",
	"this board holds as many images as it can (2000 files or 512 MiB); remove some before adding more")

type boardQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *Server) handleBoardFilePut(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	fileID := chi.URLParam(r, "fileId")
	if !boardFileID.MatchString(fileID) {
		return httpx.BadRequest("file id must be 1 to 128 letters, digits, dashes or underscores")
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !boardFileTypes[mediaType] {
		return httpx.Err(http.StatusUnsupportedMediaType, "board_file_type",
			"a board image must be SVG, PNG, JPEG, GIF, WebP, BMP, ICO or AVIF")
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBoardFileBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return errBoardFileTooLarge()
		}
		return httpx.BadRequest("the image could not be read: %v", err)
	}
	if len(data) == 0 {
		return httpx.BadRequest("the image is empty")
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
	httpx.SetAudit(r, "board.file.upload", name, nil)
	added, err := s.storeBoardFile(r.Context(), tx, id, fileID, mediaType, data, time.Now())
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return httpx.Internal(err)
	}
	status := http.StatusOK
	if added {
		status = http.StatusCreated
	}
	httpx.JSON(w, status, map[string]string{"id": fileID})
	return nil
}

func errBoardFileTooLarge() error {
	return httpx.Err(http.StatusRequestEntityTooLarge, "board_file_too_large", "an image on a board can be at most 8 MiB")
}

func errBoardNotFound() error {
	return httpx.Err(http.StatusNotFound, "board_not_found", "board not found")
}

// storeBoardFile adds a file to a board, or says it was already there. The id
// is a content hash or random, so a second upload under the same id is the
// same picture; it only counts as the file being in use again, which restarts
// its grace so an undo that brings an old image back is not undone by the
// collection.
func (s *Server) storeBoardFile(ctx context.Context, q boardQuerier, boardID int64, fileID, mimeType string, data []byte, now time.Time) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM board_files WHERE board_id = ? AND file_id = ?`, boardID, fileID).Scan(&one)
	if err == nil {
		if _, err := q.ExecContext(ctx, `UPDATE board_files SET created_at = ? WHERE board_id = ? AND file_id = ?`,
			now.Unix(), boardID, fileID); err != nil {
			return false, httpx.Internal(err)
		}
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, httpx.Internal(err)
	}
	var count, total int64
	if err := q.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(LENGTH(data)), 0) FROM board_files WHERE board_id = ?`, boardID,
	).Scan(&count, &total); err != nil {
		return false, httpx.Internal(err)
	}
	if count >= s.boards.maxFiles || total+int64(len(data)) > s.boards.maxFileBytes {
		return false, errBoardFilesFull
	}
	if _, err := q.ExecContext(ctx,
		`INSERT INTO board_files(board_id, file_id, mime_type, data, created_at) VALUES(?,?,?,?,?)`,
		boardID, fileID, mimeType, data, now.Unix()); err != nil {
		return false, httpx.Internal(err)
	}
	return true, nil
}

func (s *Server) handleBoardFileGet(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var mimeType string
	var data []byte
	err = s.Store.DB.QueryRowContext(r.Context(),
		`SELECT mime_type, data FROM board_files WHERE board_id = ? AND file_id = ?`, id, chi.URLParam(r, "fileId"),
	).Scan(&mimeType, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return httpx.Err(http.StatusNotFound, "board_file_not_found", "image not found")
	}
	if err != nil {
		return httpx.Internal(err)
	}
	h := w.Header()
	h.Set("Content-Type", mimeType)
	h.Set("Content-Length", strconv.Itoa(len(data)))
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	// An SVG opened on its own address runs in the dashboard's origin; the
	// sandbox and the empty policy leave it nothing to run or fetch.
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
	return nil
}

// dataURLFile reads a data URL of an allowed image type. It answers false
// for anything it would not store, which then stays in the scene as it was.
func dataURLFile(dataURL string) (string, []byte, bool) {
	rest, ok := strings.CutPrefix(dataURL, "data:")
	if !ok {
		return "", nil, false
	}
	mimeType, payload, ok := strings.Cut(rest, ";base64,")
	mimeType = strings.ToLower(mimeType)
	if !ok || !boardFileTypes[mimeType] || base64.StdEncoding.DecodedLen(len(payload)) > maxBoardFileBytes+2 {
		return "", nil, false
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(data) == 0 || len(data) > maxBoardFileBytes {
		return "", nil, false
	}
	return mimeType, data, true
}

// extractSceneFiles moves the images embedded in a scene's files into
// board_files and returns the entries left behind: those that are not a valid
// image, and those the board has no room for. An entry the server will not
// store must never be dropped, or an old drawing would lose its picture.
func (s *Server) extractSceneFiles(ctx context.Context, q boardQuerier, boardID int64, files map[string]json.RawMessage, now time.Time) (map[string]json.RawMessage, error) {
	ids := make([]string, 0, len(files))
	for id := range files {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	left := make(map[string]json.RawMessage, len(files))
	for id, raw := range files {
		left[id] = raw
	}
	for _, id := range ids {
		if !boardFileID.MatchString(id) {
			continue
		}
		var entry struct {
			DataURL string `json:"dataURL"`
		}
		if json.Unmarshal(files[id], &entry) != nil {
			continue
		}
		mimeType, data, ok := dataURLFile(entry.DataURL)
		if !ok {
			continue
		}
		if _, err := s.storeBoardFile(ctx, q, boardID, id, mimeType, data, now); errors.Is(err, errBoardFilesFull) {
			continue
		} else if err != nil {
			return nil, err
		}
		delete(left, id)
	}
	return left, nil
}

// collectBoardFiles deletes a board's files that no element names and that
// are past the grace, and answers the ids that remain, sorted.
func collectBoardFiles(ctx context.Context, q boardQuerier, boardID int64, refs map[string]bool, now time.Time) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT file_id, created_at FROM board_files WHERE board_id = ? ORDER BY file_id`, boardID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	defer rows.Close()
	kept := []string{}
	var drop []string
	cutoff := now.Add(-boardFileGrace).Unix()
	for rows.Next() {
		var id string
		var created int64
		if err := rows.Scan(&id, &created); err != nil {
			return nil, httpx.Internal(err)
		}
		if refs[id] || created > cutoff {
			kept = append(kept, id)
		} else {
			drop = append(drop, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, httpx.Internal(err)
	}
	// Close before deleting: inside a transaction there is one connection.
	if err := rows.Close(); err != nil {
		return nil, httpx.Internal(err)
	}
	for _, id := range drop {
		if _, err := q.ExecContext(ctx, `DELETE FROM board_files WHERE board_id = ? AND file_id = ?`, boardID, id); err != nil {
			return nil, httpx.Internal(err)
		}
	}
	return kept, nil
}

func boardFileIDs(ctx context.Context, q boardQuerier, boardID int64) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT file_id FROM board_files WHERE board_id = ? ORDER BY file_id`, boardID)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, httpx.Internal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, httpx.Internal(err)
	}
	return ids, nil
}
