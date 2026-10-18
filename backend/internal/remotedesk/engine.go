package remotedesk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
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
	// Media is where an engine's UDP ports for WebRTC are published, as
	// address:first-last, when it has them.
	Media     string `json:"media,omitempty"`
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

// engineSpec is one container the dashboard owns for Remote Desktop: guacd,
// and the stream bridge beside it. Both join the same bridge network and
// publish one port on loopback only.
type engineSpec struct {
	name     string
	role     string
	product  string
	image    string
	version  string
	port     nat.Port
	prefer   int
	volume   string
	mountAt  string
	env      []string
	memory   int64
	hostname string
	// media, when set, is the address a block of mediaPorts UDP ports is
	// published on for WebRTC, settled at install: none when it is empty.
	media      func(ctx context.Context) string
	mediaPorts int
	// mediaEnv names the variable the image reads its UDP range from, as
	// "first:last"; the image sets its own default, which outranks its config.
	mediaEnv string
	// seed is a tar archive copied into the container before its first start,
	// given the media address and the first of its ports.
	seed func(media string, firstPort int) ([]byte, error)
	// revision names what seed writes. A container made by another revision,
	// or for another media address, is made again by the next install.
	revision string
	probe    func(ctx context.Context, address string) error
}

// revisionLabel carries the revision and media address a container was made
// with, so an install can tell a container that is out of date.
const revisionLabel = engineLabel + ".revision"

// guacdRevision changes whenever the container's own configuration does (its
// resources, its environment), so an engine made by an older dashboard is
// made again on the next start; its volume, and the files on it, stay.
const guacdRevision = "2"

var guacdSpec = engineSpec{
	name: engineName, role: engineRole, product: "guacd", image: GuacdImage, version: GuacdVersion,
	port: guacdPort, prefer: 4822, volume: engineVolume, mountAt: engineHome,
	env: []string{"LOG_LEVEL=info"}, memory: 2 << 30, hostname: "remote-desktop", probe: probeGuacd,
	revision: guacdRevision,
}

type dockerEngine struct {
	cli  *client.Client
	err  error
	spec engineSpec
}

func NewDockerEngine(host string) Engine { return newDockerEngine(host, guacdSpec) }

