package dbx

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The inventory is a claim about somebody's machine, and every wrong claim is
// either a database that is not listed or a thing listed as a database that is
// not one. These tests write the machine out by hand, because the classifier
// is a pure function and a test that needs a daemon to notice a regression is
// a test nobody runs.

func find(t *testing.T, inv Inventory, key string) *Instance {
	t.Helper()
	inst, ok := inv.Find(key)
	if !ok {
		keys := []string{}
		for _, i := range inv.Instances {
			keys = append(keys, i.Key)
		}
		t.Fatalf("no instance %q; have %v", key, keys)
	}
	return inst
}

func running(name, image string, env map[string]string, argv ...string) ContainerFacts {
	return ContainerFacts{
		ID: strings.Repeat("a", 52) + name + strings.Repeat("0", 12), Name: name, Image: image,
		State: "running", Labels: map[string]string{}, Inspected: true, Env: env, Argv: argv,
	}
}

func published(c ContainerFacts, containerPort, hostPort int) ContainerFacts {
	c.Ports = append(c.Ports, PublishedPort{ContainerPort: containerPort, HostIP: "127.0.0.1", HostPort: hostPort})
	return c
}

// The images an operator actually runs are not all the official short names,
// and the ones detection used to miss are named here so they stay found.
func TestImageTableCoversThePublishersPeopleUse(t *testing.T) {
	cases := []struct {
		image  string
		engine string
		driver Driver
		flavor string
	}{
		{"postgres:16-alpine", "postgres", DriverPostgres, "postgres"},
		{"bitnami/postgresql:16", "postgres", DriverPostgres, "postgres"},
		{"timescale/timescaledb-ha:pg16", "postgres", DriverPostgres, "timescaledb"},
		{"timescale/timescaledb:latest-pg16", "postgres", DriverPostgres, "timescaledb"},
		{"ghcr.io/cloudnative-pg/postgresql:16", "postgres", DriverPostgres, "postgres"},
		{"cockroachdb/cockroach:v24.1.0", "postgres", DriverPostgres, "cockroachdb"},
		{"percona/percona-server:8.0", "mysql", DriverMySQL, "percona"},
		{"mariadb:11", "mysql", DriverMySQL, "mariadb"},
		{"lscr.io/linuxserver/mariadb:latest", "mysql", DriverMySQL, "mariadb"},
		{"percona/percona-server-mongodb:7.0", "mongodb", DriverMongo, "mongodb"},
		{"bitnami/mongodb:7.0", "mongodb", DriverMongo, "mongodb"},
		{"redis/redis-stack:latest", "redis", DriverRedis, "redis"},
		{"redis/redis-stack-server:latest", "redis", DriverRedis, "redis"},
		{"eqalpha/keydb:latest", "redis", DriverRedis, "keydb"},
		{"docker.dragonflydb.io/dragonflydb/dragonfly:latest", "redis", DriverRedis, "dragonfly"},
		{"valkey/valkey:8", "redis", DriverRedis, "valkey"},
		{"bitnami/valkey:8.0", "redis", DriverRedis, "valkey"},
		{"mcr.microsoft.com/azure-sql-edge:latest", "sqlserver", DriverMSSQL, "azure-sql-edge"},
		{"bitnami/clickhouse:24", "clickhouse", DriverClickHouse, "clickhouse"},
		{"container-registry.oracle.com/database/express:21.3.0-xe", "oracle", DriverOracle, "oracle"},
		// Seen, with nothing here to open them.
		{"memcached:1", "memcached", "", ""},
		{"docker.elastic.co/elasticsearch/elasticsearch:8.15.0", "elasticsearch", "", ""},
		{"opensearchproject/opensearch:2", "opensearch", "", ""},
		{"quay.io/coreos/etcd:v3.5.0", "etcd", "", ""},
		{"cassandra:5", "cassandra", "", ""},
		{"scylladb/scylla:latest", "scylladb", "", ""},
		{"neo4j:5", "neo4j", "", ""},
		{"influxdb:2", "influxdb", "", ""},
		{"rabbitmq:4", "rabbitmq", "", ""},
		{"nats:2", "nats", "", ""},
		{"qdrant/qdrant:latest", "qdrant", "", ""},
		{"getmeili/meilisearch:latest", "meilisearch", "", ""},
		{"typesense/typesense:27.1", "typesense", "", ""},
		{"redpandadata/redpanda:latest", "kafka", "", ""},
	}
	for _, tc := range cases {
		inv := Discover(Facts{Containers: []ContainerFacts{running("c", tc.image, nil)}})
		if len(inv.Instances) != 1 {
			t.Errorf("%s: %d instances, want it recognised", tc.image, len(inv.Instances))
			continue
		}
		got := inv.Instances[0]
		if got.Engine != tc.engine || got.Driver != tc.driver || got.Flavor != tc.flavor {
			t.Errorf("%s: engine %q driver %q flavor %q, want %q %q %q",
				tc.image, got.Engine, got.Driver, got.Flavor, tc.engine, tc.driver, tc.flavor)
		}
		if got.Confidence != ConfidenceImage {
			t.Errorf("%s: confidence %q, want the image rung", tc.image, got.Confidence)
		}
		if got.Label == "" {
			t.Errorf("%s: no label", tc.image)
		}
	}
}

// A name that only resembles a database's is not one.
func TestImageTableLeavesLookalikesAlone(t *testing.T) {
	for _, image := range []string{
		"acme/postgres-backup:1", "mycorp/redis-exporter", "prometheuscommunity/postgres-exporter",
		"edoburu/pgbouncer", "nginx:alpine", "caddy:2-alpine", "oliver006/redis_exporter",
		"sha256:3c5c8892d184aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	} {
		c := running("c", image, map[string]string{"PATH": "/usr/bin"}, "/app")
		if inv := Discover(Facts{Containers: []ContainerFacts{c}}); len(inv.Instances) != 0 {
			t.Errorf("%s was taken for %s", image, inv.Instances[0].Label)
		}
	}
}

