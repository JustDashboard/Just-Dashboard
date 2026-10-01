package dbx

import (
	"sort"
	"strconv"
	"strings"
)

// Recognising a database in a container, whatever the container is called.
//
// The image name is the best evidence and the easiest to lose. A tag that
// moved leaves the listing naming the image by its id; a private registry, a
// retag or a build of the operator's own leaves a name that says nothing. The
// server inside is the same in every case, and it still states itself: the
// stock images set variables nobody else sets, and the command they run is the
// server's own program. So the image is the first rung of a ladder rather
// than the whole test, and each instance records which rung it stood on.
//
// The last rung is a port and nothing else. That is a guess, it is labelled as
// one, and nothing is ever connected on the strength of it.

// ContainerFacts is what Docker says about one container. The first block
// comes with the listing; the second needs an inspect, and Inspected says
// whether it was made — an empty environment that was never read is not an
// environment with nothing in it.
type ContainerFacts struct {
	ID     string
	Name   string
	Image  string
	State  string
	Status string
	Health string
	Labels map[string]string
	Ports  []PublishedPort
	Mounts []ContainerMount
	// Command is the listing's one-line command, which is all there is to
	// read when the container was not inspected.
	Command string

	Inspected bool
	Env       map[string]string
	// Argv is the program the container runs and its arguments, entrypoint
	// first.
	Argv        []string
	NetworkMode string
	IPs         []string
}

// ContainerMount is one volume or bind mount of a container.
type ContainerMount struct {
	Type        string
	Name        string
	Source      string
	Destination string
}

const (
	labelComposeProject = "com.docker.compose.project"
	labelComposeService = "com.docker.compose.service"
	labelComposeNumber  = "com.docker.compose.container-number"
	labelEnvironmentID  = "io.just-dashboard.environment-id"
)

// ContainerKey is the stable identity of a container's instance.
//
// A compose service is named for its project and service rather than for the
// container, because the container's name is compose's to choose and its
// service is the operator's: the instance is the same one before the stack
// has ever been brought up and after it has been recreated.
func ContainerKey(name string, labels map[string]string) string {
	project, service := labels[labelComposeProject], labels[labelComposeService]
	if project == "" || service == "" {
		return "docker:" + name
	}
	key := "compose:" + project + "/" + service
	if n := labels[labelComposeNumber]; n != "" && n != "1" {
		// A scaled service is several servers.
		key += "#" + n
	}
	return key
}

// containerMatch is which rung recognised a container, and as what.
type containerMatch struct {
	product    *product
	variant    string
	style      credentialStyle
	confidence string
	evidence   string
}

// classifyContainer walks the ladder, strongest evidence first.
func classifyContainer(c ContainerFacts) (containerMatch, bool) {
	argv := c.Argv
	if len(argv) == 0 {
		argv = strings.Fields(c.Command)
	}
	if runsNonServer(argv) {
		// A database's image started as something else: a Redis image running
		// a sentinel is not a Redis to connect to.
		return containerMatch{}, false
	}
	if rule, ok := imageRuleFor(c.Image); ok {
		return containerMatch{
			product: productByID[rule.product], variant: rule.variant, style: rule.style,
			confidence: ConfidenceImage, evidence: "the image is " + imageRepo(c.Image),
		}, true
	}
	if p, name := productFromEnv(c.Env); p != nil {
		return containerMatch{
			product: p, style: styleFor(p.id, c.Env),
			confidence: ConfidenceFingerprint,
			evidence:   "its environment has " + name + ", which the " + p.label + " image sets",
		}, true
	}
	if p, word := productFromCommand(argv); p != nil {
		return containerMatch{
			product: p, style: styleFor(p.id, c.Env),
			confidence: ConfidenceCommand, evidence: "it runs " + word,
		}, true
	}
	for _, port := range c.Ports {
		if p := productForPort(port.ContainerPort); p != nil {
			return containerMatch{
				product: p, style: styleNone, confidence: ConfidencePort,
				evidence: "it exposes port " + strconv.Itoa(port.ContainerPort) + ", which is " + p.label +
					"'s — nothing else about it says so, so this is a guess",
			}, true
		}
	}
	return containerMatch{}, false
}

