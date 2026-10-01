package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/portalloc"
	"github.com/docker/docker/errdefs"
	"go.mongodb.org/mongo-driver/bson"
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
// It is the inventory, narrowed to the shape this route has always answered
// with: the running servers a driver here can open, each as the connection
// that could be made to it. The inventory route carries everything else — the
// stopped ones, the files, the engines with no driver.
//
// Docker is not required. A server with no Docker socket at all still runs
// databases — that is what a VPS with an apt-installed Postgres is — and
// answering "unavailable" to the whole question because one of its two halves
// is missing was how a native database became invisible.
func (s *Server) handleDBDetected(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()

	inv, _ := s.buildInventory(ctx, true, dbFileScanResult{})
	conns, names, err := s.savedConnections(ctx)
	if err != nil {
		return err
	}
	dbx.AttachConnections(inv.Instances, conns)

	out := detectedResponse{Servers: []detectedServer{}}
	for _, inst := range inv.Instances {
		access, ok := inv.Access(inst.Key)
		if !ok || inst.Kind != dbx.KindServer || inst.State != dbx.StateRunning {
			continue
		}
		if inst.Confidence == dbx.ConfidencePort {
			// A container known only by a port it exposes is a guess, and this
			// shape has no field to say so: every row here reads as a server
			// that was detected. The inventory lists it, labelled.
			continue
		}
		server := detectedServer{Candidate: access.Candidate}
		if !inst.Connectable {
			server.Reason = inst.Reason
		}
		if adopted := connectionNames(inst.Connections, names); len(adopted) > 0 {
			server.Adopted = adopted[0]
		}
		if inst.Container != nil {
			server.Health, server.Status = inst.Container.Health, inst.Container.Status
		}
		out.Servers = append(out.Servers, server)
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// hostCandidates finds the database servers installed on the machine itself,
// as opposed to the ones in containers: one candidate per server, at the
// address its driver dials.
//
// A container published to the host is owned by `docker-proxy`, which matches
// no rule, so the two halves do not normally overlap. One case does: a
// container on the host's network namespace, which looks from here exactly
// like a native server. Its sockets are left out, because the Docker half
// knows its credentials and this one does not — reporting the same server
// twice, once connectable and once asking for a password, would be worse than
// either answer alone.
func (s *Server) hostCandidates(ctx context.Context, known []detectedServer) []dbx.Candidate {
	listeners, scan := s.collectListeners(ctx)
	if !scan.OK {
		return nil
	}
	native := listeners[:0:0]
	for _, l := range listeners {
		if l.Manager != "container" {
			native = append(native, l)
		}
	}
	seen := map[string]bool{}
	for _, k := range known {
		seen[addressKey(k.Host, k.Port)] = true
	}
	inv := dbx.Discover(dbx.Facts{Listeners: native})
	out := []dbx.Candidate{}
	for _, inst := range inv.Instances {
		access, ok := inv.Access(inst.Key)
		if !ok {
			continue
		}
		key := addressKey(access.Candidate.Host, access.Candidate.Port)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, access.Candidate)
	}
	return out
}

// existingDSNs maps an address already covered by a saved connection to that
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

// addressKey names a server's address so that two spellings of it agree:
// localhost, 127.0.0.1 and [::1] are one place on this machine, and a
// connection typed with one must cover a server found at another.
func addressKey(host string, port int) string { return dbx.AddressIdentity(host, port) }

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
//
// It signs in before it saves. The caller that starts a server retries this
// until the engine inside it answers, and used to be handed a connection row
// on the first attempt whether or not the credentials in it worked.
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

	inst, access, err := s.containerInstance(ctx, req.Container)
	if err != nil {
		return err
	}
	if !inst.Connectable {
		return httpx.BadRequest("%s cannot be connected to: %s", inst.Name, inst.Reason)
	}
	return s.connectInstance(ctx, w, r, inst, access, connectOptions{
		name: req.Name, action: "database.connection.adopt",
	})
}

