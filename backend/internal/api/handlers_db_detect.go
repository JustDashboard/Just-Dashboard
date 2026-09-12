package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/portalloc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
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
	existing, err := s.existingDSNs(r.Context())
	if err != nil {
		return err
	}
	if have, ok := existing[addressKey(cand.Host, cand.Port)]; ok {
		conn, err := s.connectionByName(r.Context(), have)
		if err != nil {
			return err
		}
		httpx.SkipAudit(r)
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
		conn, err := s.connectionByName(ctx, have)
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
	driver   dbx.Driver
	label    string
	image    string
	port     int
	dataPath string
	// env builds the container's environment from a generated password.
	env func(password, database string) []dockerx.EnvVar
}

var provisionTemplates = map[string]provisionTemplate{
	"postgres": {
		driver: dbx.DriverPostgres, label: "PostgreSQL 16", image: "postgres:16-alpine",
		port: 5432, dataPath: "/var/lib/postgresql/data",
		env: func(pw, db string) []dockerx.EnvVar {
			return []dockerx.EnvVar{
				{Name: "POSTGRES_USER", Value: "jd"},
				{Name: "POSTGRES_PASSWORD", Value: pw},
				{Name: "POSTGRES_DB", Value: db},
			}
		},
	},
	"mysql": {
		driver: dbx.DriverMySQL, label: "MySQL 8", image: "mysql:8",
		port: 3306, dataPath: "/var/lib/mysql",
		env: func(pw, db string) []dockerx.EnvVar {
			return []dockerx.EnvVar{
				{Name: "MYSQL_ROOT_PASSWORD", Value: pw},
				{Name: "MYSQL_USER", Value: "jd"},
				{Name: "MYSQL_PASSWORD", Value: pw},
				{Name: "MYSQL_DATABASE", Value: db},
			}
		},
	},
	"mariadb": {
		driver: dbx.DriverMySQL, label: "MariaDB 11", image: "mariadb:11",
		port: 3306, dataPath: "/var/lib/mysql",
		env: func(pw, db string) []dockerx.EnvVar {
			return []dockerx.EnvVar{
				{Name: "MARIADB_ROOT_PASSWORD", Value: pw},
				{Name: "MARIADB_USER", Value: "jd"},
				{Name: "MARIADB_PASSWORD", Value: pw},
				{Name: "MARIADB_DATABASE", Value: db},
			}
		},
	},
	"redis": {
		driver: dbx.DriverRedis, label: "Redis 7", image: "redis:7-alpine",
		port: 6379, dataPath: "/data",
		env: func(pw, _ string) []dockerx.EnvVar {
			return []dockerx.EnvVar{{Name: "REDIS_PASSWORD", Value: pw}}
		},
	},
	"mongodb": {
		driver: dbx.DriverMongo, label: "MongoDB 7", image: "mongo:7",
		port: 27017, dataPath: "/data/db",
		env: func(pw, db string) []dockerx.EnvVar {
			return []dockerx.EnvVar{
				{Name: "MONGO_INITDB_ROOT_USERNAME", Value: "jd"},
				{Name: "MONGO_INITDB_ROOT_PASSWORD", Value: pw},
				{Name: "MONGO_INITDB_DATABASE", Value: db},
			}
		},
	},
}

type provisionOption struct {
	Engine string `json:"engine"`
	Label  string `json:"label"`
	Image  string `json:"image"`
	Driver string `json:"driver"`
}

