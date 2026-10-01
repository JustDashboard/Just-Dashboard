package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/portalloc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/docker/docker/errdefs"
)

// Finding and making database servers, so nobody has to write a DSN by hand.
//
// The dashboard already drives the Docker socket and already knows how to run
// a container. An operator who wants a database was nonetheless being asked to
// go to the Docker page, start one, work out what its connection string would
// be from the environment variables they had just typed, come back, and paste
// it in. Every fact in that string was already in this process.
//
// Two routes close it. The first reports what is already running; the second
// starts something new. Both end at the same place — a connection row whose
// DSN was assembled here and sealed here.
//
// The password never crosses the wire in either direction. A detected server's
// is read from its container at the moment it is adopted; a provisioned one's
// is generated here and handed to the container, never to the browser. That is
// not decoration: `dockerx.RedactEnv` exists because container environment is
// where deployments keep their secrets, and a route that answered "here is the
// password for every database on this host" would undo it.

// detectedResponse is what the Databases page lists above the connections the
// operator has already made.
type detectedResponse struct {
	Servers []detectedServer `json:"servers"`
}

type detectedServer struct {
	dbx.Candidate
	// Adopted names the existing connection pointing at this container, so the
	// page can show "already connected" rather than offering to add a second
	// row for the same server.
	Adopted string `json:"adopted,omitempty"`
	Health  string `json:"health,omitempty"`
	Status  string `json:"status,omitempty"`
}

