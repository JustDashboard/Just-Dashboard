package dockerx

import (
	"context"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/pkg/stdcopy"
)

type Port struct {
	IP          string `json:"ip,omitempty"`
	PrivatePort uint16 `json:"privatePort"`
	PublicPort  uint16 `json:"publicPort,omitempty"`
	Type        string `json:"type"`
}

type Container struct {
	ID           string            `json:"id"`
	Names        []string          `json:"names"`
	Name         string            `json:"name"`
	Image        string            `json:"image"`
	ImageID      string            `json:"imageId"`
	Command      string            `json:"command"`
	State        string            `json:"state"`
	Status       string            `json:"status"`
	Health       string            `json:"health,omitempty"`
	CreatedAt    time.Time         `json:"createdAt"`
	StartedAt    *time.Time        `json:"startedAt,omitempty"`
	UptimeSecond int64             `json:"uptimeSeconds"`
	Ports        []Port            `json:"ports"`
	Labels       map[string]string `json:"labels"`
	Networks     []string          `json:"networks"`
	SizeRw       int64             `json:"sizeRw,omitempty"`
	ComposeStack string            `json:"composeStack,omitempty"`
	ComposeSvc   string            `json:"composeService,omitempty"`
	// Mounts comes free with the listing. Carried because the volumes view
	// needs "which containers use this volume" for every volume at once, and
	// the alternative is an inspect per container on every poll — sixty round
	// trips to the socket to draw one table.
	Mounts []MountPoint `json:"mounts,omitempty"`

	// Exposure is the port list with its meaning attached: which of these
	// bindings reach beyond this server and which do not. Computed once here
	// rather than in each of the four components that render a port, all of
	// which previously drew 0.0.0.0:5432 and 127.0.0.1:5432 the same way.
	Exposure []PortExposure `json:"exposure"`

	// What the container was told it may use, and whether anything is watching
	// it. Filled by inspection for running containers only — a stopped
	// container is not consuming anything, and inspecting every container on
	// a host with a hundred of them to draw a table nobody is reading is the
	// cost this listing exists to avoid.
	MemoryLimit   int64   `json:"memoryLimit,omitempty"`
	CPULimit      float64 `json:"cpuLimit,omitempty"`
	HasHealthchk  bool    `json:"hasHealthcheck"`
	RestartPolicy string  `json:"restartPolicy,omitempty"`
	Privileged    bool    `json:"privileged,omitempty"`
	// Inspected says whether the three fields above are an answer or an
	// absence, so the UI never renders "no memory limit" for a container it
	// simply did not look at.
	Inspected bool `json:"inspected"`
}

func (c *Client) ListContainers(ctx context.Context, all bool) ([]Container, error) {
	return c.listContainers(ctx, container.ListOptions{All: all})
}

// ListContainersWithLabels filters at the daemon before uptime/health inspection,
// so observing one deployment does not inspect every running container on the host.
func (c *Client) ListContainersWithLabels(ctx context.Context, labels map[string]string) ([]Container, error) {
	args := filters.NewArgs()
	for key, value := range labels {
		args.Add("label", key+"="+value)
	}
	return c.listContainers(ctx, container.ListOptions{All: true, Filters: args})
}

