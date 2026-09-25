package api

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// maxUploadBytes bounds a single uploaded file. Large transfers belong in the
// terminal with scp or rsync, not in a browser form post.
const maxUploadBytes = 2 << 30

func (s *Server) mountFileRoutes(r chi.Router) {
	r.Route("/files", func(r chi.Router) {
		r.Method(http.MethodGet, "/list", s.handle(s.handleFileList))
		r.Method(http.MethodGet, "/stat", s.handle(s.handleFileStat))
		r.Method(http.MethodGet, "/read", s.handle(s.handleFileRead))
		r.Method(http.MethodGet, "/download", s.handle(s.handleFileDownload))
		r.Method(http.MethodGet, "/raw", s.handle(s.handleFileRaw))
		r.Method(http.MethodGet, "/search", s.handle(s.handleFileSearch))
		r.Method(http.MethodGet, "/find", s.handle(s.handleFileFind))
		r.Method(http.MethodGet, "/preview", s.handle(s.handleFilePreview))
		r.Method(http.MethodGet, "/places", s.handle(s.handleFilePlaces))
		r.Method(http.MethodGet, "/complete", s.handle(s.handleFileComplete))
		r.Method(http.MethodGet, "/usage", s.handle(s.handleFileUsage))
		r.Method(http.MethodGet, "/checksum", s.handle(s.handleFileChecksum))
		r.Method(http.MethodGet, "/archive", s.handle(s.handleFileArchive))

		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapFileWrite))
			r.Method(http.MethodPut, "/write", s.handle(s.handleFileWrite))
			r.Method(http.MethodPost, "/upload", s.handle(s.handleFileUpload))
			r.Method(http.MethodPost, "/mkdir", s.handle(s.handleFileMkdir))
			r.Method(http.MethodPost, "/touch", s.handle(s.handleFileTouch))
			r.Method(http.MethodPost, "/move", s.handle(s.handleFileMove))
			r.Method(http.MethodPost, "/copy", s.handle(s.handleFileCopy))
			r.Method(http.MethodPost, "/symlink", s.handle(s.handleFileSymlink))
			r.Method(http.MethodPost, "/extract", s.handle(s.handleFileExtract))
			r.Method(http.MethodPut, "/bookmarks", s.handle(s.handleFileBookmarks))
			r.Method(http.MethodPut, "/colours", s.handle(s.handleFileColour))
			r.Method(http.MethodPut, "/colours/default", s.handle(s.handleFileDefaultColour))
		})
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			r.Method(http.MethodPost, "/chmod", s.handle(s.handleFileChmod))
			r.Method(http.MethodPost, "/chown", s.handle(s.handleFileChown))
		})
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/delete", s.handle(s.handleFileDelete))
		})
	})
}

func mapFileError(err error) error {
	switch {
	case errors.Is(err, files.ErrOutsideRoot):
		return httpx.Err(http.StatusForbidden, "outside_root", err.Error())
	case errors.Is(err, files.ErrTooLarge):
		return httpx.Err(http.StatusRequestEntityTooLarge, "too_large", err.Error())
	case errors.Is(err, files.ErrArchiveTooLarge), errors.Is(err, files.ErrArchiveTooManyEntries),
		errors.Is(err, files.ErrArchiveNoSpace):
		return httpx.Err(http.StatusRequestEntityTooLarge, "archive_limit", err.Error())
	case errors.Is(err, files.ErrIsDir), errors.Is(err, files.ErrNotDir):
		return httpx.Err(http.StatusBadRequest, "wrong_type", err.Error())
	case errors.Is(err, fs.ErrNotExist):
		return httpx.ErrNotFound
	case errors.Is(err, fs.ErrPermission):
		return httpx.Err(http.StatusForbidden, "permission_denied", err.Error())
	case errors.Is(err, fs.ErrExist):
		return httpx.Err(http.StatusConflict, "already_exists", err.Error())
	default:
		return httpx.BadRequest("%v", err)
	}
}

