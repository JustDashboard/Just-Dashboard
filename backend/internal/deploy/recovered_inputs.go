package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// These identities explain captured values without creating another reveal
// surface. Storage keys stay distinct even when services share an env name.
type RecoveredInput struct {
	StorageKey  string `json:"storageKey"`
	Name        string `json:"name"`
	Service     string `json:"service,omitempty"`
	Kind        string `json:"kind"`
	Origin      string `json:"origin"`
	Category    string `json:"category"`
	Sensitivity string `json:"sensitivity"`
	Retained    bool   `json:"retained"`
	Empty       bool   `json:"empty"`
}

func (s *PlanningStore) validateRecoveredInputMutationTx(ctx context.Context, tx *sql.Tx, environmentID int64, name string, request *VariableWriteRequest) error {
	var original, current, provenanceRaw string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT src.config_json FROM deploy_sources src JOIN deploy_releases r ON r.source_id=src.id WHERE r.environment_id=e.id AND r.plan_revision=1 AND json_extract(r.provenance_json,'$.adopted')=1 ORDER BY r.id LIMIT 1),''), COALESCE((SELECT config_json FROM deploy_sources WHERE environment_id=e.id AND revision=e.desired_revision),''), COALESCE((SELECT provenance_json FROM deploy_releases WHERE environment_id=e.id AND plan_revision=1 AND json_extract(provenance_json,'$.adopted')=1 ORDER BY id LIMIT 1),'') FROM deploy_environments e WHERE e.id=?`, environmentID).Scan(&original, &current, &provenanceRaw); err != nil {
		return err
	}
	var baseline, desired DraftSourceConfig
	if json.Unmarshal([]byte(original), &baseline) != nil || json.Unmarshal([]byte(current), &desired) != nil {
		return nil
	}
	var provenance struct {
		Inputs []RecoveredInput `json:"inputs"`
	}
	_ = json.Unmarshal([]byte(provenanceRaw), &provenance)
	inputs := append(provenance.Inputs, recoveredInputBindings(baseline)...)
	bound := false
	for _, input := range inputs {
		if input.StorageKey == name {
			bound = true
			break
		}
	}
	if !bound {
		return nil
	}
	bound = false
	for _, document := range desired.ComposeFiles {
		if strings.Contains(document.Content, "${"+name+"}") {
			bound = true
			break
		}
	}
	if !bound {
		return nil
	}
	if request == nil {
		return fmt.Errorf("%w: captured input %s is still used by the runtime; update its source binding before removing it", ErrInvalidVariable, name)
	}
	var sensitivity, scopes string
	if err := tx.QueryRowContext(ctx, `SELECT sensitivity,scopes FROM deploy_variable_revisions WHERE environment_id=? AND key=? AND active=1`, environmentID, name).Scan(&sensitivity, &scopes); err != nil {
		return err
	}
	if request.Value == nil || request.Sensitivity != sensitivity || !slices.Equal(slices.Sorted(slices.Values(request.Scopes)), slices.Sorted(slices.Values(strings.Split(scopes, ",")))) {
		return fmt.Errorf("%w: captured input %s requires a literal replacement with its existing storage sensitivity and scopes", ErrInvalidVariable, name)
	}
	return nil
}

func AddRecoveredInput(result *RecoveredWorkload, storageKey, name, service, kind, origin, category string) {
	value, exists := result.Environment[storageKey]
	if !exists || result.Adoption == nil {
		return
	}
	sensitivity := "plain"
	if secretShapedKey(name) || containsURLCredentials(value) || browserSecretValue.MatchString(value) {
		sensitivity = "secret"
	}
	input := RecoveredInput{StorageKey: storageKey, Name: name, Service: service, Kind: kind, Origin: origin, Category: category, Sensitivity: sensitivity, Retained: true, Empty: value == ""}
	for i := range result.Adoption.Inputs {
		if result.Adoption.Inputs[i].StorageKey == storageKey {
			result.Adoption.Inputs[i] = input
			return
		}
	}
	result.Adoption.Inputs = append(result.Adoption.Inputs, input)
	sort.Slice(result.Adoption.Inputs, func(i, j int) bool {
		return result.Adoption.Inputs[i].StorageKey < result.Adoption.Inputs[j].StorageKey
	})
}

func recoveredInputBindings(source DraftSourceConfig) []RecoveredInput {
	var inputs []RecoveredInput
	for _, document := range source.ComposeFiles {
		var model map[string]any
		if yaml.Unmarshal([]byte(document.Content), &model) != nil {
			continue
		}
		for service, raw := range object(model["services"]) {
			for field, kind := range map[string]string{"environment": "environment", "labels": "label"} {
				for name, raw := range object(object(raw)[field]) {
					value, ok := raw.(string)
					if !ok || !isVariableExpression(value) {
						continue
					}
					key := strings.TrimSuffix(strings.TrimPrefix(value, "${"), "}")
					if !strings.HasPrefix(key, "JD_IMPORT_") {
						continue
					}
					category, sensitivity := "application", "plain"
					if kind != "environment" {
						category = "runtime_setting"
					}
					if secretShapedKey(name) {
						sensitivity = "secret"
					}
					inputs = append(inputs, RecoveredInput{StorageKey: key, Name: name, Service: service, Kind: kind, Origin: "container", Category: category, Sensitivity: sensitivity})
				}
			}
		}
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].StorageKey < inputs[j].StorageKey })
	return inputs
}

func (s *PlanningStore) importedInputs(ctx context.Context, environmentID int64, variables []DeploymentVariable) ([]RecoveredInput, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT provenance_json FROM deploy_releases WHERE environment_id=? AND plan_revision=1 AND json_extract(provenance_json,'$.adopted')=1 ORDER BY id LIMIT 1),'')`, environmentID).Scan(&raw); err != nil {
		return nil, err
	}
	var provenance struct {
		Inputs []RecoveredInput `json:"inputs"`
	}
	_ = json.Unmarshal([]byte(raw), &provenance)
	if len(provenance.Inputs) == 0 && raw != "" {
		var config string
		if err := s.db.QueryRowContext(ctx, `SELECT config_json FROM deploy_sources WHERE environment_id=? AND revision=1`, environmentID).Scan(&config); err != nil {
			return nil, err
		}
		var source DraftSourceConfig
		if json.Unmarshal([]byte(config), &source) == nil {
			provenance.Inputs = recoveredInputBindings(source)
		}
	}
	active := map[string]DeploymentVariable{}
	for _, variable := range variables {
		active[variable.Name] = variable
	}
	for i := range provenance.Inputs {
		variable, exists := active[provenance.Inputs[i].StorageKey]
		provenance.Inputs[i].Retained = exists
		provenance.Inputs[i].Empty = exists && variable.ValueDigest == digestBytes([]byte(""))
	}
	return provenance.Inputs, nil
}

