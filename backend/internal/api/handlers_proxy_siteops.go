package api

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// mountSiteOpsRoutes holds what is done to a site as a whole, as opposed to
// what the site form writes into it. It shares /proxy/sites with the builder
// but not its file, so the two grow independently.
func (s *Server) mountSiteOpsRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		// Admin rather than read like /proxy/config: a download or an
		// import hands over or writes a whole file in one request.
		r.Method(http.MethodGet, "/{name}/download", s.handle(s.handleSiteDownload))
		r.Method(http.MethodPost, "/import/preview", s.handle(s.handleSiteImportPreview))
		r.Method(http.MethodPost, "/import", s.handle(s.handleSiteImport))
		// Destructive whatever the action: a bulk disable takes sites
		// offline and a bulk delete removes their files, and an enable is
		// the same switch a single toggle gates the same way.
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodPost, "/bulk", s.handle(s.handleSitesBulk))
			// Destructive as a delete is: the old name stops existing, and
			// anything that reached the site by it — a bookmark, a script,
			// a log path — no longer does.
			r.Method(http.MethodPost, "/{name}/rename", s.handle(s.handleSiteRename))
		})
	})
}

type sitesBulkRequest struct {
	Action proxysvc.BulkAction `json:"action"`
	Names  []string            `json:"names"`
}

type sitesBulkResult struct {
	Action      proxysvc.BulkAction    `json:"action"`
	Changed     []string               `json:"changed"`
	Unchanged   []string               `json:"unchanged"`
	Reloaded    bool                   `json:"reloaded"`
	ReloadError string                 `json:"reloadError,omitempty"`
	Reload      *proxysvc.ReloadResult `json:"reload,omitempty"`
}

// handleSitesBulk enables, disables or deletes several sites as one change:
// one nginx -t over all of them, every site put back on a refusal, then one
// reload.
func (s *Server) handleSitesBulk(w http.ResponseWriter, r *http.Request) error {
	var req sitesBulkRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	const action = "proxy.sites.bulk"
	detail := map[string]any{"action": req.Action, "names": req.Names}
	res, reload, err := s.modules.proxy.BulkSites(r.Context(), req.Action, req.Names, true)
	if err != nil {
		var refused *proxysvc.RefusedError
		if errors.As(err, &refused) {
			return refusedLinkChange(r, action, "", detail, err)
		}
		// Recorded too: a refusal names which site stopped the whole
		// change, and that nothing was changed.
		detail["result"] = "refused"
		detail["reason"] = err.Error()
		httpx.SetAudit(r, action, "", detail)
		return mapProxyError(err)
	}
	link := vhostLinkResult{}
	link.reloaded(reload)
	out := sitesBulkResult{
		Action: req.Action, Changed: res.Changed, Unchanged: res.Unchanged,
		Reloaded: link.Reloaded, ReloadError: link.ReloadError, Reload: link.Reload,
	}
	detail["changed"] = res.Changed
	httpx.SetAudit(r, action, "", link.auditDetail(detail))
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

type siteRenameRequest struct {
	To     string `json:"to"`
	Reload bool   `json:"reload"`
}

type siteRenameResult struct {
	vhostLinkResult
	From       string   `json:"from"`
	Path       string   `json:"path"`
	Rerendered bool     `json:"rerendered"`
	Warnings   []string `json:"warnings"`
}

// handleSiteRename moves a site to a new name behind one nginx test, and
// reloads when asked.
func (s *Server) handleSiteRename(w http.ResponseWriter, r *http.Request) error {
	from := httpx.URLParam(r, "name")
	var req siteRenameRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	const action = "proxy.site.rename"
	detail := map[string]any{"to": req.To}
	res, reload, err := s.modules.proxy.RenameSite(r.Context(), from, req.To, req.Reload)
	if err != nil {
		var refused *proxysvc.RefusedError
		if errors.As(err, &refused) {
			return refusedLinkChange(r, action, from, detail, err)
		}
		detail["result"] = "refused"
		detail["reason"] = err.Error()
		httpx.SetAudit(r, action, from, detail)
		return mapProxyError(err)
	}
	out := siteRenameResult{
		vhostLinkResult: vhostLinkResult{Name: res.Name, Enabled: res.Enabled},
		From:            from, Path: res.Path, Rerendered: res.Rerendered, Warnings: res.Warnings,
	}
	out.reloaded(reload)
	detail["to"], detail["path"], detail["rerendered"] = res.Name, res.Path, res.Rerendered
	httpx.SetAudit(r, action, from, out.auditDetail(detail))
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// mountSiteFileRoutes is the export of every site and the backups deletes
// leave beside them. Mounted under /proxy rather than /proxy/sites, where
// GET /export and GET /backups would take the place of reading a site of
// that name into the form.
func (s *Server) mountSiteFileRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodGet, "/sites-export", s.handle(s.handleSitesExport))
		r.Method(http.MethodGet, "/site-backups", s.handle(s.handleSiteBackups))
		r.Method(http.MethodPost, "/site-backups/{dir}/{file}/restore", s.handle(s.handleSiteBackupRestore))
		// Admin: the upload is a whole NPM database, passwords included,
		// and the apply writes sites, streams and password files.
		r.Method(http.MethodPost, "/import/npm", s.handle(s.handleNPMImportPreview))
		r.Method(http.MethodPost, "/import/npm/apply", s.handle(s.handleNPMImportApply))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/site-backups/{dir}/{file}", s.handle(s.handleSiteBackupPurge))
		})
	})
}

