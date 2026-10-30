package api

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/gitx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// The server map's lines between kinds, beyond the ones a kind draws as its
// own shelf is read (a container's mounts, a backup's dumps). Each is read
// from a source that knows it, as map_build.go's lines are: what a
// deployment's variables refer to and its plan depends on, what the
// databases topology saw a container's environment name or the engine saw
// connected, who holds the port a site or a stream forwards to, the
// checkout a deployment is built from or a PM2 app runs in, the folders a
// backup takes, the account a key belongs to and what an MCP key reaches,
// the unit a timer starts, the channels an alert rule tells and the Linux
// account a shell or a job runs as.

// mapViaOrder ranks the ways a line is known, surest first: what was
// declared — a binding, a reference, a dependency — before what was seen,
// and a session or a mention in an environment last. A pair known several
// ways is drawn once, named by the first.
var mapViaOrder = []string{
	"binding", "reference", "dependency", "release", "route", "serves", "pooler",
	"source", "workdir", "member", "self", "mount", "certificate", "backup", "starts",
	"access", "owner", "notifies", "user", "image", "network", "tailnet", "env", "session",
}

func mapViaRank(via string) int {
	for i, v := range mapViaOrder {
		if v == via {
			return i
		}
	}
	return len(mapViaOrder)
}

// mergeLinks makes each pair of things one line, named by the surest way it
// is known and drawn in that way's direction, with the other ways in Also.
func mergeLinks(links []mapLink) []mapLink {
	type pair struct{ a, b string }
	at := map[pair]int{}
	out := []mapLink{}
	for _, l := range links {
		key := pair{l.From, l.To}
		if l.To < l.From {
			key = pair{l.To, l.From}
		}
		i, ok := at[key]
		if !ok {
			at[key] = len(out)
			out = append(out, l)
			continue
		}
		have := &out[i]
		if mapViaRank(l.Via) < mapViaRank(have.Via) {
			l.Also = append([]string{have.Via}, have.Also...)
			*have = l
		} else {
			have.Also = append(have.Also, l.Via)
		}
	}
	for i := range out {
		if len(out[i].Also) == 0 {
			continue
		}
		seen := map[string]bool{out[i].Via: true}
		also := []string{}
		for _, via := range out[i].Also {
			if !seen[via] {
				seen[via] = true
				also = append(also, via)
			}
		}
		sort.SliceStable(also, func(a, b int) bool { return mapViaRank(also[a]) < mapViaRank(also[b]) })
		out[i].Also = also
		if len(also) == 0 {
			out[i].Also = nil
		}
	}
	return out
}

func mapPM2ID(p procs.PM2Process) string { return fmt.Sprintf("pm2:%s:%d", p.DaemonID, p.ID) }

func mapCronID(tab procs.Crontab, job procs.CronJob) string {
	return fmt.Sprintf("cron:%s:%d", tab.Source, job.Line)
}

// composeProjectLabel is the label Compose puts on what it creates for a
// project — its containers, volumes and networks.
const composeProjectLabel = "com.docker.compose.project"

