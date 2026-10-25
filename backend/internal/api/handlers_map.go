package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate"
)

// The server map: one read of everything the dashboard manages, for the page
// that draws the server in the middle and every kind of thing on it around it.
//
// `GET /map` reads each kind from the source its own page reads (through the
// held answers in map_sources.go, so an open map costs the server little) and
// answers with what the account may see: a kind its role cannot open is left
// out, and the API keys are the account's own unless it administers the
// server, as `GET /tokens` decides. Nothing on the map changes the server; every box
// links to the page that manages it, which keeps its own guards.
//
// `GET`/`PUT`/`DELETE /map/layout` keep how the account arranged its map. It
// is the account's own and needs a browser session: arranging your view of
// the server is open to every role, and is no business of an API key.
func (s *Server) mountMapRoutes(r chi.Router) {
	r.Route("/map", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleServerMap))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireSession)
			r.Method(http.MethodGet, "/layout", s.handle(s.handleMapLayoutGet))
			r.Method(http.MethodPut, "/layout", s.handle(s.handleMapLayoutPut))
			r.Method(http.MethodDelete, "/layout", s.handle(s.handleMapLayoutDelete))
		})
	})
}

// How long each kind's answer stays true, how long a map waits for a fresh
// one and how long a read may take. Things that change by the second
// (containers, shells) are read often; what changes when somebody edits it
// (sites, rules, accounts) less; what is costly to read and rarely changes
// (repositories, the systemd inventory, the Linux accounts) seldom.
var (
	mapFast    = mapSourceSpec{ttl: 10 * time.Second, budget: 2500 * time.Millisecond, limit: 20 * time.Second}
	mapSteady  = mapSourceSpec{ttl: 30 * time.Second, budget: 2500 * time.Millisecond, limit: 30 * time.Second}
	mapSlow    = mapSourceSpec{ttl: 2 * time.Minute, budget: 1200 * time.Millisecond, limit: 45 * time.Second}
	mapRare    = mapSourceSpec{ttl: 5 * time.Minute, budget: 1200 * time.Millisecond, limit: 60 * time.Second}
	mapHolding = mapSourceSpec{ttl: 3 * time.Second, budget: time.Second, limit: 5 * time.Second}
)

func spec(key string, base mapSourceSpec) mapSourceSpec {
	base.key = key
	return base
}