// handleSiteDownload hands over one site's file as it is on disk.
func (s *Server) handleSiteDownload(w http.ResponseWriter, r *http.Request) error {
	name := httpx.URLParam(r, "name")
	file, content, err := s.modules.proxy.SiteDownload(name)
	if err != nil {
		return mapProxyError(err)
	}
	httpx.SetAudit(r, "proxy.site.download", name, map[string]any{"bytes": len(content)})
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file}))
	_, _ = w.Write(content)
	return nil
}

// handleSitesExport hands over every site as a tar.gz with a manifest.
func (s *Server) handleSitesExport(w http.ResponseWriter, r *http.Request) error {
	at := time.Now()
	archive, err := s.modules.proxy.ExportSites(at)
	if err != nil {
		return httpx.Internal(err)
	}
	name := fmt.Sprintf("nginx-sites-%s.tar.gz", at.UTC().Format("2006-01-02"))
	httpx.SetAudit(r, "proxy.sites.export", "", map[string]any{"bytes": len(archive)})
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	_, _ = w.Write(archive)
	return nil
}

type siteImportRequest struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	Enable  bool   `json:"enable"`
	Reload  bool   `json:"reload"`
}

type sitePlacementResult struct {
	*proxysvc.SitePlacement
	Reloaded    bool                   `json:"reloaded"`
	ReloadError string                 `json:"reloadError,omitempty"`
	Reload      *proxysvc.ReloadResult `json:"reload,omitempty"`
}

// handleSiteImportPreview tests a server block in place as a new site and
// takes it back out, whatever nginx says.
func (s *Server) handleSiteImportPreview(w http.ResponseWriter, r *http.Request) error {
	var req siteImportRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	res, _, err := s.modules.proxy.ImportSite(r.Context(), req.Name, req.Content, req.Enable, false, false)
	if err != nil {
		return mapProxyError(err)
	}
	// Audited though nothing stays: nginx -t ran with the file in place.
	httpx.SetAudit(r, "proxy.site.import.preview", req.Name, map[string]any{"valid": res.Validation.Valid, "bytes": len(req.Content)})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

// handleSiteImport adds a server block as a new site behind one nginx test,
// and reloads when asked.
func (s *Server) handleSiteImport(w http.ResponseWriter, r *http.Request) error {
	var req siteImportRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	const action = "proxy.site.import"
	detail := map[string]any{"enable": req.Enable, "bytes": len(req.Content)}
	res, reload, err := s.modules.proxy.ImportSite(r.Context(), req.Name, req.Content, req.Enable, true, req.Reload)
	return s.placedSite(w, r, action, req.Name, detail, res, reload, err)
}

// placedSite answers an import or a restore the way a link change is
// answered: a refusal is nginx's first error, and a failed reload is
// reported beside a change that stands.
func (s *Server) placedSite(w http.ResponseWriter, r *http.Request, action, target string, detail map[string]any, res *proxysvc.SitePlacement, reload *proxysvc.LinkReload, err error) error {
	if err != nil {
		var refused *proxysvc.RefusedError
		if errors.As(err, &refused) {
			return refusedLinkChange(r, action, target, detail, err)
		}
		detail["result"] = "refused"
		detail["reason"] = err.Error()
		httpx.SetAudit(r, action, target, detail)
		return mapProxyError(err)
	}
	link := vhostLinkResult{}
	link.reloaded(reload)
	detail["name"], detail["path"], detail["enabled"] = res.Name, res.Path, res.Enabled
	httpx.SetAudit(r, action, target, link.auditDetail(detail))
	httpx.JSON(w, http.StatusOK, sitePlacementResult{
		SitePlacement: res, Reloaded: link.Reloaded, ReloadError: link.ReloadError, Reload: link.Reload,
	})
	return nil
}

func (s *Server) handleSiteBackups(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, s.modules.proxy.ListSiteBackups())
	return nil
}

type siteBackupRestoreRequest struct {
	As     string `json:"as"`
	Enable bool   `json:"enable"`
	Reload bool   `json:"reload"`
}

// handleSiteBackupRestore puts a backup back as a site, tested as an import is.
func (s *Server) handleSiteBackupRestore(w http.ResponseWriter, r *http.Request) error {
	dir, file := httpx.URLParam(r, "dir"), httpx.URLParam(r, "file")
	var req siteBackupRestoreRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	const action = "proxy.site.backup.restore"
	detail := map[string]any{"dir": dir, "as": req.As, "enable": req.Enable}
	res, reload, err := s.modules.proxy.RestoreSiteBackup(r.Context(), dir, file, req.As, req.Enable, req.Reload)
	return s.placedSite(w, r, action, file, detail, res, reload, err)
}