// handleDBDetected lists what is running here, from both places it can be.
//
// Docker is no longer required. A server with no Docker socket at all still
// runs databases — that is what a VPS with an apt-installed Postgres is — and
// answering "unavailable" to the whole question because one of its two halves
// is missing was how a native database became invisible.
func (s *Server) handleDBDetected(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()

	var containers []dockerx.Container
	if s.modules.docker != nil {
		var err error
		containers, err = s.modules.docker.ListContainers(ctx, false)
		if err != nil {
			return httpx.Err(http.StatusBadGateway, "docker_failed", err.Error())
		}
	}
	existing, err := s.existingDSNs(ctx)
	if err != nil {
		return err
	}

	out := detectedResponse{Servers: []detectedServer{}}
	for _, c := range containers {
		cand, _ := dbx.Detect(c.Name, c.Image, nil, publishedPorts(c.Ports), nil)
		if cand == nil {
			continue
		}
		// The environment is only read once the image is known to be a
		// database, so an inspect is not paid for every container on the host.
		if detail, err := s.modules.docker.Inspect(ctx, c.ID); err == nil {
			cand, _ = dbx.Detect(c.Name, c.Image, envMap(detail.Env), publishedPorts(c.Ports), containerIPs(detail))
		}
		if cand == nil {
			continue
		}
		out.Servers = append(out.Servers, detectedServer{
			Candidate: *cand,
			Adopted:   existing[addressKey(cand.Host, cand.Port)],
			Health:    c.Health,
			Status:    c.Status,
		})
	}
	for _, cand := range s.hostCandidates(ctx, out.Servers) {
		out.Servers = append(out.Servers, detectedServer{
			Candidate: cand,
			Adopted:   existing[addressKey(cand.Host, cand.Port)],
		})
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// hostCandidates finds the database servers installed on the machine itself,
// as opposed to the ones in containers.
//
// A container published to the host is owned by `docker-proxy`, which matches
// no rule, so the two halves do not normally overlap. One case does: a
// container on the host's network namespace, which looks from here exactly
// like a native server. Those are dropped by address, because the Docker half
// knows their credentials and this one does not — reporting the same server
// twice, once connectable and once asking for a password, would be worse than
// either answer alone.
func (s *Server) hostCandidates(ctx context.Context, known []detectedServer) []dbx.Candidate {
	listeners, err := proxysvc.ListListeners(ctx)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, k := range known {
		seen[addressKey(k.Host, k.Port)] = true
	}
	out := []dbx.Candidate{}
	for _, l := range listeners {
		cand := dbx.DetectHost(dbx.HostListener{
			Protocol: l.Protocol, Address: l.Address, Port: int(l.Port),
			Process: l.Process, User: l.User,
		})
		if cand == nil {
			continue
		}
		key := addressKey(cand.Host, cand.Port)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, *cand)
	}
	return out
}

// existingDSNs maps a host:port already covered by a saved connection to that
// connection's name. It opens each stored DSN, which is why it lives behind
// the same capability as the rest of this file.
func (s *Server) existingDSNs(ctx context.Context) (map[string]string, error) {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT id FROM db_connections`)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, httpx.Internal(err)
		}
		ids = append(ids, id)
	}
	out := map[string]string{}
	for _, id := range ids {
		conn, _, err := s.dbConnRow(ctx, id)
		if err != nil {
			continue
		}
		out[addressKey(conn.Host, atoiDefault(conn.Port, 0))] = conn.Name
	}
	return out, nil
}

func addressKey(host string, port int) string { return host + ":" + strconv.Itoa(port) }

func publishedPorts(ports []dockerx.Port) []dbx.PublishedPort {
	out := make([]dbx.PublishedPort, 0, len(ports))
	for _, p := range ports {
		out = append(out, dbx.PublishedPort{
			ContainerPort: int(p.PrivatePort), HostIP: p.IP, HostPort: int(p.PublicPort),
		})
	}
	return out
}

func envMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, e := range env {
		if name, value, ok := strings.Cut(e, "="); ok {
			out[name] = value
		}
	}
	return out
}

type adoptRequest struct {
	Container string `json:"container"`
	Name      string `json:"name"`
}

// handleDBAdopt turns a detected server into a saved connection.
//
// The client names a container, not a DSN. That is the whole point: the
// credentials are read from the container here and sealed here, so the browser
// never holds them and an operator never types them.
func (s *Server) handleDBAdopt(w http.ResponseWriter, r *http.Request) error {
	var req adoptRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Container) == "" {
		return httpx.BadRequest("container is required")
	}
	if s.modules.docker == nil {
		return httpx.Err(http.StatusServiceUnavailable, "docker_unavailable",
			"this host has no Docker socket")
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()

	cand, password, err := s.candidateFor(ctx, req.Container)
	if err != nil {
		return err
	}
	if !cand.Connectable() {
		return httpx.BadRequest("%s cannot be connected to: %s", cand.Container, cand.Reason)
	}
	dsn := dbx.BuildDSN(*cand, password)
	if dsn == "" {
		return httpx.BadRequest("no connection string could be built for %s", cand.Driver)
	}

	// Adopting the same container twice is not an error, it is the same
	// request arriving again — which it does, because the caller that creates a
	// server retries this until the engine inside it is actually answering, and
	// the first attempt is the one that creates the row. Returning what is
	// already there beats a UNIQUE violation on the name.
	conn, stored, existing, err := s.adoptedDatabaseConnection(ctx, cand.Driver, dsn)
	if err != nil {
		return err
	}
	if conn != nil {
		dsn, err = refreshedDatabasePassword(cand.Driver, stored, password)
		if err != nil {
			return httpx.Internal(err)
		}
		if stored == dsn {
			httpx.SkipAudit(r)
			httpx.JSON(w, http.StatusOK, conn)
			return nil
		}
		// Preserve this login's database and transport options. A replacement
		// container may rotate its password without changing its logical identity.
		sealed, err := s.Sealer.Seal(dsn)
		if err != nil {
			return httpx.Internal(err)
		}
		if _, err := s.Store.DB.ExecContext(r.Context(),
			`UPDATE db_connections SET dsn_enc = ? WHERE id = ?`, sealed, conn.ID); err != nil {
			return httpx.BadRequest("could not update connection: %v", err)
		}
		// The pool, if any, was dialled with the old DSN and would keep failing.
		s.modules.dbs.Close(conn.ID)
		conn, _, err = s.dbConnRow(r.Context(), conn.ID)
		if err != nil {
			return err
		}
		httpx.SetAudit(r, "database.connection.refresh", conn.Name, map[string]any{
			"container": cand.Container, "image": cand.Image, "driver": cand.Driver,
			"host": conn.Host, "user": conn.User,
		})
		httpx.JSON(w, http.StatusOK, conn)
		return nil
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = cand.Container
	}
	if !connNameRe.MatchString(name) {
		return httpx.BadRequest("name may contain letters, digits, spaces, dots, dashes and underscores")
	}
	name = uniqueConnectionName(name, existing)
	return s.saveConnection(w, r, name, cand.Driver, dsn, "database.connection.adopt",
		map[string]any{"container": cand.Container, "image": cand.Image})
}

type hostConnectRequest struct {
	Driver   dbx.Driver `json:"driver"`
	Host     string     `json:"host"`
	Port     int        `json:"port"`
	User     string     `json:"user"`
	Password string     `json:"password"`
	Database string     `json:"database"`
	Name     string     `json:"name"`
}

// handleDBConnectHost saves a connection to a database installed on the
// machine rather than in a container.
//
// It dials before it stores, and that ordering is the whole point. The first
// version of this handed the browser a connection string with the password
// left out and let it be saved as it stood, which produced a connection row
// that existed, looked connected, and answered "password authentication failed
// for user postgres" to every request made of it afterwards. A credential the
// dashboard cannot verify is one it must not keep: the engine's own refusal
// belongs in the dialog the operator is still looking at, where it names the
// user it refused, not in a red badge on a row they have to delete.
//
// The password arrives in the body and goes no further than dbx.BuildDSN,
// which is the one place a secret joins a DSN, and it is sealed like every
// other stored one.
func (s *Server) handleDBConnectHost(w http.ResponseWriter, r *http.Request) error {
	var req hostConnectRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if !req.Driver.Valid() {
		return httpx.BadRequest("driver must be one of %s", driverNames())
	}
	if strings.TrimSpace(req.Host) == "" || req.Port <= 0 {
		return httpx.BadRequest("a host and port are required")
	}
	cand := dbx.Candidate{
		Driver: req.Driver, Source: dbx.SourceHost, Host: strings.TrimSpace(req.Host),
		Port: req.Port, User: strings.TrimSpace(req.User), Database: strings.TrimSpace(req.Database),
	}
	dsn := dbx.BuildDSN(cand, req.Password)
	if dsn == "" {
		return httpx.BadRequest("no connection string could be built for %s", req.Driver)
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	if err := s.probeConnection(ctx, req.Driver, dsn); err != nil {
		httpx.SetAudit(r, "database.connection.host", cand.Host, map[string]any{
			"ok": false, "driver": string(req.Driver), "user": cand.User,
		})
		// The engine's own words. "password authentication failed for user
		// postgres" is the entire diagnosis and names the account it refused;
		// anything this layer said instead would be a worse version of it.
		return httpx.BadRequest("%v", err)
	}

	existing, err := s.existingDSNs(ctx)
	if err != nil {
		return err
	}
	if have, ok := existing[addressKey(cand.Host, cand.Port)]; ok {
		conn, _, err := s.connectionByName(ctx, have)
		if err != nil {
			return err
		}
		httpx.SkipAudit(r)
		httpx.JSON(w, http.StatusOK, conn)
		return nil
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = dbx.HostConnectionName(cand)
	}
	if !connNameRe.MatchString(name) {
		return httpx.BadRequest("name may contain letters, digits, spaces, dots, dashes and underscores")
	}
	name = uniqueConnectionName(name, existing)
	return s.saveConnection(w, r, name, cand.Driver, dsn, "database.connection.host",
		map[string]any{"process": cand.Process, "port": cand.Port})
}

// candidateFor re-detects one container by name, so an adopt acts on what is
// true now rather than on what a listing said some time ago.
func (s *Server) candidateFor(ctx context.Context, name string) (*dbx.Candidate, string, error) {
	containers, err := s.modules.docker.ListContainers(ctx, false)
	if err != nil {
		return nil, "", httpx.Err(http.StatusBadGateway, "docker_failed", err.Error())
	}
	for _, c := range containers {
		if c.Name != name && c.ID != name {
			continue
		}
		detail, err := s.modules.docker.Inspect(ctx, c.ID)
		if err != nil {
			return nil, "", httpx.Err(http.StatusBadGateway, "docker_failed", err.Error())
		}
		cand, password := dbx.Detect(c.Name, c.Image, envMap(detail.Env), publishedPorts(c.Ports), containerIPs(detail))
		if cand == nil {
			return nil, "", httpx.BadRequest("%s is not a database image this dashboard recognises", name)
		}
		return cand, password, nil
	}
	return nil, "", httpx.BadRequest("no running container named %q", name)
}

// saveConnection is the tail every route that creates a connection shares:
// contain the DSN, seal it, store it, audit what it points at but never the
// string itself.
func (s *Server) saveConnection(
	w http.ResponseWriter, r *http.Request,
	name string, driver dbx.Driver, dsn string, action string, detail map[string]any,
) error {
	contained, err := s.containDSN(driver, dsn)
	if err != nil {
		return err
	}
	sealed, err := s.Sealer.Seal(contained)
	if err != nil {
		return httpx.Internal(err)
	}
	res, err := s.Store.DB.ExecContext(r.Context(),
		`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
		name, string(driver), sealed, time.Now().Unix())
	if err != nil {
		return httpx.BadRequest("could not save connection: %v", err)
	}
	id, _ := res.LastInsertId()
	conn, _, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	detail["driver"] = driver
	detail["host"] = conn.Host
	detail["user"] = conn.User
	httpx.SetAudit(r, action, name, detail)
	httpx.JSON(w, http.StatusCreated, conn)
	return nil
}

