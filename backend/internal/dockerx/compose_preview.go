package dockerx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ComposePreviewIsolation is how a pull request's preview of a Compose
// release runs: every service the way a single preview container runs, on the
// preview's own bridge and nothing else, with storage of its own and no port
// on the host but the one the preview's route reaches.
type ComposePreviewIsolation struct {
	// Network is the preview's own bridge, which the caller created and owns.
	Network string
	// Networks are managed database networks the preview's own links join.
	Networks []string
	// Public is the service the preview's route and tailnet address reach. It
	// alone publishes, on Publish; every other service publishes nothing.
	Public  string
	Publish PortMapping
	// Volume names the preview volume standing in for a mount, from a key
	// that says what it replaces, so two services sharing a volume in
	// production share one in the preview too.
	Volume func(key string) string
	// Labels go on every service, beside the release's own.
	Labels map[string]string
}

// ComposePreviewOverride is the release override a preview starts with, the
// preview volumes it names (to be created, owned and labelled before Compose
// runs, since Compose is told they are external), and what was left out.
type ComposePreviewOverride struct {
	Override string
	Volumes  []string
	// LeftOut names each read-only path of this server a service asked for
	// and the preview does not get, as "service:target".
	LeftOut []string
}

// ErrComposePreviewRefused is what a Compose file asks for that no preview may
// have: the server's own namespaces, devices or privileges, or another
// container's network or storage.
var ErrComposePreviewRefused = errors.New("the Compose stack asks for something a preview never gets")

const composePreviewNetworkKey = "jd_preview"

// ComposePreviewOverride reads the stack as Compose resolves it, from the
// source files alone and the preview's own frozen variables, and returns the
// override that isolates every service. Only structure leaves the resolved
// document: mount targets, the preview's own names, aliases and the
// checkout's own paths — never an interpolated value.
func (c *Client) ComposePreviewOverride(ctx context.Context, spec ComposeReleaseSpec, override string, isolation ComposePreviewIsolation) (ComposePreviewOverride, error) {
	spec.OverrideFile = ""
	raw, err := c.composeReleaseConfiguration(ctx, spec)
	if err != nil {
		return ComposePreviewOverride{}, err
	}
	return composePreviewOverride(raw, override, spec.ProjectDirectory, isolation)
}

