package api

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
	"gopkg.in/yaml.v3"
)

// Reading the machine for the database inventory.
//
// dbx.Discover is a pure function; this file is what feeds it. Each collector
// reads one thing — Docker's containers, the compose files on disk, the
// kernel's socket tables, systemd — and reports how that went on its own line.
// That is the difference between an inventory and a request that either works
// or does not: a server with no Docker daemon still runs databases, and
// answering 502 to the whole question because one of its halves was missing
// is how a native Postgres used to become invisible. A collector that could
// not read is a stated silence with its reason, and never an empty list that
// reads as "there is nothing here".

// inventoryScan is how one collector fared.
type inventoryScan struct {
	// Source is docker, compose, listeners, sockets, units or files.
	Source string `json:"source"`
	OK     bool   `json:"ok"`
	// Reason says why it could not read, or what it left out.
	Reason string `json:"reason,omitempty"`
	// Truncated says the collector stopped before the end of what there was.
	Truncated bool `json:"truncated,omitempty"`
	// Running marks a file scan that is under way: what is listed is from the
	// one before it.
	Running    bool      `json:"running,omitempty"`
	Count      int       `json:"count"`
	DurationMs int64     `json:"durationMs"`
	CheckedAt  time.Time `json:"checkedAt"`
}

// dbInventoryHost is what the inventory reads off the machine itself, beside
// what Docker tells it. A test hands in a machine of its own.
type dbInventoryHost struct {
	listeners func(context.Context) ([]proxysvc.Listener, error)
	sockets   func(context.Context, func(string) bool) ([]proxysvc.UnixListener, error)
	systemd   func() bool
	// units is the units systemd has loaded and, beside them, every installed
	// unit file and its state: the units a listing leaves out because they
	// are stopped and disabled.
	units func(context.Context) ([]procs.Unit, map[string]string, error)
	// etc is where the host's configuration is read from, for a Debian
	// PostgreSQL cluster's port.
	etc string
	// volumes is where Docker's local driver keeps volumes, and roots are the
	// directories applications are installed under; both are walked for
	// database files. hostRoot is where a containerised dashboard has the
	// host's whole filesystem mounted, for the roots it has under no other
	// name.
	volumes  string
	roots    []string
	hostRoot string
}

var hostInventory = dbInventoryHost{
	listeners: proxysvc.ListListeners,
	sockets:   proxysvc.ListUnixListeners,
	systemd:   hostSystemd.Available,
	units:     hostSystemd.ListInstalled,
	etc:       "/etc",
	volumes:   "/var/lib/docker/volumes",
	roots:     []string{"/home", "/srv", "/opt", "/var/www", "/var/lib", "/root"},
	hostRoot:  "/host",
}

// inventoryHost is the machine the inventory reads: the real one, unless a
// test replaced it.
func (s *Server) inventoryHost() dbInventoryHost {
	if s.dbInventory.host != nil {
		return *s.dbInventory.host
	}
	return hostInventory
}

// maxInventoryInspects bounds how many containers are inspected for one
// inventory. An inspect is a local round trip of a millisecond or two, so the
// bound is for the host with a thousand containers, not the ordinary one.
const maxInventoryInspects = 400