// handleDBSync connects everything found running here that is not connected
// already, and reports what it added.
//
// The list-with-a-Connect-button this replaces was a step that had exactly one
// sensible answer. A database running on the operator's own server, which this
// process can already read the credentials of, is one they want to work with;
// asking them to confirm that once per container was ceremony, and it left a
// panel of buttons occupying the top of the page for as long as they ignored it.
//
// It is a POST, and it audits, because it writes: a GET that quietly created
// connection rows would be both a lie about the verb and a hole in invariant 5.
// Adopting is idempotent — a server already covered by a connection is skipped
// by address, so calling this on every page load converges rather than
// accumulating duplicates.
//
// It also reports what it recognised and could *not* adopt, which it used to
// drop on the floor. A Postgres on a compose network with no published port is
// the commonest database on any server this runs on, and the old behaviour was
// the worst available: the container was detected, its credentials were read,
// `Connectable()` came back false, and the loop moved on — so the operator
// pressed a button that appeared to do nothing at all, about a database
// sitting in plain sight on their own Docker page. The reason string had
// existed the whole time and had nowhere to go. Silence is the one answer a
// reconcile must never give about a server it can see.
func (s *Server) handleDBSync(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()

	var containers []dockerx.Container
	if s.modules.docker != nil {
		var err error
		containers, err = s.modules.docker.ListContainers(ctx, false)
		if err != nil {
			return httpx.Err(http.StatusBadGateway, "docker_failed", err.Error())
		}
	}
	existing, err := s.existingDSNs(ctx)
	if err != nil {
		return err
	}

	added, skipped := []string{}, []string{}
	unreachable := []unreachableServer{}
	for _, c := range containers {
		if cand, _ := dbx.Detect(c.Name, c.Image, nil, publishedPorts(c.Ports), nil); cand == nil {
			continue
		}
		detail, err := s.modules.docker.Inspect(ctx, c.ID)
		if err != nil {
			continue
		}
		cand, password := dbx.Detect(c.Name, c.Image, envMap(detail.Env), publishedPorts(c.Ports), containerIPs(detail))
		if cand == nil {
			continue
		}
		if row, ok := unreachableFrom(cand); ok {
			unreachable = append(unreachable, row)
			continue
		}
		if name, ok := existing[addressKey(cand.Host, cand.Port)]; ok {
			skipped = append(skipped, name)
			continue
		}
		dsn := dbx.BuildDSN(*cand, password)
		if dsn == "" {
			continue
		}
		name := uniqueConnectionName(cand.Container, existing)
		contained, err := s.containDSN(cand.Driver, dsn)
		if err != nil {
			continue
		}
		sealed, err := s.Sealer.Seal(contained)
		if err != nil {
			return httpx.Internal(err)
		}
		if _, err := s.Store.DB.ExecContext(ctx,
			`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
			name, string(cand.Driver), sealed, time.Now().Unix()); err != nil {
			continue
		}
		existing[addressKey(cand.Host, cand.Port)] = name
		added = append(added, name)
	}

	// The databases installed on the machine itself, which the loop above
	// cannot see because they are in no container.
	needsCredentials := []credentialServer{}
	for _, cand := range s.hostCandidates(ctx, detectedFrom(containers)) {
		if name, ok := existing[addressKey(cand.Host, cand.Port)]; ok {
			skipped = append(skipped, name)
			continue
		}
		dsn := dbx.BuildDSN(cand, "")
		// An engine that ships with no credentials at all is simply tried, and
		// kept if it answers. Everything else is asked about rather than
		// guessed at: a wrong password against the operator's own server is an
		// authentication failure in their logs, and on a host running fail2ban
		// it is a step towards banning this dashboard.
		if cand.NeedsCredentials || dsn == "" || s.probeConnection(ctx, cand.Driver, dsn) != nil {
			needsCredentials = append(needsCredentials, credentialServer{
				Driver: string(cand.Driver), Host: cand.Host, Port: cand.Port,
				Process: cand.Process, Name: dbx.HostConnectionName(cand),
				User: cand.User, Database: cand.Database,
			})
			continue
		}
		name := uniqueConnectionName(dbx.HostConnectionName(cand), existing)
		contained, err := s.containDSN(cand.Driver, dsn)
		if err != nil {
			continue
		}
		sealed, err := s.Sealer.Seal(contained)
		if err != nil {
			return httpx.Internal(err)
		}
		if _, err := s.Store.DB.ExecContext(ctx,
			`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
			name, string(cand.Driver), sealed, time.Now().Unix()); err != nil {
			continue
		}
		existing[addressKey(cand.Host, cand.Port)] = name
		added = append(added, name)
	}

	if len(added) == 0 {
		// Nothing happened, so nothing is worth a line in the audit log. The
		// alternative is an entry every time somebody opens the page.
		httpx.SkipAudit(r)
	} else {
		httpx.SetAudit(r, "database.connection.sync", strings.Join(added, ", "),
			map[string]any{"added": added})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"added": added, "already": skipped, "unreachable": unreachable,
		"needsCredentials": needsCredentials,
	})
	return nil
}

