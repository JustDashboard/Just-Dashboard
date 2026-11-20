package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	fc "github.com/Wayy01/Just-Dashboard/backend/internal/filecontext"
	"github.com/Wayy01/Just-Dashboard/backend/internal/gitx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// What a folder is to this server, for the Files page.
//
// `GET /files/context?path=` answers for the folder on screen and each entry
// in it: a container's volume, a site's root, a Git checkout, the backup that
// covers it or that none does. It asks the machine nothing new. Every source
// is the reading its own page already makes, held between requests in the
// server map's shelf (map_sources.go) under the same keys, so an open map and
// an open Files page share one read; the two the map does not draw — Compose
// projects with their files, and the folders deployments keep data in — are
// held there too. The page asks after its listing is drawn and never waits
// for this.
//
// Bounded: a source that has no answer within contextBudget is left out and
// named in `partial`. The only command run on the folder's account is a Git
// checkout's status, for the checkout on screen and for entries that are
// checkouts, at most contextGitSummaries of them, each remembered briefly.
//
// Who sees what: a fact from a source is sent only to someone whose role
// could open that source's page (contextSourceNeeds).
func (s *Server) mountFilesContextRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/context", s.handle(s.handleFileContext))
}

const (
	// contextBudget is how long one answer waits for a source that is being
	// read. The held answers make most requests immediate; a cold source
	// that is slower than this is left out of this answer and fills the next.
	contextBudget = 800 * time.Millisecond
	// contextEntries bounds the entries of one folder that are matched.
	contextEntries = 5000
	// contextGitSummaries bounds the checkouts whose status one answer asks
	// for; each is remembered for gitSummaryFresh.
	contextGitSummaries = 6
	gitSummaryFresh     = 15 * time.Second
)

// contextSourceNeeds is the capability each source's own page's read routes
// require. Every one of them is open to any signed-in role today; the table
// is what keeps a source added behind a capability from leaking here.
var contextSourceNeeds = map[string]auth.Capability{
	fc.SourceDocker:    auth.CapRead,
	fc.SourceProxy:     auth.CapRead,
	fc.SourceBackups:   auth.CapRead,
	fc.SourceGit:       auth.CapRead,
	fc.SourceDeploy:    auth.CapRead,
	fc.SourceDatabases: auth.CapRead,
	fc.SourceProcesses: auth.CapRead,
	fc.SourceDashboard: auth.CapRead,
}

