package dbx

import (
	"path"
	"regexp"
	"strings"
)

// What this dashboard knows about each database product, in one table.
//
// Discovery asks the same question of five different things — an image name, a
// container's environment, the command it runs, a process on the host and a
// systemd unit — and the answers have to agree. Kept apart, the lists drift:
// a product added to the image table and forgotten in the process table is a
// server that is found in a container and invisible beside it. So a product is
// described once, with every name it goes by, and each rung reads its own
// column.
//
// A product is not always something this dashboard can open. Memcached and
// Elasticsearch have no driver here, and they are listed anyway: "there is a
// database on this server that this page cannot show you" is a fact the
// operator is owed, where silence would read as "there is none".

// product is one database product and every name it is known by.
type product struct {
	// id is the product's own name: the flavour for an engine this dashboard
	// opens ("mariadb"), the engine itself for one it only sees ("memcached").
	id string
	// engine is what reaches the wire as Instance.Engine: the driver's id for
	// anything a driver here speaks to, the product's id otherwise.
	engine string
	driver Driver
	label  string
	// port is where the product listens unless told otherwise. sidePorts are
	// the other sockets the same process holds — an HTTP interface, a cluster
	// bus, an admin port — which are part of the server and never the address
	// to dial.
	port      int
	sidePorts []int
	// portHint marks a default port distinctive enough that a container
	// exposing it, and saying nothing else about itself, is worth listing as a
	// guess. 9000 and 8086 are everybody's port; 5432 is not.
	portHint bool
	// procs are the kernel's names for the server process, matched exactly.
	// A name longer than the 15 characters comm holds is written in full and
	// compared truncated.
	procs []string
	// runtimes and markers describe a server that runs inside another
	// program: the process is "java" or "beam.smp", and only its command line
	// says which product it is.
	runtimes []string
	markers  []string
	// units are the systemd unit names the product's packages install,
	// without ".service" and without an instance suffix.
	units []string
	// envNames are variables the product's own image sets — not ones an
	// operator supplies — so their presence says what the image was built
	// from whatever it has been tagged as since.
	envNames []string
	// versionEnv is the one of those that carries the version.
	versionEnv string
	// user and database are the engine's own conventions for a server found
	// on the host, where nothing states them. open marks a product that ships
	// accepting connections with no credentials at all.
	user, database string
	open           bool
}

