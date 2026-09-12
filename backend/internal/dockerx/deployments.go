package dockerx

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// A record of what a compose stack was, so that changing it is reversible.
//
// Docker keeps none of this. Bringing a project up replaces what was running,
// and unless the compose file happened to be committed a minute earlier, the
// previous configuration is gone: "what changed" has no answer and "put it
// back" has no target. That is the single worst property of managing a server
// by compose file, and it is entirely fixable by writing down the file and the
// digests before touching anything.
//
// What is stored is deliberately small — a file, a service list, a digest per
// service, and who did it. Not the images: those are in the registry or on
// disk, and a rollback that cannot find them says so rather than pretending.

// StackDeployment is one recorded state of a compose project.
type StackDeployment struct {
	ID         int64     `json:"id"`
	Project    string    `json:"project"`
	WorkingDir string    `json:"workingDir,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`

	// ConfigHash identifies the compose file, so two deployments of identical
	// configuration are visibly identical. Config is the file itself, which is
	// what makes a rollback possible at all.
	ConfigHash string `json:"configHash"`
	Config     string `json:"config,omitempty"`

	// Services is what the file declared, and ImageDigests is what each one
	// was actually running — the resolved digest, not the tag, because a tag
	// is the thing that moved.
	Services     []string          `json:"services"`
	ImageDigests map[string]string `json:"imageDigests"`

	// EnvHash covers the .env file where there is one, so an environment
	// change is visible without the values ever being stored. Storing the
	// values would put every database password in this table.
	EnvHash string `json:"envHash,omitempty"`

	GitCommit string `json:"gitCommit,omitempty"`
	GitBranch string `json:"gitBranch,omitempty"`
	GitDirty  bool   `json:"gitDirty,omitempty"`

	// Actor is who asked, Source is what asked (the dashboard, a schedule),
	// Action is what was done, and Result is how it went.
	Actor  string `json:"actor,omitempty"`
	Source string `json:"source,omitempty"`
	Action string `json:"action,omitempty"`
	Result string `json:"result,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// deploymentsKept is how many records survive per project. Enough to reach
// back past a bad week, small enough that the table never becomes a thing to
// worry about.
const deploymentsKept = 50

// DeploymentStore records and reads stack deployment history.
type DeploymentStore struct{ db *sql.DB }

func NewDeploymentStore(db *sql.DB) *DeploymentStore { return &DeploymentStore{db: db} }

const deploymentCols = `id, project, working_dir, config_hash, config, services,
	image_digests, env_hash, git_commit, git_branch, git_dirty, actor, source,
	action, result, detail, created_at`

// Record stores one deployment and prunes the project's oldest.
func (s *DeploymentStore) Record(ctx context.Context, d StackDeployment) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	services, _ := json.Marshal(orEmpty(d.Services))
	digests, _ := json.Marshal(orEmptyMap(d.ImageDigests))
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO docker_stack_deployments
			(project, working_dir, config_hash, config, services, image_digests,
			 env_hash, git_commit, git_branch, git_dirty, actor, source, action,
			 result, detail, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.Project, d.WorkingDir, d.ConfigHash, d.Config, string(services), string(digests),
		d.EnvHash, d.GitCommit, d.GitBranch, boolInt(d.GitDirty), d.Actor, d.Source,
		d.Action, d.Result, d.Detail, d.CreatedAt.Unix())
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	// Pruning by rank rather than by age: a stack deployed twice a year should
	// keep its history, and one deployed hourly should not fill the table.
	_, _ = s.db.ExecContext(ctx, `
		DELETE FROM docker_stack_deployments
		WHERE project = ? AND id NOT IN (
			SELECT id FROM docker_stack_deployments
			WHERE project = ? ORDER BY created_at DESC, id DESC LIMIT ?
		)`, d.Project, d.Project, deploymentsKept)
	return id, nil
}

// List returns a project's history, newest first. The compose file itself is
// omitted — a list of fifty deployments is not fifty compose files.
func (s *DeploymentStore) List(ctx context.Context, project string, limit int) ([]StackDeployment, error) {
	if s == nil || s.db == nil {
		return []StackDeployment{}, nil
	}
	if limit <= 0 || limit > deploymentsKept {
		limit = deploymentsKept
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+deploymentCols+`
		FROM docker_stack_deployments WHERE project = ?
		ORDER BY created_at DESC, id DESC LIMIT ?`, project, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StackDeployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		d.Config = ""
		out = append(out, *d)
	}
	return out, rows.Err()
}

// Get returns one deployment with its compose file, which is what a rollback
// needs and what a diff is drawn against.
func (s *DeploymentStore) Get(ctx context.Context, id int64) (*StackDeployment, error) {
	if s == nil || s.db == nil {
		return nil, sql.ErrNoRows
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+deploymentCols+` FROM docker_stack_deployments WHERE id = ?`, id)
	return scanDeployment(row)
}

// Latest is the most recent record for a project, or nil when there is none.
func (s *DeploymentStore) Latest(ctx context.Context, project string) (*StackDeployment, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT `+deploymentCols+` FROM docker_stack_deployments
		WHERE project = ? ORDER BY created_at DESC, id DESC LIMIT 1`, project)
	d, err := scanDeployment(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return d, err
}