// credentialServer is a database running on this machine that the dashboard
// can see but cannot sign in to.
//
// It carries the connection string it would use, with the password left out,
// because that is the difference between "there is a Postgres here somewhere"
// and a form the operator finishes in one field. Nothing secret is in it: the
// whole point is that this process does not know the secret.
type credentialServer struct {
	Driver  string `json:"driver"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Process string `json:"process,omitempty"`
	Name    string `json:"name"`
	// User and Database are the engine's own conventions — the account a
	// Postgres always has, the database a MySQL does not need — so the form
	// opens with everything filled in but the password.
	User     string `json:"user,omitempty"`
	Database string `json:"database,omitempty"`
}

// detectedFrom is the addresses the Docker half already accounts for, so the
// host half does not report the same server a second time. It reads the
// published ports only — an inspect per container is what the caller pays for
// when it actually needs the credentials, and this needs nothing but addresses.
func detectedFrom(containers []dockerx.Container) []detectedServer {
	out := []detectedServer{}
	for _, c := range containers {
		cand, _ := dbx.Detect(c.Name, c.Image, nil, publishedPorts(c.Ports), nil)
		if cand != nil {
			out = append(out, detectedServer{Candidate: *cand})
		}
	}
	return out
}

// probeConnection dials a DSN once, without keeping anything. Every engine
// answers something, including the two that are not SQL — a Redis reported as
// unreachable because the SQL path refused it would be exactly the silence
// this whole file exists to remove.
func (s *Server) probeConnection(ctx context.Context, driver dbx.Driver, dsn string) error {
	contained, err := s.containDSN(driver, dsn)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	switch driver {
	case dbx.DriverMongo:
		client, err := dbx.MongoClient(ctx, contained)
		if err != nil {
			return err
		}
		return client.Disconnect(context.Background())
	case dbx.DriverRedis:
		client, err := dbx.RedisClient(ctx, contained, 0)
		if err != nil {
			return err
		}
		return client.Close()
	}
	_, err = dbx.Probe(ctx, driver, contained)
	return err
}

// unreachableServer is a database this host is running that could be
// recognised but not connected to, and why.
//
// The reason comes from dbx.Detect and is phrased for the operator rather than
// for a log — it names the fix, because the only useful thing to say about a
// container with no published port is which one it is and what to do.
type unreachableServer struct {
	Container string `json:"container"`
	Driver    string `json:"driver"`
	Reason    string `json:"reason"`
}

// unreachableFrom projects a detected candidate into the row the sync response
// carries, reporting false for one that can simply be adopted.
//
// A pure function, and tested as one, for the reason updaterArgs is: the whole
// defect this replaces was a branch that fell through to `continue`, and a
// test that needed a Docker daemon to notice it coming back is a test nobody
// runs. The fallback reason matters too — a candidate that is unconnectable
// for a reason dbx did not name would otherwise be reported as a blank line,
// which is the same silence in a different shape.
func unreachableFrom(c *dbx.Candidate) (unreachableServer, bool) {
	if c == nil || c.Connectable() {
		return unreachableServer{}, false
	}
	reason := c.Reason
	if strings.TrimSpace(reason) == "" {
		reason = "this container was recognised but does not expose a port this dashboard can reach"
	}
	return unreachableServer{
		Container: c.Container,
		Driver:    string(c.Driver),
		Reason:    reason,
	}, true
}

// uniqueConnectionName keeps a second container whose name collides with an
// existing connection from being rejected by the insert. The address is what
// identifies a server, not the name, so a suffix is enough.
func uniqueConnectionName(base string, existing map[string]string) string {
	taken := map[string]bool{}
	for _, name := range existing {
		taken[name] = true
	}
	if !taken[base] {
		return base
	}
	for n := 2; n < 100; n++ {
		candidate := base + "-" + strconv.Itoa(n)
		if !taken[candidate] {
			return candidate
		}
	}
	return base + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

// --- provisioning -----------------------------------------------------------

// provisionTemplate is a database server this dashboard can start.
//
// Deliberately a short, closed list of the official images, and deliberately
// server-side: the frontend's Docker templates are starting points a person
// reads and edits, whereas these are run unattended and their settings have to
// be ones this package can also connect to afterwards. The port is where the
// engine listens; the volume is what stops the data disappearing with the
// container.
type provisionTemplate struct {
	driver dbx.Driver
	// flavor is the product in the shared vocabulary: valkey behind the redis
	// driver, timescaledb behind postgres.
	flavor string
	label  string
	// image is what a request that names no version starts, and versions the
	// closed list it may choose from, newest first. A request never supplies
	// an image reference: the tag is looked up, not taken.
	image    string
	versions []provisionVersion
	port     int
	dataPath string
	// user is the account the server is created with, and empty for an engine
	// protected by a password alone. database reports whether the image
	// creates a named database on first boot.
	user     string
	database bool
	// env builds the container's environment from the account, its password
	// and the initial database.
	env func(user, password, database string) []dockerx.EnvVar
	// bootstrap replaces the image's command for an engine that does not
	// read its password from the environment. It is a constant script: the
	// secret reaches it through the environment, never through its text.
	bootstrap string
}

// provisionVersion is one release a template can start.
type provisionVersion struct {
	id, image string
}

func postgresProvisionEnv(user, pw, db string) []dockerx.EnvVar {
	return []dockerx.EnvVar{
		{Name: "POSTGRES_USER", Value: user},
		{Name: "POSTGRES_PASSWORD", Value: pw},
		{Name: "POSTGRES_DB", Value: db},
	}
}

// passwordProvisionEnv is the environment of the engines that take a
// password and nothing else. The variable is REDIS_PASSWORD for all of them,
// whatever they are called: it is the name detection reads the password back
// from when the container is adopted.
func passwordProvisionEnv(_, pw, _ string) []dockerx.EnvVar {
	return []dockerx.EnvVar{{Name: "REDIS_PASSWORD", Value: pw}}
}

// passwordProvisionBootstrap starts a Redis-protocol server that does not
// read REDIS_PASSWORD itself. A private configuration keeps the password out
// of command arguments; the script is built from two constants and contains no
// request text.
//
// The file is removed before it is written, and that line is what lets the
// container start a second time. It lives in /tmp and is handed to the
// server's own account, and the kernel refuses to open a file in a sticky,
// world-writable directory that neither the opener nor the directory's owner
// owns (fs.protected_regular, on by default on every current distribution) —
// root included. Without the rm the first start worked and every restart
// after it died on "can't create: Permission denied", which with a restart
// policy is a container that crash-loops from the first reboot onward.
func passwordProvisionBootstrap(server, account string) string {
	return `set -eu; umask 077; conf=/tmp/jd-` + server + `.conf; rm -f "$conf"; ` +
		`printf 'requirepass %s\n' "$REDIS_PASSWORD" > "$conf"; chown ` + account + `:` + account + ` "$conf"; ` +
		`exec docker-entrypoint.sh ` + server + ` "$conf"`
}

var provisionTemplates = map[string]provisionTemplate{
	"postgres": {
		driver: dbx.DriverPostgres, flavor: dbx.FlavorPostgres, label: "PostgreSQL", image: "postgres:16-alpine",
		versions: []provisionVersion{
			{"17", "postgres:17-alpine"}, {"16", "postgres:16-alpine"},
			{"15", "postgres:15-alpine"}, {"14", "postgres:14-alpine"},
		},
		port: 5432, dataPath: "/var/lib/postgresql/data", user: "jd", database: true,
		env: postgresProvisionEnv,
	},
	// The same server with the extension a retrieval or geospatial schema
	// creates on its first migration; the official image ships neither.
	"pgvector": {
		driver: dbx.DriverPostgres, flavor: dbx.FlavorPostgres, label: "PostgreSQL + pgvector", image: "pgvector/pgvector:pg16",
		versions: []provisionVersion{
			{"17", "pgvector/pgvector:pg17"}, {"16", "pgvector/pgvector:pg16"}, {"15", "pgvector/pgvector:pg15"},
		},
		port: 5432, dataPath: "/var/lib/postgresql/data", user: "jd", database: true,
		env: postgresProvisionEnv,
	},
	"postgis": {
		driver: dbx.DriverPostgres, flavor: dbx.FlavorPostgres, label: "PostgreSQL + PostGIS", image: "postgis/postgis:16-3.5-alpine",
		versions: []provisionVersion{
			{"17", "postgis/postgis:17-3.5-alpine"}, {"16", "postgis/postgis:16-3.5-alpine"},
			{"15", "postgis/postgis:15-3.5-alpine"},
		},
		port: 5432, dataPath: "/var/lib/postgresql/data", user: "jd", database: true,
		env: postgresProvisionEnv,
	},
	// PostgreSQL with the time-series extension preloaded, on the official
	// image's own layout: the same variables, the same data directory.
	"timescaledb": {
		driver: dbx.DriverPostgres, flavor: dbx.FlavorTimescaleDB, label: "TimescaleDB", image: "timescale/timescaledb:latest-pg16",
		versions: []provisionVersion{
			{"17", "timescale/timescaledb:latest-pg17"}, {"16", "timescale/timescaledb:latest-pg16"},
			{"15", "timescale/timescaledb:latest-pg15"},
		},
		port: 5432, dataPath: "/var/lib/postgresql/data", user: "jd", database: true,
		env: postgresProvisionEnv,
	},
	"mysql": {
		driver: dbx.DriverMySQL, flavor: dbx.FlavorMySQL, label: "MySQL", image: "mysql:8.4",
		versions: []provisionVersion{{"9", "mysql:9"}, {"8.4", "mysql:8.4"}, {"8.0", "mysql:8.0"}},
		port:     3306, dataPath: "/var/lib/mysql", user: "jd", database: true,
		env: func(user, pw, db string) []dockerx.EnvVar {
			return []dockerx.EnvVar{
				{Name: "MYSQL_ROOT_PASSWORD", Value: pw},
				{Name: "MYSQL_USER", Value: user},
				{Name: "MYSQL_PASSWORD", Value: pw},
				{Name: "MYSQL_DATABASE", Value: db},
			}
		},
	},
	"mariadb": {
		driver: dbx.DriverMySQL, flavor: dbx.FlavorMariaDB, label: "MariaDB", image: "mariadb:11",
		versions: []provisionVersion{
			{"12", "mariadb:12"}, {"11", "mariadb:11"}, {"11.4", "mariadb:11.4"},
			{"10.11", "mariadb:10.11"}, {"10.6", "mariadb:10.6"},
		},
		port: 3306, dataPath: "/var/lib/mysql", user: "jd", database: true,
		env: func(user, pw, db string) []dockerx.EnvVar {
			return []dockerx.EnvVar{
				{Name: "MARIADB_ROOT_PASSWORD", Value: pw},
				{Name: "MARIADB_USER", Value: user},
				{Name: "MARIADB_PASSWORD", Value: pw},
				{Name: "MARIADB_DATABASE", Value: db},
			}
		},
	},
	"redis": {
		driver: dbx.DriverRedis, flavor: dbx.FlavorRedis, label: "Redis", image: "redis:7-alpine",
		versions: []provisionVersion{{"8", "redis:8-alpine"}, {"7", "redis:7-alpine"}, {"6", "redis:6-alpine"}},
		port:     6379, dataPath: "/data",
		env: passwordProvisionEnv, bootstrap: passwordProvisionBootstrap("redis-server", "redis"),
	},
	"valkey": {
		driver: dbx.DriverRedis, flavor: dbx.FlavorValkey, label: "Valkey", image: "valkey/valkey:8-alpine",
		versions: []provisionVersion{
			{"9", "valkey/valkey:9-alpine"}, {"8", "valkey/valkey:8-alpine"}, {"7.2", "valkey/valkey:7.2-alpine"},
		},
		port: 6379, dataPath: "/data",
		env: passwordProvisionEnv, bootstrap: passwordProvisionBootstrap("valkey-server", "valkey"),
	},
	// KeyDB publishes one tag for both architectures, and it has not moved
	// since 6.3.4: there is no other release to offer.
	"keydb": {
		driver: dbx.DriverRedis, flavor: dbx.FlavorKeyDB, label: "KeyDB", image: "eqalpha/keydb:latest",
		versions: []provisionVersion{{"latest", "eqalpha/keydb:latest"}},
		port:     6379, dataPath: "/data",
		env: passwordProvisionEnv, bootstrap: passwordProvisionBootstrap("keydb-server", "keydb"),
	},
	// Dragonfly reads any flag from a DFLY_-prefixed variable, so it needs no
	// bootstrap. REDIS_PASSWORD is set beside it only so that adopting the
	// container finds the password where it looks for every other one.
	"dragonfly": {
		driver: dbx.DriverRedis, flavor: dbx.FlavorDragonfly, label: "Dragonfly",
		image:    "docker.dragonflydb.io/dragonflydb/dragonfly:latest",
		versions: []provisionVersion{{"latest", "docker.dragonflydb.io/dragonflydb/dragonfly:latest"}},
		port:     6379, dataPath: "/data",
		env: func(_, pw, _ string) []dockerx.EnvVar {
			return []dockerx.EnvVar{{Name: "DFLY_requirepass", Value: pw}, {Name: "REDIS_PASSWORD", Value: pw}}
		},
	},
	"mongodb": {
		driver: dbx.DriverMongo, flavor: dbx.FlavorMongoDB, label: "MongoDB", image: "mongo:7",
		versions: []provisionVersion{{"8", "mongo:8"}, {"7", "mongo:7"}, {"6", "mongo:6"}},
		port:     27017, dataPath: "/data/db", user: "jd", database: true,
		env: func(user, pw, db string) []dockerx.EnvVar {
			return []dockerx.EnvVar{
				{Name: "MONGO_INITDB_ROOT_USERNAME", Value: user},
				{Name: "MONGO_INITDB_ROOT_PASSWORD", Value: pw},
				{Name: "MONGO_INITDB_DATABASE", Value: db},
			}
		},
	},
	// The native protocol's port, which is the one this package's driver
	// speaks; the HTTP interface is left unpublished. Access management is
	// switched on for the account so the roles it is later asked to create
	// are ones it may create.
	"clickhouse": {
		driver: dbx.DriverClickHouse, flavor: dbx.FlavorClickHouse, label: "ClickHouse",
		image: "clickhouse/clickhouse-server:25.8",
		versions: []provisionVersion{
			{"26.3", "clickhouse/clickhouse-server:26.3"}, {"25.8", "clickhouse/clickhouse-server:25.8"},
			{"25.3", "clickhouse/clickhouse-server:25.3"}, {"24.8", "clickhouse/clickhouse-server:24.8"},
		},
		port: 9000, dataPath: "/var/lib/clickhouse", user: "jd", database: true,
		env: func(user, pw, db string) []dockerx.EnvVar {
			return []dockerx.EnvVar{
				{Name: "CLICKHOUSE_USER", Value: user},
				{Name: "CLICKHOUSE_PASSWORD", Value: pw},
				{Name: "CLICKHOUSE_DB", Value: db},
				{Name: "CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT", Value: "1"},
			}
		},
	},
}

// provisionOrder is the order the templates are offered in. Written out
// because a map has none, and a list of engines that reshuffles on every poll
// is unusable.
var provisionOrder = []string{
	"postgres", "pgvector", "postgis", "timescaledb", "mysql", "mariadb",
	"redis", "valkey", "keydb", "dragonfly", "mongodb", "clickhouse",
}

// version resolves the release a request named to its image, and the
// template's own when it named none. Anything else is refused: the list is
// closed so that what gets pulled is always a reference written in this file.
func (t provisionTemplate) version(id string) (provisionVersion, error) {
	for _, v := range t.versions {
		if (id == "" && v.image == t.image) || (id != "" && v.id == id) {
			return v, nil
		}
	}
	ids := make([]string, 0, len(t.versions))
	for _, v := range t.versions {
		ids = append(ids, v.id)
	}
	return provisionVersion{}, fmt.Errorf("%s is offered in versions %s", t.label, strings.Join(ids, ", "))
}

type provisionOption struct {
	Engine string `json:"engine"`
	Label  string `json:"label"`
	Image  string `json:"image"`
	Driver string `json:"driver"`
	Flavor string `json:"flavor"`
	// Versions is every release a request may ask for, newest first, and
	// DefaultVersion the one a request that names none is given.
	Versions       []provisionVersionOption `json:"versions"`
	DefaultVersion string                   `json:"defaultVersion"`
	Port           int                      `json:"port"`
	// DefaultUser is the account the server is created with unless the
	// request names another, and empty for an engine that has no accounts —
	// which then accepts no `user` at all. Database reports whether an
	// initial database can be named.
	DefaultUser string `json:"defaultUser"`
	Database    bool   `json:"database"`
}

type provisionVersionOption struct {
	Version string `json:"version"`
	Image   string `json:"image"`
}

func (s *Server) handleDBProvisionOptions(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	out := []provisionOption{}
	for _, key := range provisionOrder {
		if key == "postgis" && !deploy.PostGISImageSupported(runtime.GOARCH) {
			// Offered where it cannot run, it would fail only at the pull.
			continue
		}
		t := provisionTemplates[key]
		option := provisionOption{
			Engine: key, Label: t.label, Image: t.image, Driver: string(t.driver), Flavor: t.flavor,
			Versions: []provisionVersionOption{}, Port: t.port, DefaultUser: t.user, Database: t.database,
		}
		for _, v := range t.versions {
			option.Versions = append(option.Versions, provisionVersionOption{Version: v.id, Image: v.image})
			if v.image == t.image {
				option.DefaultVersion = v.id
			}
		}
		out = append(out, option)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

type provisionRequest struct {
	Engine   string `json:"engine"`
	Name     string `json:"name"`
	Database string `json:"database"`
	// Exposure is where the new server is reachable from: "local" keeps it to
	// this server, "public" publishes its port on every interface and opens
	// the firewall. Empty means local.
	Exposure dbExposure `json:"exposure"`
	// Version is one of the template's own; empty means its default.
	Version string `json:"version"`
	// User and Password replace the account name and the generated password.
	// Both are optional, and a password the request did not supply is never
	// sent back to it.
	User     string `json:"user"`
	Password string `json:"password"`
}

// provisionBinding turns the requested exposure into the host address the
// port is published on. The default is this server only.
//
// It used to be every interface, on the reasoning that a database made from
// the Databases page exists to be handed to somebody. What that produced was
// a fresh server on 0.0.0.0 with the firewall opened for it, for every
// operator who pressed the button without reading the switch — the exposure
// the fleet page then flags as a finding. Reaching a database from another
// machine is one deliberate change on its settings page; reaching it from the
// internet by default was a decision nobody made.
func provisionBinding(exposure dbExposure) (dbExposure, string, error) {
	switch exposure {
	case "", exposureLocal:
		return exposureLocal, "127.0.0.1", nil
	case exposurePublic:
		return exposurePublic, "0.0.0.0", nil
	}
	return "", "", fmt.Errorf("exposure must be local or public")
}

var (
	// provisionUserRe bounds an account name to what every engine here takes
	// unquoted, since it becomes an environment value an image interpolates
	// into its own first-boot SQL.
	provisionUserRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,31}$`)
	// provisionPasswordRe is deliberately narrow. The password is written
	// into a Redis configuration line, a ClickHouse XML file, and a MySQL DSN
	// whose own syntax is made of "@", ":" and "/"; a character that means
	// something in any of those is refused here rather than escaped three
	// different ways. Length is what makes it strong, not punctuation.
	provisionPasswordRe = regexp.MustCompile(`^[A-Za-z0-9._~!*+=,-]{8,128}$`)
)

