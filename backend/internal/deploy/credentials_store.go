package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Credential kinds are the closed vocabulary the source adapters already
// consume through OpenCredential (git_bearer/provider_token as an HTTPS
// bearer header, registry as a scoped username/password) plus git_ssh, which
// this pass adds end to end: a sealed private key written to a 0600 file for
// GIT_SSH_COMMAND exactly where the bearer path already handles HTTPS.
const (
	CredentialGitBearer     = "git_bearer"
	CredentialGitSSH        = "git_ssh"
	CredentialRegistry      = "registry"
	CredentialProviderToken = "provider_token"
	// CredentialGitHubApp holds no secret of its own: it names an
	// installation of the dashboard's GitHub App, and opening it mints that
	// installation's hour-long token. The App service creates and removes
	// these rows; the credential routes only list and delete them.
	CredentialGitHubApp = "github_app"
)

func validCredentialKind(kind string) bool {
	switch kind {
	case CredentialGitBearer, CredentialGitSSH, CredentialRegistry, CredentialProviderToken, CredentialGitHubApp:
		return true
	default:
		return false
	}
}

var (
	// ErrCredentialNotFound names a credential id that no row matches.
	ErrCredentialNotFound = errors.New("deployment credential not found")
	// ErrCredentialInUse marks a delete refused because a non-archived
	// source still resolves to this credential — the same "recreate it from
	// the same form" reasoning the invariants give a saved DNS credential.
	ErrCredentialInUse = errors.New("deployment credential is in use")
	// ErrInvalidCredential covers shape refusals: an unsupported kind, a
	// secret that does not parse for the kind it claims, or a malformed
	// target/name.
	ErrInvalidCredential = errors.New("invalid deployment credential")
	// ErrCredentialNameTaken is its own sentinel rather than a reuse of the
	// project ErrNameTaken: that one's fixed message text ("a project with
	// that name already exists") would wrap into a credential's own error
	// text through %w, reading as two different objects at once.
	ErrCredentialNameTaken = errors.New("a credential with that name already exists")
)

// CredentialSummary is the read model: everything about a saved credential
// except the secret, which no route ever returns.
type CredentialSummary struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Kind       string     `json:"kind"`
	Target     string     `json:"target"`
	Username   string     `json:"username,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	UsedBy     int        `json:"usedBy"`
	// UsedByProjectIDs names the projects behind UsedBy, so the page can draw
	// who a credential serves instead of only counting them. UsedBy counts
	// environments; a project with two on the same credential appears once.
	UsedByProjectIDs []int64 `json:"usedByProjectIds,omitempty"`
}

// CredentialCreateRequest is POST /deploy/credentials's body.
type CredentialCreateRequest struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Target   string `json:"target,omitempty"`
	Username string `json:"username,omitempty"`
	Secret   string `json:"secret"`
}

// CredentialUpdateRequest is PUT /deploy/credentials/{id}'s body. Every field
// is a pointer so an omitted one leaves the stored value untouched — an
// omitted secret keeps the sealed one exactly as the brief asks.
type CredentialUpdateRequest struct {
	Name     *string `json:"name,omitempty"`
	Target   *string `json:"target,omitempty"`
	Username *string `json:"username,omitempty"`
	Secret   *string `json:"secret,omitempty"`
}

// CredentialTestRequest is POST /deploy/credentials/{id}/test's body.
type CredentialTestRequest struct {
	Repository string `json:"repository,omitempty"`
}

// CredentialTestResult never carries the secret; ok/message is all a caller
// needs to decide whether the saved credential still works.
type CredentialTestResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// credentialConfig is config_json's shape for every kind. ServerAddress is
// registryAuth's own existing field name (kept unchanged so a registry
// credential this route writes still decodes the same way it always has);
// Target is the descriptive host label for the git-shaped kinds, which
// nothing else reads today.
type credentialConfig struct {
	ServerAddress string `json:"serverAddress,omitempty"`
	Target        string `json:"target,omitempty"`
	Username      string `json:"username,omitempty"`
	IdentityToken bool   `json:"identityToken,omitempty"`
	LastUsedAt    int64  `json:"lastUsedAt,omitempty"`
	// InstallationID and Account belong to a github_app credential: which
	// installation mints its tokens, and whose repositories those reach.
	InstallationID int64  `json:"installationId,omitempty"`
	Account        string `json:"account,omitempty"`
}

func (c credentialConfig) displayTarget() string {
	if c.ServerAddress != "" {
		return c.ServerAddress
	}
	return c.Target
}

var credentialNameRe = projectNameRe

// validateCredentialFields checks the parts of a credential that do not
// depend on ever seeing the secret again: shape of kind/target/username. A
// rename or a target/username edit re-runs this without needing to unseal
// anything.
func validateCredentialFields(kind, target, username string) error {
	if !validCredentialKind(kind) {
		return fmt.Errorf("%w: unsupported kind %q", ErrInvalidCredential, kind)
	}
	if target != "" && !validRemoteHost(target) {
		return fmt.Errorf("%w: target must be a bare host", ErrInvalidCredential)
	}
	if kind == CredentialRegistry {
		if target == "" {
			return fmt.Errorf("%w: registry credentials require a target host", ErrInvalidCredential)
		}
		if username == "" {
			return fmt.Errorf("%w: registry credentials require a username", ErrInvalidCredential)
		}
	}
	return nil
}

// validateCredentialSecret checks the per-kind shape the brief asks for: a
// PEM key for git_ssh, a bounded token for the bearer kinds, a bounded
// opaque password/token for a registry. Only called when a secret is
// actually being written — an update that keeps the stored secret never
// unseals it to re-check a shape that was already proven valid.
func validateCredentialSecret(kind, secret string) error {
	switch kind {
	case CredentialRegistry:
		if secret == "" || len(secret) > 16<<10 || strings.ContainsAny(secret, "\x00\r\n") {
			return fmt.Errorf("%w: registry secret is empty or malformed", ErrInvalidCredential)
		}
	case CredentialGitSSH:
		if len(secret) > 64<<10 {
			return fmt.Errorf("%w: private key exceeds 64 KiB", ErrInvalidCredential)
		}
		block, _ := pem.Decode([]byte(secret))
		if block == nil || !strings.Contains(strings.ToUpper(block.Type), "PRIVATE KEY") {
			return fmt.Errorf("%w: git_ssh requires a PEM-encoded private key", ErrInvalidCredential)
		}
	case CredentialGitBearer, CredentialProviderToken:
		if !validGitBearerToken(secret) {
			return fmt.Errorf("%w: bearer token is empty, too long or contains unsupported characters", ErrInvalidCredential)
		}
	}
	return nil
}

// credentialExists is the fail-fast check a draft's source step and the
// source-change route run before a credentialId is allowed to be saved,
// so a typo surfaces immediately instead of only at detection time.
func (s *PlanningStore) credentialExists(ctx context.Context, id int64) (bool, error) {
	if id == 0 {
		return true, nil
	}
	var found int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM deploy_credentials WHERE id = ?`, id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

