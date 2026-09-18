package api

import (
	"context"
	"fmt"
	"mime"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// The two ends of a database's life that the rest of the database surface does
// not cover: getting a dump off the server, and removing the database itself.

// dbDumpDir is where this connection's dumps land. One directory per
// connection, named after it, which is why connNameRe excludes a separator.
func (s *Server) dbDumpDir(connName string) string {
	return filepath.Join(s.Cfg.BackupLocalDir, "databases", connName)
}

// handleDBBackupDownload streams a dump back to the browser.
//
// A dump that only exists on the server is half a backup: the machine it is
// protecting against losing is the machine it is stored on. The file stays
// where it was written — that is what the scheduled jobs and the restore route
// read — and this hands a copy to whoever asked for it.
//
// Invariant 6 with a different root. The client supplies a name, not a path,
// and the containment is against this connection's dump directory rather than
// JD_FILE_ROOTS: the roots are about what an operator may browse, and a
// narrowed set of them must not stop the dashboard handing back a file it
// wrote itself. files.Service is reused rather than reimplemented so the
// symlink rule is the same one every other path check applies.
func (s *Server) handleDBBackupDownload(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	conn, _, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	name := r.URL.Query().Get("file")
	if name == "" {
		return httpx.BadRequest("file is required")
	}
	dir := s.dbDumpDir(conn.Name)
	f, st, err := files.New([]string{dir}).Open(filepath.Join(dir, name))
	if err != nil {
		return mapFileError(err)
	}
	defer f.Close()
	if st.IsDir() {
		return httpx.BadRequest("%s is a directory", name)
	}
	base := filepath.Base(st.Name())
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition",
		mime.FormatMediaType("attachment", map[string]string{"filename": base}))
	http.ServeContent(w, r, base, st.ModTime(), f)
	return nil
}

type dbDropDatabaseRequest struct {
	Database string `json:"database"`
	// RemoveContainer takes the whole server away rather than one database
	// on it: the container and the named volumes it was writing to. It is
	// the only form of delete that works on a server the dashboard can see
	// but can no longer sign in to, and for a server started from this page
	// it is what "delete" meant all along.
	RemoveContainer bool `json:"removeContainer"`
}

