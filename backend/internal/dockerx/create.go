package dockerx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/portalloc"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/errdefs"
	"github.com/docker/go-connections/nat"
)

// ContainerSpec is the dashboard's description of a container to run.
//
// It is deliberately *not* container.Config plus container.HostConfig. Those
// two are the Engine's shape — ninety fields split across the pair on the
// historical accident of which ones the daemon could change after creation —
// and asking an operator to fill them in is how Portainer's create form ended
// up as twelve accordions that assume you already know Docker. This is the
// twenty-odd decisions someone actually makes when running something, in the
// words they would use to describe them, and `toEngine` does the translation.
//
// Everything optional has a working default, so the minimum viable spec is an
// image and a name.
type ContainerSpec struct {
	Name  string `json:"name"`
	Image string `json:"image"`

	// Command and Entrypoint override what the image does on start. Empty
	// means "whatever the image says", which is what you want almost always
	// and what the UI shows as a placeholder rather than a blank box.
	Command    []string `json:"command,omitempty"`
	Entrypoint []string `json:"entrypoint,omitempty"`

	Env     []EnvVar       `json:"env,omitempty"`
	Ports   []PortMapping  `json:"ports,omitempty"`
	Mounts  []MountSpec    `json:"mounts,omitempty"`
	Devices []DeviceSpec   `json:"devices,omitempty"`
	Labels  []LabelSpec    `json:"labels,omitempty"`
	Health  *HealthSpec    `json:"health,omitempty"`
	Limits  ResourceLimits `json:"limits"`

	// Networks the container joins. The first is set at creation; the rest are
	// attached immediately after, because the Engine only takes one in the
	// create call and silently ignores the others.
	Networks []string `json:"networks,omitempty"`
	// NetworkMode is "bridge", "host", "none" or a container:<id>. It is
	// mutually exclusive with Networks; the UI presents it as a choice of
	// "its own network" / "the host's network" rather than as a mode string.
	NetworkMode string `json:"networkMode,omitempty"`
	Hostname    string `json:"hostname,omitempty"`
	// ExtraHosts are "name:ip" entries added to /etc/hosts. The common use is
	// host.docker.internal, which is how a container reaches a service on the
	// server itself.
	ExtraHosts []string `json:"extraHosts,omitempty"`
	DNS        []string `json:"dns,omitempty"`

	// RestartPolicy is "no", "on-failure", "unless-stopped" or "always".
	// Empty means "no", matching the Engine; the UI defaults it to
	// unless-stopped, which is what someone running a service on a server
	// means even when they do not know the option exists.
	RestartPolicy string `json:"restartPolicy,omitempty"`
	MaxRetries    int    `json:"maxRetries,omitempty"`
	// Logging is how much of what this container prints is kept.
	//
	// It is on the spec because it has to survive a round trip: reading a
	// container back and recreating it is how "edit" works here, and a field
	// the spec cannot represent is a setting silently dropped on the way
	// through. Docker's default keeps every line ever printed in one file
	// that is never rotated, so a container whose logging *was* capped and
	// came back out of an edit uncapped is a disk filling up in slow motion
	// with nothing on screen having changed.
	Logging LogConfig `json:"logging,omitempty"`

	WorkingDir string `json:"workingDir,omitempty"`
	User       string `json:"user,omitempty"`
	StopSignal string `json:"stopSignal,omitempty"`

	// Privileged and CapAdd hand the container the host. They are here
	// because some workloads genuinely need them and refusing outright would
	// send the operator to a shell; the API gates them behind system.admin
	// and the UI says plainly what they mean.
	Privileged bool     `json:"privileged,omitempty"`
	CapAdd     []string `json:"capAdd,omitempty"`
	CapDrop    []string `json:"capDrop,omitempty"`
	// Init runs tini as PID 1 so a process that never learned to reap
	// children does not leave zombies. Cheap, and almost always right.
	Init       bool `json:"init,omitempty"`
	AutoRemove bool `json:"autoRemove,omitempty"`
	TTY        bool `json:"tty,omitempty"`
	OpenStdin  bool `json:"openStdin,omitempty"`
	ReadOnly   bool `json:"readOnlyRootfs,omitempty"`

	// Pull is "missing" (default) or "always". Always is what an operator
	// means by "update it" and is the difference between redeploying and
	// re-running the image from six months ago.
	Pull string `json:"pull,omitempty"`
	// Start controls whether the container is started after creation. A spec
	// created but not started is the equivalent of `docker create`.
	Start bool `json:"start"`
}

type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type LabelSpec struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// PortMapping publishes a container port on the host.
//
// HostIP is the field that decides whether a service is on the internet. It
// defaults to empty, which Docker reads as every interface — and Docker's
// published ports bypass most firewall rules because they are DNAT rules
// inserted ahead of the INPUT chain. `Create` therefore returns a warning for
// every mapping left on 0.0.0.0, rather than letting the dashboard be the
// thing that quietly exposed a database.
type PortMapping struct {
	HostIP        string `json:"hostIp,omitempty"`
	HostPort      int    `json:"hostPort"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol,omitempty"`
}

