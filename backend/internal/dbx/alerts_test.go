package dbx

import (
	"slices"
	"strings"
	"testing"
)

// What each engine can be watched for is written out by hand, from what each
// reading below can read, and not derived from the rule it checks.
func TestAlertKindsPerEngine(t *testing.T) {
	every := []string{AlertDown, AlertDisk, AlertConnections, AlertReplication, AlertBackupFailed, AlertBackupMissed}
	noReplication := []string{AlertDown, AlertDisk, AlertConnections, AlertBackupFailed, AlertBackupMissed}
	serverOnly := []string{AlertDown, AlertDisk, AlertBackupFailed, AlertBackupMissed}
	for _, c := range []struct {
		d      Driver
		flavor string
		want   []string
	}{
		{DriverPostgres, "", every},
		{DriverPostgres, FlavorTimescaleDB, every},
		// No statistics collector and no replication view behind the wire.
		{DriverPostgres, FlavorCockroachDB, serverOnly},
		{DriverMySQL, FlavorMariaDB, every},
		{DriverMySQL, FlavorTiDB, serverOnly},
		{DriverMSSQL, "", noReplication},
		{DriverClickHouse, "", noReplication},
		{DriverOracle, "", noReplication},
		{DriverMongo, "", every},
		// FerretDB's serverStatus counts no connections and it has no replica set.
		{DriverMongo, FlavorFerretDB, serverOnly},
		{DriverRedis, FlavorValkey, every},
		{DriverRedis, FlavorDragonfly, every},
		// A file has no server to go down; it can still be in a backup job.
		{DriverSQLite, "", []string{AlertBackupFailed, AlertBackupMissed}},
		{Driver("memcached"), "", []string{}},
	} {
		if got := AlertKinds(c.d, c.flavor); !slices.Equal(got, c.want) {
			t.Errorf("%s/%s: alert kinds %v, want %v", c.d, c.flavor, got, c.want)
		}
		if c.d.Valid() {
			if got, _ := Capabilities(c.d, c.flavor)["alertKinds"].([]string); !slices.Equal(got, c.want) {
				t.Errorf("%s/%s: the catalogue serves alertKinds %v, want %v", c.d, c.flavor, got, c.want)
			}
		}
	}
	// The replication alert is offered exactly where the replication view is.
	for _, d := range Drivers() {
		for _, flavor := range Flavors(d) {
			offered := slices.Contains(AlertKinds(d, flavor), AlertReplication)
			if offered != Capable(d, flavor, "replication") {
				t.Errorf("%s/%s: replication alert %v, replication view %v", d, flavor, offered, Capable(d, flavor, "replication"))
			}
		}
	}
	if !AlertKindKnown(AlertBackupMissed) || AlertKindKnown("cpu") {
		t.Fatal("the kind vocabulary is not the closed list")
	}
}

func TestMySQLReplicationLagIsTheReplicasOwn(t *testing.T) {
	running := ReplicaSource{Host: "db1", Port: "3306", IORunning: true, SQLRunning: true, LagSeconds: 42}
	lag := mysqlReplicationLag(&ReplicationReport{Sources: []ReplicaSource{running}})
	if !lag.Replicating || lag.Seconds != 42 || lag.Of != "db1:3306" || lag.Stopped != "" {
		t.Fatalf("running replica: %+v", lag)
	}
	stopped := ReplicaSource{Channel: "orders", Host: "db1", Port: "3306", IORunning: true, LagSeconds: -1, LastError: "Duplicate entry '7' for key 'PRIMARY'"}
	lag = mysqlReplicationLag(&ReplicationReport{Sources: []ReplicaSource{running, stopped}})
	if lag.Stopped != "Replication from orders (db1:3306) is stopped: its SQL thread is not running." {
		t.Fatalf("stopped channel: %+v", lag)
	}
	// A primary does not know its replicas' lag, and says so rather than 0.
	lag = mysqlReplicationLag(&ReplicationReport{Replicas: []Replica{{Name: "2", LagSeconds: -1}}})
	if lag.Replicating || !strings.Contains(lag.Note, "does not know") {
		t.Fatalf("primary: %+v", lag)
	}
	if lag := mysqlReplicationLag(&ReplicationReport{}); lag.Replicating || lag.Note != "" {
		t.Fatalf("standalone: %+v", lag)
	}
}

