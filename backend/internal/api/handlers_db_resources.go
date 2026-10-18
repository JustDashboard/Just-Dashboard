package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// What a database's container may use of this machine, and changing it.
//
// Docker holds the limits in the container's own configuration, and that is
// the only place they are kept: every way this dashboard replaces a container
// (a change of exposure, the Docker page's Recreate) reads the configuration
// back through SpecOf, so a limit set here is the limit the replacement gets.
// A copy in the dashboard's store would be a second answer, and the first
// time somebody changed the limit on the Docker page the two would disagree.
//
// Docker applies a new or changed limit to a container in place, and cannot
// take one away: zero means "unchanged" to `docker update`. Lifting a limit
// is therefore a replacement of the container with the same configuration
// and no limit, which drops every session while it restarts. That one case
// is the destructive kind of this request, decided from what the container
// holds now, with the capability and budget asked for by hand as the power
// route does.

const mib = 1 << 20

// dockerMinMemoryMB is the smallest memory limit Docker accepts.
const dockerMinMemoryMB = 6

// dbResourceLimits is what a container may use: memory in bytes and
// processors in cores, zero where there is no limit, in the units the
// connection summary reports them.
type dbResourceLimits struct {
	MemoryBytes int64   `json:"memoryBytes"`
	CPUs        float64 `json:"cpus"`
}

// dbHostCapacity is this machine as Docker sees it, which is what a limit is
// validated against: Docker itself refuses more processors than it has.
type dbHostCapacity struct {
	MemoryBytes int64 `json:"memoryBytes"`
	CPUs        int   `json:"cpus"`
}

// dbResources is the Resources page's reading.
type dbResources struct {
	// Container is the container the server runs in, where it runs in one
	// on this machine.
	Container string `json:"container,omitempty"`
	// Editable reports that this route can change the limits, and Refusal
	// why it cannot, in a sentence for the page.
	Editable bool   `json:"editable"`
	Refusal  string `json:"refusal,omitempty"`
	// Running reports a container that is running now: what it uses and the
	// engine's own buffer are read only then.
	Running bool             `json:"running"`
	Limits  dbResourceLimits `json:"limits"`
	// UsedBytes is the memory it uses now, as `docker stats` counts it.
	UsedBytes int64 `json:"usedBytes,omitempty"`
	// OOMKilled reports that the kernel stopped it, the last time it
	// stopped, for going past its memory limit.
	OOMKilled bool           `json:"oomKilled,omitempty"`
	Host      dbHostCapacity `json:"host"`
	// Buffer is the memory the engine keeps for itself, by its own setting,
	// and BufferError why it could not be read from a server that runs.
	Buffer      *dbx.MemoryBuffer `json:"buffer,omitempty"`
	BufferError string            `json:"bufferError,omitempty"`
}

// dbResourcesRequest is a change of limits. A field left out keeps its
// limit; zero lifts it.
type dbResourcesRequest struct {
	MemoryMB *int64   `json:"memoryMb"`
	CPUs     *float64 `json:"cpus"`
}

func (s *Server) hostCapacity(ctx context.Context) dbHostCapacity {
	if s.modules.docker == nil {
		return dbHostCapacity{}
	}
	memory, cpus := s.modules.docker.HostCapacity(ctx)
	return dbHostCapacity{MemoryBytes: memory, CPUs: cpus}
}

// dbLimitsProblem is what is wrong with a limit asked for, as a sentence, or
// empty. Zero is no limit; a capacity Docker could not report is not held
// against, since Docker still refuses more processors than it has.
func dbLimitsProblem(memoryMB int64, cpus float64, host dbHostCapacity) string {
	switch {
	case memoryMB < 0 || cpus < 0:
		return "a limit cannot be negative; zero is no limit"
	case memoryMB > 0 && memoryMB < dockerMinMemoryMB:
		return fmt.Sprintf("a memory limit is at least %d MiB, the smallest Docker accepts", dockerMinMemoryMB)
	case memoryMB > math.MaxInt64/(2*mib),
		host.MemoryBytes > 0 && memoryMB > host.MemoryBytes/mib:
		return fmt.Sprintf("this server has %d MiB of memory, and a limit above it limits nothing", host.MemoryBytes/mib)
	case cpus > 0 && cpus < 0.01:
		return "a processor limit is at least 0.01 of a core"
	case host.CPUs > 0 && cpus > float64(host.CPUs):
		return fmt.Sprintf("this server has %d processors, and a limit is at most that many", host.CPUs)
	}
	return ""
}