// Flavours share one vocabulary with the frontend's engine registry: the id a
// connection's server reports once it is dialled is the same id discovery
// guesses from the image before anybody has.
var products = []product{
	{
		id: "postgres", engine: "postgres", driver: DriverPostgres, label: "PostgreSQL",
		port: 5432, portHint: true, procs: []string{"postgres", "postmaster"},
		units:    []string{"postgresql"},
		envNames: []string{"PG_MAJOR", "PG_VERSION", "PGDATA"}, versionEnv: "PG_VERSION",
		user: "postgres", database: "postgres",
	},
	{id: "timescaledb", engine: "postgres", driver: DriverPostgres, label: "TimescaleDB", port: 5432, user: "postgres", database: "postgres"},
	{
		id: "cockroachdb", engine: "postgres", driver: DriverPostgres, label: "CockroachDB",
		port: 26257, sidePorts: []int{8080, 26258}, portHint: true,
		procs: []string{"cockroach"}, units: []string{"cockroach", "cockroachdb"},
		envNames: []string{"COCKROACH_CHANNEL"}, user: "root", database: "defaultdb",
	},
	{
		id: "yugabytedb", engine: "postgres", driver: DriverPostgres, label: "YugabyteDB",
		port: 5433, sidePorts: []int{7000, 7100, 9000, 9100, 9042, 6379, 10100, 11000, 12000, 13000, 15433, 18018},
		units: []string{"yugabyted", "yb-tserver"}, envNames: []string{"YB_HOME"},
		user: "yugabyte", database: "yugabyte",
	},
	{
		id: "mysql", engine: "mysql", driver: DriverMySQL, label: "MySQL",
		port: 3306, sidePorts: []int{33060, 33061, 33062}, portHint: true,
		procs: []string{"mysqld"}, units: []string{"mysql", "mysqld"},
		envNames: []string{"MYSQL_MAJOR", "MYSQL_VERSION"}, versionEnv: "MYSQL_VERSION",
		// The MySQL DSN takes an empty database happily, and picking one the
		// server may not have would turn a working connection into a failing one.
		user: "root",
	},
	{
		id: "mariadb", engine: "mysql", driver: DriverMySQL, label: "MariaDB",
		port: 3306, procs: []string{"mariadbd"}, units: []string{"mariadb"},
		envNames: []string{"MARIADB_VERSION"}, versionEnv: "MARIADB_VERSION", user: "root",
	},
	{
		id: "percona", engine: "mysql", driver: DriverMySQL, label: "Percona Server",
		port: 3306, sidePorts: []int{33060, 33061, 33062},
		envNames: []string{"PS_VERSION"}, versionEnv: "PS_VERSION", user: "root",
	},
	{
		id: "tidb", engine: "mysql", driver: DriverMySQL, label: "TiDB",
		port: 4000, sidePorts: []int{10080}, procs: []string{"tidb-server"}, units: []string{"tidb"},
		user: "root", open: true,
	},
	{
		id: "mongodb", engine: "mongodb", driver: DriverMongo, label: "MongoDB",
		port: 27017, portHint: true, procs: []string{"mongod", "mongos"}, units: []string{"mongod", "mongodb"},
		envNames: []string{"MONGO_MAJOR", "MONGO_VERSION", "MONGO_PACKAGE", "PSMDB_VERSION"}, versionEnv: "MONGO_VERSION",
		database: "admin", open: true,
	},
	{
		id: "ferretdb", engine: "mongodb", driver: DriverMongo, label: "FerretDB",
		port: 27017, sidePorts: []int{27018, 8088}, procs: []string{"ferretdb"}, units: []string{"ferretdb"},
		envNames: []string{"FERRETDB_LISTEN_ADDR", "FERRETDB_STATE_DIR"}, database: "admin",
	},
	{
		id: "redis", engine: "redis", driver: DriverRedis, label: "Redis",
		port: 6379, sidePorts: []int{16379}, portHint: true,
		procs: []string{"redis-server"}, units: []string{"redis-server", "redis"},
		envNames: []string{"REDIS_VERSION", "REDISEARCH_ARGS", "REDISJSON_ARGS"}, versionEnv: "REDIS_VERSION",
		database: "0", open: true,
	},
	{
		id: "valkey", engine: "redis", driver: DriverRedis, label: "Valkey",
		port: 6379, sidePorts: []int{16379}, procs: []string{"valkey-server"}, units: []string{"valkey-server", "valkey"},
		envNames: []string{"VALKEY_VERSION"}, versionEnv: "VALKEY_VERSION", database: "0", open: true,
	},
	{
		id: "keydb", engine: "redis", driver: DriverRedis, label: "KeyDB",
		port: 6379, sidePorts: []int{16379}, procs: []string{"keydb-server"}, units: []string{"keydb-server", "keydb"},
		envNames: []string{"KEYDB_PRO_DIRECTORY"}, database: "0", open: true,
	},
	{
		id: "dragonfly", engine: "redis", driver: DriverRedis, label: "Dragonfly",
		port: 6379, sidePorts: []int{11211}, procs: []string{"dragonfly"}, units: []string{"dragonfly"},
		database: "0", open: true,
	},
	{
		id: "sqlserver", engine: "sqlserver", driver: DriverMSSQL, label: "SQL Server",
		port: 1433, sidePorts: []int{1434}, portHint: true,
		procs: []string{"sqlservr"}, units: []string{"mssql-server"},
		user: "sa", database: "master",
	},
	{id: "azure-sql-edge", engine: "sqlserver", driver: DriverMSSQL, label: "Azure SQL Edge", port: 1433, sidePorts: []int{1401}, user: "sa", database: "master"},
	{
		id: "clickhouse", engine: "clickhouse", driver: DriverClickHouse, label: "ClickHouse",
		// The native protocol, not the 8123 HTTP one: that is the port this
		// package's driver speaks.
		port: 9000, sidePorts: []int{8123, 8443, 9004, 9005, 9009, 9010, 9100, 9363, 9440},
		procs: []string{"clickhouse-server", "clickhouse"}, units: []string{"clickhouse-server"},
		envNames: []string{"CLICKHOUSE_CONFIG"}, user: "default", database: "default", open: true,
	},
	{
		// Oracle's listener is the process that owns the socket; the database
		// itself is behind it and always wants credentials.
		id: "oracle", engine: "oracle", driver: DriverOracle, label: "Oracle Database",
		port: 1521, sidePorts: []int{5500}, portHint: true,
		procs: []string{"tnslsnr"}, envNames: []string{"ORACLE_SID", "ORACLE_HOME"},
		user: "system",
	},

	// Seen, and not opened: nothing in this package speaks to these.
	{
		id: "memcached", engine: "memcached", label: "Memcached", port: 11211, portHint: true,
		procs: []string{"memcached"}, units: []string{"memcached"},
		envNames: []string{"MEMCACHED_VERSION"}, versionEnv: "MEMCACHED_VERSION",
	},
	{
		id: "elasticsearch", engine: "elasticsearch", label: "Elasticsearch", port: 9200, sidePorts: []int{9300}, portHint: true,
		runtimes: []string{"java"}, markers: []string{"org.elasticsearch.", "-Des.path.home"},
		units: []string{"elasticsearch"}, envNames: []string{"ELASTIC_CONTAINER"},
	},
	{
		id: "opensearch", engine: "opensearch", label: "OpenSearch", port: 9200, sidePorts: []int{9300, 9600, 9650},
		runtimes: []string{"java"}, markers: []string{"org.opensearch.", "-Dopensearch.path.home"},
		units: []string{"opensearch"},
	},
	{
		id: "etcd", engine: "etcd", label: "etcd", port: 2379, sidePorts: []int{2380}, portHint: true,
		procs: []string{"etcd"}, units: []string{"etcd"},
	},
	{
		id: "cassandra", engine: "cassandra", label: "Apache Cassandra", port: 9042, sidePorts: []int{7000, 7001, 7199, 9160}, portHint: true,
		runtimes: []string{"java"}, markers: []string{"org.apache.cassandra."},
		units: []string{"cassandra"}, envNames: []string{"CASSANDRA_VERSION", "CASSANDRA_HOME"}, versionEnv: "CASSANDRA_VERSION",
	},
	{
		id: "scylladb", engine: "scylladb", label: "ScyllaDB", port: 9042, sidePorts: []int{7000, 7001, 9160, 9180, 10000, 19042},
		procs: []string{"scylla"}, units: []string{"scylla-server"},
	},
	{
		id: "neo4j", engine: "neo4j", label: "Neo4j", port: 7687, sidePorts: []int{7473, 7474}, portHint: true,
		runtimes: []string{"java"}, markers: []string{"org.neo4j.", "neo4j.home"},
		units: []string{"neo4j"}, envNames: []string{"NEO4J_HOME", "NEO4J_EDITION"},
	},
	{
		id: "influxdb", engine: "influxdb", label: "InfluxDB", port: 8086, sidePorts: []int{8088},
		procs: []string{"influxd", "influxdb3"}, units: []string{"influxdb", "influxd"},
		envNames: []string{"INFLUXDB_VERSION"}, versionEnv: "INFLUXDB_VERSION",
	},
	{
		id: "couchdb", engine: "couchdb", label: "CouchDB", port: 5984, sidePorts: []int{4369, 9100}, portHint: true,
		runtimes: []string{"beam.smp"}, markers: []string{"couchdb"},
		units: []string{"couchdb"}, envNames: []string{"COUCHDB_VERSION"}, versionEnv: "COUCHDB_VERSION",
	},
	{
		id: "rabbitmq", engine: "rabbitmq", label: "RabbitMQ", port: 5672, sidePorts: []int{4369, 5671, 15671, 15672, 15691, 15692, 25672}, portHint: true,
		runtimes: []string{"beam.smp"}, markers: []string{"rabbit"},
		units: []string{"rabbitmq-server"}, envNames: []string{"RABBITMQ_VERSION", "RABBITMQ_HOME"}, versionEnv: "RABBITMQ_VERSION",
	},
	{
		id: "nats", engine: "nats", label: "NATS", port: 4222, sidePorts: []int{6222, 8222}, portHint: true,
		procs: []string{"nats-server"}, units: []string{"nats-server", "nats"},
	},
	{
		id: "kafka", engine: "kafka", label: "Apache Kafka", port: 9092, sidePorts: []int{9093, 9094, 8081, 8082, 9644}, portHint: true,
		procs: []string{"redpanda"}, runtimes: []string{"java"}, markers: []string{"kafka.Kafka", "org.apache.kafka."},
		units: []string{"kafka", "redpanda"},
	},
	{
		id: "qdrant", engine: "qdrant", label: "Qdrant", port: 6333, sidePorts: []int{6334, 6335}, portHint: true,
		procs: []string{"qdrant"}, units: []string{"qdrant"},
	},
	{
		id: "meilisearch", engine: "meilisearch", label: "Meilisearch", port: 7700, portHint: true,
		procs: []string{"meilisearch"}, units: []string{"meilisearch"},
		envNames: []string{"MEILI_SERVER_PROVIDER"},
	},
	{
		id: "typesense", engine: "typesense", label: "Typesense", port: 8108, portHint: true,
		procs: []string{"typesense-server"}, units: []string{"typesense-server"},
	},

	// Files. Neither has a process, a port or a unit; they are found by the
	// first bytes of the file.
	{id: "sqlite", engine: "sqlite", driver: DriverSQLite, label: "SQLite"},
	{id: "duckdb", engine: "duckdb", label: "DuckDB"},
}

