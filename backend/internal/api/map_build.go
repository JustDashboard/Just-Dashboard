package api

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/gitx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/linuxusers"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate"
)

// The server map, built: every source's last answer turned into the boxes a
// map draws and the lines between them.
//
// Pure, so it is tested over plain values (map_build_test.go) rather than a
// Docker daemon, nginx and systemd. What it says of each thing is what that
// thing's own page reads, cut to one state and one fact; the state is what
// was measured — a container's state and health, a certificate's days, a
// backup's last run — and never a verdict the map reached by itself. A line
// joins two things only where the server knows they are joined: a label, a
// route the proxy holds, a binding, a mount. Nothing is guessed from a name.

type mapTone string

const (
	mapOK      mapTone = "ok"
	mapWarning mapTone = "warning"
	mapDanger  mapTone = "danger"
	mapIdle    mapTone = "idle"
	mapUnknown mapTone = "unknown"
)

type mapItem struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Product   string  `json:"product,omitempty"`
	ProjectID int64   `json:"projectId,omitempty"`
	Icon      string  `json:"icon,omitempty"`
	Tone      mapTone `json:"tone"`
	State     string  `json:"state"`
	Detail    string  `json:"detail,omitempty"`
	Href      string  `json:"href"`
	// Facts are what the inspector lists under the box: a few label and
	// value pairs read from the same source as its state.
	Facts []mapFact `json:"facts,omitempty"`
	// owner is the account an API key belongs to, for the per-account filter.
	owner int64
}

type mapLink struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Via is how the server knows: route, release, member, binding, mount,
	// network, certificate, backup, image, serves.
	Via string `json:"via"`
}

type mapFact struct {
	Label string  `json:"label"`
	Value string  `json:"value"`
	Tone  mapTone `json:"tone,omitempty"`
}

type mapKind struct {
	Key   string    `json:"key"`
	Total int       `json:"total"`
	Items []mapItem `json:"items"`
	// State is read, unread (its source failed, Reason says why), limited
	// (this role is shown part of it) or absent (the server does not run the
	// module, so there is no shelf).
	State  string    `json:"state"`
	Reason string    `json:"reason,omitempty"`
	Facts  []mapFact `json:"facts,omitempty"`
}

type mapHost struct {
	Hostname string `json:"hostname"`
}

type mapResponse struct {
	Host      mapHost   `json:"host"`
	Kinds     []mapKind `json:"kinds"`
	Links     []mapLink `json:"links"`
	CheckedAt time.Time `json:"checkedAt"`
}

// mapItemCap is the most things one shelf is sent. A shelf draws its first
// row or two and "+N more"; the rest of a thousand-unit systemd list is its
// own page's to show.
const mapItemCap = 300

// mapTerminal is a shell workspace as the map draws it: its windows folded
// into one, as the terminal page's rail does.
type mapTerminal struct {
	ID      string
	Title   string
	User    string
	Windows int
	Working bool
	Busy    bool
	Process string
}

type mapComputer struct {
	ID            string
	Name          string
	OS            string
	Host          string
	Protocol      string
	LastSessionAt time.Time
}

type mapBoard struct {
	ID        int64
	Name      string
	UpdatedAt time.Time
}

// mapAlerts is every alert rule the server holds, with how many subjects of
// each are firing now.
type mapAlerts struct {
	Host        []hostAlertRule
	HostFiring  map[int64]int
	Proxy       []proxyAlertRule
	ProxyFiring map[int64]int
}

type mapStreams struct {
	Streams []proxysvc.StreamEntry
	Paused  []proxysvc.StreamEntry
}

// mapInputs is every source's last answer, as readMapSource left it.
type mapInputs struct {
	now        time.Time
	hostname   string
	projects   mapRead[[]deploy.DeploymentSummary]
	containers mapRead[[]dockerx.Container]
	images     mapRead[[]dockerx.Image]
	volumes    mapRead[[]dockerx.Volume]
	networks   mapRead[[]dockerx.Network]
	databases  mapRead[[]fleetEntry]
	bindings   mapRead[[]topoBinding]
	vhosts     mapRead[[]proxysvc.VHost]
	certs      mapRead[[]proxysvc.Certificate]
	streams    mapRead[mapStreams]
	backups    mapRead[[]*backups.Job]
	timers     mapRead[[]procs.Timer]
	cron       mapRead[[]procs.Crontab]
	alerts     mapRead[mapAlerts]
	channels   mapRead[[]deploy.NotificationChannel]
	terminals  mapRead[[]mapTerminal]
	// terminalHome is where a new shell starts, for the shelf's New shell.
	terminalHome string
	boards       mapRead[[]mapBoard]
	repos        mapRead[[]gitx.Repo]
	computers    mapRead[[]mapComputer]
	accounts     mapRead[[]*auth.User]
	keys         mapRead[[]auth.APIToken]
	sysUsers     mapRead[[]linuxusers.User]
	units        mapRead[*procs.Inventory]
	pm2          mapRead[[]procs.PM2Process]
	firewall     mapRead[*netsec.FirewallStatus]
	vpn          mapRead[netx.VPNSummary]
	dashboard    mapRead[selfupdate.Report]

	// absent names the kinds whose module this server does not run.
	absent map[string]bool
}

// mapBuilder collects the kinds and the lines as the sources are read.
type mapBuilder struct {
	now   time.Time
	kinds []mapKind
	links []mapLink
	seen  map[string]bool
}

func (b *mapBuilder) link(from, to, via string) {
	if from == "" || to == "" || from == to {
		return
	}
	key := from + "\x00" + to + "\x00" + via
	if b.seen[key] {
		return
	}
	b.seen[key] = true
	b.links = append(b.links, mapLink{From: from, To: to, Via: via})
}

// kindFrom turns one source's answer into a shelf. A source that has never
// answered is a shelf that says so; one whose last read failed keeps what it
// last showed and says when that was, never an empty shelf in its place.
func kindFrom[T any](b *mapBuilder, key string, read mapRead[T], absent bool, items func(T) []mapItem) {
	kind := mapKind{Key: key, State: "read", Items: []mapItem{}}
	switch {
	case absent:
		kind.State = "absent"
	case !read.Have:
		kind.State, kind.Reason = "unread", mapReason(read.Err)
	default:
		kind.Items = items(read.Value)
		if read.Err != nil {
			kind.State, kind.Reason = "unread", mapReason(read.Err)
			if !read.At.IsZero() {
				kind.Reason = fmt.Sprintf("Last read %s ago: %s", mapAgo(b.now.Sub(read.At)), kind.Reason)
			}
		}
	}
	kind.Total = len(kind.Items)
	b.kinds = append(b.kinds, kind)
}