// containerInstance re-detects one container by name or id, so an adopt acts
// on what is true now rather than on what a listing said some time ago.
func (s *Server) containerInstance(ctx context.Context, name string) (*dbx.Instance, *dbx.Access, error) {
	ctx = s.modules.docker.WithReadSnapshot(ctx)
	facts, _, scan := s.collectContainers(ctx, true)
	if !scan.OK {
		return nil, nil, httpx.Err(http.StatusBadGateway, "docker_failed", scan.Reason)
	}
	listeners, _ := s.collectListeners(ctx)
	inv := dbx.Discover(dbx.Facts{Containers: facts, Listeners: listeners})
	running := false
	for _, c := range facts {
		if c.Name != name && c.ID != name {
			continue
		}
		if c.State != "running" {
			continue
		}
		running = true
		inst, ok := inv.FindContainer(c.Name)
		if !ok {
			break
		}
		if access, ok := inv.Access(inst.Key); ok {
			return inst, &access, nil
		}
		return inst, nil, nil
	}
	if running {
		return nil, nil, httpx.BadRequest("%s is not a database image this dashboard recognises", name)
	}
	return nil, nil, httpx.BadRequest("no running container named %q", name)
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
	if err := validConnectionNames(req.User, req.Database); err != nil {
		return err
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
	if err := s.dialDatabase(ctx, req.Driver, dsn); err != nil {
		httpx.SetAudit(r, "database.connection.host", cand.Host, map[string]any{
			"ok": false, "driver": string(req.Driver), "user": cand.User,
		})
		// The engine's own words. "password authentication failed for user
		// postgres" is the entire diagnosis and names the account it refused;
		// anything this layer said instead would be a worse version of it.
		return httpx.BadRequest("%v", err)
	}

	// The same server, database and account is the same connection, and takes
	// the password that was just seen to work. Anything less specific — the
	// address alone — used to hand back whichever connection happened to be
	// there and throw the new credentials away.
	conn, stored, names, err := s.adoptedDatabaseConnection(ctx, req.Driver, dsn)
	if err != nil {
		return err
	}
	if conn != nil {
		if stored == dsn {
			httpx.SkipAudit(r)
			httpx.JSON(w, http.StatusOK, conn)
			return nil
		}
		return s.resealConnection(ctx, w, r, conn, req.Driver, dsn, "database.connection.host",
			map[string]any{"port": cand.Port, "driver": req.Driver, "user": cand.User, "refreshed": true})
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = dbx.HostConnectionName(cand)
	}
	if !connNameRe.MatchString(name) {
		return httpx.BadRequest("name may contain letters, digits, spaces, dots, dashes and underscores")
	}
	name = uniqueConnectionName(name, names)
	return s.saveConnectionFrom(w, r, s.hostOrigin(ctx, cand), name, cand.Driver, dsn, "database.connection.host",
		map[string]any{"process": cand.Process, "port": cand.Port})
}

// resealConnection replaces a saved connection's DSN with one that has just
// been dialled, keeping the row and everything that references it.
func (s *Server) resealConnection(
	ctx context.Context, w http.ResponseWriter, r *http.Request,
	conn *dbConnection, driver dbx.Driver, dsn, action string, detail map[string]any,
) error {
	contained, err := s.containDSN(driver, dsn)
	if err != nil {
		return err
	}
	sealed, err := s.Sealer.Seal(contained)
	if err != nil {
		return httpx.Internal(err)
	}
	if _, err := s.Store.DB.ExecContext(ctx, `UPDATE db_connections SET dsn_enc = ? WHERE id = ?`, sealed, conn.ID); err != nil {
		return httpx.Internal(err)
	}
	// The pool, if any, was dialled with the old DSN and would keep failing.
	s.modules.dbs.Close(conn.ID)
	conn, _, err = s.dbConnRow(ctx, conn.ID)
	if err != nil {
		return err
	}
	detail["connection"] = conn.Name
	httpx.SetAudit(r, action, conn.Host, detail)
	httpx.JSON(w, http.StatusOK, conn)
	return nil
}

// hostOrigin is the inventory key of the server on this machine a candidate
// dials, or "" for a server that is somewhere else. It is what lets a
// connection typed with a password be found again as the same server the
// inventory lists.
func (s *Server) hostOrigin(ctx context.Context, cand dbx.Candidate) string {
	if !databaseLoopback(cand.Host) {
		return ""
	}
	facts := dbx.Facts{}
	facts.Listeners, _ = s.collectListeners(ctx)
	facts.Sockets, _ = s.collectSockets(ctx)
	facts.Units, _ = s.collectUnits(ctx)
	native := facts.Listeners[:0:0]
	for _, l := range facts.Listeners {
		if l.Manager != "container" {
			native = append(native, l)
		}
	}
	facts.Listeners = native
	want := addressKey(cand.Host, cand.Port)
	for _, inst := range dbx.Discover(facts).Instances {
		if inst.Driver != cand.Driver {
			continue
		}
		for _, e := range inst.Endpoints {
			if e.Kind == "tcp" && addressKey(e.Host, e.Port) == want {
				return inst.Key
			}
		}
	}
	return ""
}

// saveConnection is the tail every route that creates a connection shares:
// contain the DSN, seal it, store it, audit what it points at but never the
// string itself.
func (s *Server) saveConnection(
	w http.ResponseWriter, r *http.Request,
	name string, driver dbx.Driver, dsn string, action string, detail map[string]any,
) error {
	return s.saveConnectionFrom(w, r, "", name, driver, dsn, action, detail)
}

// saveConnectionFrom is saveConnection for a connection made from a found
// database: origin is that database's inventory key, kept on the row so the
// connection is known for the same server after its address changes. Saving
// one is the operator saying they want it, so any earlier word to ignore it
// is taken back.
func (s *Server) saveConnectionFrom(
	w http.ResponseWriter, r *http.Request,
	origin, name string, driver dbx.Driver, dsn string, action string, detail map[string]any,
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
		`INSERT INTO db_connections(name, driver, dsn_enc, created_at, origin) VALUES(?,?,?,?,?)`,
		name, string(driver), sealed, time.Now().Unix(), origin)
	if err != nil {
		return httpx.BadRequest("could not save connection: %v", err)
	}
	id, _ := res.LastInsertId()
	if origin != "" {
		unignored, err := s.setIgnored(r.Context(), origin, "", false)
		if err != nil {
			return err
		}
		if unignored {
			detail["unignored"] = true
		}
	}
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
// Adopting is idempotent — a server already covered by a connection is skipped,
// so calling this on every page load converges rather than accumulating
// duplicates.
//
// It signs in before it saves. It used not to: whatever a container's
// environment stated was sealed and stored, and a container whose volume had
// been initialised under another password became a connection that failed
// every request made of it. A server that refuses what its container states
// is reported with the engine's own words instead.
//
// It leaves alone what the operator said to leave alone. A server marked
// ignored — by hand, or by forgetting its last connection — is not connected
// again, which is what makes forgetting one mean something.
//
// It does not ask the same thing twice. This runs whenever the page opens, and
// a server that refused what its container states would otherwise be sent the
// same failed login on every visit; a refusal is remembered against what was
// tried (see database_inventory_signin.go) and repeated from memory until the
// container or its credentials change.
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

	// A fresh reading, with no files in it: a file is never connected unasked.
	inv, scans := s.buildInventory(ctx, true, dbFileScanResult{})
	conns, names, err := s.savedConnections(ctx)
	if err != nil {
		return err
	}
	dbx.AttachConnections(inv.Instances, conns)
	ignored, err := s.ignoredOrigins(ctx)
	if err != nil {
		return err
	}
	taken := map[string]string{}
	for id, name := range names {
		taken[strconv.FormatInt(id, 10)] = name
	}

	// Three passes. The first decides, without touching the network, what each
	// server needs; the second signs in to the ones worth trying, a few at a
	// time; the third saves what worked and says the rest, in the order the
	// servers were found — so the answer does not depend on which dial
	// happened to finish first.
	added, skipped, left, linked := []string{}, []string{}, []string{}, []string{}
	unreachable := []unreachableServer{}
	needsCredentials := []credentialServer{}
	present := map[string]bool{}
	attempts := []*syncAttempt{}
	// outcomes holds, per instance, what the third pass reports for it.
	outcomes := make([]func(), len(inv.Instances))
	for i := range inv.Instances {
		inst := &inv.Instances[i]
		present[inst.Key] = true
		if inst.Kind != dbx.KindServer || inst.Driver == "" || inst.State != dbx.StateRunning {
			// Stopped servers, declared services and engines with no driver
			// are the inventory's to list; there is nothing here to connect.
			continue
		}
		if len(inst.Connections) > 0 {
			skipped = append(skipped, connectionNames(inst.Connections, names)...)
			// A connection made before origins were recorded, or typed by
			// hand, was matched by where it dials. It learns which server it
			// is, so it is still known for it when that address changes.
			for _, id := range inst.Connections {
				res, err := s.Store.DB.ExecContext(ctx,
					`UPDATE db_connections SET origin = ? WHERE id = ? AND origin = ''`, inst.Key, id)
				if err != nil {
					return httpx.Internal(err)
				}
				if n, _ := res.RowsAffected(); n > 0 {
					linked = append(linked, names[id])
				}
			}
			continue
		}
		if ignored[inst.Key] {
			left = append(left, inst.Key)
			continue
		}
		access, hasAccess := inv.Access(inst.Key)
		if inst.Source == dbx.SourceHost {
			if !inst.Connectable || !hasAccess {
				continue
			}
			cand := access.Candidate
			asks := func() {
				needsCredentials = append(needsCredentials, credentialServer{
					Driver: string(cand.Driver), Host: cand.Host, Port: cand.Port,
					Process: cand.Process, Name: dbx.HostConnectionName(cand),
					User: cand.User, Database: cand.Database,
				})
			}
			// An engine that ships with no credentials at all is simply
			// tried, and kept if it answers. Everything else is asked about
			// rather than guessed at: a wrong password against the operator's
			// own server is an authentication failure in their logs, and on a
			// host running fail2ban it is a step towards banning this
			// dashboard.
			if cand.NeedsCredentials || dbx.BuildDSN(cand, "") == "" {
				outcomes[i] = asks
				continue
			}
			attempt := &syncAttempt{inst: inst, access: access, fingerprint: signInFingerprint(inst, access)}
			if _, refused := s.refusedBefore(inst.Key, attempt.fingerprint); refused {
				// It was tried as it ships and wanted a password. It is not
				// tried again until it is another process.
				outcomes[i] = asks
				continue
			}
			attempts = append(attempts, attempt)
			outcomes[i] = func() {
				switch {
				case attempt.unattempted:
					unreachable = append(unreachable, unreachableServer{
						Container: inst.Name, Driver: string(inst.Driver), Reason: syncOutOfTime,
					})
				case attempt.err != nil:
					s.rememberRefusal(inst.Key, attempt.fingerprint, "", credentialRefusal(attempt.err))
					asks()
				case attempt.dsn != "":
					s.forgetRefusal(inst.Key)
					name, err := s.insertConnection(ctx, inst.Key, uniqueConnectionName(dbx.HostConnectionName(cand), taken), cand.Driver, attempt.dsn)
					if err != nil {
						return
					}
					taken["+"+name] = name
					added = append(added, name)
				}
			}
			continue
		}

		container := inst.Name
		if inst.Confidence == dbx.ConfidencePort {
			// A guess from a port number is listed, and never connected — or
			// reported as a failure to connect — on the dashboard's own
			// initiative.
			continue
		}
		cannot := func(reason string) func() {
			return func() {
				unreachable = append(unreachable, unreachableServer{
					Container: container, Driver: string(inst.Driver), Reason: reason,
				})
			}
		}
		if !inst.Connectable || !hasAccess {
			outcomes[i] = cannot(unreachableReason(inst.Reason))
			continue
		}
		switch inst.Credentials {
		case dbx.CredentialsEnv, dbx.CredentialsArgs, dbx.CredentialsOpen, dbx.CredentialsSecretFile:
		default:
			outcomes[i] = cannot("its container states no password — connect it with the one it uses")
			continue
		}
		attempt := &syncAttempt{inst: inst, access: access, fingerprint: signInFingerprint(inst, access)}
		if kept, refused := s.refusedBefore(inst.Key, attempt.fingerprint); refused {
			// The same container, stating the same credentials, already
			// refused them. Saying so again costs nothing; asking again is a
			// failed login in its log every time this page opens.
			outcomes[i] = cannot(kept.says)
			continue
		}
		attempts = append(attempts, attempt)
		outcomes[i] = func() {
			switch {
			case attempt.unattempted:
				cannot(syncOutOfTime)()
			case attempt.secretErr != nil:
				says := "its password is kept in " + access.SecretFile + " inside the container, which could not be read — connect it with the password"
				s.rememberRefusal(inst.Key, attempt.fingerprint, says, false)
				cannot(says)()
			case attempt.err != nil:
				says := refusalReason(attempt.err)
				s.rememberRefusal(inst.Key, attempt.fingerprint, says, credentialRefusal(attempt.err))
				cannot(says)()
			case attempt.dsn != "":
				s.forgetRefusal(inst.Key)
				name, err := s.insertConnection(ctx, inst.Key, uniqueConnectionName(container, taken), inst.Driver, attempt.dsn)
				if err != nil {
					return
				}
				taken["+"+name] = name
				added = append(added, name)
			}
		}
	}

	s.signInAll(ctx, attempts)
	for _, outcome := range outcomes {
		if outcome != nil {
			outcome()
		}
	}
	s.pruneRefusals(present)

	if len(added) == 0 && len(linked) == 0 {
		// Nothing happened, so nothing is worth a line in the audit log. The
		// alternative is an entry every time somebody opens the page.
		httpx.SkipAudit(r)
	} else {
		// A connection that learned which server it is changed too, once: it
		// is what the next forget and the next reconcile will act on.
		s.dropInventory()
		httpx.SetAudit(r, "database.connection.sync", strings.Join(append(append([]string{}, added...), linked...), ", "),
			map[string]any{"added": added, "linked": linked})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"added": added, "already": skipped, "unreachable": unreachable,
		"needsCredentials": needsCredentials, "ignored": left, "scans": scans,
	})
	return nil
}

