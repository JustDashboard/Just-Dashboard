package api

import (
	"context"
	"encoding/json"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// repairLinkedDatabaseVariables gives back the database links committed as
// text from 0.7.2 on, before the engine claims a run, so the next deploy of
// such a project reaches its database with nobody having to relink it. Both
// ways of linking wrote the reference as a value: the new-deployment page
// (whose committed draft proves a person linked it,
// deploy.RepairLinkedDatabaseLiterals) and Settings → Databases (whose write
// the audit trail proves, personWroteVariable). A deploy key may never point
// a variable at a saved database, so nothing a key wrote is promoted. Each
// rewrite is on the audit trail; a failure is logged and never stops the
// dashboard from starting.
func (s *Server) repairLinkedDatabaseVariables(ctx context.Context) {
	if s.modules.deployPlanning == nil {
		return
	}
	repaired, err := s.modules.deployPlanning.RepairLinkedDatabaseLiterals(ctx)
	if err != nil {
		s.Log.Warn("linked database variables could not all be repaired", "error", err)
	}
	for _, repair := range repaired {
		s.recordLinkedDatabaseRepair(ctx, repair.ProjectID, repair.EnvironmentID, repair.Name,
			"the database link was stored as text when the project was created")
	}
	literals, err := s.modules.deployPlanning.LinkedDatabaseLiterals(ctx)
	if err != nil {
		s.Log.Warn("linked database variables could not be read for repair", "error", err)
		return
	}
	for _, literal := range literals {
		if !s.personWroteVariable(ctx, literal) {
			continue
		}
		done, err := s.modules.deployPlanning.RestoreLinkedDatabaseLiteral(ctx, literal)
		if err != nil {
			s.Log.Warn("a linked database variable could not be repaired", "variable", literal.Name,
				"environment", literal.EnvironmentID, "error", err)
			continue
		}
		if done {
			s.recordLinkedDatabaseRepair(ctx, literal.ProjectID, literal.EnvironmentID, literal.Name,
				"the database link was stored as text when it was linked from Settings")
		}
	}
}

// personWroteVariable reads the audit trail for the write that made a
// variable revision: a person — a session, a hub session or the assistant
// acting as one, never an API key — setting that variable in that environment
// as the revision was written, and no key writing it then.
func (s *Server) personWroteVariable(ctx context.Context, literal deploy.LinkedDatabaseLiteral) bool {
	rows, err := s.Store.DB.QueryContext(ctx, `
		SELECT actor, key_id FROM audit_log
		 WHERE action = 'deploy.variable.update' AND target = ? AND success = 1
		   AND ts BETWEEN ? AND ?
		   AND json_extract(detail, '$.environmentId') = ?`,
		literal.Name, literal.CreatedAt-1, literal.CreatedAt+5, literal.EnvironmentID)
	if err != nil {
		return false
	}
	defer rows.Close()
	person := false
	for rows.Next() {
		var actor string
		var keyID int64
		if err := rows.Scan(&actor, &keyID); err != nil {
			return false
		}
		if keyID != 0 || (actor != "session" && actor != "hub-session" && actor != "assistant") {
			return false
		}
		person = true
	}
	return person && rows.Err() == nil
}

func (s *Server) recordLinkedDatabaseRepair(ctx context.Context, projectID, environmentID int64, name, reason string) {
	detail, _ := json.Marshal(map[string]any{
		"deploymentId": projectID, "environmentId": environmentID, "reference": true, "reason": reason,
	})
	s.Audit.Record(ctx, audit.Entry{Actor: "system", Action: "deploy.variable.repair", Target: name, Success: true, Detail: string(detail)})
}