// handleDBDropDatabase removes the database itself, as opposed to the
// dashboard's connection to it.
//
// Typed confirmation, and this is the clearest case for one in the whole
// product: it is done rarely, it takes everything with it, and there is no way
// back that does not involve a dump taken beforehand. The phrase is the
// database's own name, so the operator has to read which one they are pointing
// at — the mistake this guards is not "did I mean to do this" but "did I have
// the right connection selected".
func (s *Server) handleDBDropDatabase(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req dbDropDatabaseRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	target := dropTargetName(conn, req.Database)
	if target == "" {
		return httpx.BadRequest("this connection names no database to drop")
	}
	if err := httpx.RequireTypedConfirmation(w, r, target); err != nil {
		return err
	}
	if sameDatabase(conn, target) || req.RemoveContainer {
		var linked bool
		if err := s.Store.DB.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM deploy_database_bindings WHERE connection_id=?)`, id).Scan(&linked); err != nil {
			return httpx.Internal(err)
		}
		if linked {
			return httpx.Err(http.StatusConflict, "database_linked", "remove the deployment's managed database network before dropping this linked database")
		}
	}
	// Let go of our own connections first. Postgres refuses to drop a database
	// while anything is attached to it, and the pool this dashboard has been
	// browsing with is one of the things attached.
	s.modules.dbs.Close(id)

	ctx, cancel := timeoutCtx(r, 5*time.Minute)
	defer cancel()
	if req.RemoveContainer {
		return s.removeDatabaseContainer(ctx, w, r, id, conn, dsn, target)
	}
	res, err := dbx.DropDatabase(ctx, conn.Driver, dsn, dropArgument(conn, req.Database))
	if err != nil {
		httpx.SetAudit(r, "database.drop", conn.Name,
			map[string]any{"database": target, "error": err.Error()})
		return httpx.Err(http.StatusBadGateway, "drop_failed", err.Error())
	}

	// A connection whose database no longer exists cannot answer a single
	// request, so leaving the row behind would leave a picker entry that errors
	// on every tab. It goes with the database it pointed at — but only then:
	// dropping some *other* database on the same server leaves the connection
	// perfectly usable.
	removed := res.Gone && sameDatabase(conn, target)
	if removed {
		// A deployment may have linked the connection during the engine call.
		// Retain that identity rather than reporting a successful drop as failed.
		result, err := s.Store.DB.ExecContext(r.Context(),
			`DELETE FROM db_connections WHERE id=? AND NOT EXISTS (SELECT 1 FROM deploy_database_bindings WHERE connection_id=?)`, id, id)
		if err != nil {
			return httpx.Internal(err)
		}
		affected, _ := result.RowsAffected()
		removed = affected == 1
	}
	httpx.SetAudit(r, "database.drop", conn.Name, map[string]any{
		"database": target, "detail": res.Detail, "connectionRemoved": removed,
	})
	httpx.JSON(w, http.StatusOK, map[string]any{
		"detail":            res.Detail,
		"database":          target,
		"connectionRemoved": removed,
	})
	return nil
}

// removeDatabaseContainer is the delete that does not need a password.
//
// The DROP path signs in and asks the engine, which is the right thing for a
// database on a server that holds others — and no use at all on the case
// that actually arrives here: a container this dashboard started, whose data
// volume was initialised under an older password than the one its environment
// now states, so every connection fails with "password authentication
// failed" and the drop fails the same way. The container is the database.
// Removing it, with the volumes it was writing to, is what the operator
// meant, and Docker does not need the engine's cooperation to do it.
//
// A volume another container still uses is refused by Docker and reported as
// a warning rather than forced: the connection's server is what was asked
// for, not whatever else shares its storage. A compose-owned container is
// refused outright, since the stack would recreate it on the next deploy.
func (s *Server) removeDatabaseContainer(
	ctx context.Context, w http.ResponseWriter, r *http.Request,
	id int64, conn *dbConnection, dsn, target string,
) error {
	if s.modules.docker == nil {
		return httpx.Err(http.StatusServiceUnavailable, "docker_unavailable", "this host has no Docker socket")
	}
	info, err := dbx.ParseDSN(conn.Driver, dsn)
	if err != nil {
		return httpx.BadRequest("the saved connection could not be read")
	}
	server := s.serverBehind(ctx, conn, info)
	if server == nil {
		return httpx.BadRequest("no container on this server runs %s; drop the database instead", conn.Name)
	}
	if project := server.container.Labels["com.docker.compose.project"]; project != "" {
		return httpx.Err(http.StatusConflict, "compose_managed",
			fmt.Sprintf("%s belongs to the compose project %s; take the stack down from the Docker page instead", server.container.Name, project))
	}
	detail, err := s.modules.docker.Inspect(ctx, server.container.ID)
	if err != nil {
		return s.dockerErr(err)
	}
	volumes := []string{}
	for _, m := range detail.Mounts {
		if m.Type == "volume" && m.Name != "" {
			volumes = append(volumes, m.Name)
		}
	}
	if err := s.modules.docker.RemoveContainer(ctx, server.container.ID, true, true); err != nil {
		httpx.SetAudit(r, "database.drop", conn.Name,
			map[string]any{"database": target, "container": server.container.Name, "error": err.Error()})
		return s.dockerErr(err)
	}
	warnings := []string{}
	removedVolumes := []string{}
	for _, name := range volumes {
		if err := s.modules.docker.RemoveVolume(ctx, name, false); err != nil {
			warnings = append(warnings, fmt.Sprintf("volume %s was kept: %v", name, err))
			continue
		}
		removedVolumes = append(removedVolumes, name)
	}
	result, err := s.Store.DB.ExecContext(r.Context(),
		`DELETE FROM db_connections WHERE id=? AND NOT EXISTS (SELECT 1 FROM deploy_database_bindings WHERE connection_id=?)`, id, id)
	if err != nil {
		return httpx.Internal(err)
	}
	affected, _ := result.RowsAffected()
	removed := affected == 1
	summary := "container " + server.container.Name + " removed"
	if len(removedVolumes) > 0 {
		summary += " with its data"
	}
	httpx.SetAudit(r, "database.drop", conn.Name, map[string]any{
		"database": target, "container": server.container.Name, "volumes": removedVolumes,
		"warnings": warnings, "connectionRemoved": removed,
	})
	httpx.JSON(w, http.StatusOK, map[string]any{
		"detail":            summary,
		"database":          target,
		"container":         server.container.Name,
		"volumes":           removedVolumes,
		"warnings":          warnings,
		"connectionRemoved": removed,
	})
	return nil
}

// dropTargetName is what the operator has to type, and what the toast reports.
//
// It is the database's name on six engines. SQLite's database is a file, so it
// is the file's name rather than the path — a phrase nobody can type without
// copying it is a phrase that gets copied without being read. Redis numbers its
// keyspaces, and "0" is not a confirmation, so it takes the form Redis itself
// uses in INFO keyspace.
func dropTargetName(conn *dbConnection, requested string) string {
	name := requested
	if name == "" {
		name = conn.Database
	}
	switch conn.Driver {
	case dbx.DriverSQLite:
		return filepath.Base(conn.Database)
	case dbx.DriverRedis:
		if name == "" {
			name = "0"
		}
		return "db" + strings.TrimPrefix(name, "db")
	}
	return name
}

// dropArgument is what dbx is asked to remove, which for SQLite is decided by
// the connection string rather than by anything the client sent.
func dropArgument(conn *dbConnection, requested string) string {
	if conn.Driver == dbx.DriverSQLite {
		return ""
	}
	if requested == "" {
		return conn.Database
	}
	return requested
}

// sameDatabase reports whether the dropped database was this connection's own.
func sameDatabase(conn *dbConnection, target string) bool {
	switch conn.Driver {
	case dbx.DriverSQLite:
		return true
	case dbx.DriverRedis:
		// Never: flushing a keyspace leaves the connection working.
		return false
	}
	return strings.EqualFold(target, conn.Database)
}

// handleDBConnURL hands back the connection string this dashboard holds.
//
// Every other database route exists so nobody has to see a DSN. This one
// exists because of what happens next: the string is pasted into another
// deployment's DATABASE_URL, and a server the operator can browse but cannot
// connect an application to is half a feature. The secret is already theirs —
// it is readable from the container by anyone who can reach this route — so
// the protection that matters is that the read is deliberate and recorded,
// not that it is impossible.
func (s *Server) handleDBConnURL(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return httpx.BadRequest("invalid id")
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	target := r.URL.Query().Get("target")
	switch target {
	case "", "host":
	case "container":
		ctx, cancel := timeoutCtx(r, 15*time.Second)
		defer cancel()
		dsn, err = s.databaseApplicationURL(ctx, conn, dsn)
		if err != nil {
			return err
		}
	case "public":
		// The same string with this machine's public address where loopback
		// was: what gets pasted on a laptop. A connection that already names
		// another machine is handed back as it is — that address is the one
		// everybody else uses too.
		dsn, err = s.publicDatabaseURL(conn, dsn)
		if err != nil {
			return err
		}
	default:
		return httpx.BadRequest("target must be host, container or public")
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.SetAudit(r, "database.connection.reveal", conn.Name,
		map[string]any{"driver": string(conn.Driver), "target": target})
	reference := ""
	if target == "container" {
		reference = fmt.Sprintf("${{database.%d}}", conn.ID)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"id": conn.ID, "name": conn.Name, "driver": conn.Driver, "url": dsn, "reference": reference,
	})
	return nil
}

func (s *Server) publicDatabaseURL(conn *dbConnection, dsn string) (string, error) {
	info, err := dbx.ParseDSN(conn.Driver, dsn)
	if err != nil {
		return "", httpx.BadRequest("the saved connection could not be read")
	}
	if !databaseLoopback(info.Host) && !isContainerAddress(info.Host) {
		return dsn, nil
	}
	out, err := publicConnectionURL(conn.Driver, dsn, info, preferredPublicAddress(proxysvc.PublicAddresses()))
	if err != nil {
		return "", httpx.BadRequest("%v", err)
	}
	return out, nil
}

// preferredPublicAddress picks the IPv4 address where the host has one: it is
// the one the operator's own network can be relied on to route to.
func preferredPublicAddress(addrs []string) string {
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			return a
		}
	}
	if len(addrs) > 0 {
		return addrs[0]
	}
	return ""
}
