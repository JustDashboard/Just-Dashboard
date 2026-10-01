package dbx

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// What a server is, as opposed to how this package talks to it.
//
// A driver is a wire protocol. MariaDB, Percona and TiDB all speak MySQL's;
// Valkey, KeyDB and Dragonfly all speak Redis's; CockroachDB and YugabyteDB
// speak Postgres's. The page that draws a MariaDB server under MySQL's name
// and logo is telling the operator they connected to something they did not,
// and a tab offered for a feature the flavour lacks fails on its first click.
// The flavour is the second word: the driver says which code path, the flavour
// says which product, and Capabilities takes both.
//
// Everything that decides a flavour here is a pure function over what the
// server said about itself — its version string, the comment its build left,
// the text of INFO. Nothing is inferred from an image name or a port: those
// are hints the discovery side reads before anything is dialled, and a server
// that has answered is the better witness.

// The flavour ids are shared vocabulary: the frontend's engine registry, the
// inventory and the capability table all spell them this way. A driver's own
// flavour carries the driver's id.
const (
	FlavorPostgres     = "postgres"
	FlavorTimescaleDB  = "timescaledb"
	FlavorCockroachDB  = "cockroachdb"
	FlavorYugabyteDB   = "yugabytedb"
	FlavorMySQL        = "mysql"
	FlavorMariaDB      = "mariadb"
	FlavorPercona      = "percona"
	FlavorTiDB         = "tidb"
	FlavorRedis        = "redis"
	FlavorValkey       = "valkey"
	FlavorKeyDB        = "keydb"
	FlavorDragonfly    = "dragonfly"
	FlavorMongoDB      = "mongodb"
	FlavorFerretDB     = "ferretdb"
	FlavorSQLServer    = "sqlserver"
	FlavorAzureSQLEdge = "azure-sql-edge"
	FlavorSQLite       = "sqlite"
	FlavorClickHouse   = "clickhouse"
	FlavorOracle       = "oracle"
)

// flavors lists each driver's flavours, its own first.
var flavors = map[Driver][]string{
	DriverPostgres:   {FlavorPostgres, FlavorTimescaleDB, FlavorCockroachDB, FlavorYugabyteDB},
	DriverMySQL:      {FlavorMySQL, FlavorMariaDB, FlavorPercona, FlavorTiDB},
	DriverRedis:      {FlavorRedis, FlavorValkey, FlavorKeyDB, FlavorDragonfly},
	DriverMongo:      {FlavorMongoDB, FlavorFerretDB},
	DriverMSSQL:      {FlavorSQLServer, FlavorAzureSQLEdge},
	DriverSQLite:     {FlavorSQLite},
	DriverClickHouse: {FlavorClickHouse},
	DriverOracle:     {FlavorOracle},
}

var flavorLabels = map[string]string{
	FlavorPostgres:     "PostgreSQL",
	FlavorTimescaleDB:  "TimescaleDB",
	FlavorCockroachDB:  "CockroachDB",
	FlavorYugabyteDB:   "YugabyteDB",
	FlavorMySQL:        "MySQL",
	FlavorMariaDB:      "MariaDB",
	FlavorPercona:      "Percona Server",
	FlavorTiDB:         "TiDB",
	FlavorRedis:        "Redis",
	FlavorValkey:       "Valkey",
	FlavorKeyDB:        "KeyDB",
	FlavorDragonfly:    "Dragonfly",
	FlavorMongoDB:      "MongoDB",
	FlavorFerretDB:     "FerretDB",
	FlavorSQLServer:    "SQL Server",
	FlavorAzureSQLEdge: "Azure SQL Edge",
	FlavorSQLite:       "SQLite",
	FlavorClickHouse:   "ClickHouse",
	FlavorOracle:       "Oracle",
}

// Flavors returns the flavours a driver can turn out to be, its own first.
func Flavors(d Driver) []string {
	return append([]string(nil), flavors[d]...)
}

// DefaultFlavor is the driver's own product: what a connection is taken for
// until its server has said otherwise.
func DefaultFlavor(d Driver) string {
	if list := flavors[d]; len(list) > 0 {
		return list[0]
	}
	return string(d)
}