var productByID = func() map[string]*product {
	out := make(map[string]*product, len(products))
	for i := range products {
		out[products[i].id] = &products[i]
	}
	return out
}()

// flavor is the id a product is known by among the engines that share its
// driver, and empty for one this dashboard cannot open: a flavour is a kind of
// connection, and there is no connection to have a kind.
func (p *product) flavor() string {
	if p.driver == "" {
		return ""
	}
	return p.id
}

// sidePort reports a socket the server holds that is not the one to dial.
func (p *product) sidePort(port int) bool {
	for _, side := range p.sidePorts {
		if side == port {
			return true
		}
	}
	return false
}

// credentialStyle is which set of documented variables an image reads its
// credentials from. The same engine is packaged by several publishers, and
// each named its variables differently.
type credentialStyle int

const (
	styleNone credentialStyle = iota
	stylePostgres
	styleBitnamiPostgres
	styleMySQL
	styleMongo
	styleBitnamiMongo
	styleAtlasLocal
	styleRedis
	styleMSSQL
	styleClickHouse
	styleBitnamiClickHouse
	styleOracleGvenzl
	styleOracleOfficial
	styleCockroach
)

// imageRule is one image repository and what it is.
type imageRule struct {
	// repo is matched against the repository part of the image reference, as
	// a whole or as a path suffix, so both "postgres" and
	// "docker.io/library/postgres" hit and "mycorp/postgres-backup" does not.
	repo    string
	product string
	// variant names what the image adds to the product it is built from —
	// pgvector, PostGIS, a publisher's packaging — where that is worth saying.
	variant string
	style   credentialStyle
}

