package cftunnel

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// The connector is cloudflared, run as a container the dashboard owns —
// never as a process on the host — on a Docker network of its own that the
// proxy is joined to. It publishes nothing: it dials out to Cloudflare's edge
// and in to the proxy, and its metrics are read at its address on that
// network. The image is pinned by digest, so an install runs exactly the
// connector this release was tested with.
const (
	ConnectorImage   = "cloudflare/cloudflared:2025.10.0@sha256:396cd2e6f021275ad09969a1b4f1a7e62ca5349fde62781ce082bb2c18105c70"
	ConnectorVersion = "2025.10.0"
	ConnectorName    = "just-dashboard-cloudflared"
	// TunnelNetwork is the network the connector and the proxy share.
	TunnelNetwork = "just-dashboard-tunnel"

	connectorLabel = "io.justdashboard.cloudflare-tunnel"
	tokenLabel     = connectorLabel + ".token"
	originLabel    = connectorLabel + ".origin"
	metricsPort    = 20241
)

// ConnectorState is what Docker says about the connector now.
type ConnectorState struct {
	// State is Docker's word — running, restarting, exited — or "missing".
	State        string `json:"state"`
	ID           string `json:"id,omitempty"`
	Image        string `json:"image,omitempty"`
	Version      string `json:"version"`
	StartedAt    string `json:"startedAt,omitempty"`
	Address      string `json:"address,omitempty"`
	RestartCount int    `json:"restartCount,omitempty"`
	ExitCode     int    `json:"exitCode,omitempty"`
	// Foreign is a container of the connector's name the dashboard did not
	// make, which is never touched.
	Foreign     bool   `json:"foreign,omitempty"`
	TokenDigest string `json:"-"`
	Origin      string `json:"origin,omitempty"`
}

// Readiness is cloudflared's own /ready: how many connections to
// Cloudflare's edge it holds.
type Readiness struct {
	Status           int    `json:"status"`
	ReadyConnections int    `json:"readyConnections"`
	ConnectorID      string `json:"connectorId,omitempty"`
}

// Connector is the container's driver; tests replace it.
type Connector interface {
	Inspect(ctx context.Context) (ConnectorState, error)
	Run(ctx context.Context, token string, origin Origin) error
	Remove(ctx context.Context) error
	Ready(ctx context.Context, state ConnectorState) (Readiness, error)
	// LastError is cloudflared's last complaint in its own log, in its
	// own words: the reason a tunnel is down.
	LastError(ctx context.Context) string
}

// DockerConnector drives the connector through the docker command line,
// each call an argv. The token reaches the container through the docker
// client's own environment (`-e TUNNEL_TOKEN` with no value), never an
// argument a process list would show.
type DockerConnector struct {
	Image string
	Name  string
	// Network is the network the connector joins; TunnelNetwork unless a
	// test names its own.
	Network string
	// Labels are added to the container and the network, for a test to
	// find and remove what it made.
	Labels map[string]string
}

func NewDockerConnector() *DockerConnector {
	return &DockerConnector{Image: ConnectorImage, Name: ConnectorName, Network: TunnelNetwork}
}

