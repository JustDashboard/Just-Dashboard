package auth

// Role governs what a principal may do. The dashboard authorises on the API
// surface, never on the UI alone: handlers are registered behind an explicit
// capability, so an unlisted route is unreachable rather than open.
type Role string

const (
	RoleAdmin    Role = "admin"
	RoleLimited  Role = "limited"
	RoleReadOnly Role = "readonly"
)

func (r Role) Valid() bool {
	switch r {
	case RoleAdmin, RoleLimited, RoleReadOnly:
		return true
	}
	return false
}

// Capability is a coarse permission attached to each route.
type Capability string

const (
	CapRead           Capability = "read"            // any authenticated principal
	CapServiceControl Capability = "service.control" // start/stop/restart containers, units, processes
	CapFileWrite      Capability = "file.write"      // mutate the filesystem
	CapTerminal       Capability = "terminal"        // PTY and container exec
	CapDestructive    Capability = "destructive"     // remove, prune, kill, restore
	CapSystemAdmin    Capability = "system.admin"    // linux users, firewall, dashboard accounts, tokens
	CapMetricsRead    Capability = "metrics.read"    // scrape the recorded database metrics; the only one a scoped API key holds
)

var rolePermissions = map[Role]map[Capability]bool{
	RoleAdmin: {
		CapRead: true, CapServiceControl: true, CapFileWrite: true,
		CapTerminal: true, CapDestructive: true, CapSystemAdmin: true, CapMetricsRead: true,
	},
	RoleLimited: {
		CapRead: true, CapServiceControl: true, CapFileWrite: true, CapMetricsRead: true,
	},
	RoleReadOnly: {
		CapRead: true, CapMetricsRead: true,
	},
}

func (r Role) Can(c Capability) bool {
	return rolePermissions[r][c]
}

// Capabilities lists what the role grants, for the frontend to hide controls
// it must not offer. The API enforces the same set independently.
func (r Role) Capabilities() []Capability {
	all := []Capability{CapRead, CapServiceControl, CapFileWrite, CapTerminal, CapDestructive, CapSystemAdmin, CapMetricsRead}
	out := make([]Capability, 0, len(all))
	for _, c := range all {
		if r.Can(c) {
			out = append(out, c)
		}
	}
	return out
}