// The rung below the image name. These are the variables the stock images set
// themselves, read off the real images: a container is the same server
// whatever it has been tagged as since, and a tag that moved leaves the
// listing naming the image by an id that says nothing.
func TestARetaggedImageIsRecognisedByWhatItSets(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		argv    []string
		engine  string
		flavor  string
		version string
	}{
		{"pg", map[string]string{"PG_MAJOR": "16", "PG_VERSION": "16.15-1.pgdg12+2", "PGDATA": "/var/lib/postgresql/data"},
			[]string{"docker-entrypoint.sh", "-c", "shared_preload_libraries=pg_stat_statements"}, "postgres", "postgres", "16.15"},
		{"maria", map[string]string{"MARIADB_VERSION": "1:11.4.2+maria~ubu2404"}, []string{"docker-entrypoint.sh", "mariadbd"}, "mysql", "mariadb", "11.4.2"},
		{"my", map[string]string{"MYSQL_MAJOR": "8.4", "MYSQL_VERSION": "8.4.11-1.el9"}, []string{"docker-entrypoint.sh", "mysqld"}, "mysql", "mysql", "8.4.11"},
		{"kv", map[string]string{"REDIS_VERSION": "7.4.0"}, []string{"docker-entrypoint.sh", "redis-server"}, "redis", "redis", "7.4.0"},
		{"valkey", map[string]string{"VALKEY_VERSION": "8.0.1"}, []string{"tini", "--", "docker-entrypoint.sh", "valkey-server"}, "redis", "valkey", "8.0.1"},
		{"doc", map[string]string{"MONGO_MAJOR": "7.0", "MONGO_VERSION": "7.0.12"}, []string{"docker-entrypoint.sh", "mongod"}, "mongodb", "mongodb", "7.0.12"},
		{"ch", map[string]string{"CLICKHOUSE_CONFIG": "/etc/clickhouse-server/config.xml"}, []string{"/entrypoint.sh"}, "clickhouse", "clickhouse", ""},
		{"bitnami", map[string]string{"BITNAMI_APP_NAME": "postgresql", "APP_VERSION": "16.6.0"},
			[]string{"/opt/bitnami/scripts/postgresql/entrypoint.sh", "/opt/bitnami/scripts/postgresql/run.sh"}, "postgres", "postgres", "16.6.0"},
		{"percona", map[string]string{"PS_VERSION": "8.0.46-37.1", "MYSQL_SHELL_VERSION": "8.0.46-1"}, []string{"/docker-entrypoint.sh", "mysqld"}, "mysql", "percona", "8.0.46"},
		{"cache", map[string]string{"MEMCACHED_VERSION": "1.6.45"}, []string{"docker-entrypoint.sh", "memcached"}, "memcached", "", "1.6.45"},
	}
	for _, tc := range cases {
		c := running(tc.name, "jdcc/fixture-"+tc.name+":1", tc.env, tc.argv...)
		inv := Discover(Facts{Containers: []ContainerFacts{c}})
		if len(inv.Instances) != 1 {
			t.Errorf("%s: not recognised from its environment", tc.name)
			continue
		}
		got := inv.Instances[0]
		if got.Engine != tc.engine || got.Flavor != tc.flavor {
			t.Errorf("%s: engine %q flavor %q, want %q %q", tc.name, got.Engine, got.Flavor, tc.engine, tc.flavor)
		}
		if got.Confidence != ConfidenceFingerprint {
			t.Errorf("%s: confidence %q, want fingerprint", tc.name, got.Confidence)
		}
		if got.Version != tc.version {
			t.Errorf("%s: version %q, want %q", tc.name, got.Version, tc.version)
		}
		if len(got.Evidence) == 0 || got.Evidence[0] == "" {
			t.Errorf("%s: no evidence for the classification", tc.name)
		}
	}
}

// The command rung. Only the command position counts: a program that takes a
// database's name as an argument is not that database.
func TestTheCommandSaysWhatAContainerRuns(t *testing.T) {
	cases := []struct {
		argv   []string
		engine string
	}{
		{[]string{"docker-entrypoint.sh", "postgres"}, "postgres"},
		{[]string{"tini", "--", "docker-entrypoint.sh", "valkey-server", "--save", "60", "1"}, "redis"},
		{[]string{"tini", "-g", "--", "/startup/docker-entrypoint.sh", "neo4j"}, "neo4j"},
		{[]string{"python3", "/usr/local/bin/docker-entrypoint.py", "mongod"}, "mongodb"},
		{[]string{"tini", "--", "/bin/sh", "-c", "/bin/meilisearch"}, "meilisearch"},
		{[]string{"/cockroach/cockroach.sh", "start-single-node", "--insecure"}, "postgres"},
		{[]string{"/nats-server", "--config", "nats-server.conf"}, "nats"},
		{[]string{"/opt/typesense-server"}, "typesense"},
		{[]string{"gosu", "mysql", "mysqld", "--user=mysql"}, "mysql"},
		{[]string{"/usr/bin/tini", "--", "entrypoint.sh", "dragonfly", "--logtostderr"}, "redis"},
		{[]string{"/opt/bitnami/scripts/redis/entrypoint.sh", "/opt/bitnami/scripts/redis/run.sh"}, "redis"},
	}
	for _, tc := range cases {
		c := running("c", "mycorp/custom:1", map[string]string{"PATH": "/bin"}, tc.argv...)
		inv := Discover(Facts{Containers: []ContainerFacts{c}})
		if len(inv.Instances) != 1 || inv.Instances[0].Engine != tc.engine {
			t.Errorf("%v: not read as %s", tc.argv, tc.engine)
			continue
		}
		if inv.Instances[0].Confidence != ConfidenceCommand {
			t.Errorf("%v: confidence %q, want command", tc.argv, inv.Instances[0].Confidence)
		}
	}
	for _, argv := range [][]string{
		{"./app", "migrate", "postgres"},
		{"wait-for", "postgres", "--", "node", "server.js"},
		{"node", "server.js", "--cache", "redis-server"},
		{"postgres_exporter"},
		{"redis-sentinel", "/etc/redis/sentinel.conf"},
		{"/bin/pgbouncer", "/etc/pgbouncer/pgbouncer.ini"},
	} {
		c := running("c", "mycorp/custom:1", map[string]string{"PATH": "/bin"}, argv...)
		if inv := Discover(Facts{Containers: []ContainerFacts{c}}); len(inv.Instances) != 0 {
			t.Errorf("%v was taken for %s", argv, inv.Instances[0].Label)
		}
	}
}

// A port alone is a guess: it is listed as one, and it states nothing about
// how to sign in.
func TestAPortAloneIsAGuessAndSaysSo(t *testing.T) {
	c := published(running("mystery", "mycorp/custom:1", map[string]string{"PATH": "/bin"}, "/start"), 5432, 15432)
	inv := Discover(Facts{Containers: []ContainerFacts{c}})
	got := find(t, inv, "docker:mystery")
	if got.Confidence != ConfidencePort || got.Engine != "postgres" {
		t.Fatalf("confidence %q engine %q", got.Confidence, got.Engine)
	}
	if got.Credentials != CredentialsUnknown {
		t.Errorf("credentials = %q, a guess knows nothing about signing in", got.Credentials)
	}
	if !strings.Contains(got.Evidence[0], "guess") {
		t.Errorf("evidence %q does not say it is a guess", got.Evidence[0])
	}
	// And the image of a database started as something that is not one.
	sentinel := published(running("sentinel", "redis:7-alpine", map[string]string{"REDIS_VERSION": "7.4.0"},
		"docker-entrypoint.sh", "sh", "-c", `printf "port 26379\n" > /tmp/s.conf && exec redis-sentinel /tmp/s.conf`), 6379, 0)
	if inv := Discover(Facts{Containers: []ContainerFacts{sentinel}}); len(inv.Instances) != 0 {
		t.Errorf("a sentinel was listed as %s", inv.Instances[0].Label)
	}
	// 9000 and 8080 are everybody's port.
	other := published(running("app", "mycorp/custom:1", nil, "/start"), 9000, 9000)
	if inv := Discover(Facts{Containers: []ContainerFacts{other}}); len(inv.Instances) != 0 {
		t.Errorf("port 9000 alone was taken for %s", inv.Instances[0].Label)
	}
}

