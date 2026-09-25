package deploy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	basestore "github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

var (
	ErrTriggerNotFound       = errors.New("deployment trigger not found")
	ErrHookDisabled          = errors.New("deployment hook is disabled")
	ErrBadHookSignature      = errors.New("deployment hook signature is invalid")
	ErrWrongEvent            = errors.New("deployment provider event is not supported")
	ErrWrongRepository       = errors.New("deployment provider repository does not match")
	ErrWrongRef              = errors.New("deployment provider ref does not match")
	ErrDeliveryReplayed      = errors.New("deployment provider delivery was already received")
	ErrWatchPathsIgnored     = errors.New("deployment change does not match watched paths")
	ErrPreviewQuota          = errors.New("deployment preview quota has been reached")
	ErrPreviewApproval       = errors.New("this pull request revision needs administrator approval before it can build or run")
	ErrPreviewIsolation      = errors.New("preview configuration does not satisfy storage and execution isolation")
	ErrPreviewCleanupPending = errors.New("the previous preview must finish cleanup before this pull request can reopen; retry approval after cleanup completes")
)

const MaxAutomationBody = 4 << 20

type AutomationStore struct {
	db     *sql.DB
	sealer *auth.Sealer
	now    func() time.Time
	mailer Mailer
}

func NewAutomationStore(st *basestore.Store, sealer *auth.Sealer) *AutomationStore {
	return &AutomationStore{db: st.DB, sealer: sealer, now: time.Now}
}

type Trigger struct {
	ID             int64         `json:"id"`
	EnvironmentID  int64         `json:"environmentId"`
	ProjectID      int64         `json:"projectId"`
	Name           string        `json:"name"`
	Kind           TriggerKind   `json:"kind"`
	Provider       string        `json:"provider,omitempty"`
	Config         TriggerConfig `json:"config"`
	HookID         string        `json:"hookId,omitempty"`
	Enabled        bool          `json:"enabled"`
	LastDeliveryAt *time.Time    `json:"lastDeliveryAt,omitempty"`
	LastStatus     string        `json:"lastStatus,omitempty"`
	CreatedAt      time.Time     `json:"createdAt"`
	UpdatedAt      time.Time     `json:"updatedAt"`
	// LastDelivery and Recent are the trigger's delivery log as its list row
	// draws it. Only AttachTriggerDeliveries fills them, because the log is an
	// administrator's reading and the list is not.
	LastDelivery *TriggerDelivery `json:"lastDelivery,omitempty"`
	Recent       []TriggerOutcome `json:"recent,omitempty"`
}

// TriggerOutcome is one square of a trigger's recent-delivery strip: what this
// host decided about a delivery, and why when it did not deploy.
type TriggerOutcome struct {
	Decision   string    `json:"decision"`
	Reason     string    `json:"reason,omitempty"`
	ReceivedAt time.Time `json:"receivedAt"`
}

// triggerRecentOutcomes is how many deliveries a trigger's strip draws.
const triggerRecentOutcomes = 14

type TriggerConfig struct {
	Repository    string   `json:"repository,omitempty"`
	Ref           string   `json:"ref,omitempty"`
	Events        []string `json:"events,omitempty"`
	WatchInclude  []string `json:"watchInclude,omitempty"`
	WatchExclude  []string `json:"watchExclude,omitempty"`
	Preview       bool     `json:"preview,omitempty"`
	PreviewQuota  int      `json:"previewQuota,omitempty"`
	PreviewDomain string   `json:"previewDomain,omitempty"`
	// Delivery is how a GitHub trigger's events arrive: through its own
	// per-trigger hook and secret, or, as DeliveryApp, through the
	// dashboard's GitHub App, which needs nothing configured on GitHub.
	Delivery string `json:"delivery,omitempty"`
}

// DeliveryApp marks a trigger fed by the GitHub App's single webhook.
const DeliveryApp = "app"

type TriggerWrite struct {
	Name     string        `json:"name"`
	Kind     TriggerKind   `json:"kind"`
	Provider string        `json:"provider,omitempty"`
	Config   TriggerConfig `json:"config"`
	Enabled  bool          `json:"enabled"`
}

type TriggerCreated struct {
	Trigger Trigger `json:"trigger"`
	Secret  string  `json:"secret"`
}

func (s *AutomationStore) ListTriggers(ctx context.Context, projectID, environmentID int64) ([]Trigger, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.id,t.environment_id,e.project_id,t.name,t.kind,t.provider,t.config_json,t.hook_id,t.enabled,t.last_delivery_at,t.last_status,t.created_at,t.updated_at FROM deploy_triggers t JOIN deploy_environments e ON e.id=t.environment_id WHERE e.project_id=? AND t.environment_id=? ORDER BY t.id`, projectID, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Trigger{}
	for rows.Next() {
		v, err := scanTrigger(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

func scanTrigger(row interface{ Scan(...any) error }) (*Trigger, error) {
	var t Trigger
	var cfg string
	var enabled int
	var last, created, updated int64
	if err := row.Scan(&t.ID, &t.EnvironmentID, &t.ProjectID, &t.Name, &t.Kind, &t.Provider, &cfg, &t.HookID, &enabled, &last, &t.LastStatus, &created, &updated); err != nil {
		return nil, err
	}
	if json.Unmarshal([]byte(cfg), &t.Config) != nil {
		return nil, fmt.Errorf("%w: malformed trigger configuration", ErrInvalidPlan)
	}
	t.Enabled = enabled != 0
	t.CreatedAt = time.Unix(created, 0).UTC()
	t.UpdatedAt = time.Unix(updated, 0).UTC()
	if last > 0 {
		value := time.Unix(last, 0).UTC()
		t.LastDeliveryAt = &value
	}
	return &t, nil
}

// TriggersForAppDelivery finds every enabled GitHub trigger that asked the App
// to deliver for a repository. One push can legitimately reach several: a
// production trigger on main and a preview trigger for pull requests.
func (s *AutomationStore) TriggersForAppDelivery(ctx context.Context, repository string) ([]Trigger, error) {
	repository = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(repository), ".git"))
	if repository == "" {
		return []Trigger{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT t.id,t.environment_id,e.project_id,t.name,t.kind,t.provider,t.config_json,t.hook_id,t.enabled,t.last_delivery_at,t.last_status,t.created_at,t.updated_at FROM deploy_triggers t JOIN deploy_environments e ON e.id=t.environment_id JOIN deploy_projects p ON p.id=e.project_id WHERE t.provider='github' AND t.enabled=1 AND e.archived_at=0 AND p.archived_at=0 ORDER BY t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Trigger{}
	for rows.Next() {
		t, err := scanTrigger(rows)
		if err != nil {
			return nil, err
		}
		if t.Config.Delivery == DeliveryApp && strings.ToLower(strings.TrimSuffix(t.Config.Repository, ".git")) == repository {
			out = append(out, *t)
		}
	}
	return out, rows.Err()
}

func (s *AutomationStore) TriggerByHook(ctx context.Context, hookID string) (*Trigger, string, error) {
	return s.triggerWithSecret(ctx, `t.hook_id=?`, hookID)
}

func (s *AutomationStore) TriggerByID(ctx context.Context, projectID, triggerID int64) (*Trigger, string, error) {
	return s.triggerWithSecret(ctx, `t.id=? AND e.project_id=?`, triggerID, projectID)
}

func (s *AutomationStore) triggerWithSecret(ctx context.Context, predicate string, args ...any) (*Trigger, string, error) {
	var sealed, cfg string
	var enabled int
	var last, created, updated int64
	var t Trigger
	err := s.db.QueryRowContext(ctx, `SELECT t.id,t.environment_id,e.project_id,t.name,t.kind,t.provider,t.config_json,t.hook_id,t.enabled,t.last_delivery_at,t.last_status,t.created_at,t.updated_at,t.secret_enc FROM deploy_triggers t JOIN deploy_environments e ON e.id=t.environment_id WHERE `+predicate+` AND e.archived_at=0`, args...).Scan(&t.ID, &t.EnvironmentID, &t.ProjectID, &t.Name, &t.Kind, &t.Provider, &cfg, &t.HookID, &enabled, &last, &t.LastStatus, &created, &updated, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrTriggerNotFound
	}
	if err != nil {
		return nil, "", err
	}
	if json.Unmarshal([]byte(cfg), &t.Config) != nil {
		return nil, "", fmt.Errorf("%w: malformed trigger configuration", ErrInvalidPlan)
	}
	t.Enabled = enabled != 0
	t.CreatedAt, t.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	if last > 0 {
		value := time.Unix(last, 0).UTC()
		t.LastDeliveryAt = &value
	}
	secret, err := s.sealer.Open(sealed)
	if err != nil {
		return nil, "", err
	}
	return &t, secret, nil
}

func randomHex(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *AutomationStore) CreateTrigger(ctx context.Context, projectID, environmentID int64, in TriggerWrite) (*TriggerCreated, error) {
	if err := validateTriggerWrite(&in); err != nil {
		return nil, err
	}
	cfg, _ := json.Marshal(in.Config)
	secret, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	sealed, err := s.sealer.Seal(secret)
	if err != nil {
		return nil, err
	}
	hook, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO deploy_triggers(environment_id,name,kind,provider,config_json,secret_enc,hook_id,enabled,created_at,updated_at) SELECT id,?,?,?,?,?,?,?,?,? FROM deploy_environments WHERE id=? AND project_id=? AND archived_at=0`, in.Name, in.Kind, in.Provider, string(cfg), sealed, hook, boolInt(in.Enabled), now, now, environmentID, projectID)
	if err != nil {
		return nil, err
	}
	if changed, _ := res.RowsAffected(); changed != 1 {
		return nil, ErrEnvironmentNotFound
	}
	if err := syncTriggerGitPolicyTx(ctx, tx, environmentID, in.Config, now); err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	t, err := s.triggerByID(ctx, id, projectID)
	if err != nil {
		return nil, err
	}
	return &TriggerCreated{Trigger: *t, Secret: secret}, nil
}
func (s *AutomationStore) UpdateTrigger(ctx context.Context, projectID, environmentID, triggerID int64, in TriggerWrite) (*Trigger, error) {
	if err := validateTriggerWrite(&in); err != nil {
		return nil, err
	}
	cfg, _ := json.Marshal(in.Config)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now().UTC().Unix()
	result, err := tx.ExecContext(ctx, `UPDATE deploy_triggers SET name=?,kind=?,provider=?,config_json=?,enabled=?,updated_at=? WHERE id=? AND environment_id=? AND environment_id IN (SELECT id FROM deploy_environments WHERE project_id=?)`, in.Name, in.Kind, in.Provider, string(cfg), boolInt(in.Enabled), now, triggerID, environmentID, projectID)
	if err != nil {
		return nil, err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return nil, ErrTriggerNotFound
	}
	if err := syncTriggerGitPolicyTx(ctx, tx, environmentID, in.Config, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.triggerByID(ctx, triggerID, projectID)
}

// RotateTriggerSecret issues a new HMAC secret for a trigger, invalidating
// the old one, without disturbing the trigger's configuration or hook id.
func (s *AutomationStore) RotateTriggerSecret(ctx context.Context, projectID, environmentID, triggerID int64) (string, error) {
	secret, err := randomHex(32)
	if err != nil {
		return "", err
	}
	sealed, err := s.sealer.Seal(secret)
	if err != nil {
		return "", err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE deploy_triggers SET secret_enc=?,updated_at=?
		 WHERE id=? AND environment_id=? AND environment_id IN (SELECT id FROM deploy_environments WHERE project_id=?)`,
		sealed, s.now().UTC().Unix(), triggerID, environmentID, projectID)
	if err != nil {
		return "", err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return "", ErrTriggerNotFound
	}
	return secret, nil
}

// TriggerDelivery is one row of a trigger's webhook delivery log: enough to
// tell an operator what arrived and what this host decided to do with it,
// without repeating the raw payload.
type TriggerDelivery struct {
	DeliveryID string    `json:"deliveryId"`
	Event      string    `json:"event"`
	Ref        string    `json:"ref,omitempty"`
	Decision   string    `json:"decision"`
	Reason     string    `json:"reason,omitempty"`
	RunID      int64     `json:"runId,omitempty"`
	ReceivedAt time.Time `json:"receivedAt"`
}

// TriggerDeliveries returns the last limit deliveries recorded for a trigger,
// newest first.
func (s *AutomationStore) TriggerDeliveries(ctx context.Context, projectID, environmentID, triggerID int64, limit int) ([]TriggerDelivery, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	var exists int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM deploy_triggers t JOIN deploy_environments e ON e.id=t.environment_id
		 WHERE t.id=? AND t.environment_id=? AND e.project_id=?`, triggerID, environmentID, projectID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTriggerNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT delivery_id,event,ref,status,reason,run_id,received_at
		  FROM deploy_webhook_deliveries WHERE trigger_id=? ORDER BY id DESC LIMIT ?`, triggerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TriggerDelivery{}
	for rows.Next() {
		var d TriggerDelivery
		var received int64
		if err := rows.Scan(&d.DeliveryID, &d.Event, &d.Ref, &d.Decision, &d.Reason, &d.RunID, &received); err != nil {
			return nil, err
		}
		d.ReceivedAt = time.Unix(received, 0).UTC()
		out = append(out, d)
	}
	return out, rows.Err()
}

// AttachTriggerDeliveries gives each trigger its newest delivery and its last
// triggerRecentOutcomes decisions, oldest first, read for every trigger in
// one statement rather than one delivery request per row.
func (s *AutomationStore) AttachTriggerDeliveries(ctx context.Context, triggers []Trigger) error {
	if len(triggers) == 0 {
		return nil
	}
	byID := make(map[int64]*Trigger, len(triggers))
	ids := make([]int64, 0, len(triggers))
	for i := range triggers {
		byID[triggers[i].ID] = &triggers[i]
		ids = append(ids, triggers[i].ID)
	}
	placeholders, args := inPlaceholders(ids)
	rows, err := s.db.QueryContext(ctx, `
		SELECT trigger_id,delivery_id,event,ref,status,reason,run_id,received_at FROM (
		  SELECT id,trigger_id,delivery_id,event,ref,status,reason,run_id,received_at,
		         ROW_NUMBER() OVER (PARTITION BY trigger_id ORDER BY id DESC) AS rank
		    FROM deploy_webhook_deliveries WHERE trigger_id IN `+placeholders+`
		) WHERE rank<=? ORDER BY trigger_id,id`, append(args, triggerRecentOutcomes)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var triggerID, received int64
		var d TriggerDelivery
		if err := rows.Scan(&triggerID, &d.DeliveryID, &d.Event, &d.Ref, &d.Decision, &d.Reason, &d.RunID, &received); err != nil {
			return err
		}
		d.ReceivedAt = time.Unix(received, 0).UTC()
		trigger := byID[triggerID]
		trigger.Recent = append(trigger.Recent, TriggerOutcome{Decision: d.Decision, Reason: d.Reason, ReceivedAt: d.ReceivedAt})
		trigger.LastDelivery = &d
	}
	return rows.Err()
}

func (s *AutomationStore) DeleteTrigger(ctx context.Context, projectID, environmentID, triggerID int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM deploy_triggers WHERE id=? AND environment_id=? AND environment_id IN (SELECT id FROM deploy_environments WHERE project_id=?)`, triggerID, environmentID, projectID)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrTriggerNotFound
	}
	return nil
}

