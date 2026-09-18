package dbx

import (
	"encoding/json"
	"strings"
	"testing"
)

func ports(containerPort, hostPort int) []PublishedPort {
	return []PublishedPort{{ContainerPort: containerPort, HostIP: "127.0.0.1", HostPort: hostPort}}
}

// The DSN built from a container's own statement of itself has to be one the
// driver accepts, because the operator never sees it: it is sealed on the way
// in, and a wrong one surfaces much later as a login failure they cannot
// debug from the UI.
func TestDetectBuildsAUsableDSN(t *testing.T) {
	cases := []struct {
		name      string
		image     string
		env       map[string]string
		published []PublishedPort
		driver    Driver
		dsn       string
	}{
		{
			name: "postgres", image: "postgres:16",
			env:       map[string]string{"POSTGRES_USER": "jdtest", "POSTGRES_PASSWORD": "jdtest", "POSTGRES_DB": "shop"},
			published: ports(5432, 5432), driver: DriverPostgres,
			dsn: "postgres://jdtest:jdtest@127.0.0.1:5432/shop?sslmode=disable",
		},
		{
			// POSTGRES_DB defaults to the user's name, not to "postgres".
			name: "postgres without a database", image: "postgres:16-alpine",
			env:       map[string]string{"POSTGRES_USER": "app", "POSTGRES_PASSWORD": "pw"},
			published: ports(5432, 5432), driver: DriverPostgres,
			dsn: "postgres://app:pw@127.0.0.1:5432/app?sslmode=disable",
		},
		{
			name: "mysql prefers the unprivileged account", image: "mysql:8",
			env: map[string]string{
				"MYSQL_ROOT_PASSWORD": "rootpw", "MYSQL_USER": "app",
				"MYSQL_PASSWORD": "apppw", "MYSQL_DATABASE": "shop",
			},
			published: ports(3306, 3306), driver: DriverMySQL,
			dsn: "app:apppw@tcp(127.0.0.1:3306)/shop",
		},
		{
			name: "mysql falls back to root", image: "mysql:8",
			env:       map[string]string{"MYSQL_ROOT_PASSWORD": "rootpw", "MYSQL_DATABASE": "shop"},
			published: ports(3306, 3306), driver: DriverMySQL,
			dsn: "root:rootpw@tcp(127.0.0.1:3306)/shop",
		},
		{
			name: "mariadb", image: "mariadb:11",
			env:       map[string]string{"MARIADB_ROOT_PASSWORD": "pw", "MARIADB_DATABASE": "shop"},
			published: ports(3306, 3307), driver: DriverMySQL,
			dsn: "root:pw@tcp(127.0.0.1:3307)/shop",
		},
		{
			name: "redis with no password at all", image: "redis:7",
			published: ports(6379, 6379), driver: DriverRedis,
			dsn: "redis://127.0.0.1:6379/0",
		},
		{
			name: "redis with a password", image: "redis:7-alpine",
			env:       map[string]string{"REDIS_PASSWORD": "s3cret"},
			published: ports(6379, 6379), driver: DriverRedis,
			dsn: "redis://:s3cret@127.0.0.1:6379/0",
		},
		{
			name: "mongo", image: "mongo:7",
			env:       map[string]string{"MONGO_INITDB_ROOT_USERNAME": "root", "MONGO_INITDB_ROOT_PASSWORD": "pw"},
			published: ports(27017, 27017), driver: DriverMongo,
			dsn: "mongodb://root:pw@127.0.0.1:27017/admin?authSource=admin",
		},
		{
			name: "sql server", image: "mcr.microsoft.com/mssql/server:2022-latest",
			env:       map[string]string{"MSSQL_SA_PASSWORD": "JdTest#2024pw"},
			published: ports(1433, 1433), driver: DriverMSSQL,
			dsn: "sqlserver://sa:JdTest%232024pw@127.0.0.1:1433?database=master",
		},
		{
			// The native port, not the 8123 HTTP one the same image publishes.
			name: "clickhouse", image: "clickhouse/clickhouse-server:24.8",
			published: []PublishedPort{
				{ContainerPort: 8123, HostIP: "127.0.0.1", HostPort: 8123},
				{ContainerPort: 9000, HostIP: "127.0.0.1", HostPort: 9000},
			},
			driver: DriverClickHouse,
			dsn:    "clickhouse://default@127.0.0.1:9000/default",
		},
		{
			name: "oracle prefers the application account", image: "gvenzl/oracle-free:23-slim-faststart",
			env:       map[string]string{"ORACLE_PASSWORD": "sys", "APP_USER": "jdtest", "APP_USER_PASSWORD": "jdtest"},
			published: ports(1521, 1521), driver: DriverOracle,
			dsn: "oracle://jdtest:jdtest@127.0.0.1:1521/FREEPDB1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, password := Detect("db", c.image, c.env, c.published, nil)
			if got == nil {
				t.Fatalf("image %q was not recognised", c.image)
			}
			if got.Driver != c.driver {
				t.Errorf("driver = %q, want %q", got.Driver, c.driver)
			}
			if !got.Connectable() {
				t.Fatalf("not connectable: %s", got.Reason)
			}
			if dsn := BuildDSN(*got, password); dsn != c.dsn {
				t.Errorf("dsn  = %q\nwant = %q", dsn, c.dsn)
			}
		})
	}
}

