package dockerx

import (
	"strconv"
	"strings"
)

// Network owner kinds. Each names who created a network and, more to the
// point, who will act on it next: Compose recreates its own on the next `up`,
// a deployment reconciles its own on the next deploy, and nothing comes back
// for a network somebody made by hand.
const (
	NetworkOwnerSystem       = "system"
	NetworkOwnerDashboard    = "dashboard"
	NetworkOwnerDatabaseLink = "database-link"
	NetworkOwnerDeployment   = "deployment"
	NetworkOwnerCompose      = "compose"
	NetworkOwnerManual       = "manual"
)

// NetworkOwner is read from the labels the owner stamped at creation. It is
// evidence of who created the network, not of who uses it now: members are
// a separate reading.
type NetworkOwner struct {
	Kind string `json:"kind"`
	// Project is the Compose project, ComposeNetwork the key the network
	// has in that project's file.
	Project        string `json:"project,omitempty"`
	ComposeNetwork string `json:"composeNetwork,omitempty"`
	// EnvironmentID is the deployment environment a dashboard-managed
	// network belongs to. Deployment is its project and environment by name,
	// joined by the API, and empty once that environment is gone.
	EnvironmentID int64  `json:"environmentId,omitempty"`
	Deployment    string `json:"deployment,omitempty"`
}

// OwnerOfNetwork classifies a network by its labels. selfProject is the
// dashboard's own Compose project, empty where it could not be told; a
// network in that project carries the dashboard itself.
func OwnerOfNetwork(name string, labels map[string]string, selfProject string) NetworkOwner {
	if IsSystemNetwork(name) {
		return NetworkOwner{Kind: NetworkOwnerSystem}
	}
	project := labels["com.docker.compose.project"]
	owner := NetworkOwner{Kind: NetworkOwnerManual, Project: project, ComposeNetwork: labels["com.docker.compose.network"]}
	managed := labels["io.just-dashboard.managed"] == "true"
	environment, _ := strconv.ParseInt(labels["io.just-dashboard.environment-id"], 10, 64)
	switch {
	case selfProject != "" && project == selfProject:
		owner.Kind = NetworkOwnerDashboard
	case managed && labels["io.just-dashboard.database-network"] != "":
		owner.Kind, owner.EnvironmentID = NetworkOwnerDatabaseLink, environment
	case managed && environment > 0:
		owner.Kind, owner.EnvironmentID = NetworkOwnerDeployment, environment
	case project != "":
		owner.Kind = NetworkOwnerCompose
	}
	return owner
}

// ingressLabel is what the dashboard stamps on the public Caddy it
// provisions for deployments.
const ingressLabel = "com.just-dashboard.ingress"

// IsIngressContainer reports the shared public Caddy deployments are routed
// through: the one the dashboard provisioned, by its label or its name, or an
// adopted Caddy publishing 80 and 443 on every interface, which is the shape
// Proxy's discovery accepts. Its network memberships are how routes reach
// deployment containers, so detaching it cuts them.
func IsIngressContainer(c Container) bool {
	if c.Labels[ingressLabel] == "true" || c.Name == "just-dashboard-ingress" {
		return true
	}
	image := strings.ToLower(c.Image)
	if !strings.HasPrefix(image, "caddy") && !strings.Contains(image, "/caddy") {
		return false
	}
	public := func(port uint16) bool {
		for _, p := range c.Ports {
			if p.PublicPort == port && p.Type == "tcp" && (p.IP == "" || p.IP == "0.0.0.0" || p.IP == "::") {
				return true
			}
		}
		return false
	}
	return public(80) && public(443)
}
