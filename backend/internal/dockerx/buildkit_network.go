package dockerx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/docker/docker/errdefs"
)

// NetworkedBuilderImage is the BuildKit daemon a build runs in when it has to
// reach a database on its environment's network. Docker's own builder cannot
// join a user-defined network ("network mode not supported by buildkit"), so
// such a build gets a builder container of its own on that network. Pinned by
// digest, because whatever this image is runs every such build.
const NetworkedBuilderImage = "moby/buildkit:v0.34.2@sha256:c5f8862f52499773af85bc07695b2379ef08e8002352b0df2c4864c91d78dabe"

// networkedBuilderConfig is the daemon's configuration. Build steps share the
// daemon's network namespace (--oci-worker-net=host, which here is the builder
// container's namespace, never the server's), and BuildKit drops a loopback
// resolver from the resolv.conf it hands a step, which leaves a step unable to
// ask Docker's embedded DNS for db-N.jd.internal. Naming that resolver here
// keeps it: 127.0.0.11 answers the network's aliases and forwards every other
// name to the server's own resolver.
const networkedBuilderConfig = "# Written by Just Dashboard for builds that reach a database on their environment's network.\n" +
	"[dns]\n  nameservers = [\"127.0.0.11\"]\n  options = [\"ndots:0\"]\n"

const networkedBuilderConfigName = "networked-buildkitd.toml"

var networkedBuilderNameRE = regexp.MustCompile(`^jd-build-[a-z0-9][a-z0-9_.-]{0,100}$`)

// NetworkedBuilderName is the builder the builds that reach an environment's
// databases run on, named after that environment's database network
// (jd-e<environment>-db-<owner key>): one per environment, so two
// environments' builds never share a network namespace, and with the
// network's random owner key in it, so another store on the same daemon — a
// live test's — never names this install's builder.
func NetworkedBuilderName(network string) string {
	return "jd-build-" + strings.TrimPrefix(strings.ToLower(network), "jd-")
}

// networkedBuilderContainer is the container buildx's docker-container driver
// runs the builder's first node in.
func networkedBuilderContainer(name string) string { return "buildx_buildkit_" + name + "0" }

// NetworkedBuilderStateVolume is where that builder keeps its cache. It
// outlives the builder's container, which is removed after every build, and
// goes with the environment.
func NetworkedBuilderStateVolume(name string) string {
	return networkedBuilderContainer(name) + "_state"
}

// NetworkedBuilderSpec is the builder to start: its name, the networks its
// build steps join (the first is the one the daemon starts on) and a folder
// the docker CLI reads by the same path as this process, for its
// configuration file.
type NetworkedBuilderSpec struct {
	Name      string
	Networks  []string
	ConfigDir string
}

// NetworkedBuilder is a builder running on an environment's networks. Close
// hands it back once the build is done: it stays running for
// NetworkedBuilderIdle, so the environment's next build — a fix pushed a
// minute later — starts on it at once instead of creating it again (5 to 30
// seconds, more on a busy server), and is removed after that. Its state
// volume, the cache, outlives the container either way.
//
// While it waits it is a BuildKit daemon with no build running, which
// listens on no port of the network: the docker CLI reaches it through the
// container itself. Removing the environment's network removes it first
// (RemoveNetworkedBuilderState), as before; the next build that starts on it
// matches its networks to what that build may reach, so a database unlinked
// meanwhile is not reachable from it. One a stopped dashboard left running is
// not trusted: the next build removes and creates it again.
type NetworkedBuilder struct {
	Name     string
	Networks []string
	// Reused says the build starts on the builder an earlier build left
	// waiting, rather than one created for it.
	Reused  bool
	client  *Client
	release func()
	once    sync.Once
}

// NetworkedBuilderIdle is how long a networked builder waits for its
// environment's next build before it is removed. Zero removes it as soon as
// its build is done.
var NetworkedBuilderIdle = 15 * time.Minute

