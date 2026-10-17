package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// Index advice, on request. The Explain view offers these two actions on a
// statement the operator is already looking at, and the operator presses them;
// neither is ever called by a page on its own, and no result is kept.
//
// Both sit on the read side beside /explain, for the reason /explain does:
// they plan a statement and run none. A hypothetical index exists only in the
// memory of one connection, which is thrown away afterwards, so a protected
// connection and a read-only role may ask. Making the index real is the
// schema form's job (`POST /ddl/index`, with its own capability, preview and
// audit), reached from the Explain view only by the operator's click.

type indexAdviceRequest struct {
	Query string `json:"query"`
}

type whatIfRequest struct {
	Query   string   `json:"query"`
	Schema  string   `json:"schema"`
	Table   string   `json:"table"`
	Columns []string `json:"columns"`
}

// handleDBIndexCandidates proposes indexes for the columns a statement's plan
// filters, joins and sorts on, and says whether HypoPG can try them.
func (s *Server) handleDBIndexCandidates(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req indexAdviceRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Query) == "" {
		return httpx.BadRequest("query is required")
	}
	conn, err := s.sqlConnection(r, id)
	if err != nil {
		return err
	}
	statement, err := dbx.ExplainTarget(conn.Driver, req.Query)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	pool, _, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	advice, err := dbx.IndexCandidates(ctx, pool, conn.Driver, statement)
	if err != nil {
		return indexAdviceFailed(err)
	}
	httpx.SetAudit(r, "database.explain.index_candidates", conn.Name, map[string]any{
		"statement": statement.SQL, "candidates": len(advice.Candidates), "hypopg": advice.HypoPG.State,
	})
	httpx.JSON(w, http.StatusOK, advice)
	return nil
}

// handleDBIndexWhatIf plans a statement with and without a hypothetical index.
func (s *Server) handleDBIndexWhatIf(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req whatIfRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Query) == "" {
		return httpx.BadRequest("query is required")
	}
	if strings.TrimSpace(req.Table) == "" || len(req.Columns) == 0 {
		return httpx.BadRequest("a table and at least one column are required")
	}
	conn, err := s.sqlConnection(r, id)
	if err != nil {
		return err
	}
	statement, err := dbx.ExplainTarget(conn.Driver, req.Query)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	pool, _, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	tried, err := dbx.TryIndex(ctx, pool, conn.Driver, statement, dbx.WhatIfIndex{
		Schema: req.Schema, Table: req.Table, Columns: req.Columns,
	})
	if err != nil {
		return indexAdviceFailed(err)
	}
	httpx.SetAudit(r, "database.explain.what_if", conn.Name, map[string]any{
		"statement": statement.SQL, "index": tried.Statement,
		"used": tried.Used, "beforeCost": tried.BeforeCost, "afterCost": tried.AfterCost,
	})
	httpx.JSON(w, http.StatusOK, tried)
	return nil
}

// indexAdviceFailed answers the two refusals that are a fact about the server
// as such, and the rest as the engine's own words, as /explain does.
func indexAdviceFailed(err error) error {
	var missing dbx.ErrHypoPGUnavailable
	switch {
	case errors.As(err, &missing), errors.Is(err, dbx.ErrNoWhatIf), errors.Is(err, dbx.ErrExplainUnsupported):
		return httpx.Err(http.StatusBadRequest, "unsupported", err.Error())
	}
	return httpx.BadRequest("%v", err)
}

// handleDBMissingIndexes reads SQL Server's missing-index views. It is a
// request for a list the operator opened, so nothing is recorded, as for the
// other reads of the server's own views.
func (s *Server) handleDBMissingIndexes(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	report, err := dbx.ListMissingIndexes(ctx, pool, conn.Driver)
	if err != nil {
		return queryFailed(err)
	}
	httpx.JSON(w, http.StatusOK, report)
	return nil
}
