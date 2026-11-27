package stacks

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// CredentialStore keeps a stack's own secrets, sealed. The runner backs it
// with the dashboard's store; tests with a map.
type CredentialStore interface {
	Get(ctx context.Context, name string) (string, bool, error)
	Put(ctx context.Context, name, value string) error
	Delete(ctx context.Context, name string) error
}

// DockerEnv is Env over containers this process reaches on the stack's
// private network. Production builds one per configuration run from the live
// release; live tests build one over containers they started.
type DockerEnv struct {
	Docker *dockerx.Client
	// Network is the Docker network services are reached on, by its full
	// name ("<compose project>_internal").
	Network string
	// Container resolves a service to its container id, on every call.
	Container func(ctx context.Context, service string) (string, error)
	PresetID  string
	Running   map[string]bool
	// Options are the stack's options, already resolved to their values.
	Options map[string]string
	// Variables reads one of the deployment's own variables.
	Variables   func(ctx context.Context, name string) (string, error)
	Credentials CredentialStore
	// Lengths is each generated credential's length, from the manifest.
	Lengths map[string]int
	// Outputs are what earlier steps reported, by step then key.
	Outputs map[string]map[string]string
	// PublicURLs are the addresses the live release serves services at, by
	// service (PublicURL).
	PublicURLs map[string]string
	// Log receives each Logf line of a step run on this env directly, as the
	// live tests do. Under the runner a step's lines go to its own record
	// instead (stepEnv), so this is unset there.
	Log func(line string)

	clientOnce sync.Once
	client     *http.Client
	mu         sync.Mutex
	secrets    map[string]bool
}

var _ Env = (*DockerEnv)(nil)

func (e *DockerEnv) Preset() string { return e.PresetID }

func (e *DockerEnv) Has(service string) bool { return e.Running[service] }

func (e *DockerEnv) Option(name string) string { return e.Options[name] }

// BaseURL looks the container's address up on the stack's network. A
// service on no such network, or a container that is not running, is an
// error the runner retries: the container may be being recreated.
func (e *DockerEnv) BaseURL(ctx context.Context, service string, port int) (string, error) {
	if !e.Running[service] {
		return "", Permanent(fmt.Errorf("%s is not a service of this stack", service))
	}
	id, err := e.Container(ctx, service)
	if err != nil {
		return "", err
	}
	detail, err := e.Docker.Inspect(ctx, id)
	if err != nil {
		return "", fmt.Errorf("could not read %s's container: %w", service, err)
	}
	if detail.State != "running" {
		return "", fmt.Errorf("%s is %s, not running", service, detail.State)
	}
	for _, endpoint := range detail.NetworkList {
		if endpoint.Name != e.Network {
			continue
		}
		address := net.ParseIP(endpoint.IPAddress)
		if address == nil {
			return "", fmt.Errorf("%s has no address on %s yet", service, e.Network)
		}
		return "http://" + net.JoinHostPort(address.String(), strconv.Itoa(port)), nil
	}
	return "", fmt.Errorf("%s is not on the stack's network %s", service, e.Network)
}

// HTTP is a client for the stack's own addresses only: no proxy from the
// environment (an HTTP_PROXY on the host must not see the stack's API keys),
// bounded timeouts, and no redirect away from the address asked.
func (e *DockerEnv) HTTP() *http.Client {
	e.clientOnce.Do(func() { e.client = StackHTTPClient() })
	return e.client
}

// StackHTTPClient is the client every adapter request goes through.
func StackHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		ResponseHeaderTimeout: 60 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConnsPerHost:   2,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   2 * time.Minute,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if request.URL.Host != via[0].URL.Host {
				return fmt.Errorf("refused a redirect from %s to %s", via[0].URL.Host, request.URL.Host)
			}
			return nil
		},
	}
}

func (e *DockerEnv) Variable(ctx context.Context, name string) (string, error) {
	if e.Variables == nil {
		return "", fmt.Errorf("variable %s is not available", name)
	}
	value, err := e.Variables(ctx, name)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fmt.Errorf("the deployment has no value for %s", name)
	}
	e.mu.Lock()
	e.remember(value)
	e.mu.Unlock()
	return value, nil
}

// Credential returns the stored value, generating and storing one the first
// time. Two steps asking at once get the same value.
func (e *DockerEnv) Credential(ctx context.Context, name string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	value, found, err := e.Credentials.Get(ctx, name)
	if err != nil || found {
		e.remember(value)
		return value, err
	}
	length := e.Lengths[name]
	if length <= 0 {
		return "", Permanent(fmt.Errorf("credential %s is handed back by its application, not generated", name))
	}
	value, err = GeneratePassword(length)
	if err != nil {
		return "", err
	}
	if err := e.Credentials.Put(ctx, name, value); err != nil {
		return "", err
	}
	e.remember(value)
	return value, nil
}

func (e *DockerEnv) SetCredential(ctx context.Context, name, value string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.remember(value)
	return e.Credentials.Put(ctx, name, value)
}

func (e *DockerEnv) LookupCredential(ctx context.Context, name string) (string, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	value, found, err := e.Credentials.Get(ctx, name)
	e.remember(value)
	return value, found, err
}