func mapReason(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, errMapReading) {
		return "Still being read"
	}
	msg := err.Error()
	if len(msg) > 160 {
		msg = msg[:157] + "…"
	}
	return msg
}

func buildServerMap(in mapInputs) mapResponse {
	b := &mapBuilder{now: in.now, seen: map[string]bool{}}
	absent := func(key string) bool { return in.absent[key] }

	// Deployed.
	envProject := map[int64]int64{}
	kindFrom(b, "projects", in.projects, false, func(list []deploy.DeploymentSummary) []mapItem {
		out := make([]mapItem, 0, len(list))
		for _, d := range list {
			envProject[d.EnvironmentID] = d.ID
			out = append(out, mapProject(d))
		}
		return out
	})
	containerByName := map[string]string{}
	publishedBy := map[int]string{}
	kindFrom(b, "containers", in.containers, absent("docker"), func(list []dockerx.Container) []mapItem {
		out := make([]mapItem, 0, len(list))
		for _, c := range list {
			id := "container:" + c.ID
			containerByName[c.Name] = id
			for _, p := range c.Ports {
				if p.PublicPort > 0 {
					publishedBy[int(p.PublicPort)] = id
				}
			}
			out = append(out, mapContainer(c))
			if env, err := strconv.ParseInt(c.Labels["io.just-dashboard.environment-id"], 10, 64); err == nil {
				if project, ok := envProject[env]; ok {
					b.link("project:"+strconv.FormatInt(project, 10), id, "release")
				}
			}
			if c.ComposeStack != "" {
				b.link("stack:"+c.ComposeStack, id, "member")
			}
			for _, m := range c.Mounts {
				if m.Type == "volume" && m.Name != "" {
					b.link(id, "volume:"+m.Name, "mount")
				}
			}
			for _, n := range c.Networks {
				if !systemNetwork(n) {
					b.link(id, "network:"+n, "network")
				}
			}
			if c.ImageID != "" {
				b.link(id, "image:"+c.ImageID, "image")
			}
		}
		return out
	})
	kindFrom(b, "stacks", in.containers, absent("docker"), mapStacks)
	kindFrom(b, "volumes", in.volumes, absent("docker"), func(list []dockerx.Volume) []mapItem {
		used := map[string]bool{}
		for _, l := range b.links {
			if l.Via == "mount" {
				used[l.To] = true
			}
		}
		out := make([]mapItem, 0, len(list))
		for _, v := range list {
			out = append(out, mapVolume(v, used["volume:"+v.Name]))
		}
		return out
	})
	networkIDs := map[string]string{}
	kindFrom(b, "networks", in.networks, absent("docker"), func(list []dockerx.Network) []mapItem {
		out := make([]mapItem, 0, len(list))
		for _, n := range list {
			if n.Name == "host" || n.Name == "none" {
				continue
			}
			networkIDs[n.Name] = "network:" + n.ID
			out = append(out, mapNetwork(n))
		}
		return out
	})
	// A container names its networks by name; the network's box is its id.
	for i, l := range b.links {
		if l.Via == "network" {
			if id, ok := networkIDs[strings.TrimPrefix(l.To, "network:")]; ok {
				b.links[i].To = id
			}
		}
	}
	kindFrom(b, "images", in.images, absent("docker"), func(list []dockerx.Image) []mapItem {
		out := make([]mapItem, 0, len(list))
		for _, img := range list {
			if img.Dangling {
				continue
			}
			out = append(out, mapImage(img))
		}
		return out
	})
	kindFrom(b, "databases", in.databases, false, func(list []fleetEntry) []mapItem {
		out := make([]mapItem, 0, len(list))
		for _, e := range list {
			id := "db:" + strconv.FormatInt(e.ID, 10)
			out = append(out, mapDatabase(e))
			if c, ok := containerByName[e.Container]; ok && e.Container != "" {
				b.link(c, id, "serves")
			}
		}
		return out
	})
	if in.bindings.Have {
		for _, bind := range in.bindings.Value {
			b.link("project:"+strconv.FormatInt(bind.projectID, 10),
				"db:"+strconv.FormatInt(bind.connID, 10), "binding")
		}
	}

	// Web.
	certFor := map[string][]string{}
	if in.certs.Have {
		for _, c := range in.certs.Value {
			for _, site := range c.UsedBy {
				certFor[site] = append(certFor[site], "cert:"+c.Path)
			}
		}
	}
	kindFrom(b, "domains", in.vhosts, absent("proxy"), func(list []proxysvc.VHost) []mapItem {
		seen := map[string]int{}
		out := []mapItem{}
		for _, v := range list {
			for _, name := range v.ServerNames {
				if !servedName(name) {
					continue
				}
				id := "domain:" + strings.ToLower(name)
				item := mapDomain(v, name)
				if at, ok := seen[id]; ok {
					// Two sites name it — the HTTP redirect and the TLS server,
					// usually — and the one that serves it over TLS is the one
					// to draw.
					if v.TLS && v.Enabled {
						out[at] = item
					}
				} else {
					seen[id] = len(out)
					out = append(out, item)
				}
				if v.Owner != nil && v.Owner.ProjectID > 0 && !v.Owner.Archived {
					b.link(id, "project:"+strconv.FormatInt(v.Owner.ProjectID, 10), "route")
				}
				for _, cert := range certFor[v.Name] {
					b.link(id, cert, "certificate")
				}
				for _, up := range v.Upstreams {
					if port, ok := loopbackPort(up); ok {
						if c, ok := publishedBy[port]; ok {
							b.link(id, c, "route")
						}
					}
				}
			}
		}
		return out
	})
	kindFrom(b, "certificates", in.certs, absent("proxy"), func(list []proxysvc.Certificate) []mapItem {
		out := make([]mapItem, 0, len(list))
		for _, c := range list {
			out = append(out, mapCertificate(c))
		}
		return out
	})
	kindFrom(b, "streams", in.streams, absent("proxy"), func(s mapStreams) []mapItem {
		out := make([]mapItem, 0, len(s.Streams)+len(s.Paused))
		for _, st := range s.Streams {
			out = append(out, mapStream(st))
		}
		for _, st := range s.Paused {
			st.Paused = true
			out = append(out, mapStream(st))
		}
		return out
	})

	// Host.
	kindFrom(b, "services", in.units, absent("systemd"), func(inv *procs.Inventory) []mapItem {
		out := []mapItem{}
		if inv == nil {
			return out
		}
		for _, u := range inv.Units {
			if !strings.HasSuffix(u.Name, ".service") || u.LoadState == "not-found" {
				continue
			}
			out = append(out, mapUnit(u))
		}
		return out
	})
	kindFrom(b, "pm2", in.pm2, absent("pm2"), func(list []procs.PM2Process) []mapItem {
		out := make([]mapItem, 0, len(list))
		for _, p := range list {
			out = append(out, mapPM2(p))
		}
		return out
	})
	kindFrom(b, "dashboard", in.dashboard, false, func(r selfupdate.Report) []mapItem {
		return []mapItem{mapDashboard(r)}
	})

	// Workspace.
	kindFrom(b, "terminals", in.terminals, absent("terminal"), func(list []mapTerminal) []mapItem {
		out := make([]mapItem, 0, len(list))
		for _, t := range list {
			out = append(out, mapTerminalItem(t))
		}
		return out
	})
	kindFrom(b, "drawings", in.boards, false, func(list []mapBoard) []mapItem {
		out := make([]mapItem, 0, len(list))
		for _, d := range list {
			out = append(out, mapItem{
				ID: "drawing:" + strconv.FormatInt(d.ID, 10), Name: d.Name, Product: "excalidraw",
				Tone: mapIdle, State: "edited " + mapAgo(b.now.Sub(d.UpdatedAt)) + " ago",
				Href:  "/excalidraw/" + strconv.FormatInt(d.ID, 10),
				Facts: mapFacts("Edited", d.UpdatedAt.Format("2 Jan 2006, 15:04")),
			})
		}
		return out
	})
	kindFrom(b, "repositories", in.repos, absent("git"), func(list []gitx.Repo) []mapItem {
		out := make([]mapItem, 0, len(list))
		for _, r := range list {
			out = append(out, mapRepo(r))
		}
		return out
	})
	kindFrom(b, "computers", in.computers, false, func(list []mapComputer) []mapItem {
		out := make([]mapItem, 0, len(list))
		for _, c := range list {
			state := "never opened"
			if !c.LastSessionAt.IsZero() {
				state = "opened " + mapAgo(b.now.Sub(c.LastSessionAt)) + " ago"
			}
			out = append(out, mapItem{
				ID: "computer:" + c.ID, Name: c.Name, Product: strings.ToLower(c.OS),
				Tone: mapIdle, State: state, Detail: c.Host, Href: "/remote-desktop/" + url.PathEscape(c.ID),
				Facts: mapFacts("Address", c.Host, "System", c.OS, "Connects with", strings.ToUpper(c.Protocol)),
			})
		}
		return out
	})

	// Jobs.
	kindFrom(b, "backups", in.backups, false, func(list []*backups.Job) []mapItem {
		out := make([]mapItem, 0, len(list))
		for _, j := range list {
			id := "backup:" + strconv.FormatInt(j.ID, 10)
			out = append(out, mapBackup(j, b.now))
			for _, conn := range j.DatabaseDumps {
				b.link("db:"+strconv.FormatInt(conn, 10), id, "backup")
			}
			for _, name := range j.PauseContainers {
				if c, ok := containerByName[name]; ok {
					b.link(c, id, "backup")
				}
			}
		}
		return out
	})
	// Two sources, one shelf: the systemd timers and the system's cron files.
	// It is read once either has answered; the one still missing, or failing,
	// is what the shelf says it could not read.
	scheduled := mapRead[[]mapItem]{Have: in.timers.Have || in.cron.Have}
	for _, at := range []time.Time{in.timers.At, in.cron.At} {
		if at.After(scheduled.At) {
			scheduled.At = at
		}
	}
	// When it was last read is the failing half's: "last read" over a half
	// that never answered would date the half that did.
	switch {
	case in.timers.Err != nil && !absent("systemd"):
		scheduled.Err, scheduled.At = in.timers.Err, in.timers.At
	case in.cron.Err != nil:
		scheduled.Err, scheduled.At = in.cron.Err, in.cron.At
	}
	if in.timers.Have {
		for _, t := range in.timers.Value {
			scheduled.Value = append(scheduled.Value, mapTimer(t, b.now))
		}
	}
	if in.cron.Have {
		for _, tab := range in.cron.Value {
			for _, job := range tab.Jobs {
				scheduled.Value = append(scheduled.Value, mapCron(tab, job))
			}
		}
	}
	kindFrom(b, "scheduled", scheduled, absent("systemd") && !in.cron.Have, func(items []mapItem) []mapItem {
		return items
	})
	kindFrom(b, "alerts", in.alerts, false, mapAlertItems)
	kindFrom(b, "channels", in.channels, false, func(list []deploy.NotificationChannel) []mapItem {
		out := make([]mapItem, 0, len(list))
		for _, c := range list {
			out = append(out, mapChannel(c))
		}
		return out
	})

	// People.
	kindFrom(b, "accounts", in.accounts, false, func(list []*auth.User) []mapItem {
		out := make([]mapItem, 0, len(list))
		for _, u := range list {
			out = append(out, mapAccount(u))
		}
		return out
	})
	kindFrom(b, "keys", in.keys, false, func(list []auth.APIToken) []mapItem {
		out := []mapItem{}
		for _, k := range list {
			if k.Revoked {
				continue
			}
			out = append(out, mapKey(k, b.now))
		}
		return out
	})
	kindFrom(b, "systemUsers", in.sysUsers, false, func(list []linuxusers.User) []mapItem {
		out := []mapItem{}
		for _, u := range list {
			if u.System {
				continue
			}
			out = append(out, mapSystemUser(u))
		}
		return out
	})

	// Protection.
	kindFrom(b, "security", in.firewall, absent("firewall"), func(fw *netsec.FirewallStatus) []mapItem {
		if fw == nil || !fw.Available {
			return []mapItem{}
		}
		return []mapItem{mapFirewall(fw)}
	})
	kindFrom(b, "tunnels", in.vpn, absent("vpn"), mapTunnels)

	for i := range b.kinds {
		b.kinds[i].Items, b.kinds[i].Total = capItems(b.kinds[i].Key, b.kinds[i].Items)
		if b.kinds[i].Key == "terminals" && in.terminalHome != "" {
			b.kinds[i].Facts = []mapFact{{Label: "home", Value: in.terminalHome}}
		}
	}
	return mapResponse{
		Host:      mapHost{Hostname: in.hostname},
		Kinds:     b.kinds,
		Links:     linksBetween(b.links, b.kinds),
		CheckedAt: in.now.UTC(),
	}
}