func (s *Server) handleServerMap(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	p := httpx.MustPrincipal(r)
	in := s.readServerMap(p)
	out := filterServerMap(buildServerMap(in), p)
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// readServerMap asks every source at once: each waits at most its own
// budget, so the map answers in about the slowest budget on a cold start and
// at once while the held answers are fresh. Sources this role may not read
// are not asked for it.
func (s *Server) readServerMap(p *httpx.Principal) mapInputs {
	in := mapInputs{now: time.Now(), absent: map[string]bool{}}
	in.hostname, _ = os.Hostname()
	c := &s.mapSources
	admin := p.Can(auth.CapSystemAdmin)
	var wg sync.WaitGroup
	run := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn()
		}()
	}

	m := &s.modules
	if m.docker == nil {
		in.absent["docker"] = true
	} else {
		run(func() {
			in.containers = readMapSource(c, spec("containers", mapFast), func(ctx context.Context) ([]dockerx.Container, error) {
				return m.docker.ListContainers(ctx, true)
			})
		})
		run(func() {
			in.images = readMapSource(c, spec("images", mapSteady), func(ctx context.Context) ([]dockerx.Image, error) {
				return m.docker.ListImages(ctx, false)
			})
		})
		run(func() {
			in.volumes = readMapSource(c, spec("volumes", mapSteady), m.docker.ListVolumes)
		})
		run(func() {
			in.networks = readMapSource(c, spec("networks", mapSteady), m.docker.ListNetworks)
		})
	}
	if m.deployRuns != nil {
		run(func() {
			in.projects = readMapSource(c, spec("projects", mapFast), func(ctx context.Context) ([]deploy.DeploymentSummary, error) {
				fleet, err := m.deployRuns.Fleet(ctx, deploy.QueueBudget{Heavy: s.Cfg.DeployHeavySlots, Light: s.Cfg.DeployLightSlots})
				if err != nil || fleet == nil {
					return nil, err
				}
				return fleet.Deployments, nil
			})
		})
	}
	run(func() {
		in.databases = readMapSource(c, spec("databases", mapSteady), s.mapDatabases)
	})
	run(func() {
		in.bindings = readMapSource(c, spec("bindings", mapSteady), s.topologyBindings)
	})
	run(func() {
		in.relations = readMapSource(c, spec("relations", mapSteady), s.mapRelations)
	})
	// The databases page's own reading of who uses each database: every
	// running container's environment searched for its addresses, and the
	// engine's session list. It inspects each container and dials each
	// server, so it is held the longest the lines can wait.
	run(func() {
		in.uses = readMapSource(c, spec("uses", mapSlow), s.mapUses)
	})
	// Who holds each port — the unit, the PM2 app, the container, this
	// dashboard — as the ports page reads it, for the lines from a site or a
	// stream to what it forwards to.
	run(func() {
		in.listeners = readMapSource(c, spec("listeners", mapSteady), s.mapListeners)
	})
	in.dataDir = s.Cfg.DataDir
	if m.proxy == nil {
		in.absent["proxy"] = true
	} else {
		run(func() {
			in.vhosts = readMapSource(c, spec("vhosts", mapSteady), func(ctx context.Context) ([]proxysvc.VHost, error) {
				hosts, err := m.proxy.ListVHosts(ctx)
				if err != nil {
					return nil, err
				}
				s.markDeploymentRoutes(ctx, hosts)
				return hosts, nil
			})
		})
		run(func() {
			in.certs = readMapSource(c, spec("certificates", mapSlow), m.proxy.CertificateInventory)
		})
		run(func() {
			in.streams = readMapSource(c, spec("streams", mapSlow), func(ctx context.Context) (mapStreams, error) {
				status, err := m.proxy.Streams(ctx)
				if err != nil || status == nil {
					return mapStreams{}, err
				}
				return mapStreams{Streams: status.Streams, Paused: status.Paused}, nil
			})
		})
	}
	if m.backupStore != nil {
		run(func() {
			in.backups = readMapSource(c, spec("backups", mapSteady), func(ctx context.Context) ([]*backups.Job, error) {
				jobs, err := m.backupStore.List(ctx)
				if err != nil {
					return nil, err
				}
				for _, j := range jobs {
					if m.backupSched != nil {
						j.NextRun = m.backupSched.NextRun(j.ID)
					}
				}
				return jobs, nil
			})
		})
	}
	if m.systemd == nil || !m.systemd.Available() {
		in.absent["systemd"] = true
	} else {
		run(func() {
			in.timers = readMapSource(c, spec("timers", mapSlow), m.systemd.Timers)
		})
		run(func() {
			in.units = readMapSource(c, spec("units", mapSlow), m.systemd.Inventory)
		})
	}
	if m.cron != nil {
		// The system's own cron files only: reading every account's crontab
		// runs `crontab -u` once per account, and the Scheduled page is where
		// that costly read belongs.
		run(func() {
			in.cron = readMapSource(c, spec("cron", mapSlow), m.cron.SystemCronFiles)
		})
	}
	if m.pm2 == nil || !m.pm2.Available() {
		in.absent["pm2"] = true
	} else {
		run(func() {
			in.pm2 = readMapSource(c, spec("pm2", mapSteady), m.pm2.List)
		})
	}
	if m.selfUpdate != nil {
		run(func() {
			in.dashboard = readMapSource(c, spec("dashboard", mapRare), func(ctx context.Context) (selfupdate.Report, error) {
				report := m.selfUpdate.Report(ctx, selfupdate.Cached)
				// The page draws one line of it; the release notes stay on the
				// Version page.
				report.Releases, report.History, report.Log = nil, nil, ""
				return report, nil
			})
		})
	}
	if m.deployAutomation != nil {
		run(func() {
			in.channels = readMapSource(c, spec("channels", mapSteady), m.deployAutomation.ListNotificationChannels)
		})
	}
	run(func() {
		in.boards = readMapSource(c, spec("boards", mapSteady), s.mapBoards)
	})
	run(func() {
		in.keys = readMapSource(c, spec("keys", mapSteady), func(ctx context.Context) ([]auth.APIToken, error) {
			list, err := s.Auth.ListAPITokens(ctx, 0, true)
			if err != nil {
				return nil, err
			}
			out := make([]auth.APIToken, 0, len(list))
			for _, k := range list {
				if k != nil {
					out = append(out, *k)
				}
			}
			return out, nil
		})
	})
	if p.Can(auth.CapTerminal) {
		if m.term == nil || !m.term.Enabled() {
			in.absent["terminal"] = true
		} else {
			in.terminals = readMapSource(c, spec("terminals", mapHolding), s.mapTerminals)
			if account, err := m.term.Account(); err == nil {
				in.terminalHome = account.Home
			}
		}
	}
	if m.git == nil || !m.git.Available() {
		in.absent["git"] = true
	} else {
		run(func() {
			in.repos = readMapSource(c, spec("repositories", mapRare), m.git.Discover)
		})
	}
	if admin {
		run(func() {
			in.alerts = readMapSource(c, spec("alerts", mapSteady), s.mapAlerts)
		})
		run(func() {
			in.accounts = readMapSource(c, spec("accounts", mapSteady), s.Auth.ListUsers)
		})
		run(func() {
			in.computers = readMapSource(c, spec("computers", mapSteady), s.mapComputers)
		})
		if m.linuxUsers != nil {
			run(func() {
				in.sysUsers = readMapSource(c, spec("systemUsers", mapRare), m.linuxUsers.List)
			})
		}
		if m.network == nil {
			in.absent["vpn"] = true
		} else {
			run(func() {
				in.vpn = readMapSource(c, spec("vpn", mapSlow), func(ctx context.Context) (netx.VPNSummary, error) {
					return m.network.VPNSummary(ctx), nil
				})
			})
		}
	}
	if m.netsec == nil {
		in.absent["firewall"] = true
	} else {
		run(func() {
			in.firewall = readMapSource(c, spec("firewall", mapSlow), m.netsec.Status)
		})
	}
	wg.Wait()
	if !admin {
		// Not asked for this role, so not on its map at all (filterServerMap
		// drops the kinds); marked absent so nothing reads as unread.
		for _, key := range []string{"alerts", "accounts", "computers", "systemUsers", "vpn"} {
			in.absent[key] = true
		}
	}
	if in.vpn.Have && !in.vpn.Value.WireGuard.Installed && !in.vpn.Value.Tailscale.Installed {
		in.absent["vpn"] = true
	}
	return in
}

