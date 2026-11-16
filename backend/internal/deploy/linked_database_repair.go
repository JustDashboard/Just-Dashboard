package deploy

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
)

// LinkedDatabaseRepair names a variable RepairLinkedDatabaseLiterals gave its
// database link back.
type LinkedDatabaseRepair struct {
	ProjectID     int64
	EnvironmentID int64
	Name          string
}

// RepairLinkedDatabaseLiterals restores the database links that the
// new-deployment page committed as text from 0.7.2 on. The page links a
// database by writing `${{database.5}}` into the variable's row, and commit
// stored every value typed there as literal, so the application received
// those characters for its database URL and no network to the database.
//
// The proof is the draft that made the project, which is kept with it. It
// must be a person's (no deploy key: a key may not point a variable at a saved
// database), it must have staged exactly this value for this name, and its own
// plan must link that database, as must the environment still. The variable's
// active revision must still be the literal first one that commit wrote, so a
// value anyone set afterwards is theirs and stays as it is, and a captured
// value that only reads like a reference has no link to stand on. The rewrite
// is a new revision holding the same value as a reference, which the next
// deploy resolves; a run that already failed keeps its own snapshot.
func (s *PlanningStore) RepairLinkedDatabaseLiterals(ctx context.Context) ([]LinkedDatabaseRepair, error) {
	type origin struct {
		draftID                  string
		projectID, environmentID int64
	}
	// A project's first environment is the one its draft's commit made.
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id, p.id, e.id
		  FROM deploy_drafts d
		  JOIN deploy_projects p ON p.id = d.committed_project_id
		  JOIN deploy_environments e ON e.id = (SELECT MIN(id) FROM deploy_environments WHERE project_id = p.id)
		 WHERE d.key_id = 0 AND d.environment_enc <> ''
		   AND p.archived_at = 0 AND e.archived_at = 0
		   AND EXISTS (SELECT 1 FROM deploy_variable_revisions v
		                WHERE v.environment_id = e.id AND v.active = 1 AND v.revision = 1
		                  AND v.value_mode = 'literal' AND v.copied_from_environment = 0)
		   AND EXISTS (SELECT 1 FROM deploy_dependencies dep
		                WHERE dep.environment_id = e.id AND dep.release_id = 0
		                  AND dep.kind = 'database' AND dep.resource_kind = 'database_connection')
		 ORDER BY e.id`)
	if err != nil {
		return nil, err
	}
	origins := []origin{}
	for rows.Next() {
		var item origin
		if err := rows.Scan(&item.draftID, &item.projectID, &item.environmentID); err != nil {
			rows.Close()
			return nil, err
		}
		origins = append(origins, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	repaired := []LinkedDatabaseRepair{}
	var failures []error
	for _, item := range origins {
		draft, err := s.Get(ctx, item.draftID)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if draft.KeyID != 0 || draft.Data.Configuration == nil {
			continue
		}
		planned := linkedDatabaseConnections(draft.Data.Configuration.Dependencies)
		current, err := s.environmentLinkedDatabases(ctx, item.environmentID)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		linked := map[string]bool{}
		for id := range planned {
			if current[id] {
				linked[id] = true
			}
		}
		names := make([]string, 0, len(draft.environment))
		for name := range draft.environment {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			staged := draft.environment[name]
			if !linkedDatabaseReference(staged, linked) {
				continue
			}
			repair := LinkedDatabaseRepair{ProjectID: item.projectID, EnvironmentID: item.environmentID, Name: name}
			done, err := s.restoreLinkedDatabaseReference(ctx, repair, staged)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			if done {
				repaired = append(repaired, repair)
			}
		}
	}
	return repaired, errors.Join(failures...)
}

// LinkedDatabaseLiteral is an active variable revision whose whole value is a
// reference to a database its environment links, stored literal: what a link
// made from Settings → Databases was written as from 0.7.2 on, the page
// sending the reference as a value. Who wrote it is not the store's to prove;
// the caller reads that from the audit trail before restoring it.
type LinkedDatabaseLiteral struct {
	ProjectID     int64
	EnvironmentID int64
	RevisionID    int64
	Name          string
	CreatedAt     int64
}

// LinkedDatabaseLiterals lists every such revision of a live environment.
func (s *PlanningStore) LinkedDatabaseLiterals(ctx context.Context) ([]LinkedDatabaseLiteral, error) {
	type row struct {
		literal LinkedDatabaseLiteral
		sealed  string
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.project_id, v.environment_id, v.id, v.key, v.created_at, v.value_enc
		  FROM deploy_variable_revisions v
		  JOIN deploy_environments e ON e.id = v.environment_id
		  JOIN deploy_projects p ON p.id = e.project_id
		 WHERE v.active = 1 AND v.value_mode = 'literal' AND v.copied_from_environment = 0
		   AND e.archived_at = 0 AND p.archived_at = 0
		   AND EXISTS (SELECT 1 FROM deploy_dependencies dep
		                WHERE dep.environment_id = e.id AND dep.release_id = 0
		                  AND dep.kind = 'database' AND dep.resource_kind = 'database_connection')
		 ORDER BY v.environment_id, v.key`)
	if err != nil {
		return nil, err
	}
	found := []row{}
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.literal.ProjectID, &item.literal.EnvironmentID, &item.literal.RevisionID,
			&item.literal.Name, &item.literal.CreatedAt, &item.sealed); err != nil {
			rows.Close()
			return nil, err
		}
		found = append(found, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	literals := []LinkedDatabaseLiteral{}
	for _, item := range found {
		value, err := s.sealer.Open(item.sealed)
		if err != nil {
			continue
		}
		linked, err := s.environmentLinkedDatabases(ctx, item.literal.EnvironmentID)
		if err != nil {
			return nil, err
		}
		if linkedDatabaseReference(value, linked) {
			literals = append(literals, item.literal)
		}
	}
	return literals, nil
}

