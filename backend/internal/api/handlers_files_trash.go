package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// The Trash's routes. A delete on the Files page used to be final; it now
// moves the entry to its filesystem's Trash (files/trash.go), and these are
// the ways back out: the listing, a restore, and the two deletions that are
// still final — one item, or everything. Reading the Trash is a read like a
// listing; a restore is a write; anything that destroys for good stays behind
// s.destructive, as DELETE /files/delete always was.
func (s *Server) mountFilesTrashRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/trash", s.handle(s.handleFileTrashList))
	r.Method(http.MethodGet, "/trash/check", s.handle(s.handleFileTrashCheck))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapFileWrite))
		r.Method(http.MethodPost, "/trash/restore", s.handle(s.handleFileTrashRestore))
	})
	s.destructive(r, func(r chi.Router) {
		r.Method(http.MethodDelete, "/trash/item", s.handle(s.handleFileTrashRemove))
		r.Method(http.MethodDelete, "/trash", s.handle(s.handleFileTrashEmpty))
	})
}

// trashActor is who the Trash records as having deleted something.
func trashActor(r *http.Request) string {
	p, _ := httpx.PrincipalFrom(r.Context())
	if name := p.Username(); name != "" {
		return name
	}
	if name := p.KeyName(); name != "" {
		return "key " + name
	}
	return ""
}

func mapTrashError(err error) error {
	var refusal *files.TrashUnavailableError
	switch {
	case errors.As(err, &refusal):
		return httpx.Err(http.StatusConflict, "trash_unavailable", refusal.Reason)
	case errors.Is(err, files.ErrNotInTrash):
		return httpx.Err(http.StatusNotFound, "not_in_trash", "that is no longer in the Trash")
	}
	return mapFileError(err)
}

// deleteFileResponse says what DELETE /files/delete did: trashed, with the
// item to restore, or deleted for good.
type deleteFileResponse struct {
	Trashed bool             `json:"trashed"`
	Path    string           `json:"path"`
	Item    *files.TrashItem `json:"item,omitempty"`
}

// handleFileDelete moves the entry to the Trash, or with permanent=true
// deletes it for good, which is what it did before there was a Trash. An
// entry that cannot be trashed is a 409 trash_unavailable carrying the
// reason; the page shows it and asks for the permanent delete instead.
func (s *Server) handleFileDelete(w http.ResponseWriter, r *http.Request) error {
	path := r.URL.Query().Get("path")
	if path == "" {
		return httpx.BadRequest("path query parameter is required")
	}
	recursive, err := httpx.QueryBool(r, "recursive")
	if err != nil {
		return err
	}
	permanent, err := httpx.QueryBool(r, "permanent")
	if err != nil {
		return err
	}
	entry, err := s.modules.hostFiles.ResolveEntry(path)
	if err != nil {
		return mapFileError(err)
	}
	// An undo that puts a copy in the Trash names the copy it made.
	if err := s.expectEntry(entry, r.URL.Query().Get("expect")); err != nil {
		return err
	}
	if !permanent {
		item, err := s.modules.fileTrash.Put(path, trashActor(r))
		if err != nil {
			return mapTrashError(err)
		}
		s.dropFileColours(r.Context(), entry)
		httpx.SetAudit(r, "file.trash", path, map[string]any{"id": item.ID, "trash": item.Folder})
		httpx.JSON(w, http.StatusOK, deleteFileResponse{Trashed: true, Path: entry, Item: item})
		return nil
	}
	if err := s.modules.hostFiles.Delete(path, recursive); err != nil {
		return mapFileError(err)
	}
	s.dropFileColours(r.Context(), entry)
	httpx.SetAudit(r, "file.delete", path, map[string]any{"recursive": recursive, "permanent": true})
	httpx.JSON(w, http.StatusOK, deleteFileResponse{Trashed: false, Path: entry})
	return nil
}

// replacedEntry is what a move or a copy with overwrite did with the entry
// that held its destination.
type replacedEntry struct {
	// Item is that entry in the Trash, to restore when the operation is undone.
	Item *files.TrashItem `json:"item,omitempty"`
	// ForGood says why it could not go to the Trash and was replaced anyway,
	// as overwriting always did.
	ForGood string `json:"forGood,omitempty"`
	// linked is a regular file the Trash holds by a second link while the
	// copy replaces the name; undone by discarding the item.
	linked bool
}