// MountSpec is one thing the container can see from outside itself.
//
// Type is "volume" for Docker-managed storage, "bind" for a path on the
// server, or "tmpfs" for memory. The distinction is the single most confusing
// thing about Docker for a newcomer, so the UI names them "managed volume",
// "folder on this server" and "temporary memory" and this field carries the
// answer rather than making them pick the word.
type MountSpec struct {
	Type     string `json:"type"`
	Source   string `json:"source,omitempty"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"readOnly,omitempty"`
	// SizeMB applies to tmpfs only.
	SizeMB int64 `json:"sizeMb,omitempty"`
}

type DeviceSpec struct {
	Host      string `json:"host"`
	Container string `json:"container,omitempty"`
	// Permissions is some subset of "rwm"; empty means all three.
	Permissions string `json:"permissions,omitempty"`
}

// HealthSpec is how the container reports whether it is actually working.
//
// Worth offering in a create form even though most images ship one: a
// container that is "running" while the process inside it is wedged is the
// failure operators find hardest to see, and it is the difference between
// the dashboard saying "up" and saying "up but not answering".
type HealthSpec struct {
	// Test is the command, as an argv. A single element is run through the
	// image's shell, matching CMD-SHELL.
	Test           []string `json:"test"`
	IntervalSec    int      `json:"intervalSeconds,omitempty"`
	TimeoutSec     int      `json:"timeoutSeconds,omitempty"`
	StartPeriodSec int      `json:"startPeriodSeconds,omitempty"`
	Retries        int      `json:"retries,omitempty"`
	// Disable turns off a healthcheck the image defined.
	Disable bool `json:"disable,omitempty"`
}

// LogConfig is the container's log driver and its options.
//
// An empty Driver means Docker's default, which on every normal install is
// json-file with no rotation — the single most common way a server runs out of
// disk without anything appearing to be wrong.
type LogConfig struct {
	Driver  string            `json:"driver,omitempty"`
	Options map[string]string `json:"options,omitempty"`
}

// Capped is the logging this dashboard offers as the remedy: ten megabytes a
// file, three files, so a container keeps a useful amount of recent output and
// cannot grow past thirty megabytes. Named rather than written inline so the
// finding, the button and the rendered compose all quote the same numbers.
func CappedLogging() LogConfig {
	return LogConfig{
		Driver:  "json-file",
		Options: map[string]string{"max-size": "10m", "max-file": "3"},
	}
}

// ResourceLimits are the ceilings. Zero means "no limit", which is Docker's
// default and is also how a single container takes a server down.
type ResourceLimits struct {
	MemoryMB     int64   `json:"memoryMb,omitempty"`
	MemorySwapMB int64   `json:"memorySwapMb,omitempty"`
	CPUs         float64 `json:"cpus,omitempty"`
	PidsLimit    int64   `json:"pidsLimit,omitempty"`
	ShmSizeMB    int64   `json:"shmSizeMb,omitempty"`
}

// CreateResult is what the caller needs to show the operator afterwards: the
// container that now exists, and everything about the request that was
// accepted but is probably not what they meant.
type CreateResult struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Warnings []string      `json:"warnings"`
	Started  bool          `json:"started"`
	Ports    []PortMapping `json:"ports"`
}

var (
	// ErrNameTaken is returned before anything is created, so the operator
	// fixes a name rather than reading a 409 from the Engine.
	ErrNameTaken = errors.New("a container with that name already exists")
	// ErrComposeManaged marks a container that compose owns. Recreating one
	// behind compose's back leaves the stack describing a container that no
	// longer exists, so the caller is pointed at the stack instead.
	ErrComposeManaged = errors.New("this container belongs to a compose stack")
)

// validNameRe-equivalent: Docker accepts [a-zA-Z0-9][a-zA-Z0-9_.-]*.
func validContainerName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case i > 0 && (r == '_' || r == '.' || r == '-'):
		default:
			return false
		}
	}
	return true
}