// handleSiteBackupPurge deletes one backup file for good.
func (s *Server) handleSiteBackupPurge(w http.ResponseWriter, r *http.Request) error {
	dir, file := httpx.URLParam(r, "dir"), httpx.URLParam(r, "file")
	const action = "proxy.site.backup.purge"
	b, err := s.modules.proxy.PurgeSiteBackup(dir, file)
	if err != nil {
		httpx.SetAudit(r, action, file, map[string]any{"dir": dir, "result": "refused", "reason": err.Error()})
		return mapProxyError(err)
	}
	httpx.SetAudit(r, action, file, map[string]any{"dir": dir, "path": b.Path, "bytes": b.Size})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// npmUploadAllowance covers the multipart framing around the database.
const npmUploadAllowance = 64 << 10

// handleNPMImportPreview takes an Nginx Proxy Manager database.sqlite as
// the "file" part, copies it to a private temporary file, maps it, and
// deletes the copy before answering. The plan stays behind the token.
func (s *Server) handleNPMImportPreview(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, proxysvc.MaxNPMDatabaseBytes+npmUploadAllowance)
	path, size, err := receiveNPMDatabase(r)
	if path != "" {
		defer os.Remove(path)
	}
	if err != nil {
		return err
	}
	preview, err := s.modules.proxy.PreviewNPMImport(r.Context(), path)
	if err != nil {
		return mapProxyError(err)
	}
	counts := map[string]int{}
	for _, item := range preview.Items {
		counts[item.Kind]++
	}
	// Audited though nothing on the host changes: an upload of a database
	// holding passwords is worth a line.
	httpx.SetAudit(r, "proxy.import.npm.preview", "", map[string]any{"bytes": size, "items": counts})
	httpx.JSON(w, http.StatusOK, preview)
	return nil
}

func receiveNPMDatabase(r *http.Request) (string, int64, error) {
	tooLarge := func(err error) bool {
		var limit *http.MaxBytesError
		return errors.As(err, &limit)
	}
	reader, err := r.MultipartReader()
	if err != nil {
		return "", 0, httpx.BadRequest("expected a multipart upload of database.sqlite: %v", err)
	}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return "", 0, httpx.BadRequest("no file was uploaded")
		}
		if err != nil {
			if tooLarge(err) {
				return "", 0, httpx.Err(http.StatusRequestEntityTooLarge, "too_large", "the database is larger than 50 MB")
			}
			return "", 0, httpx.BadRequest("malformed upload: %v", err)
		}
		if part.FormName() != "file" {
			part.Close()
			continue
		}
		dir := filepath.Join(os.TempDir(), "just-dashboard")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			part.Close()
			return "", 0, httpx.Internal(err)
		}
		f, err := os.CreateTemp(dir, "npm-*.sqlite")
		if err != nil {
			part.Close()
			return "", 0, httpx.Internal(err)
		}
		size, err := io.Copy(f, io.LimitReader(part, proxysvc.MaxNPMDatabaseBytes+1))
		part.Close()
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		switch {
		case err != nil && tooLarge(err), size > proxysvc.MaxNPMDatabaseBytes:
			return f.Name(), size, httpx.Err(http.StatusRequestEntityTooLarge, "too_large", "the database is larger than 50 MB")
		case err != nil:
			return f.Name(), size, httpx.BadRequest("could not read the upload: %v", err)
		}
		return f.Name(), size, nil
	}
}

type npmImportApplyRequest struct {
	Token string   `json:"token"`
	IDs   []string `json:"ids"`
}

type npmImportApplyResult struct {
	*proxysvc.NPMApplyResult
	Reloaded    bool                   `json:"reloaded"`
	ReloadError string                 `json:"reloadError,omitempty"`
	Reload      *proxysvc.ReloadResult `json:"reload,omitempty"`
}

// handleNPMImportApply adds the selected items of a preview behind one
// nginx test, and reloads.
func (s *Server) handleNPMImportApply(w http.ResponseWriter, r *http.Request) error {
	var req npmImportApplyRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	const action = "proxy.import.npm"
	detail := map[string]any{"ids": req.IDs}
	res, reload, err := s.modules.proxy.ApplyNPMImport(r.Context(), req.Token, req.IDs, true)
	if err != nil {
		var refused *proxysvc.RefusedError
		if errors.As(err, &refused) {
			return refusedLinkChange(r, action, "", detail, err)
		}
		detail["result"] = "refused"
		detail["reason"] = err.Error()
		httpx.SetAudit(r, action, "", detail)
		if errors.Is(err, proxysvc.ErrNPMPreviewGone) {
			return httpx.Err(http.StatusGone, "preview_gone", err.Error())
		}
		return mapProxyError(err)
	}
	link := vhostLinkResult{}
	link.reloaded(reload)
	detail["sites"], detail["disabled"], detail["streams"], detail["authFiles"] = res.Sites, res.Disabled, res.Streams, res.AuthFiles
	httpx.SetAudit(r, action, "", link.auditDetail(detail))
	httpx.JSON(w, http.StatusOK, npmImportApplyResult{
		NPMApplyResult: res, Reloaded: link.Reloaded, ReloadError: link.ReloadError, Reload: link.Reload,
	})
	return nil
}