// FlavorLabel is the product's name as its own documentation writes it.
func FlavorLabel(flavor string) string {
	if label, ok := flavorLabels[flavor]; ok {
		return label
	}
	return flavor
}

// FlavorOf reports whether a flavour belongs to a driver.
func FlavorOf(d Driver, flavor string) bool {
	for _, f := range flavors[d] {
		if f == flavor {
			return true
		}
	}
	return false
}

// DetectFlavor names the product behind a driver from what the server said
// about itself.
//
// The signals are whatever was read: the version string, MySQL's
// version_comment, the names of a Postgres database's installed extensions,
// the text of Redis's INFO, the keys of Mongo's buildInfo. They are taken
// together and in any order, because which of them names the product differs
// per fork — Percona's version string is indistinguishable from MySQL's and
// only its comment says, while MariaDB's comment says nothing its version did
// not. With no signal at all the answer is the driver's own flavour, which is
// the honest reading of a server that has not been asked.
func DetectFlavor(d Driver, signals ...string) string {
	text := strings.ToLower(strings.Join(signals, "\n"))
	has := func(needle string) bool { return strings.Contains(text, needle) }
	switch d {
	case DriverPostgres:
		switch {
		case has("cockroachdb"):
			return FlavorCockroachDB
		// YugabyteDB answers version() as the PostgreSQL it forked, with its
		// own release after a "-YB-" marker.
		case has("-yb-"), has("yugabyte"):
			return FlavorYugabyteDB
		case has("timescaledb"):
			return FlavorTimescaleDB
		}
	case DriverMySQL:
		switch {
		case has("tidb"):
			return FlavorTiDB
		case has("mariadb"):
			return FlavorMariaDB
		case has("percona"):
			return FlavorPercona
		}
	case DriverRedis:
		info := infoFields(strings.Join(signals, "\n"))
		switch {
		case info["dragonfly_version"] != "":
			return FlavorDragonfly
		case info["valkey_version"] != "", strings.EqualFold(info["server_name"], "valkey"):
			return FlavorValkey
		// KeyDB reports itself as the Redis it forked. Its own section of INFO
		// and the name of the program are the two places it says otherwise.
		case info["mvcc_depth"] != "", strings.Contains(strings.ToLower(info["executable"]), "keydb"):
			return FlavorKeyDB
		}
	case DriverMongo:
		if has("ferretdb") {
			return FlavorFerretDB
		}
	case DriverMSSQL:
		if has("azure sql edge") {
			return FlavorAzureSQLEdge
		}
	}
	return DefaultFlavor(d)
}

// infoFields reads the key:value lines of a Redis INFO reply. Text that is
// not INFO simply yields no fields.
func infoFields(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if key, value, ok := strings.Cut(line, ":"); ok {
			out[key] = strings.TrimSpace(value)
		}
	}
	return out
}

var (
	// dottedVersionRe is the first run of dot-separated numbers in a string.
	dottedVersionRe = regexp.MustCompile(`\d+(?:\.\d+)+`)
	// perconaVersionRe keeps the release Percona appends to the MySQL version
	// it is built from: 8.0.35-27 is a different server from 8.0.35-26.
	perconaVersionRe = regexp.MustCompile(`^(\d+(?:\.\d+)+-\d+)`)
	mariaVersionRe   = regexp.MustCompile(`(?i)(\d+(?:\.\d+)+)-mariadb`)
	tidbVersionRe    = regexp.MustCompile(`(?i)tidb-v?(\d+(?:\.\d+)+)`)
	yugabyteRe       = regexp.MustCompile(`(?i)-yb-(\d+(?:\.\d+)+)`)
	cockroachRe      = regexp.MustCompile(`(?i)cockroachdb.*?v(\d+(?:\.\d+)+)`)
	mssqlBuildRe     = regexp.MustCompile(` - (\d+(?:\.\d+)+)`)
	oracleReleaseRe  = regexp.MustCompile(`(?i)(?:release|version) (\d+(?:\.\d+)+)`)
)