// nanoCPUs is a processor limit as Docker holds it, converted the way
// dockerx converts one, so a limit compared with the one it set is equal.
func nanoCPUs(cpus float64) int64 { return int64(cpus * 1e9) }

// dbLimitsPlan is what a change of limits does to a container.
type dbLimitsPlan struct {
	after dbResourceLimits
	// update is what `docker update` is asked to change; a zero field is
	// left as it is.
	update dockerx.ResourceLimits
	// lifts names the limits taken away, which only a new container can do.
	lifts   []string
	changed bool
}

func planDBLimits(current dbResourceLimits, req dbResourcesRequest) dbLimitsPlan {
	plan := dbLimitsPlan{after: current}
	if req.MemoryMB != nil && *req.MemoryMB*mib != current.MemoryBytes {
		plan.after.MemoryBytes, plan.changed = *req.MemoryMB*mib, true
		if *req.MemoryMB == 0 {
			plan.lifts = append(plan.lifts, "memory")
		} else {
			plan.update.MemoryMB = *req.MemoryMB
		}
	}
	if req.CPUs != nil && nanoCPUs(*req.CPUs) != nanoCPUs(current.CPUs) {
		plan.after.CPUs, plan.changed = *req.CPUs, true
		if *req.CPUs == 0 {
			plan.lifts = append(plan.lifts, "cpus")
		} else {
			plan.update.CPUs = *req.CPUs
		}
	}
	return plan
}

// resourcesRefusal is why a server's limits are not this route's to change,
// or empty when they are: a container on this machine that is the engine's
// own and that compose does not own.
func resourcesRefusal(conn *dbConnection, place dbPlacement) string {
	c := place.Container
	switch {
	case c != nil && place.foreign:
		return fmt.Sprintf("The connection is answered by the container %s, whose image (%s) is not one of this engine's, so what it would limit is not known to be the database. Set its limits on the Docker page.",
			c.Name, c.Image)
	case c != nil && c.ComposeProject != "":
		return fmt.Sprintf("%s belongs to the compose project %s, which puts back what its file says the next time it recreates the container. Set the limits in the compose file and redeploy the stack.",
			c.Name, c.ComposeProject)
	case c != nil:
		return ""
	case place.Source == "file":
		return "A SQLite database is a file; there is no server to limit."
	case place.Source == "remote":
		return fmt.Sprintf("The server is on another machine (%s), and what it may use is set there.", conn.Host)
	case place.Unit != nil:
		return fmt.Sprintf("It runs as the systemd unit %s rather than in a container, and its limits are the unit's own (MemoryMax=, CPUQuota=).", place.Unit.Name)
	}
	return "No container on this machine was found serving it, so there is nothing here to limit."
}

// readDBResources reads a connection's limits off its container, and the
// container's inspect for the change that follows a read.
func (s *Server) readDBResources(ctx context.Context, conn *dbConnection, dsn string) (dbResources, *dockerx.ContainerDetail, dbPlacement) {
	out := dbResources{Host: s.hostCapacity(ctx)}
	place := s.newDBHostView(ctx).place(ctx, conn, dsn)
	out.Refusal = resourcesRefusal(conn, place)
	if place.Container == nil {
		return out, nil, place
	}
	detail, err := s.modules.docker.Inspect(ctx, place.Container.ID)
	if err != nil {
		out.Refusal = "Docker did not describe the container: " + err.Error()
		return out, nil, place
	}
	out.Container, out.Editable = detail.Name, out.Refusal == ""
	out.Running = detail.State == "running"
	out.Limits = dbResourceLimits{MemoryBytes: detail.MemoryLimit, CPUs: detail.CPULimit}
	out.OOMKilled = detail.OOMKilled
	return out, detail, place
}

