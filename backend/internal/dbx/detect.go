package dbx

import (
	"net/url"
	"strconv"
	"strings"
)

// Recognising a database server from the container running it.
//
// The dashboard already drives the Docker socket, and an official database
// image states everything a connection needs: the image says which engine, the
// standard environment variables say the credentials and the initial database,
// and the published port says where to reach it. Asking the operator to
// assemble a DSN by hand out of facts this process can already read is the
// gap this closes — "I do not know the connection string" is not a thing a
// control panel should ever leave somebody stuck on.
//
// The recognition is deliberately conservative. It matches the official images
// and their well-known variables and gives up otherwise, because a wrong guess
// here does not fail cleanly: it produces a connection that looks real, is
// saved, and then refuses to open with an error about credentials rather than
// about the guess. Nothing is inferred that the container did not state.

// Candidate is a database server found running on this host.
//
// It carries no password. What reaches a browser is the description of a
// connection that could be made, not the means to make it: the secret is read
// on the server at the moment the connection is adopted and sealed there, so
// it never crosses the wire. That is the same rule the rest of this package
// follows for a stored DSN.
type Candidate struct {
	AuthSource string `json:"-"`
	Driver     Driver `json:"driver"`
	Container  string `json:"container"`
	Image      string `json:"image"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	User       string `json:"user,omitempty"`
	Database   string `json:"database,omitempty"`
	// Reason explains a container that was recognised as a database but cannot
	// be connected to, so the UI can say why rather than silently omitting it.
	Reason string `json:"reason,omitempty"`
	// Source is "docker" or "host". The two are found differently and adopted
	// differently — a container states its credentials and a native server
	// does not — so the page has to be able to tell them apart.
	Source string `json:"source,omitempty"`
	// Process is the listening program, for a server found on the host.
	Process string `json:"process,omitempty"`
	// NeedsCredentials marks a server this dashboard can see but cannot sign
	// in to on its own. It is never true for a container, whose environment
	// says everything, and usually true for one installed on the host.
	NeedsCredentials bool `json:"needsCredentials,omitempty"`
	// ViaContainerNetwork marks a candidate reached at its container address
	// rather than a published port. It matters because that address is not
	// stable: Docker hands out a new one when the container is recreated, so a
	// connection made this way needs re-detecting after a redeploy, where one
	// made to a published port does not.
	ViaContainerNetwork bool `json:"viaContainerNetwork,omitempty"`
}

// Connectable reports whether this candidate has everything a DSN needs.
func (c Candidate) Connectable() bool { return c.Reason == "" && c.Port > 0 }

// engineRule is one image family and how to read its configuration.
type engineRule struct {
	driver Driver
	// images are matched against the repository part of the image reference,
	// as a suffix, so both "postgres" and "docker.io/library/postgres" hit and
	// "mycorp/postgres-backup" does not.
	images []string
	port   int
	// read pulls the credentials and initial database out of the container's
	// environment. Only what the image documents is looked at.
	read func(env map[string]string) (user, password, database string)
}

var engineRules = []engineRule{
	{
		driver: DriverPostgres,
		images: []string{"postgres", "postgis/postgis", "pgvector/pgvector", "timescale/timescaledb"},
		port:   5432,
		read: func(e map[string]string) (string, string, string) {
			user := firstNonEmpty(e["POSTGRES_USER"], "postgres")
			// POSTGRES_DB defaults to the user's own name, not to "postgres".
			return user, e["POSTGRES_PASSWORD"], firstNonEmpty(e["POSTGRES_DB"], user)
		},
	},
	{
		driver: DriverMySQL,
		images: []string{"mysql", "mariadb", "percona"},
		port:   3306,
		read: func(e map[string]string) (string, string, string) {
			db := firstNonEmpty(e["MYSQL_DATABASE"], e["MARIADB_DATABASE"])
			// The unprivileged account the image creates is preferred over
			// root: it is the one scoped to the database that was asked for,
			// and connecting a dashboard as root by default is a choice the
			// operator should make deliberately rather than inherit.
			if u := firstNonEmpty(e["MYSQL_USER"], e["MARIADB_USER"]); u != "" {
				return u, firstNonEmpty(e["MYSQL_PASSWORD"], e["MARIADB_PASSWORD"]), db
			}
			return "root", firstNonEmpty(e["MYSQL_ROOT_PASSWORD"], e["MARIADB_ROOT_PASSWORD"]), db
		},
	},
	{
		driver: DriverMongo,
		images: []string{"mongo", "mongodb/mongodb-community-server"},
		port:   27017,
		read: func(e map[string]string) (string, string, string) {
			return e["MONGO_INITDB_ROOT_USERNAME"], e["MONGO_INITDB_ROOT_PASSWORD"],
				firstNonEmpty(e["MONGO_INITDB_DATABASE"], "admin")
		},
	},
	{
		driver: DriverRedis,
		images: []string{"redis", "valkey/valkey", "redis/redis-stack-server"},
		port:   6379,
		read: func(e map[string]string) (string, string, string) {
			// Redis has no user in the common configuration, and the password
			// is only set when the image is told to require one.
			return "", firstNonEmpty(e["REDIS_PASSWORD"], e["REDIS_ARGS_PASSWORD"]), "0"
		},
	},
	{
		driver: DriverMSSQL,
		images: []string{"mcr.microsoft.com/mssql/server", "mssql/server"},
		port:   1433,
		read: func(e map[string]string) (string, string, string) {
			return "sa", firstNonEmpty(e["MSSQL_SA_PASSWORD"], e["SA_PASSWORD"]), "master"
		},
	},
	{
		driver: DriverClickHouse,
		images: []string{"clickhouse/clickhouse-server", "clickhouse", "yandex/clickhouse-server"},
		// The native protocol, not the 8123 HTTP one: that is the port this
		// package's driver speaks.
		port: 9000,
		read: func(e map[string]string) (string, string, string) {
			return firstNonEmpty(e["CLICKHOUSE_USER"], "default"), e["CLICKHOUSE_PASSWORD"],
				firstNonEmpty(e["CLICKHOUSE_DB"], "default")
		},
	},
	{
		driver: DriverOracle,
		images: []string{"gvenzl/oracle-free", "gvenzl/oracle-xe", "container-registry.oracle.com/database/free"},
		port:   1521,
		read: func(e map[string]string) (string, string, string) {
			// The image creates an application account when asked, which is
			// the one to prefer over SYSTEM for the same reason as MySQL.
			if u := e["APP_USER"]; u != "" {
				return u, e["APP_USER_PASSWORD"], firstNonEmpty(e["ORACLE_DATABASE"], "FREEPDB1")
			}
			return "system", e["ORACLE_PASSWORD"], firstNonEmpty(e["ORACLE_DATABASE"], "FREEPDB1")
		},
	},
}

// PublishedPort is one container port and where it is reachable on the host.
type PublishedPort struct {
	ContainerPort int
	HostIP        string
	HostPort      int
}

// Detect recognises a database server from what the container states about
// itself, returning nil when the image is not one it knows.
//
// The password is returned separately from the Candidate so a caller can hand
// the description to a browser and keep the secret. There is no path that puts
// them in the same value.
func Detect(container, image string, env map[string]string, ports []PublishedPort, ips []string) (*Candidate, string) {
	rule, ok := ruleFor(image)
	if !ok {
		return nil, ""
	}
	user, password, database := rule.read(env)
	c := &Candidate{
		Driver: rule.driver, Container: container, Image: image,
		User: user, Database: database, Source: SourceDocker,
	}
	// The official Mongo image creates its initial root user in admin,
	// even when MONGO_INITDB_DATABASE names another application database.
	if rule.driver == DriverMongo && env["MONGO_INITDB_ROOT_USERNAME"] != "" {
		c.AuthSource = "admin"
	}
	for _, p := range ports {
		if p.ContainerPort != rule.port || p.HostPort == 0 {
			continue
		}
		c.Host, c.Port = hostAddress(p.HostIP), p.HostPort
		break
	}
	// Nothing published is not the same as nothing reachable, and treating it
	// as such was wrong for the deployment this product is built around.
	//
	// The backend runs in the host's network namespace, and a Docker bridge
	// network is routable from there — the bridge interface belongs to the
	// host. So a database on a compose network with no published port, which
	// is how nearly every application ships its own Postgres, is reachable at
	// its container address on the engine's standard port. Refusing it meant
	// the commonest database on any server this runs on was the one database
	// it would not connect to, while `psql` from the same namespace worked.
	//
	// The published port is still preferred where there is one: it is stable
	// across a recreate, and a container address is not.
	if c.Port == 0 {
		if ip := firstRoutable(ips); ip != "" {
			c.Host, c.Port, c.ViaContainerNetwork = ip, rule.port, true
		}
	}
	if c.Port == 0 {
		// Genuinely nowhere to dial: no published port and no address of its
		// own, which is what a container sharing another's namespace looks
		// like. Worth saying rather than omitting, since it is a database the
		// operator can see running.
		c.Reason = "no published port and no container address — nothing here can be dialled"
	}
	return c, password
}

// firstRoutable picks the container address to dial.
//
// A container on several networks has several addresses and any of them
// reaches it, so the first usable one is as good as a choice between them.
// The empty string is what Docker reports for a container that has no address
// of its own, which is not an address and must not become "".
func firstRoutable(ips []string) string {
	for _, ip := range ips {
		if ip = strings.TrimSpace(ip); ip != "" {
			return ip
		}
	}
	return ""
}

// ruleFor matches the repository part of an image reference, ignoring the tag
// and any digest, as a path suffix — so "postgres:16", "library/postgres" and
// "docker.io/library/postgres@sha256:…" all match and "acme/postgres-backup"
// does not.
func ruleFor(image string) (engineRule, bool) {
	repo := image
	if i := strings.IndexByte(repo, '@'); i >= 0 {
		repo = repo[:i]
	}
	// A tag separator is a colon after the last slash; a colon before it is a
	// registry port.
	if i := strings.LastIndexByte(repo, ':'); i >= 0 && !strings.Contains(repo[i:], "/") {
		repo = repo[:i]
	}
	for _, rule := range engineRules {
		for _, want := range rule.images {
			if repo == want || strings.HasSuffix(repo, "/"+want) {
				return rule, true
			}
		}
	}
	return engineRule{}, false
}

// hostAddress turns Docker's binding address into one to dial. A container
// published to 0.0.0.0 is reachable on loopback, and loopback is what the
// dashboard should use: it is the same machine, and it keeps the connection
// off the network whatever the port is bound to.
func hostAddress(ip string) string {
	switch ip {
	case "", "0.0.0.0", "::", "[::]", "*":
		return "127.0.0.1"
	}
	// A bare IPv6 literal has to be bracketed before anything can join a port
	// to it: "fd00::1:5432" is not an address, it is a parse error waiting for
	// whichever driver reads it first.
	if strings.Contains(ip, ":") && !strings.HasPrefix(ip, "[") {
		return "[" + ip + "]"
	}
	return ip
}

// BuildDSN renders the connection string for a detected server. It is the one
// place the password is joined to the rest, and it runs on the server.
func BuildDSN(c Candidate, password string) string {
	host := c.Host + ":" + strconv.Itoa(c.Port)
	switch c.Driver {
	case DriverPostgres:
		u := url.URL{Scheme: "postgres", Host: host, Path: "/" + c.Database}
		u.User = userInfo(c.User, password)
		// sslmode=disable because this is a container on the same host reached
		// over loopback; requiring TLS there fails against every stock image.
		u.RawQuery = "sslmode=disable"
		return u.String()
	case DriverMySQL:
		// The MySQL driver takes its own format rather than a URL.
		return c.User + ":" + password + "@tcp(" + host + ")/" + c.Database
	case DriverMongo:
		u := url.URL{Scheme: "mongodb", Host: host, Path: "/" + c.Database}
		u.User = userInfo(c.User, password)
		if c.AuthSource != "" {
			u.RawQuery = url.Values{"authSource": {c.AuthSource}}.Encode()
		}
		return u.String()
	case DriverRedis:
		u := url.URL{Scheme: "redis", Host: host, Path: "/" + firstNonEmpty(c.Database, "0")}
		u.User = userInfo(c.User, password)
		return u.String()
	case DriverMSSQL:
		u := url.URL{Scheme: "sqlserver", Host: host}
		u.User = userInfo(c.User, password)
		u.RawQuery = "database=" + url.QueryEscape(c.Database)
		return u.String()
	case DriverClickHouse:
		u := url.URL{Scheme: "clickhouse", Host: host, Path: "/" + c.Database}
		u.User = userInfo(c.User, password)
		return u.String()
	case DriverOracle:
		u := url.URL{Scheme: "oracle", Host: host, Path: "/" + c.Database}
		u.User = userInfo(c.User, password)
		return u.String()
	}
	return ""
}

// userInfo keeps url.URL from rendering a "@" for a connection that has no
// credentials at all, which is the ordinary Redis case.
func userInfo(user, password string) *url.Userinfo {
	switch {
	case user == "" && password == "":
		return nil
	case password == "":
		return url.User(user)
	default:
		return url.UserPassword(user, password)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