func (d *DockerConnector) docker(ctx context.Context, args ...string) ([]byte, error) {
	var stderr bytes.Buffer
	command := hostexec.Command(ctx, "docker", args...)
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		return out, fmt.Errorf("docker %s: %s", args[0], strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

type inspected struct {
	ID    string
	Name  string
	State struct {
		Status    string
		StartedAt string
		ExitCode  int
	}
	RestartCount int
	Config       struct {
		Image  string
		Labels map[string]string
	}
	NetworkSettings struct {
		Networks map[string]struct{ IPAddress string }
	}
}

func (d *DockerConnector) Inspect(ctx context.Context) (ConnectorState, error) {
	out := ConnectorState{State: "missing", Version: ConnectorVersion}
	raw, err := hostexec.Command(ctx, "docker", "inspect", "--type", "container", d.Name).Output()
	if err != nil {
		if !hostexec.Available("docker") {
			return out, errors.New("Docker is not available on this host, so the connector cannot run")
		}
		return out, nil
	}
	var containers []inspected
	if json.Unmarshal(raw, &containers) != nil || len(containers) != 1 {
		return out, errors.New("the connector's container could not be read")
	}
	c := containers[0]
	out.State, out.ID, out.Image = c.State.Status, c.ID, c.Config.Image
	out.StartedAt, out.ExitCode, out.RestartCount = c.State.StartedAt, c.State.ExitCode, c.RestartCount
	out.Foreign = c.Config.Labels[connectorLabel] != "connector"
	out.TokenDigest, out.Origin = c.Config.Labels[tokenLabel], c.Config.Labels[originLabel]
	if network, ok := c.NetworkSettings.Networks[d.Network]; ok {
		out.Address = network.IPAddress
	}
	return out, nil
}

// ensureNetwork makes the tunnel's network, a bridge of its own: the
// connector needs a way out to Cloudflare, and the proxy is joined to it.
func (d *DockerConnector) ensureNetwork(ctx context.Context) error {
	if _, err := hostexec.Command(ctx, "docker", "network", "inspect", d.Network).Output(); err == nil {
		return nil
	}
	args := []string{"network", "create", "--driver", "bridge", "--label", connectorLabel + "=network"}
	for key, value := range d.Labels {
		args = append(args, "--label", key+"="+value)
	}
	_, err := d.docker(ctx, append(args, d.Network)...)
	return err
}

// Run makes the connector again for this token and origin: removed if it is
// the dashboard's, then created and started. A container of the name the
// dashboard did not make is refused.
func (d *DockerConnector) Run(ctx context.Context, token string, origin Origin) error {
	current, err := d.Inspect(ctx)
	if err != nil {
		return err
	}
	if current.State != "missing" {
		if current.Foreign {
			return fmt.Errorf("a container named %s that this dashboard did not make is in the way; rename or remove it", d.Name)
		}
		if _, err := d.docker(ctx, "rm", "-f", current.ID); err != nil {
			return err
		}
	}
	if err := d.ensureNetwork(ctx); err != nil {
		return err
	}
	args := []string{"run", "-d", "--name", d.Name, "--restart", "unless-stopped",
		"--network", d.Network,
		"--label", connectorLabel + "=connector",
		"--label", tokenLabel + "=" + TokenDigest(token),
		"--label", originLabel + "=" + origin.Service,
		"--memory", "256m", "--security-opt", "no-new-privileges",
		"-e", "TUNNEL_TOKEN",
	}
	for key, value := range d.Labels {
		args = append(args, "--label", key+"="+value)
	}
	if origin.HostGateway {
		args = append(args, "--add-host", "host.docker.internal:host-gateway")
	}
	args = append(args, d.Image, "tunnel", "--no-autoupdate", "--metrics", "0.0.0.0:"+strconv.Itoa(metricsPort), "run")
	var stderr bytes.Buffer
	command := hostexec.Command(ctx, "docker", args...)
	command.Env = append(os.Environ(), "TUNNEL_TOKEN="+strings.TrimSpace(token))
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("the connector could not be started: %s", strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (d *DockerConnector) Remove(ctx context.Context) error {
	current, err := d.Inspect(ctx)
	if err != nil || current.State == "missing" {
		return err
	}
	if current.Foreign {
		return fmt.Errorf("the container named %s is not this dashboard's, so it is left alone", d.Name)
	}
	_, err = d.docker(ctx, "rm", "-f", current.ID)
	return err
}

// Ready asks cloudflared's metrics server, at the connector's address on
// the tunnel network: 200 with the connections it holds, or 503 with none.
func (d *DockerConnector) Ready(ctx context.Context, state ConnectorState) (Readiness, error) {
	if state.State != "running" || net.ParseIP(state.Address) == nil {
		return Readiness{}, fmt.Errorf("the connector is %s", state.State)
	}
	return readReady(ctx, "http://"+net.JoinHostPort(state.Address, strconv.Itoa(metricsPort))+"/ready")
}

func readReady(ctx context.Context, address string) (Readiness, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return Readiness{}, err
	}
	response, err := (&http.Client{Transport: &http.Transport{Proxy: nil}}).Do(request)
	if err != nil {
		return Readiness{}, errors.New("the connector's metrics server does not answer")
	}
	defer response.Body.Close()
	var ready Readiness
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&ready); err != nil {
		return Readiness{Status: response.StatusCode}, nil
	}
	return ready, nil
}

// cloudflaredLine is one line of cloudflared's log: a timestamp, a level and
// the message, with its key=value fields after it.
var cloudflaredLine = regexp.MustCompile(`^\S+\s+(ERR|WRN|INF|DBG)\s+(.*)$`)

func (d *DockerConnector) LastError(ctx context.Context) string {
	raw, err := hostexec.Command(ctx, "docker", "logs", "--tail", "200", d.Name).CombinedOutput()
	if err != nil {
		return ""
	}
	return lastComplaint(raw)
}

// lastComplaint is the newest error cloudflared logged, else the newest line
// of a connector that exited — "Provided Tunnel token is not valid." is
// printed bare, before any logger is set up.
func lastComplaint(raw []byte) string {
	last, bare := "", ""
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if m := cloudflaredLine.FindStringSubmatch(line); m != nil {
			if m[1] == "ERR" {
				last = m[2]
			}
			continue
		}
		if !strings.HasPrefix(line, "See '") {
			bare = line
		}
	}
	if last != "" {
		return redactToken(last)
	}
	return redactToken(bare)
}

// tokenish is anything long enough to be a token; cloudflared does not log
// one, and if a future one did this keeps it off the page.
var tokenish = regexp.MustCompile(`[A-Za-z0-9+/=_-]{80,}`)

func redactToken(line string) string {
	if len(line) > 400 {
		line = line[:400] + "…"
	}
	return tokenish.ReplaceAllString(line, "[redacted]")
}