// collectContainers reads Docker's containers, stopped ones included.
//
// With detail, every container is inspected: its environment and command are
// the rungs that recognise a database whose image name says nothing, and its
// network mode and addresses decide where it is dialled. Without it, only what
// the listing states is read — no environment — which is the list a role that
// may not read container environment is shown.
func (s *Server) collectContainers(ctx context.Context, detail bool) ([]dbx.ContainerFacts, []dockerx.Container, inventoryScan) {
	started := time.Now()
	scan := inventoryScan{Source: "docker", CheckedAt: started.UTC()}
	if s.modules.docker == nil {
		scan.Reason = "this host has no Docker socket"
		return nil, nil, scan
	}
	list, err := s.modules.docker.ListContainers(ctx, true)
	if err != nil {
		scan.Reason = "Docker did not answer: " + err.Error()
		scan.DurationMs = time.Since(started).Milliseconds()
		return nil, nil, scan
	}
	facts := make([]dbx.ContainerFacts, 0, len(list))
	inspected := 0
	for _, c := range list {
		f := dbx.ContainerFacts{
			ID: c.ID, Name: c.Name, Image: s.containerImage(ctx, c), State: c.State, Status: c.Status,
			Health: c.Health, Labels: c.Labels, Ports: publishedPorts(c.Ports), Command: c.Command,
		}
		for _, m := range c.Mounts {
			f.Mounts = append(f.Mounts, dbx.ContainerMount{Type: m.Type, Name: m.Name, Source: m.Source, Destination: m.Destination})
		}
		if detail {
			if inspected >= maxInventoryInspects {
				scan.Truncated = true
			} else if d, err := s.modules.docker.Inspect(ctx, c.ID); err == nil {
				inspected++
				f.Inspected = true
				f.Env = envMap(d.Env)
				f.Argv = containerArgv(d)
				f.NetworkMode = d.NetworkMode
				f.IPs = containerIPs(d)
				if len(d.Ports) > 0 {
					// The inspect names an exposed port that has no host
					// binding, which the listing leaves out for some states.
					f.Ports = publishedPorts(d.Ports)
				}
			}
		}
		facts = append(facts, f)
	}
	scan.OK = true
	scan.Count = len(facts)
	if scan.Truncated {
		scan.Reason = fmt.Sprintf("only the first %d containers were inspected", maxInventoryInspects)
	}
	scan.DurationMs = time.Since(started).Milliseconds()
	return facts, list, scan
}

// containerArgv is the program a container runs and its arguments. Docker
// reports the arguments after the program; the program itself is the first
// word of the entrypoint, or of the command where there is no entrypoint.
func containerArgv(d *dockerx.ContainerDetail) []string {
	argv := []string{}
	switch {
	case len(d.Entrypoint) > 0:
		argv = append(argv, d.Entrypoint[0])
	case strings.TrimSpace(d.Command) != "":
		argv = append(argv, strings.Fields(d.Command)[0])
	}
	return append(argv, d.Args...)
}

// maxComposeFileBytes bounds a compose file read for its image names.
const maxComposeFileBytes = 1 << 20

// collectDeclared reads the services compose files declare that have no
// container, with the image each names. Only the image is read: a file's
// environment is not the environment of a container that does not exist.
func (s *Server) collectDeclared(ctx context.Context) ([]dbx.DeclaredService, []dockerx.ComposeStack, inventoryScan) {
	started := time.Now()
	scan := inventoryScan{Source: "compose", CheckedAt: started.UTC()}
	if s.modules.docker == nil {
		scan.Reason = "this host has no Docker socket"
		return nil, nil, scan
	}
	stacks, err := s.modules.docker.ListStacks(ctx, s.Cfg.ComposeRoots)
	if err != nil {
		scan.Reason = "Docker did not answer: " + err.Error()
		scan.DurationMs = time.Since(started).Milliseconds()
		return nil, nil, scan
	}
	// A compose file found on disk is listed under its directory's name, and
	// the project its containers run under may be called something else. The
	// directory is then a project that is deployed, not a second one whose
	// every service is missing.
	deployed := map[string]bool{}
	for _, stack := range stacks {
		if stack.Containers > 0 && stack.WorkingDir != "" {
			deployed[filepath.Clean(stack.WorkingDir)] = true
		}
	}
	out := []dbx.DeclaredService{}
	for _, stack := range stacks {
		missing := false
		for _, svc := range stack.Services {
			missing = missing || svc.Missing
		}
		if !missing || (stack.Containers == 0 && deployed[filepath.Clean(stack.WorkingDir)]) {
			continue
		}
		images := s.composeImages(stack.ConfigFiles)
		for _, svc := range stack.Services {
			if image := images[svc.Name]; svc.Missing && image != "" {
				out = append(out, dbx.DeclaredService{Project: stack.Name, Service: svc.Name, Image: image})
			}
		}
	}
	scan.OK = true
	scan.Count = len(out)
	scan.DurationMs = time.Since(started).Milliseconds()
	return out, stacks, scan
}