// remember keeps a secret this env handed out, so Redact can take it out of
// anything recorded. Callers hold e.mu.
func (e *DockerEnv) remember(value string) {
	if len(value) < 6 {
		return
	}
	if e.secrets == nil {
		e.secrets = map[string]bool{}
	}
	e.secrets[value] = true
}

// Redact replaces every secret this env handed out with a mask: an
// application that echoes a request back in its error must not put an API key
// into the step's record.
func (e *DockerEnv) Redact(text string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	for secret := range e.secrets {
		text = strings.ReplaceAll(text, secret, "••••••")
	}
	return text
}

func (e *DockerEnv) DeleteCredential(ctx context.Context, name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.Credentials.Delete(ctx, name)
}

func (e *DockerEnv) PublicURL(service string) (string, bool) {
	address, found := e.PublicURLs[service]
	return address, found && address != ""
}

// NetworkSubnets reads each network the service's container is attached to.
func (e *DockerEnv) NetworkSubnets(ctx context.Context, service string) ([]string, error) {
	id, err := e.Container(ctx, service)
	if err != nil {
		return nil, err
	}
	detail, err := e.Docker.Inspect(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("could not read %s's container: %w", service, err)
	}
	var subnets []string
	for _, endpoint := range detail.NetworkList {
		network, err := e.Docker.InspectNetwork(ctx, endpoint.NetworkID)
		if err != nil {
			return nil, fmt.Errorf("could not read %s's network %s: %w", service, endpoint.Name, err)
		}
		for _, config := range network.IPAM.Config {
			if prefix, err := netip.ParsePrefix(config.Subnet); err == nil && !slices.Contains(subnets, prefix.Masked().String()) {
				subnets = append(subnets, prefix.Masked().String())
			}
		}
	}
	slices.Sort(subnets)
	if len(subnets) == 0 {
		return nil, fmt.Errorf("%s is on no network with an address range yet", service)
	}
	return subnets, nil
}

func (e *DockerEnv) Exec(ctx context.Context, service, user string, argv []string) (ExecResult, error) {
	id, err := e.Container(ctx, service)
	if err != nil {
		return ExecResult{ExitCode: -1}, err
	}
	// The step's own deadline bounds the command, not a fixed minute: a first
	// quality-profile sync can take longer than that on a slow server.
	timeout := time.Minute
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
		if timeout <= 0 {
			return ExecResult{ExitCode: -1}, context.DeadlineExceeded
		}
	}
	output, err := e.Docker.ExecCapture(ctx, id, user, argv, timeout)
	return ExecResult{Stdout: string(output.Stdout), Stderr: string(output.Stderr), ExitCode: output.ExitCode}, err
}

func (e *DockerEnv) Logs(ctx context.Context, service string, tail int) (string, error) {
	id, err := e.Container(ctx, service)
	if err != nil {
		return "", err
	}
	if tail <= 0 || tail > 5000 {
		tail = 500
	}
	lines, closer, err := e.Docker.Logs(ctx, id, dockerx.LogOptions{Tail: strconv.Itoa(tail), Stdout: true, Stderr: true})
	if err != nil {
		return "", err
	}
	defer closer.Close()
	var out strings.Builder
	for line := range lines {
		out.WriteString(line.Text)
		if !strings.HasSuffix(line.Text, "\n") {
			out.WriteByte('\n')
		}
	}
	return out.String(), nil
}

func (e *DockerEnv) ReadFile(ctx context.Context, service, path string) ([]byte, error) {
	id, err := e.Container(ctx, service)
	if err != nil {
		return nil, err
	}
	return e.Docker.ReadContainerFile(ctx, id, path)
}

func (e *DockerEnv) WriteFile(ctx context.Context, service, path string, content []byte, mode int64, uid, gid int) error {
	id, err := e.Container(ctx, service)
	if err != nil {
		return err
	}
	return e.Docker.WriteContainerFileOwned(ctx, id, path, content, mode, uid, gid)
}

func (e *DockerEnv) Output(step, key string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.Outputs[step][key]
}

// SetOutputs records a finished step's outputs for the steps after it.
func (e *DockerEnv) SetOutputs(step string, outputs map[string]string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Outputs == nil {
		e.Outputs = map[string]map[string]string{}
	}
	e.Outputs[step] = outputs
}

func (e *DockerEnv) Logf(format string, args ...any) {
	if e.Log != nil {
		e.Log(fmt.Sprintf(format, args...))
	}
}

const passwordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

// GeneratePassword is letters and digits only: every application in a stack
// accepts it in a form, a URL and a configuration file without escaping.
func GeneratePassword(length int) (string, error) {
	var out strings.Builder
	limit := big.NewInt(int64(len(passwordAlphabet)))
	for range length {
		index, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		out.WriteByte(passwordAlphabet[index.Int64()])
	}
	return out.String(), nil
}

// JoinURL appends a path and query to a base URL from BaseURL.
func JoinURL(base, path string, query url.Values) string {
	joined := strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
	if len(query) > 0 {
		joined += "?" + query.Encode()
	}
	return joined
}