type composePreviewMount struct {
	Type     string `json:"type"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}

type composePreviewHook struct {
	Privileged bool `json:"privileged"`
}

type composePreviewService struct {
	NetworkMode       string                                 `json:"network_mode"`
	Networks          map[string]*struct{ Aliases []string } `json:"networks"`
	Volumes           []composePreviewMount                  `json:"volumes"`
	VolumesFrom       []string                               `json:"volumes_from"`
	Privileged        bool                                   `json:"privileged"`
	CapAdd            []string                               `json:"cap_add"`
	Devices           []json.RawMessage                      `json:"devices"`
	DeviceCgroupRules []string                               `json:"device_cgroup_rules"`
	GPUs              []json.RawMessage                      `json:"gpus"`
	Pid               string                                 `json:"pid"`
	Ipc               string                                 `json:"ipc"`
	Uts               string                                 `json:"uts"`
	UsernsMode        string                                 `json:"userns_mode"`
	Cgroup            string                                 `json:"cgroup"`
	CgroupParent      string                                 `json:"cgroup_parent"`
	SecurityOpt       []string                               `json:"security_opt"`
	Runtime           string                                 `json:"runtime"`
	Provider          json.RawMessage                        `json:"provider"`
	PostStart         []composePreviewHook                   `json:"post_start"`
	PreStop           []composePreviewHook                   `json:"pre_stop"`
	ContainerName     string                                 `json:"container_name"`
	ExternalLinks     []string                               `json:"external_links"`
	Secrets           []json.RawMessage                      `json:"secrets"`
	Labels            map[string]string                      `json:"labels"`
	Deploy            *struct {
		Resources struct {
			Reservations *struct {
				Devices []json.RawMessage `json:"devices"`
			} `json:"reservations"`
		} `json:"resources"`
	} `json:"deploy"`
}

type composePreviewConfiguration struct {
	Services map[string]composePreviewService `json:"services"`
	Networks map[string]json.RawMessage       `json:"networks"`
	Secrets  map[string]json.RawMessage       `json:"secrets"`
}

// composePreviewRefusal is the first thing a service asks for that a preview
// may not have, worded as what it is. Defense in depth: planning already
// refuses most of these from the files, and this reads them as Compose
// itself will apply them, merged and interpolated.
func composePreviewRefusal(service composePreviewService) string {
	hostNamespace := func(value string) bool {
		return value == "host" || strings.HasPrefix(value, "container:")
	}
	switch {
	case service.NetworkMode != "":
		return "uses network_mode " + strconv.Quote(service.NetworkMode) + ", another network namespace than the preview's own"
	case service.Privileged:
		return "runs privileged"
	case len(service.CapAdd) > 0:
		return "adds Linux capabilities"
	case len(service.Devices) > 0, len(service.DeviceCgroupRules) > 0, len(service.GPUs) > 0:
		return "uses devices of this server"
	case service.Deploy != nil && service.Deploy.Resources.Reservations != nil && len(service.Deploy.Resources.Reservations.Devices) > 0:
		return "reserves devices of this server"
	case hostNamespace(service.Pid), hostNamespace(service.Ipc), hostNamespace(service.Uts),
		service.UsernsMode == "host", service.Cgroup == "host":
		return "shares a namespace of this server or another container"
	case service.CgroupParent != "":
		return "names a cgroup parent of this server"
	case service.Runtime != "" && service.Runtime != "runc" && service.Runtime != "runsc":
		return "runs under the " + strconv.Quote(service.Runtime) + " runtime"
	case len(service.Provider) > 0 && string(service.Provider) != "null":
		return "is provided by a program on this server"
	case len(service.VolumesFrom) > 0:
		return "mounts another container's storage (volumes_from)"
	}
	for _, option := range service.SecurityOpt {
		normalized := strings.ToLower(strings.ReplaceAll(option, "=", ":"))
		if strings.HasSuffix(normalized, ":unconfined") || normalized == "label:disable" {
			return "turns off confinement (security_opt " + strconv.Quote(option) + ")"
		}
	}
	for _, hook := range append(append([]composePreviewHook(nil), service.PostStart...), service.PreStop...) {
		if hook.Privileged {
			return "runs a privileged lifecycle hook"
		}
	}
	for _, mount := range service.Volumes {
		switch mount.Type {
		case "bind", "volume", "tmpfs":
		default:
			return "mounts " + strconv.Quote(mount.Type) + " storage"
		}
		if mount.Type == "bind" && dockerSocketPath(mount.Source) {
			return "mounts the Docker socket"
		}
	}
	keys := make([]string, 0, len(service.Labels))
	for key := range service.Labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		// A preview's labels are the dashboard's word on what owns it; one the
		// stack sets itself could hide a container from cleanup or hand it to
		// another owner's sweep.
		if strings.HasPrefix(key, "io.just-dashboard.") {
			return "sets the dashboard's own label " + key
		}
	}
	return ""
}

func dockerSocketPath(path string) bool {
	clean := filepath.Clean(path)
	return clean == "/var/run/docker.sock" || clean == "/run/docker.sock" || strings.HasSuffix(clean, "/docker.sock")
}

// checkoutPath answers the bind source as the checkout's own file when it is
// one: inside the project directory, with no symbolic link anywhere on the
// way, since a link in a pull request's tree could otherwise point a
// read-only mount at any file on this server.
func checkoutPath(root, source string) (string, bool) {
	if root == "" || !filepath.IsAbs(source) {
		return "", false
	}
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(source))
	if err != nil || !filepath.IsLocal(relative) {
		return "", false
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	expected := filepath.Join(resolvedRoot, relative)
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil || resolved != expected {
		return "", false
	}
	return expected, true
}

func composeTagged(tag string, value any) (*yaml.Node, error) {
	var node yaml.Node
	if err := node.Encode(value); err != nil {
		return nil, err
	}
	node.Tag = tag
	return &node, nil
}

func composePreviewOverride(raw []byte, override, root string, isolation ComposePreviewIsolation) (ComposePreviewOverride, error) {
	var config composePreviewConfiguration
	if json.Unmarshal(raw, &config) != nil || len(config.Services) == 0 {
		return ComposePreviewOverride{}, errors.New("Compose returned no services for the preview")
	}
	if !validResourceName(isolation.Network) || isolation.Volume == nil {
		return ComposePreviewOverride{}, errors.New("invalid preview isolation")
	}
	if _, ok := config.Services[isolation.Public]; !ok {
		return ComposePreviewOverride{}, fmt.Errorf("the public service %q is not in the Compose stack", isolation.Public)
	}
	if _, taken := config.Networks[composePreviewNetworkKey]; taken {
		return ComposePreviewOverride{}, errors.New("Compose uses a network key reserved for previews")
	}
	var result map[string]any
	if yaml.Unmarshal([]byte(override), &result) != nil {
		return ComposePreviewOverride{}, errors.New("invalid deployment override")
	}
	services, ok := result["services"].(map[string]any)
	if !ok {
		return ComposePreviewOverride{}, errors.New("deployment override has no services")
	}

	networks := map[string]any{composePreviewNetworkKey: map[string]any{"external": true, "name": isolation.Network}}
	managed := make([]string, 0, len(isolation.Networks))
	for _, network := range isolation.Networks {
		if !validResourceName(network) {
			return ComposePreviewOverride{}, errors.New("invalid managed network name")
		}
		key := "jd_managed_" + network
		if _, exists := config.Networks[key]; exists {
			return ComposePreviewOverride{}, errors.New("Compose uses a reserved managed network key")
		}
		networks[key] = map[string]any{"external": true, "name": network}
		managed = append(managed, key)
	}

	names := make([]string, 0, len(config.Services))
	for name := range config.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	volumes := map[string]any{}
	var leftOut []string
	for _, name := range names {
		service := config.Services[name]
		if reason := composePreviewRefusal(service); reason != "" {
			return ComposePreviewOverride{}, fmt.Errorf("%w: service %s %s", ErrComposePreviewRefused, name, reason)
		}
		entry, ok := services[name].(map[string]any)
		if !ok {
			return ComposePreviewOverride{}, errors.New("Compose service identity changed after resolution")
		}

		mounts := []map[string]any{}
		for _, mount := range service.Volumes {
			if mount.Target == "" {
				return ComposePreviewOverride{}, fmt.Errorf("Compose service %s has a mount without a target", name)
			}
			switch {
			case mount.Type == "tmpfs":
				mounts = append(mounts, map[string]any{"type": "tmpfs", "target": mount.Target})
				continue
			case mount.Type == "bind" && mount.ReadOnly:
				if path, inside := checkoutPath(root, mount.Source); inside {
					// The commit's own file, read-only: the bytes the build
					// already read, and none of production's state.
					mounts = append(mounts, map[string]any{"type": "bind", "source": path, "target": mount.Target, "read_only": true})
				} else {
					// A read-only path of this server is production's to read,
					// not the preview's; a volume over a file would not even
					// start, so it is left out and said so.
					leftOut = append(leftOut, name+":"+mount.Target)
				}
				continue
			}
			key := "bind:" + mount.Source
			switch {
			case mount.Type == "volume" && mount.Source != "":
				key = "volume:" + mount.Source
			case mount.Type == "volume":
				key = "anonymous:" + name + ":" + mount.Target
			}
			volume := isolation.Volume(key)
			if !validResourceName(volume) {
				return ComposePreviewOverride{}, errors.New("invalid preview volume name")
			}
			volumes[volume] = map[string]any{"external": true, "name": volume}
			replacement := map[string]any{"type": "volume", "source": volume, "target": mount.Target}
			if mount.ReadOnly {
				replacement["read_only"] = true
			}
			mounts = append(mounts, replacement)
		}
		volumeNode, err := composeTagged("!override", mounts)
		if err != nil {
			return ComposePreviewOverride{}, err
		}
		entry["volumes"] = volumeNode

		aliases := []string{}
		seenAlias := map[string]bool{}
		for _, attached := range service.Networks {
			if attached == nil {
				continue
			}
			for _, alias := range attached.Aliases {
				if alias != "" && !seenAlias[alias] {
					seenAlias[alias] = true
					aliases = append(aliases, alias)
				}
			}
		}
		sort.Strings(aliases)
		own := map[string]any{}
		if len(aliases) > 0 {
			own["aliases"] = aliases
		}
		joined := map[string]any{composePreviewNetworkKey: own}
		for _, key := range managed {
			joined[key] = nil
		}
		networkNode, err := composeTagged("!override", joined)
		if err != nil {
			return ComposePreviewOverride{}, err
		}
		entry["networks"] = networkNode

		ports := []map[string]any{}
		if name == isolation.Public && isolation.Publish.ContainerPort > 0 && isolation.Publish.HostPort > 0 {
			ports = append(ports, map[string]any{
				"target": isolation.Publish.ContainerPort, "published": strconv.Itoa(isolation.Publish.HostPort),
				"host_ip": isolation.Publish.HostIP, "protocol": "tcp",
			})
		}
		portNode, err := composeTagged("!override", ports)
		if err != nil {
			return ComposePreviewOverride{}, err
		}
		entry["ports"] = portNode

		if service.ContainerName != "" {
			// A fixed name is production's container's name.
			if entry["container_name"], err = composeTagged("!reset", nil); err != nil {
				return ComposePreviewOverride{}, err
			}
		}
		if len(service.ExternalLinks) > 0 {
			if entry["external_links"], err = composeTagged("!reset", []string{}); err != nil {
				return ComposePreviewOverride{}, err
			}
		}
		if len(service.Secrets) > 0 {
			if entry["secrets"], err = composeTagged("!reset", []string{}); err != nil {
				return ComposePreviewOverride{}, err
			}
		}
		labels, _ := entry["labels"].(map[string]any)
		if labels == nil {
			labels = map[string]any{}
		}
		for key, value := range isolation.Labels {
			labels[key] = value
		}
		entry["labels"] = labels
	}

	var err error
	if result["networks"], err = composeTagged("!override", networks); err != nil {
		return ComposePreviewOverride{}, err
	}
	if result["volumes"], err = composeTagged("!override", volumes); err != nil {
		return ComposePreviewOverride{}, err
	}
	if len(config.Secrets) > 0 {
		if result["secrets"], err = composeTagged("!reset", map[string]any{}); err != nil {
			return ComposePreviewOverride{}, err
		}
	}
	out, err := yaml.Marshal(result)
	if err != nil {
		return ComposePreviewOverride{}, err
	}
	names = names[:0]
	for volume := range volumes {
		names = append(names, volume)
	}
	sort.Strings(names)
	sort.Strings(leftOut)
	return ComposePreviewOverride{Override: string(out), Volumes: names, LeftOut: leftOut}, nil
}