// Create runs a container from a spec.
//
// Pulling happens here rather than being left to the Engine's implicit pull,
// because an image that is not present locally otherwise turns a create into a
// silent multi-minute wait with no progress anywhere. `progress` may be nil
// when the caller has nowhere to show it.
func (c *Client) Create(ctx context.Context, spec ContainerSpec, progress chan<- PullProgress) (*CreateResult, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(spec.Image) == "" {
		return nil, errors.New("an image is required")
	}
	spec.Image = ImageRef(spec.Image)
	spec.Name = strings.TrimSpace(strings.TrimPrefix(spec.Name, "/"))
	if spec.Name != "" && !validContainerName(spec.Name) {
		return nil, errors.New("a container name may contain letters, digits, and _ . - after the first character")
	}
	if spec.Name != "" {
		if _, err := cli.ContainerInspect(ctx, spec.Name); err == nil {
			return nil, ErrNameTaken
		}
	}

	if spec.Pull == "always" || !c.imagePresent(ctx, spec.Image) {
		if progress != nil {
			if err := c.PullImage(ctx, spec.Image, progress); err != nil {
				return nil, fmt.Errorf("pull %s: %w", spec.Image, err)
			}
		} else {
			sink := make(chan PullProgress, 16)
			go func() {
				for range sink {
				}
			}()
			err := c.PullImage(ctx, spec.Image, sink)
			close(sink)
			if err != nil {
				return nil, fmt.Errorf("pull %s: %w", spec.Image, err)
			}
		}
	}

	cfg, hostCfg, netCfg, warnings, err := spec.toEngine()
	if err != nil {
		return nil, err
	}

	for attempt := 0; attempt < 4; attempt++ {
		created, err := cli.ContainerCreate(ctx, cfg, hostCfg, netCfg, nil, spec.Name)
		if err != nil {
			return nil, err
		}
		warnings = append(warnings, created.Warnings...)

		// Networks beyond the first: the create call takes exactly one, and
		// passing more is accepted and ignored, which is worse than an error.
		for _, extra := range extraNetworks(spec) {
			if err := cli.NetworkConnect(ctx, extra, created.ID, nil); err != nil {
				warnings = append(warnings, fmt.Sprintf("created, but could not attach network %s: %v", extra, err))
			}
		}

		res := &CreateResult{ID: created.ID, Name: spec.Name, Warnings: warnings}
		if spec.Start {
			if err := cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
				if portalloc.IsConflict(err) && len(hostCfg.PortBindings) > 0 && attempt < 3 {
					// Only the new, unstarted container is removed. Its volumes and
					// the process already holding the preferred port remain intact.
					if removeErr := cli.ContainerRemove(ctx, created.ID, container.RemoveOptions{}); removeErr != nil {
						return res, fmt.Errorf("port conflict: %v; could not remove unstarted candidate: %w", err, removeErr)
					}
					if err := relocateBindings(hostCfg.PortBindings); err != nil {
						return res, err
					}
					warnings = append(warnings, "A requested host port was occupied; available host ports were selected and saved. See the published ports for the addresses to use.")
					continue
				}
				// The container exists and is inspectable, which is what the
				// operator needs in order to find out why it would not start.
				return res, fmt.Errorf("created %s but it would not start: %w", ShortID(created.ID), err)
			}
			res.Started = true
		}
		res.Ports = append([]PortMapping{}, spec.Ports...)
		if res.Name == "" || (res.Started && len(hostCfg.PortBindings) > 0) {
			if insp, err := cli.ContainerInspect(ctx, created.ID); err == nil {
				res.Name = strings.TrimPrefix(insp.Name, "/")
				if res.Started && insp.NetworkSettings != nil {
					res.Ports = publishedMappings(insp.NetworkSettings.Ports)
				}
				if res.Started && len(hostCfg.PortBindings) > 0 && (insp.NetworkSettings == nil || len(res.Ports) == 0) {
					return res, errors.New("container started but Docker reported no published ports")
				}
			} else {
				return res, fmt.Errorf("container started but its published ports could not be read: %w", err)
			}
		}
		return res, nil
	}
	return nil, errors.New("could not publish container ports after repeated conflicts")
}

func publishedMappings(ports nat.PortMap) []PortMapping {
	result := []PortMapping{}
	for port, bindings := range ports {
		for _, binding := range bindings {
			hostPort, _ := strconv.Atoi(binding.HostPort)
			result = append(result, PortMapping{HostIP: binding.HostIP, HostPort: hostPort, ContainerPort: port.Int(), Protocol: port.Proto()})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.ContainerPort != b.ContainerPort {
			return a.ContainerPort < b.ContainerPort
		}
		if a.Protocol != b.Protocol {
			return a.Protocol < b.Protocol
		}
		if a.HostIP != b.HostIP {
			return a.HostIP < b.HostIP
		}
		return a.HostPort < b.HostPort
	})
	return result
}

func extraNetworks(spec ContainerSpec) []string {
	if spec.NetworkMode != "" || len(spec.Networks) < 2 {
		return nil
	}
	return spec.Networks[1:]
}

func (c *Client) imagePresent(ctx context.Context, ref string) bool {
	cli, err := c.api()
	if err != nil {
		return false
	}
	_, err = cli.ImageInspect(ctx, ref)
	return err == nil
}