// composeImages reads the image each service of a compose file names.
//
// The file's path comes from a container's labels or from a walk of the
// compose roots, and is put through the same containment as a path a client
// supplied: a label is whatever the container's creator wrote.
func (s *Server) composeImages(paths []string) map[string]string {
	out := map[string]string{}
	if s.modules.files == nil {
		return out
	}
	for _, path := range paths {
		resolved, err := s.modules.files.Resolve(path)
		if err != nil {
			continue
		}
		raw, err := readBounded(resolved, maxComposeFileBytes)
		if err != nil {
			continue
		}
		var doc struct {
			Services map[string]struct {
				Image string `yaml:"image"`
			} `yaml:"services"`
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			continue
		}
		for name, svc := range doc.Services {
			image := strings.TrimSpace(svc.Image)
			// "postgres:${PG_TAG:-16}" names the image; only its tag is a
			// variable. One whose name is itself a variable says nothing.
			if i := strings.Index(image, ":${"); i > 0 {
				image = image[:i]
			}
			if image == "" || strings.Contains(image, "$") {
				continue
			}
			if _, seen := out[name]; !seen {
				out[name] = image
			}
		}
	}
	return out
}

func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}

// collectListeners reads the host's listening TCP sockets, each joined to the
// process that holds it and to whatever supervises that process.
func (s *Server) collectListeners(ctx context.Context) ([]dbx.HostListener, inventoryScan) {
	started := time.Now()
	scan := inventoryScan{Source: "listeners", CheckedAt: started.UTC()}
	listeners, err := s.inventoryHost().listeners(ctx)
	scan.DurationMs = time.Since(started).Milliseconds()
	if err != nil {
		scan.Reason = "this machine's listening sockets could not be read: " + err.Error()
		return nil, scan
	}
	out := make([]dbx.HostListener, 0, len(listeners))
	for _, l := range listeners {
		out = append(out, dbx.HostListener{
			Protocol: l.Protocol, Address: l.Address, Port: int(l.Port), Process: l.Process, User: l.User,
			PID: l.PID, Cmdline: l.Cmdline, Manager: l.Manager, ManagerName: l.ManagerName,
		})
	}
	scan.OK, scan.Count = true, len(out)
	return out, scan
}

// collectSockets reads the listening unix sockets named the way a database
// names its own.
func (s *Server) collectSockets(ctx context.Context) ([]dbx.UnixSocket, inventoryScan) {
	started := time.Now()
	scan := inventoryScan{Source: "sockets", CheckedAt: started.UTC()}
	sockets, err := s.inventoryHost().sockets(ctx, func(path string) bool {
		_, _, ok := dbx.SocketEngine(path)
		return ok
	})
	scan.DurationMs = time.Since(started).Milliseconds()
	if err != nil {
		scan.Reason = "this machine's unix sockets could not be read: " + err.Error()
		return nil, scan
	}
	out := make([]dbx.UnixSocket, 0, len(sockets))
	for _, u := range sockets {
		out = append(out, dbx.UnixSocket{
			Path: u.Path, PID: u.PID, Process: u.Process, Cmdline: u.Cmdline, User: u.User,
			Manager: u.Manager, ManagerName: u.ManagerName,
		})
	}
	scan.OK, scan.Count = true, len(out)
	return out, scan
}

// collectUnits reads systemd's service units, stopped and failed ones
// included: an installed server that is not running has no socket to be found
// by, and its unit is the only thing that says it is there.
func (s *Server) collectUnits(ctx context.Context) ([]dbx.HostUnit, inventoryScan) {
	started := time.Now()
	scan := inventoryScan{Source: "units", CheckedAt: started.UTC()}
	host := s.inventoryHost()
	if host.systemd == nil || !host.systemd() {
		scan.Reason = "this machine has no systemd to ask"
		return nil, scan
	}
	units, installed, err := host.units(ctx)
	scan.DurationMs = time.Since(started).Milliseconds()
	if err != nil {
		scan.Reason = "systemd's units could not be read: " + err.Error()
		return nil, scan
	}
	out := make([]dbx.HostUnit, 0, len(units))
	for _, u := range units {
		out = append(out, dbx.HostUnit{
			Name: u.Name, LoadState: u.LoadState, ActiveState: u.ActiveState, SubState: u.SubState, Enabled: u.Enabled,
		})
	}
	// The listing is the units systemd has loaded, and one that is stopped and
	// disabled is not loaded. The unit files say what is installed.
	if len(installed) > 0 {
		out = append(out, dbx.InstalledUnits(installed, out, debianClusters(host.etc))...)
	} else {
		scan.Reason = "the installed unit files could not be read, so a server that is stopped and disabled may be missing"
	}
	for i := range out {
		if version, cluster, ok := dbx.DebianCluster(out[i].Name); ok {
			config := filepath.Join(host.etc, "postgresql", version, cluster, "postgresql.conf")
			if port, dataDir := postgresClusterConfig(config); port > 0 {
				out[i].ConfigFile, out[i].Port, out[i].DataDir = config, port, dataDir
			}
		}
	}
	scan.OK, scan.Count = true, len(out)
	scan.DurationMs = time.Since(started).Milliseconds()
	return out, scan
}