// VersionNumber is the product's own version number, bare — "16.4", not
// "PostgreSQL 16.4 on x86_64-pc-linux-musl" — read out of the same signals
// DetectFlavor takes.
//
// The fork's number is the one returned where the server states two: Valkey
// answers redis_version with the Redis release it stays compatible with, TiDB
// and YugabyteDB lead with the upstream version they imitate, and a MariaDB
// behind an old replication shim prefixes its own with "5.5.5-". Reporting the
// compatibility number would have every Valkey 8 shown as Redis 7.2. Empty
// when nothing in the signals reads as a version.
func VersionNumber(d Driver, flavor string, signals ...string) string {
	text := strings.Join(signals, "\n")
	first := func(re *regexp.Regexp) string {
		if m := re.FindStringSubmatch(text); len(m) > 1 {
			return m[1]
		}
		return ""
	}
	switch d {
	case DriverRedis:
		info := infoFields(text)
		switch flavor {
		case FlavorDragonfly:
			return strings.TrimPrefix(strings.TrimPrefix(info["dragonfly_version"], "df-"), "v")
		case FlavorValkey:
			if v := info["valkey_version"]; v != "" {
				return v
			}
		}
		return info["redis_version"]
	case DriverMySQL:
		switch flavor {
		case FlavorMariaDB:
			if v := first(mariaVersionRe); v != "" {
				return v
			}
		case FlavorTiDB:
			if v := first(tidbVersionRe); v != "" {
				return v
			}
		case FlavorPercona:
			if v := first(perconaVersionRe); v != "" {
				return v
			}
		}
	case DriverPostgres:
		switch flavor {
		case FlavorCockroachDB:
			if v := first(cockroachRe); v != "" {
				return v
			}
		case FlavorYugabyteDB:
			if v := first(yugabyteRe); v != "" {
				return v
			}
		}
	case DriverMSSQL:
		if v := first(mssqlBuildRe); v != "" {
			return v
		}
	case DriverOracle:
		if v := first(oracleReleaseRe); v != "" {
			return v
		}
	}
	return dottedVersionRe.FindString(text)
}

// ShortVersion trims a server's description of itself to what fits a tile:
// "PostgreSQL 16.4 on x86_64-pc-linux-musl, compiled by gcc ..." becomes
// "PostgreSQL 16.4". What follows the product and its release — the platform,
// the compiler, SQL Server's build number and date — is cut at the separator
// each engine uses, and anything still too long ends at a word rather than in
// the middle of one.
func ShortVersion(v string) string {
	v = strings.TrimSpace(v)
	for _, sep := range []string{"\n", " on ", ",", " - "} {
		if i := strings.Index(v, sep); i > 0 {
			v = strings.TrimSpace(v[:i])
		}
	}
	const longest = 48
	if len(v) > longest {
		v = v[:longest]
		if i := strings.LastIndex(v, " "); i > 0 {
			v = v[:i]
		}
	}
	return v
}

// Identity is what a server says it is.
type Identity struct {
	Flavor string `json:"flavor"`
	// Version is the server's own description, shortened for a tile, and
	// Number the bare version number inside it.
	Version string `json:"version,omitempty"`
	Number  string `json:"versionNumber,omitempty"`
}

// IdentifySQL asks a SQL server what it is. Every query past the first is a
// courtesy: one that the engine refuses leaves the flavour at whatever the
// version string alone said, never an error — a server that answered its
// ping is identified as far as it will say.
func IdentifySQL(ctx context.Context, db *sql.DB, driver Driver) Identity {
	d, err := DialectFor(driver)
	if err != nil {
		return Identity{Flavor: DefaultFlavor(driver)}
	}
	var version string
	_ = db.QueryRowContext(ctx, d.VersionQuery()).Scan(&version)
	signals := []string{version}
	switch driver {
	case DriverMySQL:
		// Percona builds answer version() exactly as MySQL does; the comment
		// their build carries is the one place the product is named.
		var comment sql.NullString
		if db.QueryRowContext(ctx, "SELECT @@version_comment").Scan(&comment) == nil {
			signals = append(signals, comment.String)
		}
	case DriverPostgres:
		// TimescaleDB is PostgreSQL with an extension, so version() cannot
		// say. It is asked only where the server is PostgreSQL proper: the
		// forks answer for themselves above and need not carry the catalogue.
		if DetectFlavor(driver, version) == FlavorPostgres {
			var name string
			if db.QueryRowContext(ctx,
				"SELECT extname FROM pg_extension WHERE extname = 'timescaledb'").Scan(&name) == nil {
				signals = append(signals, name)
			}
		}
	}
	flavor := DetectFlavor(driver, signals...)
	return Identity{
		Flavor:  flavor,
		Version: ShortVersion(version),
		Number:  VersionNumber(driver, flavor, version),
	}
}

