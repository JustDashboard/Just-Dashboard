package deploy

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
	"github.com/Wayy01/Just-Dashboard/backend/internal/version"
)

// Templates an operator adds to the one-click catalogue: a definition in
// blueprint's own schema, stored in deploy_templates under an id in the
// user- namespace no built-in may use, and validated as a built-in is
// (blueprint.PrepareUserTemplate) and as the planner reads it (every fixture
// planned through RenderBlueprintPlan) before it is stored.
//
// A deployment never reads this table to run. Saving a source that names one
// freezes the definition into the source (WithUserTemplate), and from then on
// the deployment renders, plans and redeploys from that copy through exactly
// the path a built-in takes; editing or deleting the template changes what
// the next deployment of it gets and nothing that already runs.

// TemplateOrigin is how a template added here was made.
type TemplateOrigin string

const (
	TemplateFromCompose    TemplateOrigin = "compose"
	TemplateFromDeployment TemplateOrigin = "deployment"
	TemplateImported       TemplateOrigin = "import"
)

var (
	ErrTemplateNotFound = errors.New("template not found")
	ErrTemplateExists   = errors.New("a template with this name already exists")
	ErrInvalidTemplate  = errors.New("template is invalid")
)

// UserTemplate is one template added here, as the templates page lists it.
type UserTemplate struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Version string         `json:"version"`
	Source  TemplateOrigin `json:"source"`
	// FromDeploymentID is the deployment a template was saved from; it may
	// have been removed since.
	FromDeploymentID int64             `json:"fromDeploymentId,omitempty"`
	Summary          blueprint.Summary `json:"summary"`
	// Deployments is how many deployments' current source names it. Each
	// runs its own frozen copy, so deleting the template stops none of them.
	Deployments int       `json:"deployments"`
	CreatedBy   string    `json:"createdBy"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedBy   string    `json:"updatedBy"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// UserTemplateDetail is a template with its whole definition.
type UserTemplateDetail struct {
	UserTemplate
	Definition *blueprint.Blueprint `json:"definition"`
}

// ComposeTemplateRequest makes a template from a pasted Compose file.
type ComposeTemplateRequest struct {
	Name string `json:"name"`
	// Slug is the id's name part; empty takes it from Name.
	Slug        string             `json:"slug,omitempty"`
	Description string             `json:"description,omitempty"`
	Category    blueprint.Category `json:"category,omitempty"`
	Compose     string             `json:"compose"`
	// Inputs are the operator's declarations of the variables the file
	// reads; a ${VAR} nobody declared becomes an input named after it.
	Inputs []blueprint.Input `json:"inputs,omitempty"`
	// PublicService is the service the domain is routed to, when more than
	// one could be.
	PublicService string `json:"publicService,omitempty"`
	// HealthPath makes the readiness check an HTTP request for this path.
	HealthPath string            `json:"healthPath,omitempty"`
	Access     *blueprint.Access `json:"access,omitempty"`
	DocsURL    string            `json:"docsUrl,omitempty"`
	// DryRun answers with the definition the file makes, stores nothing.
	DryRun bool `json:"dryRun,omitempty"`
}

// DeploymentTemplateRequest saves a deployment's frozen plan as a template.
type DeploymentTemplateRequest struct {
	DeploymentID int64 `json:"deploymentId"`
	// EnvironmentID is the environment whose live release is read; zero is
	// production.
	EnvironmentID int64  `json:"environmentId,omitempty"`
	Name          string `json:"name"`
	Slug          string `json:"slug,omitempty"`
	Description   string `json:"description,omitempty"`
	DryRun        bool   `json:"dryRun,omitempty"`
}

// TemplatePreview is a definition a dry run built and did not store.
type TemplatePreview struct {
	Definition *blueprint.Blueprint `json:"definition"`
	Summary    blueprint.Summary    `json:"summary"`
	// Exists is whether a template already has this id, so saving it would
	// be refused until one of them is renamed.
	Exists bool `json:"exists"`
}