// syncOutOfTime is said of a server the reconcile did not get to. It is not a
// refusal and must not be worded as one.
const syncOutOfTime = "it was not tried this time: the reconcile ran out of time before reaching it"

// syncAttempt is one sign-in the reconcile makes, and how it went.
type syncAttempt struct {
	inst        *dbx.Instance
	access      dbx.Access
	fingerprint string

	// dsn is the connection string that signed in. unattempted says the
	// request's time ran out first; secretErr that the container's password
	// file could not be read; err that the server answered no.
	dsn         string
	unattempted bool
	secretErr   error
	err         error
}

// signInAll makes the reconcile's sign-ins, a few at a time. Each checks the
// request's clock before it dials: a sign-in started after the deadline would
// fail with the deadline's own error, and be reported as a refusal by a server
// that was never asked.
func (s *Server) signInAll(ctx context.Context, attempts []*syncAttempt) {
	slots := make(chan struct{}, syncDialWorkers)
	var wg sync.WaitGroup
	for _, attempt := range attempts {
		wg.Add(1)
		go func(a *syncAttempt) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				a.unattempted = true
				return
			}
			if ctx.Err() != nil {
				a.unattempted = true
				return
			}
			password := a.access.Password
			if a.inst.Source != dbx.SourceHost && a.inst.Credentials == dbx.CredentialsSecretFile {
				secret, err := s.containerSecret(ctx, a.inst.Container.ID, a.access.SecretFile)
				if err != nil {
					a.secretErr = err
					return
				}
				password = secret
			}
			dsn := dbx.BuildDSN(a.access.Candidate, password)
			if dsn == "" {
				return
			}
			if err := s.signIn(ctx, a.access.Candidate, &dsn, a.access.Unverified); err != nil {
				if ctx.Err() != nil {
					// The clock ran out under the dial; the server said nothing.
					a.unattempted = true
					return
				}
				a.err = err
				return
			}
			a.dsn = dsn
		}(attempt)
	}
	wg.Wait()
}

