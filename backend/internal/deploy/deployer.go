package deploy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

var ErrAlreadyDeploying = fmt.Errorf("a deployment for this project is already running")

// Deployer executes deployments. One at a time per project: two concurrent
// `compose up` runs in the same directory fight over the same containers.
type Deployer struct {
	store *Store
	log   *slog.Logger

	mu      sync.Mutex
	running map[int64]bool
}

func NewDeployer(store *Store, log *slog.Logger) *Deployer {
	return &Deployer{store: store, log: log, running: map[int64]bool{}}
}

func (d *Deployer) IsRunning(projectID int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.running[projectID]
}

type GitState struct {
	SHA    string `json:"sha"`
	Ref    string `json:"ref"`
	Dirty  bool   `json:"dirty"`
	Remote string `json:"remote,omitempty"`
}

// Inspect reports where a project's checkout currently stands, which is what
// the UI shows next to the deploy button.
func (d *Deployer) Inspect(ctx context.Context, p *Project) (*GitState, error) {
	if _, err := os.Stat(filepath.Join(p.RepoPath, ".git")); err != nil {
		return nil, fmt.Errorf("%s is not a git repository", p.RepoPath)
	}
	st := &GitState{}
	if out, err := d.git(ctx, p.RepoPath, "rev-parse", "HEAD"); err == nil {
		st.SHA = strings.TrimSpace(out)
	}
	if out, err := d.git(ctx, p.RepoPath, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		st.Ref = strings.TrimSpace(out)
	}
	if out, err := d.git(ctx, p.RepoPath, "status", "--porcelain"); err == nil {
		st.Dirty = strings.TrimSpace(out) != ""
	}
	if out, err := d.git(ctx, p.RepoPath, "config", "--get", "remote.origin.url"); err == nil {
		st.Remote = strings.TrimSpace(out)
	}
	return st, nil
}

// Deploy fetches the configured branch, writes the project's sealed
// environment into .env, and rebuilds the stack. Returns the completed run.
func (d *Deployer) Deploy(ctx context.Context, projectID int64, trigger, actor string) (*Run, error) {
	return d.execute(ctx, projectID, trigger, actor, "")
}

// Rollback re-deploys a specific commit. It is the same pipeline as Deploy
// with a checkout of the target commit instead of a pull, so a rollback is
// exercised by exactly the code path that deploys.
func (d *Deployer) Rollback(ctx context.Context, projectID int64, commit, actor string) (*Run, error) {
	if err := ValidateSHA(commit); err != nil {
		return nil, err
	}
	return d.execute(ctx, projectID, "rollback", actor, commit)
}

func (d *Deployer) execute(ctx context.Context, projectID int64, trigger, actor, targetCommit string) (*Run, error) {
	d.mu.Lock()
	if d.running[projectID] {
		d.mu.Unlock()
		return nil, ErrAlreadyDeploying
	}
	d.running[projectID] = true
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		delete(d.running, projectID)
		d.mu.Unlock()
	}()

	project, err := d.store.Get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if !project.Enabled && trigger == "webhook" {
		return nil, ErrDisabled
	}
	before, _ := d.Inspect(ctx, project)
	fromCommit := ""
	if before != nil {
		fromCommit = before.SHA
	}
	runID, err := d.store.StartRun(ctx, projectID, trigger, actor, fromCommit)
	if err != nil {
		return nil, err
	}

	var logBuf bytes.Buffer
	toCommit, err := d.pipeline(ctx, project, targetCommit, &logBuf)
	status := StatusSuccess
	if err != nil {
		status = StatusFailed
		fmt.Fprintf(&logBuf, "\nFAILED: %v\n", err)
		d.log.Error("deployment failed", "project", project.Name, "err", err)
	}
	if toCommit == "" {
		toCommit = fromCommit
	}
	if finErr := d.store.FinishRun(ctx, runID, status, toCommit, logBuf.String()); finErr != nil {
		return nil, finErr
	}
	return d.store.Run(ctx, runID)
}