func validateTriggerWrite(in *TriggerWrite) error {
	if _, err := canonicalWatchPatterns(in.Config.WatchInclude); err != nil {
		return err
	}
	if _, err := canonicalWatchPatterns(in.Config.WatchExclude); err != nil {
		return err
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Provider = strings.ToLower(strings.TrimSpace(in.Provider))
	in.Config.Repository = strings.TrimSpace(in.Config.Repository)
	in.Config.Ref = strings.TrimSpace(in.Config.Ref)
	if in.Name == "" || len(in.Name) > 100 {
		return fmt.Errorf("trigger name is required")
	}
	if in.Config.PreviewQuota < 0 || in.Config.PreviewQuota > 20 {
		return fmt.Errorf("preview quota must be between 0 and 20")
	}
	if domain := strings.TrimSpace(in.Config.PreviewDomain); domain != "" {
		if strings.Count(domain, "{number}") != 1 || len(domain) > 240 || !strings.Contains(domain, ".") || strings.ContainsAny(strings.ReplaceAll(domain, "{number}", "1"), " {}:/\\\x00\r\n") {
			return fmt.Errorf("preview domain must be a hostname containing one {number} placeholder")
		}
		in.Config.PreviewDomain = strings.ToLower(domain)
	}
	valid := map[TriggerKind]bool{TriggerGenericHook: true, TriggerAPI: true, TriggerGitHub: true, TriggerGitLab: true, TriggerBitbucket: true, TriggerGitea: true}
	if !valid[in.Kind] {
		return fmt.Errorf("unsupported trigger kind %q", in.Kind)
	}
	in.Config.Delivery = strings.ToLower(strings.TrimSpace(in.Config.Delivery))
	switch in.Config.Delivery {
	case "":
	case DeliveryApp:
		if in.Kind != TriggerGitHub || in.Config.Repository == "" {
			return fmt.Errorf("GitHub App delivery needs a GitHub trigger with a repository")
		}
	default:
		return fmt.Errorf("unsupported delivery %q", in.Config.Delivery)
	}
	if in.Kind == TriggerGitHub || in.Kind == TriggerGitLab || in.Kind == TriggerBitbucket || in.Kind == TriggerGitea {
		if in.Provider == "" {
			in.Provider = string(in.Kind)
		}
		if in.Provider != string(in.Kind) {
			return fmt.Errorf("provider and trigger kind disagree")
		}
		if in.Config.Repository == "" || in.Config.Ref == "" {
			return fmt.Errorf("provider triggers require repository and ref")
		}
	}
	return nil
}

func (s *AutomationStore) triggerByID(ctx context.Context, id, projectID int64) (*Trigger, error) {
	t, err := scanTrigger(s.db.QueryRowContext(ctx, `SELECT t.id,t.environment_id,e.project_id,t.name,t.kind,t.provider,t.config_json,t.hook_id,t.enabled,t.last_delivery_at,t.last_status,t.created_at,t.updated_at FROM deploy_triggers t JOIN deploy_environments e ON e.id=t.environment_id WHERE t.id=? AND e.project_id=?`, id, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTriggerNotFound
	}
	return t, err
}

type ProviderEvent struct {
	DeliveryID     string
	Event          string
	Repository     string
	Ref            string
	Action         string
	Revision       string
	ChangedPaths   []string
	PreviewRef     string
	PreviewNumber  int
	PreviewClosed  bool
	Author         string
	HeadRepository string
	// HeadRef is the branch a pull request proposes. PreviewRef is the ref the
	// build fetches, which on GitHub and GitLab is the provider's own
	// refs/pull/N/head rather than a name anyone chose.
	HeadRef string
	// BaseRef is the branch the pull request wants to land on, and Merged
	// says whether a close delivery closed it by merging: that is what turns
	// a preview teardown into a production redeploy.
	BaseRef string
	Merged  bool
}

func VerifyProvider(provider string, headers http.Header, body []byte, secret string) (ProviderEvent, error) {
	provider = strings.ToLower(provider)
	if len(body) > MaxAutomationBody {
		return ProviderEvent{}, fmt.Errorf("payload too large")
	}
	var signature, delivery, event string
	switch provider {
	case "github":
		signature = headers.Get("X-Hub-Signature-256")
		delivery = headers.Get("X-GitHub-Delivery")
		event = headers.Get("X-GitHub-Event")
	case "gitlab":
		signature = headers.Get("X-Gitlab-Token")
		delivery = headers.Get("X-Gitlab-Event-UUID")
		event = headers.Get("X-Gitlab-Event")
	case "bitbucket":
		signature = headers.Get("X-Hub-Signature")
		delivery = headers.Get("X-Request-UUID")
		event = headers.Get("X-Event-Key")
	case "gitea":
		signature = headers.Get("X-Gitea-Signature")
		delivery = headers.Get("X-Gitea-Delivery")
		event = headers.Get("X-Gitea-Event")
	default:
		return ProviderEvent{}, ErrWrongEvent
	}
	if provider == "gitlab" {
		if !hmac.Equal([]byte(signature), []byte(secret)) {
			return ProviderEvent{}, ErrBadHookSignature
		}
	} else if !verifyHMAC(body, secret, signature) {
		return ProviderEvent{}, ErrBadHookSignature
	}
	if strings.TrimSpace(delivery) == "" || len(delivery) > 256 {
		return ProviderEvent{}, ErrWrongEvent
	}
	var raw map[string]any
	if json.Unmarshal(body, &raw) != nil {
		return ProviderEvent{}, ErrWrongEvent
	}
	result := ProviderEvent{DeliveryID: delivery, Event: event}
	if !validProviderEvent(provider, event) {
		return ProviderEvent{}, ErrWrongEvent
	}
	if provider == "github" || provider == "gitea" {
		result.Repository = nestedString(raw, "repository", "full_name")
		result.Ref = stringValue(raw["ref"])
		result.Revision = stringValue(raw["after"])
		result.Action = stringValue(raw["action"])
		result.ChangedPaths = githubChanged(raw)
		if pr, ok := raw["pull_request"].(map[string]any); ok {
			result.PreviewNumber = intValue(raw["number"])
			result.HeadRef = nestedString(pr, "head", "ref")
			result.Revision = nestedString(pr, "head", "sha")
			result.PreviewClosed = result.Action == "closed"
			result.Merged = boolValue(pr["merged"])
			result.BaseRef = nestedString(pr, "base", "ref")
			result.Author = nestedString(pr, "user", "login")
			result.HeadRepository = nestedString(pr, "head", "repo", "full_name")
			result.PreviewRef = fmt.Sprintf("refs/pull/%d/head", result.PreviewNumber)
		}

	}
	if provider == "gitlab" {
		result.Repository = nestedString(raw, "project", "path_with_namespace")
		result.Ref = stringValue(raw["ref"])
		result.Revision = stringValue(raw["checkout_sha"])
		result.Action = stringValue(raw["object_kind"])
		if attrs, ok := raw["object_attributes"].(map[string]any); ok {
			result.PreviewNumber = intValue(attrs["iid"])
			result.HeadRef = stringValue(attrs["source_branch"])
			result.Revision = nestedString(attrs, "last_commit", "id")
			state := stringValue(attrs["state"])
			result.PreviewClosed = state == "closed" || state == "merged"
			result.Merged = state == "merged"
			result.BaseRef = stringValue(attrs["target_branch"])
			result.Author = nestedString(raw, "user", "username")

			result.HeadRepository = nestedString(attrs, "source", "path_with_namespace")
			result.PreviewRef = fmt.Sprintf("refs/merge-requests/%d/head", result.PreviewNumber)
		}
	}
	if provider == "bitbucket" {
		result.Repository = nestedString(raw, "repository", "full_name")
		if push, ok := raw["push"].(map[string]any); ok {
			if changes, ok := push["changes"].([]any); ok && len(changes) > 0 {
				if c, ok := changes[0].(map[string]any); ok {
					result.Ref = nestedString(c, "new", "name")
					result.Revision = nestedString(c, "new", "target", "hash")
				}
			}
		}
		if pr, ok := raw["pullrequest"].(map[string]any); ok {
			result.PreviewNumber = intValue(pr["id"])
			result.HeadRef = nestedString(pr, "source", "branch", "name")
			result.PreviewRef = result.HeadRef
			result.Revision = nestedString(pr, "source", "commit", "hash")
			result.PreviewClosed = strings.Contains(event, "fulfilled") || strings.Contains(event, "rejected")
			result.Merged = strings.Contains(event, "fulfilled")
			result.BaseRef = nestedString(pr, "destination", "branch", "name")
			result.Author = nestedString(pr, "author", "nickname")

			result.HeadRepository = nestedString(pr, "source", "repository", "full_name")
		}
	}
	if event == "pull_request" || event == "Merge Request Hook" || strings.HasPrefix(event, "pullrequest:") {
		if result.PreviewNumber <= 0 || (!result.PreviewClosed && !gitObjectIDRE.MatchString(result.Revision)) {
			return ProviderEvent{}, ErrWrongEvent
		}
	}
	return result, nil
}

func validProviderEvent(provider, event string) bool {
	allowed := map[string]map[string]bool{"github": {"push": true, "pull_request": true}, "gitea": {"push": true, "pull_request": true}, "gitlab": {"Push Hook": true, "Merge Request Hook": true}, "bitbucket": {"repo:push": true, "pullrequest:created": true, "pullrequest:updated": true, "pullrequest:fulfilled": true, "pullrequest:rejected": true}}
	return allowed[provider][event]
}

func verifyHMAC(body []byte, secret, signature string) bool {
	signature = strings.TrimPrefix(signature, "sha256=")
	want := hmac.New(sha256.New, []byte(secret))
	want.Write(body)
	got, err := hex.DecodeString(signature)
	return err == nil && hmac.Equal(got, want.Sum(nil))
}
func VerifyGenericHook(body []byte, secret, signature string) bool {
	return verifyHMAC(body, secret, signature)
}
func stringValue(v any) string { s, _ := v.(string); return s }
func boolValue(v any) bool     { b, _ := v.(bool); return b }

func intValue(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(x)
		return n
	}
	return 0
}
func nestedString(v any, keys ...string) string {
	cur := v
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[k]
	}
	return stringValue(cur)
}
func githubChanged(raw map[string]any) []string {
	var out []string
	for _, name := range []string{"commits"} {
		if items, ok := raw[name].([]any); ok {
			for _, item := range items {
				if m, ok := item.(map[string]any); ok {
					for _, field := range []string{"added", "modified", "removed"} {
						if paths, ok := m[field].([]any); ok {
							for _, p := range paths {
								if s := stringValue(p); s != "" {
									out = append(out, s)
								}
							}
						}
					}
				}
			}
		}
	}
	sort.Strings(out)
	return uniqueStrings(out)
}
func uniqueStrings(in []string) []string {
	out := in[:0]
	var prior string
	for i, v := range in {
		if i == 0 || v != prior {
			out = append(out, v)
			prior = v
		}
	}
	return out
}

