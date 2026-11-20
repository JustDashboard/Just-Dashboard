package api

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/contentguard"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/fileversions"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// A file's versions: what the dashboard replaced when it saved over the file
// (internal/fileversions), and the backup runs that hold a copy of it
// (internal/backups), as one list by time. Reading a version is a file read
// and answers to the same roots; restoring an edit version is a save, and
// restoring a backup's copy is a backup restore, behind the backups' own
// destructive gate.
func (s *Server) mountFilesVersionsRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/versions", s.handle(s.handleFileVersions))
	r.Method(http.MethodGet, "/versions/content", s.handle(s.handleFileVersionContent))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapFileWrite))
		r.Method(http.MethodPost, "/versions/restore", s.handle(s.handleFileVersionRestore))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodPost, "/versions/restore-backup", s.handle(s.handleFileVersionRestoreBackup))
		})
	})
}

// fileVersionsSlot builds the keeper on first use, from the settings as they
// are then.
type fileVersionsSlot struct {
	once   sync.Once
	keeper *fileversions.Keeper
}

func (s *Server) fileVersions() *fileversions.Keeper {
	slot := &s.modules.fileVersions
	slot.once.Do(func() {
		slot.keeper = fileversions.New(s.Store.DB, filepath.Join(s.Cfg.DataDir, "file-versions"),
			fileversions.Limits{PerFile: s.Cfg.FileVersions, TotalBytes: int64(s.Cfg.FileVersionsMB) << 20})
	})
	return slot.keeper
}

// recordTimeout bounds how long a save waits on keeping its version. The save
// has already happened; a busy store costs the version, not the save.
const recordTimeout = 5 * time.Second

func principalName(r *http.Request) string {
	if p, ok := httpx.PrincipalFrom(r.Context()); ok && p.User != nil {
		if p.User.DisplayName != "" {
			return p.User.DisplayName
		}
		return p.User.Username
	}
	return ""
}

// keepFileVersion records what a successful save replaced. It never fails the
// save: what it could not keep is logged.
func (s *Server) keepFileVersion(r *http.Request, kept *files.Replaced, save fileversions.Save) {
	if kept == nil || kept.Path == "" {
		return
	}
	keeper := s.fileVersions()
	if !keeper.Enabled() {
		return
	}
	save.Path, save.Existed, save.Content, save.TooLarge = kept.Path, kept.Existed, kept.Content, kept.TooLarge
	save.By = principalName(r)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), recordTimeout)
	defer cancel()
	if err := keeper.Record(ctx, save); err != nil {
		s.Log.Warn("the file was saved, but its earlier content could not be kept as a version",
			"path", kept.Path, "err", err)
	}
}

// writeFileKept is PUT /files/write's save: WriteChecked, keeping what it
// replaced.
func (s *Server) writeFileKept(r *http.Request, path, content string, expected *string) error {
	kept, err := s.modules.hostFiles.WriteKept(path, content, expected)
	if err != nil {
		return err
	}
	s.keepFileVersion(r, kept, fileversions.Save{Written: []byte(content), Reason: fileversions.ReasonSave})
	return nil
}

// uploadFileKept is one uploaded file's publication: UploadChecked, keeping
// what it replaced. written is the hash the handler takes of the bytes as
// they stream; a picture's upload is never held whole.
func (s *Server) uploadFileKept(r *http.Request, path string, body io.Reader, overwrite bool, expected *string, written hash.Hash) (int64, error) {
	n, kept, err := s.modules.hostFiles.UploadKept(path, body, overwrite, expected)
	if err != nil {
		return n, err
	}
	reason := fileversions.ReasonUpload
	if expected != nil {
		reason = fileversions.ReasonImage
	}
	s.keepFileVersion(r, kept, fileversions.Save{
		WrittenRevision: hex.EncodeToString(written.Sum(nil)), WrittenSize: n, Reason: reason,
	})
	return n, nil
}