// fileContextResponse is the resolver's answer and what it was made from.
type fileContextResponse struct {
	fc.Answer
	// Partial names the sources this answer could not include: still being
	// read, or failing with nothing kept.
	Partial []string `json:"partial"`
	// Truncated says the folder held more entries than were matched.
	Truncated bool      `json:"truncated,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
}

func (s *Server) handleFileContext(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	hidden, err := httpx.QueryBool(r, "hidden")
	if err != nil {
		return err
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		return httpx.BadRequest("path query parameter is required")
	}
	p := httpx.MustPrincipal(r)
	in := s.readFileContext()
	things, jobs := contextThings(in)

	known := make([]string, 0, len(things)*2)
	for _, t := range things {
		known = append(known, t.Paths...)
	}
	for _, j := range jobs {
		known = append(known, j.Sources...)
		known = append(known, j.Excludes...)
	}
	paths, err := s.modules.hostFiles.ContextPaths(path, hidden, known, contextEntries)
	if err != nil {
		return mapFileError(err)
	}
	resolveThings(things, jobs, paths.Known)

	children := make([]fc.Child, len(paths.Children))
	for i, c := range paths.Children {
		children[i] = fc.Child{Path: c.Path, Real: c.Real}
	}
	s.freshenCheckouts(things, paths.Folder, children)

	answer := fc.Resolve(fc.Input{
		Folder: paths.Folder, Children: children, Things: things, Jobs: jobs, Read: in.read,
		Can: func(source string) bool {
			need, ok := contextSourceNeeds[source]
			return ok && p.Can(need)
		},
	})
	partial := []string{}
	for source := range in.partial {
		if need, ok := contextSourceNeeds[source]; ok && p.Can(need) {
			partial = append(partial, source)
		}
	}
	sort.Strings(partial)
	httpx.JSON(w, http.StatusOK, fileContextResponse{
		Answer: answer, Partial: partial, Truncated: paths.Truncated, CheckedAt: time.Now().UTC(),
	})
	return nil
}

// contextInputs is every source's held reading for one answer.
type contextInputs struct {
	containers mapRead[[]dockerx.Container]
	volumes    mapRead[[]dockerx.Volume]
	stacks     mapRead[[]dockerx.ComposeStack]
	projects   mapRead[[]deploy.DeploymentSummary]
	folders    mapRead[[]contextDeployFolder]
	vhosts     mapRead[[]proxysvc.VHost]
	certs      mapRead[[]proxysvc.Certificate]
	backups    mapRead[[]*backups.Job]
	repos      mapRead[[]gitx.Repo]
	units      mapRead[*procs.Inventory]
	pm2        mapRead[[]procs.PM2Process]
	databases  []dbx.Instance
	dataDir    string

	// read names the sources with at least one answer; partial those with
	// a reading that has none.
	read    map[string]bool
	partial map[string]bool
}

// contextSpec is a map source's spec with this read's own budget: the same
// key, freshness and limit, so the two pages share one held answer.
func contextSpec(key string, base mapSourceSpec) mapSourceSpec {
	base = spec(key, base)
	base.budget = contextBudget
	return base
}

// readFileContext asks every source at once, each for at most contextBudget.
func (s *Server) readFileContext() contextInputs {
	in := contextInputs{read: map[string]bool{}, partial: map[string]bool{}, dataDir: s.Cfg.DataDir}
	c := &s.mapSources
	m := &s.modules
	var wg sync.WaitGroup
	run := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn()
		}()
	}
	if m.docker != nil {
		run(func() {
			in.containers = readMapSource(c, contextSpec("containers", mapFast), func(ctx context.Context) ([]dockerx.Container, error) {
				return m.docker.ListContainers(ctx, true)
			})
		})
		run(func() { in.volumes = readMapSource(c, contextSpec("volumes", mapSteady), m.docker.ListVolumes) })
		run(func() {
			in.stacks = readMapSource(c, contextSpec("stacks", mapSlow), func(ctx context.Context) ([]dockerx.ComposeStack, error) {
				return m.docker.ListStacks(ctx, s.Cfg.ComposeRoots)
			})
		})
	}
	if m.deployRuns != nil {
		run(func() { in.projects = readMapSource(c, contextSpec("projects", mapFast), s.mapReadProjects) })
	}
	run(func() { in.folders = readMapSource(c, contextSpec("deployFolders", mapSteady), s.contextDeployFolders) })
	if m.proxy != nil {
		run(func() { in.vhosts = readMapSource(c, contextSpec("vhosts", mapSteady), s.mapReadVHosts) })
		run(func() {
			in.certs = readMapSource(c, contextSpec("certificates", mapSlow), m.proxy.CertificateInventory)
		})
	}
	if m.backupStore != nil {
		run(func() { in.backups = readMapSource(c, contextSpec("backups", mapSteady), s.mapReadBackups) })
	}
	if m.git != nil && m.git.Available() {
		run(func() { in.repos = readMapSource(c, contextSpec("repositories", mapRare), m.git.Discover) })
	}
	if m.systemd != nil && m.systemd.Available() {
		run(func() { in.units = readMapSource(c, contextSpec("units", mapSlow), m.systemd.Inventory) })
	}
	if m.pm2 != nil && m.pm2.Available() {
		run(func() { in.pm2 = readMapSource(c, contextSpec("pm2", mapSteady), m.pm2.List) })
	}
	wg.Wait()

	// The databases page's own inventory, as a role without system.admin is
	// shown it (no container environment read), and only as last kept: it
	// is read again in the background when stale, never waited on here.
	if m.docker != nil {
		if kept, ok := s.keptInventory(false); ok {
			in.databases = kept.instances
			in.read[fc.SourceDatabases] = true
		} else {
			in.partial[fc.SourceDatabases] = true
		}
	}

	// A source with an answer is read. One still being read when the budget
	// ran out is partial: this answer is missing it and the next one will
	// not be. One that failed (no Docker socket, no proxy on this host) is
	// left out without being called partial, or the page would say it is
	// still reading something that is never going to answer.
	note := func(source string, asked, have bool, err error) {
		switch {
		case !asked:
		case have:
			in.read[source] = true
		case errors.Is(err, errMapReading):
			in.partial[source] = true
		}
	}
	note(fc.SourceDocker, m.docker != nil, in.containers.Have, in.containers.Err)
	note(fc.SourceDocker, m.docker != nil, in.volumes.Have, in.volumes.Err)
	note(fc.SourceDocker, m.docker != nil, in.stacks.Have, in.stacks.Err)
	note(fc.SourceDeploy, m.deployRuns != nil, in.projects.Have, in.projects.Err)
	note(fc.SourceDeploy, true, in.folders.Have, in.folders.Err)
	note(fc.SourceProxy, m.proxy != nil, in.vhosts.Have, in.vhosts.Err)
	note(fc.SourceProxy, m.proxy != nil, in.certs.Have, in.certs.Err)
	note(fc.SourceBackups, m.backupStore != nil, in.backups.Have, in.backups.Err)
	note(fc.SourceGit, m.git != nil && m.git.Available(), in.repos.Have, in.repos.Err)
	note(fc.SourceProcesses, m.systemd != nil && m.systemd.Available(), in.units.Have, in.units.Err)
	note(fc.SourceProcesses, m.pm2 != nil && m.pm2.Available(), in.pm2.Have, in.pm2.Err)
	// A source read in part (containers but not yet volumes) gives what it
	// has and is still named partial.
	in.read[fc.SourceDashboard] = in.dataDir != ""
	// Coverage is said only from a complete list of jobs.
	if !in.backups.Have {
		delete(in.read, fc.SourceBackups)
	}
	return in
}

// contextDeployFolder is a folder a deployment names in the store: the
// checkout it was created from, or a host path its plan keeps data in.
type contextDeployFolder struct {
	projectID int64
	name      string
	path      string
	data      bool
}

func (s *Server) contextDeployFolders(ctx context.Context) ([]contextDeployFolder, error) {
	out := []contextDeployFolder{}
	rows, err := s.Store.DB.QueryContext(ctx, `
	  SELECT p.id, p.name, d.resource_id
	    FROM deploy_dependencies d
	    JOIN deploy_environments e ON e.id = d.environment_id
	    JOIN deploy_projects p ON p.id = e.project_id
	   WHERE d.release_id = 0 AND d.kind = 'storage' AND d.resource_kind = 'bind_path'
	     AND e.archived_at = 0 AND p.archived_at = 0
	   ORDER BY d.id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var f contextDeployFolder
		if err := rows.Scan(&f.projectID, &f.name, &f.path); err != nil {
			rows.Close()
			return nil, err
		}
		f.data = true
		out = append(out, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if s.modules.deployStore != nil {
		projects, err := s.modules.deployStore.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range projects {
			if p.ArchivedAt == nil && filepath.IsAbs(p.RepoPath) {
				out = append(out, contextDeployFolder{projectID: p.ID, name: p.Name, path: p.RepoPath})
			}
		}
	}
	return out, nil
}

// contextThings turns each reading into the facts it holds and the paths
// they are about, before those paths are resolved.
func contextThings(in contextInputs) ([]fc.Thing, []fc.Job) {
	var things []fc.Thing
	add := func(t fc.Thing) {
		if t.Fact.Names == nil {
			t.Fact.Names = []string{}
		}
		things = append(things, t)
	}

	// The dashboard's own: its data, and the stack it runs as.
	if in.dataDir != "" {
		add(fc.Thing{
			Fact:  fc.Fact{Kind: fc.KindDashboardData, Source: fc.SourceDashboard, Href: "/dashboard", Inherits: true},
			Paths: []string{in.dataDir}, Notable: true,
		})
	}
	selfContainers := map[string]bool{}
	for _, c := range in.containers.Value {
		for _, mnt := range c.Mounts {
			if in.dataDir != "" && (filepath.Clean(mnt.Source) == filepath.Clean(in.dataDir) || filepath.Clean(mnt.Destination) == filepath.Clean(in.dataDir)) {
				selfContainers[c.ID] = true
			}
		}
		if selfContainers[c.ID] {
			if dir := c.Labels["com.docker.compose.project.working_dir"]; filepath.IsAbs(dir) {
				add(fc.Thing{
					Fact:  fc.Fact{Kind: fc.KindDashboardInstall, Source: fc.SourceDashboard, Href: "/dashboard", Inherits: true},
					Paths: []string{dir},
				})
			}
		}
	}

	// Docker: volumes with the containers using them, bind mounts, stacks.
	users := map[string][]dockerx.Container{}
	for _, c := range in.containers.Value {
		for _, mnt := range c.Mounts {
			switch mnt.Type {
			case "volume":
				users[mnt.Name] = append(users[mnt.Name], c)
			case "bind":
				// The dashboard's own containers mirror the server's tree
				// (/home, /etc, / itself); those are said as its own data.
				if selfContainers[c.ID] || !filepath.IsAbs(mnt.Source) {
					continue
				}
				depth := strings.Count(filepath.Clean(mnt.Source), "/")
				add(fc.Thing{
					Fact: fc.Fact{
						Kind: fc.KindBind, Source: fc.SourceDocker, Names: []string{c.Name, mnt.Destination},
						Image: c.Image, State: c.State, Href: "/docker/containers/" + url.PathEscape(c.ID),
						// A top-level folder (/srv, /home) bound whole does not
						// make everything in it the container's.
						Inherits: depth >= 2,
					},
					Paths: []string{mnt.Source}, Notable: depth >= 2,
				})
			}
		}
	}
	for _, v := range in.volumes.Value {
		if !filepath.IsAbs(v.Mountpoint) {
			continue
		}
		names := []string{v.Name}
		image, state := "", "unused"
		for _, c := range users[v.Name] {
			if selfContainers[c.ID] {
				continue
			}
			names = append(names, c.Name)
			if image == "" {
				image = c.Image
			}
			state = "used"
		}
		paths := []string{v.Mountpoint}
		// /var/lib/docker/volumes/<name> is what a listing of the volumes
		// folder shows; the files are in its _data.
		if dir := filepath.Dir(v.Mountpoint); filepath.Base(dir) == v.Name {
			paths = append(paths, dir)
		}
		add(fc.Thing{
			Fact: fc.Fact{
				Kind: fc.KindVolume, Source: fc.SourceDocker, Names: names, Image: image, State: state,
				Href: "/docker/volumes/" + url.PathEscape(v.Name), Inherits: true,
			},
			Paths: paths, Notable: true,
		})
	}
	for _, st := range in.stacks.Value {
		href := "/docker/stacks/" + url.PathEscape(st.Name)
		state := "stopped"
		if st.Running > 0 {
			state = "running"
		}
		if filepath.IsAbs(st.WorkingDir) {
			add(fc.Thing{
				Fact: fc.Fact{Kind: fc.KindCompose, Source: fc.SourceDocker, Names: []string{st.Name},
					Product: "docker-compose", State: state, Href: href, Inherits: true},
				Paths: []string{st.WorkingDir}, Notable: true,
			})
		}
		for _, f := range st.ConfigFiles {
			if !filepath.IsAbs(f) && filepath.IsAbs(st.WorkingDir) {
				f = filepath.Join(st.WorkingDir, f)
			}
			add(fc.Thing{
				Fact: fc.Fact{Kind: fc.KindComposeFile, Source: fc.SourceDocker, Names: []string{st.Name},
					Product: "docker-compose", State: state, Href: href},
				Paths: []string{f},
			})
		}
	}

	// Deployments: the checkout each builds from, and its data folders.
	for _, d := range in.projects.Value {
		if filepath.IsAbs(d.SourcePath) {
			add(fc.Thing{
				Fact: fc.Fact{Kind: fc.KindDeploySource, Source: fc.SourceDeploy, Names: []string{d.Name},
					Href: "/deploy/" + strconv.FormatInt(d.ID, 10), Inherits: true},
				Paths: []string{d.SourcePath}, Notable: true,
			})
		}
	}
	for _, f := range in.folders.Value {
		kind := fc.KindDeploySource
		if f.data {
			kind = fc.KindDeployData
		}
		add(fc.Thing{
			Fact: fc.Fact{Kind: kind, Source: fc.SourceDeploy, Names: []string{f.name},
				Href: "/deploy/" + strconv.FormatInt(f.projectID, 10), Inherits: true},
			Paths: []string{f.path}, Notable: true,
		})
	}

	// Proxy: what each site serves, its files, and the certificates.
	for _, v := range in.vhosts.Value {
		names := siteNames(v)
		href := "/proxy/sites/" + url.PathEscape(v.Name)
		product := string(v.Kind)
		if v.Kind == proxysvc.KindCaddyIngress {
			product = string(proxysvc.KindCaddy)
		}
		state := ""
		if !v.Enabled {
			state = "disabled"
		}
		if len(v.Roots) > 0 {
			add(fc.Thing{
				Fact: fc.Fact{Kind: fc.KindSiteRoot, Source: fc.SourceProxy, Names: names, Product: product,
					State: state, Href: href, Inherits: true},
				Paths: v.Roots, Notable: true,
			})
		}
		add(fc.Thing{
			Fact:  fc.Fact{Kind: fc.KindSiteConfig, Source: fc.SourceProxy, Names: names, Product: product, State: state, Href: href},
			Paths: nonEmpty(v.Path, v.EnabledPath, v.ResolvesTo),
		})
		logs := nonEmpty(v.AccessLogPath, v.ErrorLogPath)
		if len(logs) > 0 {
			add(fc.Thing{
				Fact:  fc.Fact{Kind: fc.KindSiteLog, Source: fc.SourceProxy, Names: names, Product: product, Href: href},
				Paths: logs,
			})
		}
	}
	for _, cert := range in.certs.Value {
		if !filepath.IsAbs(cert.Path) {
			continue
		}
		names := cert.Domains
		if len(names) == 0 && cert.Name != "" {
			names = []string{cert.Name}
		}
		state := "ok"
		switch {
		case cert.Error != "":
			state = "unreadable"
		case cert.Expired:
			state = "expired"
		case cert.Staging:
			state = "staging"
		case cert.Expiring:
			state = "expiring"
		}
		f := fc.Fact{Kind: fc.KindCertificate, Source: fc.SourceProxy, Names: append([]string{}, names...),
			State: state, Detail: cert.Issuer, Href: mapCertificateHref(cert)}
		if !cert.NotAfter.IsZero() {
			until := cert.NotAfter
			f.Until = &until
		}
		paths := []string{cert.Path}
		// certbot keeps each certificate in a folder of its own name.
		if dir := filepath.Dir(cert.Path); cert.Name != "" && filepath.Base(dir) == cert.Name {
			paths = append(paths, dir)
		}
		add(fc.Thing{Fact: f, Paths: paths})
	}

	// Git: every checkout under the git roots.
	for _, repo := range in.repos.Value {
		add(fc.Thing{Fact: gitFact(repo), Paths: []string{repo.Path}, Notable: true})
	}

	// Databases the inventory found on the host: data directories, files,
	// configuration. One in a container is said through its volume.
	for _, inst := range in.databases {
		href := "/databases"
		if len(inst.Connections) > 0 {
			href = "/databases/" + strconv.FormatInt(inst.Connections[0], 10)
		}
		product := inst.Engine
		if inst.Flavor != "" {
			product = inst.Flavor
		}
		base := fc.Fact{Source: fc.SourceDatabases, Names: []string{inst.Name}, Product: product,
			Detail: inst.Version, State: inst.State, Href: href}
		if inst.Self {
			continue
		}
		if h := inst.Host; h != nil {
			if filepath.IsAbs(h.DataDir) {
				f := base
				f.Kind, f.Inherits = fc.KindDatabaseData, true
				add(fc.Thing{Fact: f, Paths: []string{h.DataDir}, Notable: true})
			}
			if filepath.IsAbs(h.ConfigFile) {
				f := base
				f.Kind = fc.KindDatabaseConfig
				add(fc.Thing{Fact: f, Paths: []string{h.ConfigFile}})
			}
		}
		if file := inst.File; file != nil && filepath.IsAbs(file.Path) && inst.Container == nil {
			f := base
			f.Kind, f.Inherits = fc.KindDatabaseData, true
			add(fc.Thing{Fact: f, Paths: []string{file.Path}, Notable: true})
		}
	}

	// Processes: unit files, and where each PM2 app runs.
	if in.units.Value != nil {
		for _, u := range in.units.Value.Units {
			if !filepath.IsAbs(u.Fragment) {
				continue
			}
			add(fc.Thing{
				Fact: fc.Fact{Kind: fc.KindUnitFile, Source: fc.SourceProcesses, Names: []string{u.Name},
					Product: "systemd", State: u.ActiveState, Href: "/processes/services/" + url.PathEscape(u.Name)},
				Paths: []string{u.Fragment},
			})
		}
	}
	for _, app := range in.pm2.Value {
		href := mapPM2(app).Href
		if filepath.IsAbs(app.CWD) {
			add(fc.Thing{
				Fact: fc.Fact{Kind: fc.KindPM2, Source: fc.SourceProcesses, Names: []string{app.Name},
					Product: "pm2", State: app.Status, Href: href, Inherits: true},
				Paths: []string{app.CWD}, Notable: true,
			})
		}
		if filepath.IsAbs(app.ScriptPath) {
			add(fc.Thing{
				Fact:  fc.Fact{Kind: fc.KindPM2File, Source: fc.SourceProcesses, Names: []string{app.Name}, Product: "pm2", Detail: "script", Href: href},
				Paths: []string{app.ScriptPath},
			})
		}
		if logs := nonEmpty(app.OutLogPath, app.ErrLogPath); len(logs) > 0 {
			add(fc.Thing{
				Fact:  fc.Fact{Kind: fc.KindPM2File, Source: fc.SourceProcesses, Names: []string{app.Name}, Product: "pm2", Detail: "log", Href: href},
				Paths: logs,
			})
		}
	}

	// Backups: what each job covers, and where a local one keeps archives.
	var jobs []fc.Job
	for _, j := range in.backups.Value {
		if j == nil || j.Imported {
			// An imported job's sources are another server's paths.
			continue
		}
		f := fc.Fact{Kind: fc.KindBackup, Source: fc.SourceBackups, Names: []string{j.Name},
			Target: string(j.TargetKind), Schedule: j.Schedule, State: fc.BackupNever,
			Href: "/backups/" + strconv.FormatInt(j.ID, 10)}
		if j.LastSuccessAt != nil {
			at := *j.LastSuccessAt
			f.At, f.State = &at, fc.BackupOK
		}
		if j.LastRun != nil && j.LastRun.Status == backups.StatusFailed {
			f.State = fc.BackupFailed
		}
		if j.TargetKind == backups.TargetRclone && j.Target.RemoteType != "" {
			f.Detail = j.Target.RemoteType
		}
		jobs = append(jobs, fc.Job{Fact: f, Sources: j.Sources, Excludes: j.Excludes, Enabled: j.Enabled})
		if j.TargetKind == backups.TargetLocal && filepath.IsAbs(j.Target.Path) {
			target := f
			target.Kind, target.Inherits = fc.KindBackupTarget, true
			add(fc.Thing{Fact: target, Paths: []string{j.Target.Path}})
		}
	}
	return things, jobs
}

func gitFact(repo gitx.Repo) fc.Fact {
	changes := repo.Changes
	state := "clean"
	switch {
	case repo.Conflicts > 0:
		state = "conflicts"
	case repo.Changes > 0:
		state = "changed"
	}
	return fc.Fact{
		Kind: fc.KindGit, Source: fc.SourceGit, Names: []string{repo.Name}, Product: "git",
		Detail: repo.Branch, State: state, Count: &changes,
		Href: "/git?repo=" + url.QueryEscape(repo.Path), Inherits: true,
	}
}

// siteNames are the names a site answers to, without nginx's catch-all.
func siteNames(v proxysvc.VHost) []string {
	out := []string{}
	for _, n := range v.ServerNames {
		if n != "" && n != "_" && n != "localhost" {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		out = append(out, v.Name)
	}
	return out
}

func nonEmpty(paths ...string) []string {
	out := []string{}
	for _, p := range paths {
		if filepath.IsAbs(p) {
			out = append(out, p)
		}
	}
	return out
}

// resolveThings replaces each path with what it resolves to on the host, so
// a site root spelled through a symlink matches the folder it really is.
func resolveThings(things []fc.Thing, jobs []fc.Job, known map[string]string) {
	resolve := func(paths []string) []string {
		out := make([]string, 0, len(paths))
		for _, p := range paths {
			if real, ok := known[p]; ok {
				out = append(out, real)
			} else if filepath.IsAbs(p) {
				out = append(out, filepath.Clean(p))
			}
		}
		return out
	}
	for i := range things {
		things[i].Paths = resolve(things[i].Paths)
	}
	for i := range jobs {
		jobs[i].Sources = resolve(jobs[i].Sources)
		jobs[i].Excludes = resolve(jobs[i].Excludes)
	}
}

// freshenCheckouts replaces the held count of uncommitted changes with a
// fresh one for the checkout on screen (or the one it is inside) and for
// entries that are checkouts themselves, at most contextGitSummaries, each
// remembered for gitSummaryFresh. The repository list is held for minutes;
// "3 uncommitted" from five minutes ago is the wrong answer on the folder
// where the editing happens.
func (s *Server) freshenCheckouts(things []fc.Thing, folder string, children []fc.Child) {
	if s.modules.git == nil || !s.modules.git.Available() {
		return
	}
	wanted := map[string]bool{}
	nearest, nearestLen := -1, -1
	for i, t := range things {
		if t.Fact.Kind != fc.KindGit || len(t.Paths) == 0 {
			continue
		}
		if fc.Within(t.Paths[0], folder) && len(t.Paths[0]) > nearestLen {
			nearest, nearestLen = i, len(t.Paths[0])
		}
	}
	order := []int{}
	if nearest >= 0 {
		order = append(order, nearest)
		wanted[things[nearest].Paths[0]] = true
	}
	byPath := map[string]int{}
	for i, t := range things {
		if t.Fact.Kind == fc.KindGit && len(t.Paths) > 0 {
			byPath[t.Paths[0]] = i
		}
	}
	for _, c := range children {
		real := c.Real
		if real == "" {
			real = c.Path
		}
		if i, ok := byPath[real]; ok && !wanted[real] {
			wanted[real] = true
			order = append(order, i)
		}
	}
	if len(order) > contextGitSummaries {
		order = order[:contextGitSummaries]
	}
	var wg sync.WaitGroup
	for _, i := range order {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := things[i].Paths[0]
			read := readMapSource(&s.mapSources, mapSourceSpec{
				key: "git-summary:" + path, ttl: gitSummaryFresh, budget: contextBudget, limit: 10 * time.Second,
			}, func(ctx context.Context) (*gitx.Repo, error) {
				return s.modules.git.Summary(ctx, path)
			})
			if read.Have && read.Value != nil {
				fresh := gitFact(*read.Value)
				fresh.Names = things[i].Fact.Names
				fresh.Href = things[i].Fact.Href
				things[i].Fact = fresh
			}
		}(i)
	}
	// Each read waits at most contextBudget; one still running keeps going on
	// its own context and fills the next answer.
	wg.Wait()
}