func mapJoins(b *mapBuilder, in mapInputs) {
	project := func(id int64) string { return "project:" + strconv.FormatInt(id, 10) }
	db := func(id int64) string { return "db:" + strconv.FormatInt(id, 10) }

	containerByName := map[string]string{}
	containerByShort := map[string]string{}
	if in.containers.Have {
		for _, c := range in.containers.Value {
			id := "container:" + c.ID
			containerByName[c.Name] = id
			if len(c.ID) >= 12 {
				containerByShort[c.ID[:12]] = id
			}
		}
		// The dashboard's own stack: the compose project of the container
		// mounting its data directory, as the ports page tells its sockets.
		// That container is the one its store is kept in.
		if self := proxysvc.SelfContainer(runningContainers(in.containers.Value), in.dataDir); self != nil {
			if self.Project != "" {
				b.link("dashboard:self", "stack:"+self.Project, "self")
			}
			b.link("container:"+self.ID, "dashboard:store", "mount")
		}
	}

	// A stack's volumes and networks, by the label Compose made them with.
	if in.volumes.Have {
		for _, v := range in.volumes.Value {
			if p := v.Labels[composeProjectLabel]; p != "" {
				b.link("stack:"+p, "volume:"+v.Name, "member")
			}
		}
	}
	if in.networks.Have {
		for _, n := range in.networks.Value {
			if p := n.Labels[composeProjectLabel]; p != "" {
				b.link("stack:"+p, "network:"+n.ID, "member")
			}
		}
	}

	// The databases, by the names a reference may give them, and the unit a
	// server on this machine runs under.
	dbByName := map[string]int64{}
	if in.databases.Have {
		for _, e := range in.databases.Value {
			dbByName[e.Name] = e.ID
			if e.Unit != "" {
				b.link("unit:"+e.Unit, db(e.ID), "serves")
			}
		}
	}
	databaseID := func(target string) int64 {
		candidates, _, _, _ := databaseReferenceTarget(target)
		for _, c := range candidates {
			if id, err := strconv.ParseInt(c, 10, 64); err == nil && id > 0 {
				return id
			}
			if id, ok := dbByName[c]; ok {
				return id
			}
		}
		return 0
	}
	if in.relations.Have {
		for _, r := range in.relations.Value.refs {
			from := project(r.projectID)
			switch r.kind {
			case "database":
				if id := databaseID(r.target); id > 0 {
					b.link(from, db(id), r.via)
				}
			case "domain":
				b.link(from, "domain:"+strings.ToLower(r.target), r.via)
			case "storage":
				b.link(from, "volume:"+r.target, r.via)
			case "backup":
				b.link(from, "backup:"+r.target, r.via)
			}
		}
		conns := make([]int64, 0, len(in.relations.Value.poolers))
		for conn := range in.relations.Value.poolers {
			conns = append(conns, conn)
		}
		sort.Slice(conns, func(i, j int) bool { return conns[i] < conns[j] })
		for _, conn := range conns {
			if c, ok := containerByName[in.relations.Value.poolers[conn]]; ok {
				b.link(c, db(conn), "pooler")
			}
		}
	}
	if in.uses.Have {
		for _, u := range in.uses.Value {
			from := containerByName[u.container]
			if u.projectID > 0 {
				from = project(u.projectID)
			}
			b.link(from, db(u.connID), u.via)
		}
	}

	// What a deployment runs and what it is built from.
	if in.projects.Have {
		imageByRef := map[string]string{}
		if in.images.Have {
			for _, img := range in.images.Value {
				for _, ref := range append(append([]string{}, img.RepoTags...), img.RepoDigests...) {
					imageByRef[imageRefKey(ref)] = "image:" + img.ID
				}
			}
		}
		for _, d := range in.projects.Value {
			for _, ref := range d.Images {
				if id, ok := imageByRef[imageRefKey(ref)]; ok {
					b.link(project(d.ID), id, "image")
				}
			}
			if in.repos.Have {
				for _, r := range sourceRepos(in.repos.Value, d) {
					b.link(project(d.ID), "repo:"+r.Path, "source")
				}
			}
		}
	}

	// The Linux account a shell, an app or a job runs as.
	user := func(from, name string) {
		if name != "" {
			b.link(from, "sysuser:"+name, "user")
		}
	}
	if in.terminals.Have {
		for _, t := range in.terminals.Value {
			user("terminal:"+t.ID, t.User)
		}
	}
	pm2ByPID := map[int32]string{}
	if in.pm2.Have {
		for _, p := range in.pm2.Value {
			id := mapPM2ID(p)
			if p.PID > 0 {
				pm2ByPID[int32(p.PID)] = id
			}
			user(id, p.User)
			if in.repos.Have {
				if r, ok := repoHolding(in.repos.Value, p.CWD); ok {
					b.link(id, "repo:"+r.Path, "workdir")
				}
			}
		}
	}
	if in.cron.Have {
		for _, tab := range in.cron.Value {
			for _, job := range tab.Jobs {
				name := job.User
				if name == "" {
					name = tab.User
				}
				user(mapCronID(tab, job), name)
			}
		}
	}
	if in.timers.Have {
		for _, t := range in.timers.Value {
			if t.Activates != "" {
				b.link("timer:"+t.Unit, "unit:"+t.Activates, "starts")
			}
		}
	}

	// The channels an alert rule tells: the ones it names, or every enabled
	// one where it names none.
	if in.alerts.Have {
		enabled := []int64{}
		if in.channels.Have {
			for _, c := range in.channels.Value {
				if c.Enabled {
					enabled = append(enabled, c.ID)
				}
			}
		}
		notify := func(from string, channels []int64) {
			if len(channels) == 0 {
				channels = enabled
			}
			for _, id := range channels {
				b.link(from, "channel:"+strconv.FormatInt(id, 10), "notifies")
			}
		}
		for _, r := range in.alerts.Value.Host {
			notify("alert:host:"+strconv.FormatInt(r.ID, 10), r.Channels)
		}
		for _, r := range in.alerts.Value.Proxy {
			notify("alert:proxy:"+strconv.FormatInt(r.ID, 10), r.Channels)
		}
	}

	// A key's account, and the databases an MCP key reaches.
	if in.keys.Have {
		for _, k := range in.keys.Value {
			if k.Revoked {
				continue
			}
			id := "key:" + strconv.FormatInt(k.ID, 10)
			b.link(id, "account:"+strconv.FormatInt(k.UserID, 10), "owner")
			for _, c := range k.Connections {
				b.link(id, db(c.ConnectionID), "access")
			}
		}
	}

	// The folders a backup takes: a volume's or a checkout's, or one holding
	// them, unless the job leaves that folder out.
	if in.backups.Have {
		for _, j := range in.backups.Value {
			id := "backup:" + strconv.FormatInt(j.ID, 10)
			takes := func(path string) bool {
				if path == "" {
					return false
				}
				for _, ex := range j.Excludes {
					if filepath.IsAbs(ex) && within(path, ex) {
						return false
					}
				}
				for _, src := range j.Sources {
					if within(path, src) || within(src, path) {
						return true
					}
				}
				return false
			}
			if in.volumes.Have {
				for _, v := range in.volumes.Value {
					if takes(v.Mountpoint) {
						b.link("volume:"+v.Name, id, "backup")
					}
				}
			}
			if in.repos.Have {
				for _, r := range in.repos.Value {
					if takes(r.Path) {
						b.link("repo:"+r.Path, id, "backup")
					}
				}
			}
		}
	}

	if in.listeners.Have {
		mapRoutes(b, in, containerByShort, pm2ByPID)
	}
}