// trashReplaced puts the entry a move or a copy is about to replace into the
// Trash, so Replace in the conflict dialog can be undone. A copy keeps a file
// by a hard link, because the copy's atomic replacement reads the old file's
// owner, mode and access list from where it is; a folder, and anything a move
// replaces, is renamed away. A symbolic link at a copy's destination is left
// to Copy, which refuses it.
func (s *Server) trashReplaced(r *http.Request, src, dst string, overwrite, copying bool) (*replacedEntry, error) {
	if !overwrite || src == dst {
		return nil, nil
	}
	occupant, err := s.modules.hostFiles.Stat(dst)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, mapFileError(err)
	}
	if copying && occupant.IsSymlink {
		return nil, nil
	}
	link := copying && !occupant.IsDir
	item, err := s.modules.fileTrash.PutReplaced(dst, trashActor(r), link)
	var refusal *files.TrashUnavailableError
	if errors.As(err, &refusal) {
		return &replacedEntry{ForGood: refusal.Reason}, nil
	}
	if err != nil {
		return nil, mapFileError(err)
	}
	return &replacedEntry{Item: item, linked: link}, nil
}

// settleReplaced undoes trashReplaced when the move or copy then failed: the
// entry goes back to its name, or the extra link is dropped.
func (s *Server) settleReplaced(replaced *replacedEntry, failed bool) {
	if replaced == nil || replaced.Item == nil || !failed {
		return
	}
	var err error
	if replaced.linked {
		err = s.modules.fileTrash.Discard(replaced.Item.ID)
	} else {
		_, err = s.modules.fileTrash.Restore(replaced.Item.ID, files.RestoreAsk, "")
	}
	if err != nil {
		s.Log.Warn("an entry put in the Trash for a replacement that failed could not be put back",
			"id", replaced.Item.ID, "err", err)
	}
}

// movedResponse answers a move or a copy: the identity of what now sits at
// the destination, which the page's undo sends back as `expect`, and what was
// replaced and where it went when something was.
type movedResponse struct {
	Identity string         `json:"identity,omitempty"`
	Replaced *replacedEntry `json:"replaced,omitempty"`
}

func (s *Server) respondMoved(w http.ResponseWriter, dst string, replaced *replacedEntry) {
	identity, err := s.modules.hostFiles.Identity(dst)
	if err != nil {
		// Done all the same; the undo simply cannot check it.
		s.Log.Warn("a moved or copied entry's identity could not be read", "path", dst, "err", err)
	}
	httpx.JSON(w, http.StatusOK, movedResponse{Identity: identity, Replaced: replaced})
}

// expectEntry refuses a mutation of an entry that is not the one the caller
// last saw at that name: a queued undo of a copy or a move, run after
// something else took the name, must leave that something alone.
func (s *Server) expectEntry(path, expect string) error {
	if expect == "" {
		return nil
	}
	identity, err := s.modules.hostFiles.Identity(path)
	if err != nil {
		return mapFileError(err)
	}
	if identity != expect {
		return httpx.Err(http.StatusConflict, "entry_changed",
			"what has that name now is not the entry this was about, so it was left alone")
	}
	return nil
}

func (s *Server) handleFileTrashList(w http.ResponseWriter, r *http.Request) error {
	listing, err := s.modules.fileTrash.List()
	if err != nil {
		return mapTrashError(err)
	}
	httpx.JSON(w, http.StatusOK, listing)
	return nil
}

func (s *Server) handleFileTrashCheck(w http.ResponseWriter, r *http.Request) error {
	path := r.URL.Query().Get("path")
	if path == "" {
		return httpx.BadRequest("path query parameter is required")
	}
	plan, err := s.modules.fileTrash.Check(path)
	if err != nil {
		return mapTrashError(err)
	}
	httpx.JSON(w, http.StatusOK, plan)
	return nil
}

