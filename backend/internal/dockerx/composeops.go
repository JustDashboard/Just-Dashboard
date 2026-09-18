package dockerx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Compose, as a thing you can watch happen.
//
// RunCompose in compose.go collects a command's output and returns it when the
// command finishes. For `docker compose stop` that is fine. For `up` on a
// stack that has to pull four images and build one, it is a POST that hangs
// for six minutes and then produces a wall of text — which is indistinguishable
// from a broken dashboard, and is why an operator who has been burned once
// goes back to ssh. Dockge got this right and it is most of why people like
// it: you press the button and you watch the same output you would have
// watched in a terminal.
//
// So this file adds the streaming runner, the editor's validation, and stack
// creation. What it does not do is invent a stack format: a stack here is a
// directory with a compose file in it, exactly what the compose CLI already
// understands, so everything done here is still doable from a shell and
// nothing is trapped inside the dashboard.

// ComposeStep is one command in an action. Two of the actions are sequences —
// "update" is a pull followed by an up — and the operator watching should see
// them as the two steps they are rather than as one long silence.
type ComposeStep struct {
	Label string
	Args  []string
}

// composeSteps maps an action to the commands that carry it out.
//
// `--remove-orphans` on up is deliberate: without it, renaming a service
// leaves the old container running forever, owned by a stack that no longer
// describes it, and nothing in any UI ever mentions it again.
func composeSteps(action ComposeAction, service string) ([]ComposeStep, error) {
	svc := func(args []string) []string {
		if service != "" {
			return append(args, service)
		}
		return args
	}
	switch action {
	case ComposeUp:
		return []ComposeStep{{"Starting", svc([]string{"up", "-d", "--remove-orphans"})}}, nil
	case ComposeDown:
		// Never per-service: `down` tears the project down, and passing it a
		// service name is silently accepted and means something else.
		return []ComposeStep{{"Stopping and removing", []string{"down", "--remove-orphans"}}}, nil
	case ComposeRestart:
		return []ComposeStep{{"Restarting", svc([]string{"restart"})}}, nil
	case ComposePull:
		return []ComposeStep{{"Pulling images", svc([]string{"pull"})}}, nil
	case ComposeStop:
		return []ComposeStep{{"Stopping", svc([]string{"stop"})}}, nil
	case ComposeStart:
		return []ComposeStep{{"Starting", svc([]string{"start"})}}, nil
	case ComposeBuild:
		return []ComposeStep{{"Building", svc([]string{"build", "--pull"})}}, nil
	case ComposeUpdate:
		// The action every self-hosted stack actually wants, and the one that
		// takes two commands and a paragraph of explanation everywhere else.
		// Pull first so a registry that is down leaves the running stack
		// alone rather than taking it half down and failing.
		return []ComposeStep{
			{"Pulling newer images", svc([]string{"pull"})},
			{"Recreating what changed", svc([]string{"up", "-d", "--remove-orphans"})},
		}, nil
	case ComposeRecreate:
		return []ComposeStep{{"Recreating", svc([]string{"up", "-d", "--force-recreate", "--remove-orphans"})}}, nil
	default:
		return nil, errUnknownAction(LifecycleAction(action))
	}
}

// The actions beyond the six compose.go already had.
const (
	ComposeBuild    ComposeAction = "build"
	ComposeUpdate   ComposeAction = "update"
	ComposeRecreate ComposeAction = "recreate"
)

