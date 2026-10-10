package dnsservice

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/go-connections/nat"
)

const dnsOwnerLabel = "io.justdashboard.dns.owner"
const dnsProvisionLabel = "io.justdashboard.dns.provision"

type dockerRuntime struct {
	cli *client.Client
	err error
}

func NewDockerRuntime(host string) ProvisionRuntime {
	cli, err := client.NewClientWithOpts(client.WithHost(host), client.WithAPIVersionNegotiation())
	return &dockerRuntime{cli, err}
}
func (d *dockerRuntime) ready() error {
	if d == nil || d.cli == nil || d.err != nil {
		return errors.New("Docker DNS provision runtime is unavailable")
	}
	return nil
}
func (d *dockerRuntime) Close() error {
	if d == nil || d.cli == nil {
		return nil
	}
	return d.cli.Close()
}
func (d *dockerRuntime) Image(ctx context.Context, engine Engine) (string, error) {
	if err := d.ready(); err != nil {
		return "", err
	}
	ref := pinnedImages[engine]
	if ref == "" {
		return "", errors.New("native DNS engine has no reviewed image")
	}
	image, err := d.cli.ImageInspect(ctx, ref)
	if err != nil {
		return "", errors.New("the reviewed immutable native DNS image must already be cached; this action never pulls an image")
	}
	found := false
	for _, digest := range image.RepoDigests {
		if digest == ref {
			found = true
		}
	}
	if !found || !strings.HasPrefix(image.ID, "sha256:") {
		return "", errors.New("cached native DNS image identity does not match the reviewed digest")
	}
	return image.ID, nil
}
func ownedLabels(s provisionSpec) map[string]string {
	return map[string]string{dnsOwnerLabel: s.Owner, dnsProvisionLabel: s.ID}
}
func owned(labels map[string]string, s provisionSpec) bool {
	return labels[dnsOwnerLabel] == s.Owner && labels[dnsProvisionLabel] == s.ID
}
func volumeTargets(engine Engine) []string {
	switch engine {
	case AdGuard:
		return []string{"/opt/adguardhome/conf", "/opt/adguardhome/work"}
	case PiHole:
		return []string{"/etc/pihole", "/var/log/pihole"}
	default:
		return []string{"/etc/dns", "/var/log/technitium/dns"}
	}
}
func nativeWebPort(engine Engine) int {
	switch engine {
	case AdGuard:
		return 3000
	case PiHole:
		return 80
	default:
		return 5380
	}
}

func nativeCapabilities(engine Engine) []string {
	caps := []string{"CHOWN", "DAC_OVERRIDE", "FOWNER", "SETGID", "SETUID", "NET_BIND_SERVICE", "KILL"}
	if engine == PiHole {
		// The pinned entrypoint sets FTL file capabilities and uses capsh to
		// enter its native pihole account. DHCP, raw sockets and host privilege
		// remain unavailable; these two capabilities stay inside its own bridge.
		caps = append(caps, "SETFCAP", "SETPCAP")
	}
	return caps
}
func nativeEnvironment(s provisionSpec, subnet string) []string {
	switch s.Request.Engine {
	case PiHole:
		return []string{"TZ=UTC", "WEBPASSWORD_FILE=jd_dns_password"}
	case Technitium:
		return []string{"DNS_SERVER_DOMAIN=dns." + s.ID + ".invalid", "DNS_SERVER_ADMIN_PASSWORD_FILE=/run/secrets/jd_dns_password", "DNS_SERVER_WEB_SERVICE_HTTP_PORT=5380", "DNS_SERVER_WEB_SERVICE_LOCAL_ADDRESSES=0.0.0.0", "DNS_SERVER_RECURSION=UseSpecifiedNetworkACL", "DNS_SERVER_RECURSION_NETWORK_ACL=" + subnet, "DNS_SERVER_FORWARDERS=" + strings.Join(s.Request.Upstreams, ","), "DNS_SERVER_FORWARDER_PROTOCOL=Udp", "DNS_SERVER_ENABLE_BLOCKING=true", "DNS_SERVER_LOG_FOLDER_PATH=/var/log/technitium/dns"}
	default:
		return nil
	}
}