// imageRules is deliberately a list of names rather than a pattern. A wrong
// guess does not fail cleanly: it lists a server that is not one, and offers
// to connect to it.
var imageRules = []imageRule{
	{"postgres", "postgres", "", stylePostgres},
	{"postgis/postgis", "postgres", "postgis", stylePostgres},
	{"imresamu/postgis", "postgres", "postgis", stylePostgres},
	{"pgvector/pgvector", "postgres", "pgvector", stylePostgres},
	{"ankane/pgvector", "postgres", "pgvector", stylePostgres},
	{"tensorchord/pgvecto-rs", "postgres", "pgvecto.rs", stylePostgres},
	{"paradedb/paradedb", "postgres", "paradedb", stylePostgres},
	{"citusdata/citus", "postgres", "citus", stylePostgres},
	{"supabase/postgres", "postgres", "supabase", stylePostgres},
	{"cloudnative-pg/postgresql", "postgres", "cloudnative-pg", stylePostgres},
	{"groonga/pgroonga", "postgres", "pgroonga", stylePostgres},
	{"pgautoupgrade/pgautoupgrade", "postgres", "", stylePostgres},
	{"bitnami/postgresql", "postgres", "bitnami", styleBitnamiPostgres},
	{"bitnamilegacy/postgresql", "postgres", "bitnami", styleBitnamiPostgres},
	{"timescale/timescaledb", "timescaledb", "", stylePostgres},
	{"timescale/timescaledb-ha", "timescaledb", "timescaledb-ha", stylePostgres},
	{"cockroachdb/cockroach", "cockroachdb", "", styleCockroach},
	{"yugabytedb/yugabyte", "yugabytedb", "", styleNone},

	{"mysql", "mysql", "", styleMySQL},
	{"mysql/mysql-server", "mysql", "", styleMySQL},
	{"mysql/community-server", "mysql", "", styleMySQL},
	{"bitnami/mysql", "mysql", "bitnami", styleMySQL},
	{"bitnamilegacy/mysql", "mysql", "bitnami", styleMySQL},
	{"mariadb", "mariadb", "", styleMySQL},
	{"bitnami/mariadb", "mariadb", "bitnami", styleMySQL},
	{"bitnamilegacy/mariadb", "mariadb", "bitnami", styleMySQL},
	{"linuxserver/mariadb", "mariadb", "linuxserver", styleMySQL},
	{"percona", "percona", "", styleMySQL},
	{"percona/percona-server", "percona", "", styleMySQL},
	{"pingcap/tidb", "tidb", "", styleNone},

	{"mongo", "mongodb", "", styleMongo},
	{"mongodb/mongodb-community-server", "mongodb", "", styleMongo},
	{"mongodb/mongodb-enterprise-server", "mongodb", "enterprise", styleMongo},
	{"mongodb/mongodb-atlas-local", "mongodb", "atlas-local", styleAtlasLocal},
	{"percona/percona-server-mongodb", "mongodb", "percona", styleMongo},
	{"bitnami/mongodb", "mongodb", "bitnami", styleBitnamiMongo},
	{"bitnamilegacy/mongodb", "mongodb", "bitnami", styleBitnamiMongo},
	{"ferretdb/ferretdb", "ferretdb", "", styleNone},

	{"redis", "redis", "", styleRedis},
	{"redis/redis-stack-server", "redis", "redis-stack", styleRedis},
	{"redis/redis-stack", "redis", "redis-stack", styleRedis},
	{"bitnami/redis", "redis", "bitnami", styleRedis},
	{"bitnamilegacy/redis", "redis", "bitnami", styleRedis},
	{"valkey/valkey", "valkey", "", styleRedis},
	{"bitnami/valkey", "valkey", "bitnami", styleRedis},
	{"bitnamilegacy/valkey", "valkey", "bitnami", styleRedis},
	{"eqalpha/keydb", "keydb", "", styleRedis},
	{"bitnami/keydb", "keydb", "bitnami", styleRedis},
	{"bitnamilegacy/keydb", "keydb", "bitnami", styleRedis},
	{"dragonflydb/dragonfly", "dragonfly", "", styleRedis},

	{"mcr.microsoft.com/mssql/server", "sqlserver", "", styleMSSQL},
	{"mssql/server", "sqlserver", "", styleMSSQL},
	{"mcr.microsoft.com/azure-sql-edge", "azure-sql-edge", "", styleMSSQL},
	{"azure-sql-edge", "azure-sql-edge", "", styleMSSQL},

	{"clickhouse/clickhouse-server", "clickhouse", "", styleClickHouse},
	{"clickhouse", "clickhouse", "", styleClickHouse},
	{"yandex/clickhouse-server", "clickhouse", "", styleClickHouse},
	{"altinity/clickhouse-server", "clickhouse", "altinity", styleClickHouse},
	{"bitnami/clickhouse", "clickhouse", "bitnami", styleBitnamiClickHouse},
	{"bitnamilegacy/clickhouse", "clickhouse", "bitnami", styleBitnamiClickHouse},

	{"gvenzl/oracle-free", "oracle", "", styleOracleGvenzl},
	{"gvenzl/oracle-xe", "oracle", "", styleOracleGvenzl},
	{"container-registry.oracle.com/database/free", "oracle", "", styleOracleOfficial},
	{"container-registry.oracle.com/database/express", "oracle", "", styleOracleOfficial},
	{"container-registry.oracle.com/database/enterprise", "oracle", "", styleOracleOfficial},

	{"memcached", "memcached", "", styleNone},
	{"bitnami/memcached", "memcached", "bitnami", styleNone},
	{"bitnamilegacy/memcached", "memcached", "bitnami", styleNone},
	{"elasticsearch", "elasticsearch", "", styleNone},
	{"elasticsearch/elasticsearch", "elasticsearch", "", styleNone},
	{"bitnami/elasticsearch", "elasticsearch", "bitnami", styleNone},
	{"opensearchproject/opensearch", "opensearch", "", styleNone},
	{"bitnami/opensearch", "opensearch", "bitnami", styleNone},
	{"coreos/etcd", "etcd", "", styleNone},
	{"etcd-development/etcd", "etcd", "", styleNone},
	{"bitnami/etcd", "etcd", "bitnami", styleNone},
	{"bitnamilegacy/etcd", "etcd", "bitnami", styleNone},
	{"cassandra", "cassandra", "", styleNone},
	{"bitnami/cassandra", "cassandra", "bitnami", styleNone},
	{"scylladb/scylla", "scylladb", "", styleNone},
	{"neo4j", "neo4j", "", styleNone},
	{"influxdb", "influxdb", "", styleNone},
	{"couchdb", "couchdb", "", styleNone},
	{"apache/couchdb", "couchdb", "", styleNone},
	{"rabbitmq", "rabbitmq", "", styleNone},
	{"bitnami/rabbitmq", "rabbitmq", "bitnami", styleNone},
	{"nats", "nats", "", styleNone},
	{"apache/kafka", "kafka", "", styleNone},
	{"confluentinc/cp-kafka", "kafka", "confluent", styleNone},
	{"bitnami/kafka", "kafka", "bitnami", styleNone},
	{"bitnamilegacy/kafka", "kafka", "bitnami", styleNone},
	{"redpandadata/redpanda", "kafka", "redpanda", styleNone},
	{"qdrant/qdrant", "qdrant", "", styleNone},
	{"getmeili/meilisearch", "meilisearch", "", styleNone},
	{"typesense/typesense", "typesense", "", styleNone},
}

