package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// The comparison of two schemas of one engine, and the migration that turns the
// first into the second.
//
// It reads both and writes neither: the statements it returns are text for the
// operator to copy or open in the Query tab, where the statement classifier and
// the audit trail apply to them as they do to anything typed there. So the
// route sits on the read surface, on a protected connection included, and
// leaves no audit entry of its own.

// schemaDiffSide names one side of a comparison as it was resolved: the
// connection, the product behind it and the schema that was read.
type schemaDiffSide struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Driver      dbx.Driver `json:"driver"`
	Flavor      string     `json:"flavor"`
	FlavorLabel string     `json:"flavorLabel"`
	Schema      string     `json:"schema"`
}

type schemaDiffResponse struct {
	From schemaDiffSide `json:"from"`
	To   schemaDiffSide `json:"to"`
	// Objects is what differs, and empty when the two schemas are alike.
	Objects []dbx.ObjectDiff `json:"objects"`
	// Steps is the migration in the order it has to run, and Script the same
	// as one text.
	Steps  []dbx.MigrationStep `json:"steps"`
	Script string              `json:"script"`
	// Notes say what the comparison did not look at or could not be sure of.
	Notes []string `json:"notes"`
}

func (s *Server) handleDBSchemaDiff(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	toID, err := strconv.ParseInt(q.Get("to"), 10, 64)
	if err != nil {
		return httpx.BadRequest("name the connection to compare with (to=)")
	}
	fromConn, _, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	toConn, _, err := s.dbConnRow(r.Context(), toID)
	if err != nil {
		return err
	}
	if fromConn.Driver != toConn.Driver {
		return httpx.BadRequest("%s is %s and %s is %s: a comparison needs two connections to the same kind of engine",
			fromConn.Name, fromConn.Driver, toConn.Name, toConn.Driver)
	}
	if !fromConn.Driver.IsSQL() {
		return httpx.BadRequest("%s has no schema to compare", fromConn.Driver)
	}
	fromSchema, toSchema := strings.TrimSpace(q.Get("schema")), strings.TrimSpace(q.Get("toSchema"))
	if id == toID && fromSchema == toSchema {
		return httpx.BadRequest("both sides are the same schema of the same connection, so there is nothing to compare")
	}

	fromPool, _, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	toPool, _, err := s.dbPool(r.Context(), toID)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 3*time.Minute)
	defer cancel()

	fromFlavor := dbx.IdentifySQL(ctx, fromPool, fromConn.Driver).Flavor
	toFlavor := dbx.IdentifySQL(ctx, toPool, toConn.Driver).Flavor
	for _, side := range []struct{ name, flavor string }{{fromConn.Name, fromFlavor}, {toConn.Name, toFlavor}} {
		if !dbx.Capable(fromConn.Driver, side.flavor, "schemaDiff") {
			return httpx.BadRequest("%s is %s, whose schema this does not compare", side.name, dbx.FlavorLabel(side.flavor))
		}
	}

	from, err := dbx.ReadSchemaSnapshot(ctx, fromPool, fromConn.Driver, fromFlavor, fromSchema)
	if err != nil {
		return schemaDiffReadFailed(err)
	}
	to, err := dbx.ReadSchemaSnapshot(ctx, toPool, toConn.Driver, toFlavor, toSchema)
	if err != nil {
		return schemaDiffReadFailed(err)
	}
	diff := dbx.DiffSchemas(from, to)
	migration := dbx.PlanMigration(ctx, fromPool, from, to, diff)

	notes := append(from.UnreadNotes(fromConn.Name), to.UnreadNotes(toConn.Name)...)
	if fromFlavor != toFlavor {
		notes = append(notes, dbx.FlavorLabel(fromFlavor)+" and "+dbx.FlavorLabel(toFlavor)+
			" are not the same product, so the statements are the ones for the first and may not read the same on the second.")
	}
	httpx.JSON(w, http.StatusOK, schemaDiffResponse{
		From:    schemaDiffSide{ID: id, Name: fromConn.Name, Driver: fromConn.Driver, Flavor: fromFlavor, FlavorLabel: dbx.FlavorLabel(fromFlavor), Schema: from.Schema},
		To:      schemaDiffSide{ID: toID, Name: toConn.Name, Driver: toConn.Driver, Flavor: toFlavor, FlavorLabel: dbx.FlavorLabel(toFlavor), Schema: to.Schema},
		Objects: nonNil(diff.Objects), Steps: nonNil(migration.Steps), Script: migration.Script(), Notes: nonNil(notes),
	})
	return nil
}

func schemaDiffReadFailed(err error) error {
	if errors.Is(err, dbx.ErrSchemaTooLarge) {
		return httpx.BadRequest("%v", err)
	}
	return queryFailed(err)
}

func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}