// RunComposeStream runs an action and forwards every line as it appears.
//
// The returned exit code is that of the last step; a step that fails stops the
// sequence, because "pull failed, now recreating anyway" is the one thing an
// update must not do.
func (c *Client) RunComposeStream(ctx context.Context, dir string, action ComposeAction, service string, out chan<- LogLine) (int, error) {
	if !dirExists(dir) {
		return -1, os.ErrNotExist
	}
	steps, err := composeSteps(action, service)
	if err != nil {
		return -1, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	for i, step := range steps {
		if len(steps) > 1 {
			out <- LogLine{Stream: "status", Text: fmt.Sprintf("[%d/%d] %s", i+1, len(steps), step.Label)}
		}
		environment := append(os.Environ(), "COMPOSE_PROGRESS=plain", "DOCKER_CLI_HINTS=false", "BUILDKIT_PROGRESS=plain")
		base, err := composePortBase(dir, environment)
		if err != nil {
			return -1, err
		}
		code, err := runComposePorts(ctx, dir, base, step.Args, environment, filepath.Join(dir, ".just-dashboard-ports.yml"), func(line LogLine) error {
			select {
			case out <- line:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		if err != nil {
			return code, err
		}
		if code != 0 {
			return code, nil
		}
	}
	return 0, nil
}

// ComposeReleaseSpec is an immutable deployment-owned Compose invocation.
// Every source file and the generated image-pinning override is named
// explicitly; the dashboard process environment is never inherited for
// interpolation.
type ComposeReleaseSpec struct {
	ProjectName      string
	ProjectDirectory string
	Files            []string
	OverrideFile     string
	Environment      map[string]string
}

type ComposeReleaseAction string

const (
	ComposeReleaseUp    ComposeReleaseAction = "up"
	ComposeReleaseStart ComposeReleaseAction = "start"
	ComposeReleaseStop  ComposeReleaseAction = "stop"
	ComposeReleaseKill  ComposeReleaseAction = "kill"
	ComposeReleaseDown  ComposeReleaseAction = "down"
)

// RunComposeRelease invokes Compose with only reviewed, structured arguments.
// The temporary env file is removed before this method returns, including on
// cancellation and command failure.
func (c *Client) RunComposeRelease(
	ctx context.Context,
	spec ComposeReleaseSpec,
	action ComposeReleaseAction,
	graceSeconds int,
	emit func(LogLine) error,
) error {
	if !validComposeProjectName(spec.ProjectName) || !dirExists(spec.ProjectDirectory) ||
		len(spec.Files) == 0 || len(spec.Files) > 16 || graceSeconds < 0 || graceSeconds > 300 {
		return errors.New("invalid deployment Compose invocation")
	}
	args := []string{"compose", "--project-name", spec.ProjectName, "--project-directory", spec.ProjectDirectory}
	for _, configured := range spec.Files {
		path := configured
		if !filepath.IsAbs(path) {
			path = filepath.Join(spec.ProjectDirectory, filepath.Clean(path))
		}
		if !containedComposeFile(spec.ProjectDirectory, path) {
			return errors.New("deployment Compose file escapes its workspace")
		}
		args = append(args, "-f", path)
	}
	if spec.OverrideFile == "" || !containedComposeFile(spec.ProjectDirectory, spec.OverrideFile) {
		return errors.New("deployment Compose override is outside its workspace")
	}
	args = append(args, "-f", spec.OverrideFile)

	envFile, err := writeComposeReleaseEnv(spec.ProjectDirectory, spec.Environment)
	if err != nil {
		return err
	}
	defer os.Remove(envFile)
	args = append(args, "--env-file", envFile)
	base := append([]string{}, args...)
	args = nil
	switch action {
	case ComposeReleaseUp:
		args = append(args, "up", "-d", "--no-build", "--remove-orphans")
	case ComposeReleaseStart:
		args = append(args, "start")
	case ComposeReleaseStop:
		args = append(args, "stop", "--timeout", strconv.Itoa(graceSeconds))
	case ComposeReleaseKill:
		args = append(args, "kill", "--signal", "SIGKILL")
	case ComposeReleaseDown:
		args = append(args, "down", "--remove-orphans", "--timeout", strconv.Itoa(graceSeconds))
	default:
		return errors.New("unknown deployment Compose action")
	}

	commandCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	code, runErr := runComposePorts(commandCtx, spec.ProjectDirectory, base, args,
		composeReleaseProcessEnvironment(c.host, spec.ProjectDirectory), spec.OverrideFile+".ports.yml", emit)
	if runErr != nil {
		return runErr
	}
	if code != 0 {
		return fmt.Errorf("docker compose exited with status %d", code)
	}
	return nil
}

func validComposeProjectName(value string) bool {
	if value == "" || len(value) > 63 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') ||
			(index > 0 && (character == '-' || character == '_')) {
			continue
		}
		return false
	}
	return true
}

func containedComposeFile(root, candidate string) bool {
	root, candidate = filepath.Clean(root), filepath.Clean(candidate)
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != "." && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func writeComposeReleaseEnv(directory string, values map[string]string) (string, error) {
	keys := make([]string, 0, len(values))
	for key := range values {
		if !validComposeEnvironmentName(key) {
			return "", errors.New("invalid deployment Compose environment name")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var contents strings.Builder
	contents.WriteString("# temporary Just Dashboard release environment\n")
	for _, key := range keys {
		contents.WriteString(key)
		contents.WriteByte('=')
		contents.WriteString(strconv.Quote(values[key]))
		contents.WriteByte('\n')
	}
	file, err := os.CreateTemp(directory, ".just-dashboard-env-*")
	if err != nil {
		return "", err
	}
	name := file.Name()
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		os.Remove(name)
		return "", err
	}
	if _, err := file.WriteString(contents.String()); err != nil {
		file.Close()
		os.Remove(name)
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

func composeReleaseProcessEnvironment(host, directory string) []string {
	environment := []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + directory,
		"DOCKER_CONFIG=" + filepath.Join(directory, ".docker-empty"),
		"COMPOSE_PROGRESS=plain", "DOCKER_CLI_HINTS=false", "BUILDKIT_PROGRESS=plain",
	}
	if host != "" {
		environment = append(environment, "DOCKER_HOST="+host)
	}
	return environment
}

// ComposeFileFor picks the file to edit for a stack.
//
// Compose supports several names and an override file layered on top. The
// editor deliberately opens only the first: an operator who has a
// docker-compose.override.yml knows what it is and can open it through the
// file manager, and a UI that silently merged the two would show a file that
// exists nowhere on disk.
func ComposeFileFor(stack *ComposeStack) (string, error) {
	if stack == nil {
		return "", os.ErrNotExist
	}
	for _, f := range stack.ConfigFiles {
		if f != "" {
			return f, nil
		}
	}
	if stack.WorkingDir == "" {
		return "", os.ErrNotExist
	}
	for _, name := range composeNames {
		candidate := filepath.Join(stack.WorkingDir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", os.ErrNotExist
}

// ComposeValidation is the result of asking compose whether a file makes
// sense, before it is written over one that currently works.
type ComposeValidation struct {
	Valid bool   `json:"valid"`
	Error string `json:"error,omitempty"`
	// Normalised is the file as compose understands it: defaults filled in,
	// variables substituted, `extends` resolved. Useful next to the source
	// when a value is not what the author expected.
	Normalised string `json:"normalised,omitempty"`
	// Services is what the file would create, which is the check an operator
	// actually wants — "did my edit accidentally delete a service".
	Services []string `json:"services"`
}

// ComposeInput is one file in an ordered Compose configuration. Paths are
// relative labels used only inside a temporary validation workspace.
type ComposeInput struct {
	Path    string
	Content string
}

// ValidateComposePlan validates untrusted deployment-planning input without
// inheriting dashboard variables or loading a project .env file. Variable
// names discovered by the structural parser receive inert placeholders; no
// deployment secret enters this subprocess or its output.
func (c *Client) ValidateComposePlan(
	ctx context.Context,
	dir string,
	inputs []ComposeInput,
	variables []string,
) (*ComposeValidation, error) {
	if len(inputs) == 0 || len(inputs) > 16 {
		return &ComposeValidation{Valid: false, Error: "between 1 and 16 Compose files are required", Services: []string{}}, nil
	}
	temporary, err := os.MkdirTemp("", "just-dashboard-compose-plan-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temporary)
	if err := os.Chmod(temporary, 0o700); err != nil {
		return nil, err
	}

	projectDirectory := dir
	if projectDirectory == "" || projectDirectory == "." {
		projectDirectory = temporary
	}
	envPath := filepath.Join(temporary, "planning.env")
	if err := os.WriteFile(envPath, []byte("# isolated deployment planning environment\n"), 0o600); err != nil {
		return nil, err
	}
	args := []string{"compose", "--project-directory", projectDirectory, "--env-file", envPath}
	total := 0
	seenPaths := map[string]bool{}
	for index, input := range inputs {
		total += len(input.Content)
		clean := filepath.Clean(input.Path)
		if total > 4<<20 || clean == "." || filepath.IsAbs(clean) || clean == ".." ||
			strings.HasPrefix(clean, ".."+string(filepath.Separator)) || seenPaths[clean] {
			return &ComposeValidation{Valid: false, Error: "Compose input path or size is invalid", Services: []string{}}, nil
		}
		seenPaths[clean] = true
		path := filepath.Join(temporary, strconv.Itoa(index)+"-"+filepath.Base(clean))
		if err := os.WriteFile(path, []byte(input.Content), 0o600); err != nil {
			return nil, err
		}
		args = append(args, "-f", path)
	}

	environment := []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + temporary, "DOCKER_CONFIG=" + filepath.Join(temporary, "docker-config"),
		"COMPOSE_PROGRESS=plain", "DOCKER_CLI_HINTS=false", "BUILDKIT_PROGRESS=plain",
	}
	seenVariables := map[string]bool{}
	for _, variable := range variables {
		if !validComposeEnvironmentName(variable) || seenVariables[variable] {
			return &ComposeValidation{Valid: false, Error: "Compose variable name is invalid", Services: []string{}}, nil
		}
		seenVariables[variable] = true
		environment = append(environment, variable+"=just-dashboard-planning-placeholder")
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	run := func(suffix ...string) (string, string, error) {
		cmd := exec.CommandContext(ctx, "docker", append(args, suffix...)...)
		cmd.Dir = projectDirectory
		cmd.Env = environment
		stdout, stderr := &boundedComposeBuffer{limit: 8 << 20}, &boundedComposeBuffer{limit: 1 << 20}
		cmd.Stdout, cmd.Stderr = stdout, stderr
		err := cmd.Run()
		return stdout.String(), stderr.String(), err
	}
	if _, stderr, err := run("config", "--quiet"); err != nil {
		return &ComposeValidation{Valid: false, Error: cleanComposeError(stderr, err), Services: []string{}}, nil
	}
	validation := &ComposeValidation{Valid: true, Services: []string{}}
	if names, _, err := run("config", "--services"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(names), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				validation.Services = append(validation.Services, line)
			}
		}
	}
	return validation, nil
}

func validComposeEnvironmentName(value string) bool {
	if value == "" || !((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z') || value[0] == '_') {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if !((character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') || character == '_') {
			return false
		}
	}
	return true
}

type boundedComposeBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedComposeBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := b.limit - b.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = b.Buffer.Write(value)
	}
	return original, nil
}

// ValidateCompose runs the candidate content through the compose parser
// without writing it anywhere.
//
// The file is fed on stdin, so a syntax error never touches disk and a valid
// edit is never half-written. `--project-directory` keeps relative paths and
// the `.env` file resolving against the real stack directory, which they would
// not do for a file compose thinks came from nowhere.
func (c *Client) ValidateCompose(ctx context.Context, dir, content string) (*ComposeValidation, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	run := func(args ...string) (string, string, error) {
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Dir = dir
		cmd.Stdin = strings.NewReader(content)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stdout.String(), stderr.String(), err
	}

	base := []string{"compose", "--project-directory", dir, "-f", "-"}
	out, errOut, err := run(append(base, "config", "--quiet")...)
	if err != nil {
		return &ComposeValidation{Valid: false, Error: cleanComposeError(errOut, err), Services: []string{}}, nil
	}
	v := &ComposeValidation{Valid: true, Services: []string{}}
	if names, _, err := run(append(base, "config", "--services")...); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(names), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				v.Services = append(v.Services, line)
			}
		}
	}
	if full, _, err := run(append(base, "config")...); err == nil {
		v.Normalised = full
	}
	_ = out
	return v, nil
}

// ValidateComposeFiles asks Compose to merge and validate an ordered set of
// files. Candidate content is written only to a private temporary directory
// and removed before this call returns; the source tree is never changed.
func (c *Client) ValidateComposeFiles(ctx context.Context, dir string, inputs []ComposeInput) (*ComposeValidation, error) {
	if len(inputs) == 1 {
		return c.ValidateCompose(ctx, dir, inputs[0].Content)
	}
	if len(inputs) == 0 || len(inputs) > 16 {
		return &ComposeValidation{Valid: false, Error: "between 1 and 16 Compose files are required", Services: []string{}}, nil
	}
	temporary, err := os.MkdirTemp("", "just-dashboard-compose-validate-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temporary)

	args := []string{"compose", "--project-directory", dir}
	total := 0
	for index, input := range inputs {
		total += len(input.Content)
		if total > 4<<20 {
			return &ComposeValidation{Valid: false, Error: "Compose input exceeds 4 MiB", Services: []string{}}, nil
		}
		clean := filepath.Clean(input.Path)
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return &ComposeValidation{Valid: false, Error: "Compose file path escapes the validation workspace", Services: []string{}}, nil
		}
		path := filepath.Join(temporary, filepath.Base(clean))
		if index > 0 {
			path = filepath.Join(temporary, strconv.Itoa(index)+"-"+filepath.Base(clean))
		}
		if err := os.WriteFile(path, []byte(input.Content), 0o600); err != nil {
			return nil, err
		}
		args = append(args, "-f", path)
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	run := func(suffix ...string) (string, string, error) {
		cmd := exec.CommandContext(ctx, "docker", append(args, suffix...)...)
		cmd.Dir = dir
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stdout.String(), stderr.String(), err
	}
	if _, stderr, err := run("config", "--quiet"); err != nil {
		return &ComposeValidation{Valid: false, Error: cleanComposeError(stderr, err), Services: []string{}}, nil
	}
	validation := &ComposeValidation{Valid: true, Services: []string{}}
	if names, _, err := run("config", "--services"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(names), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				validation.Services = append(validation.Services, line)
			}
		}
	}
	if full, _, err := run("config"); err == nil {
		validation.Normalised = full
	}
	return validation, nil
}