func (c *Client) listContainers(ctx context.Context, options container.ListOptions) ([]Container, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	items, err := cli.ContainerList(ctx, options)
	if err != nil {
		return nil, err
	}
	out := make([]Container, 0, len(items))
	for _, it := range items {
		cn := Container{
			ID:        it.ID,
			Names:     trimNames(it.Names),
			Image:     it.Image,
			ImageID:   it.ImageID,
			Command:   it.Command,
			State:     it.State,
			Status:    it.Status,
			CreatedAt: time.Unix(it.Created, 0).UTC(),
			Labels:    it.Labels,
			SizeRw:    it.SizeRw,
			Ports:     make([]Port, 0, len(it.Ports)),
			Networks:  []string{},
		}
		if len(cn.Names) > 0 {
			cn.Name = cn.Names[0]
		}
		if cn.Labels == nil {
			cn.Labels = map[string]string{}
		}
		for _, p := range it.Ports {
			cn.Ports = append(cn.Ports, Port{IP: p.IP, PrivatePort: p.PrivatePort, PublicPort: p.PublicPort, Type: p.Type})
		}
		for _, m := range it.Mounts {
			cn.Mounts = append(cn.Mounts, MountPoint{
				Type: string(m.Type), Name: m.Name, Source: m.Source,
				Destination: m.Destination, Mode: m.Mode, RW: m.RW,
			})
		}
		if it.NetworkSettings != nil {
			for name := range it.NetworkSettings.Networks {
				cn.Networks = append(cn.Networks, name)
			}
			sort.Strings(cn.Networks)
		}
		cn.ComposeStack = it.Labels["com.docker.compose.project"]
		cn.ComposeSvc = it.Labels["com.docker.compose.service"]
		out = append(out, cn)
	}
	// Running first, then by name — the same ordering Portainer uses, and the
	// one that puts the containers an operator is likely to act on at the top.
	sort.Slice(out, func(i, j int) bool {
		if (out[i].State == "running") != (out[j].State == "running") {
			return out[i].State == "running"
		}
		return out[i].Name < out[j].Name
	})
	c.enrichUptime(ctx, out)
	return out, nil
}

// enrichUptime fills in what the listing endpoint cannot say.
//
// The list reports uptime as a human string ("Up 3 days"), which is useless
// for sorting or charts, and says nothing at all about limits, health checks
// or restart policy. All of it comes from one inspect per running container —
// the same call that was already being made for the timestamp, so the extra
// facts are free.
//
// Only running containers are inspected. A stopped one is consuming nothing,
// and inspecting a hundred of them to draw a table would make the page slower
// for information nobody is reading; `Inspected` marks the difference so the
// UI never renders an absence as an answer.
func (c *Client) enrichUptime(ctx context.Context, list []Container) {
	cli, err := c.api()
	if err != nil {
		return
	}
	for i := range list {
		list[i].Exposure = DescribePorts(list[i].Ports)
		if list[i].State != "running" {
			continue
		}
		insp, err := cli.ContainerInspect(ctx, list[i].ID)
		if err != nil || insp.State == nil {
			continue
		}
		list[i].Inspected = true
		if started, err := time.Parse(time.RFC3339Nano, insp.State.StartedAt); err == nil {
			s := started.UTC()
			list[i].StartedAt = &s
			list[i].UptimeSecond = int64(time.Since(started).Seconds())
		}
		if insp.State.Health != nil {
			list[i].Health = insp.State.Health.Status
		}
		list[i].HasHealthchk = healthFactsOf(insp).hasCheck
		if insp.HostConfig != nil {
			list[i].MemoryLimit = insp.HostConfig.Memory
			list[i].RestartPolicy = string(insp.HostConfig.RestartPolicy.Name)
			list[i].Privileged = insp.HostConfig.Privileged
			switch {
			case insp.HostConfig.NanoCPUs > 0:
				list[i].CPULimit = float64(insp.HostConfig.NanoCPUs) / 1e9
			case insp.HostConfig.CPUQuota > 0 && insp.HostConfig.CPUPeriod > 0:
				list[i].CPULimit = float64(insp.HostConfig.CPUQuota) / float64(insp.HostConfig.CPUPeriod)
			}
		}
	}
}

func trimNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, strings.TrimPrefix(n, "/"))
	}
	return out
}