// mapRoutes draws a site or a stream to what holds the port it forwards to on
// this machine — a container, a unit, a PM2 app, this dashboard — by the
// proxy's own reading of an upstream (SitesForUpstreamPort), as the ports
// page says what routes to each socket.
func mapRoutes(b *mapBuilder, in mapInputs, containers map[string]string, pm2 map[int32]string) {
	var sites []proxysvc.VHost
	if in.vhosts.Have {
		sites = in.vhosts.Value
	}
	var streams []proxysvc.StreamEntry
	if in.streams.Have {
		streams = append(append(streams, in.streams.Value.Streams...), in.streams.Value.Paused...)
	}
	// Only a port something forwards to is looked up, so a host with a
	// hundred sockets costs the few the proxy names.
	wanted := map[int]bool{}
	for _, v := range sites {
		for _, up := range v.Upstreams {
			for _, target := range strings.Fields(up) {
				if port, ok := upstreamPort(target); ok {
					wanted[port] = true
				}
			}
		}
	}
	every := false
	for _, s := range streams {
		if s.SamePort {
			// Forwarded port for port: the ports it reaches are its own range.
			last := max(s.ListenEnd, s.Listen)
			if last-s.Listen > 1024 {
				every = true
			}
			for port := s.Listen; port <= last && !every; port++ {
				wanted[port] = true
			}
			continue
		}
		for _, target := range append([]string{s.Upstream}, streamServers(s)...) {
			if port, ok := upstreamPort(target); ok {
				wanted[port] = true
			}
		}
	}
	for _, l := range in.listeners.Value {
		if !wanted[int(l.Port)] && !every {
			continue
		}
		owners := listenerOwners(l, containers, pm2)
		if len(owners) == 0 {
			continue
		}
		if l.Protocol == "tcp" {
			for _, v := range proxysvc.SitesForUpstreamPort(sites, int(l.Port), l.Address) {
				for _, name := range v.ServerNames {
					if !servedName(name) {
						continue
					}
					for _, owner := range owners {
						b.link("domain:"+strings.ToLower(name), owner, "route")
					}
				}
			}
		}
		for _, s := range proxysvc.StreamsForUpstreamPort(streams, int(l.Port), l.Address) {
			if s.Protocol != "" && s.Protocol != "both" && s.Protocol != l.Protocol {
				continue
			}
			for _, owner := range owners {
				b.link("stream:"+s.Name, owner, "route")
			}
		}
	}
}

func streamServers(s proxysvc.StreamEntry) []string {
	out := make([]string, 0, len(s.Servers))
	for _, server := range s.Servers {
		out = append(out, server.Address)
	}
	return out
}