func (d *dockerRuntime) Prepare(ctx context.Context, s provisionSpec, r ProvisionResources, journal func(ProvisionResources) error) (ProvisionResources, error) {
	if err := d.ready(); err != nil {
		return r, err
	}
	image, err := d.Image(ctx, s.Request.Engine)
	if err != nil || image != s.ImageID {
		return r, errors.New("reviewed cached image changed before resource creation")
	}
	if _, err = d.cli.ContainerInspect(ctx, r.ContainerName); !errdefs.IsNotFound(err) {
		return r, errors.New("DNS container name is already present or cannot be inspected")
	}
	if _, err = d.cli.NetworkInspect(ctx, r.NetworkName, network.InspectOptions{}); !errdefs.IsNotFound(err) {
		return r, errors.New("DNS network name is already present or cannot be inspected")
	}
	for _, name := range r.Volumes {
		if _, err = d.cli.VolumeInspect(ctx, name); !errdefs.IsNotFound(err) {
			return r, errors.New("DNS volume name is already present or cannot be inspected")
		}
	}
	created, err := d.cli.NetworkCreate(ctx, r.NetworkName, network.CreateOptions{Driver: "bridge", Labels: ownedLabels(s)})
	if err != nil {
		return r, errors.New("owned DNS bridge creation failed")
	}
	r.NetworkID, r.Phase = created.ID, "network_created"
	if err = journal(r); err != nil {
		return r, err
	}
	for i, name := range r.Volumes {
		if _, err = d.cli.VolumeCreate(ctx, volume.CreateOptions{Name: name, Driver: "local", Labels: ownedLabels(s)}); err != nil {
			return r, errors.New("owned DNS named-volume creation failed")
		}
		r.Phase = fmt.Sprintf("volume_%d_created", i+1)
		if err = journal(r); err != nil {
			return r, err
		}
	}
	nw, err := d.cli.NetworkInspect(ctx, r.NetworkID, network.InspectOptions{})
	if err != nil || len(nw.IPAM.Config) != 1 || nw.IPAM.Config[0].Subnet == "" {
		return r, errors.New("owned DNS bridge subnet is unreadable")
	}
	web := nat.Port(strconv.Itoa(nativeWebPort(s.Request.Engine)) + "/tcp")
	ports := nat.PortSet{web: {}, "53/tcp": {}, "53/udp": {}}
	bindings := nat.PortMap{web: {{HostIP: "127.0.0.1", HostPort: strconv.Itoa(s.Request.ManagementPort)}}, "53/tcp": {{HostIP: "127.0.0.1", HostPort: strconv.Itoa(s.Request.DNSPort)}}, "53/udp": {{HostIP: "127.0.0.1", HostPort: strconv.Itoa(s.Request.DNSPort)}}}
	mounts := []mount.Mount{}
	targets := volumeTargets(s.Request.Engine)
	for i, name := range r.Volumes {
		mounts = append(mounts, mount.Mount{Type: mount.TypeVolume, Source: name, Target: targets[i]})
	}
	pids := int64(128)
	host := &container.HostConfig{NetworkMode: container.NetworkMode(r.NetworkID), PortBindings: bindings, Mounts: mounts, CapDrop: []string{"ALL"}, CapAdd: nativeCapabilities(s.Request.Engine), SecurityOpt: []string{"no-new-privileges:true"}, Resources: container.Resources{Memory: s.Request.MemoryMiB << 20, MemorySwap: s.Request.MemoryMiB << 20, NanoCPUs: int64(s.Request.CPUs * 1e9), PidsLimit: &pids}, RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled}, LogConfig: container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "5m", "max-file": "2"}}}
	config := &container.Config{Image: s.Image, Hostname: "dns-" + s.ID, Labels: ownedLabels(s), Env: nativeEnvironment(s, nw.IPAM.Config[0].Subnet), ExposedPorts: ports}
	result, err := d.cli.ContainerCreate(ctx, config, host, &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{r.NetworkID: {NetworkID: r.NetworkID}}}, nil, r.ContainerName)
	if err != nil {
		return r, errors.New("owned DNS container creation failed")
	}
	r.ContainerID, r.Phase = result.ID, "container_created"
	if err = journal(r); err != nil {
		return r, err
	}
	files, err := nativeSeedFiles(s, nw.IPAM.Config[0].Subnet)
	if err != nil {
		return r, err
	}
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	if s.Request.Engine != AdGuard {
		if err = writer.WriteHeader(&tar.Header{Name: "run/secrets", Typeflag: tar.TypeDir, Mode: 0700}); err != nil {
			return r, err
		}
	}
	for path, data := range files {
		if err = writer.WriteHeader(&tar.Header{Name: path, Mode: 0600, Size: int64(len(data))}); err != nil {
			return r, err
		}
		if _, err = writer.Write(data); err != nil {
			return r, err
		}
	}
	if err = writer.Close(); err != nil {
		return r, err
	}
	if err = d.cli.CopyToContainer(ctx, r.ContainerID, "/", &archive, container.CopyToContainerOptions{}); err != nil {
		return r, errors.New("private native DNS bootstrap configuration could not be seeded")
	}
	r.Phase = "prepared"
	return r, journal(r)
}