// imageRepo is the repository part of an image reference: the tag and any
// digest are dropped, a registry port is kept.
func imageRepo(image string) string {
	repo := image
	if i := strings.IndexByte(repo, '@'); i >= 0 {
		repo = repo[:i]
	}
	// A tag separator is a colon after the last slash; a colon before it is a
	// registry port.
	if i := strings.LastIndexByte(repo, ':'); i >= 0 && !strings.Contains(repo[i:], "/") {
		repo = repo[:i]
	}
	return repo
}

// imageRuleFor matches the repository part of an image reference, as a whole
// or as a path suffix — so "postgres:16", "library/postgres" and
// "docker.io/library/postgres@sha256:…" all match and "acme/postgres-backup"
// does not. The longest rule wins, so "bitnami/redis" is read as Bitnami's
// image rather than as somebody's copy of the official one.
func imageRuleFor(image string) (imageRule, bool) {
	repo := imageRepo(image)
	if repo == "" {
		return imageRule{}, false
	}
	var best imageRule
	found := false
	for _, rule := range imageRules {
		if repo != rule.repo && !strings.HasSuffix(repo, "/"+rule.repo) {
			continue
		}
		if !found || len(rule.repo) > len(best.repo) {
			best, found = rule, true
		}
	}
	return best, found
}

