package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

const (
	// revisionsPerPath is how far back a file's history goes. Old enough to
	// reach the last working version after a bad afternoon, small enough
	// that a deployment loop rewriting one route cannot grow the database.
	revisionsPerPath = 50
	// revisionMaxBytes keeps a generated map or a pasted bundle out of the
	// database. Configuration files are a few kilobytes.
	revisionMaxBytes = 1 << 20

	// The actions a revision is recorded under beyond proxysvc's own.
	revisionOutside  = "outside"
	revisionBaseline = "baseline"
	revisionRestore  = "restore"
)

// proxyRevisions keeps every configuration file the proxy service changes,
// in proxy_config_revisions. It is the service's ChangeRecorder, so it runs
// under the service lock and only ever talks to the database.
type proxyRevisions struct {
	db *sql.DB
}

type revisionActionKey struct{}

// withRevisionAction names the change a request makes when the service's
// own action would say less than the operator did: a restore is a write.
func withRevisionAction(ctx context.Context, action string) context.Context {
	return context.WithValue(ctx, revisionActionKey{}, action)
}

func contentSum(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Record keeps the file as the change left it. Before that it compares the
// file as the change found it with the newest revision kept: when they
// differ, somebody changed the file outside the dashboard in between, and
// that state is kept too, so the history never shows the dashboard's change
// as the only difference. A file with no history yet keeps its before-state
// as the baseline every later diff starts from.
func (p *proxyRevisions) Record(ctx context.Context, c proxysvc.Change) error {
	toggle := c.Action == proxysvc.ChangeEnable || c.Action == proxysvc.ChangeDisable
	// A toggled site file outside the proxy's directories comes without
	// content, and an empty revision would read as an emptied file.
	if toggle && c.Before == nil && c.After == nil {
		return nil
	}
	if len(c.Before) > revisionMaxBytes || len(c.After) > revisionMaxBytes {
		return fmt.Errorf("%s is larger than %d bytes, so its history is not kept", c.Path, revisionMaxBytes)
	}
	action := string(c.Action)
	if named, ok := ctx.Value(revisionActionKey{}).(string); ok {
		action = named
	}
	now := time.Now().UTC().Unix()

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var lastSum string
	var lastExisted bool
	err = tx.QueryRowContext(ctx,
		`SELECT sha256, existed FROM proxy_config_revisions WHERE path = ? ORDER BY id DESC LIMIT 1`,
		c.Path).Scan(&lastSum, &lastExisted)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if c.BeforeExisted {
			if err := insertRevision(ctx, tx, c.Path, c.Before, true, revisionBaseline, "", now); err != nil {
				return err
			}
		}
	case err != nil:
		return err
	case lastExisted != c.BeforeExisted || (c.BeforeExisted && lastSum != contentSum(c.Before)):
		if err := insertRevision(ctx, tx, c.Path, c.Before, c.BeforeExisted, revisionOutside, "", now); err != nil {
			return err
		}
	}

	after, existed := c.After, c.Action != proxysvc.ChangeDelete
	if !existed {
		after = nil
	}
	if err := insertRevision(ctx, tx, c.Path, after, existed, action, c.Actor, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM proxy_config_revisions WHERE path = ? AND id NOT IN
		(SELECT id FROM proxy_config_revisions WHERE path = ? ORDER BY id DESC LIMIT ?)`,
		c.Path, c.Path, revisionsPerPath); err != nil {
		return err
	}
	return tx.Commit()
}

func insertRevision(ctx context.Context, tx *sql.Tx, path string, content []byte, existed bool, action, actor string, at int64) error {
	if content == nil {
		content = []byte{}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO proxy_config_revisions(path, sha256, content, existed, action, actor, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)`, path, contentSum(content), content, existed, action, actor, at)
	return err
}

