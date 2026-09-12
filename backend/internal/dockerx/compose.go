package dockerx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Compose stacks are discovered from the labels the compose plugin writes onto
// every container it creates, so a stack is visible whether or not its project
// directory is somewhere we were told to look.
const (
	labelProject    = "com.docker.compose.project"
	labelService    = "com.docker.compose.service"
	labelWorkingDir = "com.docker.compose.project.working_dir"
	labelConfigFile = "com.docker.compose.project.config_files"
)

type ComposeService struct {
	Name      string `json:"name"`
	Container string `json:"container"`
	State     string `json:"state"`
	Status    string `json:"status"`
	Image     string `json:"image"`
	// Health and Ports are carried so a stack card can answer "is it working"
	// and "where do I reach it" without opening every container in turn —
	// which is the whole reason somebody looks at a stack rather than at the
	// container table.
	Health string `json:"health,omitempty"`
	Ports  []Port `json:"ports"`
	// Missing marks a service the compose file declares that has no container
	// at all. Nothing else in Docker will ever mention it: `docker ps` cannot
	// list what does not exist, so a service that failed to create is
	// indistinguishable from one that was never written down.
	Missing bool `json:"missing,omitempty"`
}

type ComposeStack struct {
	Name        string           `json:"name"`
	WorkingDir  string           `json:"workingDir"`
	ConfigFiles []string         `json:"configFiles"`
	Services    []ComposeService `json:"services"`
	Running     int              `json:"running"`
	// Total is how many services the compose file declares — not how many
	// containers exist. Those are different questions, and answering the
	// second while labelling it the first is what produced "0/0 up" for a
	// stack that had never been deployed and "1/1 up" for one whose second
	// service had failed to create.
	Total   int  `json:"total"`
	Managed bool `json:"managed"`

	// Declared is the service list from the compose file, and DeclaredSource
	// says where it came from: "compose" is `docker compose config`, which
	// resolves includes, profiles and variables; "file" is a direct read of
	// the YAML, which is what a polling list can afford and does not resolve
	// any of that; "" means no file was reachable. The distinction is on the
	// wire because a count that might be wrong should say so.
	Declared       []string `json:"declared"`
	DeclaredSource string   `json:"declaredSource,omitempty"`

	// Containers is how many containers Docker holds for this project,
	// running or not. Deployed is whether that number is above zero — the
	// difference between "a compose file exists" and "this is a thing on the
	// server", which the overview and the stacks page used to count
	// differently and report as two different stack totals.
	Containers int  `json:"containers"`
	Deployed   bool `json:"deployed"`

	// Orphans are running containers labelled with this project that the
	// compose file does not declare. They are what a renamed or removed
	// service leaves behind, and nothing in Docker will ever mention them
	// again.
	Orphans []string `json:"orphans"`

	// State is the word, Summary the sentence. Both are computed here so
	// every surface that shows a stack says the same thing about it.
	State   StackState `json:"state"`
	Summary string     `json:"summary"`
}

