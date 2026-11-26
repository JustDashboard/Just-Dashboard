// Package stackstest is what adapter tests run a step against: a fake
// environment over httptest servers for unit tests, and the production
// DockerEnv over containers a live test started itself.
package stackstest

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
)

// Credentials is a CredentialStore in memory.
type Credentials struct {
	mu     sync.Mutex
	Values map[string]string
}

func (c *Credentials) Get(_ context.Context, name string) (string, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, found := c.Values[name]
	return value, found, nil
}

func (c *Credentials) Put(_ context.Context, name, value string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Values == nil {
		c.Values = map[string]string{}
	}
	c.Values[name] = value
	return nil
}

// Fake is an Env whose services are base URLs (httptest servers) and whose
// container operations are functions a test sets. Anything a test does not
// set fails loudly.
type Fake struct {
	PresetID    string
	Services    map[string]string // service → base URL
	OptionsMap  map[string]string
	Vars        map[string]string
	Creds       Credentials
	Lengths     map[string]int
	Outputs     map[string]map[string]string
	ExecFunc    func(service, user string, argv []string) (stacks.ExecResult, error)
	LogsFunc    func(service string) (string, error)
	Files       map[string][]byte // "service:path" → content
	Lines       []string
	mu          sync.Mutex
	writtenMode map[string]int64
}

var _ stacks.Env = (*Fake)(nil)

func (f *Fake) Preset() string { return f.PresetID }

func (f *Fake) Has(service string) bool { _, found := f.Services[service]; return found }

func (f *Fake) Option(name string) string { return f.OptionsMap[name] }

func (f *Fake) BaseURL(_ context.Context, service string, _ int) (string, error) {
	base, found := f.Services[service]
	if !found {
		return "", stacks.Permanent(fmt.Errorf("%s is not a service of this stack", service))
	}
	return base, nil
}

func (f *Fake) HTTP() *http.Client { return stacks.StackHTTPClient() }

func (f *Fake) Variable(_ context.Context, name string) (string, error) {
	value, found := f.Vars[name]
	if !found || value == "" {
		return "", fmt.Errorf("the deployment has no value for %s", name)
	}
	return value, nil
}

func (f *Fake) Credential(ctx context.Context, name string) (string, error) {
	if value, found, _ := f.Creds.Get(ctx, name); found {
		return value, nil
	}
	length := f.Lengths[name]
	if length == 0 {
		length = 24
	}
	value, err := stacks.GeneratePassword(length)
	if err != nil {
		return "", err
	}
	return value, f.Creds.Put(ctx, name, value)
}

func (f *Fake) SetCredential(ctx context.Context, name, value string) error {
	return f.Creds.Put(ctx, name, value)
}

func (f *Fake) LookupCredential(ctx context.Context, name string) (string, bool, error) {
	return f.Creds.Get(ctx, name)
}

func (f *Fake) Exec(_ context.Context, service, user string, argv []string) (stacks.ExecResult, error) {
	if f.ExecFunc == nil {
		return stacks.ExecResult{ExitCode: -1}, fmt.Errorf("unexpected exec in %s: %q", service, argv)
	}
	return f.ExecFunc(service, user, argv)
}

func (f *Fake) Logs(_ context.Context, service string, _ int) (string, error) {
	if f.LogsFunc == nil {
		return "", fmt.Errorf("unexpected logs read of %s", service)
	}
	return f.LogsFunc(service)
}

func (f *Fake) ReadFile(_ context.Context, service, path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	content, found := f.Files[service+":"+path]
	if !found {
		return nil, os.ErrNotExist
	}
	return content, nil
}

func (f *Fake) WriteFile(_ context.Context, service, path string, content []byte, mode int64, _, _ int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Files == nil {
		f.Files = map[string][]byte{}
	}
	if f.writtenMode == nil {
		f.writtenMode = map[string]int64{}
	}
	f.Files[service+":"+path] = append([]byte(nil), content...)
	f.writtenMode[service+":"+path] = mode
	return nil
}

func (f *Fake) Output(step, key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Outputs[step][key]
}

// SetOutputs records what a step reported, as the runner does.
func (f *Fake) SetOutputs(step string, outputs map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Outputs == nil {
		f.Outputs = map[string]map[string]string{}
	}
	f.Outputs[step] = outputs
}

func (f *Fake) Logf(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Lines = append(f.Lines, fmt.Sprintf(format, args...))
}

// Run runs a step against env as the runner would once: one attempt, its
// outputs recorded for the steps after it.
func Run(ctx context.Context, env interface {
	stacks.Env
	SetOutputs(string, map[string]string)
}, step stacks.Step) (stacks.Result, error) {
	result, err := step.Run(ctx, env)
	if err == nil {
		env.SetOutputs(step.ID, result.Outputs)
	}
	return result, err
}

// Live builds the production DockerEnv over containers a live test started,
// named by service. Live tests run only with JD_STACKS_LIVE=1, as root (built
// with go test -c and run under sudo), on a network the test created.
func Live(t *testing.T, manifest *stacks.Manifest, network string, containers map[string]string, preset string, options, variables map[string]string) *stacks.DockerEnv {
	t.Helper()
	if os.Getenv("JD_STACKS_LIVE") != "1" {
		t.Skip("set JD_STACKS_LIVE=1 to run against real containers")
	}
	docker := dockerx.New("unix:///var/run/docker.sock")
	t.Cleanup(func() { _ = docker.Close() })
	running := map[string]bool{}
	for service := range containers {
		running[service] = true
	}
	return &stacks.DockerEnv{
		Docker:  docker,
		Network: network,
		Container: func(_ context.Context, service string) (string, error) {
			name, found := containers[service]
			if !found {
				return "", fmt.Errorf("no container for %s", service)
			}
			return name, nil
		},
		PresetID: preset,
		Running:  running,
		Options:  options,
		Variables: func(_ context.Context, name string) (string, error) {
			return variables[name], nil
		},
		Credentials: &Credentials{},
		Lengths:     stacks.CredentialLengths(manifest),
		Log:         func(line string) { t.Log(strings.TrimSpace(line)) },
	}
}
