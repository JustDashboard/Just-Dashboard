package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
)

// settingsLinkedDatabase is what Settings → Databases left from 0.7.2 on: the
// database linked as a dependency and its reference written as a value, so
// stored literal, by whoever the audit entry beside it names.
func settingsLinkedDatabase(t *testing.T, s *Server, writer audit.Entry) (projectID, environmentID int64) {
	t.Helper()
	projectID, environmentID = seedDeployment(t, s, "orders")
	if _, err := s.Store.DB.ExecContext(t.Context(), `
		INSERT INTO deploy_dependencies(environment_id, release_id, kind, ownership, resource_kind, resource_id, config_json, created_at)
		VALUES(?, 0, 'database', 'linked', 'database_connection', '2', '{}', 1)`, environmentID); err != nil {
		t.Fatal(err)
	}
	// The revision PutVariable wrote for a value: literal, the reference's
	// own characters sealed.
	link := "${{database.2}}"
	sealed, err := s.Sealer.Seal(link)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(append([]byte(link), 0))
	if _, err := s.Store.DB.ExecContext(t.Context(), `
		INSERT INTO deploy_variable_revisions(environment_id, key, revision, sensitivity, scopes, value_enc,
		  value_digest, active, created_by, created_at, value_mode)
		VALUES(?, 'DATABASE_URL', 1, 'secret', 'runtime,build', ?, ?, 1, 'operator', ?, 'literal')`,
		environmentID, sealed, "sha256:"+hex.EncodeToString(digest[:]), time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if writer.Action != "" {
		detail, _ := json.Marshal(map[string]any{"deploymentId": projectID, "environmentId": environmentID, "reference": false})
		writer.Target, writer.Success, writer.Detail, writer.TS = "DATABASE_URL", true, string(detail), time.Now()
		s.Audit.Record(t.Context(), writer)
	}
	return projectID, environmentID
}

func activeDatabaseURLMode(t *testing.T, s *Server, environmentID int64) string {
	t.Helper()
	var mode string
	if err := s.Store.DB.QueryRowContext(t.Context(), `
		SELECT value_mode FROM deploy_variable_revisions WHERE environment_id = ? AND key = 'DATABASE_URL' AND active = 1`,
		environmentID).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	return mode
}

func TestStartupRepairsADatabaseLinkAPersonMadeInSettings(t *testing.T) {
	s := testServer(t)
	_, environmentID := settingsLinkedDatabase(t, s, audit.Entry{Actor: "session", Action: "deploy.variable.update", UserID: 1, Username: "operator"})
	s.repairLinkedDatabaseVariables(t.Context())
	if mode := activeDatabaseURLMode(t, s, environmentID); mode != "reference" {
		t.Fatalf("a link a person made from Settings is still %s", mode)
	}
	var repairs int
	if err := s.Store.DB.QueryRowContext(t.Context(), `
		SELECT COUNT(*) FROM audit_log WHERE action = 'deploy.variable.repair' AND actor = 'system' AND target = 'DATABASE_URL'`).Scan(&repairs); err != nil || repairs != 1 {
		t.Fatalf("repair audit entries = %d, %v", repairs, err)
	}
	// A second start finds nothing left to do.
	s.repairLinkedDatabaseVariables(t.Context())
	if err := s.Store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_log WHERE action = 'deploy.variable.repair'`).Scan(&repairs); err != nil || repairs != 1 {
		t.Fatalf("repair audit entries after a second pass = %d, %v", repairs, err)
	}
}

func TestStartupLeavesADatabaseLinkNoPersonProvablyMade(t *testing.T) {
	for name, writer := range map[string]audit.Entry{
		// A deploy key may never point a variable at a saved database.
		"a deploy key":   {Actor: "token", Action: "deploy.variable.update", KeyID: 5, KeyName: "ci"},
		"no audit entry": {},
	} {
		t.Run(name, func(t *testing.T) {
			s := testServer(t)
			_, environmentID := settingsLinkedDatabase(t, s, writer)
			s.repairLinkedDatabaseVariables(t.Context())
			if mode := activeDatabaseURLMode(t, s, environmentID); mode != "literal" {
				t.Fatalf("%s: promoted to %s", name, mode)
			}
		})
	}
}