func MatchWatchPaths(changed, include, exclude []string) bool {
	if len(changed) == 0 {
		return len(include) == 0
	}
	for _, file := range changed {
		file = strings.TrimPrefix(path.Clean("/"+file), "/")
		included := len(include) == 0
		for _, pattern := range include {
			if globMatch(pattern, file) {
				included = true
				break
			}
		}
		if !included {
			continue
		}
		blocked := false
		for _, pattern := range exclude {
			if globMatch(pattern, file) {
				blocked = true
				break
			}
		}
		if !blocked {
			return true
		}
	}
	return false
}
func globMatch(pattern, name string) bool {
	pattern = strings.TrimPrefix(path.Clean("/"+pattern), "/")
	if strings.HasSuffix(pattern, "/**") {
		base := strings.TrimSuffix(pattern, "/**")
		return name == base || strings.HasPrefix(name, base+"/")
	}
	ok, _ := path.Match(pattern, name)
	return ok
}

func (s *AutomationStore) RecordDelivery(ctx context.Context, t *Trigger, event ProviderEvent, body []byte, status, reason string, runID int64) error {
	digest := sha256.Sum256(body)
	_, err := s.db.ExecContext(ctx, `INSERT INTO deploy_webhook_deliveries(trigger_id,delivery_id,event,repository,ref,body_digest,status,reason,run_id,received_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, t.ID, event.DeliveryID, event.Event, event.Repository, event.Ref, hex.EncodeToString(digest[:]), status, reason, runID, s.now().UTC().Unix())
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		return ErrDeliveryReplayed
	}
	if err == nil {
		_, err = s.db.ExecContext(ctx, `UPDATE deploy_triggers SET last_delivery_at=?,last_status=?,updated_at=? WHERE id=?`, s.now().UTC().Unix(), status, s.now().UTC().Unix(), t.ID)
	}
	return err
}

func (s *AutomationStore) FinishDelivery(ctx context.Context, t *Trigger, deliveryID, status, reason string, runID int64) error {
	now := s.now().UTC().Unix()
	result, err := s.db.ExecContext(ctx, `UPDATE deploy_webhook_deliveries SET status=?,reason=?,run_id=? WHERE trigger_id=? AND delivery_id=?`, status, reason, runID, t.ID, deliveryID)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrTriggerNotFound
	}
	_, err = s.db.ExecContext(ctx, `UPDATE deploy_triggers SET last_delivery_at=?,last_status=?,updated_at=? WHERE id=?`, now, status, now, t.ID)
	return err
}

type Schedule struct {
	ID            int64          `json:"id"`
	EnvironmentID int64          `json:"environmentId"`
	ProjectID     int64          `json:"projectId"`
	Name          string         `json:"name"`
	Expression    string         `json:"expression"`
	Timezone      string         `json:"timezone"`
	Enabled       bool           `json:"enabled"`
	NextRunAt     *time.Time     `json:"nextRunAt,omitempty"`
	Steps         []ScheduleStep `json:"steps"`
	CreatedAt     time.Time      `json:"createdAt"`
	UpdatedAt     time.Time      `json:"updatedAt"`
	// NextRuns is the next ScheduleNextRuns firings, starting with NextRunAt,
	// walked in the schedule's own time zone so a daylight-saving change
	// lands where the dispatcher will put it. A paused schedule has none.
	NextRuns []time.Time `json:"nextRuns,omitempty"`
}

// ScheduleNextRuns is how many upcoming firings a schedule reports.
const ScheduleNextRuns = 5

type ScheduleStep struct {
	Action   string          `json:"action"`
	Config   json.RawMessage `json:"config"`
	Required bool            `json:"required"`
}

type ScheduleWrite struct {
	Name       string         `json:"name"`
	Expression string         `json:"expression"`
	Timezone   string         `json:"timezone"`
	Enabled    bool           `json:"enabled"`
	Steps      []ScheduleStep `json:"steps"`
}

func (s *AutomationStore) ListSchedules(ctx context.Context, projectID, environmentID int64) ([]Schedule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.id,s.environment_id,e.project_id,s.name,s.expression,s.timezone,s.enabled,s.next_run_at,s.created_at,s.updated_at FROM deploy_schedules s JOIN deploy_environments e ON e.id=s.environment_id WHERE e.project_id=? AND s.environment_id=? ORDER BY s.id`, projectID, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		var v Schedule
		var enabled int
		var next, created, updated int64
		if err := rows.Scan(&v.ID, &v.EnvironmentID, &v.ProjectID, &v.Name, &v.Expression, &v.Timezone, &enabled, &next, &created, &updated); err != nil {
			return nil, err
		}
		v.Enabled = enabled != 0
		v.CreatedAt = time.Unix(created, 0).UTC()
		v.UpdatedAt = time.Unix(updated, 0).UTC()
		if next > 0 {
			x := time.Unix(next, 0).UTC()
			v.NextRunAt = &x
			if v.Enabled {
				// The stored firing is the dispatcher's own answer and leads;
				// an expression that no longer walks (a zone gone from the
				// host's tzdata) is left to the dispatcher to disable.
				rest, _ := NextCronRuns(v.Expression, v.Timezone, x, ScheduleNextRuns-1)
				v.NextRuns = append([]time.Time{x}, rest...)
			}
		}
		steps, err := s.scheduleSteps(ctx, v.ID)
		if err != nil {
			return nil, err
		}
		v.Steps = steps
		out = append(out, v)
	}
	return out, rows.Err()
}