func (d *dockerRuntime) containerIdentity(ctx context.Context, s provisionSpec, r ProvisionResources) (container.InspectResponse, error) {
	if err := d.ready(); err != nil {
		return container.InspectResponse{}, err
	}
	id := r.ContainerID
	if id == "" {
		id = r.ContainerName
	}
	info, err := d.cli.ContainerInspect(ctx, id)
	if err != nil {
		return info, err
	}
	if info.Config == nil || info.HostConfig == nil || info.NetworkSettings == nil || info.State == nil || !owned(info.Config.Labels, s) || info.Name != "/"+r.ContainerName || info.Image != s.ImageID || info.Config.Image != s.Image || r.ContainerID != "" && info.ID != r.ContainerID {
		return info, errors.New("owned DNS container identity changed")
	}
	if info.HostConfig.Privileged || info.HostConfig.NetworkMode == "host" || info.HostConfig.PidMode != "" || len(info.HostConfig.Devices) != 0 || len(info.HostConfig.Binds) != 0 || len(info.Mounts) != 2 || info.HostConfig.Memory != s.Request.MemoryMiB<<20 || info.HostConfig.NanoCPUs != int64(s.Request.CPUs*1e9) {
		return info, errors.New("owned DNS container isolation or resource contract changed")
	}
	if info.HostConfig.NetworkMode != container.NetworkMode(r.NetworkID) || len(info.HostConfig.DeviceRequests) != 0 || len(info.HostConfig.DeviceCgroupRules) != 0 || info.HostConfig.MemorySwap != s.Request.MemoryMiB<<20 || info.HostConfig.PidsLimit == nil || *info.HostConfig.PidsLimit != 128 || len(info.HostConfig.CapDrop) != 1 || info.HostConfig.CapDrop[0] != "ALL" || !sameCapabilities(info.HostConfig.CapAdd, nativeCapabilities(s.Request.Engine)) || !reflect.DeepEqual(info.HostConfig.SecurityOpt, []string{"no-new-privileges:true"}) {
		return info, errors.New("owned DNS privilege or resource bound changed")
	}
	expectedPorts := nat.PortMap{nat.Port(strconv.Itoa(nativeWebPort(s.Request.Engine)) + "/tcp"): {{HostIP: "127.0.0.1", HostPort: strconv.Itoa(s.Request.ManagementPort)}}, "53/tcp": {{HostIP: "127.0.0.1", HostPort: strconv.Itoa(s.Request.DNSPort)}}, "53/udp": {{HostIP: "127.0.0.1", HostPort: strconv.Itoa(s.Request.DNSPort)}}}
	if !reflect.DeepEqual(info.HostConfig.PortBindings, expectedPorts) || len(info.NetworkSettings.Networks) != 1 {
		return info, errors.New("owned DNS loopback publication or bridge membership changed")
	}
	for _, nw := range info.NetworkSettings.Networks {
		if nw.NetworkID != r.NetworkID && (info.State.Running || nw.NetworkID != "") {
			return info, errors.New("owned DNS bridge identity changed")
		}
	}
	targets := volumeTargets(s.Request.Engine)
	for _, m := range info.Mounts {
		matched := false
		for i, name := range r.Volumes {
			if m.Type == mount.TypeVolume && m.Name == name && m.Destination == targets[i] && m.RW {
				matched = true
			}
		}
		if !matched {
			return info, errors.New("owned DNS persistent mount identity changed")
		}
	}
	return info, nil
}

func sameCapabilities(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	seen := map[string]bool{}
	for _, value := range actual {
		seen[strings.TrimPrefix(value, "CAP_")] = true
	}
	for _, value := range expected {
		if !seen[value] {
			return false
		}
	}
	return true
}