// provisionAccount resolves the account a request asked for against what the
// engine allows, and holds a password it supplied to the characters every
// place it is written can take.
func provisionAccount(engine string, t provisionTemplate, user, password string) (string, error) {
	user = strings.TrimSpace(user)
	switch {
	case user == "":
		user = t.user
	case t.user == "":
		return "", fmt.Errorf("%s has no accounts to name; it is protected by its password alone", t.label)
	case !provisionUserRe.MatchString(user):
		return "", fmt.Errorf("a user name may contain letters, digits and underscores, and starts with a letter")
	case (engine == "mysql" || engine == "mariadb") && strings.EqualFold(user, "root"):
		// The image refuses it: root already exists and takes its password
		// from another variable.
		return "", fmt.Errorf("%s creates root itself; choose another name for the account", t.label)
	}
	if password != "" && !provisionPasswordRe.MatchString(password) {
		return "", fmt.Errorf("a password is 8 to 128 characters from letters, digits and . _ ~ ! * + = , -")
	}
	return user, nil
}

// handleDBProvision starts a database server; the adopt that follows saves
// the connection to it.
//
// Unless the request supplies one, the password is generated here and never
// leaves this process except into the container's own environment: the
// operator does not choose it, see it or type it, which is the difference
// between "automatic" and "a form with fewer fields". It can always be read
// back from the container by an admin, and the dashboard's own copy is sealed
// like every other stored DSN.
//
// The port is published on this server only unless the request asks for every
// interface, in which case the firewall is opened for it the way the
// connection page's Open to the internet switch does. The binding and what
// the firewall did are audited, and the saved connection dials loopback
// either way: hostAddress maps a 0.0.0.0 binding to 127.0.0.1, which is the
// address this process can reach.
func (s *Server) handleDBProvision(w http.ResponseWriter, r *http.Request) error {
	var req provisionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	tmpl, ok := provisionTemplates[req.Engine]
	if !ok {
		return httpx.BadRequest("unknown engine %q", req.Engine)
	}
	if req.Engine == "postgis" && !deploy.PostGISImageSupported(runtime.GOARCH) {
		return httpx.BadRequest("the PostGIS image is published for x86-64 only and this server is %s; run a PostGIS server yourself and connect it", runtime.GOARCH)
	}
	version, err := tmpl.version(strings.TrimSpace(req.Version))
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	// MongoDB 5 and later die with an illegal instruction on a CPU without
	// AVX (a Proxmox default CPU type) or ARMv8.2 atomics, and the linked
	// application then restarts in a loop; saying so here is cheaper than a
	// pull and a crash.
	if req.Engine == "mongodb" {
		if features := deploy.HostCPUFeatures(); features != nil {
			if missing := deploy.MongoCPUUnsupported(runtime.GOARCH, features); missing != "" {
				return httpx.BadRequest("MongoDB %s cannot run on this server's CPU (%s); set the VM's CPU type to host, or run MongoDB 4.4 from the Docker page", version.id, missing)
			}
		}
	}
	exposure, hostIP, err := provisionBinding(req.Exposure)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	if s.modules.docker == nil {
		return httpx.Err(http.StatusServiceUnavailable, "docker_unavailable",
			"this host has no Docker socket, so a server cannot be started")
	}

	name := strings.TrimSpace(req.Name)
	if name != "" && !containerNameRe.MatchString(name) {
		return httpx.BadRequest("a container name may contain letters, digits, dots, dashes and underscores")
	}
	database := strings.TrimSpace(req.Database)
	if !tmpl.database {
		// An engine with no named database has nothing to call one, and the
		// request's word for it is dropped rather than refused: the same form
		// is posted for every engine.
		database = ""
	} else {
		if database == "" {
			database = "app"
		}
		if !dbNameRe.MatchString(database) {
			return httpx.BadRequest("a database name may contain letters, digits and underscores")
		}
	}
	user, err := provisionAccount(req.Engine, tmpl, req.User, req.Password)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	password := req.Password
	if password == "" {
		if password, err = generatePassword(); err != nil {
			return httpx.Internal(err)
		}
	}
	// A pull on a slow link is the long part, and the engines here are small.
	ctx, cancel := timeoutCtx(r, 10*time.Minute)
	defer cancel()

	name, releaseName, err := s.reserveDatabaseName(ctx, name, req.Engine)
	if err != nil {
		return err
	}
	defer releaseName()
	volume := name + "-data"

	port, err := freeHostPort(tmpl.port, hostIP)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	spec := dockerx.ContainerSpec{
		Name:  name,
		Image: version.image,
		// Started, not merely created. A spec that only creates leaves a
		// container in "Created" that never listens, so the adopt that follows
		// waits for an engine that was never going to answer.
		Start:         true,
		RestartPolicy: "unless-stopped",
		Env:           tmpl.env(user, password, database),
		Ports: []dockerx.PortMapping{
			{HostIP: hostIP, HostPort: port, ContainerPort: tmpl.port, Protocol: "tcp"},
		},
		Mounts: []dockerx.MountSpec{
			{Type: "volume", Source: volume, Target: tmpl.dataPath},
		},
	}
	if tmpl.bootstrap != "" {
		spec.Command = []string{"sh", "-c", tmpl.bootstrap}
	}
	created, err := s.modules.docker.Create(ctx, spec, nil)
	if err != nil {
		return httpx.BadRequest("could not start %s: %v", tmpl.label, err)
	}
	// Docker lists the v6 twin of a 0.0.0.0 binding beside it; the v4 one is
	// the one the saved connection string dials.
	for _, binding := range created.Ports {
		if binding.ContainerPort == tmpl.port && binding.Protocol == "tcp" && !strings.Contains(binding.HostIP, ":") {
			port = binding.HostPort
			break
		}
	}
	detail := map[string]any{
		"engine": req.Engine, "image": version.image, "version": version.id, "port": port,
		"exposure": exposure, "user": user, "passwordSupplied": req.Password != "",
	}
	// The container is up before the engine is, but the firewall rule can go
	// in now: nothing answers on the port until the engine does, and the
	// connection string handed out afterwards has to work the first time.
	firewall := "none"
	var ferr error
	if exposure == exposurePublic {
		firewall, ferr = s.setDBFirewall(ctx, r, port, true)
		detail["firewall"] = firewall
		if ferr != nil {
			detail["firewallError"] = ferr.Error()
		}
	}
	httpx.SetAudit(r, "database.server.provision", name, detail)

	// The container exists; whether the engine inside it is ready to be talked
	// to is a separate question, and the answer takes seconds to a minute. The
	// client polls for that rather than this request hanging: a POST that
	// blocks for a minute is indistinguishable from a broken dashboard, which
	// is the same reason the compose runner streams.
	resp := map[string]any{
		"container": name, "engine": req.Engine, "driver": tmpl.driver, "flavor": tmpl.flavor,
		"version": version.id, "image": version.image,
		"host": "127.0.0.1", "port": port, "user": user, "database": database,
		"exposure": exposure, "firewall": firewall,
	}
	if ferr != nil {
		resp["firewallError"] = ferr.Error()
	}
	httpx.JSON(w, http.StatusAccepted, resp)
	return nil
}