type ScheduleDispatch struct {
	Schedule Schedule
	DueAt    time.Time
}

func (s *AutomationStore) ClaimDueSchedules(ctx context.Context, limit int) ([]ScheduleDispatch, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	now := s.now().UTC()
	// The project join is a defensive backstop, not the primary guard: Archive
	// disables a project's schedules directly. It only matters for a schedule
	// that was already enabled on a project archived before that fix shipped.
	rows, err := s.db.QueryContext(ctx, `SELECT s.id,s.environment_id,e.project_id,s.name,s.expression,s.timezone,s.enabled,s.next_run_at,s.created_at,s.updated_at FROM deploy_schedules s JOIN deploy_environments e ON e.id=s.environment_id JOIN deploy_projects p ON p.id=e.project_id WHERE s.enabled=1 AND s.next_run_at>0 AND s.next_run_at<=? AND e.archived_at=0 AND p.archived_at=0 ORDER BY s.next_run_at LIMIT ?`, now.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScheduleDispatch{}
	for rows.Next() {
		var v Schedule
		var enabled int
		var next, created, updated int64
		if err := rows.Scan(&v.ID, &v.EnvironmentID, &v.ProjectID, &v.Name, &v.Expression, &v.Timezone, &enabled, &next, &created, &updated); err != nil {
			return nil, err
		}
		v.Enabled = true
		v.CreatedAt = time.Unix(created, 0).UTC()
		v.UpdatedAt = time.Unix(updated, 0).UTC()
		due := time.Unix(next, 0).UTC()
		v.NextRunAt = &due
		v.Steps, err = s.scheduleSteps(ctx, v.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, ScheduleDispatch{Schedule: v, DueAt: due})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	claimed := out[:0]
	for _, item := range out {
		next, calcErr := NextCron(item.Schedule.Expression, item.Schedule.Timezone, item.DueAt)
		if calcErr != nil {
			_, _ = s.db.ExecContext(ctx, `UPDATE deploy_schedules SET enabled=0,updated_at=? WHERE id=? AND next_run_at=?`, now.Unix(), item.Schedule.ID, item.DueAt.Unix())
			continue
		}
		result, updateErr := s.db.ExecContext(ctx, `UPDATE deploy_schedules SET next_run_at=?,updated_at=? WHERE id=? AND next_run_at=?`, next.Unix(), now.Unix(), item.Schedule.ID, item.DueAt.Unix())
		if updateErr != nil {
			return nil, updateErr
		}
		changed, _ := result.RowsAffected()
		if changed == 1 {
			item.Schedule.NextRunAt = &next
			claimed = append(claimed, item)
		}
	}
	return claimed, nil
}

type AutomationScheduler struct {
	store    *AutomationStore
	dispatch func(context.Context, ScheduleDispatch) error
	sweeps   []func(context.Context)
	cancel   context.CancelFunc
	done     chan struct{}
}

func NewAutomationScheduler(store *AutomationStore, dispatch func(context.Context, ScheduleDispatch) error) *AutomationScheduler {
	return &AutomationScheduler{store: store, dispatch: dispatch}
}

// WithSweep adds periodic housekeeping that rides the scheduler's tick, such
// as retrying failed notification deliveries. It must be called before Start.
func (s *AutomationScheduler) WithSweep(sweep func(context.Context)) *AutomationScheduler {
	if sweep != nil {
		s.sweeps = append(s.sweeps, sweep)
	}
	return s
}
func (s *AutomationScheduler) Start(ctx context.Context) {
	if s == nil || s.cancel != nil {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		s.run(runCtx)
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				s.run(runCtx)
			}
		}
	}()
}
func (s *AutomationScheduler) run(ctx context.Context) {
	due, err := s.store.ClaimDueSchedules(ctx, 20)
	if err != nil {
		return
	}
	for _, item := range due {
		_ = s.dispatch(ctx, item)
	}
	for _, sweep := range s.sweeps {
		if ctx.Err() != nil {
			return
		}
		sweep(ctx)
	}
}
func (s *AutomationScheduler) Stop() {
	if s == nil || s.cancel == nil {
		return
	}
	s.cancel()
	<-s.done
	s.cancel = nil
}
func (s *AutomationStore) scheduleSteps(ctx context.Context, id int64) ([]ScheduleStep, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT action,config_json,required FROM deploy_schedule_steps WHERE schedule_id=? ORDER BY ordinal`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScheduleStep{}
	for rows.Next() {
		var v ScheduleStep
		var cfg string
		var required int
		if err := rows.Scan(&v.Action, &cfg, &required); err != nil {
			return nil, err
		}
		v.Config = json.RawMessage(cfg)
		v.Required = required != 0
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *AutomationStore) CreateSchedule(ctx context.Context, projectID, environmentID int64, in ScheduleWrite) (*Schedule, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 100 {
		return nil, fmt.Errorf("schedule name is required")
	}
	if in.Timezone == "" {
		in.Timezone = "UTC"
	}
	next, err := NextCron(in.Expression, in.Timezone, s.now())
	if err != nil {
		return nil, err
	}
	if len(in.Steps) == 0 {
		return nil, fmt.Errorf("schedule needs at least one action")
	}
	for _, step := range in.Steps {
		if !validScheduleAction(step.Action) || !json.Valid(step.Config) {
			return nil, fmt.Errorf("invalid scheduled action %q", step.Action)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now().UTC().Unix()
	res, err := tx.ExecContext(ctx, `INSERT INTO deploy_schedules(environment_id,name,expression,timezone,enabled,next_run_at,created_at,updated_at) SELECT id,?,?,?,?,?,?,? FROM deploy_environments WHERE id=? AND project_id=? AND archived_at=0`, in.Name, in.Expression, in.Timezone, boolInt(in.Enabled), next.Unix(), now, now, environmentID, projectID)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	for i, step := range in.Steps {
		if _, err = tx.ExecContext(ctx, `INSERT INTO deploy_schedule_steps(schedule_id,ordinal,action,config_json,required) VALUES(?,?,?,?,?)`, id, i, step.Action, string(step.Config), boolInt(step.Required)); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	list, err := s.ListSchedules(ctx, projectID, environmentID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, ErrTriggerNotFound
}
func (s *AutomationStore) UpdateSchedule(ctx context.Context, projectID, environmentID, scheduleID int64, in ScheduleWrite) (*Schedule, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 100 {
		return nil, fmt.Errorf("schedule name is required")
	}
	if in.Timezone == "" {
		in.Timezone = "UTC"
	}
	next, err := NextCron(in.Expression, in.Timezone, s.now())
	if err != nil {
		return nil, err
	}
	if len(in.Steps) == 0 {
		return nil, fmt.Errorf("schedule needs at least one action")
	}
	for _, step := range in.Steps {
		if !validScheduleAction(step.Action) || !json.Valid(step.Config) {
			return nil, fmt.Errorf("invalid scheduled action %q", step.Action)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now().UTC().Unix()
	result, err := tx.ExecContext(ctx, `UPDATE deploy_schedules SET name=?,expression=?,timezone=?,enabled=?,next_run_at=?,updated_at=? WHERE id=? AND environment_id=? AND environment_id IN (SELECT id FROM deploy_environments WHERE project_id=? AND archived_at=0)`, in.Name, in.Expression, in.Timezone, boolInt(in.Enabled), next.Unix(), now, scheduleID, environmentID, projectID)
	if err != nil {
		return nil, err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return nil, ErrTriggerNotFound
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM deploy_schedule_steps WHERE schedule_id=?`, scheduleID); err != nil {
		return nil, err
	}
	for i, step := range in.Steps {
		if _, err = tx.ExecContext(ctx, `INSERT INTO deploy_schedule_steps(schedule_id,ordinal,action,config_json,required) VALUES(?,?,?,?,?)`, scheduleID, i, step.Action, string(step.Config), boolInt(step.Required)); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	list, err := s.ListSchedules(ctx, projectID, environmentID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == scheduleID {
			return &list[i], nil
		}
	}
	return nil, ErrTriggerNotFound
}
func (s *AutomationStore) DeleteSchedule(ctx context.Context, projectID, environmentID, scheduleID int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM deploy_schedules WHERE id=? AND environment_id=? AND environment_id IN (SELECT id FROM deploy_environments WHERE project_id=?)`, scheduleID, environmentID, projectID)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrTriggerNotFound
	}
	return nil
}
func validScheduleAction(action string) bool {
	switch action {
	case "deploy", "restart", "container_command", "game_command", "backup":
		return true
	}
	return false
}

// PreviewRef is one preview environment as the pull request panels and the
// automation page read it. Everything past IsolationReason is read from the
// approval, source, address and run rows beside the preview in one query, so
// a list of pull requests can say what each preview is doing without a
// second round trip per row.
type PreviewRef struct {
	ID              int64     `json:"id"`
	TriggerID       int64     `json:"triggerId"`
	ProjectID       int64     `json:"projectId"`
	ProviderRef     string    `json:"providerRef"`
	Number          int       `json:"number"`
	EnvironmentID   int64     `json:"environmentId"`
	EnvironmentSlug string    `json:"environmentSlug"`
	State           string    `json:"state"`
	UpdatedAt       time.Time `json:"updatedAt"`
	IsolationStatus string    `json:"isolationStatus,omitempty"`
	IsolationReason string    `json:"isolationReason,omitempty"`
	// Origin is "" for a preview a webhook opened and PreviewOriginDashboard
	// for one started with "Test this pull request".
	Origin string `json:"origin,omitempty"`
	Title  string `json:"title,omitempty"`
	// Revision is the approved head the preview is configured at;
	// HeadRevision is the newest head seen on the pull request since. They
	// differ once new commits arrive, which is what "out of date" means.
	Revision       string `json:"revision,omitempty"`
	HeadRef        string `json:"headRef,omitempty"`
	HeadRepository string `json:"headRepository,omitempty"`
	Author         string `json:"author,omitempty"`
	HeadRevision   string `json:"headRevision,omitempty"`
	ApprovalState  string `json:"approvalState,omitempty"`
	// VariablesCopiedRevision is the head whose production variables were
	// copied into the preview, or "" when none are.
	VariablesCopiedRevision string          `json:"variablesCopiedRevision,omitempty"`
	LiveReleaseID           int64           `json:"liveReleaseId,omitempty"`
	Address                 *PreviewAddress `json:"address,omitempty"`
	LastRun                 *RecentRun      `json:"lastRun,omitempty"`
}

func (s *AutomationStore) ListPreviews(ctx context.Context, projectID int64) ([]PreviewRef, error) {
	return s.previewRefs(ctx, `WHERE e.project_id=? ORDER BY p.updated_at DESC,p.id DESC`, projectID)
}

func (s *AutomationStore) EnsurePreview(ctx context.Context, t *Trigger, event ProviderEvent) (*PreviewRef, bool, error) {
	if !t.Config.Preview || event.PreviewNumber <= 0 {
		return nil, false, ErrWrongEvent
	}
	if err := s.requirePreviewApproval(ctx, t, event); err != nil {
		return nil, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	ref := fmt.Sprintf("%d", event.PreviewNumber)
	if !event.PreviewClosed {
		// A close or new-head delivery can arrive between the initial approval
		// lookup and this transaction. Configuration must not revive that head.
		var approved int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_preview_approvals WHERE trigger_id=? AND provider_ref=? AND revision=? AND state='approved'`, t.ID, ref, event.Revision).Scan(&approved); err != nil {
			return nil, false, err
		}
		if approved != 1 {
			return nil, false, ErrPreviewApproval
		}
	}
	var existing PreviewRef
	var updated int64
	err = tx.QueryRowContext(ctx, `SELECT p.id,p.trigger_id,p.provider_ref,p.environment_id,e.slug,p.state,p.updated_at FROM deploy_preview_refs p JOIN deploy_environments e ON e.id=p.environment_id WHERE p.trigger_id=? AND p.provider_ref=?`, t.ID, ref).Scan(&existing.ID, &existing.TriggerID, &existing.ProviderRef, &existing.EnvironmentID, &existing.EnvironmentSlug, &existing.State, &updated)
	if err == nil {
		existing.UpdatedAt = time.Unix(updated, 0).UTC()
		if !event.PreviewClosed {
			if err := previewQuarantineAdmissionTx(ctx, tx, existing.EnvironmentID); err != nil {
				return nil, false, err
			}
		}
		if existing.State == "closed" && !event.PreviewClosed {
			var archived, liveID int64
			var active int
			if err := tx.QueryRowContext(ctx, `SELECT archived_at,live_release_id FROM deploy_environments WHERE id=? AND kind='preview'`, existing.EnvironmentID).Scan(&archived, &liveID); err != nil {
				return nil, false, err
			}
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_runs WHERE environment_id=? AND state NOT IN ('succeeded','failed','cancelled','rolled_back','superseded')`, existing.EnvironmentID).Scan(&active); err != nil {
				return nil, false, err
			}
			if archived == 0 || liveID != 0 || active != 0 {
				return nil, false, ErrPreviewCleanupPending
			}
			quota := t.Config.PreviewQuota
			if quota == 0 {
				quota = 5
			}
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_preview_refs WHERE trigger_id=? AND state='open'`, t.ID).Scan(&active); err != nil {
				return nil, false, err
			}
			if active >= quota {
				return nil, false, ErrPreviewQuota
			}
			if _, err := tx.ExecContext(ctx, `UPDATE deploy_environments SET archived_at=0,updated_at=? WHERE id=?`, s.now().UTC().Unix(), existing.EnvironmentID); err != nil {
				return nil, false, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE deploy_preview_refs SET state='open',updated_at=? WHERE id=?`, s.now().UTC().Unix(), existing.ID); err != nil {
				return nil, false, err
			}
			existing.State = "open"
		}
		if event.PreviewClosed {
			now := s.now().UTC().Unix()
			_, err = tx.ExecContext(ctx, `UPDATE deploy_preview_refs SET state='closed',updated_at=? WHERE id=?`, now, existing.ID)
			existing.State = "closed"
		} else {
			var revision int
			if err = tx.QueryRowContext(ctx, `SELECT desired_revision FROM deploy_environments WHERE id=? AND kind='preview' AND archived_at=0`, existing.EnvironmentID).Scan(&revision); err == nil {
				var currentRevision string
				var isolated bool
				var quarantined int
				if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_preview_quarantines WHERE environment_id=? AND status='quarantined'`, existing.EnvironmentID).Scan(&quarantined); err != nil {
					return nil, false, err
				}
				if err = tx.QueryRowContext(ctx, `SELECT COALESCE(json_extract(s.identity_json,'$.revision'),''),COALESCE(json_extract(r.config_json,'$.previewIsolation'),0) FROM deploy_sources s JOIN deploy_runtime_plans r ON r.environment_id=s.environment_id AND r.revision=s.revision WHERE s.environment_id=? AND s.revision=?`, existing.EnvironmentID, revision).Scan(&currentRevision, &isolated); err != nil {
					return nil, false, err
				}
				if currentRevision == event.Revision && isolated && quarantined == 0 {
					if _, err := tx.ExecContext(ctx, `UPDATE deploy_preview_quarantines SET status='cleared',updated_at=? WHERE environment_id=? AND status='quarantined'`, s.now().UTC().Unix(), existing.EnvironmentID); err != nil {
						return nil, false, err
					}
					if err := tx.Commit(); err != nil {
						return nil, false, err
					}
					return &existing, false, nil
				}
				next := revision + 1
				err = createIsolatedPreviewPlansTx(ctx, tx, existing.EnvironmentID, revision, existing.EnvironmentID, next, s.now().UTC().Unix())
				if err == nil {
					err = createPreviewSourceTx(ctx, tx, existing.EnvironmentID, revision, existing.EnvironmentID, next, event)
				}
				if err == nil {
					_, err = tx.ExecContext(ctx, `UPDATE deploy_environments SET desired_revision=?,updated_at=? WHERE id=?`, next, s.now().UTC().Unix(), existing.EnvironmentID)
				}
				if err == nil {
					_, err = tx.ExecContext(ctx, `UPDATE deploy_preview_quarantines SET status='cleared',updated_at=? WHERE environment_id=? AND status='quarantined'`, s.now().UTC().Unix(), existing.EnvironmentID)
				}
			}
		}
		if err != nil {
			return nil, false, err
		}
		if err = tx.Commit(); err != nil {
			return nil, false, err
		}
		return &existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	if event.PreviewClosed {
		return nil, false, ErrWrongEvent
	}
	quota := t.Config.PreviewQuota
	if quota == 0 {
		quota = 5
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deploy_preview_refs WHERE trigger_id=? AND state='open'`, t.ID).Scan(&active); err != nil {
		return nil, false, err
	}
	if active >= quota {
		return nil, false, ErrPreviewQuota
	}
	var name, strategy string
	var desired int
	var downtime, protected int
	if err = tx.QueryRowContext(ctx, `SELECT name,desired_revision,strategy,expected_downtime,protected FROM deploy_environments WHERE id=?`, t.EnvironmentID).Scan(&name, &desired, &strategy, &downtime, &protected); err != nil {
		return nil, false, err
	}
	slug := fmt.Sprintf("pr-%d-t%d", event.PreviewNumber, t.ID)
	now := s.now().UTC().Unix()
	res, err := tx.ExecContext(ctx, `INSERT INTO deploy_environments(project_id,name,slug,kind,desired_revision,strategy,expected_downtime,protected,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, t.ProjectID, "Preview "+ref, slug, EnvironmentPreview, desired, strategy, downtime, 0, now, now)
	if err != nil {
		return nil, false, err
	}
	envID, _ := res.LastInsertId()
	if err = createIsolatedPreviewPlansTx(ctx, tx, t.EnvironmentID, desired, envID, desired, now); err != nil {
		return nil, false, err
	}
	if err = createPreviewSourceTx(ctx, tx, t.EnvironmentID, desired, envID, desired, event); err != nil {
		return nil, false, err
	}
	// Variables and dependencies belong to the preview itself. Even a linked
	// database or a read-only production mount can expose production secrets.
	if err = copyPreviewChecksTx(ctx, tx, t.EnvironmentID, envID, now); err != nil {
		return nil, false, err
	}
	if domain := strings.TrimSpace(t.Config.PreviewDomain); domain != "" {
		domain = strings.ToLower(strings.ReplaceAll(domain, "{number}", ref))
		if !strings.Contains(domain, ".") || strings.ContainsAny(domain, " /\\") {
			return nil, false, fmt.Errorf("%w: invalid preview domain", ErrInvalidPlan)
		}
		config, _ := json.Marshal(map[string]any{"hostname": domain, "https": true, "preview": true})
		if _, err = tx.ExecContext(ctx, `INSERT INTO deploy_dependencies(environment_id,release_id,kind,ownership,resource_kind,resource_id,config_json,created_at) VALUES(?,0,'domain','managed','proxy_site',?,?,?)`, envID, domain, string(config), now); err != nil {
			return nil, false, err
		}
	}
	res, err = tx.ExecContext(ctx, `INSERT INTO deploy_preview_refs(trigger_id,provider_ref,environment_id,state,updated_at) VALUES(?,?,?,'open',?)`, t.ID, ref, envID, now)
	if err != nil {
		return nil, false, err
	}
	previewID, _ := res.LastInsertId()
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	return &PreviewRef{ID: previewID, TriggerID: t.ID, ProviderRef: ref, EnvironmentID: envID, EnvironmentSlug: slug, State: "open", UpdatedAt: time.Unix(now, 0).UTC()}, true, nil
}