// toEngine translates the spec into the three structures the Engine wants,
// collecting warnings about choices that are legal and probably unintended.
func (s ContainerSpec) toEngine() (*container.Config, *container.HostConfig, *network.NetworkingConfig, []string, error) {
	warnings := []string{}

	cfg := &container.Config{
		Image:        s.Image,
		Hostname:     s.Hostname,
		User:         s.User,
		WorkingDir:   s.WorkingDir,
		Tty:          s.TTY,
		OpenStdin:    s.OpenStdin,
		StopSignal:   s.StopSignal,
		Env:          make([]string, 0, len(s.Env)),
		Labels:       map[string]string{},
		ExposedPorts: nat.PortSet{},
	}
	for _, e := range s.Env {
		name := strings.TrimSpace(e.Name)
		if name == "" {
			continue
		}
		cfg.Env = append(cfg.Env, name+"="+e.Value)
	}
	for _, l := range s.Labels {
		if name := strings.TrimSpace(l.Name); name != "" {
			cfg.Labels[name] = l.Value
		}
	}
	if len(s.Command) > 0 {
		cfg.Cmd = s.Command
	}
	if len(s.Entrypoint) > 0 {
		cfg.Entrypoint = s.Entrypoint
	}
	if s.Health != nil {
		cfg.Healthcheck = s.Health.toEngine()
	}

	hostCfg := &container.HostConfig{
		Privileged:     s.Privileged,
		CapAdd:         s.CapAdd,
		CapDrop:        s.CapDrop,
		AutoRemove:     s.AutoRemove,
		ReadonlyRootfs: s.ReadOnly,
		ExtraHosts:     s.ExtraHosts,
		DNS:            s.DNS,
		PortBindings:   nat.PortMap{},
		Mounts:         []mount.Mount{},
		Resources:      container.Resources{},
	}
	if s.Init {
		init := true
		hostCfg.Init = &init
	}
	if s.Logging.Driver != "" || len(s.Logging.Options) != 0 {
		hostCfg.LogConfig = container.LogConfig{
			Type: s.Logging.Driver, Config: s.Logging.Options,
		}
	}

	policy, err := restartPolicy(s.RestartPolicy, s.MaxRetries)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	hostCfg.RestartPolicy = policy
	if s.AutoRemove && policy.Name != container.RestartPolicyDisabled {
		return nil, nil, nil, nil, errors.New("a container cannot both restart automatically and remove itself when it exits")
	}

	for _, p := range s.Ports {
		if p.ContainerPort <= 0 || p.ContainerPort > 65535 {
			return nil, nil, nil, nil, fmt.Errorf("container port %d is not a port number", p.ContainerPort)
		}
		proto := strings.ToLower(p.Protocol)
		if proto != "udp" {
			proto = "tcp"
		}
		port, err := nat.NewPort(proto, strconv.Itoa(p.ContainerPort))
		if err != nil {
			return nil, nil, nil, nil, err
		}
		cfg.ExposedPorts[port] = struct{}{}
		if p.HostPort == 0 {
			// Exposed but not published: reachable from other containers on
			// the same network and from nowhere else. A legitimate choice,
			// and the one a database should be making.
			continue
		}
		if p.HostPort < 0 || p.HostPort > 65535 {
			return nil, nil, nil, nil, fmt.Errorf("host port %d is not a port number", p.HostPort)
		}
		hostIP := strings.TrimSpace(p.HostIP)
		hostCfg.PortBindings[port] = append(hostCfg.PortBindings[port], nat.PortBinding{
			HostIP:   hostIP,
			HostPort: strconv.Itoa(p.HostPort),
		})
		if hostIP == "" || hostIP == "0.0.0.0" || hostIP == "::" {
			warnings = append(warnings, fmt.Sprintf(
				"port %d is published on every interface. Docker's published ports are NAT rules that sit in front of the firewall, so this is reachable from anywhere that can route to this server. Bind it to 127.0.0.1 if only this machine should reach it.",
				p.HostPort))
		}
	}

	for _, m := range s.Mounts {
		mnt, warn, err := m.toEngine()
		if err != nil {
			return nil, nil, nil, nil, err
		}
		if warn != "" {
			warnings = append(warnings, warn)
		}
		hostCfg.Mounts = append(hostCfg.Mounts, mnt)
	}

	for _, d := range s.Devices {
		perms := d.Permissions
		if perms == "" {
			perms = "rwm"
		}
		target := d.Container
		if target == "" {
			target = d.Host
		}
		hostCfg.Devices = append(hostCfg.Devices, container.DeviceMapping{
			PathOnHost:        d.Host,
			PathInContainer:   target,
			CgroupPermissions: perms,
		})
	}

	if s.Limits.MemoryMB > 0 {
		hostCfg.Memory = s.Limits.MemoryMB * 1024 * 1024
	}
	if s.Limits.MemorySwapMB > 0 {
		hostCfg.MemorySwap = s.Limits.MemorySwapMB * 1024 * 1024
	}
	if s.Limits.CPUs > 0 {
		// NanoCPUs is the modern quota: 1.5 means one and a half cores, which
		// is the unit `docker run --cpus` takes and the only one an operator
		// should have to think in.
		hostCfg.NanoCPUs = int64(s.Limits.CPUs * 1e9)
	}
	if s.Limits.PidsLimit > 0 {
		limit := s.Limits.PidsLimit
		hostCfg.PidsLimit = &limit
	}
	if s.Limits.ShmSizeMB > 0 {
		hostCfg.ShmSize = s.Limits.ShmSizeMB * 1024 * 1024
	}
	if s.Limits.MemoryMB == 0 {
		warnings = append(warnings, "no memory limit is set, so this container can use every byte on the server and the kernel will pick which process to kill when it runs out.")
	}

	netCfg := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{}}
	switch {
	case s.NetworkMode != "":
		hostCfg.NetworkMode = container.NetworkMode(s.NetworkMode)
		if s.NetworkMode == "host" {
			warnings = append(warnings, "on the host's network the container shares every port on the server directly, and published ports are ignored.")
		}
	case len(s.Networks) > 0:
		hostCfg.NetworkMode = container.NetworkMode(s.Networks[0])
		netCfg.EndpointsConfig[s.Networks[0]] = &network.EndpointSettings{}
	}

	if s.Privileged {
		warnings = append(warnings, "privileged containers can reach every device on the server and are equivalent to root on the host.")
	}
	for _, cap := range s.CapAdd {
		if strings.EqualFold(cap, "SYS_ADMIN") || strings.EqualFold(cap, "ALL") {
			warnings = append(warnings, "adding "+cap+" is close to running privileged.")
		}
	}

	sort.Strings(warnings)
	return cfg, hostCfg, netCfg, warnings, nil
}

