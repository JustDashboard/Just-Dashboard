package deploy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// A staging environment is a second, long-lived copy of production inside the
// same deployment: its own branch, domain, variables, volumes and database
// links, built and deployed exactly as production is, so that what production
// later runs is an image staging already ran (promotion.go). There is at most
// one per deployment and it is always named "staging".
const stagingSlug = "staging"

var (
	ErrStagingExists = errors.New("this deployment already has a staging environment")
	// ErrStagingUnsupported names the plans a second copy cannot run beside
	// production on one host without fighting it for the same resource.
	ErrStagingUnsupported   = errors.New("this deployment cannot have a staging environment")
	ErrStagingDomainTaken   = errors.New("that domain already belongs to another environment")
	ErrStagingDomainInvalid = errors.New("staging's domain must be a hostname such as staging.example.com")
	ErrNotStaging           = errors.New("only a staging environment can be removed here")
	// ErrStagingIsolation refuses a database staging would share with
	// another environment.
	ErrStagingIsolation = errors.New("staging needs a database of its own: its release tasks would change the database it shares")
	// ErrPromotionOnlyNeedsStaging refuses to remove the one environment a
	// promotion-only production can still change through.
	ErrPromotionOnlyNeedsStaging = errors.New("production takes only promotions from staging; turn that off before removing staging")
)

// EnvironmentSummary is one long-lived environment of a deployment, for the
// environment switcher. Previews are listed with their pull requests instead.
type EnvironmentSummary struct {
	ID              int64           `json:"id"`
	Name            string          `json:"name"`
	Slug            string          `json:"slug"`
	Kind            EnvironmentKind `json:"kind"`
	DesiredRevision int             `json:"desiredRevision"`
	LiveReleaseID   int64           `json:"liveReleaseId,omitempty"`
	// PromotionOnly is production refusing builds in favour of promotions.
	PromotionOnly bool      `json:"promotionOnly,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

// StagingRequest is what adding staging decides beyond the copy itself. Ref
// and Revision are resolved by the caller against the remote before the
// transaction, so a branch that does not exist is refused before anything is
// written and nothing in the transaction waits on the network.
type StagingRequest struct {
	Ref         string
	Revision    string
	Domain      string
	CopySecrets bool
	Actor       string
}

// StagingSkipped is a production setting the copy deliberately left behind,
// and why, so the operator knows what staging still needs before it deploys.
type StagingSkipped struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

// StagingCopy reports what adding staging copied, what it left behind and
// what it changed on the way, by name only: no value leaves the store here.
type StagingCopy struct {
	Environment EnvironmentSummary `json:"environment"`
	Copied      []string           `json:"copied"`
	Skipped     []StagingSkipped   `json:"skipped"`
	Adjusted    []string           `json:"adjusted"`
}

// stagingVolumeName is the named volume a staging mount gets in place of
// production's. Only names with this prefix are ever deleted with staging.
func stagingVolumeName(environmentID int64, target string) string {
	digest := sha256.Sum256([]byte(target))
	return fmt.Sprintf("%s%s", stagingVolumePrefix(environmentID), hex.EncodeToString(digest[:8]))
}

func stagingVolumePrefix(environmentID int64) string {
	return fmt.Sprintf("jd-staging-e%d-", environmentID)
}

type environmentQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// stagingEnvironmentTx is the deployment's staging environment, or
// ErrEnvironmentNotFound when it has none.
func stagingEnvironmentTx(ctx context.Context, q environmentQuery, projectID int64) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `SELECT id FROM deploy_environments
		 WHERE project_id = ? AND kind = 'staging' AND archived_at = 0 ORDER BY id LIMIT 1`, projectID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrEnvironmentNotFound
	}
	return id, err
}

// ProjectEnvironments lists a deployment's production and staging
// environments, production first.
func (s *OrchestrationStore) ProjectEnvironments(ctx context.Context, projectID int64) ([]EnvironmentSummary, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_projects WHERE id = ?`, projectID).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id, e.name, e.slug, e.kind, e.desired_revision, e.live_release_id,
		       COALESCE(g.promotion_only, 0), e.created_at
		  FROM deploy_environments e LEFT JOIN deploy_git_policies g ON g.environment_id = e.id
		 WHERE e.project_id = ? AND e.archived_at = 0 AND e.kind IN ('production', 'staging')
		 ORDER BY CASE e.kind WHEN 'production' THEN 0 ELSE 1 END, e.id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []EnvironmentSummary{}
	for rows.Next() {
		var environment EnvironmentSummary
		var created int64
		var promotionOnly int
		if err := rows.Scan(&environment.ID, &environment.Name, &environment.Slug, &environment.Kind,
			&environment.DesiredRevision, &environment.LiveReleaseID, &promotionOnly, &created); err != nil {
			return nil, err
		}
		environment.PromotionOnly = promotionOnly != 0
		environment.CreatedAt = unixTime(created)
		result = append(result, environment)
	}
	return result, rows.Err()
}