// credentials is what a container states about signing in to it.
type credentials struct {
	user, password, database string
	class                    string
	secretFile               string
	authSource               string
	sslMode                  string
	unverified               bool
	evidence                 string
}

// envOrFile reads a variable, or notes that the image was told to read it
// from a file instead: the documented `_FILE` form, which is how a compose
// secret reaches a database.
func envOrFile(env map[string]string, names ...string) (value, file, name string) {
	for _, n := range names {
		if v := env[n]; strings.TrimSpace(v) != "" {
			return v, "", n
		}
	}
	for _, n := range names {
		if v := strings.TrimSpace(env[n+"_FILE"]); v != "" {
			return "", v, n + "_FILE"
		}
	}
	return "", "", ""
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "yes", "true", "y", "on":
		return true
	}
	return false
}

// stated turns a password read by envOrFile into a credentials class.
func (c *credentials) stated(value, file, name string) {
	switch {
	case value != "":
		c.password, c.class, c.evidence = value, CredentialsEnv, "the password is stated in "+name
	case file != "":
		c.secretFile, c.class, c.evidence = file, CredentialsSecretFile, name+" names a file holding the password"
	}
}

// readCredentials reads the variables one publisher's image documents. Only
// what the image documents is looked at: nothing is inferred that the
// container did not state.
func readCredentials(style credentialStyle, p *product, env map[string]string, argv []string) credentials {
	c := credentials{class: CredentialsUnknown}
	switch style {
	case stylePostgres:
		c.user = firstNonEmpty(env["POSTGRES_USER"], "postgres")
		// POSTGRES_DB defaults to the user's own name, not to "postgres".
		c.database = firstNonEmpty(env["POSTGRES_DB"], c.user)
		c.class = CredentialsNeeded
		c.stated(envOrFile(env, "POSTGRES_PASSWORD"))
		if strings.EqualFold(strings.TrimSpace(env["POSTGRES_HOST_AUTH_METHOD"]), "trust") {
			c.password, c.secretFile = "", ""
			c.class, c.evidence = CredentialsOpen, "POSTGRES_HOST_AUTH_METHOD is trust, so it asks for no password"
		}
	case styleBitnamiPostgres:
		c.user = firstNonEmpty(env["POSTGRESQL_USERNAME"], env["POSTGRES_USER"], "postgres")
		c.database = firstNonEmpty(env["POSTGRESQL_DATABASE"], env["POSTGRES_DB"], "postgres")
		c.class = CredentialsNeeded
		c.stated(envOrFile(env, "POSTGRESQL_PASSWORD", "POSTGRES_PASSWORD"))
		if c.class == CredentialsNeeded && truthy(env["ALLOW_EMPTY_PASSWORD"]) {
			c.class, c.evidence = CredentialsOpen, "ALLOW_EMPTY_PASSWORD is set, so it asks for no password"
		}
	case styleMySQL:
		c.database = firstNonEmpty(env["MYSQL_DATABASE"], env["MARIADB_DATABASE"])
		c.class = CredentialsNeeded
		// The unprivileged account the image creates is preferred over root:
		// it is the one scoped to the database that was asked for, and
		// connecting a dashboard as root by default is a choice the operator
		// should make deliberately rather than inherit.
		if u := firstNonEmpty(env["MYSQL_USER"], env["MARIADB_USER"]); u != "" {
			c.user = u
			c.stated(envOrFile(env, "MYSQL_PASSWORD", "MARIADB_PASSWORD"))
			break
		}
		c.user = "root"
		c.stated(envOrFile(env, "MYSQL_ROOT_PASSWORD", "MARIADB_ROOT_PASSWORD"))
		if c.class == CredentialsNeeded && (truthy(env["MYSQL_ALLOW_EMPTY_PASSWORD"]) ||
			truthy(env["MARIADB_ALLOW_EMPTY_ROOT_PASSWORD"]) || truthy(env["MARIADB_ALLOW_EMPTY_PASSWORD"]) ||
			truthy(env["ALLOW_EMPTY_PASSWORD"])) {
			c.class, c.evidence = CredentialsOpen, "an empty root password is allowed, so it asks for none"
		}
	case styleMongo:
		c.database = firstNonEmpty(env["MONGO_INITDB_DATABASE"], "admin")
		c.user = env["MONGO_INITDB_ROOT_USERNAME"]
		if c.user == "" && strings.TrimSpace(env["MONGO_INITDB_ROOT_USERNAME_FILE"]) == "" {
			// No root user was asked for, so the image starts with access
			// control off.
			c.class, c.evidence = CredentialsOpen, "no root user is configured, so it asks for no password"
			break
		}
		// The official Mongo image creates its initial root user in admin,
		// even when MONGO_INITDB_DATABASE names another application database.
		c.authSource = "admin"
		c.class = CredentialsNeeded
		c.stated(envOrFile(env, "MONGO_INITDB_ROOT_PASSWORD"))
		if c.user == "" {
			// The name is in a file too; the operator has to say who.
			c.class, c.password, c.secretFile = CredentialsNeeded, "", ""
			c.evidence = "MONGO_INITDB_ROOT_USERNAME_FILE names a file holding the user"
		}
	case styleAtlasLocal:
		c.database = "admin"
		c.user = env["MONGODB_INITDB_ROOT_USERNAME"]
		if c.user == "" {
			c.class, c.evidence = CredentialsOpen, "no root user is configured, so it asks for no password"
			break
		}
		c.authSource = "admin"
		c.class = CredentialsNeeded
		c.stated(envOrFile(env, "MONGODB_INITDB_ROOT_PASSWORD"))
	case styleBitnamiMongo:
		c.database = firstNonEmpty(env["MONGODB_DATABASE"], "admin")
		c.class = CredentialsNeeded
		if u := env["MONGODB_USERNAME"]; u != "" && env["MONGODB_DATABASE"] != "" {
			// Bitnami's application account lives in its own database.
			c.user, c.authSource = u, env["MONGODB_DATABASE"]
			c.stated(envOrFile(env, "MONGODB_PASSWORD"))
			break
		}
		value, file, name := envOrFile(env, "MONGODB_ROOT_PASSWORD")
		if value == "" && file == "" {
			if truthy(env["ALLOW_EMPTY_PASSWORD"]) {
				c.class, c.evidence = CredentialsOpen, "ALLOW_EMPTY_PASSWORD is set, so it asks for no password"
			}
			break
		}
		c.user, c.authSource, c.database = firstNonEmpty(env["MONGODB_ROOT_USER"], "root"), "admin", "admin"
		c.stated(value, file, name)
	case styleRedis:
		c.database = "0"
		c.class, c.evidence = CredentialsOpen, "no password is configured"
		if pw := argValue(argv, "--requirepass"); pw != "" {
			c.password, c.class, c.evidence = pw, CredentialsArgs, "the password is stated by --requirepass in its command"
			break
		}
		if pw := argValue(strings.Fields(env["REDIS_ARGS"]), "--requirepass"); pw != "" {
			c.password, c.class, c.evidence = pw, CredentialsEnv, "the password is stated by --requirepass in REDIS_ARGS"
			break
		}
		value, file, name := envOrFile(env, "REDIS_PASSWORD", "VALKEY_PASSWORD", "KEYDB_PASSWORD", "DFLY_requirepass", "REDIS_ARGS_PASSWORD")
		if value == "" && file == "" {
			break
		}
		c.stated(value, file, name)
		// The official image reads none of these; only Bitnami's and the
		// servers this dashboard starts do. Elsewhere the variable may be an
		// intention the server never heard of.
		c.unverified = env["BITNAMI_APP_NAME"] == "" && name != "DFLY_requirepass"
	case styleMSSQL:
		c.user, c.database, c.class = "sa", "master", CredentialsNeeded
		c.stated(envOrFile(env, "MSSQL_SA_PASSWORD", "SA_PASSWORD"))
	case styleClickHouse:
		c.user = firstNonEmpty(env["CLICKHOUSE_USER"], "default")
		c.database = firstNonEmpty(env["CLICKHOUSE_DB"], "default")
		c.class, c.evidence = CredentialsOpen, "no password is configured for "+c.user
		c.stated(envOrFile(env, "CLICKHOUSE_PASSWORD"))
	case styleBitnamiClickHouse:
		c.user = firstNonEmpty(env["CLICKHOUSE_ADMIN_USER"], "default")
		c.database, c.class = "default", CredentialsNeeded
		c.stated(envOrFile(env, "CLICKHOUSE_ADMIN_PASSWORD"))
		if c.class == CredentialsNeeded && truthy(env["ALLOW_EMPTY_PASSWORD"]) {
			c.class, c.evidence = CredentialsOpen, "ALLOW_EMPTY_PASSWORD is set, so it asks for no password"
		}
	case styleOracleGvenzl:
		c.database, c.class = firstNonEmpty(env["ORACLE_DATABASE"], "FREEPDB1"), CredentialsNeeded
		// The image creates an application account when asked, which is the
		// one to prefer over SYSTEM for the same reason as MySQL.
		if u := env["APP_USER"]; u != "" {
			c.user = u
			c.stated(envOrFile(env, "APP_USER_PASSWORD"))
			break
		}
		c.user = "system"
		c.stated(envOrFile(env, "ORACLE_PASSWORD"))
	case styleOracleOfficial:
		c.user, c.database, c.class = "system", firstNonEmpty(env["ORACLE_PDB"], "FREEPDB1"), CredentialsNeeded
		c.stated(envOrFile(env, "ORACLE_PWD"))
	case styleCockroach:
		c.user, c.database, c.class = "root", "defaultdb", CredentialsNeeded
		// A secure cluster signs in over TLS; requiring it does not verify the
		// certificate, which a loopback connection has no use for.
		c.sslMode = "require"
		if hasArg(argv, "--insecure") {
			c.sslMode = ""
			c.class, c.evidence = CredentialsOpen, "it was started with --insecure, so it asks for no password"
		}
	default:
		if p != nil {
			c.user, c.database = p.user, p.database
			if p.open {
				c.class, c.evidence = CredentialsOpen, p.label+" ships accepting connections with no password"
			} else if p.driver != "" {
				c.class = CredentialsNeeded
			}
		}
	}
	return c
}