func (s *Server) handleFileList(w http.ResponseWriter, r *http.Request) error {
	listing, err := s.modules.files.List(r.URL.Query().Get("path"),
		r.URL.Query().Get("hidden") == "true")
	if err != nil {
		return mapFileError(err)
	}
	httpx.JSON(w, http.StatusOK, listing)
	return nil
}

func (s *Server) handleFileStat(w http.ResponseWriter, r *http.Request) error {
	entry, err := s.modules.files.Stat(r.URL.Query().Get("path"))
	if err != nil {
		return mapFileError(err)
	}
	httpx.JSON(w, http.StatusOK, entry)
	return nil
}

func (s *Server) handleFileRead(w http.ResponseWriter, r *http.Request) error {
	content, err := s.modules.files.Read(r.URL.Query().Get("path"))
	if err != nil {
		return mapFileError(err)
	}
	httpx.JSON(w, http.StatusOK, content)
	return nil
}

// handleFileDownload streams a file with http.ServeContent so range requests
// and resumption work — a browser download of a large file that cannot resume
// is a download that fails.
func (s *Server) handleFileDownload(w http.ResponseWriter, r *http.Request) error {
	path := r.URL.Query().Get("path")
	f, st, err := s.modules.files.Open(path)
	if err != nil {
		return mapFileError(err)
	}
	defer f.Close()
	if st.IsDir() {
		return httpx.BadRequest("use /api/files/archive to download a directory")
	}
	name := filepath.Base(st.Name())
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	http.ServeContent(w, r, name, st.ModTime(), f)
	return nil
}

func (s *Server) handleFileArchive(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	base := q.Get("base")
	paths := q["path"]
	if len(paths) == 0 {
		return httpx.BadRequest("at least one path query parameter is required")
	}
	format := files.FormatTarGz
	ext := ".tar.gz"
	if q.Get("format") == "zip" {
		format, ext = files.FormatZip, ".zip"
	}
	// Resolved before any header goes out: a refusal has to arrive as a JSON
	// error, not as a 200 whose attachment is an empty archive.
	baseDir, members, err := s.modules.files.ResolveArchive(base, paths)
	if err != nil {
		return mapFileError(err)
	}
	name := "archive"
	if len(paths) == 1 {
		name = filepath.Base(paths[0])
	} else if base != "" {
		name = filepath.Base(baseDir)
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition",
		mime.FormatMediaType("attachment", map[string]string{"filename": name + ext}))
	if err := s.modules.files.Compress(w, baseDir, members, format); err != nil {
		// The archive body has already begun; log rather than attempt a
		// status code that can no longer be sent.
		s.Log.Error("archive stream failed", "err", err, "paths", paths)
	}
	return nil
}

type writeFileRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (s *Server) handleFileWrite(w http.ResponseWriter, r *http.Request) error {
	var req writeFileRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := s.modules.files.Write(req.Path, req.Content); err != nil {
		return mapFileError(err)
	}
	httpx.SetAudit(r, "file.write", req.Path, map[string]any{"bytes": len(req.Content)})
	entry, err := s.modules.files.Stat(req.Path)
	if err != nil {
		return mapFileError(err)
	}
	httpx.JSON(w, http.StatusOK, entry)
	return nil
}

func (s *Server) handleFileUpload(w http.ResponseWriter, r *http.Request) error {
	dir := r.URL.Query().Get("path")
	if dir == "" {
		return httpx.BadRequest("path query parameter is required")
	}
	overwrite := r.URL.Query().Get("overwrite") == "true"

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		return httpx.BadRequest("expected a multipart upload: %v", err)
	}
	written := []string{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return uploadError(err)
		}
		if part.FormName() != "file" || part.FileName() == "" {
			part.Close()
			continue
		}
		// Only the base name is honoured: a browser (or a crafted client) can
		// put a path in the filename field, and joining it blindly would let
		// an upload land anywhere.
		name := filepath.Base(part.FileName())
		if name == "." || name == string(filepath.Separator) {
			part.Close()
			continue
		}
		_, err = s.modules.files.Upload(filepath.Join(dir, name), part, overwrite)
		part.Close()
		if err != nil {
			return uploadError(err)
		}
		written = append(written, name)
	}
	if len(written) == 0 {
		return httpx.BadRequest("no file part found in the upload")
	}
	httpx.SetAudit(r, "file.upload", dir, map[string]any{"files": written})
	httpx.JSON(w, http.StatusCreated, map[string]any{"uploaded": written, "path": dir})
	return nil
}