// The port a server listens on inside its container is what it was told, not
// what its engine defaults to.
func TestTheInContainerPortIsReadFromWhatTheContainerSays(t *testing.T) {
	cases := []struct {
		name  string
		image string
		env   map[string]string
		argv  []string
		port  int
	}{
		{"PGPORT", "postgres:16", map[string]string{"PGPORT": "6543"}, []string{"docker-entrypoint.sh", "postgres"}, 6543},
		{"postgres -p", "postgres:16", nil, []string{"docker-entrypoint.sh", "postgres", "-p", "5433"}, 5433},
		{"postgres -c port", "postgres:16", nil, []string{"docker-entrypoint.sh", "postgres", "-c", "port=5444"}, 5444},
		{"redis --port", "redis:7", nil, []string{"docker-entrypoint.sh", "redis-server", "--port", "6380"}, 6380},
		{"mysqld --port=", "mysql:8", nil, []string{"docker-entrypoint.sh", "mysqld", "--port=3307"}, 3307},
		{"mongod --port", "mongo:7", nil, []string{"docker-entrypoint.sh", "mongod", "--port", "27018"}, 27018},
		{"bitnami port number", "bitnami/postgresql:16", map[string]string{"POSTGRESQL_PORT_NUMBER": "5440"}, nil, 5440},
		{"cockroach sql addr", "cockroachdb/cockroach:latest", nil, []string{"/cockroach/cockroach.sh", "start-single-node", "--insecure", "--sql-addr=:26300"}, 26300},
		{"the default", "postgres:16", nil, []string{"docker-entrypoint.sh", "postgres"}, 5432},
	}
	for _, tc := range cases {
		c := running("db", tc.image, tc.env, tc.argv...)
		c.Ports = []PublishedPort{
			{ContainerPort: tc.port, HostIP: "127.0.0.1", HostPort: 40000},
			{ContainerPort: 9999, HostIP: "127.0.0.1", HostPort: 40001},
		}
		inv := Discover(Facts{Containers: []ContainerFacts{c}})
		got := find(t, inv, "docker:db")
		if len(got.Endpoints) != 1 || got.Endpoints[0].Port != 40000 || !got.Endpoints[0].Primary {
			t.Errorf("%s: endpoints %+v, want the binding of container port %d", tc.name, got.Endpoints, tc.port)
		}
	}
	// Published on two addresses: loopback is the one dialled.
	both := running("db", "postgres:16", map[string]string{"POSTGRES_PASSWORD": "pw"})
	both.Ports = []PublishedPort{
		{ContainerPort: 5432, HostIP: "10.0.0.5", HostPort: 5432},
		{ContainerPort: 5432, HostIP: "127.0.0.1", HostPort: 15432},
	}
	inv := Discover(Facts{Containers: []ContainerFacts{both}})
	if got := find(t, inv, "docker:db"); len(got.Endpoints) != 2 || got.Endpoints[0].Port != 15432 || !got.Endpoints[0].Primary ||
		got.Endpoints[1].Scope != ScopePrivate {
		t.Errorf("endpoints = %+v, want the loopback binding first", got.Endpoints)
	}
	// With nothing published the container's own address is dialled on that port.
	c := running("db", "postgres:16", map[string]string{"PGPORT": "6543", "POSTGRES_PASSWORD": "pw"})
	c.IPs = []string{"172.18.0.4"}
	inv = Discover(Facts{Containers: []ContainerFacts{c}})
	access, _ := inv.Access("docker:db")
	if access.Candidate.Host != "172.18.0.4" || access.Candidate.Port != 6543 || !access.Candidate.ViaContainerNetwork {
		t.Errorf("candidate = %+v, want the container address on 6543", access.Candidate)
	}
}

// How a container's credentials are known decides what the operator is asked
// for. Each class here is one a real deployment uses.
func TestCredentialClasses(t *testing.T) {
	cases := []struct {
		name     string
		image    string
		env      map[string]string
		argv     []string
		class    string
		user     string
		password string
		file     string
	}{
		{"stated in the environment", "postgres:16", map[string]string{"POSTGRES_USER": "app", "POSTGRES_PASSWORD": "hunter2"}, nil, CredentialsEnv, "app", "hunter2", ""},
		{"a compose secret", "postgres:16", map[string]string{"POSTGRES_PASSWORD_FILE": "/run/secrets/pg"}, nil, CredentialsSecretFile, "postgres", "", "/run/secrets/pg"},
		{"trust", "postgres:16", map[string]string{"POSTGRES_HOST_AUTH_METHOD": "trust"}, nil, CredentialsOpen, "postgres", "", ""},
		{"nothing stated", "postgres:16", map[string]string{}, nil, CredentialsNeeded, "postgres", "", ""},
		{"bitnami names", "bitnami/postgresql:16", map[string]string{"POSTGRESQL_USERNAME": "app", "POSTGRESQL_PASSWORD": "hunter2", "POSTGRESQL_DATABASE": "shop"}, nil, CredentialsEnv, "app", "hunter2", ""},
		{"bitnami empty allowed", "bitnami/postgresql:16", map[string]string{"ALLOW_EMPTY_PASSWORD": "yes"}, nil, CredentialsOpen, "postgres", "", ""},
		{"mysql root secret file", "mysql:8", map[string]string{"MYSQL_ROOT_PASSWORD_FILE": "/run/secrets/root"}, nil, CredentialsSecretFile, "root", "", "/run/secrets/root"},
		{"mysql empty root", "mysql:8", map[string]string{"MYSQL_ALLOW_EMPTY_PASSWORD": "1"}, nil, CredentialsOpen, "root", "", ""},
		{"mysql random root", "mysql:8", map[string]string{"MYSQL_RANDOM_ROOT_PASSWORD": "yes"}, nil, CredentialsNeeded, "root", "", ""},
		{"mongo with no root user", "mongo:7", map[string]string{}, nil, CredentialsOpen, "", "", ""},
		{"mongo root", "mongo:7", map[string]string{"MONGO_INITDB_ROOT_USERNAME": "root", "MONGO_INITDB_ROOT_PASSWORD": "hunter2"}, nil, CredentialsEnv, "root", "hunter2", ""},
		{"bitnami mongo root", "bitnami/mongodb:7.0", map[string]string{"MONGODB_ROOT_PASSWORD": "hunter2"}, nil, CredentialsEnv, "root", "hunter2", ""},
		{"redis with nothing", "redis:7", map[string]string{}, []string{"docker-entrypoint.sh", "redis-server"}, CredentialsOpen, "", "", ""},
		{"requirepass in the command", "redis:7", map[string]string{}, []string{"docker-entrypoint.sh", "redis-server", "--requirepass", "hunter2"}, CredentialsArgs, "", "hunter2", ""},
		{"requirepass= in the command", "valkey/valkey:8", map[string]string{}, []string{"valkey-server", "--requirepass=hunter2"}, CredentialsArgs, "", "hunter2", ""},
		{"redis-stack REDIS_ARGS", "redis/redis-stack-server:latest", map[string]string{"REDIS_ARGS": "--save 60 1 --requirepass hunter2"}, nil, CredentialsEnv, "", "hunter2", ""},
		{"bitnami redis", "bitnami/redis:7.4", map[string]string{"REDIS_PASSWORD": "hunter2", "BITNAMI_APP_NAME": "redis"}, nil, CredentialsEnv, "", "hunter2", ""},
		{"sql server", "mcr.microsoft.com/mssql/server:2022-latest", map[string]string{"MSSQL_SA_PASSWORD": "hunter2"}, nil, CredentialsEnv, "sa", "hunter2", ""},
		{"bitnami clickhouse", "bitnami/clickhouse:24", map[string]string{"CLICKHOUSE_ADMIN_USER": "admin", "CLICKHOUSE_ADMIN_PASSWORD": "hunter2"}, nil, CredentialsEnv, "admin", "hunter2", ""},
		{"oracle official", "container-registry.oracle.com/database/free:latest", map[string]string{"ORACLE_PWD": "hunter2"}, nil, CredentialsEnv, "system", "hunter2", ""},
		{"cockroach insecure", "cockroachdb/cockroach:latest", nil, []string{"/cockroach/cockroach.sh", "start-single-node", "--insecure"}, CredentialsOpen, "root", "", ""},
		{"cockroach secure", "cockroachdb/cockroach:latest", nil, []string{"/cockroach/cockroach.sh", "start-single-node", "--certs-dir=/certs"}, CredentialsNeeded, "root", "", ""},
		{"no driver", "memcached:1", nil, nil, CredentialsUnknown, "", "", ""},
	}
	for _, tc := range cases {
		c := published(running("db", tc.image, tc.env, tc.argv...), 0, 0)
		c.IPs = []string{"172.18.0.9"}
		inv := Discover(Facts{Containers: []ContainerFacts{c}})
		got := find(t, inv, "docker:db")
		if got.Credentials != tc.class {
			t.Errorf("%s: class %q, want %q", tc.name, got.Credentials, tc.class)
		}
		if got.User != tc.user {
			t.Errorf("%s: user %q, want %q", tc.name, got.User, tc.user)
		}
		access, _ := inv.Access("docker:db")
		if access.Password != tc.password || access.SecretFile != tc.file {
			t.Errorf("%s: password %q file %q, want %q %q", tc.name, access.Password, access.SecretFile, tc.password, tc.file)
		}
		blob, err := json.Marshal(inv.Instances)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(blob), "hunter2") {
			t.Errorf("%s: the password reached the serialised inventory: %s", tc.name, blob)
		}
	}
}