const (
	maxTemplateName        = 80
	maxTemplateDescription = 600
)

// templateSubjectRE is the definition's own id at the front of a refusal,
// which the operator is looking at already.
var templateSubjectRE = regexp.MustCompile(`^` + blueprint.UserPrefix + `[a-z0-9-]+: `)

// invalidTemplate words a refusal for the person who wrote the definition:
// the catalogue's own sentence, without the sentinels it was carried in.
func invalidTemplate(err error) error {
	if errors.Is(err, ErrInvalidTemplate) {
		return err
	}
	message := err.Error()
	for _, prefix := range []string{
		blueprint.ErrInvalidBlueprint.Error() + ": ", blueprint.ErrUnknownField.Error() + ": ",
		blueprint.ErrInvalidInput.Error() + ": ",
	} {
		message = strings.TrimPrefix(message, prefix)
	}
	return fmt.Errorf("%w: %s", ErrInvalidTemplate, templateSubjectRE.ReplaceAllString(message, ""))
}

// TemplateRefusal is a refused template's reason without the sentinel, as a
// route answers it.
func TemplateRefusal(err error) string {
	return strings.TrimPrefix(err.Error(), ErrInvalidTemplate.Error()+": ")
}

// templateMeta is what every way of making a template shares: the id from
// the name or the slug, and who added it on which day with which dashboard.
func templateMeta(name, slug, description, actor string, now time.Time) (blueprint.TemplateMeta, error) {
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if name == "" || len(name) > maxTemplateName || strings.ContainsAny(name, "\x00\r\n") {
		return blueprint.TemplateMeta{}, fmt.Errorf("%w: a template's name is 1-%d characters on one line", ErrInvalidTemplate, maxTemplateName)
	}
	if len(description) > maxTemplateDescription || strings.ContainsRune(description, 0) {
		return blueprint.TemplateMeta{}, fmt.Errorf("%w: a template's description is at most %d characters", ErrInvalidTemplate, maxTemplateDescription)
	}
	from := slug
	if strings.TrimSpace(from) == "" {
		from = name
	}
	id, err := blueprint.UserID(from)
	if err != nil {
		return blueprint.TemplateMeta{}, invalidTemplate(err)
	}
	maintainer := strings.TrimSpace(actor)
	if maintainer == "" {
		maintainer = "This dashboard"
	}
	return blueprint.TemplateMeta{
		ID: id, Name: name, Description: description, Maintainer: maintainer,
		ReviewedAt: now.UTC().Format("2006-01-02"), MinimumDashboard: version.Version,
	}, nil
}

// prepareTemplate holds a definition to every rule before it is stored: the
// catalogue's (PrepareUserTemplate, which records the fixtures' digests),
// that this dashboard can deploy it end to end, and the planner's — each
// fixture is planned exactly as a draft from the template would be, so a
// Compose file the planner refuses is refused here rather than after a
// deployment was started from it.
func prepareTemplate(definition *blueprint.Blueprint) ([]byte, error) {
	raw, err := blueprint.PrepareUserTemplate(definition)
	if err != nil {
		return nil, invalidTemplate(err)
	}
	if supported, reason := blueprint.DeploymentSupport(definition); !supported {
		return nil, fmt.Errorf("%w: %s", ErrInvalidTemplate, reason)
	}
	for _, fixture := range definition.Fixtures {
		source := DraftSourceConfig{
			Kind: BlueprintSourceKind(definition), Mode: SourceModeBlueprint,
			BlueprintID: definition.ID, BlueprintVersion: definition.Version,
			BlueprintInputs: fixture.Inputs, BlueprintDefinition: raw,
		}
		if _, err := RenderBlueprintPlan(source, definition.ID); err != nil {
			return nil, invalidTemplate(fmt.Errorf("fixture %q does not plan: %w", fixture.Name, err))
		}
	}
	return raw, nil
}

func compactJSON(raw []byte) json.RawMessage {
	var buffer bytes.Buffer
	if json.Compact(&buffer, raw) != nil {
		return append(json.RawMessage(nil), raw...)
	}
	return buffer.Bytes()
}

