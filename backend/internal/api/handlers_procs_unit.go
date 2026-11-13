package api

import (
	"net/http"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/go-chi/chi/v5"
)

// handleUnitFiles is the unit's fragment and drop-ins as the host has them.
// A unit file's Environment= lines carry what the Environment property does,
// and SetCredential= carries a credential outright, so both are masked for
// anybody without system.admin, as handleUnitShow masks the property.
func (s *Server) handleUnitFiles(w http.ResponseWriter, r *http.Request) error {
	files, err := s.modules.systemd.Files(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		return mapProcsError(err)
	}
	if !httpx.MustPrincipal(r).Can(auth.CapSystemAdmin) {
		for i := range files.Files {
			files.Files[i].Content = procs.MaskUnitFileSecrets(files.Files[i].Content,
				dockerx.IsSecretEnvKey, dockerx.RedactedEnvValue)
		}
	}
	httpx.JSON(w, http.StatusOK, files)
	return nil
}

// handleUnitSecurity is systemd's exposure assessment of a service. A unit
// systemd will not assess answers available:false with the reason rather
// than an error: the page has a section to fill either way.
func (s *Server) handleUnitSecurity(w http.ResponseWriter, r *http.Request) error {
	report, err := s.modules.systemd.Security(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		return mapProcsError(err)
	}
	httpx.JSON(w, http.StatusOK, report)
	return nil
}

func (s *Server) handleUnitRelated(w http.ResponseWriter, r *http.Request) error {
	units, err := s.modules.systemd.Related(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		return mapProcsError(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"units": units})
	return nil
}

type unitKillRequest struct {
	Signal string `json:"signal"`
	// Whom is "main", the unit's main process, or "all", every process in
	// its cgroup; empty is "all".
	Whom string `json:"whom"`
}

func (s *Server) handleUnitKill(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	var req unitKillRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Whom == "" {
		req.Whom = "all"
	}
	req.Signal = strings.ToUpper(req.Signal)
	if req.Signal == "" {
		return httpx.BadRequest("signal is required")
	}
	if !procs.AllowedSignal(req.Signal) {
		return httpx.BadRequest("signal %q is not permitted", req.Signal)
	}
	if req.Whom != "main" && req.Whom != "all" {
		return httpx.BadRequest(`whom must be "main" or "all"`)
	}
	// No typed phrase, for the reason handleProcessSignal gives: signalling
	// is the control's whole purpose, and the dialog names the unit and the
	// signal.
	res, err := s.modules.systemd.Kill(r.Context(), name, req.Signal, req.Whom)
	if err != nil {
		return mapProcsError(err)
	}
	httpx.SetAudit(r, "systemd.kill", name, map[string]any{
		"signal": req.Signal, "whom": req.Whom, "exitCode": res.ExitCode,
	})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleUnitLimits(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	var req procs.UnitLimits
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if _, err := req.Assignments(); err != nil {
		return httpx.Refused(err)
	}
	res, err := s.modules.systemd.SetLimits(r.Context(), name, req)
	if err != nil {
		return mapProcsError(err)
	}
	detail := map[string]any{"persistent": req.Persistent, "exitCode": res.ExitCode}
	for key, value := range map[string]*string{"memoryMax": req.MemoryMax, "cpuQuota": req.CPUQuota, "tasksMax": req.TasksMax} {
		if value != nil {
			detail[key] = *value
		}
	}
	httpx.SetAudit(r, "systemd.set-property", name, detail)
	httpx.JSON(w, http.StatusOK, res)
	return nil
}