// The official Redis image reads no REDIS_PASSWORD. A password from that
// variable may be an intention the server never heard of, and the caller is
// told so it can try the open server once the password is refused.
func TestAnUnreadRedisPasswordIsMarkedUnverified(t *testing.T) {
	c := published(running("kv", "redis:7", map[string]string{"REDIS_PASSWORD": "pw"}, "docker-entrypoint.sh", "redis-server"), 6379, 6379)
	inv := Discover(Facts{Containers: []ContainerFacts{c}})
	access, _ := inv.Access("docker:kv")
	if access.Password != "pw" || !access.Unverified {
		t.Fatalf("access = %+v, want the password marked unverified", access)
	}
	stated := published(running("kv", "redis:7", nil, "docker-entrypoint.sh", "redis-server", "--requirepass", "pw"), 6379, 6379)
	inv = Discover(Facts{Containers: []ContainerFacts{stated}})
	if access, _ := inv.Access("docker:kv"); access.Unverified {
		t.Error("a password the server was started with is not in doubt")
	}
}

// A stopped container is a database the operator runs. It is listed, with its
// state and why it cannot be connected, and nothing about it can be dialled.
func TestStoppedContainersAreListedAndNotConnectable(t *testing.T) {
	c := running("old-db", "postgres:16", map[string]string{"POSTGRES_PASSWORD": "pw"})
	c.State, c.Status = "exited", "Exited (0) 3 weeks ago"
	inv := Discover(Facts{Containers: []ContainerFacts{c}})
	got := find(t, inv, "docker:old-db")
	if got.State != "exited" || got.Connectable {
		t.Fatalf("state %q connectable %v", got.State, got.Connectable)
	}
	if !strings.Contains(got.Reason, "exited") {
		t.Errorf("reason %q does not say the container is stopped", got.Reason)
	}
	if _, ok := inv.Access("docker:old-db"); ok {
		t.Error("a stopped container has nothing to sign in to")
	}
	if len(got.Endpoints) != 0 {
		t.Errorf("endpoints = %+v", got.Endpoints)
	}
}

// A container on the host's own network has no published port and no address
// of its own, and its server shows up in the host's socket table looking like
// a native one. It is one server, and it is the container.
func TestAHostNetworkContainerIsJoinedToItsSocket(t *testing.T) {
	c := running("hostdb", "postgres:16", map[string]string{"POSTGRES_PASSWORD": "pw"})
	c.ID = "0123456789abcdef0123456789abcdef"
	c.NetworkMode = "host"
	listeners := []HostListener{
		{Protocol: "tcp", Address: "0.0.0.0", Port: 5432, Process: "postgres", PID: 4242, Manager: "container", ManagerName: "0123456789ab"},
		{Protocol: "tcp", Address: "::", Port: 5432, Process: "postgres", PID: 4242, Manager: "container", ManagerName: "0123456789ab"},
	}
	inv := Discover(Facts{Containers: []ContainerFacts{c}, Listeners: listeners})
	if len(inv.Instances) != 1 {
		t.Fatalf("%d instances, want the one server", len(inv.Instances))
	}
	got := inv.Instances[0]
	if got.Source != SourceDocker || got.Key != "docker:hostdb" {
		t.Fatalf("source %q key %q", got.Source, got.Key)
	}
	if !got.Connectable || len(got.Endpoints) != 1 || got.Endpoints[0].Port != 5432 || got.Endpoints[0].Scope != ScopePublic {
		t.Fatalf("connectable %v endpoints %+v reason %q", got.Connectable, got.Endpoints, got.Reason)
	}
	access, ok := inv.Access("docker:hostdb")
	if !ok || access.Candidate.Host != "127.0.0.1" || access.Candidate.Port != 5432 || access.Password != "pw" {
		t.Errorf("access = %+v", access)
	}

	// Without the socket it says why it cannot be dialled.
	inv = Discover(Facts{Containers: []ContainerFacts{c}})
	if got := find(t, inv, "docker:hostdb"); got.Connectable || !strings.Contains(got.Reason, "host's own network") {
		t.Errorf("connectable %v reason %q", got.Connectable, got.Reason)
	}
}

// A container whose image, environment and command say nothing is still a
// database when a server process inside it holds a socket on the host.
func TestAProcessInAnUnrecognisedContainerNamesIt(t *testing.T) {
	c := running("opaque", "mycorp/all-in-one:3", map[string]string{"PATH": "/bin"}, "/init")
	c.ID = "fedcba9876543210fedcba9876543210"
	c.NetworkMode = "host"
	inv := Discover(Facts{Containers: []ContainerFacts{c}, Listeners: []HostListener{
		{Protocol: "tcp", Address: "127.0.0.1", Port: 6379, Process: "redis-server", PID: 77, Manager: "container", ManagerName: "fedcba987654"},
	}})
	got := find(t, inv, "docker:opaque")
	if got.Engine != "redis" || got.Confidence != ConfidenceProcess || !got.Connectable {
		t.Errorf("engine %q confidence %q connectable %v", got.Engine, got.Confidence, got.Connectable)
	}
}

// A service a compose file declares and nobody has brought up exists only on
// disk. Docker cannot list it, and it is still a database this server is
// meant to run.
func TestComposeDeclaredServicesAreListed(t *testing.T) {
	c := published(running("shop-cache-1", "redis:7", nil), 6379, 6379)
	c.Labels = map[string]string{labelComposeProject: "shop", labelComposeService: "cache"}
	inv := Discover(Facts{
		Containers: []ContainerFacts{c},
		Declared: []DeclaredService{
			{Project: "shop", Service: "db", Image: "postgres:16"},
			{Project: "shop", Service: "cache", Image: "redis:7"},
			{Project: "shop", Service: "web", Image: "nginx:alpine"},
		},
	})
	if len(inv.Instances) != 2 {
		t.Fatalf("%d instances, want the running cache and the declared db", len(inv.Instances))
	}
	db := find(t, inv, "compose:shop/db")
	if db.State != StateDeclared || db.Source != SourceCompose || db.Connectable {
		t.Errorf("state %q source %q connectable %v", db.State, db.Source, db.Connectable)
	}
	if !strings.Contains(db.Reason, "bring the stack up") {
		t.Errorf("reason %q does not name the fix", db.Reason)
	}
	if cache := find(t, inv, "compose:shop/cache"); cache.State != "running" || cache.Source != SourceCompose {
		t.Errorf("the created service was replaced by its declaration: %+v", cache)
	}
}

func TestContainerKeysSurviveARecreate(t *testing.T) {
	if got := ContainerKey("jd-postgres", nil); got != "docker:jd-postgres" {
		t.Errorf("key = %q", got)
	}
	labels := map[string]string{labelComposeProject: "shop", labelComposeService: "db", labelComposeNumber: "1"}
	if got := ContainerKey("shop-db-1", labels); got != "compose:shop/db" {
		t.Errorf("key = %q", got)
	}
	labels[labelComposeNumber] = "2"
	if got := ContainerKey("shop-db-2", labels); got != "compose:shop/db#2" {
		t.Errorf("a second replica is a second server: %q", got)
	}
	for key, want := range map[string]bool{
		"docker:jd-postgres": true, "compose:shop/db": true, "host:postgresql@17-main.service": true,
		"host:postgres:5438": true, "file:/srv/app/data.db": true, "data:/var/lib/docker/volumes/x/_data": true,
		"": false, "docker:": false, "volume:x": false, "docker:a\nb": false, "jd-postgres": false,
	} {
		if got := ValidInstanceKey(key); got != want {
			t.Errorf("ValidInstanceKey(%q) = %v", key, got)
		}
	}
}

