package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// Excalidraw's shape library is one per install (board_library). The browser
// may not fetch a shared library itself, because the page's CSP allows
// connect-src 'self' only, so the dashboard downloads it on request from the
// two places Excalidraw itself accepts a library from.

const (
	maxBoardLibraryBytes = 16 << 20
	maxLibraryFetchBytes = 10 << 20
	libraryFetchTimeout  = 15 * time.Second
)

var (
	// These mirror Excalidraw's own ALLOWED_LIBRARY_URLS.
	libraryHostPath = regexp.MustCompile(`^/libraries/[A-Za-z0-9._-]+/[A-Za-z0-9._-]+\.excalidrawlib$`)
	libraryRawPath  = regexp.MustCompile(`^/excalidraw/excalidraw-libraries/[A-Za-z0-9._/-]+\.excalidrawlib$`)
)

func errLibraryTooLarge(limit string) error {
	return httpx.Err(http.StatusRequestEntityTooLarge, "library_too_large", "the library is larger than "+limit)
}

func (s *Server) handleBoardLibraryGet(w http.ResponseWriter, r *http.Request) error {
	var items string
	var revision int64
	err := s.Store.DB.QueryRowContext(r.Context(),
		`SELECT items, revision FROM board_library WHERE id = 1`).Scan(&items, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		items = "[]"
	} else if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"libraryItems": json.RawMessage(items), "revision": revision})
	return nil
}

func (s *Server) handleBoardLibraryPut(w http.ResponseWriter, r *http.Request) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return httpx.Err(http.StatusUnsupportedMediaType, "json_content_type_required", "request body must use application/json")
	}
	var req struct {
		LibraryItems json.RawMessage `json:"libraryItems"`
		Revision     int64           `json:"revision"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBoardLibraryBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return errLibraryTooLarge("16 MiB")
		}
		return httpx.BadRequest("malformed library: %v", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return httpx.BadRequest("library request must contain one JSON object")
	}
	var items []json.RawMessage
	if !isJSONArray(req.LibraryItems) || json.Unmarshal(req.LibraryItems, &items) != nil || req.Revision < 0 {
		return httpx.BadRequest("libraryItems must be an array and revision a number that is not negative")
	}
	for _, item := range items {
		if t := bytes.TrimSpace(item); len(t) == 0 || t[0] != '{' {
			return httpx.BadRequest("every library item must be an object")
		}
	}
	httpx.SetAudit(r, "board.library.save", "", nil)

	tx, err := s.Store.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return httpx.Internal(err)
	}
	defer tx.Rollback()
	var stored int64
	err = tx.QueryRowContext(r.Context(), `SELECT revision FROM board_library WHERE id = 1`).Scan(&stored)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return httpx.Internal(err)
	}
	if stored != req.Revision {
		return httpx.Err(http.StatusConflict, "library_conflict", "the library changed in another tab; reload it before saving")
	}
	if _, err := tx.ExecContext(r.Context(),
		`INSERT INTO board_library(id, items, revision, updated_at) VALUES(1,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET items = excluded.items, revision = excluded.revision, updated_at = excluded.updated_at`,
		string(bytes.TrimSpace(req.LibraryItems)), stored+1, time.Now().Unix()); err != nil {
		return httpx.Internal(err)
	}
	if err := tx.Commit(); err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]int64{"revision": stored + 1})
	return nil
}

// libraryURL checks an address against what Excalidraw accepts and answers the
// address to request, built from the checked parts rather than the input.
func libraryURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" ||
		u.RawQuery != "" || u.ForceQuery || u.RawPath != "" {
		return "", false
	}
	host := strings.ToLower(u.Host)
	if strings.ContainsAny(host, ":@") {
		return "", false
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return "", false
		}
	}
	switch {
	case host == "libraries.excalidraw.com" && libraryHostPath.MatchString(u.Path):
	case host == "raw.githubusercontent.com" && libraryRawPath.MatchString(u.Path):
	default:
		return "", false
	}
	return "https://" + host + u.Path, true
}

func newLibraryClient() *http.Client {
	return &http.Client{
		Timeout: libraryFetchTimeout,
		// A redirect would leave the two hosts that were checked.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func libraryFetchFailed(why string) error {
	return httpx.Err(http.StatusBadGateway, "library_fetch_failed", why).Retry()
}

func (s *Server) handleBoardLibraryFetch(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		URL string `json:"url"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SetAudit(r, "board.library.fetch", clipAuditText(req.URL, 300), nil)
	target, ok := libraryURL(req.URL)
	if !ok {
		return httpx.Err(http.StatusBadRequest, "library_url_refused",
			"libraries can be added only from libraries.excalidraw.com or the excalidraw-libraries repository on GitHub")
	}
	body, err := s.downloadLibrary(r.Context(), target)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
	return nil
}

func (s *Server) downloadLibrary(ctx context.Context, target string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, libraryFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	req.Header.Set("Accept", "application/json")
	res, err := s.boards.fetch.Do(req)
	if err != nil {
		return nil, libraryFetchFailed("the library could not be downloaded; try again shortly")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, libraryFetchFailed("the library's host answered with status " + res.Status)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxLibraryFetchBytes+1))
	if err != nil {
		return nil, libraryFetchFailed("the library could not be downloaded; try again shortly")
	}
	if len(body) > maxLibraryFetchBytes {
		return nil, errLibraryTooLarge("10 MiB")
	}
	var file struct {
		Type         string          `json:"type"`
		LibraryItems json.RawMessage `json:"libraryItems"`
		Library      json.RawMessage `json:"library"`
	}
	if json.Unmarshal(body, &file) != nil || file.Type != "excalidrawlib" ||
		!isJSONArray(file.LibraryItems) && !isJSONArray(file.Library) {
		return nil, libraryFetchFailed("the address did not hold an Excalidraw library")
	}
	return body, nil
}

func isJSONArray(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '['
}

// clipAuditText shortens text that came from a client before it is kept in the audit
// trail.
func clipAuditText(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