// waitingBuilder is a builder this process left running after a build, with
// what that build had it join and the timer that removes it.
type waitingBuilder struct {
	networks []string
	timer    *time.Timer
}

// waitingBuilders are the builders this process left running, by name. Only
// these are started on again; any other running under the name is removed.
var waitingBuilders = struct {
	sync.Mutex
	byName map[string]*waitingBuilder
}{byName: map[string]*waitingBuilder{}}

// takeWaitingBuilder claims the builder waiting under name, if any, and
// stops its removal. The caller holds the builder's lock.
func takeWaitingBuilder(name string) *waitingBuilder {
	waitingBuilders.Lock()
	defer waitingBuilders.Unlock()
	waiting := waitingBuilders.byName[name]
	if waiting != nil {
		delete(waitingBuilders.byName, name)
		waiting.timer.Stop()
	}
	return waiting
}

// networkedBuilderLocks serialises the builds of one environment's builder:
// two runs share its name, and the second must not remove the first's
// container from under it.
var networkedBuilderLocks sync.Map

func networkedBuilderLock(ctx context.Context, name string) (func(), error) {
	slot := networkedBuilderSlot(name)
	select {
	case slot <- struct{}{}:
		return func() { <-slot }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func networkedBuilderSlot(name string) chan struct{} {
	value, _ := networkedBuilderLocks.LoadOrStore(name, make(chan struct{}, 1))
	return value.(chan struct{})
}

// networkedBuilderTryLock takes the builder's lock only if nobody holds it:
// the removal of a waiting builder gives way to a build, whose own Close
// leaves the builder waiting again.
func networkedBuilderTryLock(name string) (func(), bool) {
	slot := networkedBuilderSlot(name)
	select {
	case slot <- struct{}{}:
		return func() { <-slot }, true
	default:
		return nil, false
	}
}

var networkNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// NetworkedBuilderCreateArgs is the buildx command that creates the builder,
// exported as a hermetic transcript boundary.
func NetworkedBuilderCreateArgs(spec NetworkedBuilderSpec, configPath string) ([]string, error) {
	if !networkedBuilderNameRE.MatchString(spec.Name) {
		return nil, fmt.Errorf("invalid networked builder name")
	}
	if len(spec.Networks) == 0 {
		return nil, fmt.Errorf("a networked builder needs a network")
	}
	for _, network := range spec.Networks {
		if !networkNameRE.MatchString(network) {
			return nil, fmt.Errorf("invalid builder network name")
		}
	}
	return []string{
		"buildx", "create", "--name", spec.Name, "--driver", "docker-container",
		"--driver-opt", "network=" + spec.Networks[0],
		"--driver-opt", "image=" + NetworkedBuilderImage,
		// The default builder keeps a step's whole log; this one would cut
		// it at 2 MiB, and a build's failure is in its last lines.
		"--driver-opt", "env.BUILDKIT_STEP_LOG_MAX_SIZE=-1",
		"--driver-opt", "env.BUILDKIT_STEP_LOG_MAX_SPEED=-1",
		"--buildkitd-flags", "--oci-worker-net=host",
		"--buildkitd-config", configPath,
		"--bootstrap",
	}, nil
}

// StartNetworkedBuilder starts the builder an environment's database-reading
// builds run on, on the given networks: the one an earlier build left
// waiting, its networks matched to these, or a new one. A builder a stopped
// dashboard left behind is removed first, its cache kept.
func (c *Client) StartNetworkedBuilder(ctx context.Context, spec NetworkedBuilderSpec) (*NetworkedBuilder, error) {
	if c == nil {
		return nil, ErrUnavailable
	}
	if strings.TrimSpace(spec.ConfigDir) == "" {
		return nil, fmt.Errorf("a networked builder needs a configuration folder")
	}
	configPath := filepath.Join(spec.ConfigDir, networkedBuilderConfigName)
	args, err := NetworkedBuilderCreateArgs(spec, configPath)
	if err != nil {
		return nil, err
	}
	release, err := networkedBuilderLock(ctx, spec.Name)
	if err != nil {
		return nil, err
	}
	builder := &NetworkedBuilder{Name: spec.Name, Networks: append([]string(nil), spec.Networks...), client: c, release: release}
	fail := func(err error) (*NetworkedBuilder, error) {
		builder.discard(context.WithoutCancel(ctx))
		return nil, err
	}
	if err := writeNetworkedBuilderConfig(spec.ConfigDir, configPath); err != nil {
		return fail(err)
	}
	if waiting := takeWaitingBuilder(spec.Name); waiting != nil {
		if c.matchWaitingBuilder(ctx, spec) == nil {
			builder.Reused = true
			return builder, nil
		}
	}
	c.removeNetworkedBuilder(ctx, spec.Name)
	createCtx, cancel := context.WithTimeout(ctx, networkedBuilderStartTimeout)
	defer cancel()
	cmd := hostexec.Command(createCtx, "docker", args...)
	cmd.Env = append(scrubBuildEnvironment(os.Environ()), "DOCKER_CLI_HINTS=false")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fail(fmt.Errorf("%w: %s", ErrNetworkedBuilder, boundedCommandOutput(output, err)))
	}
	for _, network := range spec.Networks[1:] {
		if err := c.ConnectNetwork(ctx, network, networkedBuilderContainer(spec.Name), nil); err != nil {
			return fail(fmt.Errorf("%w: join network %s: %v", ErrNetworkedBuilder, network, err))
		}
	}
	return builder, nil
}

// ErrNetworkedBuilder is a builder on an environment's network that could not
// be started.
var ErrNetworkedBuilder = errors.New("the builder on the environment's network could not start")

// networkedBuilderStartTimeout covers pulling the BuildKit image the first
// time and the daemon's start.
const networkedBuilderStartTimeout = 5 * time.Minute

// matchWaitingBuilder checks that a waiting builder still runs, and joins
// it to the build's networks and takes it off any other, so it reaches
// exactly what this build may. An error leaves it to be created again.
func (c *Client) matchWaitingBuilder(ctx context.Context, spec NetworkedBuilderSpec) error {
	container := networkedBuilderContainer(spec.Name)
	detail, err := c.Inspect(ctx, container)
	if err != nil {
		return err
	}
	if detail.State != "running" {
		return fmt.Errorf("the waiting builder is %s", detail.State)
	}
	add, remove := networkedBuilderNetworkChanges(detail.Networks, spec.Networks)
	for _, network := range remove {
		if err := c.DisconnectNetwork(ctx, network, container, true); err != nil && !errdefs.IsNotFound(err) {
			return err
		}
	}
	for _, network := range add {
		if err := c.ConnectNetwork(ctx, network, container, nil); err != nil {
			return err
		}
	}
	return nil
}

// networkedBuilderNetworkChanges are the networks a waiting builder joins
// and leaves to be on exactly want.
func networkedBuilderNetworkChanges(have, want []string) (add, remove []string) {
	for _, network := range want {
		if !slices.Contains(have, network) {
			add = append(add, network)
		}
	}
	for _, network := range have {
		if !slices.Contains(want, network) {
			remove = append(remove, network)
		}
	}
	return add, remove
}

// Close hands the builder back after its build: it waits for the
// environment's next build for NetworkedBuilderIdle, then is removed with
// its cache kept.
func (b *NetworkedBuilder) Close(ctx context.Context) {
	if b == nil {
		return
	}
	idle := NetworkedBuilderIdle
	if idle <= 0 || b.client == nil {
		b.discard(ctx)
		return
	}
	b.once.Do(func() {
		client, name := b.client, b.Name
		waiting := &waitingBuilder{networks: append([]string(nil), b.Networks...)}
		waitingBuilders.Lock()
		waiting.timer = time.AfterFunc(idle, func() { client.removeWaitingBuilder(name, waiting) })
		waitingBuilders.byName[name] = waiting
		waitingBuilders.Unlock()
		if b.release != nil {
			b.release()
		}
	})
}

// discard removes the builder's container now and keeps its cache.
func (b *NetworkedBuilder) discard(ctx context.Context) {
	if b == nil {
		return
	}
	b.once.Do(func() {
		if b.client != nil {
			b.client.removeNetworkedBuilder(ctx, b.Name)
		}
		if b.release != nil {
			b.release()
		}
	})
}

// removeWaitingBuilder removes a builder whose wait is over, unless a build
// has claimed it since. One whose lock is held — the Close that left it
// waiting has not let go yet, or the environment's networks are being
// removed — is looked at again a minute later.
func (c *Client) removeWaitingBuilder(name string, waiting *waitingBuilder) {
	release, ok := networkedBuilderTryLock(name)
	if !ok {
		waitingBuilders.Lock()
		if waitingBuilders.byName[name] == waiting {
			waiting.timer.Reset(min(NetworkedBuilderIdle, time.Minute))
		}
		waitingBuilders.Unlock()
		return
	}
	defer release()
	waitingBuilders.Lock()
	current := waitingBuilders.byName[name]
	if current == waiting {
		delete(waitingBuilders.byName, name)
	}
	waitingBuilders.Unlock()
	if current != waiting {
		return
	}
	c.removeNetworkedBuilder(context.Background(), name)
}

// removeNetworkedBuilder removes the builder and its container, keeping the
// state volume. Either may already be gone; what is left is whatever a
// stopped dashboard could not finish, and the next start removes it again.
func (c *Client) removeNetworkedBuilder(ctx context.Context, name string) {
	removeCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := hostexec.Command(removeCtx, "docker", "buildx", "rm", "--keep-state", "--force", name)
	cmd.Env = append(scrubBuildEnvironment(os.Environ()), "DOCKER_CLI_HINTS=false")
	_ = cmd.Run()
	// buildx forgets a builder whose container it cannot find, and a
	// container whose builder it has forgotten would refuse the next create.
	if err := c.RemoveContainer(removeCtx, networkedBuilderContainer(name), true, false); err != nil && !errdefs.IsNotFound(err) {
		return
	}
}

// RemoveNetworkedBuilderState removes an environment's builder and its
// cache, when the environment itself goes.
func (c *Client) RemoveNetworkedBuilderState(ctx context.Context, name string) error {
	if c == nil {
		return ErrUnavailable
	}
	if !networkedBuilderNameRE.MatchString(name) {
		return fmt.Errorf("invalid networked builder name")
	}
	release, err := networkedBuilderLock(ctx, name)
	if err != nil {
		return err
	}
	defer release()
	takeWaitingBuilder(name)
	// Only what the daemon says is there is removed: the buildx CLI talks to
	// the server's own daemon, and an environment that never built on this
	// builder has nothing for it to do.
	if _, err := c.Inspect(ctx, networkedBuilderContainer(name)); err == nil {
		c.removeNetworkedBuilder(ctx, name)
	} else if !errdefs.IsNotFound(err) {
		return err
	}
	if err := c.RemoveVolume(ctx, NetworkedBuilderStateVolume(name), true); err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	return nil
}

func writeNetworkedBuilderConfig(dir, path string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if current, err := os.ReadFile(path); err == nil && string(current) == networkedBuilderConfig {
		return nil
	}
	temporary, err := os.CreateTemp(dir, ".networked-buildkitd-")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if _, err := temporary.WriteString(networkedBuilderConfig); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// boundedCommandOutput is a failed CLI command's own words, short enough for
// a step's error.
func boundedCommandOutput(output []byte, err error) string {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return err.Error()
	}
	if len(text) > 600 {
		text = "…" + strings.ToValidUTF8(text[len(text)-600:], "")
	}
	return text
}