// argValue reads "--flag value" or "--flag=value" out of a command line.
func argValue(argv []string, flag string) string {
	for i, arg := range argv {
		if arg == flag && i+1 < len(argv) {
			return strings.Trim(argv[i+1], `"'`)
		}
		if rest, ok := strings.CutPrefix(arg, flag+"="); ok {
			return strings.Trim(rest, `"'`)
		}
	}
	return ""
}

func hasArg(argv []string, flag string) bool {
	for _, arg := range argv {
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			return true
		}
	}
	return false
}

func portOf(v string) int {
	v = strings.TrimSpace(v)
	// "--listen-addr=:26257" and "0.0.0.0:7700" carry the port after a colon.
	if i := strings.LastIndexByte(v, ':'); i >= 0 {
		v = v[i+1:]
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 || n > 65535 {
		return 0
	}
	return n
}

// enginePort is the port the server listens on inside its container: what it
// was told, where the container says, and the product's default otherwise.
//
// The default was assumed for everything, so a Postgres started with PGPORT or
// a Redis with --port was either reported unreachable or dialled on a port
// nothing listened on.
func enginePort(p *product, env map[string]string, argv []string) int {
	pick := func(values ...string) int {
		for _, v := range values {
			if n := portOf(v); n > 0 {
				return n
			}
		}
		return 0
	}
	n := 0
	switch p.engine {
	case "postgres":
		switch p.id {
		case "cockroachdb":
			n = pick(argValue(argv, "--sql-addr"), argValue(argv, "--listen-addr"))
		default:
			n = pick(argValue(argv, "-p"), argValue(argv, "--port"), settingValue(argv, "port"),
				env["PGPORT"], env["POSTGRESQL_PORT_NUMBER"])
		}
	case "mysql":
		n = pick(argValue(argv, "--port"), argValue(argv, "-P"), env["MYSQL_TCP_PORT"],
			env["MYSQL_PORT_NUMBER"], env["MARIADB_PORT_NUMBER"])
	case "mongodb":
		n = pick(argValue(argv, "--port"), env["MONGODB_PORT_NUMBER"], env["FERRETDB_LISTEN_ADDR"])
	case "redis":
		n = pick(argValue(argv, "--port"), argValue(strings.Fields(env["REDIS_ARGS"]), "--port"),
			env["REDIS_PORT_NUMBER"], env["VALKEY_PORT_NUMBER"], env["KEYDB_PORT_NUMBER"], env["DFLY_port"])
	case "sqlserver":
		n = pick(env["MSSQL_TCP_PORT"])
	case "clickhouse":
		n = pick(env["CLICKHOUSE_TCP_PORT"])
	case "memcached":
		n = pick(argValue(argv, "-p"), argValue(argv, "--port"))
	case "meilisearch":
		n = pick(env["MEILI_HTTP_ADDR"])
	}
	if n > 0 {
		return n
	}
	return p.port
}

// settingValue reads a Postgres `-c name=value` setting out of a command line.
func settingValue(argv []string, name string) string {
	for i, arg := range argv {
		if arg != "-c" || i+1 >= len(argv) {
			continue
		}
		if rest, ok := strings.CutPrefix(argv[i+1], name+"="); ok {
			return rest
		}
	}
	return ""
}

// dataPaths are where each engine's image keeps what it stores, so the volume
// mounted there can be named as the instance's data.
var dataPaths = []string{
	"/var/lib/postgresql", "/bitnami/postgresql", "/home/postgres/pgdata", "/var/lib/mysql", "/bitnami/mysql",
	"/bitnami/mariadb", "/config", "/data/db", "/data/configdb", "/bitnami/mongodb", "/data", "/bitnami/redis",
	"/bitnami/valkey", "/var/lib/clickhouse", "/bitnami/clickhouse", "/var/opt/mssql", "/opt/oracle/oradata",
	"/cockroach/cockroach-data", "/var/lib/cassandra", "/var/lib/scylla", "/var/lib/rabbitmq", "/opt/couchdb/data",
	"/var/lib/influxdb", "/var/lib/influxdb2", "/usr/share/elasticsearch/data", "/usr/share/opensearch/data",
	"/qdrant/storage", "/meili_data", "/var/lib/kafka/data", "/var/lib/redpanda/data", "/state", "/mnt/disk0",
}

func dataVolumes(mounts []ContainerMount) []DataVolume {
	out := []DataVolume{}
	for _, m := range mounts {
		for _, want := range dataPaths {
			if m.Destination == want || strings.HasPrefix(m.Destination, want+"/") {
				out = append(out, DataVolume{Type: m.Type, Name: m.Name, Source: m.Source, Destination: m.Destination})
				break
			}
		}
	}
	return out
}

// publishedEndpoints lists the host bindings of the engine's port, one per
// address family collapsed: Docker publishes 0.0.0.0 and :: as two bindings of
// one port, and they are one way in.
func publishedEndpoints(ports []PublishedPort, port int) []Endpoint {
	out := []Endpoint{}
	seen := map[string]bool{}
	for _, p := range ports {
		if p.ContainerPort != port || p.HostPort == 0 {
			continue
		}
		host := hostAddress(p.HostIP)
		id := AddressIdentity(host, p.HostPort)
		if seen[id] {
			// The second family of the same binding. The wider scope is the
			// one worth keeping.
			for i := range out {
				if AddressIdentity(out[i].Host, out[i].Port) == id && bindScope(p.HostIP) == ScopePublic {
					out[i].Scope = ScopePublic
				}
			}
			continue
		}
		seen[id] = true
		e := Endpoint{Kind: "tcp", Host: host, Port: p.HostPort, Scope: bindScope(p.HostIP)}
		if p.HostIP != "" && hostAddress(p.HostIP) != p.HostIP {
			e.Bind = p.HostIP
		}
		out = append(out, e)
	}
	// A binding on this machine's own loopback is the one to dial where there
	// is a choice: it keeps the connection off the network.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Host == "127.0.0.1" && out[j].Host != "127.0.0.1"
	})
	return out
}