// uploadError tells a transfer that hit the request size limit apart from one
// the filesystem refused: the first is 413 with the limit named, so the page
// can say "too big" rather than "bad request", and the second is whatever the
// file operation would have said on its own.
func uploadError(err error) error {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return httpx.Err(http.StatusRequestEntityTooLarge, "too_large",
			fmt.Sprintf("the upload exceeds the %d GiB limit for one request", maxUploadBytes>>30))
	}
	if mapped := mapFileError(err); mapped != nil {
		if apiErr, ok := mapped.(*httpx.APIError); ok && apiErr.Code != "unknown" && apiErr.Status != http.StatusBadRequest {
			return mapped
		}
	}
	return httpx.BadRequest("upload failed: %v", err)
}

type pathRequest struct {
	Path      string `json:"path"`
	Mode      string `json:"mode,omitempty"`
	Recursive bool   `json:"recursive,omitempty"`
}

func (s *Server) handleFileMkdir(w http.ResponseWriter, r *http.Request) error {
	var req pathRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Path == "" {
		return httpx.BadRequest("path is required")
	}
	if err := s.modules.files.Mkdir(req.Path, 0); err != nil {
		return mapFileError(err)
	}
	httpx.SetAudit(r, "file.mkdir", req.Path, nil)
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleFileTouch(w http.ResponseWriter, r *http.Request) error {
	var req pathRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Path == "" {
		return httpx.BadRequest("path is required")
	}
	if err := s.modules.files.Touch(req.Path); err != nil {
		return mapFileError(err)
	}
	httpx.SetAudit(r, "file.create", req.Path, nil)
	httpx.NoContent(w)
	return nil
}

type moveRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Overwrite replaces whatever already sits at the destination. Without it
	// an occupied destination is a 409, which is what lets the page ask
	// "replace, keep both or skip" instead of finding out afterwards.
	Overwrite bool `json:"overwrite,omitempty"`
}

// An empty side of a move or copy resolves to the first configured root —
// which is to say, "move this to /". Nobody asks for that by leaving a field
// blank.
func (req moveRequest) validate() error {
	if req.From == "" || req.To == "" {
		return httpx.BadRequest("from and to are both required")
	}
	return nil
}