// configRevision is one kept state of a file, without its content.
type configRevision struct {
	ID        int64  `json:"id"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Size      int    `json:"size"`
	Existed   bool   `json:"existed"`
	Action    string `json:"action"`
	Actor     string `json:"actor"`
	CreatedAt int64  `json:"createdAt"`
}

// fileOnDisk is a recorded file as it is now. Drift says it no longer holds
// what its newest revision does: it was changed outside the dashboard since.
type fileOnDisk struct {
	Exists bool   `json:"exists"`
	SHA256 string `json:"sha256,omitempty"`
	Drift  bool   `json:"drift"`
	// Unreadable is the reason the file could not be read, which is not the
	// same as a file that is gone.
	Unreadable string `json:"unreadable,omitempty"`
}

// mountConfigHistoryRoutes serves the kept revisions. Every route is behind
// system.admin where it is mounted: a revision is a file's full content, the
// same trust as the effective configuration.
func (s *Server) mountConfigHistoryRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/", s.handle(s.handleConfigHistory))
	r.Method(http.MethodGet, "/files", s.handle(s.handleConfigHistoryFiles))
	r.Method(http.MethodGet, "/{id}", s.handle(s.handleConfigRevision))
	// A restore is a config write, tested and rolled back the same way, so
	// it is gated as one rather than as a removal.
	r.Method(http.MethodPost, "/{id}/restore", s.handle(s.handleConfigRestore))
}

// onDisk reads a recorded file now. A read the service refuses — a password
// file, a path outside its directories — is reported rather than shown.
func (s *Server) onDisk(path string) (fileOnDisk, string) {
	content, err := s.modules.proxy.ReadConfig(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fileOnDisk{}, ""
	case err != nil:
		return fileOnDisk{Unreadable: err.Error()}, ""
	}
	return fileOnDisk{Exists: true, SHA256: contentSum([]byte(content))}, content
}

func (d *fileOnDisk) compare(latest configRevision) {
	if d.Unreadable != "" {
		return
	}
	d.Drift = d.Exists != latest.Existed || (d.Exists && d.SHA256 != latest.SHA256)
}

const revisionColumns = `id, path, sha256, length(content), existed, action, actor, created_at`

func scanRevision(row interface{ Scan(...any) error }) (configRevision, error) {
	var rev configRevision
	err := row.Scan(&rev.ID, &rev.Path, &rev.SHA256, &rev.Size, &rev.Existed, &rev.Action, &rev.Actor, &rev.CreatedAt)
	return rev, err
}

type historyFile struct {
	Path      string         `json:"path"`
	Revisions int            `json:"revisions"`
	Latest    configRevision `json:"latest"`
	Current   fileOnDisk     `json:"current"`
}

// handleConfigHistoryFiles lists every file with a history, newest change
// first, each with how it compares to the file on disk now. A deleted site's
// file is listed while its revisions are kept, which is what makes it
// restorable.
func (s *Server) handleConfigHistoryFiles(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.Store.DB.QueryContext(r.Context(), `SELECT `+revisionColumns+`, counts.n
		FROM proxy_config_revisions
		JOIN (SELECT path AS p, MAX(id) AS newest, COUNT(*) AS n FROM proxy_config_revisions GROUP BY path) counts
		  ON id = counts.newest
		ORDER BY id DESC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	files := []historyFile{}
	for rows.Next() {
		var f historyFile
		err := rows.Scan(&f.Latest.ID, &f.Latest.Path, &f.Latest.SHA256, &f.Latest.Size, &f.Latest.Existed,
			&f.Latest.Action, &f.Latest.Actor, &f.Latest.CreatedAt, &f.Revisions)
		if err != nil {
			return err
		}
		f.Path = f.Latest.Path
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range files {
		files[i].Current, _ = s.onDisk(files[i].Path)
		files[i].Current.compare(files[i].Latest)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"files": files})
	return nil
}

// handleConfigHistory lists one file's revisions, newest first, with the
// file as it is now.
func (s *Server) handleConfigHistory(w http.ResponseWriter, r *http.Request) error {
	path := r.URL.Query().Get("path")
	if path == "" {
		return httpx.BadRequest("path query parameter is required")
	}
	rows, err := s.Store.DB.QueryContext(r.Context(),
		`SELECT `+revisionColumns+` FROM proxy_config_revisions WHERE path = ? ORDER BY id DESC`, path)
	if err != nil {
		return err
	}
	defer rows.Close()
	revisions := []configRevision{}
	for rows.Next() {
		rev, err := scanRevision(rows)
		if err != nil {
			return err
		}
		revisions = append(revisions, rev)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	current, _ := s.onDisk(path)
	if len(revisions) > 0 {
		current.compare(revisions[0])
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"path": path, "revisions": revisions, "current": current})
	return nil
}