// discoverContainers adds an instance for every container recognised as a
// database.
func discoverContainers(list []ContainerFacts, inv *Inventory) {
	byRef := map[string]*ContainerFacts{}
	for i := range list {
		byRef[list[i].Name] = &list[i]
		if list[i].ID != "" {
			byRef[list[i].ID] = &list[i]
		}
	}
	for i := range list {
		c := &list[i]
		match, ok := classifyContainer(*c)
		if !ok {
			continue
		}
		inst, access := containerInstance(c, match, byRef)
		inv.Instances = append(inv.Instances, inst)
		if access != nil {
			inv.access[inst.Key] = *access
		}
	}
}

// containerInstance describes one recognised container and, where it can be
// dialled, how.
func containerInstance(c *ContainerFacts, match containerMatch, byRef map[string]*ContainerFacts) (Instance, *Access) {
	p := match.product
	inst := Instance{
		Key: ContainerKey(c.Name, c.Labels), Kind: KindServer, Name: c.Name,
		Variant: match.variant, Source: SourceDocker, State: c.State,
		Confidence: match.confidence, Evidence: []string{match.evidence},
		Container: &ContainerRef{
			ID: c.ID, Name: c.Name, Image: c.Image,
			ComposeProject: c.Labels[labelComposeProject], ComposeService: c.Labels[labelComposeService],
			Health: c.Health, Status: c.Status, NetworkMode: c.NetworkMode,
			DataVolumes: dataVolumes(c.Mounts), EnvironmentID: c.Labels[labelEnvironmentID],
		},
	}
	describe(&inst, p)
	if inst.Container.ComposeProject != "" {
		inst.Source = SourceCompose
	}
	if inst.State == "" {
		inst.State = StateRunning
	}

	creds := credentials{class: CredentialsUnknown}
	port := p.port
	if c.Inspected {
		creds = readCredentials(match.style, p, c.Env, c.Argv)
		port = enginePort(p, c.Env, c.Argv)
		if p.versionEnv != "" {
			inst.Version = cleanVersion(c.Env[p.versionEnv])
		}
		if inst.Version == "" {
			inst.Version = cleanVersion(c.Env["APP_VERSION"])
		}
		if creds.evidence != "" {
			inst.Evidence = append(inst.Evidence, creds.evidence)
		}
	}
	if p.driver == "" {
		creds.class = CredentialsUnknown
	}
	if match.confidence == ConfidencePort {
		// A guess states nothing about signing in.
		creds = credentials{class: CredentialsUnknown, user: p.user, database: p.database}
		for _, published := range c.Ports {
			if productForPort(published.ContainerPort) == p {
				port = published.ContainerPort
				break
			}
		}
	}
	inst.User, inst.Database, inst.Credentials = creds.user, creds.database, creds.class

	if inst.State != StateRunning {
		inst.Reason = "the container is " + inst.State + " — start it to connect"
		return inst, nil
	}

	// A container that shares another's network namespace has no ports or
	// address of its own: Docker reports both on the container it joined.
	ports, ips := c.Ports, c.IPs
	if owner, ok := strings.CutPrefix(c.NetworkMode, "container:"); ok {
		if other := byRef[owner]; other != nil {
			ports, ips = other.Ports, other.IPs
		}
	}
	inst.Endpoints = publishedEndpoints(ports, port)
	viaNetwork := false
	if ip := firstRoutable(ips); ip != "" {
		// Nothing published is not the same as nothing reachable: the backend
		// runs in the host's network namespace, and a Docker bridge is
		// routable from there. The published port is still preferred where
		// there is one — it survives a recreate, and a container address does
		// not.
		inst.Endpoints = append(inst.Endpoints, Endpoint{Kind: "container", Host: ip, Port: port, Scope: ScopePrivate})
		viaNetwork = len(inst.Endpoints) == 1
	}
	if len(inst.Endpoints) == 0 {
		if c.NetworkMode == "host" {
			// Filled in from the host's socket table when the server is found
			// listening there; until then there is nothing Docker can say.
			inst.Reason = "it runs on the host's own network, and nothing was found listening for it there"
		} else {
			inst.Reason = "no published port and no container address — nothing here can be dialled"
		}
		if p.driver == "" {
			inst.Reason = ""
		}
		return inst, containerAccess(c, p, creds, "", 0, false)
	}
	inst.Endpoints[0].Primary = true
	primary := inst.Endpoints[0]
	if p.driver == "" {
		return inst, nil
	}
	return inst, containerAccess(c, p, creds, primary.Host, primary.Port, viaNetwork)
}