// PreviewComposeTemplate is the definition a pasted Compose file makes,
// checked as it would be stored, without storing it.
func (s *PlanningStore) PreviewComposeTemplate(ctx context.Context, request ComposeTemplateRequest, actor string) (*TemplatePreview, error) {
	definition, _, err := s.composeTemplate(request, actor)
	if err != nil {
		return nil, err
	}
	return s.preview(ctx, definition)
}

func (s *PlanningStore) composeTemplate(request ComposeTemplateRequest, actor string) (*blueprint.Blueprint, []byte, error) {
	meta, err := templateMeta(request.Name, request.Slug, request.Description, actor, s.now())
	if err != nil {
		return nil, nil, err
	}
	meta.Category, meta.Access, meta.DocsURL = request.Category, request.Access, strings.TrimSpace(request.DocsURL)
	definition, err := blueprint.FromCompose(blueprint.ComposeTemplate{
		TemplateMeta: meta, Document: request.Compose, Inputs: request.Inputs,
		PublicService: strings.TrimSpace(request.PublicService), HealthPath: strings.TrimSpace(request.HealthPath),
	})
	if err != nil {
		return nil, nil, invalidTemplate(err)
	}
	raw, err := prepareTemplate(definition)
	if err != nil {
		return nil, nil, err
	}
	return definition, raw, nil
}

func (s *PlanningStore) preview(ctx context.Context, definition *blueprint.Blueprint) (*TemplatePreview, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_templates WHERE slug = ?`, definition.ID).Scan(&exists)
	if err != nil {
		return nil, err
	}
	return &TemplatePreview{Definition: definition, Summary: blueprint.Summarize(definition), Exists: exists > 0}, nil
}

// CreateComposeTemplate stores the template a pasted Compose file makes.
func (s *PlanningStore) CreateComposeTemplate(ctx context.Context, request ComposeTemplateRequest, actor string) (*UserTemplateDetail, error) {
	definition, raw, err := s.composeTemplate(request, actor)
	if err != nil {
		return nil, err
	}
	return s.insertTemplate(ctx, definition, raw, TemplateFromCompose, 0, actor)
}

// ImportTemplate stores a definition exported from this or another
// dashboard, or written by hand. An id outside the user namespace is put in
// it, so an exported built-in becomes a template of this dashboard's own.
func (s *PlanningStore) ImportTemplate(ctx context.Context, raw []byte, actor string) (*UserTemplateDetail, error) {
	definition, err := blueprint.DecodeUserTemplate(raw)
	if err != nil {
		return nil, invalidTemplate(err)
	}
	if definition.ID == "" {
		id, err := blueprint.UserID(definition.Name)
		if err != nil {
			return nil, invalidTemplate(err)
		}
		definition.ID = id
	}
	stored, err := prepareTemplate(definition)
	if err != nil {
		return nil, err
	}
	return s.insertTemplate(ctx, definition, stored, TemplateImported, 0, actor)
}

// FrozenPlainValues is a run's plain literal variables, by name: the values
// a release ran with, read from the revisions the run froze. A secret's
// value is never opened here — the query's rows are filtered before any is
// decrypted — so a template saved from a deployment cannot carry one.
func (s *PlanningStore) FrozenPlainValues(ctx context.Context, runID, environmentID int64) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT v.key, v.value_enc, v.value_mode
		  FROM deploy_run_variable_revisions rv
		  JOIN deploy_variable_revisions v ON v.id = rv.variable_revision_id
		 WHERE rv.run_id = ? AND v.environment_id = ? AND v.sensitivity = 'plain'
		   AND v.value_mode IN ('', 'literal')
		 ORDER BY rv.ordinal`, runID, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var name, sealed, mode string
		if err := rows.Scan(&name, &sealed, &mode); err != nil {
			return nil, err
		}
		value, err := s.sealer.Open(sealed)
		if err != nil {
			return nil, fmt.Errorf("deployment variable %s cannot be decrypted", name)
		}
		if _, reference := parseStoredReference(value, mode); reference {
			continue
		}
		values[name] = value
	}
	return values, rows.Err()
}

