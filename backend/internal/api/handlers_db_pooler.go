package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/docker/docker/errdefs"
	"github.com/go-chi/chi/v5"
)

// The pooler's routes. What a pooler is, and how it is kept, is in
// database_pooler.go; this file is the reading, the two changes and the
// statistics.

func (s *Server) mountDatabasePoolerRoutes(r chi.Router) {
	// Read surface: the pooler is a fact about the connection, like its
	// exposure, and the statistics are counters like the activity list.
	r.Method(http.MethodGet, "/{id}/pooler", s.handle(s.handleDBPooler))
	r.Method(http.MethodGet, "/{id}/pooler/stats", s.handle(s.handleDBPoolerStats))
	r.Group(func(r chi.Router) {
		// Running a container and, for auth_query, creating an account in
		// the database are an administrator's, as the access switch is.
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPut, "/{id}/pooler", s.handle(s.handleDBPoolerPut))
		// Removing it drops the connections going through it.
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{id}/pooler", s.handle(s.handleDBPoolerDelete))
		})
	})
}

// poolerView is what the connection's Pooling page reads.
type poolerView struct {
	// Available is whether a pooler can be turned on for this connection, and
	// Reason why not. A connection that already has one is always available.
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	Enabled   bool   `json:"enabled"`

	Settings dbx.PoolerSettings `json:"settings"`
	Defaults dbx.PoolerSettings `json:"defaults"`
	Limits   poolerLimits       `json:"limits"`

	// State is the container's: running, exited, created, or missing when the
	// record has no container behind it any more.
	State     string     `json:"state,omitempty"`
	Container string     `json:"container,omitempty"`
	Image     string     `json:"image"`
	Network   string     `json:"network,omitempty"`
	Exposure  dbExposure `json:"exposure,omitempty"`
	// Port is the host port it is published on, and LinkedHost the name a
	// container on an environment network reaches it by.
	Port       int    `json:"port,omitempty"`
	LinkedHost string `json:"linkedHost"`
	LinkedPort int    `json:"linkedPort"`
	// Reference is what a deployment's variable holds to get the pooled string.
	Reference string `json:"reference"`
	// InSync is whether the files the container holds were made from the
	// connection's current credentials.
	InSync    bool       `json:"inSync"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
}

type poolerLimits struct {
	PoolSize      int `json:"poolSize"`
	MaxClientConn int `json:"maxClientConn"`
}

func (s *Server) describePooler(ctx context.Context, conn *dbConnection, dsn string) (*poolerView, error) {
	view := &poolerView{
		Defaults: dbx.DefaultPoolerSettings(), Settings: dbx.DefaultPoolerSettings(),
		Limits: poolerLimits{PoolSize: dbx.PoolerMaxPoolSize, MaxClientConn: dbx.PoolerMaxClientConn},
		Image:  dbx.PoolerImage, LinkedHost: poolerDNSName(conn.ID), LinkedPort: dbx.PoolerListenPort,
		Reference: fmt.Sprintf("${{database.%d.pooled}}", conn.ID),
	}
	rec, err := s.poolerRecord(ctx, conn.ID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		if _, err := s.poolerTarget(ctx, conn, dsn); err != nil {
			view.Reason = err.Error()
			return view, nil
		}
		view.Available = true
		return view, nil
	}
	view.Available, view.Enabled = true, true
	view.Settings, view.Container, view.Network = rec.Settings, rec.Container, rec.Network
	view.CreatedAt = &rec.CreatedAt
	_, _, sha, filesErr := poolerFiles(rec, dsn)
	view.InSync = filesErr == nil && sha == rec.ConfigSHA
	view.State = "missing"
	if s.modules.docker == nil {
		return view, nil
	}
	detail, err := s.modules.docker.Inspect(ctx, rec.Container)
	switch {
	case errdefs.IsNotFound(err):
		return view, nil
	case err != nil:
		return nil, s.dockerErr(err)
	}
	view.State = detail.State
	if spec, err := s.modules.docker.SpecOf(ctx, detail.ID); err == nil {
		ip, port := poolerBinding(spec)
		view.Port = port
		view.Exposure = exposureOf(&dbServer{binding: &dockerx.Port{IP: ip}})
	}
	return view, nil
}

func (s *Server) handleDBPooler(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	view, err := s.describePooler(ctx, conn, dsn)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

// poolerStatsView is one look at PgBouncer's console.
type poolerStatsView struct {
	*dbx.PoolerReading
	At time.Time `json:"at"`
}

// handleDBPoolerStats answers SHOW POOLS and SHOW STATS. A pooler that is not
// running has no console to ask, which is a state (409) and not a failure.
func (s *Server) handleDBPoolerStats(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	if _, _, err := s.dbConnRow(r.Context(), id); err != nil {
		return err
	}
	rec, err := s.poolerRecord(r.Context(), id)
	if err != nil {
		return err
	}
	if rec == nil {
		return httpx.Err(http.StatusConflict, "no_pooler", "this database has no connection pooler")
	}
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	detail, err := s.modules.docker.Inspect(ctx, rec.Container)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return httpx.Err(http.StatusConflict, "pooler_missing", "the pooler's container is gone; turn it on again to rebuild it")
		}
		return s.dockerErr(err)
	}
	if detail.State != "running" {
		return httpx.Err(http.StatusConflict, "pooler_not_running", "the pooler is "+detail.State+"; it starts with its database")
	}
	spec, err := s.modules.docker.SpecOf(ctx, detail.ID)
	if err != nil {
		return s.dockerErr(err)
	}
	ip, port := poolerBinding(spec)
	if ip == "0.0.0.0" {
		ip = "127.0.0.1"
	}
	dsn := dbx.PoolerAdminDSN(ip, port, rec.Secrets.Stats)
	reading, err := dbx.ReadPooler(ctx, dsn)
	if err != nil {
		return retryable(httpx.Err(http.StatusBadGateway, "pooler_unreachable", connectError(dsn, err)), err)
	}
	httpx.JSON(w, http.StatusOK, poolerStatsView{PoolerReading: reading, At: time.Now().UTC()})
	return nil
}

// handleDBPoolerPut turns the pooler on, or changes its settings. It is one
// request because the two are the same act: say how it should be, and make it
// so (ensurePooler is safe to run again).
func (s *Server) handleDBPoolerPut(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var settings dbx.PoolerSettings
	if err := httpx.DecodeJSON(r, &settings); err != nil {
		return err
	}
	if err := settings.Validate(); err != nil {
		return httpx.BadRequest("%v", err)
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	// The image may have to be pulled.
	ctx, cancel := timeoutCtx(r, 5*time.Minute)
	defer cancel()
	target, err := s.poolerTarget(ctx, conn, dsn)
	if err != nil {
		return err
	}
	rec, err := s.poolerRecord(ctx, id)
	if err != nil {
		return err
	}
	created := rec == nil
	previousAuth := ""
	if created {
		auth, err := generatePassword()
		if err != nil {
			return httpx.Internal(err)
		}
		stats, err := generatePassword()
		if err != nil {
			return httpx.Internal(err)
		}
		rec = &poolerRecord{
			ConnectionID: id, Container: strings.TrimPrefix(target.detail.Name, "/") + "-pgbouncer",
			Network: poolerNetworkName(id), Secrets: poolerSecrets{Auth: auth, Stats: stats},
		}
	} else {
		previousAuth = rec.Settings.AuthMode
	}
	rec.Settings = settings
	detail := map[string]any{
		"container": rec.Container, "image": dbx.PoolerImage, "database": target.server.container.Name,
		"poolMode": settings.PoolMode, "poolSize": settings.PoolSize, "maxClientConn": settings.MaxClientConn,
		"authMode": settings.AuthMode, "created": created,
	}
	action := "database.pooler.update"
	if created {
		action = "database.pooler.enable"
	}

	if settings.AuthMode == dbx.PoolerAuthQuery {
		if err := s.setupPoolerAuth(ctx, conn, dsn, rec.Secrets.Auth); err != nil {
			detail["error"] = err.Error()
			httpx.SetAudit(r, action, conn.Name, detail)
			return err
		}
	}
	change, err := s.ensurePooler(ctx, r, target, rec, dsn, !created && previousAuth != settings.AuthMode)
	if err != nil {
		if created {
			// Nothing was recorded, so nothing would ever remove what was made.
			_ = s.removePoolerResources(ctx, rec)
		}
		detail["error"] = err.Error()
		httpx.SetAudit(r, action, conn.Name, detail)
		return err
	}
	// The database keeps nothing of auth_query once the pooler stops using it.
	warnings := []string{}
	if previousAuth == dbx.PoolerAuthQuery && settings.AuthMode != dbx.PoolerAuthQuery {
		if err := s.removePoolerAuth(ctx, conn, dsn); err != nil {
			warnings = append(warnings, "the "+dbx.PoolerAuthRole+" account and its function could not be removed from the database: "+err.Error())
		}
	}
	detail["port"], detail["recreated"], detail["reloaded"], detail["restarted"] = rec.HostPort, change.Recreated, change.Reloaded, change.Restarted
	detail["exposure"] = exposureOfBinding(target.bindIP)
	if change.Firewall != "" {
		detail["firewall"] = change.Firewall
	}
	if change.FirewallErr != nil {
		detail["firewallError"] = change.FirewallErr.Error()
	}
	if len(warnings) > 0 {
		detail["warnings"] = warnings
	}
	httpx.SetAudit(r, action, conn.Name, detail)

	view, err := s.describePooler(ctx, conn, dsn)
	if err != nil {
		return err
	}
	resp := map[string]any{"pooler": view, "warnings": warnings}
	if change.Firewall != "" {
		resp["firewall"] = change.Firewall
	}
	if change.FirewallErr != nil {
		resp["firewallError"] = change.FirewallErr.Error()
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.JSON(w, status, resp)
	return nil
}

// exposureOfBinding names how far a host address reaches, the way the access
// reading names the database's.
func exposureOfBinding(ip string) dbExposure {
	return exposureOf(&dbServer{binding: &dockerx.Port{IP: ip}})
}

// handleDBPoolerDelete removes the pooler: the container and its network, and
// auth_query's role and function. The database, its data and the connection
// are left exactly as they were.
func (s *Server) handleDBPoolerDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 2*time.Minute)
	defer cancel()
	rec, err := s.poolerRecord(ctx, id)
	if err != nil {
		return err
	}
	if rec == nil {
		return httpx.ErrNotFound
	}
	detail := map[string]any{"container": rec.Container, "port": rec.HostPort}
	if s.modules.docker == nil {
		return httpx.Err(http.StatusServiceUnavailable, "docker_unavailable", "this host has no Docker socket")
	}
	if err := s.removePoolerResources(ctx, rec); err != nil {
		detail["error"] = err.Error()
		httpx.SetAudit(r, "database.pooler.disable", conn.Name, detail)
		return s.dockerErr(err)
	}
	warnings := []string{}
	if rec.Settings.AuthMode == dbx.PoolerAuthQuery {
		if err := s.removePoolerAuth(ctx, conn, dsn); err != nil {
			warnings = append(warnings, "the "+dbx.PoolerAuthRole+" account and its function could not be removed from the database: "+err.Error())
		}
	}
	if rec.HostPort > 0 {
		// Only a rule this dashboard wrote for the port, found by its comment.
		firewall, ferr := s.setDBFirewall(ctx, r, rec.HostPort, false)
		detail["firewall"] = firewall
		if ferr != nil {
			warnings = append(warnings, ferr.Error())
		}
	}
	if _, err := s.Store.DB.ExecContext(ctx, `DELETE FROM db_poolers WHERE connection_id = ?`, id); err != nil {
		return httpx.Internal(err)
	}
	if len(warnings) > 0 {
		detail["warnings"] = warnings
	}
	httpx.SetAudit(r, "database.pooler.disable", conn.Name, detail)
	httpx.JSON(w, http.StatusOK, map[string]any{"removed": rec.Container, "warnings": warnings})
	return nil
}