// RestoreLinkedDatabaseLiteral writes the literal's value back as the
// reference it is, provided that revision is still the active one and still
// a reference to a database the environment links.
func (s *PlanningStore) RestoreLinkedDatabaseLiteral(ctx context.Context, literal LinkedDatabaseLiteral) (bool, error) {
	linked, err := s.environmentLinkedDatabases(ctx, literal.EnvironmentID)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var sensitivity, scopes, sealed string
	if err := tx.QueryRowContext(ctx, `
		SELECT sensitivity, scopes, value_enc FROM deploy_variable_revisions
		 WHERE id = ? AND environment_id = ? AND key = ? AND active = 1
		   AND value_mode = 'literal' AND copied_from_environment = 0`,
		literal.RevisionID, literal.EnvironmentID, literal.Name).Scan(&sensitivity, &scopes, &sealed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	value, err := s.sealer.Open(sealed)
	if err != nil {
		return false, nil
	}
	if !linkedDatabaseReference(value, linked) {
		return false, nil
	}
	if err := s.writeVariableRevisionTx(ctx, tx, literal.EnvironmentID, literal.Name, value, sensitivity,
		strings.Split(scopes, ","), "migration", "reference"); err != nil {
		return false, err
	}
	if err := s.validateActiveVariableGraphTx(ctx, tx, literal.EnvironmentID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *PlanningStore) environmentLinkedDatabases(ctx context.Context, environmentID int64) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT kind, ownership, resource_kind, resource_id FROM deploy_dependencies
		 WHERE environment_id = ? AND release_id = 0`, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	dependencies := []PlannedDependency{}
	for rows.Next() {
		var dependency PlannedDependency
		if err := rows.Scan(&dependency.Kind, &dependency.Ownership, &dependency.ResourceKind, &dependency.ResourceID); err != nil {
			return nil, err
		}
		dependencies = append(dependencies, dependency)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return linkedDatabaseConnections(dependencies), nil
}

// restoreLinkedDatabaseReference writes the reference revision when the
// variable's active revision is still commit's literal first one and holds
// the staged value.
func (s *PlanningStore) restoreLinkedDatabaseReference(ctx context.Context, repair LinkedDatabaseRepair, staged string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var sensitivity, scopes, sealed string
	if err := tx.QueryRowContext(ctx, `
		SELECT sensitivity, scopes, value_enc FROM deploy_variable_revisions
		 WHERE environment_id = ? AND key = ? AND active = 1 AND revision = 1
		   AND value_mode = 'literal' AND copied_from_environment = 0`,
		repair.EnvironmentID, repair.Name).Scan(&sensitivity, &scopes, &sealed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	value, err := s.sealer.Open(sealed)
	if err != nil || value != staged {
		return false, nil
	}
	if err := s.writeVariableRevisionTx(ctx, tx, repair.EnvironmentID, repair.Name, value, sensitivity,
		strings.Split(scopes, ","), "migration", "reference"); err != nil {
		return false, err
	}
	if err := s.validateActiveVariableGraphTx(ctx, tx, repair.EnvironmentID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