// DeploymentTemplate is the template a deployment's frozen plan makes
// (TemplateFromPlan), checked as it would be stored.
func (s *PlanningStore) DeploymentTemplate(
	ctx context.Context,
	plan *StoredExecutionPlan,
	runID, environmentID int64,
	request DeploymentTemplateRequest,
	actor string,
) (*blueprint.Blueprint, []byte, error) {
	meta, err := templateMeta(request.Name, request.Slug, request.Description, actor, s.now())
	if err != nil {
		return nil, nil, err
	}
	plain, err := s.FrozenPlainValues(ctx, runID, environmentID)
	if err != nil {
		return nil, nil, err
	}
	definition, err := TemplateFromPlan(plan, plain, meta)
	if err != nil {
		return nil, nil, invalidTemplate(err)
	}
	raw, err := prepareTemplate(definition)
	if err != nil {
		return nil, nil, err
	}
	return definition, raw, nil
}

// PreviewDeploymentTemplate answers a dry run of DeploymentTemplate.
func (s *PlanningStore) PreviewDeploymentTemplate(ctx context.Context, definition *blueprint.Blueprint) (*TemplatePreview, error) {
	return s.preview(ctx, definition)
}

// SaveDeploymentTemplate stores the template a deployment's plan made.
func (s *PlanningStore) SaveDeploymentTemplate(
	ctx context.Context,
	definition *blueprint.Blueprint,
	raw []byte,
	deploymentID int64,
	actor string,
) (*UserTemplateDetail, error) {
	return s.insertTemplate(ctx, definition, raw, TemplateFromDeployment, deploymentID, actor)
}

func (s *PlanningStore) insertTemplate(
	ctx context.Context,
	definition *blueprint.Blueprint,
	raw []byte,
	origin TemplateOrigin,
	fromProject int64,
	actor string,
) (*UserTemplateDetail, error) {
	now := s.now().UTC().Unix()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO deploy_templates(slug, name, version, source, definition_json, from_project_id,
		  created_by, updated_by, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		definition.ID, definition.Name, definition.Version, origin, string(raw), fromProject, actor, actor, now, now)
	if err != nil {
		if isUniqueConstraint(err) {
			return nil, fmt.Errorf("%w: %s; rename one of them", ErrTemplateExists, definition.ID)
		}
		return nil, err
	}
	return s.UserTemplate(ctx, definition.ID)
}

// UpdateTemplate replaces a template's definition. Its id is its name in the
// catalogue and in every source that deploys it, so it does not change; a
// definition that differs from the stored one under the same version is a
// new patch version, since a version names exactly one definition.
func (s *PlanningStore) UpdateTemplate(ctx context.Context, id string, raw []byte, actor string) (*UserTemplateDetail, error) {
	current, err := s.UserTemplate(ctx, id)
	if err != nil {
		return nil, err
	}
	definition, err := blueprint.DecodeUserTemplate(raw)
	if err != nil {
		return nil, invalidTemplate(err)
	}
	if definition.ID == "" {
		definition.ID = id
	}
	if definition.ID != id {
		return nil, fmt.Errorf("%w: a template's id does not change; import %s as a template of its own instead", ErrInvalidTemplate, definition.ID)
	}
	if sameDefinition(definition, current.Definition) {
		return current, nil
	}
	if definition.Version == current.Version {
		definition.Version = nextPatch(current.Version)
	}
	stored, err := prepareTemplate(definition)
	if err != nil {
		return nil, err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE deploy_templates SET name = ?, version = ?, definition_json = ?, updated_by = ?, updated_at = ?
		 WHERE slug = ?`, definition.Name, definition.Version, string(stored), actor, s.now().UTC().Unix(), id)
	if err != nil {
		return nil, err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return nil, fmt.Errorf("%w: %s", ErrTemplateNotFound, id)
	}
	return s.UserTemplate(ctx, id)
}

// sameDefinition compares two definitions as the catalogue reads them, the
// fixtures' recorded digests aside: those are the server's to write.
func sameDefinition(a, b *blueprint.Blueprint) bool {
	if a == nil || b == nil {
		return false
	}
	bare := func(definition *blueprint.Blueprint) []byte {
		copied := *definition
		copied.Fixtures = append([]blueprint.Fixture(nil), definition.Fixtures...)
		for index := range copied.Fixtures {
			copied.Fixtures[index].Digest = ""
		}
		encoded, _ := json.Marshal(copied)
		return encoded
	}
	return bytes.Equal(bare(a), bare(b))
}

func nextPatch(current string) string {
	parts := strings.Split(current, ".")
	if len(parts) != 3 {
		return "1.0.0"
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return "1.0.0"
	}
	return parts[0] + "." + parts[1] + "." + strconv.Itoa(patch+1)
}

// DeleteTemplate removes a template from the catalogue. Every deployment
// made from it keeps running, and redeploying, from the copy its source
// froze; only new deployments can no longer choose it.
func (s *PlanningStore) DeleteTemplate(ctx context.Context, id string) (*UserTemplate, error) {
	current, err := s.UserTemplate(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM deploy_templates WHERE slug = ?`, id); err != nil {
		return nil, err
	}
	return &current.UserTemplate, nil
}