func (s *Server) handleDBProvisionOptions(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	// Ordered, because a map is not, and a list of engines that reshuffles on
	// every poll is unusable.
	out := []provisionOption{}
	for _, key := range []string{"postgres", "mysql", "mariadb", "redis", "mongodb"} {
		t := provisionTemplates[key]
		out = append(out, provisionOption{
			Engine: key, Label: t.label, Image: t.image, Driver: string(t.driver),
		})
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

type provisionRequest struct {
	Engine   string `json:"engine"`
	Name     string `json:"name"`
	Database string `json:"database"`
}

// handleDBProvision starts a database server and saves the connection to it.
//
// The password is generated here and never leaves this process except into the
// container's own environment: the operator does not choose it, see it or type
// it, which is the difference between "automatic" and "a form with fewer
// fields". It can always be read back from the container by an admin, and the
// dashboard's own copy is sealed like every other stored DSN.
//
// It publishes to loopback only. A database this dashboard started should not
// become reachable from the internet because a default was convenient, and an
// operator who wants otherwise can change the port binding on the Docker page,
// where that decision is visible.
func (s *Server) handleDBProvision(w http.ResponseWriter, r *http.Request) error {
	var req provisionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	tmpl, ok := provisionTemplates[req.Engine]
	if !ok {
		return httpx.BadRequest("unknown engine %q", req.Engine)
	}
	if s.modules.docker == nil {
		return httpx.Err(http.StatusServiceUnavailable, "docker_unavailable",
			"this host has no Docker socket, so a server cannot be started")
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "jd-" + req.Engine
	}
	if !containerNameRe.MatchString(name) {
		return httpx.BadRequest("a container name may contain letters, digits, dots, dashes and underscores")
	}
	database := strings.TrimSpace(req.Database)
	if database == "" {
		database = "app"
	}
	if !dbNameRe.MatchString(database) {
		return httpx.BadRequest("a database name may contain letters, digits and underscores")
	}

	password, err := generatePassword()
	if err != nil {
		return httpx.Internal(err)
	}
	// A pull on a slow link is the long part, and the engines here are small.
	ctx, cancel := timeoutCtx(r, 10*time.Minute)
	defer cancel()

	port, err := freeHostPort(tmpl.port)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	spec := dockerx.ContainerSpec{
		Name:  name,
		Image: tmpl.image,
		// Started, not merely created. A spec that only creates leaves a
		// container in "Created" that never listens, so the adopt that follows
		// waits for an engine that was never going to answer.
		Start:         true,
		RestartPolicy: "unless-stopped",
		Env:           tmpl.env(password, database),
		Ports: []dockerx.PortMapping{
			{HostIP: "127.0.0.1", HostPort: port, ContainerPort: tmpl.port, Protocol: "tcp"},
		},
		Mounts: []dockerx.MountSpec{
			{Type: "volume", Source: name + "-data", Target: tmpl.dataPath},
		},
	}
	created, err := s.modules.docker.Create(ctx, spec, nil)
	if err != nil {
		return httpx.BadRequest("could not start %s: %v", tmpl.label, err)
	}
	for _, binding := range created.Ports {
		if binding.ContainerPort == tmpl.port && binding.Protocol == "tcp" && binding.HostIP == "127.0.0.1" {
			port = binding.HostPort
			break
		}
	}
	httpx.SetAudit(r, "database.server.provision", name,
		map[string]any{"engine": req.Engine, "image": tmpl.image, "port": port})

	// The container exists; whether the engine inside it is ready to be talked
	// to is a separate question, and the answer takes seconds to a minute. The
	// client polls for that rather than this request hanging: a POST that
	// blocks for a minute is indistinguishable from a broken dashboard, which
	// is the same reason the compose runner streams.
	httpx.JSON(w, http.StatusAccepted, map[string]any{
		"container": name, "engine": req.Engine, "driver": tmpl.driver,
		"host": "127.0.0.1", "port": port, "database": database,
	})
	return nil
}

// freeHostPort returns the engine's own port when nothing holds it, and the
// next free one above it otherwise — so a second Postgres does not fail to
// start with a message about a port collision.
func freeHostPort(preferred int) (int, error) {
	return portalloc.Select(preferred, 1024, nil, func(port int) error { return portalloc.Available("127.0.0.1", "tcp", port) })
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
func (s *Server) connectionByName(ctx context.Context, name string) (*dbConnection, error) {
	var id int64
	if err := s.Store.DB.QueryRowContext(ctx,
		`SELECT id FROM db_connections WHERE name = ?`, name).Scan(&id); err != nil {
		return nil, httpx.Internal(err)
	}
	conn, _, err := s.dbConnRow(ctx, id)
	return conn, err
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