// moveFileVersions carries a moved file's history, and that of everything
// under a moved folder, to where it landed. Best effort, as a folder's colour
// is: the move has already happened.
func (s *Server) moveFileVersions(ctx context.Context, src, dst string) {
	if err := s.fileVersions().Move(ctx, src, dst); err != nil {
		s.Log.Warn("a moved file's versions stayed under its old path", "from", src, "to", dst, "err", err)
	}
}

// fileVersionEntry is one row of a file's history: an edit version or a
// backup run's copy.
type fileVersionEntry struct {
	// ID is "e<n>" for an edit version and "b<run>" for a backup's copy.
	ID       string            `json:"id"`
	Kind     string            `json:"kind"`
	At       time.Time         `json:"at,omitzero"`
	By       string            `json:"by,omitempty"`
	Reason   string            `json:"reason,omitempty"`
	Outside  bool              `json:"outside,omitempty"`
	Size     int64             `json:"size,omitempty"`
	Revision string            `json:"revision,omitempty"`
	Backup   *backups.FileCopy `json:"backup,omitempty"`
}

type fileVersionsResponse struct {
	Path     string             `json:"path"`
	Versions []fileVersionEntry `json:"versions"`
	// LastSave is what the dashboard last wrote here, when it did.
	LastSave *fileversions.Head `json:"lastSave,omitempty"`
	// Keeping says whether saves keep versions at all, and how many.
	Keeping  bool  `json:"keeping"`
	PerFile  int   `json:"perFile"`
	MaxBytes int64 `json:"maxBytes"`
	// TooLarge is a current file over the size a version is kept at.
	TooLarge bool `json:"tooLarge"`
	// Backups is false for a person who may not see the Backups page, whose
	// list then holds no backup copies.
	Backups bool `json:"backups"`
}

// canSeeBackups is whether the backups' read routes would answer this
// person: the copies are the Backups page's runs, shown here.
func canSeeBackups(r *http.Request) bool {
	p, ok := httpx.PrincipalFrom(r.Context())
	return ok && p.Can(auth.CapRead) && !p.DeployKey()
}

// canOpenBackupCopies is whether this person may read a file's copy out of a
// backup run. It is what GET /backups/runs/{id}/download asks, for the same
// reason: the bytes leave an archive that holds whatever the job covered, and
// reading one may fetch and decrypt the whole of it. Restoring a copy in
// place hands the bytes to nobody, so it keeps the restore's own gate.
func canOpenBackupCopies(r *http.Request) bool {
	p, ok := httpx.PrincipalFrom(r.Context())
	return ok && p.Can(auth.CapSystemAdmin)
}