// styleFor is the variables a product's stock image reads, for a container
// recognised by something other than its image name. A container fingerprinted
// as the official image reads the official variables whatever it was retagged.
func styleFor(productID string, env map[string]string) credentialStyle {
	bitnami := env["BITNAMI_APP_NAME"] != ""
	switch productID {
	case "postgres", "timescaledb":
		if bitnami {
			return styleBitnamiPostgres
		}
		return stylePostgres
	case "mysql", "mariadb", "percona":
		return styleMySQL
	case "mongodb":
		if bitnami {
			return styleBitnamiMongo
		}
		return styleMongo
	case "redis", "valkey", "keydb", "dragonfly":
		return styleRedis
	case "sqlserver", "azure-sql-edge":
		return styleMSSQL
	case "clickhouse":
		if bitnami {
			return styleBitnamiClickHouse
		}
		return styleClickHouse
	case "oracle":
		return styleOracleGvenzl
	case "cockroachdb":
		return styleCockroach
	}
	return styleNone
}

// bitnamiApps maps what a Bitnami image calls itself to the product it packs.
// Every one of their images states BITNAMI_APP_NAME and starts from
// /opt/bitnami/scripts/<app>/, which is the same word.
var bitnamiApps = map[string]string{
	"postgresql": "postgres", "mysql": "mysql", "mariadb": "mariadb", "mongodb": "mongodb",
	"redis": "redis", "valkey": "valkey", "keydb": "keydb", "clickhouse": "clickhouse",
	"memcached": "memcached", "elasticsearch": "elasticsearch", "opensearch": "opensearch",
	"etcd": "etcd", "cassandra": "cassandra", "kafka": "kafka", "rabbitmq": "rabbitmq",
	"influxdb": "influxdb", "neo4j": "neo4j", "couchdb": "couchdb", "nats": "nats", "scylladb": "scylladb",
}