// One server process is one instance, however many sockets it holds.
func TestHostSocketsAreGroupedByProcess(t *testing.T) {
	inv := Discover(Facts{Listeners: []HostListener{
		// Both loopback families: two sockets, one way in.
		{Protocol: "tcp", Address: "127.0.0.1", Port: 6379, Process: "redis-server", PID: 10},
		{Protocol: "tcp", Address: "::1", Port: 6379, Process: "redis-server", PID: 10},
		// ClickHouse holds five sockets and its driver speaks to one.
		{Protocol: "tcp", Address: "127.0.0.1", Port: 8123, Process: "clickhouse-serv", PID: 20},
		{Protocol: "tcp", Address: "127.0.0.1", Port: 9004, Process: "clickhouse-serv", PID: 20},
		{Protocol: "tcp", Address: "127.0.0.1", Port: 9000, Process: "clickhouse-serv", PID: 20},
		{Protocol: "tcp", Address: "127.0.0.1", Port: 9009, Process: "clickhouse-serv", PID: 20},
		// MySQL's X protocol is not the port to dial.
		{Protocol: "tcp", Address: "0.0.0.0", Port: 33060, Process: "mysqld", PID: 30},
		{Protocol: "tcp", Address: "0.0.0.0", Port: 3307, Process: "mysqld", PID: 30},
	}})
	if len(inv.Instances) != 3 {
		t.Fatalf("%d instances, want three servers", len(inv.Instances))
	}
	redis := find(t, inv, "host:redis:6379")
	if len(redis.Endpoints) != 1 || redis.Endpoints[0].Host != "127.0.0.1" {
		t.Errorf("redis endpoints = %+v, want the two loopback families as one", redis.Endpoints)
	}
	if redis.Credentials != CredentialsOpen || !redis.Connectable {
		t.Errorf("redis credentials %q connectable %v", redis.Credentials, redis.Connectable)
	}
	ch := find(t, inv, "host:clickhouse:9000")
	if len(ch.Endpoints) != 4 {
		t.Errorf("clickhouse endpoints = %+v", ch.Endpoints)
	}
	for _, e := range ch.Endpoints {
		if e.Primary != (e.Port == 9000) {
			t.Errorf("clickhouse endpoint %d primary=%v", e.Port, e.Primary)
		}
	}
	mysql := find(t, inv, "host:mysql:3307")
	access, _ := inv.Access("host:mysql:3307")
	if access.Candidate.Port != 3307 || access.Candidate.Host != "127.0.0.1" || !access.Candidate.NeedsCredentials {
		t.Errorf("mysql candidate = %+v", access.Candidate)
	}
	if mysql.Endpoints[0].Scope != ScopePublic || mysql.Endpoints[0].Bind != "0.0.0.0" {
		t.Errorf("a server on every interface must say so: %+v", mysql.Endpoints[0])
	}
}

// A key names one instance. Two servers of one engine on the same port of two
// addresses are two servers.
func TestHostKeysAreNeverShared(t *testing.T) {
	inv := Discover(Facts{Listeners: []HostListener{
		{Protocol: "tcp", Address: "10.0.0.1", Port: 5432, Process: "postgres", PID: 11},
		{Protocol: "tcp", Address: "10.0.0.2", Port: 5432, Process: "postgres", PID: 12},
	}})
	if len(inv.Instances) != 2 || inv.Instances[0].Key == inv.Instances[1].Key {
		t.Fatalf("instances = %+v", inv.Instances)
	}
	for _, inst := range inv.Instances {
		access, ok := inv.Access(inst.Key)
		if !ok || access.Candidate.Host != inst.Endpoints[0].Host {
			t.Errorf("%s: access %+v does not dial its own endpoint %+v", inst.Key, access.Candidate, inst.Endpoints[0])
		}
	}
}

// The process name is matched exactly. A prefix match offered every exporter
// and pooler as a database server that wanted a password.
func TestProgramsNamedAfterADatabaseAreNotServers(t *testing.T) {
	for _, name := range []string{
		"postgres_exporter", "postgres_export", "postgrest", "mysqld_exporter", "mongodb_exporter",
		"mongodb_exporte", "clickhouse-keeper", "clickhouse-keep", "redis-sentinel", "redis_exporter",
		"pgbouncer", "docker-proxy", "sshd", "",
	} {
		l := HostListener{Protocol: "tcp", Address: "127.0.0.1", Port: 9187, Process: name, PID: 5}
		if inv := Discover(Facts{Listeners: []HostListener{l}}); len(inv.Instances) != 0 {
			t.Errorf("%q was taken for %s", name, inv.Instances[0].Label)
		}
		if got := DetectHost(l); got != nil {
			t.Errorf("DetectHost took %q for %s", name, got.Driver)
		}
	}
	// The one binary, run as something other than the server.
	for _, l := range []HostListener{
		{Protocol: "tcp", Address: "127.0.0.1", Port: 9181, Process: "clickhouse", Cmdline: "clickhouse keeper --config /etc/keeper.xml", PID: 6},
		{Protocol: "tcp", Address: "127.0.0.1", Port: 26379, Process: "redis-server", Cmdline: "redis-server *:26379 [sentinel]", PID: 7},
	} {
		if inv := Discover(Facts{Listeners: []HostListener{l}}); len(inv.Instances) != 0 {
			t.Errorf("%q was taken for a server", l.Cmdline)
		}
	}
	// And a side door is not a candidate for the legacy per-socket reading.
	if got := DetectHost(HostListener{Protocol: "tcp", Address: "127.0.0.1", Port: 33060, Process: "mysqld"}); got != nil {
		t.Error("MySQL's X protocol port was offered as a way in")
	}
}

// A server whose process is only a runtime is named by its command line, and
// listed although nothing here can open it.
func TestRuntimeHostedServersAreSeen(t *testing.T) {
	inv := Discover(Facts{Listeners: []HostListener{
		{Protocol: "tcp", Address: "127.0.0.1", Port: 9200, Process: "java", PID: 40, Manager: "systemd", ManagerName: "elasticsearch.service",
			Cmdline: "/usr/share/elasticsearch/jdk/bin/java -Des.path.home=/usr/share/elasticsearch org.elasticsearch.bootstrap.Elasticsearch"},
		{Protocol: "tcp", Address: "127.0.0.1", Port: 9300, Process: "java", PID: 40, Manager: "systemd", ManagerName: "elasticsearch.service",
			Cmdline: "/usr/share/elasticsearch/jdk/bin/java -Des.path.home=/usr/share/elasticsearch org.elasticsearch.bootstrap.Elasticsearch"},
		{Protocol: "tcp", Address: "127.0.0.1", Port: 8080, Process: "java", PID: 41, Cmdline: "java -jar /opt/app/app.jar"},
		{Protocol: "tcp", Address: "0.0.0.0", Port: 11211, Process: "memcached", PID: 42},
	}})
	if len(inv.Instances) != 2 {
		t.Fatalf("%d instances, want elasticsearch and memcached", len(inv.Instances))
	}
	es := find(t, inv, "host:elasticsearch.service")
	if es.Engine != "elasticsearch" || es.Driver != "" || es.Connectable {
		t.Errorf("engine %q driver %q connectable %v", es.Engine, es.Driver, es.Connectable)
	}
	if !strings.Contains(es.Reason, "no driver") {
		t.Errorf("reason %q", es.Reason)
	}
	if mc := find(t, inv, "host:memcached:11211"); mc.Flavor != "" || mc.Credentials != CredentialsUnknown {
		t.Errorf("memcached flavor %q credentials %q", mc.Flavor, mc.Credentials)
	}
}