type ContainerDetail struct {
	Container
	Env         []string          `json:"env"`
	Mounts      []MountPoint      `json:"mounts"`
	NetworkMode string            `json:"networkMode"`
	NetworkList []NetworkBinding  `json:"networkDetails"`
	RestartPol  string            `json:"restartPolicy"`
	Privileged  bool              `json:"privileged"`
	CapAdd      []string          `json:"capAdd"`
	LogPath     string            `json:"logPath"`
	Platform    string            `json:"platform"`
	ExitCode    int               `json:"exitCode"`
	OOMKilled   bool              `json:"oomKilled,omitempty"`
	Error       string            `json:"error,omitempty"`
	RestartNum  int               `json:"restartCount"`
	Args        []string          `json:"args"`
	Entrypoint  []string          `json:"entrypoint"`
	WorkingDir  string            `json:"workingDir"`
	User        string            `json:"user"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type MountPoint struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Mode        string `json:"mode"`
	RW          bool   `json:"rw"`
}

type NetworkBinding struct {
	Name       string   `json:"name"`
	IPAddress  string   `json:"ipAddress"`
	Gateway    string   `json:"gateway"`
	MacAddress string   `json:"macAddress"`
	Aliases    []string `json:"aliases"`
	NetworkID  string   `json:"networkId"`
}

// RedactedEnvValue replaces a credential-shaped environment value. It is a
// fixed string rather than a length-preserving mask so the placeholder cannot
// leak how long the original was.
const RedactedEnvValue = "\u2022\u2022\u2022 hidden"

// secretEnvHints are substrings that mark an environment variable as holding a
// credential. Matching is deliberately broad and substring-based: masking a
// value that did not need masking costs an operator one extra click, while the
// reverse hands out a key.
var secretEnvHints = []string{
	"SECRET", "PASSWORD", "PASSWD", "TOKEN", "CREDENTIAL", "PRIVATE",
	"SALT", "SIGNATURE", "CIPHER", "APIKEY", "API_KEY", "AUTH", "DSN",
	"_KEY", "KEY_", "MASTER_KEY", "ACCESS", "SESSION",
	// A connection string carries the password inside it, so the variable
	// name gives no hint that the value is a credential — DATABASE_URL is the
	// single most common way a password ends up on screen.
	"JWT", "DATABASE_URL", "DB_URL", "CONNECTION_STRING", "_URI", "WEBHOOK",
}

// IsSecretEnvKey reports whether an environment variable name conventionally
// carries a credential.
func IsSecretEnvKey(name string) bool {
	upper := strings.ToUpper(name)
	if upper == "KEY" {
		return true
	}
	for _, hint := range secretEnvHints {
		if strings.Contains(upper, hint) {
			return true
		}
	}
	return false
}

// RedactEnv masks credential-shaped values in a container's environment.
//
// Container inspect output routinely carries the dashboard's own master key,
// database passwords and deploy credentials. Every authenticated role may read
// container detail, so without this a readonly account could lift the key that
// decrypts every other stored secret. Callers hand the unmasked slice only to
// a principal that already holds system.admin.
func RedactEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, found := strings.Cut(entry, "=")
		if found && IsSecretEnvKey(name) {
			out = append(out, name+"="+RedactedEnvValue)
			continue
		}
		out = append(out, entry)
	}
	return out
}

// Inspect returns the full view an operator needs when debugging a container.
// Environment variables are included deliberately — they routinely hold
// secrets, which is why the handler redacts them for anyone below
// system.admin, and why the UI keeps even an admin's copy behind a deliberate
// reveal rather than printing it on screen.
func (c *Client) Inspect(ctx context.Context, id string) (*ContainerDetail, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	insp, err := cli.ContainerInspect(ctx, id)
	if err != nil {
		return nil, err
	}
	d := &ContainerDetail{
		Container: Container{
			ID:       insp.ID,
			Name:     strings.TrimPrefix(insp.Name, "/"),
			Names:    []string{strings.TrimPrefix(insp.Name, "/")},
			ImageID:  insp.Image,
			Ports:    []Port{},
			Networks: []string{},
		},
		Env:         []string{},
		Mounts:      []MountPoint{},
		NetworkList: []NetworkBinding{},
		Args:        orEmpty(insp.Args),
		Platform:    insp.Platform,
		LogPath:     insp.LogPath,
	}
	if insp.Config != nil {
		d.Image = insp.Config.Image
		d.Env = insp.Config.Env
		d.Labels = insp.Config.Labels
		if d.Labels == nil {
			d.Labels = map[string]string{}
		}
		d.Entrypoint = orEmpty(insp.Config.Entrypoint)
		d.WorkingDir = insp.Config.WorkingDir
		d.User = insp.Config.User
		d.Command = strings.Join(insp.Config.Cmd, " ")
		d.ComposeStack = insp.Config.Labels["com.docker.compose.project"]
		d.ComposeSvc = insp.Config.Labels["com.docker.compose.service"]
	}
	if insp.State != nil {
		d.State = insp.State.Status
		d.Status = insp.State.Status
		d.ExitCode = insp.State.ExitCode
		d.OOMKilled = insp.State.OOMKilled
		d.Error = insp.State.Error
		if insp.State.Health != nil {
			d.Health = insp.State.Health.Status
		}
		if started, err := time.Parse(time.RFC3339Nano, insp.State.StartedAt); err == nil && insp.State.Running {
			s := started.UTC()
			d.StartedAt = &s
			d.UptimeSecond = int64(time.Since(started).Seconds())
		}
	}
	if created, err := time.Parse(time.RFC3339Nano, insp.Created); err == nil {
		d.CreatedAt = created.UTC()
	}
	if insp.HostConfig != nil {
		d.NetworkMode = string(insp.HostConfig.NetworkMode)
		d.RestartPol = string(insp.HostConfig.RestartPolicy.Name)
		d.Privileged = insp.HostConfig.Privileged
		d.CapAdd = orEmpty(insp.HostConfig.CapAdd)
	}
	d.RestartNum = insp.RestartCount
	for _, m := range insp.Mounts {
		d.Mounts = append(d.Mounts, MountPoint{
			Type: string(m.Type), Name: m.Name, Source: m.Source,
			Destination: m.Destination, Mode: m.Mode, RW: m.RW,
		})
	}
	if insp.NetworkSettings != nil {
		for name, ep := range insp.NetworkSettings.Networks {
			d.Networks = append(d.Networks, name)
			d.NetworkList = append(d.NetworkList, NetworkBinding{
				Name: name, IPAddress: ep.IPAddress, Gateway: ep.Gateway,
				MacAddress: ep.MacAddress, Aliases: orEmpty(ep.Aliases), NetworkID: ep.NetworkID,
			})
		}
		sort.Strings(d.Networks)
		for portSpec, bindings := range insp.NetworkSettings.Ports {
			if len(bindings) == 0 {
				// An exposed port with no host binding. Docker reports it with
				// an empty binding list, and dropping it here is why a
				// container that publishes nothing looked identical to one
				// whose ports simply were not read.
				d.Ports = append(d.Ports, Port{
					PrivatePort: uint16(portSpec.Int()),
					Type:        portSpec.Proto(),
				})
				continue
			}
			for _, b := range bindings {
				d.Ports = append(d.Ports, Port{
					IP:          b.HostIP,
					PrivatePort: uint16(portSpec.Int()),
					PublicPort:  uint16(atoiSafe(b.HostPort)),
					Type:        portSpec.Proto(),
				})
			}
		}
	}
	d.Exposure = DescribePorts(d.Ports)
	d.Inspected = true
	d.HasHealthchk = healthFactsOf(insp).hasCheck
	if insp.HostConfig != nil {
		d.MemoryLimit = insp.HostConfig.Memory
		switch {
		case insp.HostConfig.NanoCPUs > 0:
			d.CPULimit = float64(insp.HostConfig.NanoCPUs) / 1e9
		case insp.HostConfig.CPUQuota > 0 && insp.HostConfig.CPUPeriod > 0:
			d.CPULimit = float64(insp.HostConfig.CPUQuota) / float64(insp.HostConfig.CPUPeriod)
		}
	}
	return d, nil
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

type LifecycleAction string

const (
	ActionStart   LifecycleAction = "start"
	ActionStop    LifecycleAction = "stop"
	ActionRestart LifecycleAction = "restart"
	ActionPause   LifecycleAction = "pause"
	ActionUnpause LifecycleAction = "unpause"
	ActionKill    LifecycleAction = "kill"
)

func (c *Client) Lifecycle(ctx context.Context, id string, action LifecycleAction, timeoutSec *int) error {
	cli, err := c.api()
	if err != nil {
		return err
	}
	switch action {
	case ActionStart:
		return cli.ContainerStart(ctx, id, container.StartOptions{})
	case ActionStop:
		return cli.ContainerStop(ctx, id, container.StopOptions{Timeout: timeoutSec})
	case ActionRestart:
		return cli.ContainerRestart(ctx, id, container.StopOptions{Timeout: timeoutSec})
	case ActionPause:
		return cli.ContainerPause(ctx, id)
	case ActionUnpause:
		return cli.ContainerUnpause(ctx, id)
	case ActionKill:
		return cli.ContainerKill(ctx, id, "SIGKILL")
	default:
		return errUnknownAction(action)
	}
}

type unknownActionError string

func (e unknownActionError) Error() string { return "unknown container action: " + string(e) }

func errUnknownAction(a LifecycleAction) error { return unknownActionError(a) }

func (c *Client) RemoveContainer(ctx context.Context, id string, force, removeVolumes bool) error {
	cli, err := c.api()
	if err != nil {
		return err
	}
	return cli.ContainerRemove(ctx, id, container.RemoveOptions{
		Force: force, RemoveVolumes: removeVolumes,
	})
}

type LogOptions struct {
	Tail       string
	Since      string
	Until      string
	Timestamps bool
	Follow     bool
	Stdout     bool
	Stderr     bool
}

// LogLine separates the two streams so the frontend can colour stderr without
// re-parsing. Docker multiplexes them in one framed stream unless the
// container has a TTY, in which case the frames are absent.
type LogLine struct {
	Stream string `json:"stream"`
	Text   string `json:"text"`
	// Service is set only when several containers' logs are merged into one
	// feed — a compose stack followed as a whole. Without it the merged
	// stream is four processes talking over each other with no way to tell
	// which said what.
	Service string `json:"service,omitempty"`
}

// Logs returns a reader of demultiplexed log lines. The caller must drain it;
// closing the returned closer stops the follow.
func (c *Client) Logs(ctx context.Context, id string, opts LogOptions) (<-chan LogLine, io.Closer, error) {
	cli, err := c.api()
	if err != nil {
		return nil, nil, err
	}
	insp, err := cli.ContainerInspect(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if !opts.Stdout && !opts.Stderr {
		opts.Stdout, opts.Stderr = true, true
	}
	if opts.Tail == "" {
		opts.Tail = "200"
	}
	rc, err := cli.ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: opts.Stdout,
		ShowStderr: opts.Stderr,
		Since:      opts.Since,
		Until:      opts.Until,
		Timestamps: opts.Timestamps,
		Follow:     opts.Follow,
		Tail:       opts.Tail,
	})
	if err != nil {
		return nil, nil, err
	}
	ch := make(chan LogLine, 256)
	hasTTY := insp.Config != nil && insp.Config.Tty
	go func() {
		defer close(ch)
		defer rc.Close()
		if hasTTY {
			scanLines(ctx, rc, "stdout", ch)
			return
		}
		// stdcopy splits Docker's framed stream; each side is scanned into
		// lines independently through a pipe.
		outR, outW := io.Pipe()
		errR, errW := io.Pipe()
		go func() {
			_, copyErr := stdcopy.StdCopy(outW, errW, rc)
			outW.CloseWithError(copyErr)
			errW.CloseWithError(copyErr)
		}()
		done := make(chan struct{}, 2)
		go func() { scanLines(ctx, outR, "stdout", ch); done <- struct{}{} }()
		go func() { scanLines(ctx, errR, "stderr", ch); done <- struct{}{} }()
		<-done
		<-done
	}()
	return ch, rc, nil
}

func (c *Client) PruneContainers(ctx context.Context) (uint64, []string, error) {
	cli, err := c.api()
	if err != nil {
		return 0, nil, err
	}
	rep, err := cli.ContainersPrune(ctx, filters.NewArgs())
	if err != nil {
		return 0, nil, err
	}
	defer c.forgetDiskUsage()
	deleted := rep.ContainersDeleted
	if deleted == nil {
		deleted = []string{}
	}
	return rep.SpaceReclaimed, deleted, nil
}