// capItems puts the things worth seeing first — what is down, then what
// needs a look — and keeps the shelf's own order otherwise, which for
// drawings and backups is the order their pages list them in.
func capItems(key string, items []mapItem) ([]mapItem, int) {
	total := len(items)
	rank := func(t mapTone) int {
		switch t {
		case mapDanger:
			return 0
		case mapWarning:
			return 1
		}
		return 2
	}
	keepOrder := key == "drawings" || key == "computers" || key == "scheduled"
	sort.SliceStable(items, func(i, j int) bool {
		if ri, rj := rank(items[i].Tone), rank(items[j].Tone); ri != rj {
			return ri < rj
		}
		if keepOrder {
			return false
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	if len(items) > mapItemCap {
		items = items[:mapItemCap]
	}
	return items, total
}

// linksBetween keeps the lines whose two ends are both on the map.
func linksBetween(links []mapLink, kinds []mapKind) []mapLink {
	on := map[string]bool{}
	for _, k := range kinds {
		for _, item := range k.Items {
			on[item.ID] = true
		}
	}
	out := []mapLink{}
	for _, l := range links {
		if on[l.From] && on[l.To] {
			out = append(out, l)
		}
	}
	return out
}

func systemNetwork(name string) bool {
	return name == "bridge" || name == "host" || name == "none"
}

// servedName is a name a visitor could type: not nginx's catch-all `_`, not a
// bare localhost.
func servedName(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && name != "_" && name != "localhost" && !strings.HasPrefix(name, "~")
}

// loopbackPort is the port of an upstream on this machine's loopback — the
// one place a proxied address is known to be whatever publishes that port.
func loopbackPort(upstream string) (int, bool) {
	addr := upstream
	if i := strings.Index(addr, "://"); i >= 0 {
		addr = addr[i+3:]
	}
	if i := strings.IndexAny(addr, "/?"); i >= 0 {
		addr = addr[:i]
	}
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, false
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 {
		return 0, false
	}
	switch host {
	case "127.0.0.1", "localhost", "::1", "0.0.0.0":
		return port, true
	}
	return 0, false
}

func mapProject(d deploy.DeploymentSummary) mapItem {
	id := strconv.FormatInt(d.ID, 10)
	item := mapItem{
		ID: "project:" + id, Name: d.Name, ProjectID: d.ID, Href: "/deploy/" + id,
		Product: d.Framework,
	}
	if item.Product == "" && len(d.Images) > 0 {
		item.Product = imageProduct(d.Images[0])
	}
	if host := endpointHost(d.Endpoint); host != "" {
		item.Detail = host
		if d.LiveReleaseID > 0 {
			item.Icon = "/api/v1/deploy/" + id + "/favicon"
		}
	} else if d.HostPort > 0 {
		item.Detail = "port " + strconv.Itoa(d.HostPort)
	}
	failed := func(run *deploy.EngineRun) bool {
		return run != nil && (run.State == deploy.RunFailed || run.State == deploy.RunFailedActivation ||
			run.State == deploy.RunRolledBack)
	}
	// The deployments page's own words for a project's state
	// (deploy/vocabulary.tsx projectState), in the same order.
	switch {
	case d.ActiveRun != nil:
		item.Tone, item.State = mapWarning, "deploying"
	case d.LiveReleaseID == 0 && failed(d.LastRun):
		item.Tone, item.State = mapDanger, "failed"
	case d.LiveReleaseID == 0:
		item.Tone, item.State = mapIdle, "not deployed"
	case d.Stopped:
		item.Tone, item.State = mapIdle, "stopped"
	case failed(d.LastRun) && d.LastRun.ReleaseID != d.LiveReleaseID:
		item.Tone, item.State = mapDanger, "failed"
	case d.Health == "unhealthy" || d.Health == string(deploy.HealthFailed):
		item.Tone, item.State = mapDanger, "unhealthy"
	default:
		item.Tone, item.State = mapOK, "ready"
	}
	run := ""
	if d.LastRun != nil {
		run = strings.ReplaceAll(string(d.LastRun.State), "_", " ")
	}
	port := ""
	if d.HostPort > 0 {
		port = strconv.Itoa(d.HostPort)
	}
	services := ""
	if d.ServiceCount > 0 {
		services = strconv.Itoa(d.ServiceCount)
	}
	item.Facts = mapFacts("Address", endpointHost(d.Endpoint), "Environment", d.EnvironmentName,
		"Framework", d.Framework, "Port", port, "Services", services, "Last run", run)
	return item
}

func endpointHost(endpoint string) string {
	if endpoint == "" {
		return ""
	}
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		return u.Host
	}
	return strings.TrimSuffix(endpoint, "/")
}

func mapContainer(c dockerx.Container) mapItem {
	item := mapItem{
		ID: "container:" + c.ID, Name: c.Name, Product: imageProduct(c.Image),
		Detail: c.Image, Href: "/docker/containers/" + url.PathEscape(c.ID),
	}
	switch c.State {
	case "running":
		switch c.Health {
		case "unhealthy":
			item.Tone, item.State = mapDanger, "unhealthy"
		case "starting":
			item.Tone, item.State = mapWarning, "starting"
		case "healthy":
			item.Tone, item.State = mapOK, "healthy"
		default:
			item.Tone, item.State = mapOK, "running"
		}
	case "restarting":
		item.Tone, item.State = mapWarning, "restarting"
	case "paused":
		item.Tone, item.State = mapWarning, "paused"
	case "dead":
		item.Tone, item.State = mapDanger, "dead"
	case "created":
		item.Tone, item.State = mapIdle, "created"
	case "exited":
		code, known := exitCode(c)
		item.Tone, item.State = mapIdle, "exited"
		if known {
			item.State = fmt.Sprintf("exited (%d)", code)
			// What a stop leaves behind — 0, or the signal a stop sends — is a
			// container somebody stopped; anything else is how it ended on
			// its own (host alerts judge the same codes, judgeExit).
			if code != 0 && code != 130 && code != 137 && code != 143 {
				item.Tone = mapDanger
			}
		}
	default:
		item.Tone, item.State = mapUnknown, c.State
	}
	ports := []string{}
	for _, p := range c.Ports {
		if p.PublicPort > 0 {
			ports = append(ports, fmt.Sprintf("%d→%d", p.PublicPort, p.PrivatePort))
		}
	}
	up, restarts := "", ""
	if c.State == "running" && c.UptimeSecond > 0 {
		up = mapAgo(time.Duration(c.UptimeSecond) * time.Second)
	}
	if c.Restarts > 0 {
		restarts = strconv.Itoa(c.Restarts)
	}
	item.Facts = mapFacts("Image", c.Image, "Stack", c.ComposeStack, "Published", strings.Join(ports, ", "),
		"Up for", up, "Restarts", restarts)
	return item
}

// exitCode is the code an exited container left, from the inspect where the
// listing carries it, else from Docker's own "Exited (137) 2 hours ago".
func exitCode(c dockerx.Container) (int, bool) {
	if c.Exited != nil {
		return *c.Exited, true
	}
	rest, ok := strings.CutPrefix(c.Status, "Exited (")
	if !ok {
		return 0, false
	}
	end := strings.IndexByte(rest, ')')
	if end < 0 {
		return 0, false
	}
	code, err := strconv.Atoi(rest[:end])
	return code, err == nil
}

// mapStacks are the Compose projects the containers' labels name, each with
// how many of its containers run.
func mapStacks(list []dockerx.Container) []mapItem {
	type stack struct {
		running, total int
		product        string
	}
	stacks := map[string]*stack{}
	names := []string{}
	for _, c := range list {
		if c.ComposeStack == "" {
			continue
		}
		s := stacks[c.ComposeStack]
		if s == nil {
			s = &stack{product: imageProduct(c.Image)}
			stacks[c.ComposeStack] = s
			names = append(names, c.ComposeStack)
		}
		s.total++
		if c.State == "running" {
			s.running++
		}
	}
	out := make([]mapItem, 0, len(names))
	for _, name := range names {
		s := stacks[name]
		item := mapItem{
			ID: "stack:" + name, Name: name, Product: s.product,
			Href:  "/docker/stacks/" + url.PathEscape(name),
			State: fmt.Sprintf("%d of %d running", s.running, s.total),
			Facts: mapFacts("Containers", strconv.Itoa(s.total), "Running", strconv.Itoa(s.running)),
		}
		switch {
		case s.running == s.total:
			item.Tone = mapOK
		case s.running == 0:
			item.Tone, item.State = mapIdle, "stopped"
		default:
			item.Tone = mapWarning
		}
		out = append(out, item)
	}
	return out
}

func mapVolume(v dockerx.Volume, mounted bool) mapItem {
	item := mapItem{
		ID: "volume:" + v.Name, Name: v.Name, Href: "/docker/volumes?volume=" + url.QueryEscape(v.Name),
		Tone: mapIdle, State: "unused",
	}
	if mounted || v.InUse || v.RefCount > 0 {
		item.Tone, item.State = mapOK, "in use"
	}
	if v.Size > 0 {
		item.Detail = alertBytes(v.Size)
	}
	size := ""
	if v.Size > 0 {
		size = alertBytes(v.Size)
	}
	item.Facts = mapFacts("Size", size, "Driver", v.Driver, "Mount point", v.Mountpoint)
	return item
}

func mapNetwork(n dockerx.Network) mapItem {
	item := mapItem{
		ID: "network:" + n.ID, Name: n.Name, Product: "docker",
		Href: "/docker/networks?network=" + url.QueryEscape(n.ID),
	}
	if len(n.Subnets) > 0 {
		item.Detail = n.Subnets[0]
	}
	if n.Containers > 0 {
		item.Tone, item.State = mapOK, mapPlural(n.Containers, "container", "containers")
	} else {
		item.Tone, item.State = mapIdle, "unused"
	}
	item.Facts = mapFacts("Driver", n.Driver, "Subnet", strings.Join(n.Subnets, ", "), "Gateway", n.Gateway,
		"Containers", strconv.Itoa(n.Containers))
	return item
}

func mapImage(img dockerx.Image) mapItem {
	name := shortID(img.ID)
	if len(img.RepoTags) > 0 && img.RepoTags[0] != "<none>:<none>" {
		name = img.RepoTags[0]
	}
	item := mapItem{
		ID: "image:" + img.ID, Name: name, Product: imageProduct(name),
		Href: "/docker/images?image=" + url.QueryEscape(img.ID), Detail: alertBytes(img.Size),
		Tone: mapIdle, State: "unused",
	}
	if img.Containers > 0 {
		item.Tone, item.State = mapOK, "in use"
	}
	created := ""
	if !img.Created.IsZero() {
		created = img.Created.Format("2 Jan 2006")
	}
	item.Facts = mapFacts("Size", alertBytes(img.Size), "Containers", strconv.FormatInt(img.Containers, 10),
		"Created", created)
	return item
}

func mapDatabase(e fleetEntry) mapItem {
	id := strconv.FormatInt(e.ID, 10)
	item := mapItem{
		ID: "db:" + id, Name: e.Name, Product: e.Flavor, Href: "/databases/" + id,
		Detail: e.Database,
	}
	if item.Product == "" {
		item.Product = string(e.Driver)
	}
	if e.Version != "" {
		item.Detail = strings.TrimSpace(item.Product + " " + e.VersionNumber)
	}
	switch e.State {
	case dbStateRunning:
		item.Tone, item.State = mapOK, "answers"
	case dbStateStopped:
		item.Tone, item.State = mapIdle, "stopped"
	case dbStatePaused:
		item.Tone, item.State = mapWarning, "paused"
	case dbStateUnreachable:
		item.Tone, item.State = mapDanger, "not answering"
	case dbStateBroken:
		item.Tone, item.State = mapDanger, "broken"
	default:
		item.Tone, item.State = mapUnknown, e.State
	}
	where := e.Container
	if where == "" {
		where = e.Unit
	}
	if where == "" && e.Host != "" {
		where = strings.TrimSuffix(e.Host+":"+e.Port, ":")
	}
	latency := ""
	if e.OK && e.LatencyMs > 0 {
		latency = strconv.FormatInt(e.LatencyMs, 10) + " ms"
	}
	item.Facts = mapFacts("Engine", strings.TrimSpace(item.Product+" "+e.VersionNumber), "Database", e.Database,
		"Runs in", where, "Environment", e.Environment, "Answered in", latency)
	return item
}

func mapDomain(v proxysvc.VHost, name string) mapItem {
	item := mapItem{
		ID: "domain:" + strings.ToLower(name), Name: name,
		Href: "/proxy/sites/" + url.PathEscape(v.Name),
	}
	switch {
	case v.Broken != "":
		item.Tone, item.State = mapDanger, "broken"
	case !v.Enabled:
		item.Tone, item.State = mapIdle, "disabled"
	case v.Maintenance:
		item.Tone, item.State = mapWarning, "maintenance"
	case v.TLS:
		item.Tone, item.State = mapOK, "https"
	default:
		item.Tone, item.State = mapOK, "http"
	}
	switch {
	case v.Owner != nil && v.Owner.Project != "":
		item.Detail = v.Owner.Project
		item.ProjectID = v.Owner.ProjectID
	case len(v.Upstreams) > 0:
		item.Detail = v.Upstreams[0]
	case len(v.Roots) > 0:
		item.Detail = v.Roots[0]
	}
	serves := ""
	switch {
	case v.Owner != nil && v.Owner.Project != "":
		serves = v.Owner.Project
	case len(v.Upstreams) > 0:
		serves = strings.Join(v.Upstreams, ", ")
	case len(v.Roots) > 0:
		serves = v.Roots[0]
	}
	tls := "no"
	if v.TLS {
		tls = "yes"
	}
	item.Facts = mapFacts("Site", v.Name, "Serves", serves, "Proxy", string(v.Kind), "HTTPS", tls)
	return item
}

func mapCertificate(c proxysvc.Certificate) mapItem {
	name := c.Name
	if name == "" && len(c.Domains) > 0 {
		name = c.Domains[0]
	}
	item := mapItem{
		ID: "cert:" + c.Path, Name: name, Detail: c.Issuer,
		Href: "/proxy/certificates?cert=" + url.QueryEscape(c.Path),
	}
	if strings.Contains(strings.ToLower(c.Issuer), "let's encrypt") || strings.Contains(c.Issuer, "R1") {
		item.Product = "lets-encrypt"
	}
	switch {
	case c.Error != "":
		item.Tone, item.State = mapDanger, "unreadable"
	case c.Expired:
		item.Tone, item.State = mapDanger, "expired"
	case c.Expiring:
		item.Tone, item.State = mapWarning, mapPlural(c.DaysLeft, "day left", "days left")
	default:
		item.Tone, item.State = mapOK, mapPlural(c.DaysLeft, "day left", "days left")
	}
	covers := strings.Join(c.Domains, ", ")
	if len(c.Domains) > 3 {
		covers = strings.Join(c.Domains[:3], ", ") + fmt.Sprintf(" and %d more", len(c.Domains)-3)
	}
	expires := ""
	if !c.NotAfter.IsZero() {
		expires = c.NotAfter.Format("2 Jan 2006")
	}
	item.Facts = mapFacts("Covers", covers, "Issuer", c.Issuer, "Expires", expires)
	return item
}

func mapStream(s proxysvc.StreamEntry) mapItem {
	item := mapItem{
		ID: "stream:" + s.Name, Name: s.Name, Product: "nginx",
		Href:   "/proxy/streams?stream=" + url.QueryEscape(s.Name),
		Detail: fmt.Sprintf(":%d → %s", s.Listen, s.Upstream),
	}
	switch {
	case s.Paused:
		item.Tone, item.State = mapIdle, "paused"
	case s.Error != "" || s.BindError != "":
		item.Tone, item.State = mapDanger, "not listening"
	case s.Open:
		item.Tone, item.State = mapOK, "listening"
	default:
		item.Tone, item.State = mapIdle, strings.ReplaceAll(s.State, "_", " ")
	}
	item.Facts = mapFacts("Listens on", ":"+strconv.Itoa(s.Listen), "Forwards to", s.Upstream,
		"Protocol", strings.ToUpper(s.Protocol))
	return item
}

func mapUnit(u procs.Unit) mapItem {
	name := strings.TrimSuffix(u.Name, ".service")
	item := mapItem{
		ID: "unit:" + u.Name, Name: name, Product: name, Detail: u.Description,
		Href: "/processes/services?unit=" + url.QueryEscape(u.Name),
	}
	switch u.ActiveState {
	case "failed":
		item.Tone, item.State = mapDanger, "failed"
	case "active":
		item.Tone, item.State = mapOK, u.SubState
	case "activating", "deactivating", "reloading":
		item.Tone, item.State = mapWarning, u.ActiveState
	default:
		item.Tone, item.State = mapIdle, u.ActiveState
	}
	if item.State == "" {
		item.State = u.ActiveState
	}
	enabled, restarts := "no", ""
	if u.Enabled {
		enabled = "yes"
	}
	if u.Restarts > 0 {
		restarts = strconv.Itoa(u.Restarts)
	}
	item.Facts = mapFacts("Description", u.Description, "State", strings.TrimSpace(u.ActiveState+" "+u.SubState),
		"Starts at boot", enabled, "Restarts", restarts)
	return item
}

func mapPM2(p procs.PM2Process) mapItem {
	item := mapItem{
		ID: fmt.Sprintf("pm2:%s:%d", p.DaemonID, p.ID), Name: p.Name, Product: "pm2",
		Href: "/processes/pm2?app=" + url.QueryEscape(fmt.Sprintf("%s:%d", p.DaemonID, p.ID)),
	}
	switch p.Status {
	case "online":
		item.Tone, item.State = mapOK, "online"
	case "errored":
		item.Tone, item.State = mapDanger, "errored"
	case "launching", "stopping":
		item.Tone, item.State = mapWarning, p.Status
	default:
		item.Tone, item.State = mapIdle, p.Status
	}
	if p.Restarts > 0 {
		item.Detail = mapPlural(p.Restarts, "restart", "restarts")
	}
	memory, restarts := "", ""
	if p.Memory > 0 {
		memory = alertBytes(p.Memory)
	}
	if p.Restarts > 0 {
		restarts = strconv.Itoa(p.Restarts)
	}
	item.Facts = mapFacts("Status", p.Status, "Memory", memory, "Restarts", restarts)
	return item
}

func mapDashboard(r selfupdate.Report) mapItem {
	item := mapItem{
		ID: "dashboard:self", Name: "Just Dashboard", Product: "just-dashboard",
		Href: "/dashboard", Detail: r.Version,
	}
	switch {
	case r.Run != nil && r.Run.Status == selfupdate.StatusRunning:
		item.Tone, item.State = mapWarning, "updating"
	case r.Available && r.Latest != "":
		item.Tone, item.State = mapWarning, r.Latest+" available"
	default:
		item.Tone, item.State = mapOK, "up to date"
	}
	item.Facts = mapFacts("Version", r.Version, "Newest", r.Latest)
	return item
}

func mapTerminalItem(t mapTerminal) mapItem {
	item := mapItem{
		ID: "terminal:" + t.ID, Name: t.Title, Product: "terminal",
		Href: "/terminal?session=" + url.QueryEscape(t.ID), Detail: t.User,
	}
	if item.Name == "" {
		item.Name = "Shell"
	}
	switch {
	case t.Working:
		item.Tone, item.State = mapOK, "working"
	case t.Busy && t.Process != "":
		item.Tone, item.State = mapOK, t.Process
	default:
		item.Tone, item.State = mapIdle, "idle"
	}
	if t.Windows > 1 {
		item.Detail = strings.TrimSpace(item.Detail + " · " + mapPlural(t.Windows, "window", "windows"))
	}
	item.Facts = mapFacts("Signed in as", t.User, "Windows", strconv.Itoa(t.Windows), "Running", t.Process)
	return item
}

func mapRepo(r gitx.Repo) mapItem {
	item := mapItem{
		ID: "repo:" + r.Path, Name: r.Name, Product: "git", Detail: r.Branch,
		Href: "/git?repo=" + url.QueryEscape(r.Path),
	}
	switch {
	case r.Conflicts > 0:
		item.Tone, item.State = mapDanger, mapPlural(r.Conflicts, "conflict", "conflicts")
	case r.Dirty:
		item.Tone, item.State = mapWarning, mapPlural(r.Changes, "change", "changes")
	case r.Behind > 0:
		item.Tone, item.State = mapWarning, fmt.Sprintf("%d behind", r.Behind)
	case r.Ahead > 0:
		item.Tone, item.State = mapOK, fmt.Sprintf("%d ahead", r.Ahead)
	default:
		item.Tone, item.State = mapOK, "clean"
	}
	sync := ""
	if r.Ahead > 0 || r.Behind > 0 {
		sync = fmt.Sprintf("%d ahead, %d behind", r.Ahead, r.Behind)
	}
	item.Facts = mapFacts("Branch", r.Branch, "Folder", r.Path, "Remote", r.Remote, "Upstream", sync)
	return item
}

func mapBackup(j *backups.Job, now time.Time) mapItem {
	id := strconv.FormatInt(j.ID, 10)
	item := mapItem{ID: "backup:" + id, Name: j.Name, Href: "/backups/" + id}
	switch j.TargetKind {
	case backups.TargetS3:
		item.Product = "aws"
	case backups.TargetB2:
		item.Product = "backblaze"
	}
	switch {
	case j.LastRun != nil && j.LastRun.Status == backups.StatusRunning:
		item.Tone, item.State = mapWarning, "backing up"
	case j.LastRun != nil && j.LastRun.Status == backups.StatusFailed:
		item.Tone, item.State = mapDanger, "last run failed"
	case !j.Enabled:
		item.Tone, item.State = mapIdle, "paused"
	case j.LastRun == nil:
		item.Tone, item.State = mapIdle, "never run"
	default:
		item.Tone, item.State = mapOK, "ran "+mapAgo(now.Sub(j.LastRun.StartedAt))+" ago"
	}
	if j.NextRun != nil && j.Enabled {
		item.Detail = "next in " + mapAgo(j.NextRun.Sub(now))
	}
	destination := string(j.TargetKind)
	switch {
	case j.Target.Bucket != "":
		destination += " · " + j.Target.Bucket
	case j.Target.Path != "":
		destination += " · " + j.Target.Path
	}
	last, next := "", ""
	if j.LastRun != nil {
		last = mapAgo(now.Sub(j.LastRun.StartedAt)) + " ago, " + string(j.LastRun.Status)
	}
	if j.NextRun != nil && j.Enabled {
		next = "in " + mapAgo(j.NextRun.Sub(now))
	}
	item.Facts = mapFacts("Destination", destination, "Schedule", j.Schedule, "Last run", last, "Next run", next,
		"Sources", strconv.Itoa(len(j.Sources)+len(j.DatabaseDumps)))
	return item
}

func mapTimer(t procs.Timer, now time.Time) mapItem {
	name := strings.TrimSuffix(t.Unit, ".timer")
	item := mapItem{
		ID: "timer:" + t.Unit, Name: name, Product: name,
		Href: "/processes/scheduled?timer=" + url.QueryEscape(t.Unit),
	}
	switch {
	case t.ActiveState != "active":
		item.Tone, item.State = mapIdle, t.ActiveState
	case t.Next != nil:
		item.Tone, item.State = mapOK, "next in "+mapAgo(t.Next.Sub(now))
	default:
		item.Tone, item.State = mapOK, "waiting"
	}
	if t.Activates != "" {
		item.Detail = t.Activates
	}
	next, last := "", ""
	if t.Next != nil {
		next = "in " + mapAgo(t.Next.Sub(now))
	}
	if t.Last != nil {
		last = mapAgo(now.Sub(*t.Last)) + " ago"
	}
	item.Facts = mapFacts("Runs", t.Activates, "Next", next, "Last", last)
	return item
}

func mapCron(tab procs.Crontab, job procs.CronJob) mapItem {
	command := strings.TrimSpace(job.Command)
	if len(command) > 60 {
		command = command[:57] + "…"
	}
	item := mapItem{
		ID:   fmt.Sprintf("cron:%s:%d", tab.Source, job.Line),
		Name: command, Product: "cron", Href: "/processes/scheduled",
		Tone: mapOK, State: job.Schedule, Detail: tab.Source,
	}
	if job.Disabled {
		item.Tone, item.State = mapIdle, "commented out"
	}
	item.Facts = mapFacts("Schedule", job.Schedule, "File", tab.Source, "Runs as", job.User)
	return item
}

// alertKindLabel is a rule's kind as the Alerts pages title it.
var alertKindLabel = map[string]string{
	"cpu_high": "CPU high", "memory_high": "Memory high", "disk_filling": "Disk filling",
	"container_down": "Container down", "backup_failed": "Backup failed",
	"cert_expiring": "Certificate expiring", "cert_expired": "Certificate expired",
	"renewal_failed": "Renewal failed", "served_drift": "Served certificate differs",
	"engine_down": "Proxy down", "upstream_down": "Upstream down",
	"watch_unreachable": "Watched site unreachable", "watch_untrusted": "Watched site untrusted",
	"watch_grade_below": "TLS grade below", "site_errors": "Site errors",
}

func alertName(kind string) string {
	if label, ok := alertKindLabel[kind]; ok {
		return label
	}
	return strings.ReplaceAll(kind, "_", " ")
}

func mapAlertItems(a mapAlerts) []mapItem {
	out := []mapItem{}
	rule := func(id, kind string, enabled bool, firing int, unjudged, href, scope string) mapItem {
		item := mapItem{ID: id, Name: alertName(kind), Href: href, Detail: scope,
			Facts: mapFacts("Watches", scope, "Firing for", mapPlural(firing, "subject", "subjects"))}
		switch {
		case !enabled:
			item.Tone, item.State = mapIdle, "paused"
		case firing > 0:
			item.Tone, item.State = mapDanger, fmt.Sprintf("firing · %d", firing)
		case unjudged != "":
			item.Tone, item.State = mapWarning, "cannot judge"
		default:
			item.Tone, item.State = mapOK, "quiet"
		}
		return item
	}
	for _, r := range a.Host {
		out = append(out, rule("alert:host:"+strconv.FormatInt(r.ID, 10), r.Kind, r.Enabled,
			a.HostFiring[r.ID], r.Unjudged, "/metrics/alerts", "host"))
	}
	for _, r := range a.Proxy {
		out = append(out, rule("alert:proxy:"+strconv.FormatInt(r.ID, 10), r.Kind, r.Enabled,
			a.ProxyFiring[r.ID], "", "/proxy/alerts", "proxy"))
	}
	return out
}

func mapChannel(c deploy.NotificationChannel) mapItem {
	item := mapItem{
		ID: "channel:" + strconv.FormatInt(c.ID, 10), Name: c.Name, Product: c.Kind,
		Href: "/deploy/notifications", Detail: c.Kind,
	}
	switch {
	case !c.Enabled:
		item.Tone, item.State = mapIdle, "off"
	case c.LastDelivery == nil:
		item.Tone, item.State = mapOK, "nothing sent yet"
	case c.LastDelivery.Status == "failed" || c.LastDelivery.Status == "dead":
		item.Tone, item.State = mapDanger, "last delivery failed"
	case c.LastDelivery.Status == "retrying" || c.LastDelivery.Status == "pending":
		item.Tone, item.State = mapWarning, c.LastDelivery.Status
	default:
		item.Tone, item.State = mapOK, "delivered"
	}
	last := ""
	if c.LastDelivery != nil {
		last = c.LastDelivery.Status
	}
	item.Facts = mapFacts("Kind", c.Kind, "Events", strconv.Itoa(len(c.Events)), "Last delivery", last)
	return item
}

func mapAccount(u *auth.User) mapItem {
	id := strconv.FormatInt(u.ID, 10)
	item := mapItem{
		ID: "account:" + id, Name: u.Username, Href: "/account/users", Detail: u.DisplayName,
		Tone: mapOK, State: string(u.Role),
	}
	if u.Disabled {
		item.Tone, item.State = mapIdle, "disabled"
	}
	if item.Detail == "" {
		if u.TOTPEnabled {
			item.Detail = "two-factor on"
		} else {
			item.Detail = "no two-factor"
		}
	}
	twoFactor, signedIn := "off", ""
	if u.TOTPEnabled {
		twoFactor = "on"
	}
	if !u.LastLoginAt.IsZero() {
		signedIn = u.LastLoginAt.Format("2 Jan 2006")
	}
	item.Facts = mapFacts("Role", string(u.Role), "Two-factor", twoFactor, "Last sign-in", signedIn)
	return item
}

func mapKey(k auth.APIToken, now time.Time) mapItem {
	item := mapItem{
		ID: "key:" + strconv.FormatInt(k.ID, 10), Name: k.Name, Href: "/account/keys",
		Tone: mapOK, State: string(k.Role), owner: k.UserID,
	}
	if k.Scope != "" {
		item.State = k.Scope
	}
	if k.ExpiresAt != nil && !k.ExpiresAt.After(now) {
		item.Tone, item.State = mapIdle, "expired"
	}
	if k.LastUsedAt != nil {
		item.Detail = "used " + mapAgo(now.Sub(*k.LastUsedAt)) + " ago"
	} else {
		item.Detail = "never used"
	}
	used, expires := "never", "never"
	if k.LastUsedAt != nil {
		used = mapAgo(now.Sub(*k.LastUsedAt)) + " ago"
	}
	if k.ExpiresAt != nil {
		expires = k.ExpiresAt.Format("2 Jan 2006")
	}
	item.Facts = mapFacts("Role", string(k.Role), "Reaches", k.Scope, "Last used", used, "Expires", expires)
	return item
}

func mapSystemUser(u linuxusers.User) mapItem {
	item := mapItem{
		ID: "sysuser:" + u.Username, Name: u.Username, Href: "/system-users",
		Detail: mapPlural(u.SSHKeys, "SSH key", "SSH keys"),
	}
	switch {
	case u.Locked:
		item.Tone, item.State = mapIdle, "locked"
	case u.CanLogin:
		item.Tone, item.State = mapOK, "can sign in"
	default:
		item.Tone, item.State = mapIdle, "no sign-in"
	}
	signedIn := ""
	if u.LastLogin != nil {
		signedIn = u.LastLogin.Format("2 Jan 2006")
	}
	item.Facts = mapFacts("Home", u.Home, "Shell", u.Shell, "SSH keys", strconv.Itoa(u.SSHKeys),
		"Last sign-in", signedIn)
	return item
}

func mapFirewall(fw *netsec.FirewallStatus) mapItem {
	item := mapItem{ID: "firewall", Name: "Firewall", Product: string(fw.Backend), Href: "/network/firewall"}
	if fw.Enabled {
		item.Tone, item.State = mapOK, "on"
		if fw.Policy.Incoming != "" {
			item.Detail = "incoming " + fw.Policy.Incoming
		}
	} else {
		// Off is a fact about the server, said in the colour of something to
		// look at; whether it should be on is the reader's call.
		item.Tone, item.State = mapWarning, "off"
	}
	item.Facts = mapFacts("Engine", string(fw.Backend), "Incoming", fw.Policy.Incoming,
		"Outgoing", fw.Policy.Outgoing)
	return item
}

func mapTunnels(v netx.VPNSummary) []mapItem {
	out := []mapItem{}
	if v.WireGuard.Installed {
		item := mapItem{ID: "vpn:wireguard", Name: "WireGuard", Product: "wireguard", Href: "/network/vpn"}
		switch {
		case v.WireGuard.Interfaces == 0:
			item.Tone, item.State = mapIdle, "no tunnels"
		case v.WireGuard.Online > 0:
			item.Tone, item.State = mapOK, fmt.Sprintf("%d of %d peers online", v.WireGuard.Online, v.WireGuard.Peers)
		default:
			item.Tone, item.State = mapIdle, mapPlural(v.WireGuard.Peers, "peer", "peers")
		}
		item.Detail = mapPlural(v.WireGuard.Interfaces, "tunnel", "tunnels")
		item.Facts = mapFacts("Tunnels", strconv.Itoa(v.WireGuard.Interfaces),
			"Peers online", fmt.Sprintf("%d of %d", v.WireGuard.Online, v.WireGuard.Peers))
		out = append(out, item)
	}
	if v.Tailscale.Installed {
		item := mapItem{ID: "vpn:tailscale", Name: "Tailscale", Product: "tailscale", Href: "/network/vpn"}
		if v.Tailscale.Running {
			item.Tone, item.State = mapOK, "connected"
			item.Detail = fmt.Sprintf("%d of %d peers online", v.Tailscale.Online, v.Tailscale.Peers)
			item.Facts = mapFacts("Peers online", fmt.Sprintf("%d of %d", v.Tailscale.Online, v.Tailscale.Peers))
		} else {
			item.Tone, item.State = mapIdle, "stopped"
		}
		out = append(out, item)
	}
	return out
}

// mapFacts is a box's details as the inspector lists them: label and value
// in pairs, leaving out the ones with nothing to say.
func mapFacts(pairs ...string) []mapFact {
	out := []mapFact{}
	for i := 0; i+1 < len(pairs); i += 2 {
		if strings.TrimSpace(pairs[i+1]) != "" {
			out = append(out, mapFact{Label: pairs[i], Value: pairs[i+1]})
		}
	}
	return out
}

func mapPlural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// mapAgo is a span in the fewest words a box has room for: 40s, 12m, 3h, 5d.
func mapAgo(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