func createPreviewSourceTx(ctx context.Context, tx *sql.Tx, sourceEnvironmentID int64, sourceRevision int, targetEnvironmentID int64, targetRevision int, event ProviderEvent) error {
	var kind, configText, identityText string
	var credentialID, createdAt int64
	if err := tx.QueryRowContext(ctx, `SELECT kind,config_json,credential_id,identity_json,created_at FROM deploy_sources WHERE environment_id=? AND revision=?`, sourceEnvironmentID, sourceRevision).Scan(&kind, &configText, &credentialID, &identityText, &createdAt); err != nil {
		return err
	}
	var config, identity map[string]any
	if json.Unmarshal([]byte(configText), &config) != nil || json.Unmarshal([]byte(identityText), &identity) != nil {
		return fmt.Errorf("%w: malformed preview source", ErrInvalidPlan)
	}
	if event.PreviewRef != "" {
		config["ref"] = event.PreviewRef
	}
	identity["ref"] = event.PreviewRef
	identity["revision"] = event.Revision
	identity["previewNumber"] = event.PreviewNumber
	configJSON, _ := json.Marshal(config)
	identityJSON, _ := json.Marshal(identity)
	digest := sha256.Sum256(append(append([]byte(nil), configJSON...), identityJSON...))
	_, err := tx.ExecContext(ctx, `INSERT INTO deploy_sources(environment_id,revision,kind,config_json,credential_id,identity_json,digest,created_at) VALUES(?,?,?,?,?,?,?,?)`, targetEnvironmentID, targetRevision, kind, string(configJSON), credentialID, string(identityJSON), hex.EncodeToString(digest[:]), createdAt)
	return err
}