// EnvironmentKind is the kind of one of a deployment's environments, archived
// or not, so a caller can tell staging from production before acting on it.
func (s *OrchestrationStore) EnvironmentKind(ctx context.Context, projectID, environmentID int64) (EnvironmentKind, error) {
	var kind EnvironmentKind
	err := s.db.QueryRowContext(ctx, `SELECT kind FROM deploy_environments WHERE id = ? AND project_id = ? AND archived_at = 0`,
		environmentID, projectID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrEnvironmentNotFound
	}
	return kind, err
}

// CreateStagingEnvironment copies production's desired configuration into a
// new staging environment at revision 1, in one transaction.
//
// What crosses and what does not is the point of the environment:
//
//   - The build and runtime plans, health checks and release tasks are
//     copied: staging should build and start exactly as production does, and
//     its release tasks run against staging's own variables, so a migration
//     reaches staging's database.
//   - Plain variables are copied. Secrets are not unless asked for, because
//     staging runs code production has not shipped; a production payment or
//     mail key there acts on real customers. A reference to production's
//     database, credential, domain or service is never copied — it resolves to
//     production's own resource — but a reference to a shared variable is,
//     since sharing across the project is what a shared variable is for.
//   - Database links, storage links and backup gates are not copied: a staging
//     release task would otherwise migrate production's schema from code that
//     was never promoted.
//   - Every mount becomes a fresh volume of staging's own, fixed host ports
//     are dropped because production holds them, and production's domains
//     stay production's; staging gets the one it is given.
func (s *PlanningStore) CreateStagingEnvironment(
	ctx context.Context,
	projectID int64,
	request StagingRequest,
) (*StagingCopy, error) {
	if strings.TrimSpace(request.Actor) == "" {
		return nil, fmt.Errorf("%w: actor is required", ErrInvalidVariable)
	}
	domain := strings.ToLower(strings.TrimSpace(request.Domain))
	if domain != "" && !plannedDomainRE.MatchString(domain) {
		return nil, ErrStagingDomainInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var productionID int64
	var revision, downtime int
	var strategy string
	var projectArchived int64
	err = tx.QueryRowContext(ctx, `
		SELECT e.id, e.desired_revision, e.strategy, e.expected_downtime, p.archived_at
		  FROM deploy_environments e JOIN deploy_projects p ON p.id = e.project_id
		 WHERE e.project_id = ? AND e.slug = 'production' AND e.archived_at = 0`, projectID).
		Scan(&productionID, &revision, &strategy, &downtime, &projectArchived)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if projectArchived != 0 {
		return nil, ErrProjectArchived
	}
	var taken int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_environments WHERE project_id = ? AND (slug = ? OR (kind = 'staging' AND archived_at = 0))`,
		projectID, stagingSlug).Scan(&taken); err != nil {
		return nil, err
	}
	if taken != 0 {
		return nil, ErrStagingExists
	}

	var sourceKind, sourceConfigJSON, identityJSON string
	var credentialID int64
	if err := tx.QueryRowContext(ctx, `SELECT kind, config_json, credential_id, identity_json FROM deploy_sources WHERE environment_id = ? AND revision = ?`,
		productionID, revision).Scan(&sourceKind, &sourceConfigJSON, &credentialID, &identityJSON); err != nil {
		return nil, fmt.Errorf("%w: production's source plan is missing", ErrInvalidPlan)
	}
	var method, buildJSON, evidenceJSON, runtimeJSON string
	if err := tx.QueryRowContext(ctx, `
		SELECT b.method, b.config_json, b.evidence_json, r.config_json
		  FROM deploy_build_plans b JOIN deploy_runtime_plans r ON r.environment_id = b.environment_id AND r.revision = b.revision
		 WHERE b.environment_id = ? AND b.revision = ?`, productionID, revision).
		Scan(&method, &buildJSON, &evidenceJSON, &runtimeJSON); err != nil {
		return nil, fmt.Errorf("%w: production's build plan is missing", ErrInvalidPlan)
	}
	var source DraftSourceConfig
	var identity SourceIdentity
	var build BuildPlanConfig
	var runtime RuntimePlanConfig
	if json.Unmarshal([]byte(sourceConfigJSON), &source) != nil || json.Unmarshal([]byte(identityJSON), &identity) != nil ||
		json.Unmarshal([]byte(buildJSON), &build) != nil || json.Unmarshal([]byte(runtimeJSON), &runtime) != nil {
		return nil, fmt.Errorf("%w: production's plan is malformed", ErrInvalidPlan)
	}
	build.Method = BuildMethod(method)
	switch {
	case build.Method == BuildLegacyCompose:
		return nil, fmt.Errorf("%w: a compatibility deployment has no release engine to build a second environment", ErrStagingUnsupported)
	case build.Method == BuildCompose || SourceKind(sourceKind) == SourceCompose:
		return nil, fmt.Errorf("%w: a Compose stack's container names, networks and ports would collide with production's", ErrStagingUnsupported)
	case SourceKind(sourceKind) == SourceImport:
		return nil, fmt.Errorf("%w: adopt the existing workload before adding staging", ErrStagingUnsupported)
	case runtime.HostNetwork:
		return nil, fmt.Errorf("%w: a container on the host's network would listen on production's ports", ErrStagingUnsupported)
	}

	if request.Ref != "" {
		if !IsRemoteGitSource(source) {
			return nil, ErrRefNotApplicable
		}
		if !validGitObjectID(request.Revision) {
			return nil, fmt.Errorf("%w: staging's branch has no resolved commit", ErrInvalidRef)
		}
		source.Ref, identity.Ref, identity.Revision = request.Ref, request.Ref, request.Revision
	}

	result := &StagingCopy{Copied: []string{}, Skipped: []StagingSkipped{}, Adjusted: []string{}}
	now := s.now().UTC().Unix()
	inserted, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at)
		VALUES(?, 'Staging', ?, 'staging', 1, ?, ?, 0, ?, ?)`, projectID, stagingSlug, strategy, downtime, now, now)
	if err != nil {
		return nil, err
	}
	stagingID, err := inserted.LastInsertId()
	if err != nil {
		return nil, err
	}

	stagedSourceJSON, _ := json.Marshal(source)
	stagedIdentityJSON, _ := json.Marshal(identity)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_sources(environment_id, revision, kind, config_json, credential_id, identity_json, digest, created_at)
		VALUES(?, 1, ?, ?, ?, ?, ?, ?)`, stagingID, sourceKind, string(stagedSourceJSON), credentialID,
		string(stagedIdentityJSON), digestBytes(stagedSourceJSON, stagedIdentityJSON), now); err != nil {
		return nil, err
	}
	stagedBuildJSON, _ := json.Marshal(build)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_build_plans(environment_id, revision, method, config_json, evidence_json, preview, digest, created_at)
		VALUES(?, 1, ?, ?, ?, ?, ?, ?)`, stagingID, method, string(stagedBuildJSON), evidenceJSON,
		renderBuildPreview(build), buildPlanDigest(build), now); err != nil {
		return nil, err
	}

	runtime.PreviewIsolation = false
	if runtime.HostPort != 0 {
		result.Adjusted = append(result.Adjusted, fmt.Sprintf("Host port %d was not copied: production holds it.", runtime.HostPort))
		runtime.HostPort = 0
	}
	for _, port := range runtime.Ports {
		result.Adjusted = append(result.Adjusted, fmt.Sprintf("Published port %d was not copied: production holds it.", port.HostPort))
	}
	runtime.Ports = nil
	for index := range runtime.Mounts {
		mount := &runtime.Mounts[index]
		replacement := stagingVolumeName(stagingID, mount.Target)
		result.Adjusted = append(result.Adjusted, fmt.Sprintf("%s mounts a new, empty volume %s instead of %s.", mount.Target, replacement, mount.Source))
		mount.Source, mount.Ownership = replacement, OwnershipManaged
	}
	// A sign-in for previews only exists to keep work in progress private
	// while production stays public; staging is work in progress too.
	if runtime.ForwardAuth != nil && runtime.ForwardAuth.PreviewsOnly {
		gate := *runtime.ForwardAuth
		gate.PreviewsOnly = false
		runtime.ForwardAuth = &gate
		result.Adjusted = append(result.Adjusted, "Production's sign-in for previews also guards staging.")
	}
	stagedRuntimeJSON, _ := json.Marshal(runtime)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_runtime_plans(environment_id, revision, config_json, preview, digest, created_at)
		VALUES(?, 1, ?, ?, ?, ?)`, stagingID, string(stagedRuntimeJSON), renderRuntimePreview(runtime),
		digestBytes(stagedRuntimeJSON), now); err != nil {
		return nil, err
	}
	if err := copyPreviewChecksTx(ctx, tx, productionID, stagingID, now); err != nil {
		return nil, err
	}

	if domain != "" {
		var claimed int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM deploy_dependencies d
			  JOIN deploy_environments e ON e.id = d.environment_id
			  JOIN deploy_projects p ON p.id = e.project_id
			 WHERE d.kind = 'domain' AND d.release_id = 0 AND lower(d.resource_id) = ?
			   AND e.archived_at = 0 AND p.archived_at = 0`, domain).Scan(&claimed); err != nil {
			return nil, err
		}
		if claimed != 0 {
			return nil, ErrStagingDomainTaken
		}
		config := mustJSON(PlannedDomain{Hostname: domain, HTTPS: true}.domainConfig())
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO deploy_dependencies(environment_id, release_id, kind, ownership, resource_kind, resource_id, config_json, created_at)
			VALUES(?, 0, 'domain', 'managed', 'proxy_site', ?, ?, ?)`, stagingID, domain, string(config), now); err != nil {
			return nil, err
		}
	}
	dependencies, err := tx.QueryContext(ctx, `
		SELECT kind, resource_kind, resource_id FROM deploy_dependencies
		 WHERE environment_id = ? AND release_id = 0 AND kind <> 'domain' ORDER BY id`, productionID)
	if err != nil {
		return nil, err
	}
	for dependencies.Next() {
		var kind, resourceKind, resourceID string
		if err := dependencies.Scan(&kind, &resourceKind, &resourceID); err != nil {
			dependencies.Close()
			return nil, err
		}
		reason := "Production's own resource; link staging's separately."
		if kind == "database" || resourceKind == "database_connection" {
			reason = "Production's database: staging's release tasks would migrate it. Link a database of staging's own."
		}
		result.Skipped = append(result.Skipped, StagingSkipped{Name: resourceKind + " " + resourceID, Kind: kind, Reason: reason})
	}
	if err := dependencies.Close(); err != nil {
		return nil, err
	}

	if err := s.copyStagingVariablesTx(ctx, tx, projectID, productionID, stagingID, request, result); err != nil {
		return nil, err
	}

	policy, err := gitDeploymentPolicy(ctx, tx, productionID)
	if err != nil {
		return nil, err
	}
	// Staging follows its own branch from the start: that is what the branch
	// is for. Production's watch paths and status reporting carry over.
	if err := writeGitPolicyTx(ctx, tx, stagingID, GitDeploymentPolicy{
		Automatic: true, WatchInclude: policy.WatchInclude, WatchExclude: policy.WatchExclude,
		CommitStatuses: policy.CommitStatuses, Revision: 1,
	}, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	result.Environment = EnvironmentSummary{
		ID: stagingID, Name: "Staging", Slug: stagingSlug, Kind: EnvironmentStaging,
		DesiredRevision: 1, CreatedAt: unixTime(now),
	}
	return result, nil
}

func (s *PlanningStore) copyStagingVariablesTx(
	ctx context.Context,
	tx *sql.Tx,
	projectID, productionID, stagingID int64,
	request StagingRequest,
	result *StagingCopy,
) error {
	values, err := s.activeVariableValues(ctx, tx, projectID, productionID)
	if err != nil {
		return err
	}
	type pendingAlias struct {
		value  storedVariableValue
		target string
	}
	copied := map[string]bool{}
	aliases := []pendingAlias{}
	write := func(value storedVariableValue) error {
		if err := s.writeVariableRevisionTx(ctx, tx, stagingID, value.name, value.value, value.sensitivity, value.scopes, request.Actor, value.valueMode); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE deploy_variable_revisions SET copied_from_environment = ?
			 WHERE environment_id = ? AND key = ? AND active = 1`, productionID, stagingID, value.name); err != nil {
			return err
		}
		copied[value.name] = true
		result.Copied = append(result.Copied, value.name)
		return nil
	}
	for _, value := range values {
		if reference, ok := parseStoredReference(value.value, value.valueMode); ok {
			switch reference.Kind {
			case SharedScopeServer, SharedScopeProject:
				if err := write(value); err != nil {
					return err
				}
			case "variable":
				aliases = append(aliases, pendingAlias{value: value, target: reference.Target})
			default:
				result.Skipped = append(result.Skipped, StagingSkipped{
					Name: value.name, Kind: "variable",
					Reason: fmt.Sprintf("Points at production's %s; give staging its own.", reference.Kind),
				})
			}
			continue
		}
		if value.sensitivity != "plain" && !request.CopySecrets {
			result.Skipped = append(result.Skipped, StagingSkipped{
				Name: value.name, Kind: "variable", Reason: "A secret: production's value stays in production.",
			})
			continue
		}
		if err := write(value); err != nil {
			return err
		}
	}
	// An alias means something only beside the variable it reads, so it is
	// copied once that variable has been; a chain settles in a few passes.
	for progress := true; progress; {
		progress = false
		remaining := aliases[:0]
		for _, alias := range aliases {
			if copied[alias.target] {
				if err := write(alias.value); err != nil {
					return err
				}
				progress = true
				continue
			}
			remaining = append(remaining, alias)
		}
		aliases = remaining
	}
	for _, alias := range aliases {
		result.Skipped = append(result.Skipped, StagingSkipped{
			Name: alias.value.name, Kind: "variable",
			Reason: fmt.Sprintf("Reads %s, which was not copied.", alias.target),
		})
	}
	return s.validateActiveVariableGraphTx(ctx, tx, stagingID)
}