// GET /databases/{id}/resources
func (s *Server) handleDBResources(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	out, detail, place := s.readDBResources(ctx, conn, dsn)
	if out.Running {
		if used, err := s.modules.docker.MemoryInUse(ctx, detail.ID); err == nil {
			out.UsedBytes = used
		}
	}
	// A server known to be down is not dialled, as the summary does not dial
	// it: its buffer is read when it runs.
	if place.Down == "" && place.Source != "file" {
		readCtx, cancelRead := context.WithTimeout(ctx, 6*time.Second)
		out.Buffer, err = s.readMemoryBuffer(readCtx, conn, dsn)
		cancelRead()
		if err != nil && !errors.Is(err, dbx.ErrNoMemoryBuffer) {
			out.BufferError = connectError(dsn, err)
		}
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// readMemoryBuffer reads the engine's own buffer over one connection, which
// also says which flavour it is when that is not already known: a fork with
// no such buffer is not asked.
func (s *Server) readMemoryBuffer(ctx context.Context, conn *dbConnection, dsn string) (*dbx.MemoryBuffer, error) {
	if dbx.MemoryBufferSetting(conn.Driver, dbx.DefaultFlavor(conn.Driver)) == "" {
		return nil, dbx.ErrNoMemoryBuffer
	}
	switch conn.Driver {
	case dbx.DriverMongo:
		client, err := dbx.MongoClient(ctx, dsn)
		if err != nil {
			return nil, err
		}
		defer client.Disconnect(context.Background())
		identity := s.identityOf(conn.ID, dsn, func() dbx.Identity { return dbx.IdentifyMongo(ctx, client) })
		if dbx.MemoryBufferSetting(conn.Driver, identity.Flavor) == "" {
			return nil, dbx.ErrNoMemoryBuffer
		}
		return dbx.MongoMemoryBuffer(ctx, client)
	case dbx.DriverRedis:
		client, err := dbx.RedisClient(ctx, dsn, dbx.RedisDSNDatabase)
		if err != nil {
			return nil, err
		}
		defer client.Close()
		identity := s.identityOf(conn.ID, dsn, func() dbx.Identity { return dbx.IdentifyRedis(ctx, client) })
		if dbx.MemoryBufferSetting(conn.Driver, identity.Flavor) == "" {
			return nil, dbx.ErrNoMemoryBuffer
		}
		return dbx.RedisMemoryBuffer(ctx, client)
	}
	pool, err := s.modules.dbs.Pool(ctx, conn.ID, conn.Driver, dsn)
	if err != nil {
		return nil, err
	}
	identity := s.identityOf(conn.ID, dsn, func() dbx.Identity { return dbx.IdentifySQL(ctx, pool, conn.Driver) })
	return dbx.SQLMemoryBuffer(ctx, pool, conn.Driver, identity.Flavor)
}

// PUT /databases/{id}/resources
//
// A new or changed limit is `docker update`: applied to the running cgroup at
// once, nothing restarted. A lifted one replaces the container through the
// park-and-restore path the access route uses, on the same named volume,
// and is refused while the container is stopped, because a replacement is
// started and the server would be running afterwards when it was not before.
func (s *Server) handleDBResourcesUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req dbResourcesRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.MemoryMB == nil && req.CPUs == nil {
		return httpx.BadRequest("name a memoryMb or a cpus to change")
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	if s.modules.docker == nil {
		return httpx.Err(http.StatusServiceUnavailable, "docker_unavailable", "this host has no Docker socket")
	}
	// A replacement waits for the engine to stop and the new one to start.
	ctx, cancel := timeoutCtx(r, 5*time.Minute)
	defer cancel()

	current, detail, _ := s.readDBResources(ctx, conn, dsn)
	if !current.Editable {
		httpx.SetAudit(r, "database.resources.change", conn.Name, map[string]any{"refused": true})
		return httpx.Err(http.StatusConflict, "resources_unavailable", current.Refusal)
	}
	// Only what is asked for is held to the rules: a limit left as it is was
	// Docker's to accept, by whatever set it.
	var memoryMB int64
	var cpus float64
	if req.MemoryMB != nil {
		memoryMB = *req.MemoryMB
	}
	if req.CPUs != nil {
		cpus = *req.CPUs
	}
	if problem := dbLimitsProblem(memoryMB, cpus, current.Host); problem != "" {
		return httpx.BadRequest("%s", problem)
	}
	plan := planDBLimits(current.Limits, req)
	lowered := plan.after.MemoryBytes > 0 &&
		(current.Limits.MemoryBytes == 0 || plan.after.MemoryBytes < current.Limits.MemoryBytes)
	if current.Running && lowered {
		// The kernel meets a limit under what is already in use by killing
		// the engine on the spot, so that is refused rather than carried out.
		if used, err := s.modules.docker.MemoryInUse(ctx, detail.ID); err == nil && plan.after.MemoryBytes < used {
			return httpx.BadRequest("%s uses %d MiB now, and a memory limit under that makes the kernel stop it at once",
				detail.Name, (used+mib-1)/mib)
		}
	}
	replace := len(plan.lifts) > 0
	if replace {
		if !current.Running {
			return httpx.Err(http.StatusConflict, "container_stopped", fmt.Sprintf(
				"%s is not running, and lifting a limit replaces the container with one that is started. Start it first, or lift the limit when it runs.",
				detail.Name))
		}
		p := httpx.MustPrincipal(r)
		if !p.Can(auth.CapDestructive) {
			return httpx.Err(http.StatusForbidden, "forbidden",
				"lifting a limit replaces the container, which drops every session to it, and your role does not permit it")
		}
		if !s.destrLim.Allow(p.Username() + "|dbresources") {
			return httpx.Err(http.StatusTooManyRequests, "rate_limited", "too many replacements, slow down")
		}
	}

	audit := map[string]any{
		"container": detail.Name, "id": detail.ID, "replaced": replace,
		"from": current.Limits, "to": plan.after,
	}
	out := map[string]any{"limits": plan.after, "replaced": replace, "warnings": []string{}}
	if !plan.changed {
		audit["changed"] = false
		httpx.SetAudit(r, "database.resources.change", conn.Name, audit)
		httpx.JSON(w, http.StatusOK, out)
		return nil
	}
	if replace {
		audit["lifted"] = plan.lifts
		spec, err := s.modules.docker.SpecOf(ctx, detail.ID)
		if err != nil {
			return s.dockerErr(err)
		}
		spec.Limits.MemoryMB, spec.Limits.CPUs = plan.after.MemoryBytes/mib, plan.after.CPUs
		// Everyone reading the connection sees it restarting until it
		// answers again, as they would for a restart from the power route.
		change, carriedOut := s.dbConns.begin(id, "restart"), false
		defer func() { s.dbConns.finish(id, change, carriedOut) }()
		res, err := s.modules.docker.Recreate(ctx, detail.ID, dockerx.RecreateOptions{Spec: spec}, nil)
		if err != nil {
			audit["error"] = err.Error()
			httpx.SetAudit(r, "database.resources.change", conn.Name, audit)
			if errors.Is(err, dockerx.ErrComposeManaged) {
				return httpx.Err(http.StatusConflict, "compose_managed", err.Error())
			}
			return s.dockerErr(err)
		}
		carriedOut = true
		// A list on the wire, as the update's is: the page reads its length.
		warnings := append([]string{}, res.Warnings...)
		audit["newId"], audit["warnings"], out["warnings"] = res.ID, warnings, warnings
		// The pool dialled the old container; the new one has a new socket.
		s.modules.dbs.Close(id)
		s.dbConns.stale(id)
	} else {
		warnings, err := s.modules.docker.UpdateResources(ctx, detail.ID, plan.update)
		if err != nil {
			audit["error"] = err.Error()
			httpx.SetAudit(r, "database.resources.change", conn.Name, audit)
			return s.dockerErr(err)
		}
		audit["warnings"], out["warnings"] = warnings, warnings
	}
	httpx.SetAudit(r, "database.resources.change", conn.Name, audit)
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// GET /databases/provision/capacity is the machine a new server's limits are
// held to, for the form that asks for them.
func (s *Server) handleDBProvisionCapacity(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	httpx.JSON(w, http.StatusOK, s.hostCapacity(r.Context()))
	return nil
}