// The default name must work for a second database too. A leftover volume is
// occupied even without its container: reusing it would keep the old password
// while saving a newly generated one that the engine never accepted.
func (s *Server) reserveDatabaseName(ctx context.Context, requested, engine string) (string, func(), error) {
	s.databaseProvisionMu.Lock()
	defer s.databaseProvisionMu.Unlock()
	if s.databaseProvisionNames == nil {
		s.databaseProvisionNames = make(map[string]bool)
	}
	for attempt := 1; attempt <= 1000; attempt++ {
		name := requested
		if name == "" {
			name = "jd-" + engine
			if attempt > 1 {
				name += "-" + strconv.Itoa(attempt)
			}
		}
		occupied := s.databaseProvisionNames[name]
		if !occupied {
			_, err := s.modules.docker.Inspect(ctx, name)
			switch {
			case err == nil:
				occupied = true
			case !errdefs.IsNotFound(err):
				return "", nil, httpx.Err(http.StatusBadGateway, "docker_failed", err.Error())
			}
		}
		if occupied {
			if requested != "" {
				return "", nil, httpx.Err(http.StatusConflict, "container_exists",
					"a container named "+name+" already exists or is being created; choose another name")
			}
			continue
		}
		volume := name + "-data"
		exists, err := s.modules.docker.VolumeExists(ctx, volume)
		if err != nil {
			return "", nil, httpx.Err(http.StatusBadGateway, "docker_failed", err.Error())
		}
		if exists {
			if requested != "" {
				return "", nil, httpx.Err(http.StatusConflict, "volume_exists", fmt.Sprintf(
					"a data volume named %s is left over from an earlier server, and a new one would reuse its data and its old password; choose another name, or manage that data on the Docker page", volume))
			}
			continue
		}
		s.databaseProvisionNames[name] = true
		return name, func() {
			s.databaseProvisionMu.Lock()
			delete(s.databaseProvisionNames, name)
			s.databaseProvisionMu.Unlock()
		}, nil
	}
	return "", nil, httpx.Err(http.StatusConflict, "name_unavailable", "choose a container name; the automatic database names are already in use")
}