func containerAccess(c *ContainerFacts, p *product, creds credentials, host string, port int, viaNetwork bool) *Access {
	if p.driver == "" {
		return nil
	}
	return &Access{
		Candidate: Candidate{
			Driver: p.driver, Container: c.Name, Image: c.Image, Host: host, Port: port,
			User: creds.user, Database: creds.database, Source: SourceDocker,
			AuthSource: creds.authSource, SSLMode: creds.sslMode, ViaContainerNetwork: viaNetwork,
		},
		Password: creds.password, SecretFile: creds.secretFile, Unverified: creds.unverified,
	}
}

// DeclaredService is a service a compose file declares that has no container:
// a stack that is down, or one that was never brought up.
type DeclaredService struct {
	Project string
	Service string
	Image   string
}

// discoverDeclared lists the databases a compose file says there should be.
// Only the image is read — a file's environment is not the environment of a
// container that does not exist — so nothing is said about credentials.
func discoverDeclared(list []DeclaredService, inv *Inventory) {
	seen := map[string]bool{}
	for _, inst := range inv.Instances {
		seen[inst.Key] = true
	}
	for _, d := range list {
		rule, ok := imageRuleFor(d.Image)
		if !ok || d.Project == "" || d.Service == "" {
			continue
		}
		key := "compose:" + d.Project + "/" + d.Service
		if seen[key] {
			continue
		}
		seen[key] = true
		p := productByID[rule.product]
		inst := Instance{
			Key: key, Kind: KindServer, Name: d.Project + "/" + d.Service,
			Variant: rule.variant, Source: SourceCompose, State: StateDeclared,
			Confidence: ConfidenceImage, Credentials: CredentialsUnknown,
			Evidence: []string{"the compose file of " + d.Project + " declares it with the image " + imageRepo(d.Image)},
			Container: &ContainerRef{
				Name: d.Service, Image: d.Image, ComposeProject: d.Project, ComposeService: d.Service,
			},
			Reason: "declared in " + d.Project + "'s compose file and never created — bring the stack up to connect",
		}
		describe(&inst, p)
		inv.Instances = append(inv.Instances, inst)
	}
}