// This server's own shape: a Debian PostgreSQL 17 cluster on a port that is
// not the default, with its unix socket and its unit.
func TestANativePostgresClusterIsOneInstance(t *testing.T) {
	cmdline := "/usr/lib/postgresql/17/bin/postgres -D /var/lib/postgresql/17/main -c config_file=/etc/postgresql/17/main/postgresql.conf"
	inv := Discover(Facts{
		Listeners: []HostListener{{
			Protocol: "tcp", Address: "127.0.0.1", Port: 5438, Process: "postgres", User: "postgres", PID: 900,
			Cmdline: cmdline, Manager: "systemd", ManagerName: "postgresql@17-main.service",
		}},
		Sockets: []UnixSocket{{
			Path: "/var/run/postgresql/.s.PGSQL.5438", PID: 900, Process: "postgres", Cmdline: cmdline,
			Manager: "systemd", ManagerName: "postgresql@17-main.service",
		}},
		Units: []HostUnit{
			{Name: "postgresql.service", LoadState: "loaded", ActiveState: "active", SubState: "exited", Enabled: true},
			{Name: "postgresql@17-main.service", LoadState: "loaded", ActiveState: "active", SubState: "running", Enabled: true, Port: 5438},
		},
	})
	if len(inv.Instances) != 1 {
		t.Fatalf("%d instances, want the one cluster", len(inv.Instances))
	}
	got := find(t, inv, "host:postgresql@17-main.service")
	if got.Version != "17" || got.Host.Cluster != "17/main" || got.Host.UnitState != "active" || !got.Host.Enabled {
		t.Errorf("version %q host %+v", got.Version, got.Host)
	}
	if got.Host.DataDir != "/var/lib/postgresql/17/main" || got.Host.ConfigFile != "/etc/postgresql/17/main/postgresql.conf" {
		t.Errorf("paths = %q %q", got.Host.DataDir, got.Host.ConfigFile)
	}
	if len(got.Endpoints) != 2 || got.Endpoints[0].Kind != "tcp" || !got.Endpoints[0].Primary ||
		got.Endpoints[1].Kind != "unix" || got.Endpoints[1].Path != "/var/run/postgresql/.s.PGSQL.5438" {
		t.Errorf("endpoints = %+v", got.Endpoints)
	}
	if got.Credentials != CredentialsPeer {
		t.Errorf("credentials = %q, want peer: the socket admits the postgres account", got.Credentials)
	}
	if !got.Connectable || got.User != "postgres" || got.Database != "postgres" {
		t.Errorf("connectable %v user %q database %q", got.Connectable, got.User, got.Database)
	}
}

// A server that listens on a unix socket only has no entry in the TCP table.
func TestASocketOnlyServerIsListedWithTheReason(t *testing.T) {
	inv := Discover(Facts{Sockets: []UnixSocket{
		{Path: "/run/mysqld/mysqld.sock", PID: 60, Process: "mariadbd", Manager: "systemd", ManagerName: "mariadb.service"},
		{Path: "/run/redis/redis-server.sock", PID: 61, Process: "redis-server"},
		{Path: "/run/user/1000/bus", PID: 62, Process: "dbus-daemon"},
		{Path: "/var/lib/docker/x/.s.PGSQL.5432", PID: 63, Process: "postgres", Manager: "container", ManagerName: "abcdefabcdef"},
	}})
	if len(inv.Instances) != 2 {
		t.Fatalf("%d instances, want mariadb and redis", len(inv.Instances))
	}
	maria := find(t, inv, "host:mariadb.service")
	if maria.Flavor != "mariadb" || maria.Connectable || maria.Credentials != CredentialsPeer {
		t.Errorf("flavor %q connectable %v credentials %q", maria.Flavor, maria.Connectable, maria.Credentials)
	}
	if !strings.Contains(maria.Reason, "unix socket only") {
		t.Errorf("reason = %q", maria.Reason)
	}
	if redis := find(t, inv, "host:redis:/run/redis/redis-server.sock"); redis.Confidence != ConfidenceSocket {
		t.Errorf("confidence = %q", redis.Confidence)
	}
}

// An installed server that is not running has no socket at all. systemd still
// has its unit, and for a Debian cluster its configuration says where it will
// listen.
func TestStoppedAndFailedUnitsAreListed(t *testing.T) {
	inv := Discover(Facts{Units: []HostUnit{
		{Name: "postgresql.service", LoadState: "loaded", ActiveState: "active", SubState: "exited"},
		{Name: "postgresql@16-main.service", LoadState: "loaded", ActiveState: "inactive", SubState: "dead", Port: 5433, DataDir: "/var/lib/postgresql/16/main"},
		{Name: "redis-server.service", LoadState: "loaded", ActiveState: "failed", SubState: "failed"},
		{Name: "redis-sentinel.service", LoadState: "loaded", ActiveState: "active", SubState: "running"},
		{Name: "postgres-exporter.service", LoadState: "loaded", ActiveState: "active", SubState: "running"},
		{Name: "mysql.service", LoadState: "not-found", ActiveState: "inactive", SubState: "dead"},
		{Name: "postgresql-16.service", LoadState: "loaded", ActiveState: "inactive", SubState: "dead"},
		{Name: "nginx.service", LoadState: "loaded", ActiveState: "active", SubState: "running"},
	}})
	if len(inv.Instances) != 3 {
		keys := []string{}
		for _, i := range inv.Instances {
			keys = append(keys, i.Key)
		}
		t.Fatalf("instances = %v", keys)
	}
	pg := find(t, inv, "host:postgresql@16-main.service")
	if pg.State != StateInactive || pg.Connectable || pg.Version != "16" || pg.Host.DataDir != "/var/lib/postgresql/16/main" {
		t.Errorf("state %q connectable %v version %q host %+v", pg.State, pg.Connectable, pg.Version, pg.Host)
	}
	if len(pg.Endpoints) != 1 || pg.Endpoints[0].Port != 5433 || pg.Endpoints[0].Primary {
		t.Errorf("endpoints = %+v, want the configured port and nothing to dial", pg.Endpoints)
	}
	if redis := find(t, inv, "host:redis-server.service"); redis.State != StateFailed || !strings.Contains(redis.Reason, "failed") {
		t.Errorf("state %q reason %q", redis.State, redis.Reason)
	}
	find(t, inv, "host:postgresql-16.service")
}

// A dashboard that is not root cannot read which process holds another
// account's socket. The kernel still says a socket is listening and the unit
// still says which port it was configured for, and that is enough to list the
// server as one instance rather than as a nameless socket and an idle unit.
func TestAServerWhoseProcessCannotBeReadIsJoinedByItsPort(t *testing.T) {
	inv := Discover(Facts{
		Listeners: []HostListener{{Protocol: "tcp", Address: "127.0.0.1", Port: 5438}},
		Sockets:   []UnixSocket{{Path: "/var/run/postgresql/.s.PGSQL.5438"}},
		Units: []HostUnit{
			{Name: "postgresql@17-main.service", LoadState: "loaded", ActiveState: "active", SubState: "running", Port: 5438},
			{Name: "postgresql@16-main.service", LoadState: "loaded", ActiveState: "inactive", SubState: "dead", Port: 5439},
		},
	})
	if len(inv.Instances) != 2 {
		t.Fatalf("%d instances, want the running cluster and the stopped one", len(inv.Instances))
	}
	got := find(t, inv, "host:postgresql@17-main.service")
	if !got.Connectable || got.Version != "17" || got.Credentials != CredentialsPeer {
		t.Errorf("connectable %v version %q credentials %q reason %q", got.Connectable, got.Version, got.Credentials, got.Reason)
	}
	if len(got.Endpoints) != 2 || !got.Endpoints[0].Primary || got.Endpoints[0].Port != 5438 || got.Endpoints[1].Kind != "unix" {
		t.Errorf("endpoints = %+v", got.Endpoints)
	}
	if access, ok := inv.Access(got.Key); !ok || access.Candidate.Port != 5438 {
		t.Errorf("access = %+v", access)
	}
	// A unit that is not running claims nothing, whatever listens on its port.
	inv = Discover(Facts{
		Listeners: []HostListener{{Protocol: "tcp", Address: "127.0.0.1", Port: 5439}},
		Units:     []HostUnit{{Name: "postgresql@16-main.service", LoadState: "loaded", ActiveState: "inactive", SubState: "dead", Port: 5439}},
	})
	if stopped := find(t, inv, "host:postgresql@16-main.service"); stopped.Connectable || stopped.State != StateInactive {
		t.Errorf("a stopped unit was given somebody's socket: %+v", stopped)
	}
}