func (s *Server) revisionByID(ctx context.Context, raw string) (configRevision, []byte, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return configRevision{}, nil, httpx.BadRequest("revision id must be a number")
	}
	var content []byte
	rev, err := scanRevisionWithContent(s.Store.DB.QueryRowContext(ctx,
		`SELECT `+revisionColumns+`, content FROM proxy_config_revisions WHERE id = ?`, id), &content)
	if errors.Is(err, sql.ErrNoRows) {
		return rev, nil, httpx.Err(http.StatusNotFound, "not_found", "That revision is not kept.").
			Because("Each file keeps its newest 50 revisions, and older ones are dropped as new ones arrive.", "")
	}
	return rev, content, err
}

type revisionWithContent struct {
	Revision configRevision `json:"revision"`
	Content  string         `json:"content"`
}

// handleConfigRevision answers one revision's content beside the revision
// before it and the file on disk, so the page can diff against either.
func (s *Server) handleConfigRevision(w http.ResponseWriter, r *http.Request) error {
	rev, content, err := s.revisionByID(r.Context(), httpx.URLParam(r, "id"))
	if err != nil {
		return err
	}
	var previous *revisionWithContent
	var prevContent []byte
	prevRev, err := scanRevisionWithContent(s.Store.DB.QueryRowContext(r.Context(),
		`SELECT `+revisionColumns+`, content FROM proxy_config_revisions WHERE path = ? AND id < ? ORDER BY id DESC LIMIT 1`,
		rev.Path, rev.ID), &prevContent)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	default:
		previous = &revisionWithContent{Revision: prevRev, Content: string(prevContent)}
	}
	current, currentContent := s.onDisk(rev.Path)
	current.compare(rev)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"revision":       rev,
		"content":        string(content),
		"previous":       previous,
		"current":        current,
		"currentContent": currentContent,
	})
	return nil
}

func scanRevisionWithContent(row *sql.Row, content *[]byte) (configRevision, error) {
	var rev configRevision
	err := row.Scan(&rev.ID, &rev.Path, &rev.SHA256, &rev.Size, &rev.Existed, &rev.Action, &rev.Actor, &rev.CreatedAt, content)
	return rev, err
}

type revisionRestoreRequest struct {
	Reload bool `json:"reload"`
}

// handleConfigRestore writes a revision back through WriteConfig, so the
// engine tests it in place and a refusal leaves the file as it was, exactly
// as a save from the editor. A revision of a removed file has nothing to
// write; the one before the removal is what brings the file back.
func (s *Server) handleConfigRestore(w http.ResponseWriter, r *http.Request) error {
	var req revisionRestoreRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	rev, content, err := s.revisionByID(r.Context(), httpx.URLParam(r, "id"))
	if err != nil {
		return err
	}
	if !rev.Existed {
		return httpx.Err(http.StatusConflict, "nothing_to_restore", "This revision is the file being removed.").
			Because("Restore the revision before it to bring the file back.", "")
	}
	// ReadConfig's refusals are WriteConfig's missing ones: a password file
	// is never configuration to write back from here.
	if _, err := s.modules.proxy.ReadConfig(rev.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return mapProxyError(err)
	}
	kind := s.modules.proxy.KindOf(rev.Path)
	ctx := withRevisionAction(r.Context(), revisionRestore)
	res, err := s.modules.proxy.WriteConfig(ctx, kind, rev.Path, string(content))
	if err != nil {
		if errors.Is(err, proxysvc.ErrInvalidConf) {
			httpx.SetAudit(r, "proxy.history.restore", rev.Path, map[string]any{"revision": rev.ID, "result": "rejected"})
			return refuseInvalidConfig(w, r, httpx.Err(http.StatusUnprocessableEntity, "invalid_config", res.Output), res)
		}
		return mapProxyError(err)
	}
	out := map[string]any{"validation": res}
	if req.Reload {
		reload, err := s.modules.proxy.Reload(r.Context(), kind)
		out["reload"] = reload
		if err != nil {
			httpx.SetAudit(r, "proxy.history.restore", rev.Path, map[string]any{"revision": rev.ID, "reloaded": false})
			return httpx.Err(http.StatusBadGateway, "reload_failed", err.Error())
		}
	}
	httpx.SetAudit(r, "proxy.history.restore", rev.Path,
		map[string]any{"revision": rev.ID, "kind": kind, "reloaded": req.Reload})
	httpx.JSON(w, http.StatusOK, out)
	return nil
}