// insertConnection stores one connection the reconcile signed in to.
func (s *Server) insertConnection(ctx context.Context, origin, name string, driver dbx.Driver, dsn string) (string, error) {
	contained, err := s.containDSN(driver, dsn)
	if err != nil {
		return "", err
	}
	sealed, err := s.Sealer.Seal(contained)
	if err != nil {
		return "", err
	}
	if _, err := s.Store.DB.ExecContext(ctx,
		`INSERT INTO db_connections(name, driver, dsn_enc, created_at, origin) VALUES(?,?,?,?,?)`,
		name, string(driver), sealed, time.Now().Unix(), origin); err != nil {
		return "", err
	}
	return name, nil
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
		defer client.Disconnect(context.Background())
		if mongoURINamesNobody(contained) {
			// A ping is the one thing MongoDB answers without asking who is
			// there, so a connection that names no account passes it against a
			// server that will refuse everything else. Listing the databases
			// is refused unless the server really is open. A connection that
			// names an account was authenticated when it was made, and is not
			// asked for a privilege it may not have.
			if _, err := client.ListDatabaseNames(ctx, bson.D{}); err != nil {
				return err
			}
		}
		return nil
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

// mongoURINamesNobody reports a MongoDB connection string with no account in
// it: one that signs in as nobody.
//
// Read by hand rather than with net/url, which refuses the comma-separated
// host list a replica set's string has.
func mongoURINamesNobody(uri string) bool {
	authority := uri
	if _, rest, ok := strings.Cut(uri, "://"); ok {
		authority = rest
	}
	if i := strings.IndexAny(authority, "/?"); i >= 0 {
		authority = authority[:i]
	}
	at := strings.LastIndexByte(authority, '@')
	return at <= 0 || strings.HasPrefix(authority, ":")
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
	return unreachableServer{
		Container: c.Container,
		Driver:    string(c.Driver),
		Reason:    unreachableReason(c.Reason),
	}, true
}

// unreachableReason never lets a server be reported with nothing said about it.
func unreachableReason(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return "this container was recognised but does not expose a port this dashboard can reach"
	}
	return reason
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
	// The same server with the extension a retrieval or geospatial schema
	// creates on its first migration; the official image ships neither.
	"pgvector": {
		driver: dbx.DriverPostgres, label: "PostgreSQL 16 + pgvector", image: "pgvector/pgvector:pg16",
		port: 5432, dataPath: "/var/lib/postgresql/data",
		env: func(pw, db string) []dockerx.EnvVar {
			return []dockerx.EnvVar{
				{Name: "POSTGRES_USER", Value: "jd"},
				{Name: "POSTGRES_PASSWORD", Value: pw},
				{Name: "POSTGRES_DB", Value: db},
			}
		},
	},
	"postgis": {
		driver: dbx.DriverPostgres, label: "PostgreSQL 16 + PostGIS", image: "postgis/postgis:16-3.5-alpine",
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
	for _, key := range []string{"postgres", "pgvector", "postgis", "mysql", "mariadb", "redis", "mongodb"} {
		if key == "postgis" && !deploy.PostGISImageSupported(runtime.GOARCH) {
			// Offered where it cannot run, it would fail only at the pull.
			continue
		}
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
	// Exposure is where the new server is reachable from: "public" publishes
	// its port on every interface and opens the firewall, "local" keeps it to
	// this server. Empty means public.
	Exposure dbExposure `json:"exposure"`
}

// provisionBinding turns the requested exposure into the host address the
// port is published on. The default is every interface: a database made from
// the Databases page exists to be handed to somebody, and a default that has
// to be undone under Maintenance before the connection string works from a
// laptop is a default nobody wanted. Deployment quick setup asks for "local"
// explicitly, because its database is reached over the deployment network.
func provisionBinding(exposure dbExposure) (dbExposure, string, error) {
	switch exposure {
	case "", exposurePublic:
		return exposurePublic, "0.0.0.0", nil
	case exposureLocal:
		return exposureLocal, "127.0.0.1", nil
	}
	return "", "", fmt.Errorf("exposure must be local or public")
}

// Redis does not read REDIS_PASSWORD itself. A private configuration keeps the
// generated password out of command arguments; the script contains no request text.
const redisProvisionBootstrap = `set -eu; umask 077; printf 'requirepass %s\n' "$REDIS_PASSWORD" > /tmp/jd-redis.conf; chown redis:redis /tmp/jd-redis.conf; exec docker-entrypoint.sh redis-server /tmp/jd-redis.conf`

// handleDBProvision starts a database server and saves the connection to it.
//
// The password is generated here and never leaves this process except into the
// container's own environment: the operator does not choose it, see it or type
// it, which is the difference between "automatic" and "a form with fewer
// fields". It can always be read back from the container by an admin, and the
// dashboard's own copy is sealed like every other stored DSN.
//
// The port is published on every interface unless the request asks for this
// server only, and the firewall is opened for it the way the connection page's
// Open to the internet switch does — so the string under "From anywhere" works
// the moment the engine answers. The binding and what the firewall did are
// audited, and the saved connection still dials loopback: hostAddress maps a
// 0.0.0.0 binding to 127.0.0.1, which is the address this process can reach.
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
	// MongoDB 5 and later die with an illegal instruction on a CPU without
	// AVX (a Proxmox default CPU type) or ARMv8.2 atomics, and the linked
	// application then restarts in a loop; saying so here is cheaper than a
	// pull and a crash.
	if req.Engine == "mongodb" {
		if features := deploy.HostCPUFeatures(); features != nil {
			if missing := deploy.MongoCPUUnsupported(runtime.GOARCH, features); missing != "" {
				return httpx.BadRequest("MongoDB 7 cannot run on this server's CPU (%s); set the VM's CPU type to host, or run MongoDB 4.4 from the Docker page", missing)
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
		Image: tmpl.image,
		// Started, not merely created. A spec that only creates leaves a
		// container in "Created" that never listens, so the adopt that follows
		// waits for an engine that was never going to answer.
		Start:         true,
		RestartPolicy: "unless-stopped",
		Env:           tmpl.env(password, database),
		Ports: []dockerx.PortMapping{
			{HostIP: hostIP, HostPort: port, ContainerPort: tmpl.port, Protocol: "tcp"},
		},
		Mounts: []dockerx.MountSpec{
			{Type: "volume", Source: volume, Target: tmpl.dataPath},
		},
	}
	if req.Engine == "redis" {
		// Redis does not consume REDIS_PASSWORD. A constant bootstrap writes a
		// private config so the generated secret never becomes process argv.
		spec.Command = []string{"sh", "-c", redisProvisionBootstrap}
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
		"engine": req.Engine, "image": tmpl.image, "port": port, "exposure": exposure,
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
		"container": name, "engine": req.Engine, "driver": tmpl.driver,
		"host": "127.0.0.1", "port": port, "database": database,
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
