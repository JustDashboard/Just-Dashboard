package api

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// Application containers cannot dial the host's loopback. Resolve an existing
// database binding to its observed default-bridge address without publishing a
// new port or changing the saved connection used by the database owner.
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
		if s.modules.docker == nil {
			return "", httpx.BadRequest("this database listens on host loopback; no Docker bridge address is available")
		}
		containers, err := s.modules.docker.ListContainers(ctx, false)
		if err != nil {
			return "", httpx.BadRequest("could not inspect database containers")
		}
		matches := 0
		for _, container := range containers {
			candidate, _ := dbx.Detect(container.Name, container.Image, nil, publishedPorts(container.Ports), nil)
			if candidate != nil && candidate.Driver == conn.Driver && sameDatabaseLoopback(host, candidate.Host) && strconv.Itoa(candidate.Port) == port {
				matches++
			}
		}
		if matches > 1 {
			return "", httpx.BadRequest("multiple database bindings match localhost; save the connection with its explicit loopback IP before linking it")
		}
		found := false
		for _, container := range containers {
			candidate, _ := dbx.Detect(container.Name, container.Image, nil, publishedPorts(container.Ports), nil)
			if candidate == nil || candidate.Driver != conn.Driver || !sameDatabaseLoopback(host, candidate.Host) || strconv.Itoa(candidate.Port) != port {
				continue
			}
			detail, err := s.modules.docker.Inspect(ctx, container.ID)
			if err != nil {
				return "", httpx.BadRequest("could not inspect the database network")
			}
			for _, network := range detail.NetworkList {
				if network.Name != "bridge" || net.ParseIP(network.IPAddress) == nil {
					continue
				}
				internal, _ := dbx.Detect(container.Name, container.Image, nil, nil, []string{network.IPAddress})
				if internal == nil || !internal.Connectable() {
					continue
				}
				host, port, found = internal.Host, strconv.Itoa(internal.Port), true
				break
			}
			if found {
				break
			}
		}
		if !found {
			return "", httpx.BadRequest("this database listens on host loopback and has no default-bridge address; connect it through a shared Compose network or configure an address reachable by your application")
		}
	}
	result, err := applicationConnectionURL(conn.Driver, dsn, info, host, port)
	if err != nil {
		return "", httpx.BadRequest("this connection has no supported application URL")
	}
	return result, nil
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