func (d *Deployer) pipeline(ctx context.Context, p *Project, targetCommit string, logBuf io.Writer) (commit string, runErr error) {
	envVars, err := d.store.EnvMap(ctx, p.ID)
	if err != nil {
		return "", err
	}
	redacted := newSecretWriter(logBuf, envVars)
	logBuf = redacted
	defer func() {
		if err := redacted.Close(); err != nil && runErr == nil {
			runErr = err
		}
		if runErr != nil {
			runErr = fmt.Errorf("%s", redacted.sanitize(runErr.Error()))
		}
	}()
	if _, err := os.Stat(filepath.Join(p.RepoPath, ".git")); err != nil {
		return "", fmt.Errorf("%s is not a git repository", p.RepoPath)
	}

	fmt.Fprintf(logBuf, "$ git fetch --prune origin\n")
	if out, err := d.git(ctx, p.RepoPath, "fetch", "--prune", "origin"); err != nil {
		_, _ = io.WriteString(logBuf, out)
		return "", err
	} else {
		_, _ = io.WriteString(logBuf, out)
	}

	// A hard reset is deliberate: the working tree of a deploy target is
	// disposable, and a half-applied local edit is exactly what makes a
	// deployment non-reproducible.
	ref := "origin/" + p.Branch
	if targetCommit != "" {
		ref = targetCommit
	}
	fmt.Fprintf(logBuf, "$ git reset --hard %s\n", ref)
	out, err := d.git(ctx, p.RepoPath, "reset", "--hard", ref)
	_, _ = io.WriteString(logBuf, out)
	if err != nil {
		return "", err
	}

	sha := ""
	if out, err := d.git(ctx, p.RepoPath, "rev-parse", "HEAD"); err == nil {
		sha = strings.TrimSpace(out)
		fmt.Fprintf(logBuf, "now at %s\n", sha)
	}

	if len(envVars) > 0 {
		envPath := filepath.Join(p.RepoPath, ".env")
		if err := writeEnvFile(envPath, envVars); err != nil {
			return sha, fmt.Errorf("write .env: %w", err)
		}
		// Only the names are logged. The values are the reason this file is
		// mode 0600 and the reason they are sealed in the database.
		fmt.Fprintf(logBuf, "wrote %d variable(s) to .env: %s\n", len(envVars), strings.Join(sortedKeys(envVars), ", "))
	}

	if p.PreCommand != "" {
		fmt.Fprintf(logBuf, "$ %s\n", p.PreCommand)
		if err := d.shell(ctx, p, p.PreCommand, envVars, logBuf); err != nil {
			return sha, fmt.Errorf("pre-command failed: %w", err)
		}
	}

	composePath := filepath.Join(p.RepoPath, p.ComposeFile)
	if _, err := os.Stat(composePath); err == nil {
		fmt.Fprintf(logBuf, "$ docker compose -f %s up -d --build --remove-orphans\n", p.ComposeFile)
		if err := d.compose(ctx, p, envVars, logBuf); err != nil {
			return sha, err
		}
	} else {
		fmt.Fprintf(logBuf, "no compose file at %s, skipping container rebuild\n", composePath)
	}

	if p.PostCommand != "" {
		fmt.Fprintf(logBuf, "$ %s\n", p.PostCommand)
		if err := d.shell(ctx, p, p.PostCommand, envVars, logBuf); err != nil {
			return sha, fmt.Errorf("post-command failed: %w", err)
		}
	}
	return sha, nil
}

func (d *Deployer) git(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := hostexec.CommandInDir(ctx, dir, "git", args...)
	// Any credential prompt would hang the deployment forever; failing fast
	// with a clear git error is far better than a stuck run.
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/true",
		"GIT_SSH_COMMAND=ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new",
	)
	// A checked-out project is rarely owned by root. Running as its owner
	// avoids git's "dubious ownership" refusal — which would otherwise fail
	// every deployment of a normally-owned repository — and keeps the pulled
	// files owned by the account that runs the project.
	hostexec.AsOwner(cmd)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	_, err := hostexec.RunGroup(ctx, cmd, 5*time.Second)
	if err != nil {
		return buf.String(), fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(buf.String()))
	}
	return buf.String(), nil
}

