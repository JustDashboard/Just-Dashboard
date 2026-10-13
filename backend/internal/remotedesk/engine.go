package remotedesk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/portalloc"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/go-connections/nat"
)

// The engine is guacd, Apache Guacamole's protocol daemon. It is pinned by
// digest so an install draws exactly the image this release was tested with,
// and published on loopback only: the backend is its one client.
const (
	GuacdImage   = "guacamole/guacd@sha256:8974eaa9ba32f713daf311e7cc8cd7e4cdfba1edea39eed75524e78ef4b08f4f"
	GuacdVersion = "1.6.0"

	engineName   = "just-dashboard-remote-desktop"
	engineVolume = "just-dashboard-remote-desktop-files"
	engineLabel  = "io.justdashboard.remote-desktop"
	engineRole   = "engine"
	guacdPort    = nat.Port("4822/tcp")
	// guacd's home holds the folder each Windows session shares as a drive,
	// which is how files move between the browser and the PC.
	engineHome = "/home/guacd"
)

const (
	EngineMissing     = "missing"
	EngineRunning     = "running"
	EngineStopped     = "stopped"
	EngineUnavailable = "unavailable"
	EngineForeign     = "foreign"
)

var ErrEngineNotRunning = errors.New("the remote desktop engine is not running")

// EngineStatus is what Docker says about the engine right now; nothing about
// it is stored, so a container removed by hand reads as missing.
type EngineStatus struct {
	State     string `json:"state"`
	Image     string `json:"image"`
	Version   string `json:"version"`
	Pulled    bool   `json:"pulled"`
	Address   string `json:"address,omitempty"`
	Container string `json:"container,omitempty"`
	StartedAt int64  `json:"startedAt,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Engine is the runtime the service drives; tests replace it.
type Engine interface {
	Status(ctx context.Context) EngineStatus
	Install(ctx context.Context, progress func(string)) error
	Remove(ctx context.Context) error
	Close() error
}

type dockerEngine struct {
	cli *client.Client
	err error
}

func NewDockerEngine(host string) Engine {
	cli, err := client.NewClientWithOpts(client.WithHost(host), client.WithAPIVersionNegotiation())
	return &dockerEngine{cli: cli, err: err}
}

func (d *dockerEngine) ready() error {
	if d == nil || d.cli == nil || d.err != nil {
		return errors.New("Docker is not reachable from the dashboard")
	}
	return nil
}

func (d *dockerEngine) Close() error {
	if d == nil || d.cli == nil {
		return nil
	}
	return d.cli.Close()
}

func ownedLabels() map[string]string { return map[string]string{engineLabel: engineRole} }

func owned(labels map[string]string) bool { return labels[engineLabel] == engineRole }

func (d *dockerEngine) Status(ctx context.Context) EngineStatus {
	st := EngineStatus{State: EngineMissing, Image: GuacdImage, Version: GuacdVersion}
	if err := d.ready(); err != nil {
		st.State, st.Error = EngineUnavailable, err.Error()
		return st
	}
	if _, err := d.cli.Ping(ctx); err != nil {
		st.State, st.Error = EngineUnavailable, "Docker is not answering: "+err.Error()
		return st
	}
	st.Pulled = d.imagePinned(ctx) == nil
	c, err := d.cli.ContainerInspect(ctx, engineName)
	if errdefs.IsNotFound(err) {
		return st
	}
	if err != nil {
		st.State, st.Error = EngineUnavailable, err.Error()
		return st
	}
	if c.Config == nil || !owned(c.Config.Labels) {
		st.State = EngineForeign
		st.Error = fmt.Sprintf("a container named %s exists that the dashboard did not create", engineName)
		return st
	}
	st.Container = c.ID
	if len(st.Container) > 12 {
		st.Container = st.Container[:12]
	}
	if port := boundPort(c); port != "" {
		st.Address = net.JoinHostPort("127.0.0.1", port)
	}
	st.State = EngineStopped
	if c.State != nil && c.State.Running {
		st.State = EngineRunning
		if started, err := time.Parse(time.RFC3339Nano, c.State.StartedAt); err == nil {
			st.StartedAt = started.Unix()
		}
	}
	return st
}

// boundPort only reads a loopback binding, so a container someone edited to
// publish guacd on every address is not one the dashboard will talk to.
func boundPort(c container.InspectResponse) string {
	if c.HostConfig == nil {
		return ""
	}
	for _, binding := range c.HostConfig.PortBindings[guacdPort] {
		if binding.HostIP == "127.0.0.1" && binding.HostPort != "" {
			return binding.HostPort
		}
	}
	return ""
}

func (d *dockerEngine) imagePinned(ctx context.Context) error {
	img, err := d.cli.ImageInspect(ctx, GuacdImage)
	if err != nil {
		return err
	}
	for _, digest := range img.RepoDigests {
		if digest == GuacdImage {
			return nil
		}
	}
	return errors.New("the cached guacd image does not match the pinned digest")
}

func (d *dockerEngine) Install(ctx context.Context, progress func(string)) error {
	if err := d.ready(); err != nil {
		return err
	}
	st := d.Status(ctx)
	switch st.State {
	case EngineUnavailable, EngineForeign:
		return errors.New(st.Error)
	case EngineRunning:
		if st.Address == "" {
			return errors.New("the engine is running without a loopback port; remove it and install again")
		}
		progress("guacd " + GuacdVersion + " is already running on " + st.Address)
		return nil
	}
	if !st.Pulled {
		progress("Pulling guacamole/guacd " + GuacdVersion)
		if err := d.pull(ctx, progress); err != nil {
			return fmt.Errorf("pull guacd: %w", err)
		}
	}
	if err := d.imagePinned(ctx); err != nil {
		return err
	}
	if st.State == EngineStopped {
		progress("Starting the existing engine container")
		return d.start(ctx, progress)
	}
	if err := d.ensureNetwork(ctx, progress); err != nil {
		return err
	}
	if err := d.ensureVolume(ctx, progress); err != nil {
		return err
	}
	port, err := portalloc.Select(4822, 4822, nil, func(p int) error { return portalloc.Available("127.0.0.1", "tcp", p) })
	if err != nil {
		return err
	}
	pids := int64(512)
	host := &container.HostConfig{
		NetworkMode:  container.NetworkMode(engineName),
		PortBindings: nat.PortMap{guacdPort: {{HostIP: "127.0.0.1", HostPort: strconv.Itoa(port)}}},
		Mounts:       []mount.Mount{{Type: mount.TypeVolume, Source: engineVolume, Target: engineHome}},
		CapDrop:      []string{"ALL"},
		SecurityOpt:  []string{"no-new-privileges:true"},
		// Encoding a busy screen is the heaviest thing guacd does; it gets a
		// smaller share than the dashboard so a session never starves it.
		Resources:     container.Resources{Memory: 2 << 30, MemorySwap: 2 << 30, PidsLimit: &pids, CPUShares: 512},
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
		LogConfig:     container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "5m", "max-file": "2"}},
	}
	config := &container.Config{
		Image:        GuacdImage,
		Hostname:     "remote-desktop",
		Labels:       ownedLabels(),
		Env:          []string{"LOG_LEVEL=info"},
		ExposedPorts: nat.PortSet{guacdPort: {}},
	}
	progress(fmt.Sprintf("Creating %s on 127.0.0.1:%d", engineName, port))
	if _, err := d.cli.ContainerCreate(ctx, config, host, &network.NetworkingConfig{}, nil, engineName); err != nil {
		return fmt.Errorf("create the engine container: %w", err)
	}
	return d.start(ctx, progress)
}

func (d *dockerEngine) start(ctx context.Context, progress func(string)) error {
	if err := d.cli.ContainerStart(ctx, engineName, container.StartOptions{}); err != nil {
		return fmt.Errorf("start the engine: %w", err)
	}
	st := d.Status(ctx)
	if st.Address == "" {
		return errors.New("the engine started without a loopback port")
	}
	progress("Waiting for guacd to answer on " + st.Address)
	deadline := time.Now().Add(20 * time.Second)
	for {
		err := probeGuacd(ctx, st.Address)
		if err == nil {
			progress("guacd " + GuacdVersion + " is ready")
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("guacd did not answer: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// probeGuacd asks for the RDP client's parameters and hangs up: an answer
// means the daemon is up and its RDP support loaded.
func probeGuacd(ctx context.Context, address string) error {
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write(encode("select", ProtocolRDP)); err != nil {
		return err
	}
	_, err = expect(NewReader(conn), "args")
	return err
}

func (d *dockerEngine) pull(ctx context.Context, progress func(string)) error {
	rc, err := d.cli.ImagePull(ctx, GuacdImage, image.PullOptions{})
	if err != nil {
		return err
	}
	defer rc.Close()
	dec := json.NewDecoder(rc)
	last := map[string]string{}
	for {
		var msg struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if msg.Error != "" {
			return errors.New(msg.Error)
		}
		// Docker repeats "Downloading" for every chunk; the console only
		// needs each layer's change of state.
		if last[msg.ID] == msg.Status {
			continue
		}
		last[msg.ID] = msg.Status
		if msg.ID != "" {
			progress(msg.ID + ": " + msg.Status)
		} else {
			progress(msg.Status)
		}
	}
}

func (d *dockerEngine) ensureNetwork(ctx context.Context, progress func(string)) error {
	nw, err := d.cli.NetworkInspect(ctx, engineName, network.InspectOptions{})
	if err == nil {
		if !owned(nw.Labels) {
			return fmt.Errorf("a network named %s exists that the dashboard did not create", engineName)
		}
		return nil
	}
	if !errdefs.IsNotFound(err) {
		return err
	}
	progress("Creating the engine's bridge network")
	_, err = d.cli.NetworkCreate(ctx, engineName, network.CreateOptions{Driver: "bridge", Labels: ownedLabels()})
	return err
}

func (d *dockerEngine) ensureVolume(ctx context.Context, progress func(string)) error {
	v, err := d.cli.VolumeInspect(ctx, engineVolume)
	if err == nil {
		if !owned(v.Labels) {
			return fmt.Errorf("a volume named %s exists that the dashboard did not create", engineVolume)
		}
		return nil
	}
	if !errdefs.IsNotFound(err) {
		return err
	}
	progress("Creating the volume for transferred files")
	_, err = d.cli.VolumeCreate(ctx, volume.CreateOptions{Name: engineVolume, Driver: "local", Labels: ownedLabels()})
	return err
}

// Remove takes away the container, its network and the files sessions moved
// through it. The image stays, so installing again does not download it.
func (d *dockerEngine) Remove(ctx context.Context) error {
	if err := d.ready(); err != nil {
		return err
	}
	c, err := d.cli.ContainerInspect(ctx, engineName)
	switch {
	case errdefs.IsNotFound(err):
	case err != nil:
		return err
	case c.Config == nil || !owned(c.Config.Labels):
		return fmt.Errorf("the container named %s is not the dashboard's; leaving it alone", engineName)
	default:
		timeout := 10
		d.cli.ContainerStop(ctx, c.ID, container.StopOptions{Timeout: &timeout})
		if err := d.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
			return fmt.Errorf("remove the engine container: %w", err)
		}
	}
	if nw, err := d.cli.NetworkInspect(ctx, engineName, network.InspectOptions{}); err == nil && owned(nw.Labels) {
		if len(nw.Containers) > 0 {
			return fmt.Errorf("other containers joined %s; leaving the network in place", engineName)
		}
		if err := d.cli.NetworkRemove(ctx, nw.ID); err != nil {
			return fmt.Errorf("remove the engine network: %w", err)
		}
	}
	if v, err := d.cli.VolumeInspect(ctx, engineVolume); err == nil && owned(v.Labels) {
		if err := d.cli.VolumeRemove(ctx, v.Name, false); err != nil {
			return fmt.Errorf("remove the transfer volume: %w", err)
		}
	}
	return nil
}