type templateRow struct {
	record     UserTemplate
	definition []byte
}

const templateColumns = `slug, name, version, source, definition_json, from_project_id,
	created_by, updated_by, created_at, updated_at`

func scanTemplate(scan func(...any) error) (*templateRow, error) {
	var row templateRow
	var definition string
	var created, updated int64
	if err := scan(&row.record.ID, &row.record.Name, &row.record.Version, &row.record.Source, &definition,
		&row.record.FromDeploymentID, &row.record.CreatedBy, &row.record.UpdatedBy, &created, &updated); err != nil {
		return nil, err
	}
	row.definition = []byte(definition)
	row.record.CreatedAt, row.record.UpdatedAt = unixTime(created), unixTime(updated)
	return &row, nil
}

// templateUses is how many deployments' desired source names each template
// added here.
func (s *PlanningStore) templateUses(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT json_extract(src.config_json, '$.blueprintId') AS template, COUNT(DISTINCT e.project_id)
		  FROM deploy_environments e
		  JOIN deploy_sources src ON src.environment_id = e.id AND src.revision = e.desired_revision
		 WHERE e.archived_at = 0 AND json_extract(src.config_json, '$.blueprintId') LIKE 'user-%'
		 GROUP BY template`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	uses := map[string]int{}
	for rows.Next() {
		var id string
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, err
		}
		uses[id] = count
	}
	return uses, rows.Err()
}

// UserTemplates lists every template added here, by name.
func (s *PlanningStore) UserTemplates(ctx context.Context) ([]UserTemplate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+templateColumns+` FROM deploy_templates ORDER BY name COLLATE NOCASE, slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stored []*templateRow
	for rows.Next() {
		row, err := scanTemplate(rows.Scan)
		if err != nil {
			return nil, err
		}
		stored = append(stored, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	uses, err := s.templateUses(ctx)
	if err != nil {
		return nil, err
	}
	templates := make([]UserTemplate, 0, len(stored))
	for _, row := range stored {
		// A row that no longer parses — a rule tightened since it was
		// stored — stays listed, so it can be exported, fixed and deleted,
		// and is not offered: its summary says why it cannot be deployed.
		definition, err := blueprint.ParseUserTemplate(row.definition)
		if err != nil {
			row.record.Summary = unreadableTemplateSummary(row.record, err)
		} else {
			row.record.Summary = blueprint.Summarize(definition)
		}
		row.record.Deployments = uses[row.record.ID]
		templates = append(templates, row.record)
	}
	return templates, nil
}

func unreadableTemplateSummary(record UserTemplate, err error) blueprint.Summary {
	return blueprint.Summary{
		ID: record.ID, Version: record.Version, Name: record.Name, Origin: blueprint.OriginUser,
		Category: blueprint.CategoryHTTP, SourceKind: "blueprint",
		UnavailableReason: "This template no longer meets the catalogue's rules: " + invalidTemplate(err).Error(),
	}
}

// UserTemplateSummaries is every deployable template added here, in the
// catalogue's listing shape, for the picker to show beside the built-ins.
func (s *PlanningStore) UserTemplateSummaries(ctx context.Context) ([]blueprint.Summary, error) {
	templates, err := s.UserTemplates(ctx)
	if err != nil {
		return nil, err
	}
	summaries := make([]blueprint.Summary, 0, len(templates))
	for _, template := range templates {
		summaries = append(summaries, template.Summary)
	}
	return summaries, nil
}

// UserTemplate reads one template added here with its definition.
func (s *PlanningStore) UserTemplate(ctx context.Context, id string) (*UserTemplateDetail, error) {
	if !blueprint.IsUserID(id) {
		return nil, fmt.Errorf("%w: %s", ErrTemplateNotFound, id)
	}
	row, err := scanTemplate(s.db.QueryRowContext(ctx, `SELECT `+templateColumns+` FROM deploy_templates WHERE slug = ?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ErrTemplateNotFound, id)
	}
	if err != nil {
		return nil, err
	}
	definition, err := blueprint.ParseUserTemplate(row.definition)
	if err != nil {
		return nil, invalidTemplate(err)
	}
	row.record.Summary = blueprint.Summarize(definition)
	uses, err := s.templateUses(ctx)
	if err != nil {
		return nil, err
	}
	row.record.Deployments = uses[id]
	return &UserTemplateDetail{UserTemplate: row.record, Definition: definition}, nil
}

// ResolveTemplate is the picker's resolver: a built-in from the catalogue the
// binary ships, and otherwise a template added here. The namespaces cannot
// overlap, so the store is asked only for an id no built-in can have.
func (s *PlanningStore) ResolveTemplate(ctx context.Context, id, wantVersion string) (*blueprint.Blueprint, error) {
	if !blueprint.IsUserID(id) {
		return blueprint.GetVersion(id, wantVersion)
	}
	stored, err := s.UserTemplate(ctx, id)
	if errors.Is(err, ErrTemplateNotFound) {
		return nil, fmt.Errorf("%w: %s", blueprint.ErrNotFound, id)
	}
	if err != nil {
		return nil, err
	}
	if wantVersion != "" && wantVersion != stored.Version {
		return nil, fmt.Errorf("%w: %s is at version %s now, not %s", blueprint.ErrNotFound, id, stored.Version, wantVersion)
	}
	return stored.Definition, nil
}

// WithUserTemplate freezes the template a source names into the source, so
// the deployment renders from that copy on every later run whatever becomes
// of the template. It is what every save of a source does before anything
// else reads it; a definition the client sent is never kept, and a built-in
// is never given one.
func (s *PlanningStore) WithUserTemplate(ctx context.Context, source DraftSourceConfig) (DraftSourceConfig, error) {
	source.BlueprintDefinition = nil
	if source.Mode != SourceModeBlueprint || !blueprint.IsUserID(source.BlueprintID) {
		return source, nil
	}
	var raw, current string
	err := s.db.QueryRowContext(ctx, `SELECT definition_json, version FROM deploy_templates WHERE slug = ?`, source.BlueprintID).
		Scan(&raw, &current)
	if errors.Is(err, sql.ErrNoRows) {
		return source, fmt.Errorf("%w: the template %s is no longer on this dashboard", ErrInvalidSource, source.BlueprintID)
	}
	if err != nil {
		return source, err
	}
	if source.BlueprintVersion != "" && source.BlueprintVersion != current {
		return source, fmt.Errorf("%w: %s is at version %s now, not %s; choose it again", ErrInvalidSource, source.BlueprintID, current, source.BlueprintVersion)
	}
	source.BlueprintVersion = current
	source.BlueprintDefinition = compactJSON([]byte(raw))
	return source, nil
}