func (d *dockerRuntime) Start(ctx context.Context, s provisionSpec, r ProvisionResources) error {
	if _, err := d.containerIdentity(ctx, s, r); err != nil {
		return errors.New("owned DNS start identity is unavailable or changed")
	}
	if err := d.cli.ContainerStart(ctx, r.ContainerID, container.StartOptions{}); err != nil {
		return errors.New("owned DNS container did not start")
	}
	return nil
}
func (d *dockerRuntime) Verify(ctx context.Context, s provisionSpec, r ProvisionResources) error {
	info, err := d.containerIdentity(ctx, s, r)
	if err != nil || !info.State.Running {
		return errors.New("owned DNS running container identity could not be established")
	}
	return nil
}
func (d *dockerRuntime) Activate(ctx context.Context, s provisionSpec, r ProvisionResources) error {
	if err := d.Verify(ctx, s, r); err != nil {
		return err
	}
	if _, err := d.cli.ContainerUpdate(ctx, r.ContainerID, container.UpdateConfig{RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped}}); err != nil {
		return errors.New("owned DNS persistent restart policy could not be established")
	}
	return nil
}
func (d *dockerRuntime) RemoveBootstrapSecret(ctx context.Context, s provisionSpec, r ProvisionResources) error {
	if s.Request.Engine == AdGuard {
		return nil
	}
	if _, err := d.containerIdentity(ctx, s, r); err != nil {
		return errors.New("owned DNS bootstrap cleanup identity changed")
	}
	created, err := d.cli.ContainerExecCreate(ctx, r.ContainerID, container.ExecOptions{User: "0", Cmd: []string{"rm", "-f", "--", "/run/secrets/jd_dns_password"}})
	if err != nil {
		return errors.New("owned DNS bootstrap cleanup could not be created")
	}
	if err = d.cli.ContainerExecStart(ctx, created.ID, container.ExecStartOptions{Detach: true}); err != nil {
		return errors.New("owned DNS bootstrap cleanup did not start")
	}
	for {
		result, e := d.cli.ContainerExecInspect(ctx, created.ID)
		if e != nil {
			return errors.New("owned DNS bootstrap cleanup readback failed")
		}
		if !result.Running {
			if result.ExitCode != 0 {
				return errors.New("owned DNS bootstrap cleanup failed")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (d *dockerRuntime) Destroy(ctx context.Context, s provisionSpec, r ProvisionResources) error {
	if err := d.ready(); err != nil {
		return err
	}
	info, err := d.containerIdentity(ctx, s, r)
	exists := err == nil
	if err != nil && !errdefs.IsNotFound(err) {
		return errors.New("DNS cleanup refuses an unreadable or changed container identity")
	}
	for _, name := range r.Volumes {
		v, e := d.cli.VolumeInspect(ctx, name)
		if e != nil && !errdefs.IsNotFound(e) {
			return errors.New("DNS cleanup volume identity is unreadable")
		}
		if e == nil && (v.Name != name || v.Driver != "local" || !owned(v.Labels, s) || len(v.Options) != 0) {
			return errors.New("DNS cleanup refuses changed or foreign volume identity")
		}
	}
	nw, e := d.cli.NetworkInspect(ctx, r.NetworkName, network.InspectOptions{})
	networkExists := e == nil
	if e != nil && !errdefs.IsNotFound(e) {
		return errors.New("DNS cleanup bridge identity is unreadable")
	}
	if networkExists {
		if !owned(nw.Labels, s) || nw.Driver != "bridge" || nw.Ingress || r.NetworkID != "" && nw.ID != r.NetworkID {
			return errors.New("DNS cleanup refuses changed or foreign bridge identity")
		}
		for id := range nw.Containers {
			if !exists || id != info.ID {
				return errors.New("DNS cleanup refuses a bridge with foreign attached containers")
			}
		}
	}
	if exists {
		seconds := 10
		if err = d.cli.ContainerStop(ctx, info.ID, container.StopOptions{Timeout: &seconds}); err != nil {
			return errors.New("owned DNS container could not be stopped")
		}
		if err = d.cli.ContainerRemove(ctx, info.ID, container.RemoveOptions{}); err != nil {
			return errors.New("owned DNS container could not be removed")
		}
	}
	for _, name := range r.Volumes {
		if err = d.cli.VolumeRemove(ctx, name, false); err != nil && !errdefs.IsNotFound(err) {
			return errors.New("owned DNS volume could not be removed")
		}
	}
	if networkExists {
		if err = d.cli.NetworkRemove(ctx, nw.ID); err != nil {
			return errors.New("owned DNS bridge could not be removed")
		}
	}
	return nil
}