// A wrong guess does not fail cleanly — it saves a connection that looks real
// and then refuses to open — so anything not recognised is left alone.
func TestDetectIgnoresWhatItDoesNotKnow(t *testing.T) {
	for _, image := range []string{
		"nginx:alpine",
		"acme/postgres-backup:1", // not postgres, despite the name
		"mycorp/redis-exporter",  // an exporter is not the server
		"caddy:2-alpine",
		"bet-bot-high-market-tracker",
	} {
		if got, _ := Detect("c", image, nil, ports(5432, 5432), nil); got != nil {
			t.Errorf("image %q was taken for a %s", image, got.Driver)
		}
	}
	// And the registry-with-a-port case must not be mistaken for a tag.
	if got, _ := Detect("c", "registry.example.com:5000/postgres:16", nil, ports(5432, 5432), nil); got == nil {
		t.Error("a private-registry postgres should still be recognised")
	}
}

// A database on a compose network with nothing published is the commonest
// database on any server this runs on — an application shipping its own
// Postgres, reachable by the app beside it and by nothing else.
//
// It is reachable from here. The backend shares the host's network namespace
// and a Docker bridge is routable from there, so the container's own address
// on the engine's standard port is a real destination. Refusing it was the bug:
// the one database an operator most wanted connected was the one the dashboard
// declined, while psql from the same namespace worked.
func TestDetectReachesAContainerOnItsOwnNetwork(t *testing.T) {
	got, password := Detect("db", "postgres:16",
		map[string]string{"POSTGRES_PASSWORD": "pw"}, nil, []string{"172.18.0.4"})
	if got == nil {
		t.Fatal("a postgres with no published port should still be recognised")
	}
	if !got.Connectable() {
		t.Fatalf("should be reachable at its container address: %s", got.Reason)
	}
	if got.Host != "172.18.0.4" || got.Port != 5432 {
		t.Errorf("address = %s:%d, want 172.18.0.4:5432", got.Host, got.Port)
	}
	if !got.ViaContainerNetwork {
		t.Error("a candidate reached this way must say so: the address changes when the container is recreated")
	}
	if dsn := BuildDSN(*got, password); !strings.Contains(dsn, "172.18.0.4:5432") {
		t.Errorf("dsn = %q, want it to dial the container address", dsn)
	}
}

// A published port is still preferred, because it survives a recreate and a
// container address does not.
func TestDetectPrefersAPublishedPortOverTheContainerAddress(t *testing.T) {
	got, _ := Detect("db", "postgres:16", nil, ports(5432, 5433), []string{"172.18.0.4"})
	if got.Host != "127.0.0.1" || got.Port != 5433 {
		t.Errorf("address = %s:%d, want the published 127.0.0.1:5433", got.Host, got.Port)
	}
	if got.ViaContainerNetwork {
		t.Error("this was reached at a published port, not a container address")
	}
}

// Neither a published port nor an address of its own — a container sharing
// another's namespace. There is nothing to dial, and saying so beats omitting
// it: it is a database the operator can see running.
func TestDetectExplainsAContainerWithNowhereToDial(t *testing.T) {
	got, _ := Detect("db", "postgres:16", map[string]string{"POSTGRES_PASSWORD": "pw"}, nil, nil)
	if got == nil {
		t.Fatal("it should still be recognised")
	}
	if got.Connectable() {
		t.Error("no port and no address is not connectable")
	}
	if got.Reason == "" {
		t.Error("it should say why")
	}
}

// A port published to every interface is dialled on loopback: it is the same
// machine, and that keeps the connection off the network.
func TestDetectDialsLoopback(t *testing.T) {
	got, _ := Detect("db", "postgres:16", nil,
		[]PublishedPort{{ContainerPort: 5432, HostIP: "0.0.0.0", HostPort: 5432}}, nil)
	if got.Host != "127.0.0.1" {
		t.Errorf("host = %q, want 127.0.0.1", got.Host)
	}
}

// The description handed to a browser must never carry the secret.
func TestCandidateCarriesNoPassword(t *testing.T) {
	got, password := Detect("db", "postgres:16",
		map[string]string{"POSTGRES_USER": "u", "POSTGRES_PASSWORD": "hunter2"}, ports(5432, 5432), nil)
	if password != "hunter2" {
		t.Fatalf("password = %q", password)
	}
	blob, err := jsonMarshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(blob, "hunter2") {
		t.Errorf("the password reached the serialised candidate: %s", blob)
	}
}

func jsonMarshal(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func TestMongoApplicationDatabaseAuthenticatesAgainstAdmin(t *testing.T) {
	candidate, password := Detect("mongo", "mongo:7", map[string]string{
		"MONGO_INITDB_ROOT_USERNAME": "jd", "MONGO_INITDB_ROOT_PASSWORD": "secret", "MONGO_INITDB_DATABASE": "app",
	}, []PublishedPort{{ContainerPort: 27017, HostIP: "127.0.0.1", HostPort: 27017}}, nil)
	if candidate == nil {
		t.Fatal("Mongo was not detected")
	}
	got := BuildDSN(*candidate, password)
	if got != "mongodb://jd:secret@127.0.0.1:27017/app?authSource=admin" {
		t.Fatalf("incorrect auth database: %s", got)
	}
}