// freeHostPort returns the engine's own port when nothing holds it, and the
// next free one above it otherwise — so a second Postgres does not fail to
// start with a message about a port collision. The probe binds the address
// the container will, since a port free on loopback can be held on 0.0.0.0.
func freeHostPort(preferred int, hostIP string) (int, error) {
	return portalloc.Select(preferred, 1024, nil, func(port int) error { return portalloc.Available(hostIP, "tcp", port) })
}

// generatePassword makes one nobody has to remember. It is URL-safe because it
// goes into a DSN, and long enough that its being reachable only on loopback is
// not the only thing protecting it.
func generatePassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// connectionByName resolves the row a previous adopt created, so a repeat can
// be answered with it rather than with a constraint violation.
func (s *Server) connectionByName(ctx context.Context, name string) (*dbConnection, string, error) {
	var id int64
	if err := s.Store.DB.QueryRowContext(ctx,
		`SELECT id FROM db_connections WHERE name = ?`, name).Scan(&id); err != nil {
		return nil, "", httpx.Internal(err)
	}
	return s.dbConnRow(ctx, id)
}

// containerIPs is every address this container answers on, for the fallback in
// dbx.Detect when nothing is published. The dashboard shares the host's
// network namespace and a bridge network is routable from there, so these are
// addresses it can actually dial.
func containerIPs(detail *dockerx.ContainerDetail) []string {
	if detail == nil {
		return nil
	}
	out := make([]string, 0, len(detail.NetworkList))
	for _, n := range detail.NetworkList {
		if n.IPAddress != "" {
			out = append(out, n.IPAddress)
		}
	}
	return out
}
