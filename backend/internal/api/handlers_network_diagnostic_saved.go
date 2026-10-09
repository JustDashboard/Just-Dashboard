package api

import (
	"net/http"
	"strconv"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netdiag"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/go-chi/chi/v5"
)

// History, saving an interactive result, trusted SSH fingerprints and saved
// Wake-on-LAN devices. All sit under the diagnostics route, so they carry its
// system.admin requirement; deletions are mounted inside s.destructive.

func (s *Server) handleDiagnosticHistory(w http.ResponseWriter, r *http.Request) error {
	history, err := s.modules.diagnostics.History(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return mapDiagnosticError(err)
	}
	diagnosticPrivate(w)
	httpx.JSON(w, http.StatusOK, history)
	return nil
}

func (s *Server) handleDiagnosticSaveResult(w http.ResponseWriter, r *http.Request) error {
	httpx.SetAudit(r, "network.diagnostic.save_result", "", nil)
	var req struct {
		ResultID string `json:"resultId"`
		Name     string `json:"name"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	owner := httpx.MustPrincipal(r).Username()
	held, err := quickResults.take(owner, req.ResultID)
	if err != nil {
		return httpx.Err(http.StatusGone, "result_expired", err.Error())
	}
	run, err := s.modules.diagnostics.Adopt(r.Context(), req.Name, held.request, &held.result, owner, held.started, held.ended)
	if err != nil {
		// A refused save keeps the hold, so a retry still sends no probe.
		quickResults.restore(req.ResultID, held)
		return mapDiagnosticError(err)
	}
	httpx.SetAudit(r, "network.diagnostic.save_result", run.ID, map[string]any{"name": run.Name, "tool": run.Request.Tool, "target": run.Request.Target})
	diagnosticPrivate(w)
	httpx.JSON(w, http.StatusCreated, run)
	return nil
}

func (s *Server) handleSSHTrustList(w http.ResponseWriter, r *http.Request) error {
	entries, err := s.modules.diagnostics.SSHTrust(r.Context())
	if err != nil {
		return mapDiagnosticError(err)
	}
	diagnosticPrivate(w)
	httpx.JSON(w, http.StatusOK, entries)
	return nil
}

func (s *Server) handleSSHTrustSave(w http.ResponseWriter, r *http.Request) error {
	return s.saveSSHTrust(w, r, false)
}

// handleSSHTrustReplace overwrites saved fingerprints; it is mounted inside
// s.destructive because it erases what later scans compare against.
func (s *Server) handleSSHTrustReplace(w http.ResponseWriter, r *http.Request) error {
	return s.saveSSHTrust(w, r, true)
}

// saveSSHTrust takes observed keys only from a scan the server still holds,
// with that scan's own target and port, so keys read from one host can never
// be saved as trusted for another. Entered fingerprints carry their target.
func (s *Server) saveSSHTrust(w http.ResponseWriter, r *http.Request, replace bool) error {
	action := "network.ssh_trust.save"
	if replace {
		action = "network.ssh_trust.replace"
	}
	httpx.SetAudit(r, action, "", nil)
	var req struct {
		Target   string                 `json:"target"`
		Port     int                    `json:"port"`
		Keys     []netsec.SSHTrustedKey `json:"keys"`
		Source   string                 `json:"source"`
		ResultID string                 `json:"resultId"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	owner := httpx.MustPrincipal(r).Username()
	switch req.Source {
	case "observed":
		held, err := quickResults.peek(owner, req.ResultID)
		if err != nil {
			return httpx.Err(http.StatusGone, "result_expired", err.Error())
		}
		if held.request.Tool != "ssh" {
			return httpx.BadRequest("that result is not an SSH host key scan")
		}
		req.Target, req.Port, req.Keys = held.request.Target, held.request.Port, netsec.ObservedSSHKeys(&held.result)
	case "entered":
	default:
		return httpx.BadRequest("source is observed (trusted from a held scan) or entered (copied out of band)")
	}
	target, keys, err := netsec.ValidateSSHTrust(req.Target, req.Port, req.Keys)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	fingerprints := make([]string, 0, len(keys))
	for _, k := range keys {
		fingerprints = append(fingerprints, k.Type+" "+k.Fingerprint)
	}
	httpx.SetAudit(r, action, target, map[string]any{"source": req.Source, "keys": fingerprints})
	entry, err := s.modules.diagnostics.SaveSSHTrust(r.Context(), netsec.SSHTrust{Target: target, Keys: keys, Source: req.Source, SavedBy: owner}, replace)
	if err != nil {
		return mapDiagnosticError(err)
	}
	httpx.JSON(w, http.StatusOK, entry)
	return nil
}

func (s *Server) handleSSHTrustForget(w http.ResponseWriter, r *http.Request) error {
	host := r.URL.Query().Get("host")
	port, _ := strconv.Atoi(r.URL.Query().Get("port"))
	if !netsec.ValidTarget(host) || port < 0 || port > 65535 {
		httpx.SetAudit(r, "network.ssh_trust.forget", host, nil)
		return httpx.BadRequest("host is the saved host name or address and port its SSH port")
	}
	target := netsec.SSHTrustTarget(host, port)
	httpx.SetAudit(r, "network.ssh_trust.forget", target, nil)
	if err := s.modules.diagnostics.ForgetSSHTrust(r.Context(), target); err != nil {
		return mapDiagnosticError(err)
	}
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleWakeDeviceList(w http.ResponseWriter, r *http.Request) error {
	devices, err := s.modules.diagnostics.WakeDevices(r.Context())
	if err != nil {
		return mapDiagnosticError(err)
	}
	diagnosticPrivate(w)
	httpx.JSON(w, http.StatusOK, devices)
	return nil
}

func (s *Server) handleWakeDeviceSave(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "device")
	httpx.SetAudit(r, "network.wol_device.save", id, nil)
	var device netdiag.WakeDevice
	if err := httpx.DecodeJSON(r, &device); err != nil {
		return err
	}
	device.ID = id
	httpx.SetAudit(r, "network.wol_device.save", id, map[string]any{"name": device.Name, "mac": device.MAC, "interface": device.Interface, "verify": device.Verify, "port": device.Port})
	saved, err := s.modules.diagnostics.SaveWakeDevice(r.Context(), device, httpx.MustPrincipal(r).Username())
	if err != nil {
		return mapDiagnosticError(err)
	}
	httpx.SetAudit(r, "network.wol_device.save", saved.ID, map[string]any{"name": saved.Name, "mac": saved.MAC, "interface": saved.Interface, "verify": saved.Verify, "port": saved.Port})
	status := http.StatusOK
	if id == "" {
		status = http.StatusCreated
	}
	httpx.JSON(w, status, saved)
	return nil
}

func (s *Server) handleWakeDeviceDelete(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "device")
	httpx.SetAudit(r, "network.wol_device.delete", id, nil)
	if err := s.modules.diagnostics.DeleteWakeDevice(r.Context(), id); err != nil {
		return mapDiagnosticError(err)
	}
	httpx.NoContent(w)
	return nil
}