// maxDebianClusters bounds how many directories are read looking for them.
const maxDebianClusters = 64

// debianClusters lists the PostgreSQL clusters Debian's packaging has
// configured, as "<version>-<name>": every /etc/postgresql/<version>/<name>
// that holds a postgresql.conf. A cluster is an instance of a template unit,
// so no unit file names it, and one that is stopped is in no unit listing
// either.
func debianClusters(etc string) []string {
	root := filepath.Join(etc, "postgresql")
	versions, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, version := range versions {
		if !version.IsDir() {
			continue
		}
		names, err := os.ReadDir(filepath.Join(root, version.Name()))
		if err != nil {
			continue
		}
		for _, name := range names {
			if len(out) >= maxDebianClusters {
				return out
			}
			if !name.IsDir() {
				continue
			}
			if _, err := os.Stat(filepath.Join(root, version.Name(), name.Name(), "postgresql.conf")); err == nil {
				out = append(out, version.Name()+"-"+name.Name())
			}
		}
	}
	return out
}

// postgresClusterConfig reads where a Debian PostgreSQL cluster listens and
// keeps its data out of its postgresql.conf. The path is built from a unit
// name whose two segments dbx.DebianCluster has already checked, and nothing
// is run to read it.
func postgresClusterConfig(path string) (port int, dataDir string) {
	raw, err := readBounded(path, 1<<20)
	if err != nil {
		return 0, ""
	}
	port = 5432
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `'"`)
		switch strings.TrimSpace(name) {
		case "port":
			if n, err := strconv.Atoi(value); err == nil && n > 0 && n <= 65535 {
				port = n
			}
		case "data_directory":
			dataDir = value
		}
	}
	return port, dataDir
}

// inventoryPlaces says whose each part of the filesystem is: which directories
// are mounted into which containers, and where deployments and compose stacks
// live.
func (s *Server) inventoryPlaces(ctx context.Context, containers []dockerx.Container, stacks []dockerx.ComposeStack) dbx.Places {
	places := dbx.Places{
		StorePath:  filepath.Join(filepath.Clean(s.Cfg.DataDir), store.DatabaseFile),
		DataDir:    filepath.Clean(s.Cfg.DataDir),
		VolumesDir: s.inventoryHost().volumes,
	}
	mounts := map[string]*dbx.MountPlace{}
	for _, c := range containers {
		for _, m := range c.Mounts {
			// A mount of a whole root — the dashboard's own view of /home —
			// does not make everything under it that container's.
			if m.Source == "" || (m.Type != "volume" && !(m.Type == "bind" && ownedBindSource(m.Source))) {
				continue
			}
			place := mounts[m.Source]
			if place == nil {
				place = &dbx.MountPlace{Source: m.Source}
				if m.Type == "volume" {
					place.Volume = m.Name
				}
				mounts[m.Source] = place
			}
			place.Containers = append(place.Containers, c.Name)
		}
	}
	sources := make([]string, 0, len(mounts))
	for source := range mounts {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	for _, source := range sources {
		sort.Strings(mounts[source].Containers)
		places.Mounts = append(places.Mounts, *mounts[source])
	}
	for _, stack := range stacks {
		if stack.WorkingDir != "" {
			places.Stacks = append(places.Stacks, dbx.ProjectPlace{Name: stack.Name, Path: stack.WorkingDir})
		}
	}
	places.Deployments = s.deploymentPlaces(ctx)
	return places
}

// deploymentPlaces is where each live deployment project's source lives.
func (s *Server) deploymentPlaces(ctx context.Context) []dbx.ProjectPlace {
	out := []dbx.ProjectPlace{}
	rows, err := s.Store.DB.QueryContext(ctx,
		`SELECT name, repo_path FROM deploy_projects WHERE archived_at = 0 AND repo_path <> '' ORDER BY name`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var place dbx.ProjectPlace
		if err := rows.Scan(&place.Name, &place.Path); err != nil {
			return out
		}
		out = append(out, place)
	}
	return out
}