const credentialUsageJoin = `
	  FROM deploy_credentials c
	  LEFT JOIN deploy_sources src ON src.credential_id = c.id
	  LEFT JOIN deploy_environments e ON e.id = src.environment_id AND e.desired_revision = src.revision AND e.archived_at = 0
	  LEFT JOIN deploy_projects p ON p.id = e.project_id AND p.archived_at = 0`

// credentialSummarySelect reads the usage count and the projects behind it
// from the same join, so the two can never describe different sets.
const credentialSummarySelect = `
		SELECT c.id, c.name, c.kind, c.config_json, c.created_at, c.updated_at,
		       COUNT(DISTINCT CASE WHEN p.id IS NOT NULL THEN e.id END),
		       GROUP_CONCAT(DISTINCT p.id)
		` + credentialUsageJoin

func scanCredentialSummary(row interface{ Scan(...any) error }) (*CredentialSummary, error) {
	var summary CredentialSummary
	var config string
	var created, updated int64
	var projects sql.NullString
	if err := row.Scan(&summary.ID, &summary.Name, &summary.Kind, &config, &created, &updated, &summary.UsedBy, &projects); err != nil {
		return nil, err
	}
	summary.CreatedAt, summary.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	applyCredentialConfig(&summary, config)
	if projects.Valid {
		for _, raw := range strings.Split(projects.String, ",") {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return nil, err
			}
			summary.UsedByProjectIDs = append(summary.UsedByProjectIDs, id)
		}
		// GROUP_CONCAT promises no order; sorting keeps two reads of the
		// same state identical.
		slices.Sort(summary.UsedByProjectIDs)
	}
	return &summary, nil
}