func (s *Server) handleFileVersions(w http.ResponseWriter, r *http.Request) error {
	raw := r.URL.Query().Get("path")
	if raw == "" {
		return httpx.BadRequest("path query parameter is required")
	}
	path, err := s.modules.hostFiles.Resolve(raw)
	if err != nil {
		return mapFileError(err)
	}
	keeper := s.fileVersions()
	history, err := keeper.History(r.Context(), path)
	if err != nil {
		return httpx.Internal(err)
	}
	limits := keeper.Limits()
	out := fileVersionsResponse{
		Path: path, Versions: []fileVersionEntry{}, LastSave: history.Head,
		Keeping: keeper.Enabled(), PerFile: limits.PerFile, MaxBytes: fileversions.MaxFileBytes,
	}
	if entry, err := s.modules.hostFiles.Stat(path); err == nil && !entry.IsDir && entry.Size > fileversions.MaxFileBytes {
		out.TooLarge = true
	}
	for _, v := range history.Versions {
		out.Versions = append(out.Versions, editVersionEntry(v))
	}
	if canSeeBackups(r) && s.modules.backupStore != nil {
		out.Backups = true
		copies, err := s.modules.backupStore.FileCopies(r.Context(), path, 200)
		if err != nil {
			return httpx.Internal(err)
		}
		for i := range copies {
			c := copies[i]
			out.Versions = append(out.Versions, fileVersionEntry{
				ID: "b" + strconv.FormatInt(c.RunID, 10), Kind: "backup", At: c.At, Backup: &c,
			})
		}
	}
	sort.SliceStable(out.Versions, func(i, j int) bool { return out.Versions[i].At.After(out.Versions[j].At) })
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func editVersionEntry(v fileversions.Version) fileVersionEntry {
	return fileVersionEntry{
		ID: "e" + strconv.FormatInt(v.ID, 10), Kind: "edit", At: v.At, By: v.By, Reason: v.Reason,
		Outside: v.Outside, Size: v.Size, Revision: v.Revision,
	}
}

// versionRef is a version id as the list gives it.
type versionRef struct {
	edit   int64
	backup int64
}

func parseVersionRef(id string) (versionRef, error) {
	if len(id) > 1 {
		n, err := strconv.ParseInt(id[1:], 10, 64)
		if err == nil && n > 0 {
			switch id[0] {
			case 'e':
				return versionRef{edit: n}, nil
			case 'b':
				return versionRef{backup: n}, nil
			}
		}
	}
	return versionRef{}, httpx.BadRequest("version must be a version id from the file's history")
}

func fileVersionError(err error) error {
	switch {
	case errors.Is(err, fileversions.ErrNotFound), errors.Is(err, backups.ErrNotFound):
		return httpx.Err(http.StatusNotFound, "not_found", fileversions.ErrNotFound.Error())
	case errors.Is(err, fileversions.ErrMissing), errors.Is(err, backups.ErrNotInArchive):
		return httpx.Err(http.StatusGone, "version_gone", err.Error())
	case errors.Is(err, backups.ErrFileCopyTooLarge):
		return httpx.Err(http.StatusRequestEntityTooLarge, "too_large", err.Error())
	case errors.Is(err, fileversions.ErrDamaged):
		return httpx.Internal(err)
	}
	return nil
}

// versionContent reads one version of path. A backup's copy is read from its
// run's archive, which for a remote destination means fetching it.
func (s *Server) versionContent(r *http.Request, path string, ref versionRef) ([]byte, error) {
	if ref.edit > 0 {
		content, _, err := s.fileVersions().Content(r.Context(), path, ref.edit)
		if err != nil {
			if mapped := fileVersionError(err); mapped != nil {
				return nil, mapped
			}
			return nil, httpx.Internal(err)
		}
		return content, nil
	}
	if !canSeeBackups(r) || s.modules.backupRunner == nil {
		return nil, httpx.Err(http.StatusNotFound, "not_found", fileversions.ErrNotFound.Error())
	}
	ctx, cancel := timeoutCtx(r, 30*time.Minute)
	defer cancel()
	content, err := s.modules.backupRunner.ReadFileCopy(ctx, ref.backup, path, fileversions.MaxFileBytes)
	if err != nil {
		if mapped := fileVersionError(err); mapped != nil {
			return nil, mapped
		}
		return nil, mapBackupError(err)
	}
	return content, nil
}

type fileVersionContent struct {
	Content  string `json:"content"`
	Binary   bool   `json:"binary"`
	Size     int    `json:"size"`
	Revision string `json:"revision"`
}

// handleFileVersionContent answers one version as text for the diff
// (?as=text, the default), as a picture through the raw route's allowlist and
// policy (?as=image), or as a download (?as=download).
func (s *Server) handleFileVersionContent(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	raw := q.Get("path")
	if raw == "" {
		return httpx.BadRequest("path query parameter is required")
	}
	ref, err := parseVersionRef(q.Get("version"))
	if err != nil {
		return err
	}
	path, err := s.modules.hostFiles.Resolve(raw)
	if err != nil {
		return mapFileError(err)
	}
	if ref.backup > 0 && !canOpenBackupCopies(r) {
		return httpx.Err(http.StatusForbidden, "forbidden",
			"opening a backup's copy takes it out of the archive, which only an administrator may do ("+string(auth.CapSystemAdmin)+")")
	}
	as := q.Get("as")
	mediaType := files.MediaType(filepath.Base(path))
	if as == "image" && !strings.HasPrefix(mediaType, "image/") {
		return httpx.Err(http.StatusUnsupportedMediaType, "not_inline",
			"only a picture's versions are shown inline; download this one instead")
	}
	content, err := s.versionContent(r, path, ref)
	if err != nil {
		return err
	}
	name := filepath.Base(path)
	switch as {
	case "image":
		w.Header().Set("Content-Type", mediaType)
		w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": name}))
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox; frame-ancestors 'none'")
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(content))
	case "download":
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(content))
	case "", "text":
		out := fileVersionContent{Size: len(content), Revision: contentguard.RevisionBytes(content), Binary: looksBinaryContent(content)}
		if !out.Binary {
			out.Content = string(content)
		}
		httpx.JSON(w, http.StatusOK, out)
	default:
		return httpx.BadRequest("as must be text, image or download")
	}
	return nil
}

