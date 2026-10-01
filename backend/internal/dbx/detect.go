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
	// SSLMode is the transport a Postgres-wire server insists on, where it is
	// not the plain connection every stock container on loopback takes: a
	// CockroachDB cluster that was not started insecure.
	SSLMode string `json:"-"`
}

// Connectable reports whether this candidate has everything a DSN needs.
func (c Candidate) Connectable() bool { return c.Reason == "" && c.Port > 0 }

// PublishedPort is one container port and where it is reachable on the host.
type PublishedPort struct {
	ContainerPort int
	HostIP        string
	HostPort      int
}

// Detect recognises a database server from what the container states about
// itself, returning nil when the image is not one it knows.
//
// It reads the image name and nothing else to decide what the container is,
// which is all a caller holding only the container listing can offer; the
// inventory in discover.go goes further, down to the environment and the
// command. Both read the same tables, so they cannot disagree about an image.
// An engine this dashboard has no driver for is not a candidate: a candidate
// is a connection that could be made.
//
// The password is returned separately from the Candidate so a caller can hand
// the description to a browser and keep the secret. There is no path that puts
// them in the same value.
func Detect(container, image string, env map[string]string, ports []PublishedPort, ips []string) (*Candidate, string) {
	rule, ok := imageRuleFor(image)
	if !ok {
		return nil, ""
	}
	p := productByID[rule.product]
	if p.driver == "" {
		return nil, ""
	}
	creds := readCredentials(rule.style, p, env, nil)
	c := &Candidate{
		Driver: p.driver, Container: container, Image: image,
		User: creds.user, Database: creds.database, Source: SourceDocker,
		AuthSource: creds.authSource, SSLMode: creds.sslMode,
	}
	port := enginePort(p, env, nil)
	for _, published := range ports {
		if published.ContainerPort != port || published.HostPort == 0 {
			continue
		}
		c.Host, c.Port = hostAddress(published.HostIP), published.HostPort
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
			c.Host, c.Port, c.ViaContainerNetwork = ip, port, true
		}
	}
	if c.Port == 0 {
		// Genuinely nowhere to dial: no published port and no address of its
		// own, which is what a container sharing another's namespace looks
		// like. Worth saying rather than omitting, since it is a database the
		// operator can see running.
		c.Reason = "no published port and no container address — nothing here can be dialled"
	}
	return c, creds.password
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
		u.RawQuery = "sslmode=" + firstNonEmpty(c.SSLMode, "disable")
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