// An application given no volume keeps its database in the container's own
// layer. It is listed, with its container and the warning that matters, and
// it is never something to connect.
func TestEmbeddedFilesAreListedWithTheirContainer(t *testing.T) {
	app := running("notes", "acme/notes:1", nil, "/app/server")
	app.State = "exited"
	inv := Discover(Facts{
		Containers: []ContainerFacts{app},
		Embedded: []EmbeddedFile{
			{Container: "notes", Path: "/data/notes.sqlite3", Engine: "sqlite", Size: 8192},
			{Container: "notes", Path: "/data/notes.sqlite3", Engine: "sqlite", Size: 8192},
			{Container: "gone", Path: "/x.duckdb", Engine: "duckdb"},
			{Container: "notes", Path: "/data/unknown.bin", Engine: "leveldb"},
		},
	})
	if len(inv.Instances) != 2 {
		t.Fatalf("%d instances", len(inv.Instances))
	}
	got := find(t, inv, "embedded:notes:/data/notes.sqlite3")
	if got.Kind != KindEmbedded || got.Engine != "sqlite" || got.Driver != "" || got.Connectable || got.State != "exited" {
		t.Errorf("embedded = %+v", got)
	}
	if got.Container.Image != "acme/notes:1" || got.File.Holder != HolderContainer || got.File.Size != 8192 {
		t.Errorf("container %+v file %+v", got.Container, got.File)
	}
	if !strings.Contains(got.Reason, "removing the container deletes it") {
		t.Errorf("reason = %q", got.Reason)
	}
	if !ValidInstanceKey(got.Key) {
		t.Errorf("%q is not a valid key", got.Key)
	}
	find(t, inv, "embedded:gone:/x.duckdb")
}

func TestDebianClusterNamesAreChecked(t *testing.T) {
	if v, c, ok := DebianCluster("postgresql@17-main.service"); !ok || v != "17" || c != "main" {
		t.Errorf("got %q %q %v", v, c, ok)
	}
	for _, unit := range []string{"postgresql.service", "postgresql@17.service", "postgresql@..-main.service", "postgresql@17-ma/in.service", "mysql@a-b.service"} {
		if _, _, ok := DebianCluster(unit); ok {
			t.Errorf("%q was read as a cluster", unit)
		}
	}
}

func TestFileMagic(t *testing.T) {
	sqlite := append([]byte("SQLite format 3\x00"), 0x10, 0x00)
	if engine, ok := FileMagic(sqlite); !ok || engine != "sqlite" {
		t.Errorf("sqlite header read as %q %v", engine, ok)
	}
	duck := append([]byte{1, 2, 3, 4, 5, 6, 7, 8}, []byte("DUCK\x40\x00\x00\x00")...)
	if engine, ok := FileMagic(duck); !ok || engine != "duckdb" {
		t.Errorf("duckdb header read as %q %v", engine, ok)
	}
	for _, header := range [][]byte{nil, []byte("SQLite"), []byte("PK\x03\x04 not a database"), []byte("SQLite format 2\x00")} {
		if engine, ok := FileMagic(header); ok {
			t.Errorf("%q read as %s", header, engine)
		}
	}
	for name, want := range map[string]bool{
		"app.db": true, "data.sqlite": true, "x.SQLITE3": true, "an.duckdb": true, "notes.db3": true,
		"app.db-wal": false, "dump.sql": false, "db": false, "main.go": false,
	} {
		if got := FileCandidate(name); got != want {
			t.Errorf("FileCandidate(%q) = %v", name, got)
		}
	}
}

func TestDataDirMarkers(t *testing.T) {
	names := func(list ...string) map[string]bool {
		out := map[string]bool{}
		for _, n := range list {
			out[n] = true
		}
		return out
	}
	cases := []struct {
		names  map[string]bool
		engine string
	}{
		{names("PG_VERSION", "base", "global", "pg_wal"), "postgres"},
		{names("ibdata1", "mysql", "performance_schema"), "mysql"},
		{names("WiredTiger", "WiredTiger.wt", "journal"), "mongodb"},
		{names("dump.rdb"), "redis"},
		{names("appendonlydir"), "redis"},
	}
	for _, tc := range cases {
		if engine, _, ok := DataDirMarker(tc.names); !ok || engine != tc.engine {
			t.Errorf("%v read as %q %v", tc.names, engine, ok)
		}
	}
	// A stray PG_VERSION is not a cluster.
	if engine, _, ok := DataDirMarker(names("PG_VERSION", "README")); ok {
		t.Errorf("read as %s", engine)
	}
	if _, _, ok := DataDirMarker(names("index.html", "app.js")); ok {
		t.Error("an ordinary directory was read as data")
	}
}