// Runtime inspection has already resolved interpolation. Only references
// created by this recovery are allowed to retain Compose expression semantics.
func escapeRecoveredLiterals(value any, bindings map[string]string) {
	escape := func(text string) string {
		if isVariableExpression(text) {
			key := strings.TrimSuffix(strings.TrimPrefix(text, "${"), "}")
			if _, ok := bindings[key]; ok {
				return text
			}
		}
		return strings.ReplaceAll(text, "$", "$$")
	}
	transformRecoveredStrings(value, escape)
}

// Compose config produces a reusable document. Decode its existing escaping
// before overlaying Engine strings, then encode the complete recovered model once.
func decodeComposeRenderedLiterals(value any) {
	transformRecoveredStrings(value, func(text string) string { return strings.ReplaceAll(text, "$$", "$") })
}

func transformRecoveredStrings(value any, transform func(string) string) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if text, ok := child.(string); ok {
				typed[key] = transform(text)
			} else {
				transformRecoveredStrings(child, transform)
			}
		}
	case map[string]string:
		for key, text := range typed {
			typed[key] = transform(text)
		}
	case []any:
		for i, child := range typed {
			if text, ok := child.(string); ok {
				typed[i] = transform(text)
			} else {
				transformRecoveredStrings(child, transform)
			}
		}
	case []string:
		for i, text := range typed {
			typed[i] = transform(text)
		}
	}
}

func (d *Draft) validateRecoveredInputBindings(configuration PlanConfiguration) error {
	if d.Data.Adoption == nil || d.Data.Source == nil {
		return nil
	}
	declared := map[string]PlannedVariable{}
	for _, variable := range configuration.Variables {
		declared[variable.Name] = variable
	}
	baselineVariables := map[string]PlannedVariable{}
	for _, variable := range d.Data.Adoption.BaselineConfiguration.Variables {
		baselineVariables[variable.Name] = variable
	}
	for _, input := range d.Data.Adoption.Inputs {
		bound := false
		for _, document := range d.Data.Source.ComposeFiles {
			if strings.Contains(document.Content, "${"+input.StorageKey+"}") {
				bound = true
				break
			}
		}
		if !bound {
			continue
		}
		variable, ok := declared[input.StorageKey]
		_, retained := d.environment[input.StorageKey]
		baseline := baselineVariables[input.StorageKey]
		sameScopes := slices.Equal(slices.Sorted(slices.Values(variable.Scopes)), slices.Sorted(slices.Values(baseline.Scopes)))
		if !ok || !retained || variable.Sensitivity != baseline.Sensitivity || !sameScopes || (variable.ValueMode != "literal" && !(variable.ValueMode == "" && baseline.ValueMode == "")) || variable.Reference != "" || variable.Generate != 0 {
			return fmt.Errorf("%w: captured input %s must retain its sealed literal runtime binding", ErrInvalidVariable, input.Name)
		}
	}
	return nil
}