func (s *AutomationStore) ArchiveClosedPreview(ctx context.Context, previewID int64) error {
	now := s.now().UTC().Unix()
	result, err := s.db.ExecContext(ctx, `UPDATE deploy_environments SET archived_at=?,updated_at=? WHERE id=(SELECT environment_id FROM deploy_preview_refs WHERE id=? AND state='closed') AND kind='preview'`, now, now, previewID)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return ErrTriggerNotFound
	}
	return nil
}

// NextCron implements the deliberately small five-field schedule contract. It
// walks civil minutes in the requested IANA location, so spring gaps are
// skipped and both fall-back instants remain distinct.
func NextCron(expression, timezone string, after time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid timezone")
	}
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return time.Time{}, fmt.Errorf("cron expression must have five fields")
	}
	sets := make([]map[int]bool, 5)
	ranges := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	for i, f := range fields {
		sets[i], err = parseCronField(f, ranges[i][0], ranges[i][1])
		if err != nil {
			return time.Time{}, err
		}
	}
	candidate := after.UTC().Truncate(time.Minute).Add(time.Minute)
	limit := candidate.Add(370 * 24 * time.Hour)
	for candidate.Before(limit) {
		c := candidate.In(loc)
		if sets[0][c.Minute()] && sets[1][c.Hour()] && sets[2][c.Day()] && sets[3][int(c.Month())] && sets[4][int(c.Weekday())] {
			return candidate, nil
		}
		candidate = candidate.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("schedule has no occurrence in the next year")
}

// NextCronRuns is the next count firings after the given instant, in order.
// A schedule with fewer in the coming year answers with the ones it has.
func NextCronRuns(expression, timezone string, after time.Time, count int) ([]time.Time, error) {
	runs := make([]time.Time, 0, count)
	for len(runs) < count {
		next, err := NextCron(expression, timezone, after)
		if err != nil {
			if len(runs) > 0 {
				break
			}
			return nil, err
		}
		runs = append(runs, next)
		after = next
	}
	return runs, nil
}

func parseCronField(raw string, min, max int) (map[int]bool, error) {
	out := map[int]bool{}
	for _, part := range strings.Split(raw, ",") {
		step := 1
		base := part
		if strings.Contains(part, "/") {
			pieces := strings.Split(part, "/")
			if len(pieces) != 2 {
				return nil, fmt.Errorf("invalid cron field")
			}
			base = pieces[0]
			var err error
			step, err = strconv.Atoi(pieces[1])
			if err != nil || step < 1 {
				return nil, fmt.Errorf("invalid cron step")
			}
		}
		lo, hi := min, max
		if base != "*" {
			if strings.Contains(base, "-") {
				p := strings.Split(base, "-")
				if len(p) != 2 {
					return nil, fmt.Errorf("invalid cron range")
				}
				lo, _ = strconv.Atoi(p[0])
				hi, _ = strconv.Atoi(p[1])
			} else {
				lo, _ = strconv.Atoi(base)
				hi = lo
			}
		}
		if lo < min || hi > max || lo > hi {
			return nil, fmt.Errorf("cron value outside range")
		}
		for n := lo; n <= hi; n += step {
			out[n] = true
		}
	}
	return out, nil
}

type NotificationChannel struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	URL       string    `json:"url"`
	Target    string    `json:"target"`
	Events    []string  `json:"events"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// Via, LastDelivery and Recent are the list page's reading of a channel
	// and only NotificationChannelsWithHistory fills them: the dispatcher
	// reads channels on every run and has no use for their history.
	//
	// Via is the mail server an e-mail channel hands its messages to. The host
	// is not a credential — the sign-in beside it is — so it alone is read
	// back out of the sealed configuration.
	Via          string                `json:"via,omitempty"`
	LastDelivery *NotificationAttempt  `json:"lastDelivery,omitempty"`
	Recent       []NotificationOutcome `json:"recent,omitempty"`
}

// NotificationAttempt is a channel's newest delivery attempt as its row
// reports it: what was sent, how it went and whether a retry is still ahead.
type NotificationAttempt struct {
	Status        string     `json:"status"`
	Event         string     `json:"event"`
	ResponseClass string     `json:"responseClass"`
	CreatedAt     time.Time  `json:"createdAt"`
	NextAttemptAt *time.Time `json:"nextAttemptAt,omitempty"`
}

// NotificationOutcome is one square of a channel's recent-delivery strip. Test
// marks a message an operator sent from the page, which says nothing about
// whether deployments are being announced.
type NotificationOutcome struct {
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
	Test      bool      `json:"test"`
}

// notificationRecentOutcomes is how many attempts a channel's strip draws.
const notificationRecentOutcomes = 14

// NotificationWrite is the create/update request. Kind is fixed at creation;
// Config carries the per-kind credentials and is never echoed back.
type NotificationWrite struct {
	Name    string             `json:"name"`
	Kind    string             `json:"kind,omitempty"`
	URL     string             `json:"url,omitempty"`
	Headers map[string]string  `json:"headers,omitempty"`
	Secret  string             `json:"secret,omitempty"`
	Config  NotificationConfig `json:"config,omitempty"`
	Events  []string           `json:"events"`
	Enabled bool               `json:"enabled"`
}
type NotificationDelivery struct {
	ID            int64      `json:"id"`
	ChannelID     int64      `json:"channelId"`
	RunID         int64      `json:"runId,omitempty"`
	Event         string     `json:"event"`
	Attempt       int        `json:"attempt"`
	Status        string     `json:"status"`
	ResponseClass string     `json:"responseClass"`
	NextAttemptAt *time.Time `json:"nextAttemptAt,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	CompletedAt   *time.Time `json:"completedAt,omitempty"`
	// ProjectID, ProjectName and RunNumber name the run a delivery announced,
	// so its history row can say "api · run #12" and link to it. All three are
	// absent for a test message and for a run that has since been purged.
	ProjectID   int64  `json:"projectId,omitempty"`
	ProjectName string `json:"projectName,omitempty"`
	RunNumber   int64  `json:"runNumber,omitempty"`
}