func scanDeployment(row interface{ Scan(...any) error }) (*StackDeployment, error) {
	var (
		d                 StackDeployment
		services, digests string
		dirty             int
		created           int64
	)
	if err := row.Scan(&d.ID, &d.Project, &d.WorkingDir, &d.ConfigHash, &d.Config,
		&services, &digests, &d.EnvHash, &d.GitCommit, &d.GitBranch, &dirty,
		&d.Actor, &d.Source, &d.Action, &d.Result, &d.Detail, &created); err != nil {
		return nil, err
	}
	d.GitDirty = dirty == 1
	d.CreatedAt = time.Unix(created, 0).UTC()
	d.Services = []string{}
	d.ImageDigests = map[string]string{}
	_ = json.Unmarshal([]byte(services), &d.Services)
	_ = json.Unmarshal([]byte(digests), &d.ImageDigests)
	return &d, nil
}

// HashConfig identifies a compose file by content, ignoring trailing
// whitespace so a re-save with no change reads as no change.
func HashConfig(content string) string {
	sum := sha256.Sum256([]byte(strings.TrimRight(content, " \t\r\n")))
	return hex.EncodeToString(sum[:12])
}

// SnapshotStack captures what a project is running right now.
//
// Called before a deploy, so what is recorded is the state being replaced —
// which is the state a rollback would restore.
func (c *Client) SnapshotStack(ctx context.Context, st *ComposeStack, config string) StackDeployment {
	snap := StackDeployment{
		Project:      st.Name,
		WorkingDir:   st.WorkingDir,
		Config:       config,
		ConfigHash:   HashConfig(config),
		Services:     append([]string{}, st.Declared...),
		ImageDigests: map[string]string{},
		CreatedAt:    time.Now().UTC(),
	}
	if len(snap.Services) == 0 {
		for _, svc := range st.Services {
			if !svc.Missing {
				snap.Services = append(snap.Services, svc.Name)
			}
		}
		sort.Strings(snap.Services)
	}
	// The digest each service is actually running, resolved through the
	// container rather than through the compose file. A file that says
	// `image: app:latest` records nothing useful; the container knows which
	// `latest` it got.
	cli, err := c.api()
	if err != nil {
		return snap
	}
	for _, svc := range st.Services {
		if svc.Container == "" {
			continue
		}
		insp, err := cli.ContainerInspect(ctx, svc.Container)
		if err != nil {
			continue
		}
		digest := insp.Image
		if img, err := cli.ImageInspect(ctx, insp.Image); err == nil && len(img.RepoDigests) > 0 {
			digest = img.RepoDigests[0]
		}
		snap.ImageDigests[svc.Name] = digest
	}
	return snap
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func orEmptyMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