// A file is listed with whose it is. The dashboard's own store is marked and
// never connectable; a tool's private state is classified out of the way.
func TestFilesAreListedWithTheirHolder(t *testing.T) {
	when := time.Unix(1_700_000_000, 0).UTC()
	places := Places{
		StorePath: "/var/lib/just-dashboard/vpsd.db",
		DataDir:   "/var/lib/just-dashboard",
		Mounts: []MountPlace{
			{Source: "/var/lib/docker/volumes/n8n_data/_data", Volume: "n8n_data", Containers: []string{"n8n-app"}},
			{Source: "/var/lib/docker/volumes/old-data/_data", Volume: "old-data"},
			{Source: "/var/lib/docker/volumes/pg-data/_data", Volume: "pg-data", Containers: []string{"pg"}},
		},
		Deployments: []ProjectPlace{{Name: "shop", Path: "/srv/deploy/shop"}},
		Stacks:      []ProjectPlace{{Name: "blog", Path: "/opt/blog"}},
	}
	pg := running("pg", "postgres:16", map[string]string{"POSTGRES_PASSWORD": "pw"})
	pg.IPs = []string{"172.18.0.2"}
	inv := Discover(Facts{
		Containers: []ContainerFacts{pg},
		Places:     places,
		Files: []FileFacts{
			{Path: "/var/lib/just-dashboard/vpsd.db", Engine: "sqlite", Size: 1 << 20, Modified: when, WAL: true},
			{Path: "/var/lib/docker/volumes/n8n_data/_data/database.sqlite", Engine: "sqlite", Size: 4096, Modified: when},
			{Path: "/srv/deploy/shop/data/app.db", Engine: "sqlite", Size: 4096, Modified: when},
			{Path: "/opt/blog/content/ghost.db", Engine: "sqlite", Size: 4096, Modified: when},
			{Path: "/home/ubuntu/.codex/state_5.sqlite", Engine: "sqlite", Size: 4096, Modified: when},
			{Path: "/host/var/lib/PackageKit/transactions.db", Engine: "sqlite", Size: 4096, Modified: when},
			{Path: "/home/ubuntu/.local/share/pki/nssdb/cert9.db", Engine: "sqlite", Size: 4096, Modified: when},
			{Path: "/home/ubuntu/analytics/events.duckdb", Engine: "duckdb", Size: 4096, Modified: when},
			{Path: "/home/ubuntu/project/dev.db", Engine: "sqlite", Size: 4096, Modified: when},
		},
		DataDirs: []DataDirFacts{
			{Path: "/var/lib/docker/volumes/old-data/_data", Engine: "postgres", Version: "15\n", Modified: when},
			{Path: "/var/lib/docker/volumes/pg-data/_data", Engine: "postgres", Version: "16", Modified: when},
		},
	})
	holders := map[string]string{
		"file:/var/lib/just-dashboard/vpsd.db":                        HolderSelf,
		"file:/var/lib/docker/volumes/n8n_data/_data/database.sqlite": HolderContainer,
		"file:/srv/deploy/shop/data/app.db":                           HolderDeployment,
		"file:/opt/blog/content/ghost.db":                             HolderCompose,
		"file:/home/ubuntu/.codex/state_5.sqlite":                     HolderTool,
		"file:/host/var/lib/PackageKit/transactions.db":               HolderSystem,
		"file:/home/ubuntu/.local/share/pki/nssdb/cert9.db":           HolderTool,
		"file:/home/ubuntu/analytics/events.duckdb":                   HolderApplication,
		"file:/home/ubuntu/project/dev.db":                            HolderApplication,
	}
	for key, want := range holders {
		got := find(t, inv, key)
		if got.File.Holder != want {
			t.Errorf("%s: holder %q, want %q", key, got.File.Holder, want)
		}
		if got.Kind != KindFile || got.Confidence != ConfidenceMagic {
			t.Errorf("%s: kind %q confidence %q", key, got.Kind, got.Confidence)
		}
	}
	self := find(t, inv, "file:/var/lib/just-dashboard/vpsd.db")
	if !self.Self || self.Connectable || !self.File.WAL {
		t.Errorf("self %v connectable %v wal %v — the dashboard's own store is listed and never connected", self.Self, self.Connectable, self.File.WAL)
	}
	n8n := find(t, inv, "file:/var/lib/docker/volumes/n8n_data/_data/database.sqlite")
	if !n8n.Connectable || n8n.Driver != DriverSQLite || n8n.File.Volume != "n8n_data" || len(n8n.File.Containers) != 1 {
		t.Errorf("connectable %v driver %q file %+v", n8n.Connectable, n8n.Driver, n8n.File)
	}
	if duck := find(t, inv, "file:/home/ubuntu/analytics/events.duckdb"); duck.Driver != "" || duck.Connectable || duck.Engine != "duckdb" {
		t.Errorf("duckdb: driver %q connectable %v engine %q", duck.Driver, duck.Connectable, duck.Engine)
	}

	// The volume of a removed container is data without a server; the volume
	// of a listed one is that server's, and is not reported a second time.
	orphan := find(t, inv, "data:/var/lib/docker/volumes/old-data/_data")
	if orphan.Kind != KindData || orphan.Engine != "postgres" || orphan.Version != "15" || orphan.Connectable || orphan.Driver != "" {
		t.Errorf("orphan = %+v", orphan)
	}
	if orphan.Source != SourceVolume || orphan.Name != "old-data" {
		t.Errorf("orphan source %q name %q", orphan.Source, orphan.Name)
	}
	if _, ok := inv.Find("data:/var/lib/docker/volumes/pg-data/_data"); ok {
		t.Error("a running server's own data directory was listed as data without a server")
	}
}

// Two spellings of one address are one server.
func TestAddressIdentity(t *testing.T) {
	same := []string{"localhost", "127.0.0.1", "::1", "[::1]", "0.0.0.0", "::", "", "LOCALHOST", "127.0.0.2", "::ffff:127.0.0.1"}
	for _, host := range same {
		if got := AddressIdentity(host, 5432); got != "loopback:5432" {
			t.Errorf("AddressIdentity(%q) = %q", host, got)
		}
	}
	if a, b := AddressIdentity("fd00::1", 5432), AddressIdentity("[FD00:0:0::1]", 5432); a != b || a != "[fd00::1]:5432" {
		t.Errorf("%q vs %q", a, b)
	}
	if a, b := AddressIdentity("10.0.0.6", 5432), AddressIdentity("10.0.0.7", 5432); a == b {
		t.Error("two addresses collapsed")
	}
	if a, b := AddressIdentity("127.0.0.1", 5432), AddressIdentity("127.0.0.1", 5433); a == b {
		t.Error("two ports collapsed")
	}
	if got := AddressIdentity("db.internal", 5432); got != "db.internal:5432" {
		t.Errorf("a name is kept as it is: %q", got)
	}
}

// A saved connection is matched to the instance it points at: by the origin it
// recorded, and for one made before origins existed, by where it dials.
func TestSavedConnectionsAreAttachedToTheirInstance(t *testing.T) {
	pg := published(running("jd-postgres", "postgres:16", map[string]string{"POSTGRES_PASSWORD": "pw"}), 5432, 5435)
	pg.IPs = []string{"10.0.0.6"}
	moved := running("moved", "postgres:16", map[string]string{"POSTGRES_PASSWORD": "pw"})
	moved.IPs = []string{"10.0.0.9"}
	inv := Discover(Facts{
		Containers: []ContainerFacts{pg, moved},
		Listeners:  []HostListener{{Protocol: "tcp", Address: "127.0.0.1", Port: 5438, Process: "postgres", PID: 9}},
		Files:      []FileFacts{{Path: "/srv/app/data.db", Engine: "sqlite"}},
	})
	AttachConnections(inv.Instances, []SavedConnection{
		{ID: 1, Driver: DriverPostgres, Host: "localhost", Port: 5435},
		{ID: 2, Driver: DriverPostgres, Host: "10.0.0.6", Port: 5432},
		// Made at an address the container no longer has; its origin still says whose it is.
		{ID: 3, Driver: DriverPostgres, Origin: "docker:moved", Host: "10.0.0.250", Port: 5432},
		{ID: 4, Driver: DriverPostgres, Host: "::1", Port: 5438},
		{ID: 5, Driver: DriverSQLite, Path: "/srv/app/data.db"},
		// Another engine at the same address is not this server.
		{ID: 6, Driver: DriverMySQL, Host: "127.0.0.1", Port: 5435},
		{ID: 7, Driver: DriverPostgres, Host: "db.example.com", Port: 5432},
	})
	want := map[string][]int64{
		"docker:jd-postgres":    {1, 2},
		"docker:moved":          {3},
		"host:postgres:5438":    {4},
		"file:/srv/app/data.db": {5},
	}
	for key, ids := range want {
		got := find(t, inv, key).Connections
		if len(got) != len(ids) {
			t.Errorf("%s: connections %v, want %v", key, got, ids)
			continue
		}
		for i := range ids {
			if got[i] != ids[i] {
				t.Errorf("%s: connections %v, want %v", key, got, ids)
			}
		}
	}
}

// What is running comes first, then what could be, then what is only data.
func TestInventoryOrderIsStable(t *testing.T) {
	stopped := running("a-stopped", "postgres:16", nil)
	stopped.State = "exited"
	up := published(running("z-running", "postgres:16", map[string]string{"POSTGRES_PASSWORD": "pw"}), 5432, 5432)
	inv := Discover(Facts{
		Containers: []ContainerFacts{stopped, up},
		Files:      []FileFacts{{Path: "/srv/a.db", Engine: "sqlite"}},
		DataDirs:   []DataDirFacts{{Path: "/srv/pgdata", Engine: "postgres"}},
	})
	order := []string{}
	for _, inst := range inv.Instances {
		order = append(order, inst.Key)
	}
	want := []string{"docker:z-running", "docker:a-stopped", "data:/srv/pgdata", "file:/srv/a.db"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", order, want)
	}
	for _, inst := range inv.Instances {
		if inst.Endpoints == nil || inst.Evidence == nil || inst.Connections == nil {
			t.Errorf("%s: a list is null on the wire", inst.Key)
		}
	}
}