func (s *AutomationStore) NotificationDeliveries(ctx context.Context, channelID int64, limit int) ([]NotificationDelivery, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id,d.channel_id,d.run_id,d.event,d.attempt,d.status,d.response_class,d.next_attempt_at,d.created_at,d.completed_at,
		       COALESCE(r.project_id,0),COALESCE(NULLIF(p.archived_name,''),p.name,''),COALESCE(r.run_number,0)
		  FROM deploy_notification_deliveries d
		  LEFT JOIN deploy_runs r ON r.id=d.run_id AND d.run_id<>0
		  LEFT JOIN deploy_projects p ON p.id=r.project_id
		 WHERE d.channel_id=? ORDER BY d.id DESC LIMIT ?`, channelID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NotificationDelivery{}
	for rows.Next() {
		var d NotificationDelivery
		var next, created, completed int64
		if err := rows.Scan(&d.ID, &d.ChannelID, &d.RunID, &d.Event, &d.Attempt, &d.Status, &d.ResponseClass, &next, &created, &completed, &d.ProjectID, &d.ProjectName, &d.RunNumber); err != nil {
			return nil, err
		}
		d.CreatedAt = time.Unix(created, 0).UTC()
		if next > 0 {
			x := time.Unix(next, 0).UTC()
			d.NextAttemptAt = &x
		}
		if completed > 0 {
			x := time.Unix(completed, 0).UTC()
			d.CompletedAt = &x
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// WithMailer replaces the SMTP delivery function, for tests.
func (s *AutomationStore) WithMailer(mailer Mailer) *AutomationStore {
	s.mailer = mailer
	return s
}

func scanNotificationChannels(rows *sql.Rows) ([]NotificationChannel, error) {
	out := []NotificationChannel{}
	for rows.Next() {
		var c NotificationChannel
		var events string
		var enabled int
		var created, updated int64
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &c.URL, &c.Target, &events, &enabled, &created, &updated); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(events), &c.Events)
		if c.Events == nil {
			c.Events = []string{}
		}
		if c.Kind == "" {
			c.Kind = NotificationKindWebhook
		}
		if c.Target == "" {
			c.Target = c.URL
		}
		c.Enabled = enabled != 0
		c.CreatedAt = time.Unix(created, 0).UTC()
		c.UpdatedAt = time.Unix(updated, 0).UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *AutomationStore) ListNotificationChannels(ctx context.Context) ([]NotificationChannel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,kind,url,target,events,enabled,created_at,updated_at FROM deploy_notification_channels ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNotificationChannels(rows)
}

// NotificationChannelsWithHistory is the channel list as its page reads it:
// every channel with its newest attempt, its last notificationRecentOutcomes
// outcomes oldest first, and an e-mail channel's mail server. The history is
// one statement for every channel, not a request per row.
func (s *AutomationStore) NotificationChannelsWithHistory(ctx context.Context) ([]NotificationChannel, error) {
	channels, err := s.ListNotificationChannels(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*NotificationChannel, len(channels))
	for i := range channels {
		byID[channels[i].ID] = &channels[i]
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT channel_id,event,status,response_class,next_attempt_at,created_at FROM (
		  SELECT id,channel_id,event,status,response_class,next_attempt_at,created_at,
		         ROW_NUMBER() OVER (PARTITION BY channel_id ORDER BY id DESC) AS rank
		    FROM deploy_notification_deliveries
		) WHERE rank<=? ORDER BY channel_id,id`, notificationRecentOutcomes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var channelID, next, created int64
		var attempt NotificationAttempt
		if err := rows.Scan(&channelID, &attempt.Event, &attempt.Status, &attempt.ResponseClass, &next, &created); err != nil {
			return nil, err
		}
		channel := byID[channelID]
		if channel == nil {
			// Created between the two reads; it is drawn without a history
			// until the next one.
			continue
		}
		attempt.CreatedAt = time.Unix(created, 0).UTC()
		if next > 0 {
			x := time.Unix(next, 0).UTC()
			attempt.NextAttemptAt = &x
		}
		channel.Recent = append(channel.Recent, NotificationOutcome{Status: attempt.Status, CreatedAt: attempt.CreatedAt, Test: attempt.Event == NotificationEventTest})
		channel.LastDelivery = &attempt
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	mail, err := s.db.QueryContext(ctx, `SELECT id,config_enc FROM deploy_notification_channels WHERE kind=?`, NotificationKindEmail)
	if err != nil {
		return nil, err
	}
	defer mail.Close()
	for mail.Next() {
		var channelID int64
		var sealed string
		if err := mail.Scan(&channelID, &sealed); err != nil {
			return nil, err
		}
		channel := byID[channelID]
		if channel == nil {
			continue
		}
		// A configuration this key cannot open leaves the host unnamed: its
		// deliveries already say "sealed", and the list must still load.
		if config, err := s.openNotificationConfig(sealed); err == nil {
			channel.Via = config.SMTPHost
		}
	}
	return channels, mail.Err()
}

func (s *AutomationStore) notificationChannel(ctx context.Context, id int64) (*NotificationChannel, error) {
	channels, err := s.ListNotificationChannels(ctx)
	if err != nil {
		return nil, err
	}
	for i := range channels {
		if channels[i].ID == id {
			return &channels[i], nil
		}
	}
	return nil, ErrTriggerNotFound
}

func (s *AutomationStore) CreateNotificationChannel(ctx context.Context, in NotificationWrite) (*NotificationChannel, string, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 100 {
		return nil, "", invalidNotification("channel name is required")
	}
	resolved, err := validateNotificationChannel(in.Kind, in.URL, in.Config, nil)
	if err != nil {
		return nil, "", err
	}
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	if kind == "" {
		kind = NotificationKindWebhook
	}
	events, err := normalizeNotificationEvents(in.Events)
	if err != nil {
		return nil, "", err
	}
	if in.Secret == "" {
		if in.Secret, err = randomHex(32); err != nil {
			return nil, "", err
		}
	}
	headers, err := json.Marshal(in.Headers)
	if err != nil {
		return nil, "", err
	}
	sealedHeaders, err := s.sealer.Seal(string(headers))
	if err != nil {
		return nil, "", err
	}
	sealedSecret, err := s.sealer.Seal(in.Secret)
	if err != nil {
		return nil, "", err
	}
	sealedConfig, err := s.sealer.Seal(string(mustJSON(resolved.Config)))
	if err != nil {
		return nil, "", err
	}
	now := s.now().UTC().Unix()
	res, err := s.db.ExecContext(ctx, `INSERT INTO deploy_notification_channels(name,kind,url,target,headers_enc,secret_enc,config_enc,events,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		in.Name, kind, resolved.URL, resolved.Target, sealedHeaders, sealedSecret, sealedConfig, string(mustJSON(events)), boolInt(in.Enabled), now, now)
	if err != nil {
		return nil, "", err
	}
	id, _ := res.LastInsertId()
	channel, err := s.notificationChannel(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if kind != NotificationKindWebhook {
		// Only the signed webhook contract has a shared secret the caller
		// needs to copy; provider kinds carry their credential in Config.
		in.Secret = ""
	}
	return channel, in.Secret, nil
}

func (s *AutomationStore) UpdateNotificationChannel(ctx context.Context, id int64, in NotificationWrite) (*NotificationChannel, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 100 {
		return nil, invalidNotification("channel name is required")
	}
	current, err := s.notificationChannel(ctx, id)
	if err != nil {
		return nil, err
	}
	requested := strings.ToLower(strings.TrimSpace(in.Kind))
	if requested != "" && requested != current.Kind {
		return nil, invalidNotification("a channel's kind cannot change; create a new channel instead")
	}
	existing, err := s.notificationConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	url := in.URL
	if current.Kind != NotificationKindWebhook && strings.TrimSpace(url) == current.URL {
		// The masked display value came back unchanged from the form.
		url = ""
	}
	resolved, err := validateNotificationChannel(current.Kind, url, in.Config, &existing)
	if err != nil {
		return nil, err
	}
	events, err := normalizeNotificationEvents(in.Events)
	if err != nil {
		return nil, err
	}
	headers, err := json.Marshal(in.Headers)
	if err != nil {
		return nil, err
	}
	sealedHeaders, err := s.sealer.Seal(string(headers))
	if err != nil {
		return nil, err
	}
	sealedConfig, err := s.sealer.Seal(string(mustJSON(resolved.Config)))
	if err != nil {
		return nil, err
	}
	now := s.now().UTC().Unix()
	result, err := s.db.ExecContext(ctx, `UPDATE deploy_notification_channels SET name=?,url=?,target=?,headers_enc=?,config_enc=?,events=?,enabled=?,updated_at=? WHERE id=?`,
		in.Name, resolved.URL, resolved.Target, sealedHeaders, sealedConfig, string(mustJSON(events)), boolInt(in.Enabled), now, id)
	if err != nil {
		return nil, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return nil, ErrTriggerNotFound
	}
	if in.Secret != "" {
		sealedSecret, sealErr := s.sealer.Seal(in.Secret)
		if sealErr != nil {
			return nil, sealErr
		}
		if _, err = s.db.ExecContext(ctx, `UPDATE deploy_notification_channels SET secret_enc=?,updated_at=? WHERE id=?`, sealedSecret, now, id); err != nil {
			return nil, err
		}
	}
	return s.notificationChannel(ctx, id)
}

// SetNotificationChannelEnabled pauses or resumes a channel without touching
// its configuration or credentials.
func (s *AutomationStore) SetNotificationChannelEnabled(ctx context.Context, id int64, enabled bool) (*NotificationChannel, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE deploy_notification_channels SET enabled=?,updated_at=? WHERE id=?`, boolInt(enabled), s.now().UTC().Unix(), id)
	if err != nil {
		return nil, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return nil, ErrTriggerNotFound
	}
	return s.notificationChannel(ctx, id)
}