// productFromEnv reads which product an image was built from out of the
// variables the image itself sets. Only names are looked at, with one
// exception that is not a secret either: Bitnami's statement of its own name.
func productFromEnv(env map[string]string) (*product, string) {
	if len(env) == 0 {
		return nil, ""
	}
	if app := strings.ToLower(strings.TrimSpace(env["BITNAMI_APP_NAME"])); app != "" {
		if id, ok := bitnamiApps[app]; ok {
			return productByID[id], "BITNAMI_APP_NAME"
		}
	}
	for i := range products {
		p := &products[i]
		for _, name := range p.envNames {
			if _, ok := env[name]; ok {
				return p, name
			}
		}
	}
	return nil, ""
}

// commandNames are the programs a container runs that say what it is, beyond
// the process names in the product table: the wrapper scripts and renamed
// binaries the images start instead of the server itself.
var commandNames = map[string]string{
	"cockroach.sh": "cockroachdb", "eswrapper": "elasticsearch", "opensearch": "opensearch",
	"cassandra": "cassandra", "neo4j": "neo4j", "rabbitmq-server": "rabbitmq", "couchdb": "couchdb",
	"launch_sqlservr.sh": "sqlserver", "yugabyted": "yugabytedb",
}

// commandWrappers are the words that stand in front of the real program in a
// container's command line and say nothing about it.
var commandWrappers = map[string]bool{
	"tini": true, "dumb-init": true, "sh": true, "bash": true, "ash": true, "dash": true,
	"env": true, "exec": true, "nice": true, "python": true, "python3": true, "s6-svscan": true,
	"catatonit": true, "docker-init": true, "init": true,
}

// commandRunsAs are the wrappers that take the account to run as before the
// program, so the word after them is a user and not a command.
var commandRunsAs = map[string]bool{"gosu": true, "su-exec": true, "setpriv": true, "runuser": true, "chroot": true}

var bitnamiScriptRe = regexp.MustCompile(`^/opt/bitnami/scripts/([a-z0-9-]+)/`)

// productFromCommand reads which product a container runs from its command
// line: the first word that is a program, past the init, the shell and the
// entrypoint script in front of it.
//
// Only the command position is read. "myapp migrate postgres" names a
// database as an argument and is not one, so the walk stops at the first word
// that is neither a wrapper nor a server.
func productFromCommand(argv []string) (*product, string) {
	words := []string{}
	for _, arg := range argv {
		// `sh -c "exec mysqld --user=mysql"` carries the program inside one
		// argument.
		words = append(words, strings.Fields(arg)...)
		if len(words) >= 12 {
			break
		}
	}
	skipNext := false
	for i, word := range words {
		if i >= 12 {
			break
		}
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(word, "-") {
			continue
		}
		if m := bitnamiScriptRe.FindStringSubmatch(word); m != nil {
			if id, ok := bitnamiApps[m[1]]; ok {
				return productByID[id], word
			}
		}
		name := path.Base(word)
		if p := productForProcess(name, ""); p != nil {
			return p, name
		}
		if id, ok := commandNames[name]; ok {
			return productByID[id], name
		}
		switch {
		case commandRunsAs[name]:
			skipNext = true
		case commandWrappers[name], strings.HasSuffix(name, ".sh"), strings.HasSuffix(name, ".py"),
			strings.Contains(name, "entrypoint"), name == "run":
		default:
			return nil, ""
		}
	}
	return nil, ""
}

// nonServers are programs named after a database that are not one: exporters,
// poolers, proxies, sentinels and coordinators. They are listed so the rule
// that leaves them out is written down, and tested, rather than being an
// accident of exact matching.
var nonServers = []string{
	"postgres_exporter", "postgrest", "pgbouncer", "pgpool", "odyssey", "pgcat",
	"mysqld_exporter", "mysqlrouter", "proxysql", "mongodb_exporter", "redis_exporter",
	"redis-sentinel", "clickhouse-keeper", "docker-proxy", "memcached_exporter",
	"elasticsearch_exporter", "kafka_exporter",
}