func (s *Server) handleFileMove(w http.ResponseWriter, r *http.Request) error {
	var req moveRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := req.validate(); err != nil {
		return err
	}
	src, dst, err := s.modules.files.MoveEnds(req.From, req.To)
	if err != nil {
		return mapFileError(err)
	}
	if err := s.modules.files.Move(req.From, req.To, req.Overwrite); err != nil {
		return mapFileError(err)
	}
	s.moveFileColours(r.Context(), src, dst)
	httpx.SetAudit(r, "file.move", req.From, map[string]any{"to": req.To, "overwrite": req.Overwrite})
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleFileCopy(w http.ResponseWriter, r *http.Request) error {
	var req moveRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := req.validate(); err != nil {
		return err
	}
	if err := s.modules.files.Copy(req.From, req.To, req.Overwrite); err != nil {
		return mapFileError(err)
	}
	httpx.SetAudit(r, "file.copy", req.From, map[string]any{"to": req.To, "overwrite": req.Overwrite})
	httpx.NoContent(w)
	return nil
}

type symlinkRequest struct {
	Target string `json:"target"`
	Link   string `json:"link"`
}

func (s *Server) handleFileSymlink(w http.ResponseWriter, r *http.Request) error {
	var req symlinkRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Target == "" || req.Link == "" {
		return httpx.BadRequest("target and link are both required")
	}
	if err := s.modules.files.Symlink(req.Target, req.Link); err != nil {
		return mapFileError(err)
	}
	httpx.SetAudit(r, "file.symlink", req.Link, map[string]any{"target": req.Target})
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleFileDelete(w http.ResponseWriter, r *http.Request) error {
	path := r.URL.Query().Get("path")
	if path == "" {
		return httpx.BadRequest("path query parameter is required")
	}
	recursive := r.URL.Query().Get("recursive") == "true"
	// Only a recursive delete is typed for. Deleting one file is what a file
	// manager is, done constantly, and there is no undo anywhere in this
	// product to make the typing worth it — but a recursive delete removes a
	// tree the operator cannot see the whole of from the row they clicked, and
	// that is the one where reading the name back matters.
	if recursive {
		if err := httpx.RequireTypedConfirmation(w, r, filepath.Base(path)); err != nil {
			return err
		}
	}
	entry, err := s.modules.files.ResolveEntry(path)
	if err != nil {
		return mapFileError(err)
	}
	if err := s.modules.files.Delete(path, recursive); err != nil {
		return mapFileError(err)
	}
	s.dropFileColours(r.Context(), entry)
	httpx.SetAudit(r, "file.delete", path, map[string]any{"recursive": recursive})
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleFileChmod(w http.ResponseWriter, r *http.Request) error {
	var req pathRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := s.modules.files.Chmod(req.Path, req.Mode, req.Recursive); err != nil {
		return mapFileError(err)
	}
	httpx.SetAudit(r, "file.chmod", req.Path, map[string]any{"mode": req.Mode, "recursive": req.Recursive})
	httpx.NoContent(w)
	return nil
}

type chownRequest struct {
	Path      string `json:"path"`
	Owner     string `json:"owner"`
	Group     string `json:"group"`
	Recursive bool   `json:"recursive"`
}

func (s *Server) handleFileChown(w http.ResponseWriter, r *http.Request) error {
	var req chownRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := s.modules.files.Chown(req.Path, req.Owner, req.Group, req.Recursive); err != nil {
		return mapFileError(err)
	}
	httpx.SetAudit(r, "file.chown", req.Path,
		map[string]any{"owner": req.Owner, "group": req.Group, "recursive": req.Recursive})
	httpx.NoContent(w)
	return nil
}

type extractRequest struct {
	Archive     string `json:"archive"`
	Destination string `json:"destination"`
}

func (s *Server) handleFileExtract(w http.ResponseWriter, r *http.Request) error {
	var req extractRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 10*time.Minute)
	defer cancel()
	written, err := s.modules.files.Extract(ctx, req.Archive, req.Destination)
	if err != nil {
		return mapFileError(err)
	}
	httpx.SetAudit(r, "file.extract", req.Archive,
		map[string]any{"destination": req.Destination, "entries": len(written)})
	httpx.JSON(w, http.StatusOK, map[string]any{"extracted": len(written), "destination": req.Destination})
	return nil
}

func (s *Server) handleFileSearch(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if q.Get("q") == "" {
		return httpx.BadRequest("q query parameter is required")
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	hits, err := s.modules.files.Search(ctx, files.SearchOptions{
		Root:       defaultStr(q.Get("path"), "/"),
		Query:      q.Get("q"),
		Content:    q.Get("content") == "true",
		Regex:      q.Get("regex") == "true",
		IgnoreCase: q.Get("ignoreCase") != "false",
		MaxDepth:   atoiDefault(q.Get("depth"), 12),
		Limit:      atoiDefault(q.Get("limit"), 500),
	})
	if err != nil {
		return mapFileError(err)
	}
	httpx.JSON(w, http.StatusOK, hits)
	return nil
}
