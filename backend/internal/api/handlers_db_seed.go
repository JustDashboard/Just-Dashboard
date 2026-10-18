package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// Seed data: rows of mock data written into tables that exist (dbx/seed.go).
//
// The plan is a read — the catalogue, how many rows each table holds, the
// keys a foreign key can point at — so it is on the read surface, open on a
// protected connection and recorded nowhere, as /orm and /rows/sql are. The
// write is a write: service.control, which the row editor and the import ask
// for, audited with what went where and never a value, and refused on a
// protected connection like every other route that changes data. It only
// adds rows, so it is not destructive.

func (s *Server) mountDatabaseSeedRoutes(r chi.Router) {
	r.Method(http.MethodPost, "/{id}/seed/plan", s.handle(s.handleDBSeedPlan))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapServiceControl))
		r.Method(http.MethodPost, "/{id}/seed", s.handle(s.handleDBSeed))
	})
}

// handleDBSeedPlan answers what a seed would write and the first rows of each
// table, and writes nothing.
func (s *Server) handleDBSeedPlan(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req dbx.SeedRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SkipAudit(r)
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	plan, err := dbx.PlanSeed(ctx, pool, conn.Driver, req)
	if errors.Is(err, dbx.ErrSeedRequest) {
		return httpx.BadRequest("%s", dbx.SeedRequestMessage(err))
	}
	if err != nil {
		return queryFailed(err)
	}
	httpx.JSON(w, http.StatusOK, plan)
	return nil
}

// handleDBSeed writes a seed. A refusal of the request is a 400; a failure
// once writing has begun is answered without the mark that offers it again,
// since on ClickHouse, which has no transaction, part of it may stand.
func (s *Server) handleDBSeed(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req dbx.SeedRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	asked := map[string]int{}
	for _, t := range req.Tables {
		asked[t.Name] = t.Rows
	}
	ctx, cancel := timeoutCtx(r, 10*time.Minute)
	defer cancel()
	report, err := dbx.Seed(ctx, pool, conn.Driver, req)
	if errors.Is(err, dbx.ErrSeedRequest) {
		return httpx.BadRequest("%s", dbx.SeedRequestMessage(err))
	}
	if err != nil {
		httpx.SetAudit(r, "database.seed", conn.Name, map[string]any{"tables": asked, "error": err.Error()})
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	written := map[string]int{}
	for _, t := range report.Tables {
		written[t.Name] = t.Inserted
	}
	// What went where, by count. The rows are made up, but they are rows of
	// the operator's tables now, and row data does not belong in the log.
	httpx.SetAudit(r, "database.seed", conn.Name, map[string]any{
		"seed": report.Seed, "tables": written, "inserted": report.Inserted, "skipped": report.Skipped,
	})
	httpx.JSON(w, http.StatusOK, report)
	return nil
}
