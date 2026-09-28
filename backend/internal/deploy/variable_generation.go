package deploy

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
)

// generatedVariableValue mints a value for Generate and Rotate. A name the
// environment's detection classified as a self-issued secret gets its
// framework's shape — rotating a Laravel APP_KEY must keep its base64:
// prefix, or every request fails on the cipher's key length — and any other
// name gets 32 random bytes, URL-safe.
func (s *PlanningStore) generatedVariableValue(ctx context.Context, environmentID int64, name string) (string, error) {
	if length, format, ok := s.detectedSecretShape(ctx, environmentID, name); ok {
		return generatedSecretValue(length, format)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// detectedSecretShape reads the recorded detection of the environment's
// desired build plan for the candidate that plan builds.
func (s *PlanningStore) detectedSecretShape(ctx context.Context, environmentID int64, name string) (int, string, bool) {
	var buildJSON, evidenceJSON string
	err := s.db.QueryRowContext(ctx, `
		SELECT b.config_json, b.evidence_json
		  FROM deploy_environments e
		  JOIN deploy_build_plans b ON b.environment_id = e.id AND b.revision = e.desired_revision
		 WHERE e.id = ?`, environmentID).Scan(&buildJSON, &evidenceJSON)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return 0, "", false
	}
	var build BuildPlanConfig
	var evidence struct {
		Candidates []DetectedCandidate `json:"candidates"`
	}
	if json.Unmarshal([]byte(buildJSON), &build) != nil || json.Unmarshal([]byte(evidenceJSON), &evidence) != nil {
		return 0, "", false
	}
	for _, candidate := range evidence.Candidates {
		if candidate.Root != build.RootDirectory || candidate.BuildMethod != build.Method {
			continue
		}
		for _, variable := range candidate.Variables {
			if variable.Name == name && variable.Setup == "generate" && validGeneratedSecretFormat(variable.GenerateFormat) &&
				variable.GenerateLength >= MinGeneratedSecretLength && variable.GenerateLength <= MaxGeneratedSecretLength {
				return variable.GenerateLength, variable.GenerateFormat, true
			}
		}
	}
	return 0, "", false
}