func (h *HealthSpec) toEngine() *container.HealthConfig {
	if h.Disable {
		return &container.HealthConfig{Test: []string{"NONE"}}
	}
	test := h.Test
	if len(test) == 1 {
		test = []string{"CMD-SHELL", test[0]}
	} else if len(test) > 1 && test[0] != "CMD" && test[0] != "CMD-SHELL" && test[0] != "NONE" {
		test = append([]string{"CMD"}, test...)
	}
	cfg := &container.HealthConfig{
		Test:    test,
		Retries: h.Retries,
	}
	if h.IntervalSec > 0 {
		cfg.Interval = time.Duration(h.IntervalSec) * time.Second
	}
	if h.TimeoutSec > 0 {
		cfg.Timeout = time.Duration(h.TimeoutSec) * time.Second
	}
	if h.StartPeriodSec > 0 {
		cfg.StartPeriod = time.Duration(h.StartPeriodSec) * time.Second
	}
	return cfg
}

func (m MountSpec) toEngine() (mount.Mount, string, error) {
	target := strings.TrimSpace(m.Target)
	if target == "" || !strings.HasPrefix(target, "/") {
		return mount.Mount{}, "", fmt.Errorf("mount target %q must be an absolute path inside the container", m.Target)
	}
	switch m.Type {
	case "volume", "":
		name := strings.TrimSpace(m.Source)
		if name == "" {
			// An anonymous volume: Docker names it a 64-hex string nobody can
			// recognise later, which is how orphaned volumes accumulate.
			return mount.Mount{Type: mount.TypeVolume, Target: target, ReadOnly: m.ReadOnly},
				"an unnamed volume was created for " + target + ". It survives the container but is named as a random hash, so it is hard to identify later — give it a name if the data matters.", nil
		}
		return mount.Mount{Type: mount.TypeVolume, Source: name, Target: target, ReadOnly: m.ReadOnly}, "", nil
	case "bind":
		src := strings.TrimSpace(m.Source)
		if !strings.HasPrefix(src, "/") {
			return mount.Mount{}, "", fmt.Errorf("folder %q must be an absolute path on the server", m.Source)
		}
		warn := ""
		if !m.ReadOnly && isSensitiveHostPath(src) {
			warn = src + " is mounted writable. A container that can write there can take over the server."
		}
		return mount.Mount{
			Type:   mount.TypeBind,
			Source: src, Target: target, ReadOnly: m.ReadOnly,
			// CreateMountpoint matches what `docker run -v` does: a source
			// that does not exist yet is created rather than refused.
			BindOptions: &mount.BindOptions{CreateMountpoint: true},
		}, warn, nil
	case "tmpfs":
		mnt := mount.Mount{Type: mount.TypeTmpfs, Target: target}
		if m.SizeMB > 0 {
			mnt.TmpfsOptions = &mount.TmpfsOptions{SizeBytes: m.SizeMB * 1024 * 1024}
		}
		return mnt, "", nil
	default:
		return mount.Mount{}, "", fmt.Errorf("unknown mount type %q", m.Type)
	}
}