// listenerOwners is who a socket answers for, as boxes on the map: this
// dashboard where it is its own, and the container, the PM2 app or the
// systemd service holding it — the service a socket unit starts, where
// systemd holds the socket for it.
func listenerOwners(l proxysvc.Listener, containers map[string]string, pm2 map[int32]string) []string {
	out := []string{}
	if l.Self {
		out = append(out, "dashboard:self")
	}
	switch {
	case l.Container != nil:
		if id := l.Container.ID; len(id) >= 12 {
			if box, ok := containers[id[:12]]; ok {
				out = append(out, box)
			}
		}
	case l.Manager == "pm2":
		for _, pid := range append([]int32{l.PID}, l.PIDs...) {
			if box, ok := pm2[pid]; ok {
				out = append(out, box)
				break
			}
		}
	case l.Manager == "systemd" && strings.HasSuffix(l.ManagerName, ".service"):
		out = append(out, "unit:"+l.ManagerName)
	case l.Activates != "":
		out = append(out, "unit:"+l.Activates)
	}
	return out
}

// upstreamPort is the port an upstream names, whatever its host: a site's
// http://127.0.0.1:3000/api or a stream's 10.0.0.4:5432.
func upstreamPort(upstream string) (int, bool) {
	addr := upstream
	if i := strings.Index(addr, "://"); i >= 0 {
		addr = addr[i+3:]
	}
	if i := strings.IndexAny(addr, "/?"); i >= 0 {
		addr = addr[:i]
	}
	_, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, false
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 {
		return 0, false
	}
	return port, true
}

// imageRefKey is an image reference as the daemon lists its tags: Docker
// Hub's own registry and library namespace left off, as `docker images` does.
func imageRefKey(ref string) string {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "docker.io/")
	return strings.TrimPrefix(ref, "library/")
}

// sourceRepos is the checkouts on this server of what a deployment is built
// from: the one its local source is read from, or else every checkout of the
// remote its Git source is fetched from.
func sourceRepos(repos []gitx.Repo, d deploy.DeploymentSummary) []gitx.Repo {
	if d.SourcePath != "" {
		if r, ok := repoHolding(repos, d.SourcePath); ok {
			return []gitx.Repo{r}
		}
	}
	remote := remoteKey(d.SourceRemote)
	if remote == "" {
		return nil
	}
	out := []gitx.Repo{}
	for _, r := range repos {
		if remoteKey(r.Remote) == remote {
			out = append(out, r)
		}
	}
	return out
}

// repoHolding is the checkout a folder is in: the deepest one holding it, so
// a deployment from one service's folder of a monorepo is drawn to the
// monorepo.
func repoHolding(repos []gitx.Repo, path string) (gitx.Repo, bool) {
	var best gitx.Repo
	found := false
	if path == "" {
		return best, false
	}
	for _, r := range repos {
		if r.Path != "" && within(path, r.Path) && (!found || len(r.Path) > len(best.Path)) {
			best, found = r, true
		}
	}
	return best, found
}

// within says whether path is dir or under it.
func within(path, dir string) bool {
	path, dir = filepath.Clean(path), filepath.Clean(dir)
	return path == dir || dir == "/" || strings.HasPrefix(path, dir+"/")
}

// remoteKey is a Git remote as the repository it names — host and path,
// lower-cased, with no scheme, user or ".git" — so
// git@github.com:acme/shop.git and https://github.com/acme/shop are one
// repository. A remote that is a folder on this machine is that folder.
func remoteKey(remote string) string {
	r := strings.TrimSpace(remote)
	switch {
	case r == "":
		return ""
	case strings.HasPrefix(r, "/"):
		return strings.TrimSuffix(filepath.Clean(r), ".git")
	case strings.HasPrefix(r, "file://"):
		return strings.TrimSuffix(filepath.Clean(strings.TrimPrefix(r, "file://")), ".git")
	case strings.Contains(r, "://"):
		u, err := url.Parse(r)
		if err != nil || u.Hostname() == "" {
			return ""
		}
		r = u.Hostname() + "/" + strings.TrimPrefix(u.Path, "/")
	default:
		// The scp form: [user@]host:path.
		if at := strings.Index(r, "@"); at >= 0 {
			r = r[at+1:]
		}
		host, path, ok := strings.Cut(r, ":")
		if !ok || host == "" || strings.Contains(host, "/") {
			return ""
		}
		r = host + "/" + strings.TrimPrefix(path, "/")
	}
	r = strings.TrimSuffix(strings.TrimSuffix(r, "/"), ".git")
	return strings.ToLower(r)
}