func (s *PlanningStore) ListCredentials(ctx context.Context) ([]CredentialSummary, error) {
	rows, err := s.db.QueryContext(ctx, credentialSummarySelect+`
		 GROUP BY c.id ORDER BY c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CredentialSummary{}
	for rows.Next() {
		summary, err := scanCredentialSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *summary)
	}
	return out, rows.Err()
}

func applyCredentialConfig(summary *CredentialSummary, raw string) {
	var config credentialConfig
	if json.Unmarshal([]byte(raw), &config) != nil {
		return
	}
	summary.Target, summary.Username = config.displayTarget(), config.Username
	if config.LastUsedAt > 0 {
		used := time.Unix(config.LastUsedAt, 0).UTC()
		summary.LastUsedAt = &used
	}
}

func (s *PlanningStore) GetCredential(ctx context.Context, id int64) (*CredentialSummary, error) {
	summary, err := scanCredentialSummary(s.db.QueryRowContext(ctx, credentialSummarySelect+`
		 WHERE c.id = ? GROUP BY c.id`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCredentialNotFound
	}
	if err != nil {
		return nil, err
	}
	return summary, nil
}

func (s *PlanningStore) CreateCredential(ctx context.Context, request CredentialCreateRequest) (*CredentialSummary, error) {
	if request.Kind == CredentialGitHubApp {
		return nil, fmt.Errorf("%w: a GitHub App credential is created by installing the App, not by pasting a secret", ErrInvalidCredential)
	}
	name := strings.TrimSpace(request.Name)
	if !credentialNameRe.MatchString(name) {
		return nil, fmt.Errorf("%w: name must start with a letter or digit and contain only letters, digits, dots, dashes and underscores", ErrInvalidCredential)
	}
	target, username := strings.TrimSpace(request.Target), strings.TrimSpace(request.Username)
	if err := validateCredentialFields(request.Kind, target, username); err != nil {
		return nil, err
	}
	if err := validateCredentialSecret(request.Kind, request.Secret); err != nil {
		return nil, err
	}
	sealed, err := s.sealer.Seal(request.Secret)
	if err != nil {
		return nil, err
	}
	config := credentialConfig{Target: target, Username: username}
	if request.Kind == CredentialRegistry {
		config.ServerAddress, config.Target = target, ""
	}
	configJSON, _ := json.Marshal(config)
	now := s.now().UTC().Unix()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO deploy_credentials(name, kind, config_json, secret_enc, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?)`, name, request.Kind, string(configJSON), sealed, now, now)
	if err != nil {
		return nil, credentialWriteError(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetCredential(ctx, id)
}

func (s *PlanningStore) UpdateCredential(ctx context.Context, id int64, request CredentialUpdateRequest) (*CredentialSummary, error) {
	var name, kind, configJSON string
	if err := s.db.QueryRowContext(ctx, `SELECT name, kind, config_json FROM deploy_credentials WHERE id = ?`, id).
		Scan(&name, &kind, &configJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrCredentialNotFound
		}
		return nil, err
	}
	var config credentialConfig
	if json.Unmarshal([]byte(configJSON), &config) != nil {
		config = credentialConfig{}
	}
	if kind == CredentialGitHubApp {
		return nil, fmt.Errorf("%w: a GitHub App credential follows its installation and cannot be edited", ErrInvalidCredential)
	}
	if request.Name != nil {
		name = strings.TrimSpace(*request.Name)
	}
	target := config.displayTarget()
	if request.Target != nil {
		target = strings.TrimSpace(*request.Target)
	}
	username := config.Username
	if request.Username != nil {
		username = strings.TrimSpace(*request.Username)
	}
	if !credentialNameRe.MatchString(name) {
		return nil, fmt.Errorf("%w: name must start with a letter or digit and contain only letters, digits, dots, dashes and underscores", ErrInvalidCredential)
	}
	if err := validateCredentialFields(kind, target, username); err != nil {
		return nil, err
	}
	secret := ""
	if request.Secret != nil {
		secret = *request.Secret
		if err := validateCredentialSecret(kind, secret); err != nil {
			return nil, err
		}
	}
	config.Username = username
	if kind == CredentialRegistry {
		config.ServerAddress, config.Target = target, ""
	} else {
		config.Target = target
	}
	configBytes, _ := json.Marshal(config)
	now := s.now().UTC().Unix()
	if request.Secret != nil {
		sealed, err := s.sealer.Seal(secret)
		if err != nil {
			return nil, err
		}
		_, err = s.db.ExecContext(ctx, `
			UPDATE deploy_credentials SET name = ?, config_json = ?, secret_enc = ?, updated_at = ? WHERE id = ?`,
			name, string(configBytes), sealed, now, id)
		if err != nil {
			return nil, credentialWriteError(err)
		}
	} else {
		_, err := s.db.ExecContext(ctx, `
			UPDATE deploy_credentials SET name = ?, config_json = ?, updated_at = ? WHERE id = ?`,
			name, string(configBytes), now, id)
		if err != nil {
			return nil, credentialWriteError(err)
		}
	}
	return s.GetCredential(ctx, id)
}

func credentialWriteError(err error) error {
	if strings.Contains(strings.ToLower(err.Error()), "unique") {
		return ErrCredentialNameTaken
	}
	return err
}

// DeleteCredential refuses while any non-archived project's current source
// still resolves to this credential, mirroring the invariants' treatment of
// a saved DNS-provider credential: routine, recoverable (paste it again),
// still gated by s.destructive rather than a typed phrase.
func (s *PlanningStore) DeleteCredential(ctx context.Context, id int64) error {
	var inUse bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1`+credentialUsageJoin+` WHERE c.id = ? AND p.id IS NOT NULL)`, id).Scan(&inUse)
	if err != nil {
		return err
	}
	if inUse {
		return ErrCredentialInUse
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM deploy_credentials WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrCredentialNotFound
	}
	return nil
}

// recordCredentialUsed is a best-effort usage timestamp: a source adapter
// calls it after successfully opening a credential's material, and a write
// failure here must never fail the deployment work that already has the
// secret in hand.
func (s *PlanningStore) recordCredentialUsed(ctx context.Context, id int64) {
	_, _ = s.db.ExecContext(ctx,
		`UPDATE deploy_credentials SET config_json = json_set(COALESCE(config_json, '{}'), '$.lastUsedAt', ?) WHERE id = ?`,
		s.now().UTC().Unix(), id)
}