// mapDatabases is the database control center's own fleet read — where each
// connection's server runs and whether it answers — shared with it through
// the dial cache, so a map and a control center open together dial once.
func (s *Server) mapDatabases(ctx context.Context) ([]fleetEntry, error) {
	if s.modules.docker != nil {
		ctx = s.modules.docker.WithReadSnapshot(ctx)
	}
	conns, err := s.allConnections(ctx)
	if err != nil {
		return nil, err
	}
	var jobDumps map[int64]time.Time
	if s.modules.backupStore != nil {
		jobDumps, _ = s.modules.backupStore.LastDatabaseDumps(ctx)
	}
	out := make([]fleetEntry, len(conns))
	view := s.newDBHostView(ctx)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for i, conn := range conns {
		wg.Add(1)
		go func(i int, conn *dbConnection) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			entry := fleetEntry{dbConnection: conn, Exposure: "remote"}
			s.fillFleetEntry(ctx, view, &entry, jobDumps)
			out[i] = entry
		}(i, conn)
	}
	wg.Wait()
	return out, nil
}

// mapRelations is what the deployments and the databases declare of each
// other in the store: the typed references among a deployment's current
// variables, the dependencies its plan names, and each connection's pooler.
func (s *Server) mapRelations(ctx context.Context) (mapRelations, error) {
	out := mapRelations{poolers: map[int64]string{}}
	rows, err := s.Store.DB.QueryContext(ctx, `
	  SELECT e.project_id, v.value_enc
	    FROM deploy_variable_revisions v
	    JOIN deploy_environments e ON e.id = v.environment_id
	    JOIN deploy_projects p ON p.id = e.project_id
	   WHERE v.active = 1 AND v.value_mode IN ('reference', '')
	     AND e.archived_at = 0 AND p.archived_at = 0
	   ORDER BY e.project_id, v.key`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var project int64
		var sealed string
		if err := rows.Scan(&project, &sealed); err != nil {
			rows.Close()
			return out, err
		}
		// Only the reference itself is kept: a variable written before modes
		// were recorded may be a literal, and its value is dropped here.
		value, err := s.Sealer.Open(sealed)
		if err != nil {
			continue
		}
		ref, err := deploy.ParseVariableReference(strings.TrimSpace(value))
		if err != nil || (ref.Kind != "database" && ref.Kind != "domain") {
			continue
		}
		out.refs = append(out.refs, mapProjectRef{projectID: project, kind: ref.Kind, target: ref.Target, via: "reference"})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	rows, err = s.Store.DB.QueryContext(ctx, `
	  SELECT e.project_id, d.kind, d.resource_kind, d.resource_id
	    FROM deploy_dependencies d
	    JOIN deploy_environments e ON e.id = d.environment_id
	    JOIN deploy_projects p ON p.id = e.project_id
	   WHERE d.release_id = 0 AND e.archived_at = 0 AND p.archived_at = 0
	   ORDER BY d.id`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var project int64
		var kind, resourceKind, resource string
		if err := rows.Scan(&project, &kind, &resourceKind, &resource); err != nil {
			rows.Close()
			return out, err
		}
		if kind == "storage" && resourceKind != "docker_volume" {
			// A bind path is a folder, which the map draws no box for.
			continue
		}
		out.refs = append(out.refs, mapProjectRef{projectID: project, kind: kind, target: resource, via: "dependency"})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	rows, err = s.Store.DB.QueryContext(ctx, `SELECT connection_id, container_name FROM db_poolers`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var conn int64
		var container string
		if err := rows.Scan(&conn, &container); err != nil {
			return out, err
		}
		out.poolers[conn] = container
	}
	return out, rows.Err()
}

// mapUses is the databases topology (`GET /databases/topology`) cut to the
// lines the map draws: a deployment or a container whose environment names a
// database, or that the engine reports connected to it. Its bindings are the
// bindings source's, and a stack or network shared with the server is the
// map's own member and network lines.
func (s *Server) mapUses(ctx context.Context) ([]mapUse, error) {
	if s.modules.docker != nil {
		ctx = s.modules.docker.WithReadSnapshot(ctx)
	}
	conns, err := s.existingConnections(ctx)
	if err != nil {
		return nil, err
	}
	topo, err := s.topology(ctx, conns)
	if err != nil {
		return nil, err
	}
	nodes := map[string]topoNode{}
	for _, n := range topo.Nodes {
		nodes[n.ID] = n
	}
	out := []mapUse{}
	for _, e := range topo.Edges {
		conn, err := strconv.ParseInt(strings.TrimPrefix(e.From, "db:"), 10, 64)
		if err != nil {
			continue
		}
		n := nodes[e.To]
		for _, via := range e.Via {
			if via != "env" && via != "session" {
				continue
			}
			use := mapUse{connID: conn, via: via}
			switch n.Kind {
			case "deployment":
				use.projectID = n.ProjectID
			case "container":
				use.container = n.Name
			default:
				continue
			}
			out = append(out, use)
		}
	}
	return out, nil
}

// mapListeners is every listening socket with who it answers for, read as
// the ports page reads it.
func (s *Server) mapListeners(ctx context.Context) ([]proxysvc.Listener, error) {
	owners := make(chan proxysvc.OwnerInput, 1)
	go func() {
		if s.modules.docker == nil {
			owners <- proxysvc.OwnerInput{SelfPID: int32(os.Getpid()), DataDir: s.Cfg.DataDir}
			return
		}
		// Socket units only where systemd is managed here: they name the
		// service a socket-activated port starts.
		owners <- s.ownerInput(ctx, s.modules.systemd != nil)
	}()
	listeners, err := proxysvc.ListListeners(ctx)
	in := <-owners
	if err != nil {
		return nil, err
	}
	if s.modules.pm2 != nil {
		in.PM2 = s.pm2Apps(ctx, listeners)
	}
	return proxysvc.AttributeOwners(listeners, in), nil
}

func (s *Server) mapBoards(ctx context.Context) ([]mapBoard, error) {
	rows, err := s.Store.DB.QueryContext(ctx,
		`SELECT id, name, updated_at FROM boards ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []mapBoard{}
	for rows.Next() {
		var b mapBoard
		var updated int64
		if err := rows.Scan(&b.ID, &b.Name, &updated); err != nil {
			return nil, err
		}
		b.UpdatedAt = time.Unix(updated, 0)
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Server) mapComputers(ctx context.Context) ([]mapComputer, error) {
	rows, err := s.Store.DB.QueryContext(ctx,
		`SELECT id, name, os, host, protocol, last_session_at FROM remote_desktop_computers ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []mapComputer{}
	for rows.Next() {
		var c mapComputer
		var last int64
		if err := rows.Scan(&c.ID, &c.Name, &c.OS, &c.Host, &c.Protocol, &last); err != nil {
			return nil, err
		}
		if last > 0 {
			c.LastSessionAt = time.Unix(last, 0)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// mapTerminals folds the open PTYs into the workspaces the terminal page's
// rail shows, as handleTerminalList does, without walking /proc for each
// one's folder.
func (s *Server) mapTerminals(ctx context.Context) ([]mapTerminal, error) {
	byID := map[string]*mapTerminal{}
	out := []*mapTerminal{}
	for _, sess := range s.modules.term.List() {
		if sess.TmuxName != "" {
			continue
		}
		id := sess.WorkspaceID
		if id == "" {
			id = sess.ID
		}
		t := byID[id]
		if t == nil {
			t = &mapTerminal{ID: id, Title: sess.Meta().Title, User: sess.User}
			byID[id] = t
			out = append(out, t)
		}
		t.Windows++
		activity := sess.Activity()
		t.Working = t.Working || activity.Working
		if activity.Busy {
			t.Busy = true
			if t.Process == "" {
				t.Process = activity.Process
			}
		}
	}
	list := make([]mapTerminal, 0, len(out))
	for _, t := range out {
		list = append(list, *t)
	}
	return list, nil
}

func (s *Server) mapAlerts(ctx context.Context) (mapAlerts, error) {
	out := mapAlerts{HostFiring: map[int64]int{}, ProxyFiring: map[int64]int{}}
	if a := s.modules.hostAlerts; a != nil {
		rules, err := a.Rules(ctx, false)
		if err != nil {
			return out, err
		}
		out.Host = rules
		if subjects, err := a.Subjects(ctx); err == nil {
			for _, sub := range subjects {
				if sub.State == "firing" {
					out.HostFiring[sub.RuleID]++
				}
			}
		}
	}
	if a := s.modules.proxyExtras.alerts; a != nil {
		rules, err := a.Rules(ctx, false)
		if err != nil {
			return out, err
		}
		out.Proxy = rules
		if subjects, err := a.Subjects(ctx); err == nil {
			for _, sub := range subjects {
				if sub.State == "firing" {
					out.ProxyFiring[sub.RuleID]++
				}
			}
		}
	}
	return out, nil
}

// filterServerMap holds the map to what the principal's own pages would show
// it: a kind behind a capability it lacks is not sent, and the API keys are
// its own unless it administers the server — the same line `GET /tokens`
// draws. Lines to a thing left out go with it.
func filterServerMap(m mapResponse, p *httpx.Principal) mapResponse {
	admin := p.Can(auth.CapSystemAdmin)
	need := map[string]auth.Capability{
		"terminals":   auth.CapTerminal,
		"computers":   auth.CapSystemAdmin,
		"accounts":    auth.CapSystemAdmin,
		"systemUsers": auth.CapSystemAdmin,
		"alerts":      auth.CapSystemAdmin,
		"tunnels":     auth.CapSystemAdmin,
	}
	kinds := make([]mapKind, 0, len(m.Kinds))
	for _, k := range m.Kinds {
		if c, ok := need[k.Key]; ok && !p.Can(c) {
			continue
		}
		if k.Key == "keys" && !admin {
			own := []mapItem{}
			for _, item := range k.Items {
				if item.owner == p.UserID() {
					own = append(own, item)
				}
			}
			k.Items, k.Total = own, len(own)
			if k.State == "read" {
				k.State, k.Reason = "limited", "Only your own keys are listed"
			}
		}
		kinds = append(kinds, k)
	}
	m.Kinds = kinds
	m.Links = linksBetween(m.Links, kinds)
	return m
}

// maxMapLayoutBytes caps an arrangement. A thousand boxes placed by hand is
// about 60 KiB; the cap leaves room for that several times over.
const maxMapLayoutBytes = 512 << 10

type mapLayoutRequest struct {
	Layout json.RawMessage `json:"layout"`
}

type mapLayoutResponse struct {
	Layout    json.RawMessage `json:"layout"`
	UpdatedAt *time.Time      `json:"updatedAt,omitempty"`
}

func (s *Server) handleMapLayoutGet(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	p := httpx.MustPrincipal(r)
	var (
		raw     string
		updated int64
	)
	err := s.Store.DB.QueryRowContext(r.Context(),
		`SELECT layout, updated_at FROM map_layouts WHERE user_id = ?`, p.UserID()).Scan(&raw, &updated)
	if err == sql.ErrNoRows {
		httpx.JSON(w, http.StatusOK, mapLayoutResponse{Layout: json.RawMessage("null")})
		return nil
	}
	if err != nil {
		return httpx.Internal(err)
	}
	at := time.Unix(updated, 0).UTC()
	httpx.JSON(w, http.StatusOK, mapLayoutResponse{Layout: json.RawMessage(raw), UpdatedAt: &at})
	return nil
}

func (s *Server) handleMapLayoutPut(w http.ResponseWriter, r *http.Request) error {
	p := httpx.MustPrincipal(r)
	var req mapLayoutRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	layout := bytes.TrimSpace(req.Layout)
	// A saved `null` or `[]` would come back as a document the page cannot
	// read; the decoder proved the body is JSON, not the shape of this field.
	if len(layout) == 0 || layout[0] != '{' {
		return httpx.BadRequest("layout must be a JSON object")
	}
	if len(layout) > maxMapLayoutBytes {
		return httpx.BadRequest("layout is larger than %d KiB", maxMapLayoutBytes>>10)
	}
	now := time.Now().Unix()
	if _, err := s.Store.DB.ExecContext(r.Context(),
		`INSERT INTO map_layouts(user_id, layout, updated_at) VALUES(?,?,?)
		 ON CONFLICT(user_id) DO UPDATE SET layout = excluded.layout, updated_at = excluded.updated_at`,
		p.UserID(), string(layout), now); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "map.layout.save", "server map", nil)
	at := time.Unix(now, 0).UTC()
	httpx.JSON(w, http.StatusOK, mapLayoutResponse{Layout: layout, UpdatedAt: &at})
	return nil
}

func (s *Server) handleMapLayoutDelete(w http.ResponseWriter, r *http.Request) error {
	p := httpx.MustPrincipal(r)
	if _, err := s.Store.DB.ExecContext(r.Context(),
		`DELETE FROM map_layouts WHERE user_id = ?`, p.UserID()); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "map.layout.reset", "server map", nil)
	httpx.NoContent(w)
	return nil
}