// isSensitiveHostPath marks the paths whose exposure to a container is the
// documented way to escape one. Advisory only — the API's capability check is
// what actually gates them.
func isSensitiveHostPath(p string) bool {
	clean := strings.TrimSuffix(p, "/")
	if clean == "" {
		return true // "/" itself
	}
	for _, s := range []string{
		"/", "/etc", "/root", "/boot", "/dev", "/proc", "/sys",
		"/var/run/docker.sock", "/run/docker.sock", "/var/lib/docker",
	} {
		if clean == strings.TrimSuffix(s, "/") {
			return true
		}
	}
	return false
}

func restartPolicy(name string, retries int) (container.RestartPolicy, error) {
	switch name {
	case "", "no", "none":
		return container.RestartPolicy{Name: container.RestartPolicyDisabled}, nil
	case "always":
		return container.RestartPolicy{Name: container.RestartPolicyAlways}, nil
	case "unless-stopped":
		return container.RestartPolicy{Name: container.RestartPolicyUnlessStopped}, nil
	case "on-failure":
		return container.RestartPolicy{Name: container.RestartPolicyOnFailure, MaximumRetryCount: retries}, nil
	default:
		return container.RestartPolicy{}, fmt.Errorf("unknown restart policy %q", name)
	}
}

// SpecOf reconstructs a spec from a container that already exists.
//
// This is what makes "duplicate", "edit and recreate" and "turn this into a
// compose file" possible at all: the Engine only stores the resolved config,
// so every one of those features starts by reading a container back into the
// shape the create form speaks.
func (c *Client) SpecOf(ctx context.Context, id string) (*ContainerSpec, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	insp, err := cli.ContainerInspect(ctx, id)
	if err != nil {
		return nil, err
	}
	spec := &ContainerSpec{
		Name:     strings.TrimPrefix(insp.Name, "/"),
		Env:      []EnvVar{},
		Ports:    []PortMapping{},
		Mounts:   []MountSpec{},
		Labels:   []LabelSpec{},
		Networks: []string{},
		Start:    true,
		Pull:     "missing",
	}
	if insp.Config != nil {
		spec.Image = insp.Config.Image
		spec.Hostname = insp.Config.Hostname
		spec.User = insp.Config.User
		spec.WorkingDir = insp.Config.WorkingDir
		spec.TTY = insp.Config.Tty
		spec.OpenStdin = insp.Config.OpenStdin
		spec.StopSignal = insp.Config.StopSignal
		spec.Command = insp.Config.Cmd
		spec.Entrypoint = insp.Config.Entrypoint
		for _, e := range insp.Config.Env {
			name, value, _ := strings.Cut(e, "=")
			spec.Env = append(spec.Env, EnvVar{Name: name, Value: value})
		}
		for name, value := range insp.Config.Labels {
			// Compose's own labels describe a relationship to a project, not
			// a choice the operator made, and copying them into a new
			// container would make it look like part of a stack it is not in.
			if strings.HasPrefix(name, "com.docker.compose.") {
				continue
			}
			spec.Labels = append(spec.Labels, LabelSpec{Name: name, Value: value})
		}
		sort.Slice(spec.Labels, func(i, j int) bool { return spec.Labels[i].Name < spec.Labels[j].Name })
	}
	if insp.HostConfig != nil {
		hc := insp.HostConfig
		spec.Privileged = hc.Privileged
		spec.CapAdd = hc.CapAdd
		spec.CapDrop = hc.CapDrop
		spec.AutoRemove = hc.AutoRemove
		spec.ReadOnly = hc.ReadonlyRootfs
		spec.ExtraHosts = hc.ExtraHosts
		spec.DNS = hc.DNS
		spec.RestartPolicy = string(hc.RestartPolicy.Name)
		spec.MaxRetries = hc.RestartPolicy.MaximumRetryCount
		// Read back so an edit-and-recreate keeps it. Without this the round
		// trip quietly reset a capped container to Docker's unbounded default.
		if hc.LogConfig.Type != "" || len(hc.LogConfig.Config) != 0 {
			spec.Logging = LogConfig{Driver: hc.LogConfig.Type, Options: hc.LogConfig.Config}
		}
		if hc.Init != nil {
			spec.Init = *hc.Init
		}
		spec.Limits = ResourceLimits{
			MemoryMB:  hc.Memory / (1024 * 1024),
			CPUs:      float64(hc.NanoCPUs) / 1e9,
			ShmSizeMB: hc.ShmSize / (1024 * 1024),
		}
		if hc.PidsLimit != nil {
			spec.Limits.PidsLimit = *hc.PidsLimit
		}
		for _, d := range hc.Devices {
			spec.Devices = append(spec.Devices, DeviceSpec{
				Host: d.PathOnHost, Container: d.PathInContainer, Permissions: d.CgroupPermissions,
			})
		}
		mode := string(hc.NetworkMode)
		if mode == "host" || mode == "none" || strings.HasPrefix(mode, "container:") {
			spec.NetworkMode = mode
		}
		for portSpec, bindings := range hc.PortBindings {
			for _, b := range bindings {
				hostPort, _ := strconv.Atoi(b.HostPort)
				spec.Ports = append(spec.Ports, PortMapping{
					HostIP: b.HostIP, HostPort: hostPort,
					ContainerPort: portSpec.Int(), Protocol: portSpec.Proto(),
				})
			}
		}
		for _, m := range hc.Mounts {
			ms := MountSpec{Type: string(m.Type), Source: m.Source, Target: m.Target, ReadOnly: m.ReadOnly}
			if m.TmpfsOptions != nil {
				ms.SizeMB = m.TmpfsOptions.SizeBytes / (1024 * 1024)
			}
			spec.Mounts = append(spec.Mounts, ms)
		}
	}
	// A container created by `docker run -v` carries its mounts in Binds
	// rather than Mounts, and reading only the latter loses every volume on
	// anything not created through this dashboard.
	if len(spec.Mounts) == 0 {
		for _, m := range insp.Mounts {
			spec.Mounts = append(spec.Mounts, MountSpec{
				Type: string(m.Type), Source: mountSource(m.Type, m.Name, m.Source),
				Target: m.Destination, ReadOnly: !m.RW,
			})
		}
	}
	if insp.NetworkSettings != nil && spec.NetworkMode == "" {
		for name := range insp.NetworkSettings.Networks {
			spec.Networks = append(spec.Networks, name)
		}
		sort.Strings(spec.Networks)
	}
	sort.Slice(spec.Ports, func(i, j int) bool { return spec.Ports[i].ContainerPort < spec.Ports[j].ContainerPort })
	sort.Slice(spec.Mounts, func(i, j int) bool { return spec.Mounts[i].Target < spec.Mounts[j].Target })
	return spec, nil
}