func (d *Deployer) compose(ctx context.Context, p *Project, env map[string]string, logBuf io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	cmd := hostexec.CommandInDir(ctx, p.RepoPath, "docker", "compose",
		"-f", p.ComposeFile, "up", "-d", "--build", "--remove-orphans")
	cmd.Env = append(mergeEnv(env), "COMPOSE_PROGRESS=plain", "DOCKER_CLI_HINTS=false")
	cmd.Stdout = logBuf
	cmd.Stderr = logBuf
	if _, err := hostexec.RunGroup(ctx, cmd, 5*time.Second); err != nil {
		return fmt.Errorf("docker compose up failed: %w", err)
	}
	return nil
}

// shell runs an operator-configured hook command. It goes through sh -c
// intentionally: these are pipelines the operator wrote for their own project
// ("bun install && bun run build"), and they are stored by an admin, not
// supplied per request.
func (d *Deployer) shell(ctx context.Context, p *Project, command string, env map[string]string, logBuf io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Dir = p.RepoPath
	cmd.Env = mergeEnv(env)
	cmd.Stdout = logBuf
	cmd.Stderr = logBuf
	_, err := hostexec.RunGroup(ctx, cmd, 5*time.Second)
	return err
}

// mergeEnv builds the environment for a deploy child process.
//
// The dashboard's own JD_*/VPSD_* settings are stripped rather than inherited.
// The command being run is content the repository owner controls — a compose
// file, a pre/post hook — and it has no business reading the dashboard's
// configuration. main scrubs the master key at boot as well; this is the
// second half of the same rule, and it is the half that keeps working if a new
// secret-bearing variable is added later.
func mergeEnv(extra map[string]string) []string {
	out := make([]string, 0, len(os.Environ())+len(extra))
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "JD_") || strings.HasPrefix(kv, "VPSD_") {
			continue
		}
		out = append(out, kv)
	}
	for _, k := range sortedKeys(extra) {
		out = append(out, k+"="+extra[k])
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// writeEnvFile renders a dotenv file with values quoted so newlines and
// spaces survive, at mode 0600 because it holds the project's secrets.
func writeEnvFile(path string, vars map[string]string) error {
	var b strings.Builder
	b.WriteString("# Managed by just-dashboard — changes here are overwritten on deploy.\n")
	for _, k := range sortedKeys(vars) {
		v := vars[k]
		v = strings.ReplaceAll(v, `\`, `\\`)
		v = strings.ReplaceAll(v, `"`, `\"`)
		v = strings.ReplaceAll(v, "\n", `\n`)
		v = strings.ReplaceAll(v, "\r", `\r`)
		v = strings.ReplaceAll(v, "$", "$$")
		fmt.Fprintf(&b, "%s=\"%s\"\n", k, v)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".env-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// History lists recent commits so the rollback picker shows what it is
// rolling back to rather than bare hashes.
type Commit struct {
	SHA     string    `json:"sha"`
	Short   string    `json:"short"`
	Author  string    `json:"author"`
	Date    time.Time `json:"date"`
	Subject string    `json:"subject"`
}

func (d *Deployer) History(ctx context.Context, p *Project, limit int) ([]Commit, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	out, err := d.git(ctx, p.RepoPath, "log", "-n", fmt.Sprint(limit),
		"--pretty=format:%H%x1f%h%x1f%an%x1f%aI%x1f%s")
	if err != nil {
		return nil, err
	}
	commits := []Commit{}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, "\x1f")
		if len(parts) != 5 {
			continue
		}
		c := Commit{SHA: parts[0], Short: parts[1], Author: parts[2], Subject: parts[4]}
		if t, err := time.Parse(time.RFC3339, parts[3]); err == nil {
			c.Date = t.UTC()
		}
		commits = append(commits, c)
	}
	return commits, nil
}