// cleanComposeError trims compose's error output to the part that names the
// problem. The full text repeats the command and the file path, which the
// operator is looking at.
func cleanComposeError(stderr string, err error) string {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		return err.Error()
	}
	lines := []string{}
	for _, line := range strings.Split(msg, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "time=") {
			continue
		}
		lines = append(lines, strings.TrimPrefix(line, "validating "))
	}
	if len(lines) == 0 {
		return err.Error()
	}
	return strings.Join(lines, "\n")
}

var ErrComposeUnavailable = errors.New("the docker compose plugin is not installed on this host")

// ComposeAvailable reports whether the plugin exists, so the UI can say so
// once rather than offering buttons that all fail the same way.
func (c *Client) ComposeAvailable(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "compose", "version", "--short")
	return cmd.Run() == nil
}

// WriteComposeFile replaces a stack's compose file, keeping the previous
// content beside it.
//
// The backup is one file, overwritten each time, named so it is obviously not
// a compose file itself — compose globs for specific names, and a
// `compose.yaml.bak` is ignored by the plugin while being one `mv` away from
// undoing a bad edit. It exists because the failure this guards against is
// not a syntax error (validation catches those) but a correct file that says
// the wrong thing, which is only discovered after the stack comes back up
// wrong.
func WriteComposeFile(path, content string) error {
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		if previous, err := os.ReadFile(path); err == nil {
			_ = os.WriteFile(path+".bak", previous, info.Mode().Perm())
		}
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	// Written through a temporary file in the same directory and renamed, so
	// a crash mid-write cannot leave compose with half a file.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".compose-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// NewStack creates a stack directory with a compose file in it.
//
// `dir` has already been checked against the configured roots by the caller.
// The file is named compose.yaml — the current spelling, and the one the
// compose documentation uses — rather than docker-compose.yml, which is the
// historical name kept working for compatibility.
func NewStack(dir, content string) (string, error) {
	if content == "" {
		return "", errors.New("a compose file is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		for _, name := range composeNames {
			if e.Name() == name {
				return "", fmt.Errorf("%s already has a compose file (%s)", dir, name)
			}
		}
	}
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// StarterCompose is what a new stack starts as.
//
// A blank editor is the least useful thing to show somebody who has just
// clicked "new stack": the format is the part they do not know. This is a
// working file with the four keys that matter, each one explained where the
// explanation belongs — in the file they are about to edit, not in a
// documentation page in another tab.
func StarterCompose(name string) string {
	if name == "" {
		name = "app"
	}
	return `# ` + name + `
#
# Each entry under "services" is one container. Compose starts them together,
# puts them on a private network where they can reach each other by these
# names, and restarts them in the right order.

services:
  ` + name + `:
    image: nginx:alpine
    # "unless-stopped" brings it back when the server reboots, but leaves it
    # down if you deliberately stopped it.
    restart: unless-stopped
    ports:
      # host:container. Binding to 127.0.0.1 keeps it reachable only from this
      # server — put anything public behind the reverse proxy instead.
      - "127.0.0.1:8080:80"
    # environment:
    #   TZ: Europe/London
    # volumes:
    #   # A named volume survives the container being recreated.
    #   - ` + name + `-data:/usr/share/nginx/html

# volumes:
#   ` + name + `-data:
`
}

// DeclaredServices asks the compose file what services it defines.
//
// This is the one fact the container list cannot supply: a service that is in
// the file and has no container at all is invisible everywhere else in Docker,
// and it is exactly what "I deployed it and nothing happened" looks like. Read
// on demand rather than in the stack list, because it costs a subprocess per
// stack and the list polls.
func (c *Client) DeclaredServices(ctx context.Context, dir string) ([]string, error) {
	if !dirExists(dir) {
		return nil, os.ErrNotExist
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "compose", "config", "--services")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, errors.New(cleanComposeError(stderr.String(), err))
	}
	out := []string{}
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out, nil
}
