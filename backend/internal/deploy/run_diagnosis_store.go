package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// RunDiagnosis is the last answer the assistant's provider gave about a
// failed run, kept so the run's page reads it again without paying for it.
type RunDiagnosis struct {
	RunID    int64           `json:"runId"`
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Answer   DiagnosisAnswer `json:"answer"`
	// Sent is exactly what went to the provider, already masked.
	Sent      string          `json:"sent"`
	SentLines int             `json:"sentLines"`
	Masked    int             `json:"masked"`
	Usage     json.RawMessage `json:"usage"`
	CreatedBy string          `json:"createdBy"`
	CreatedAt time.Time       `json:"createdAt"`
}

// RunDiagnosis reads a run's diagnosis, nil when nobody has asked.
func (s *OrchestrationStore) RunDiagnosis(ctx context.Context, runID int64) (*RunDiagnosis, error) {
	var d RunDiagnosis
	var answer, usage string
	var created int64
	err := s.db.QueryRowContext(ctx, `
		SELECT run_id, provider, model, answer_json, sent_text, sent_lines, masked, usage_json, created_by, created_at
		  FROM deploy_run_diagnoses WHERE run_id = ?`, runID).
		Scan(&d.RunID, &d.Provider, &d.Model, &answer, &d.Sent, &d.SentLines, &d.Masked, &usage, &d.CreatedBy, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if json.Unmarshal([]byte(answer), &d.Answer) != nil {
		return nil, fmt.Errorf("%w: stored diagnosis is malformed", ErrInvalidPlan)
	}
	if d.Answer.Evidence == nil {
		d.Answer.Evidence = []DiagnosisEvidence{}
	}
	if d.Answer.Steps == nil {
		d.Answer.Steps = []string{}
	}
	d.Usage = json.RawMessage(usage)
	d.CreatedAt = unixTime(created)
	return &d, nil
}

// SaveRunDiagnosis keeps a run's diagnosis in place of the one before it.
func (s *OrchestrationStore) SaveRunDiagnosis(ctx context.Context, d RunDiagnosis) error {
	answer, err := json.Marshal(d.Answer)
	if err != nil {
		return err
	}
	usage := string(d.Usage)
	if usage == "" || !json.Valid(d.Usage) {
		usage = "{}"
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO deploy_run_diagnoses(run_id, provider, model, answer_json, sent_text, sent_lines, masked, usage_json, created_by, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id) DO UPDATE SET
		  provider = excluded.provider, model = excluded.model, answer_json = excluded.answer_json,
		  sent_text = excluded.sent_text, sent_lines = excluded.sent_lines, masked = excluded.masked,
		  usage_json = excluded.usage_json, created_by = excluded.created_by, created_at = excluded.created_at`,
		d.RunID, d.Provider, d.Model, string(answer), d.Sent, d.SentLines, d.Masked, usage, d.CreatedBy, d.CreatedAt.Unix())
	return err
}

// DiagnosisSecrets opens every value a diagnosis of the run must not send:
// the environment's variables as they are now and as the run had them —
// each secret one, and each plain one that carries credential material or
// looks like a key —, what their references point at in this dashboard's own
// store (a credential's secret, a database's saved address and its password),
// every saved deployment credential, the environment's trigger secrets and a
// carried-over project's old variables. Nothing here contacts anything.
//
// A variable that cannot be opened fails the read, so nothing is sent rather
// than something unmasked.
func (s *PlanningStore) DiagnosisSecrets(ctx context.Context, run EngineRun) ([]string, error) {
	values := []string{}
	add := func(value string) {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	addSecret := func(value string) {
		add(value)
		// A key file printed a line at a time is masked a line at a time.
		if strings.Contains(value, "\n") {
			for _, line := range strings.Split(value, "\n") {
				if line = strings.TrimSpace(line); len(line) >= 16 && !strings.HasPrefix(line, "-----") {
					add(line)
				}
			}
		}
		add(urlPassword(value))
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT sensitivity, value_enc, value_mode FROM deploy_variable_revisions
		 WHERE environment_id = ?
		   AND (active = 1 OR id IN (SELECT variable_revision_id FROM deploy_run_variable_revisions WHERE run_id = ?))`,
		run.EnvironmentID, run.ID)
	if err != nil {
		return nil, err
	}
	type revision struct{ sensitivity, sealed, mode string }
	revisions := []revision{}
	for rows.Next() {
		var r revision
		if err := rows.Scan(&r.sensitivity, &r.sealed, &r.mode); err != nil {
			rows.Close()
			return nil, err
		}
		revisions = append(revisions, r)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, r := range revisions {
		value, err := s.sealer.Open(r.sealed)
		if err != nil {
			return nil, errors.New("a variable of this environment cannot be opened to mask it")
		}
		if reference, err := ParseVariableReference(value); err == nil && r.mode != "literal" {
			s.addReferencedSecrets(ctx, reference, addSecret)
			continue
		}
		if r.sensitivity == "secret" || diagnosisMaskable(value) {
			addSecret(value)
		}
	}

	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`SELECT secret_enc FROM deploy_credentials WHERE secret_enc != ''`, nil},
		{`SELECT secret_enc FROM deploy_triggers WHERE environment_id = ? AND secret_enc != ''`, []any{run.EnvironmentID}},
		{`SELECT value_enc FROM deploy_env WHERE project_id = ?`, []any{run.ProjectID}},
	} {
		sealed, err := s.sealedColumn(ctx, query.sql, query.args...)
		if err != nil {
			return nil, err
		}
		for _, value := range sealed {
			// A credential this key cannot open was never used by a run under
			// it either, so it cannot be in the transcript.
			if opened, err := s.sealer.Open(value); err == nil {
				addSecret(opened)
			}
		}
	}
	return values, nil
}

// addReferencedSecrets adds what a reference points at in the dashboard's own
// store. A target that is not there resolves to nothing at run time either.
func (s *PlanningStore) addReferencedSecrets(ctx context.Context, reference VariableReference, add func(string)) {
	var table, column string
	targets := []string{reference.Target}
	switch reference.Kind {
	case "credential":
		table, column = "deploy_credentials", "secret_enc"
	case "database":
		table, column = "db_connections", "dsn_enc"
		targets = databaseReferenceCandidates(reference.Target)
	default:
		return
	}
	for _, target := range targets {
		sealed, err := s.referenceSealedValue(ctx, table, column, target)
		if err != nil {
			continue
		}
		if value, err := s.sealer.Open(sealed); err == nil {
			add(value)
			return
		}
	}
}

// databaseReferenceCandidates are the names a ${{database.…}} target may
// name its connection by, without what it asks of it: "5", "5.url",
// "shop.pooled.jdbc" name connection 5 and shop.
func databaseReferenceCandidates(target string) []string {
	candidates := []string{target}
	if head, _, found := strings.Cut(target, "."); found && head != "" {
		candidates = append(candidates, head)
	}
	return candidates
}

func (s *PlanningStore) sealedColumn(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