func (s *Server) handleFileTrashRestore(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		ID string `json:"id"`
		// Conflict is what to do when the place it came from is taken:
		// empty answers 409 already_exists, "replace" puts what is there in
		// the Trash, "keep" restores it under a numbered name.
		Conflict string `json:"conflict,omitempty"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.ID == "" {
		return httpx.BadRequest("id is required")
	}
	if req.Conflict != files.RestoreAsk && req.Conflict != files.RestoreReplace && req.Conflict != files.RestoreKeep {
		return httpx.BadRequest("conflict must be empty, replace or keep")
	}
	restored, err := s.modules.fileTrash.Restore(req.ID, req.Conflict, trashActor(r))
	if err != nil {
		return mapTrashError(err)
	}
	// What the restore displaced takes its history into the Trash before
	// the restored entry's own comes back to the name.
	if restored.Replaced != nil {
		s.holdReplacedVersions(r.Context(), restored.Path, &replacedEntry{Item: restored.Replaced})
	}
	s.restoreTrashedVersions(r.Context(), req.ID, restored.Path)
	detail := map[string]any{"id": req.ID}
	if req.Conflict != "" {
		detail["conflict"] = req.Conflict
	}
	if restored.Replaced != nil {
		detail["replaced"] = restored.Replaced.ID
	}
	httpx.SetAudit(r, "file.trash.restore", restored.Path, detail)
	httpx.JSON(w, http.StatusOK, restored)
	return nil
}

func (s *Server) handleFileTrashRemove(w http.ResponseWriter, r *http.Request) error {
	id := r.URL.Query().Get("id")
	if id == "" {
		return httpx.BadRequest("id query parameter is required")
	}
	item, err := s.modules.fileTrash.Remove(id)
	if err != nil {
		return mapTrashError(err)
	}
	s.forgetTrashedVersions(r.Context(), id)
	httpx.SetAudit(r, "file.trash.delete", item.OriginalPath, map[string]any{"id": id, "name": item.Name})
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleFileTrashEmpty(w http.ResponseWriter, r *http.Request) error {
	before, _ := s.modules.fileTrash.List()
	removed, bytes, err := s.modules.fileTrash.Empty()
	s.forgetTrashedVersions(r.Context(), trashItemsGone(before, s.modules.fileTrash)...)
	httpx.SetAudit(r, "file.trash.empty", "", map[string]any{"items": removed, "bytes": bytes})
	if err != nil {
		return mapTrashError(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"removed": removed, "bytes": bytes})
	return nil
}

// trashItemsGone is the ids listed in before that the Trash no longer
// holds. Read from two listings rather than from what Empty counted, so an
// item it could not remove keeps what is held for it.
func trashItemsGone(before *files.TrashListing, trash *files.Trash) []string {
	if before == nil || len(before.Items) == 0 {
		return nil
	}
	after, err := trash.List()
	if err != nil {
		return nil
	}
	still := make(map[string]bool, len(after.Items))
	for _, item := range after.Items {
		still[item.ID] = true
	}
	var gone []string
	for _, item := range before.Items {
		if !still[item.ID] {
			gone = append(gone, item.ID)
		}
	}
	return gone
}

// expireFileTrash removes what has been in the Trash longer than
// JD_FILES_TRASH_DAYS, every hour, writing each removal to the audit log: a
// file that disappears on its own must still say where it went.
func (s *Server) expireFileTrash(ctx context.Context) {
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		gone, err := s.modules.fileTrash.Expire()
		if err != nil {
			s.Log.Warn("some expired Trash items could not be removed", "err", err)
		}
		ids := make([]string, 0, len(gone))
		for _, item := range gone {
			ids = append(ids, item.ID)
		}
		s.forgetTrashedVersions(ctx, ids...)
		for _, item := range gone {
			detail, _ := json.Marshal(map[string]any{
				"id": item.ID, "name": item.Name, "deletedAt": item.DeletedAt.UTC().Format(time.RFC3339),
				"keptDays": s.modules.fileTrash.KeepDays(),
			})
			s.Audit.Record(ctx, audit.Entry{
				Actor: "system", Action: "file.trash.expire", Target: item.Path, Success: true,
				Detail: string(detail),
			})
		}
		timer.Reset(time.Hour)
	}
}
