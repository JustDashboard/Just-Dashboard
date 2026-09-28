package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	mysql "github.com/go-sql-driver/mysql"
)

// An address identifies a server, not one of its databases or login roles.
// Keep those identities separate when refreshing a replacement container.
func (s *Server) adoptedDatabaseConnection(ctx context.Context, driver dbx.Driver, dsn string) (*dbConnection, string, map[string]string, error) {
	wanted, err := dbx.ParseDSN(driver, dsn)
	if err != nil {
		return nil, "", nil, httpx.Internal(err)
	}
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT id, name, driver FROM db_connections ORDER BY id`)
	if err != nil {
		return nil, "", nil, httpx.Internal(err)
	}
	var ids []int64
	names := map[string]string{}
	for rows.Next() {
		var id int64
		var name, savedDriver string
		if err := rows.Scan(&id, &name, &savedDriver); err != nil {
			_ = rows.Close()
			return nil, "", nil, httpx.Internal(err)
		}
		names[strconv.FormatInt(id, 10)] = name
		if dbx.Driver(savedDriver) == driver {
			ids = append(ids, id)
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, "", nil, httpx.Internal(err)
	}
	var matched *dbConnection
	var stored string
	for _, id := range ids {
		connection, value, err := s.dbConnRow(ctx, id)
		if err != nil {
			continue
		}
		if connection.Driver != driver || connection.Host != wanted.Host || connection.Port != wanted.Port ||
			connection.Database != wanted.Database || connection.User != wanted.User {
			continue
		}
		if matched != nil {
			return nil, "", nil, httpx.Err(http.StatusConflict, "database_ambiguous",
				"more than one saved connection matches this database and user; choose the saved connection explicitly")
		}
		matched, stored = connection, value
	}
	return matched, stored, names, nil
}

func refreshedDatabasePassword(driver dbx.Driver, dsn, password string) (string, error) {
	if driver == dbx.DriverMySQL && !strings.Contains(dsn, "://") {
		config, err := mysql.ParseDSN(dsn)
		if err != nil {
			return "", err
		}
		config.Passwd = password
		return config.FormatDSN(), nil
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	user := ""
	if parsed.User != nil {
		user = parsed.User.Username()
	}
	if password == "" {
		if user == "" {
			parsed.User = nil
		} else {
			parsed.User = url.User(user)
		}
	} else {
		parsed.User = url.UserPassword(user, password)
	}
	return parsed.String(), nil
}