// looksBinaryContent is the editor's own test (files.Read): a NUL in the
// first kilobyte.
func looksBinaryContent(b []byte) bool {
	return bytes.IndexByte(b[:min(len(b), 1024)], 0) >= 0
}

type restoreVersionRequest struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	// ExpectedRevision is the revision of the file as the person last saw
	// it, as a save carries; empty restores a file that is no longer there.
	ExpectedRevision *string `json:"expectedRevision"`
}

func (req restoreVersionRequest) validate() (versionRef, error) {
	if req.Path == "" {
		return versionRef{}, httpx.BadRequest("path is required")
	}
	if req.ExpectedRevision == nil {
		return versionRef{}, httpx.BadRequest("expectedRevision is required: the revision of the file as it was last seen, or empty for a file that is gone")
	}
	return parseVersionRef(req.Version)
}

// restoreVersion puts content back over path as a save does, through the
// same guarded publish, so a file changed since it was looked at is a
// 409 content_conflict and what the restore replaces is itself kept.
func (s *Server) restoreVersion(w http.ResponseWriter, r *http.Request, req restoreVersionRequest, content []byte, detail map[string]any) error {
	kept, err := s.modules.hostFiles.WriteKept(req.Path, string(content), req.ExpectedRevision)
	if err != nil {
		return mapFileError(err)
	}
	s.keepFileVersion(r, kept, fileversions.Save{Written: content, Reason: fileversions.ReasonRestore})
	detail["bytes"] = len(content)
	httpx.SetAudit(r, "file.version.restore", req.Path, detail)
	entry, err := s.modules.hostFiles.Stat(req.Path)
	if err != nil {
		return mapFileError(err)
	}
	httpx.JSON(w, http.StatusOK, struct {
		*files.Entry
		Revision string `json:"revision"`
	}{entry, contentguard.RevisionBytes(content)})
	return nil
}

func (s *Server) handleFileVersionRestore(w http.ResponseWriter, r *http.Request) error {
	var req restoreVersionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ref, err := req.validate()
	if err != nil {
		return err
	}
	if ref.edit == 0 {
		return httpx.BadRequest("a backup's copy is restored through /files/versions/restore-backup")
	}
	path, err := s.modules.hostFiles.Resolve(req.Path)
	if err != nil {
		return mapFileError(err)
	}
	content, err := s.versionContent(r, path, ref)
	if err != nil {
		return err
	}
	return s.restoreVersion(w, r, req, content, map[string]any{"version": req.Version})
}

// handleFileVersionRestoreBackup restores one file from a backup run. It is
// the Backups page's single-file restore, behind its capability: the copy is
// read from the run's verified archive by the backups package, and published
// through the Files save rather than written over the live file unguarded, so
// it conflicts on a stale revision and keeps what it replaces.
func (s *Server) handleFileVersionRestoreBackup(w http.ResponseWriter, r *http.Request) error {
	var req restoreVersionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ref, err := req.validate()
	if err != nil {
		return err
	}
	if ref.backup == 0 {
		return httpx.BadRequest("an edit version is restored through /files/versions/restore")
	}
	path, err := s.modules.hostFiles.Resolve(req.Path)
	if err != nil {
		return mapFileError(err)
	}
	content, err := s.versionContent(r, path, ref)
	if err != nil {
		httpx.SetAudit(r, "file.version.restore", req.Path, map[string]any{"run": ref.backup, "error": err.Error()})
		return err
	}
	return s.restoreVersion(w, r, req, content, map[string]any{"run": ref.backup})
}