func mountSource(kind mount.Type, name, source string) string {
	if kind == mount.TypeVolume && name != "" {
		return name
	}
	return source
}

// RecreateOptions describes an in-place replacement of a container.
type RecreateOptions struct {
	// PullLatest re-pulls the image tag first, which is the whole point of
	// the operation for anything tracking :latest.
	PullLatest bool `json:"pullLatest"`
	// Spec, when set, replaces the container's configuration instead of
	// reusing it. This is "edit a running container", which Docker does not
	// support and which every operator assumes exists.
	Spec *ContainerSpec `json:"spec,omitempty"`
}

// Recreate replaces a container with a new one from the same (or an edited)
// configuration.
//
// Docker has no notion of editing a container: every field but a handful of
// resource limits is fixed at creation. The universal workaround is to destroy
// and recreate, which is fine right up until the create fails and the
// operator is left with nothing where their service used to be. So the old
// container is renamed aside rather than removed, and is restored if anything
// after that point goes wrong. It is only deleted once the replacement is
// running.
func (c *Client) Recreate(ctx context.Context, id string, opts RecreateOptions, progress chan<- PullProgress) (*CreateResult, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	insp, err := cli.ContainerInspect(ctx, id)
	if err != nil {
		return nil, err
	}
	// Pin the inspected identity before renaming; a caller may have supplied
	// the current name, which can refer to a different container afterwards.
	if insp.ID == "" {
		return nil, errors.New("Docker returned no container identity")
	}
	id = insp.ID
	if insp.Config != nil && insp.Config.Labels[labelProject] != "" {
		return nil, fmt.Errorf("%w (%s). Redeploying the stack is what keeps compose and the Engine agreeing about what exists",
			ErrComposeManaged, insp.Config.Labels[labelProject])
	}

	spec := opts.Spec
	if spec == nil {
		spec, err = c.SpecOf(ctx, id)
		if err != nil {
			return nil, err
		}
	}
	name := strings.TrimPrefix(insp.Name, "/")
	spec.Name = name
	spec.Start = true
	if opts.PullLatest {
		spec.Pull = "always"
	}

	// A container created with --rm deletes itself the moment it stops, so
	// there is no version of this that can put it back if the replacement
	// fails to build. Refusing is the honest answer; the alternative is
	// destroying something on the operator's behalf and telling them
	// afterwards.
	if insp.HostConfig != nil && insp.HostConfig.AutoRemove {
		return nil, fmt.Errorf(
			"%s is set to remove itself when it stops, so it cannot be replaced in place — there would be nothing to put back if the new one failed to start. Create the replacement under a new name instead", name)
	}

	// Docker's rename atomically reserves a fresh parking name. A collision is
	// retried; it never grants permission to remove the container owning it.
	var parked string
	for attempt := 0; attempt < 3; attempt++ {
		var suffix [12]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return nil, err
		}
		parked = name + "_jd_replaced_" + hex.EncodeToString(suffix[:])
		err = cli.ContainerRename(ctx, id, parked)
		if err == nil {
			break
		}
		if !errdefs.IsConflict(err) {
			return nil, fmt.Errorf("could not set %s aside: %w", name, err)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("could not reserve a parking name for %s: %w", name, err)
	}

	wasRunning := insp.State != nil && insp.State.Running
	restore := func(cause error, candidate *CreateResult) error {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if candidate != nil && candidate.ID != "" && candidate.ID != id {
			if err := cli.ContainerRemove(cleanupCtx, candidate.ID, container.RemoveOptions{Force: true}); err != nil && !errdefs.IsNotFound(err) {
				return fmt.Errorf("replacement failed: %v; original is parked as %s and candidate cleanup failed: %w", cause, parked, err)
			}
		}
		if err := cli.ContainerRename(cleanupCtx, id, name); err != nil {
			return fmt.Errorf("replacement failed: %v; original is parked as %s and its name could not be restored: %w", cause, parked, err)
		}
		if wasRunning {
			if err := cli.ContainerStart(cleanupCtx, id, container.StartOptions{}); err != nil {
				return fmt.Errorf("replacement failed: %v; original name restored but restarting it failed: %w", cause, err)
			}
		}
		return fmt.Errorf("original container restored after replacement failed: %w", cause)
	}
	if wasRunning {
		timeout := 10
		if err := cli.ContainerStop(ctx, id, container.StopOptions{Timeout: &timeout}); err != nil {
			return nil, restore(fmt.Errorf("could not stop %s: %w", name, err), nil)
		}
	}
	res, err := c.Create(ctx, *spec, progress)
	if err != nil {
		return nil, restore(err, res)
	}

	// Only now is the original expendable. Its anonymous volumes are kept:
	// the replacement is using the named ones, and an anonymous volume that
	// held data is unrecoverable once removed.
	if err := cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true}); err != nil {
		res.Warnings = append(res.Warnings,
			fmt.Sprintf("the replacement is running, but the old container could not be removed and is parked as %s: %v", parked, err))
	}
	return res, nil
}

