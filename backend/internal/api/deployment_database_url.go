package api

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// Application containers use a stable logical identity. Runtime activation owns
// attaching the database and application to the environment's managed network;
// revealing a URL never mutates Docker or the host-side saved connection.
func (s *Server) databaseApplicationURL(ctx context.Context, conn *dbConnection, dsn string) (string, error) {
	if conn.Driver == dbx.DriverSQLite {
		return "", httpx.BadRequest("SQLite needs a shared file mount; use the project's Storage settings")
	}
	info, err := dbx.ParseDSN(conn.Driver, dsn)
	if err != nil {
		return "", httpx.BadRequest("the saved connection could not be read")
	}
	host, port := info.Host, info.Port
	if databaseLoopback(host) {
		_, internalPort, err := s.databaseContainer(ctx, conn, info)
		if err != nil {
			return "", err
		}
		if conn.ID <= 0 {
			return "", httpx.BadRequest("save the database connection before linking it")
		}
		host, port = databaseDNSName(conn.ID), strconv.Itoa(internalPort)
	}
	result, err := applicationConnectionURL(conn.Driver, dsn, info, host, port)
	if err != nil {
		return "", httpx.BadRequest("this connection has no supported application URL")
	}
	return result, nil
}

func databaseDNSName(id int64) string { return fmt.Sprintf("db-%d.jd.internal", id) }

func (s *Server) databaseContainer(ctx context.Context, conn *dbConnection, info *dbx.ConnInfo) (*dockerx.ContainerDetail, int, error) {
	if s.modules.docker == nil {
		return nil, 0, httpx.BadRequest("Docker is required to link a loopback database")
	}
	containers, err := s.modules.docker.ListContainers(ctx, false)
	if err != nil {
		return nil, 0, httpx.BadRequest("could not inspect database containers")
	}
	var matched *dockerx.Container
	for i := range containers {
		container := &containers[i]
		candidate, _ := dbx.Detect(container.Name, container.Image, nil, publishedPorts(container.Ports), nil)
		if candidate == nil || candidate.Driver != conn.Driver || !sameDatabaseLoopback(info.Host, candidate.Host) || strconv.Itoa(candidate.Port) != info.Port {
			continue
		}
		if matched != nil {
			return nil, 0, httpx.BadRequest("multiple database bindings match localhost; save an explicit loopback IP before linking it")
		}
		matched = container
	}
	if matched == nil {
		return nil, 0, httpx.BadRequest("the loopback database has no matching container; use an application-reachable host connection")
	}
	detail, err := s.modules.docker.Inspect(ctx, matched.ID)
	if err != nil {
		return nil, 0, httpx.BadRequest("could not inspect the database network")
	}
	if detail.NetworkMode == "host" || detail.NetworkMode == "none" || strings.HasPrefix(detail.NetworkMode, "container:") {
		return nil, 0, httpx.BadRequest("the database cannot join a managed bridge network")
	}
	internal, _ := dbx.Detect(detail.Name, detail.Image, nil, nil, []string{databaseDNSName(conn.ID)})
	if internal == nil || !internal.Connectable() {
		return nil, 0, httpx.BadRequest("the database container port is unknown")
	}
	return detail, internal.Port, nil
}

func databaseLoopback(host string) bool {
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
}

func sameDatabaseLoopback(saved, observed string) bool {
	if !databaseLoopback(observed) {
		return false
	}
	if strings.EqualFold(saved, "localhost") {
		return observed == "127.0.0.1" || observed == "::1"
	}
	return net.ParseIP(strings.Trim(saved, "[]")).Equal(net.ParseIP(strings.Trim(observed, "[]")))
}

func applicationConnectionURL(driver dbx.Driver, dsn string, info *dbx.ConnInfo, host, port string) (string, error) {
	if driver == dbx.DriverMySQL && !strings.Contains(dsn, "://") {
		// Driver-specific TLS and socket options have no portable URL equivalent.
		// Refuse conversion rather than silently dropping connection protections.
		options := strings.LastIndex(dsn, ")/")
		if options < 0 || strings.Contains(dsn[options+2:], "?") || !strings.Contains(dsn, "@tcp(") {
			return "", fmt.Errorf("driver-specific MySQL options require a manually configured application URL")
		}
		u := url.URL{Scheme: "mysql", Host: net.JoinHostPort(strings.Trim(host, "[]"), port), Path: "/" + info.Database, User: url.UserPassword(info.User, info.Password)}
		return u.String(), nil
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" || u.Scheme == "" {
		return "", fmt.Errorf("unsupported connection URL")
	}
	if host == info.Host && port == info.Port {
		// Preserve SRV discovery, seed lists, omitted default ports and options.
		return dsn, nil
	}
	u.Host = net.JoinHostPort(host, port)
	return u.String(), nil
}

// databaseExtensions answers which of the schema extensions detection asks
// about a saved PostgreSQL connection offers. It is one read-only catalogue
// query on the dashboard's own pool, bounded in time; other engines answer
// nil, which preflight reads as "not asked".
func (s *Server) databaseExtensions(ctx context.Context, id int64) ([]string, error) {
	conn, dsn, err := s.dbConnRow(ctx, id)
	if err != nil || conn.Driver != dbx.DriverPostgres || s.modules.dbs == nil {
		return nil, err
	}
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	pool, err := s.modules.dbs.Pool(probe, conn.ID, conn.Driver, dsn)
	if err != nil {
		return nil, err
	}
	rows, err := pool.QueryContext(probe, `SELECT name FROM pg_available_extensions WHERE name IN ('vector', 'postgis') ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	available := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		available = append(available, name)
	}
	return available, rows.Err()
}