// StagingRemoval is what removing staging takes off the host: its release
// containers, its route, its database networks and, when asked, the volumes
// adding staging created. Nothing else is named, so nothing else is touched.
type StagingRemoval struct {
	EnvironmentID int64    `json:"environmentId"`
	Containers    []string `json:"containers"`
	ProxySite     string   `json:"proxySite,omitempty"`
	Networks      []string `json:"networks"`
	Volumes       []string `json:"volumes"`
}

// BeginStagingRemoval archives staging so nothing new can start in it, and
// says what has to come off the host. It refuses while a run is in flight
// there, and while production takes only promotions: staging is then the only
// way production can change.
func (s *PlanningStore) BeginStagingRemoval(ctx context.Context, projectID, environmentID int64, volumes bool) (*StagingRemoval, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var kind EnvironmentKind
	var desired int
	if err := tx.QueryRowContext(ctx, `SELECT kind, desired_revision FROM deploy_environments WHERE id = ? AND project_id = ? AND archived_at = 0`,
		environmentID, projectID).Scan(&kind, &desired); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrEnvironmentNotFound
		}
		return nil, err
	}
	if kind != EnvironmentStaging {
		return nil, ErrNotStaging
	}
	var promotionOnly int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(g.promotion_only), 0) FROM deploy_git_policies g
		  JOIN deploy_environments e ON e.id = g.environment_id
		 WHERE e.project_id = ? AND e.kind = 'production' AND e.archived_at = 0`, projectID).Scan(&promotionOnly); err != nil {
		return nil, err
	}
	if promotionOnly != 0 {
		return nil, ErrPromotionOnlyNeedsStaging
	}
	// A run in staging, or a production promotion still to take one of
	// staging's images, would lose what it is working on.
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_runs
		 WHERE `+activeRunWhere+` AND (environment_id = ? OR (operation = 'promote'
		   AND CAST(json_extract(metadata_json, '$.promotion.environmentId') AS INTEGER) = ?))`,
		environmentID, environmentID).Scan(&active); err != nil {
		return nil, err
	}
	if active != 0 {
		return nil, ErrAlreadyDeploying
	}
	removal := &StagingRemoval{EnvironmentID: environmentID, Containers: []string{}, Networks: []string{}, Volumes: []string{}}
	rows, err := tx.QueryContext(ctx, `
		SELECT runtime_id FROM deploy_release_runtimes
		 WHERE environment_id = ? AND kind = 'container' AND state <> 'removed' ORDER BY release_id`, environmentID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		removal.Containers = append(removal.Containers, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var domains int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_dependencies WHERE environment_id = ? AND kind = 'domain' AND ownership = 'managed'`,
		environmentID).Scan(&domains); err != nil {
		return nil, err
	}
	var routed int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_releases WHERE environment_id = ?`, environmentID).Scan(&routed); err != nil {
		return nil, err
	}
	if domains != 0 || routed != 0 {
		removal.ProxySite = deploymentRouteName(environmentID)
	}
	rows, err = tx.QueryContext(ctx, `SELECT network_id FROM deploy_database_networks WHERE environment_id = ? AND network_id <> ''`, environmentID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		removal.Networks = append(removal.Networks, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if volumes {
		seen := map[string]bool{}
		rows, err = tx.QueryContext(ctx, `SELECT config_json FROM deploy_runtime_plans WHERE environment_id = ? ORDER BY revision`, environmentID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return nil, err
			}
			var runtime RuntimePlanConfig
			if json.Unmarshal([]byte(raw), &runtime) != nil {
				continue
			}
			for _, mount := range runtime.Mounts {
				// Only a volume adding staging made; a mount the operator
				// pointed at anything else is theirs to remove.
				if strings.HasPrefix(mount.Source, stagingVolumePrefix(environmentID)) && !seen[mount.Source] {
					seen[mount.Source] = true
					removal.Volumes = append(removal.Volumes, mount.Source)
				}
			}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deploy_environments SET archived_at = ?, updated_at = ? WHERE id = ?`,
		s.now().UTC().Unix(), s.now().UTC().Unix(), environmentID); err != nil {
		return nil, err
	}
	return removal, tx.Commit()
}

// AbortStagingRemoval puts staging back when something it owns could not be
// removed, so the removal can be tried again rather than leaving resources no
// environment answers for.
func (s *PlanningStore) AbortStagingRemoval(ctx context.Context, environmentID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE deploy_environments SET archived_at = 0, updated_at = ? WHERE id = ? AND kind = 'staging'`,
		s.now().UTC().Unix(), environmentID)
	return err
}

// FinishStagingRemoval records that staging's resources are gone. The slug is
// freed so staging can be added again, its releases stop counting towards
// retention — an image production was promoted from stays, because
// production's own release names it — and its triggers and schedules stop.
func (s *PlanningStore) FinishStagingRemoval(ctx context.Context, environmentID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UTC().Unix()
	for _, statement := range []string{
		`UPDATE deploy_environments SET slug = 'staging-removed-' || id, live_release_id = 0, updated_at = ? WHERE id = ? AND kind = 'staging' AND archived_at <> 0`,
		`UPDATE deploy_releases SET state = 'retired', retired_at = CASE WHEN retired_at = 0 THEN ? ELSE retired_at END WHERE environment_id = ? AND state IN ('live', 'retained', 'candidate')`,
		`UPDATE deploy_release_runtimes SET state = 'removed', updated_at = ? WHERE environment_id = ? AND state <> 'removed'`,
		`UPDATE deploy_triggers SET enabled = 0, updated_at = ? WHERE environment_id = ?`,
		`UPDATE deploy_schedules SET enabled = 0, updated_at = ? WHERE environment_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, now, environmentID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