// IdentifyRedis reads the server section of INFO, and KeyDB's own section
// beside it — the one place a KeyDB whose program was renamed still says so.
func IdentifyRedis(ctx context.Context, client *redis.Client) Identity {
	server, err := client.Info(ctx, "server").Result()
	if err != nil {
		return Identity{Flavor: FlavorRedis}
	}
	signals := []string{server}
	if DetectFlavor(DriverRedis, server) == FlavorRedis {
		// An unknown section is an empty reply on Redis and an error on the
		// servers that are stricter about it; either way it adds nothing.
		if keydb, err := client.Info(ctx, "keydb").Result(); err == nil {
			signals = append(signals, keydb)
		}
	}
	flavor := DetectFlavor(DriverRedis, signals...)
	number := VersionNumber(DriverRedis, flavor, server)
	return Identity{Flavor: flavor, Version: number, Number: number}
}

// IdentifyMongo reads buildInfo, which every server speaking the protocol
// answers before authentication is even asked for.
func IdentifyMongo(ctx context.Context, client *mongo.Client) Identity {
	var raw bson.M
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&raw); err != nil {
		return Identity{Flavor: FlavorMongoDB}
	}
	// FerretDB answers with the MongoDB version it emulates and names itself
	// in a key of its own, so the keys are signals as much as the values.
	signals := make([]string, 0, len(raw))
	for key := range raw {
		signals = append(signals, key)
	}
	flavor := DetectFlavor(DriverMongo, signals...)
	version, _ := raw["version"].(string)
	if flavor == FlavorFerretDB {
		// Its own release, where it states one, rather than the MongoDB
		// version it answers as: a flat key in the first major version and a
		// document in the second.
		own, _ := raw["ferretdbVersion"].(string)
		if doc, ok := raw["ferretdb"].(bson.M); ok {
			own, _ = doc["version"].(string)
		}
		if own != "" {
			version = strings.TrimPrefix(own, "v")
		}
	}
	return Identity{Flavor: flavor, Version: version, Number: version}
}

// ProbeIdentity dials a connection string once, keeps nothing, and reports
// what answered. It is the one probe for every engine: the two that are not
// SQL have no dialect to ask, and a test that reported a healthy Redis as
// unreachable for that reason is how the connection form came to contradict
// the connection it then saved.
func ProbeIdentity(ctx context.Context, driver Driver, dsn string) (Identity, error) {
	switch driver {
	case DriverMongo:
		client, err := MongoClient(ctx, dsn)
		if err != nil {
			return Identity{}, err
		}
		defer client.Disconnect(context.Background())
		return IdentifyMongo(ctx, client), nil
	case DriverRedis:
		// The identity is the server's whichever database is selected. The
		// one the string names is selected because this is also the check a
		// connection string passes before it is saved, and a string naming a
		// database the server does not have must not pass it.
		client, err := RedisClient(ctx, dsn, RedisDSNDatabase)
		if err != nil {
			return Identity{}, err
		}
		defer client.Close()
		return IdentifyRedis(ctx, client), nil
	}
	d, err := DialectFor(driver)
	if err != nil {
		return Identity{}, err
	}
	// A throwaway pool, never the manager's: a connection being tested may be
	// wrong, and a failed test must not leave a broken pool cached under an id.
	db, err := sql.Open(d.SQLDriverName(), d.NormaliseDSN(dsn))
	if err != nil {
		return Identity{}, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return Identity{}, err
	}
	return IdentifySQL(pingCtx, db, driver), nil
}