func TestMongoReplicationLagReadsEverySecondary(t *testing.T) {
	behind, close := 31.0, 2.0
	set := &MongoReplication{ReplicaSet: true, SetName: "rs0", Members: []MongoReplicaMember{
		{Name: "a:27017", State: "PRIMARY", Health: true},
		{Name: "b:27017", State: "SECONDARY", Health: true, LagSeconds: &close},
		{Name: "c:27017", State: "SECONDARY", Health: true, LagSeconds: &behind},
		{Name: "d:27017", State: "ARBITER", Health: true},
	}}
	lag := mongoReplicationLag(set)
	if !lag.Replicating || lag.Seconds != 31 || lag.Of != "c:27017" || lag.Stopped != "" {
		t.Fatalf("set: %+v", lag)
	}
	set.Members[1].Health, set.Members[1].State = false, "(not reachable/healthy)"
	if lag := mongoReplicationLag(set); !strings.Contains(lag.Stopped, "b:27017 of rs0 is not answering") {
		t.Fatalf("unreachable member: %+v", lag)
	}
	if lag := mongoReplicationLag(&MongoReplication{ReplicaSet: true, SetName: "rs0", Members: []MongoReplicaMember{
		{Name: "b:27017", State: "SECONDARY", Health: true},
	}}); lag.Stopped != "rs0 has no primary, so nothing is replicated." {
		t.Fatalf("no primary: %+v", lag)
	}
	if lag := mongoReplicationLag(&MongoReplication{}); lag.Replicating {
		t.Fatalf("standalone: %+v", lag)
	}
}

func TestRedisReplicationLagReadsInfo(t *testing.T) {
	info := redisInfoMap(parseRedisInfo("# Replication\r\nrole:master\r\nconnected_slaves:2\r\n" +
		"slave0:ip=10.0.0.5,port=6379,state=online,offset=10,lag=0\r\n" +
		"slave1:ip=10.0.0.6,port=6379,state=online,offset=8,lag=4\r\nmaster_repl_offset:10\r\n"))
	lag := redisReplicationLag(redisReplicationFacts(info))
	if !lag.Replicating || lag.Seconds != 4 || lag.Of != "10.0.0.6:6379" {
		t.Fatalf("primary: %+v", lag)
	}
	info = redisInfoMap(parseRedisInfo("# Replication\r\nrole:slave\r\nmaster_host:10.0.0.1\r\nmaster_port:6379\r\n" +
		"master_link_status:down\r\nmaster_last_io_seconds_ago:-1\r\nmaster_sync_in_progress:0\r\n"))
	if lag := redisReplicationLag(redisReplicationFacts(info)); lag.Stopped != "The link to the primary 10.0.0.1:6379 is down." {
		t.Fatalf("replica with its link down: %+v", lag)
	}
	info["master_link_status"], info["master_last_io_seconds_ago"] = "up", "7"
	if lag := redisReplicationLag(redisReplicationFacts(info)); lag.Stopped != "" || lag.Seconds != 7 {
		t.Fatalf("replica: %+v", lag)
	}
	if lag := redisReplicationLag(redisReplicationFacts(map[string]string{"role": "master"})); lag.Replicating {
		t.Fatalf("a primary with no replicas: %+v", lag)
	}
}

func TestDiskUsePercentAndDataDirHints(t *testing.T) {
	if got := (DiskUse{Used: 45, Total: 50}).Percent(); got != 90 {
		t.Fatalf("percent = %v", got)
	}
	if got := (DiskUse{}).Percent(); got != 0 {
		t.Fatalf("an empty reading is %v%% full", got)
	}
	for _, d := range Drivers() {
		hints := DataDirHints(d)
		if (d == DriverSQLite) != (len(hints) == 0) {
			t.Errorf("%s: data directory hints %v", d, hints)
		}
	}
}