func newDockerEngine(host string, spec engineSpec) Engine {
	cli, err := client.NewClientWithOpts(client.WithHost(host), client.WithAPIVersionNegotiation())
	return &dockerEngine{cli: cli, err: err, spec: spec}
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

// The network is shared, and carries the engine's label whichever container
// made it.
func ownedLabels() map[string]string { return map[string]string{engineLabel: engineRole} }

func owned(labels map[string]string) bool { return labels[engineLabel] == engineRole }

func (d *dockerEngine) labels() map[string]string { return map[string]string{engineLabel: d.spec.role} }

func (d *dockerEngine) mine(labels map[string]string) bool { return labels[engineLabel] == d.spec.role }

func (d *dockerEngine) Status(ctx context.Context) EngineStatus {
	st := EngineStatus{State: EngineMissing, Image: d.spec.image, Version: d.spec.version}
	if err := d.ready(); err != nil {
		st.State, st.Error = EngineUnavailable, err.Error()
		return st
	}
	if _, err := d.cli.Ping(ctx); err != nil {
		st.State, st.Error = EngineUnavailable, "Docker is not answering: "+err.Error()
		return st
	}
	st.Pulled = d.imagePinned(ctx) == nil
	c, err := d.cli.ContainerInspect(ctx, d.spec.name)
	if errdefs.IsNotFound(err) {
		return st
	}
	if err != nil {
		st.State, st.Error = EngineUnavailable, err.Error()
		return st
	}
	if c.Config == nil || !d.mine(c.Config.Labels) {
		st.State = EngineForeign
		st.Error = fmt.Sprintf("a container named %s exists that the dashboard did not create", d.spec.name)
		return st
	}
	st.Container = c.ID
	if len(st.Container) > 12 {
		st.Container = st.Container[:12]
	}
	if port := boundPort(c, d.spec.port); port != "" {
		st.Address = net.JoinHostPort("127.0.0.1", port)
	}
	st.Media = mediaBinding(c)
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
// publish on every address is not one the dashboard will talk to.
func boundPort(c container.InspectResponse, port nat.Port) string {
	if c.HostConfig == nil {
		return ""
	}
	for _, binding := range c.HostConfig.PortBindings[port] {
		if binding.HostIP == "127.0.0.1" && binding.HostPort != "" {
			return binding.HostPort
		}
	}
	return ""
}

// mediaBinding reads the published UDP block back from the container.
func mediaBinding(c container.InspectResponse) string {
	if c.HostConfig == nil {
		return ""
	}
	host, first, last := "", 0, 0
	for port, bindings := range c.HostConfig.PortBindings {
		if port.Proto() != "udp" {
			continue
		}
		for _, binding := range bindings {
			n := port.Int()
			host = binding.HostIP
			if first == 0 || n < first {
				first = n
			}
			if n > last {
				last = n
			}
		}
	}
	if host == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d-%d", host, first, last)
}

func (d *dockerEngine) imagePinned(ctx context.Context) error {
	img, err := d.cli.ImageInspect(ctx, d.spec.image)
	if err != nil {
		return err
	}
	for _, digest := range img.RepoDigests {
		if digest == d.spec.image {
			return nil
		}
	}
	return fmt.Errorf("the cached %s image does not match the pinned digest", d.spec.product)
}

func (d *dockerEngine) Install(ctx context.Context, progress func(string)) error {
	if err := d.ready(); err != nil {
		return err
	}
	media := ""
	if d.spec.media != nil {
		media = d.spec.media(ctx)
	}
	st := d.Status(ctx)
	if (st.State == EngineRunning || st.State == EngineStopped) && d.stale(ctx, media) {
		progress("Making " + d.spec.name + " again with this version's configuration; its data stays")
		if err := d.removeContainer(ctx); err != nil {
			return err
		}
		st = d.Status(ctx)
	}
	switch st.State {
	case EngineUnavailable, EngineForeign:
		return errors.New(st.Error)
	case EngineRunning:
		if st.Address == "" {
			return errors.New("the engine is running without a loopback port; remove it and install again")
		}
		progress(d.spec.product + " " + d.spec.version + " is already running on " + st.Address)
		return nil
	}
	if !st.Pulled {
		progress("Pulling " + d.spec.product + " " + d.spec.version)
		if err := d.pull(ctx, progress); err != nil {
			return fmt.Errorf("pull %s: %w", d.spec.product, err)
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
	port, err := portalloc.Select(d.spec.prefer, d.spec.prefer, nil, func(p int) error { return portalloc.Available("127.0.0.1", "tcp", p) })
	if err != nil {
		return err
	}
	bindings := nat.PortMap{d.spec.port: {{HostIP: "127.0.0.1", HostPort: strconv.Itoa(port)}}}
	exposed := nat.PortSet{d.spec.port: {}}
	first := 0
	if media != "" {
		if first, err = mediaBlock(media, d.spec.mediaPorts); err != nil {
			progress("No free UDP ports on " + media + " for WebRTC; streams will use the dashboard's connection: " + err.Error())
			media = ""
		}
	}
	for p := first; media != "" && p < first+d.spec.mediaPorts; p++ {
		udp := nat.Port(strconv.Itoa(p) + "/udp")
		bindings[udp] = []nat.PortBinding{{HostIP: media, HostPort: strconv.Itoa(p)}}
		exposed[udp] = struct{}{}
	}
	if media != "" {
		progress(fmt.Sprintf("Publishing UDP %d-%d on %s for WebRTC", first, first+d.spec.mediaPorts-1, media))
	}
	labels := d.labels()
	labels[revisionLabel] = d.revision(media)
	env := slices.Clone(d.spec.env)
	if media != "" && d.spec.mediaEnv != "" {
		env = append(env, fmt.Sprintf("%s=%d:%d", d.spec.mediaEnv, first, first+d.spec.mediaPorts-1))
	}
	pids := int64(512)
	host := &container.HostConfig{
		NetworkMode:  container.NetworkMode(engineName),
		PortBindings: bindings,
		Mounts:       []mount.Mount{{Type: mount.TypeVolume, Source: d.spec.volume, Target: d.spec.mountAt}},
		CapDrop:      []string{"ALL"},
		SecurityOpt:  []string{"no-new-privileges:true"},
		// Encoding a busy screen is the heaviest thing these do, and it is
		// what caps a session's frame rate: guacd starts an encoder thread
		// per CPU for each connection. They get the ordinary share of the
		// CPU, the same as the dashboard's own containers, and no quota,
		// which would stall a frame half-encoded. Memory is still bounded.
		Resources:     container.Resources{Memory: d.spec.memory, MemorySwap: d.spec.memory, PidsLimit: &pids},
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
		LogConfig:     container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "5m", "max-file": "2"}},
	}
	config := &container.Config{
		Image:        d.spec.image,
		Hostname:     d.spec.hostname,
		Labels:       labels,
		Env:          env,
		ExposedPorts: exposed,
	}
	progress(fmt.Sprintf("Creating %s on 127.0.0.1:%d", d.spec.name, port))
	created, err := d.cli.ContainerCreate(ctx, config, host, &network.NetworkingConfig{}, nil, d.spec.name)
	if err != nil {
		return fmt.Errorf("create the %s container: %w", d.spec.product, err)
	}
	if d.spec.seed != nil {
		archive, err := d.spec.seed(media, first)
		if err != nil {
			return err
		}
		if err := d.cli.CopyToContainer(ctx, created.ID, "/", bytes.NewReader(archive), container.CopyToContainerOptions{}); err != nil {
			return fmt.Errorf("seed the %s configuration: %w", d.spec.product, err)
		}
	}
	return d.start(ctx, progress)
}

func (d *dockerEngine) start(ctx context.Context, progress func(string)) error {
	if err := d.cli.ContainerStart(ctx, d.spec.name, container.StartOptions{}); err != nil {
		return fmt.Errorf("start %s: %w", d.spec.product, err)
	}
	st := d.Status(ctx)
	if st.Address == "" {
		return fmt.Errorf("%s started without a loopback port", d.spec.product)
	}
	progress("Waiting for " + d.spec.product + " to answer on " + st.Address)
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := d.spec.probe(ctx, st.Address)
		if err == nil {
			progress(d.spec.product + " " + d.spec.version + " is ready")
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not answer: %w", d.spec.product, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func (d *dockerEngine) revision(media string) string { return d.spec.revision + "|" + media }

// stale is an engine with a revision whose container was made by another
// one, or for another media address. An engine without one never is.
func (d *dockerEngine) stale(ctx context.Context, media string) bool {
	if d.spec.revision == "" {
		return false
	}
	c, err := d.cli.ContainerInspect(ctx, d.spec.name)
	if err != nil || c.Config == nil || !d.mine(c.Config.Labels) {
		return false
	}
	return c.Config.Labels[revisionLabel] != d.revision(media)
}

func (d *dockerEngine) removeContainer(ctx context.Context) error {
	timeout := 10
	d.cli.ContainerStop(ctx, d.spec.name, container.StopOptions{Timeout: &timeout})
	if err := d.cli.ContainerRemove(ctx, d.spec.name, container.RemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("remove the %s container: %w", d.spec.product, err)
	}
	return nil
}

// mediaBlock finds a block of free UDP ports on the address, from 40000 in
// steps of the block's own size.
func mediaBlock(address string, size int) (int, error) {
	for first := 40000; first+size <= 41000; first += size {
		free := true
		for p := first; p < first+size && free; p++ {
			free = portalloc.Available(address, "udp", p) == nil
		}
		if free {
			return first, nil
		}
	}
	return 0, fmt.Errorf("none between 40000 and 41000")
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
	rc, err := d.cli.ImagePull(ctx, d.spec.image, image.PullOptions{})
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
	v, err := d.cli.VolumeInspect(ctx, d.spec.volume)
	if err == nil {
		if !d.mine(v.Labels) {
			return fmt.Errorf("a volume named %s exists that the dashboard did not create", d.spec.volume)
		}
		return nil
	}
	if !errdefs.IsNotFound(err) {
		return err
	}
	progress("Creating the volume " + d.spec.volume)
	_, err = d.cli.VolumeCreate(ctx, volume.CreateOptions{Name: d.spec.volume, Driver: "local", Labels: d.labels()})
	return err
}

// Remove takes away the container and its volume, and the shared network
// once nothing else is on it. The image stays, so installing again does not
// download it.
func (d *dockerEngine) Remove(ctx context.Context) error {
	if err := d.ready(); err != nil {
		return err
	}
	c, err := d.cli.ContainerInspect(ctx, d.spec.name)
	switch {
	case errdefs.IsNotFound(err):
	case err != nil:
		return err
	case c.Config == nil || !d.mine(c.Config.Labels):
		return fmt.Errorf("the container named %s is not the dashboard's; leaving it alone", d.spec.name)
	default:
		timeout := 10
		d.cli.ContainerStop(ctx, c.ID, container.StopOptions{Timeout: &timeout})
		if err := d.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
			return fmt.Errorf("remove the %s container: %w", d.spec.product, err)
		}
	}
	// The other engine may still be on the network; it goes with the last.
	if nw, err := d.cli.NetworkInspect(ctx, engineName, network.InspectOptions{}); err == nil && owned(nw.Labels) && len(nw.Containers) == 0 {
		if err := d.cli.NetworkRemove(ctx, nw.ID); err != nil {
			return fmt.Errorf("remove the engine network: %w", err)
		}
	}
	if v, err := d.cli.VolumeInspect(ctx, d.spec.volume); err == nil && d.mine(v.Labels) {
		if err := d.cli.VolumeRemove(ctx, v.Name, false); err != nil {
			return fmt.Errorf("remove the volume %s: %w", d.spec.volume, err)
		}
	}
	return nil
}