// ListStacks groups running containers by compose project and then folds in
// any compose file found on disk under the configured roots, so a stack that
// is fully stopped still appears and can be brought up.
func (c *Client) ListStacks(ctx context.Context, roots []string) ([]ComposeStack, error) {
	// The general container listing rather than a filtered one of its own:
	// it already resolves health and uptime for everything running, which a
	// stack card needs and which a second query would have to inspect for all
	// over again.
	items, err := c.ListContainers(ctx, true)
	if err != nil {
		return nil, err
	}
	stacks := map[string]*ComposeStack{}
	for _, it := range items {
		project := it.Labels[labelProject]
		if project == "" {
			continue
		}
		st, ok := stacks[project]
		if !ok {
			st = &ComposeStack{
				Name:        project,
				WorkingDir:  it.Labels[labelWorkingDir],
				ConfigFiles: splitConfigFiles(it.Labels[labelConfigFile]),
				Services:    []ComposeService{},
			}
			stacks[project] = st
		}
		name := it.Labels[labelService]
		if name == "" {
			name = it.Name
		}
		ports := it.Ports
		if ports == nil {
			ports = []Port{}
		}
		st.Services = append(st.Services, ComposeService{
			Name:      name,
			Container: it.ID,
			State:     it.State,
			Status:    it.Status,
			Image:     it.Image,
			Health:    it.Health,
			Ports:     ports,
		})
		st.Containers++
		if it.State == "running" {
			st.Running++
		}
	}
	for _, found := range discoverComposeFiles(roots) {
		name := filepath.Base(found.dir)
		if st, ok := stacks[name]; ok {
			if st.WorkingDir == "" {
				st.WorkingDir = found.dir
			}
			if len(st.ConfigFiles) == 0 {
				st.ConfigFiles = []string{found.file}
			}
			continue
		}
		stacks[name] = &ComposeStack{
			Name:        name,
			WorkingDir:  found.dir,
			ConfigFiles: []string{found.file},
			Services:    []ComposeService{},
		}
	}
	out := make([]ComposeStack, 0, len(stacks))
	for _, st := range stacks {
		sort.Slice(st.Services, func(i, j int) bool { return st.Services[i].Name < st.Services[j].Name })
		// Only a stack whose compose file we can locate can be acted on;
		// the UI greys out up/down for the rest instead of failing later.
		st.Managed = st.WorkingDir != "" && dirExists(st.WorkingDir)
		resolveStackShape(st)
		out = append(out, *st)
	}
	// Running stacks first, then stopped ones, then compose files that have
	// never been up. Alphabetical alone put a directory of leftover test
	// compose files ahead of the services actually carrying traffic, purely
	// because of where their names fell — and the first screenful is the one
	// an operator reads when something is wrong.
	sort.Slice(out, func(i, j int) bool {
		if a, b := stackRank(out[i]), stackRank(out[j]); a != b {
			return a < b
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// stackRank orders a stack by how much it is currently doing.
func stackRank(st ComposeStack) int {
	switch {
	case st.Running > 0:
		return 0
	case st.Containers > 0:
		return 1
	default:
		return 2
	}
}

// resolveStackShape fills in what a stack *should* consist of, then says what
// it is doing.
//
// Reading the compose file directly rather than asking compose: this runs for
// every stack on a list that polls every fifteen seconds, and `docker compose
// config` is a subprocess each time. The stack detail asks compose properly
// and overwrites this with the better answer — see StackDetail — which is why
// DeclaredSource exists rather than the two silently disagreeing.
func resolveStackShape(st *ComposeStack) {
	if len(st.ConfigFiles) > 0 {
		if declared := declaredServicesFromFile(st.ConfigFiles); len(declared) > 0 {
			st.Declared = declared
			st.DeclaredSource = "file"
		}
	}
	if st.Declared == nil {
		st.Declared = []string{}
	}
	st.Orphans = orphanServices(st)
	st.Deployed = st.Containers > 0
	st.Total = len(st.Declared)
	if st.Total == 0 {
		// No file to count from. Reporting the container count as the total
		// would be claiming the file declares exactly what happens to be
		// running, which is precisely what was wrong before.
		st.Total = st.Containers
	}
	// A service the file declares with no container is invisible in Docker:
	// `docker ps` cannot list what does not exist, so a service that failed to
	// create looks the same as one that was never written down.
	present := map[string]bool{}
	for _, svc := range st.Services {
		present[svc.Name] = true
	}
	for _, name := range st.Declared {
		if !present[name] {
			st.Services = append(st.Services, ComposeService{Name: name, Missing: true, Ports: []Port{}})
		}
	}
	sort.Slice(st.Services, func(i, j int) bool { return st.Services[i].Name < st.Services[j].Name })
	st.State, st.Summary = describeStack(st)
}

// orphanServices names running containers the compose file does not declare.
func orphanServices(st *ComposeStack) []string {
	if len(st.Declared) == 0 {
		return []string{}
	}
	declared := map[string]bool{}
	for _, name := range st.Declared {
		declared[name] = true
	}
	out := []string{}
	for _, svc := range st.Services {
		if !svc.Missing && !declared[svc.Name] {
			out = append(out, svc.Name)
		}
	}
	sort.Strings(out)
	return out
}

func splitConfigFiles(v string) []string {
	if v == "" {
		return []string{}
	}
	return strings.Split(v, ",")
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

type composeFile struct{ dir, file string }

var composeNames = []string{
	"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml",
}

// discoverComposeFiles walks the configured roots shallowly. A full-depth walk
// of /home on a busy server is slow and would surface vendored fixtures, so
// the search stops three levels down.
func discoverComposeFiles(roots []string) []composeFile {
	found := []composeFile{}
	seen := map[string]bool{}
	for _, root := range roots {
		base := strings.Count(filepath.Clean(root), string(os.PathSeparator))
		filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if name == "node_modules" || name == ".git" || name == "vendor" {
					return filepath.SkipDir
				}
				if strings.Count(filepath.Clean(path), string(os.PathSeparator))-base > 3 {
					return filepath.SkipDir
				}
				return nil
			}
			for _, want := range composeNames {
				if d.Name() == want {
					dir := filepath.Dir(path)
					if !seen[dir] {
						seen[dir] = true
						found = append(found, composeFile{dir: dir, file: path})
					}
				}
			}
			return nil
		})
	}
	return found
}

type ComposeAction string

const (
	ComposeUp      ComposeAction = "up"
	ComposeDown    ComposeAction = "down"
	ComposeRestart ComposeAction = "restart"
	ComposePull    ComposeAction = "pull"
	ComposeStop    ComposeAction = "stop"
	ComposeStart   ComposeAction = "start"
)

type ComposeResult struct {
	Action   ComposeAction `json:"action"`
	Stack    string        `json:"stack"`
	ExitCode int           `json:"exitCode"`
	Output   string        `json:"output"`
	Duration string        `json:"duration"`
}

// RunCompose drives the compose plugin. Compose orchestration has no Engine
// API equivalent — the daemon knows nothing about projects — so this is the
// one place the package invokes a binary. The argument vector is built
// explicitly and never passed through a shell, and the project directory comes
// from the container labels rather than from user input.
func (c *Client) RunCompose(ctx context.Context, dir string, action ComposeAction, service string) (*ComposeResult, error) {
	if !dirExists(dir) {
		return nil, os.ErrNotExist
	}
	args := []string{"compose"}
	switch action {
	case ComposeUp:
		args = append(args, "up", "-d", "--remove-orphans")
	case ComposeDown:
		args = append(args, "down")
	case ComposeRestart:
		args = append(args, "restart")
	case ComposePull:
		args = append(args, "pull")
	case ComposeStop:
		args = append(args, "stop")
	case ComposeStart:
		args = append(args, "start")
	default:
		return nil, errUnknownAction(LifecycleAction(action))
	}
	if service != "" && action != ComposeDown {
		args = append(args, service)
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	start := time.Now()
	environment := append(os.Environ(), "COMPOSE_PROGRESS=plain", "DOCKER_CLI_HINTS=false")
	base, err := composePortBase(dir, environment)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	code, err := runComposePorts(ctx, dir, base, args[1:], environment, filepath.Join(dir, ".just-dashboard-ports.yml"), func(line LogLine) error { fmt.Fprintln(&buf, line.Text); return nil })
	res := &ComposeResult{Action: action, Stack: filepath.Base(dir), Output: buf.String(), Duration: time.Since(start).Round(time.Millisecond).String(), ExitCode: code}
	if err != nil {
		if res.ExitCode == 0 {
			res.ExitCode = -1
		}
		fmt.Fprintln(&buf, err)
		res.Output = buf.String()
	}
	return res, nil
}

// ReadComposeFile returns a stack's compose file for the config viewer.
func ReadComposeFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