func (s *AutomationStore) notificationConfig(ctx context.Context, id int64) (NotificationConfig, error) {
	var sealed string
	if err := s.db.QueryRowContext(ctx, `SELECT config_enc FROM deploy_notification_channels WHERE id=?`, id).Scan(&sealed); err != nil {
		return NotificationConfig{}, err
	}
	var cfg NotificationConfig
	if sealed == "" {
		return cfg, nil
	}
	raw, err := s.sealer.Open(sealed)
	if err != nil {
		return cfg, err
	}
	if raw == "" || raw == "null" {
		return cfg, nil
	}
	err = json.Unmarshal([]byte(raw), &cfg)
	return cfg, err
}
func (s *AutomationStore) DeleteNotificationChannel(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM deploy_notification_channels WHERE id=?`, id)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrTriggerNotFound
	}
	return nil
}

// DeliverNotification renders and sends one envelope to one channel. Every
// kind records only a status class in history: a remote endpoint or mail
// relay cannot reflect a secret into this database or the deployment log.
func (s *AutomationStore) DeliverNotification(ctx context.Context, client *http.Client, channelID int64, envelope NotificationEnvelope) error {
	var kind, url, sealedHeaders, sealedSecret, sealedConfig, events string
	var enabled int
	if err := s.db.QueryRowContext(ctx, `SELECT kind,url,headers_enc,secret_enc,config_enc,events,enabled FROM deploy_notification_channels WHERE id=?`, channelID).Scan(&kind, &url, &sealedHeaders, &sealedSecret, &sealedConfig, &events, &enabled); err != nil {
		return err
	}
	if enabled == 0 {
		return ErrHookDisabled
	}
	if kind == "" {
		kind = NotificationKindWebhook
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if envelope.SentAt.IsZero() {
		envelope.SentAt = s.now().UTC()
	}
	attempt, reserved, err := s.reserveNotificationAttempt(ctx, channelID, envelope.RunID, envelope.Event)
	if err != nil {
		return err
	}
	if !reserved {
		// Another observer owns this event already: one message per outcome.
		return nil
	}
	var sendErr error
	responseClass := "network"
	switch kind {
	case NotificationKindWebhook:
		responseClass, sendErr = s.deliverWebhook(ctx, client, url, sealedHeaders, sealedSecret, envelope)
	case NotificationKindEmail:
		responseClass, sendErr = s.deliverEmail(ctx, sealedConfig, envelope)
	default:
		responseClass, sendErr = s.deliverProvider(ctx, client, kind, sealedConfig, envelope)
	}
	status := "delivered"
	nextAttempt := int64(0)
	if sendErr != nil {
		status = "failed"
		if envelope.RunID != 0 && attempt < notificationMaxAttempts {
			nextAttempt = s.now().UTC().Add(notificationRetryDelays[min(attempt, len(notificationRetryDelays))-1]).Unix()
		}
	}
	completed := s.now().UTC().Unix()
	// The row is written with a context detached from the caller: a delivery
	// that happened must be recorded even when the observer deadline expired
	// while the provider was answering.
	if _, storeErr := s.db.ExecContext(context.WithoutCancel(ctx), `UPDATE deploy_notification_deliveries SET status=?,response_class=?,completed_at=?,next_attempt_at=? WHERE channel_id=? AND run_id=? AND event=? AND attempt=?`,
		status, responseClass, completed, nextAttempt, channelID, envelope.RunID, envelope.Event, attempt); storeErr != nil {
		return storeErr
	}
	return sendErr
}

// reserveNotificationAttempt claims the next attempt for (channel, run, event)
// in one write transaction. The store opens write transactions IMMEDIATE, so
// two observers announcing the same outcome serialize here and the second sees
// the first one's pending row. Test deliveries (run 0) are never deduplicated.
func (s *AutomationStore) reserveNotificationAttempt(ctx context.Context, channelID, runID int64, event string) (int, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	if runID != 0 {
		var owned int
		// Delivered, being delivered, or failed with a retry still ahead: the
		// event is owned and a repeated observer call must not jump the
		// backoff queue.
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM deploy_notification_deliveries WHERE channel_id=? AND run_id=? AND event=? AND (status='delivered' OR (status='pending' AND created_at>?) OR (status='failed' AND next_attempt_at>?))`,
			channelID, runID, event, now.Add(-notificationReservationTTL).Unix(), now.Unix()).Scan(&owned); err != nil {
			return 0, false, err
		}
		if owned > 0 {
			return 0, false, nil
		}
		// A stale reservation is a send whose process died; it is recorded as
		// failed so history says what happened, and the retry it never
		// scheduled is not scheduled now either.
		if _, err := tx.ExecContext(ctx, `UPDATE deploy_notification_deliveries SET status='failed',response_class='interrupted',completed_at=?,next_attempt_at=0 WHERE channel_id=? AND run_id=? AND event=? AND status='pending'`, now.Unix(), channelID, runID, event); err != nil {
			return 0, false, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE deploy_notification_deliveries SET next_attempt_at=0 WHERE channel_id=? AND run_id=? AND event=? AND status='failed'`, channelID, runID, event); err != nil {
			return 0, false, err
		}
	}
	var attempt int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(attempt),0)+1 FROM deploy_notification_deliveries WHERE channel_id=? AND run_id=? AND event=?`, channelID, runID, event).Scan(&attempt); err != nil {
		return 0, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO deploy_notification_deliveries(channel_id,run_id,event,attempt,status,response_class,created_at,completed_at) VALUES(?,?,?,?,'pending','',?,0)`, channelID, runID, event, attempt, now.Unix()); err != nil {
		if isUniqueConstraint(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return attempt, true, nil
}

type notificationRetry struct {
	ChannelID, RunID int64
	Event            string
	Attempt          int
}

// dueNotificationRetries lists failed deliveries whose retry time has passed
// and for which no later attempt exists.
func (s *AutomationStore) dueNotificationRetries(ctx context.Context, limit int) ([]notificationRetry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.channel_id, d.run_id, d.event, d.attempt
		  FROM deploy_notification_deliveries d
		  JOIN deploy_notification_channels c ON c.id = d.channel_id AND c.enabled = 1
		 WHERE d.run_id <> 0 AND d.status = 'failed' AND d.next_attempt_at > 0 AND d.next_attempt_at <= ?
		   AND NOT EXISTS (SELECT 1 FROM deploy_notification_deliveries later
		                    WHERE later.channel_id = d.channel_id AND later.run_id = d.run_id
		                      AND later.event = d.event AND later.attempt > d.attempt)
		 ORDER BY d.next_attempt_at, d.id LIMIT ?`, s.now().UTC().Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []notificationRetry{}
	for rows.Next() {
		var item notificationRetry
		if err := rows.Scan(&item.ChannelID, &item.RunID, &item.Event, &item.Attempt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// abandonNotificationRetry clears the retry of a delivery whose run can no
// longer be read; there is nothing left to say about it.
func (s *AutomationStore) abandonNotificationRetry(ctx context.Context, item notificationRetry) error {
	_, err := s.db.ExecContext(ctx, `UPDATE deploy_notification_deliveries SET next_attempt_at=0 WHERE channel_id=? AND run_id=? AND event=? AND attempt=?`, item.ChannelID, item.RunID, item.Event, item.Attempt)
	return err
}

func (s *AutomationStore) deliverWebhook(ctx context.Context, client *http.Client, url, sealedHeaders, sealedSecret string, envelope NotificationEnvelope) (string, error) {
	secret, err := s.sealer.Open(sealedSecret)
	if err != nil {
		return "sealed", err
	}
	headersJSON, err := s.sealer.Open(sealedHeaders)
	if err != nil {
		return "sealed", err
	}
	var headers map[string]string
	if headersJSON != "" {
		if err = json.Unmarshal([]byte(headersJSON), &headers); err != nil {
			return "sealed", err
		}
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return "network", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "network", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-JD-Event", envelope.Event)
	req.Header.Set("X-JD-Signature-256", SignNotification(body, secret))
	for key, value := range headers {
		if strings.EqualFold(key, "Host") || strings.EqualFold(key, "Content-Length") {
			continue
		}
		req.Header.Set(key, value)
	}
	return postNotification(client, req)
}

func (s *AutomationStore) deliverProvider(ctx context.Context, client *http.Client, kind, sealedConfig string, envelope NotificationEnvelope) (string, error) {
	cfg, err := s.openNotificationConfig(sealedConfig)
	if err != nil {
		return "sealed", err
	}
	message := renderNotification(envelope)
	var target string
	var body []byte
	switch kind {
	case NotificationKindDiscord:
		target, body = cfg.WebhookURL, discordPayload(message, envelope.SentAt)
	case NotificationKindSlack:
		target, body = cfg.WebhookURL, slackPayload(message)
	case NotificationKindTelegram:
		target = "https://api.telegram.org/bot" + cfg.BotToken + "/sendMessage"
		body = telegramPayload(cfg.ChatID, message)
	default:
		return "unsupported", invalidNotification("unknown channel kind %q", kind)
	}
	if target == "" {
		return "sealed", invalidNotification("channel has no delivery target configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return "network", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "just-dashboard-notifications")
	return postNotification(client, req)
}

func (s *AutomationStore) deliverEmail(ctx context.Context, sealedConfig string, envelope NotificationEnvelope) (string, error) {
	cfg, err := s.openNotificationConfig(sealedConfig)
	if err != nil {
		return "sealed", err
	}
	_, body := emailMessage(cfg, renderNotification(envelope), envelope.SentAt)
	mailer := s.mailer
	if mailer == nil {
		mailer = sendSMTPMail
	}
	if err := mailer(ctx, cfg, body); err != nil {
		return "smtp", err
	}
	return "smtp", nil
}

func (s *AutomationStore) openNotificationConfig(sealed string) (NotificationConfig, error) {
	var cfg NotificationConfig
	if sealed == "" {
		return cfg, nil
	}
	raw, err := s.sealer.Open(sealed)
	if err != nil {
		return cfg, err
	}
	if raw == "" || raw == "null" {
		return cfg, nil
	}
	err = json.Unmarshal([]byte(raw), &cfg)
	return cfg, err
}

func postNotification(client *http.Client, req *http.Request) (string, error) {
	res, err := client.Do(req)
	if err != nil {
		return "network", err
	}
	discardBody(res.Body)
	responseClass := fmt.Sprintf("%dxx", res.StatusCode/100)
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return responseClass, nil
	}
	return responseClass, fmt.Errorf("notification endpoint returned %s", responseClass)
}

func SignNotification(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