// UpdateResources changes the limits on a container that is already running.
//
// The one thing Docker really can change in place, and the reason it is worth
// exposing separately from Recreate: an operator who put a memory limit on
// something and got it wrong should not have to destroy the container to fix
// the number.
func (c *Client) UpdateResources(ctx context.Context, id string, limits ResourceLimits) ([]string, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	res := container.Resources{}
	if limits.MemoryMB > 0 {
		res.Memory = limits.MemoryMB * 1024 * 1024
	}
	if limits.MemorySwapMB > 0 {
		res.MemorySwap = limits.MemorySwapMB * 1024 * 1024
	}
	if limits.CPUs > 0 {
		res.NanoCPUs = int64(limits.CPUs * 1e9)
	}
	if limits.PidsLimit > 0 {
		pids := limits.PidsLimit
		res.PidsLimit = &pids
	}
	out, err := cli.ContainerUpdate(ctx, id, container.UpdateConfig{Resources: res})
	if err != nil {
		return nil, err
	}
	return orEmpty(out.Warnings), nil
}

func (c *Client) Rename(ctx context.Context, id, name string) error {
	cli, err := c.api()
	if err != nil {
		return err
	}
	if !validContainerName(name) {
		return errors.New("a container name may contain letters, digits, and _ . - after the first character")
	}
	return cli.ContainerRename(ctx, id, name)
}

// Concrete bindings survive stop/start; Docker's empty published-port setting
// allocates a different number on each start and would invalidate saved routes.
func relocateBindings(ports nat.PortMap) error {
	groups := map[string][]struct {
		port  nat.Port
		index int
	}{}
	for port, bindings := range ports {
		for index, binding := range bindings {
			groups[binding.HostPort] = append(groups[binding.HostPort], struct {
				port  nat.Port
				index int
			}{port, index})
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	reserved := map[int]bool{}
	for _, key := range keys {
		preferred, _ := strconv.Atoi(key)
		preferred = max(1024, preferred)
		selected, err := portalloc.Select(preferred, 1024, reserved, func(candidate int) error {
			for _, entry := range groups[key] {
				binding := ports[entry.port][entry.index]
				if err := portalloc.Available(binding.HostIP, entry.port.Proto(), candidate); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		reserved[selected] = true
		for _, entry := range groups[key] {
			bindings := ports[entry.port]
			bindings[entry.index].HostPort = strconv.Itoa(selected)
			ports[entry.port] = bindings
		}
	}
	return nil
}