// runsNonServer reports a command line that starts one of those programs,
// wherever in it the program is named: an image's server binary is often
// replaced by a shell line that ends in `exec redis-sentinel …`.
func runsNonServer(argv []string) bool {
	for _, arg := range argv {
		for _, word := range strings.Fields(arg) {
			if word == "--sentinel" {
				return true
			}
			name := path.Base(strings.Trim(word, `"';`))
			for _, other := range nonServers {
				if name == other {
					return true
				}
			}
		}
	}
	return false
}

// commMax is how much of a program's name the kernel keeps in comm.
const commMax = 15

// procMatches compares a process name with a program's, allowing for the
// kernel having cut the name short: "clickhouse-server" is only ever seen as
// "clickhouse-serv".
func procMatches(comm, program string) bool {
	if comm == program {
		return true
	}
	return len(program) > commMax && comm == program[:commMax]
}

// productForProcess names the product a process is, or nil.
//
// The name is matched exactly. A prefix match took "postgres_exporter" for a
// Postgres and "clickhouse-keeper" for a ClickHouse, and offered both as
// database servers that wanted a password.
func productForProcess(process, cmdline string) *product {
	name := strings.ToLower(strings.TrimSpace(process))
	if name == "" {
		return nil
	}
	for _, other := range nonServers {
		if procMatches(name, other) {
			return nil
		}
	}
	line := strings.ToLower(cmdline)
	for i := range products {
		p := &products[i]
		for _, want := range p.procs {
			if !procMatches(name, want) {
				continue
			}
			if !serverInvocation(p, name, line) {
				return nil
			}
			return p
		}
	}
	if cmdline == "" {
		return nil
	}
	for i := range products {
		p := &products[i]
		for _, runtime := range p.runtimes {
			if name != runtime {
				continue
			}
			for _, marker := range p.markers {
				if strings.Contains(cmdline, marker) {
					return p
				}
			}
		}
	}
	return nil
}

// serverInvocation rules out the programs that share a server's binary: a
// Redis started as a sentinel, and the one `clickhouse` binary run as keeper,
// client or local.
func serverInvocation(p *product, name, cmdline string) bool {
	if cmdline == "" {
		return true
	}
	switch p.engine {
	case "redis":
		return !strings.Contains(cmdline, "[sentinel]") && !strings.Contains(cmdline, "--sentinel")
	case "clickhouse":
		if name != "clickhouse" {
			return true
		}
		fields := strings.Fields(cmdline)
		if len(fields) < 2 {
			return true
		}
		return fields[1] == "server" || strings.HasPrefix(fields[1], "-")
	}
	return true
}

// unitInstanceRe is a packaged PostgreSQL that names its unit after its major
// version, as the PGDG packages for Red Hat do: postgresql-16.service.
var unitInstanceRe = regexp.MustCompile(`^postgresql-[0-9]+$`)

// productForUnit names the product a systemd unit runs, from the unit's own
// name. The instance part of a template ("postgresql@17-main") is not part of
// the name, and a sentinel or an exporter installed beside the server under a
// name of its own does not match.
func productForUnit(unit string) *product {
	base := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(unit)), ".service")
	if i := strings.IndexByte(base, '@'); i >= 0 {
		base = base[:i]
	}
	if base == "" {
		return nil
	}
	if unitInstanceRe.MatchString(base) {
		return productByID["postgres"]
	}
	for i := range products {
		for _, want := range products[i].units {
			if base == want {
				return &products[i]
			}
		}
	}
	return nil
}

// productForPort is the product a port number hints at, and nil for a port
// that says nothing on its own.
func productForPort(port int) *product {
	for i := range products {
		if products[i].portHint && products[i].port == port {
			return &products[i]
		}
	}
	return nil
}

var versionRe = regexp.MustCompile(`[0-9]+(?:\.[0-9]+){0,3}`)

// cleanVersion keeps the version out of what an image's variable says, which
// is a package version: "1:11.4.2+maria~ubu2404", "8.4.11-1.el9",
// "16.15-1.pgdg12+2".
func cleanVersion(raw string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.IndexByte(raw, ':'); i >= 0 && i < 3 {
		raw = raw[i+1:]
	}
	return versionRe.FindString(raw)
}
