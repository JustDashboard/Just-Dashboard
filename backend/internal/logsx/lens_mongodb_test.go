package logsx

import "testing"

// mongoAttrs is a line's component and context and the pairs given.
func mongoAttrs(component, ctx string, pairs ...string) map[string]string {
	m := map[string]string{"component": component, "ctx": ctx}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return m
}

const mongoSlowQuery = `{"t":{"$date":"2024-06-01T13:24:10.034+00:00"},"s":"I","c":"COMMAND","id":51803,"ctx":"conn3","msg":"Slow query","attr":{"type":"command","ns":"db.coll","appName":"MongoDB Shell","command":{"find":"coll","filter":{"b":-1},"sort":{"splitPoint":1},"$db":"db"},"planSummary":"COLLSCAN","planningTimeMicros":87,"keysExamined":0,"docsExamined":20889,"hasSortStage":true,"nBatches":1,"nreturned":0,"queryHash":"ABC123","planCacheShapeHash":"DEF456","planCacheKey":"GHI789","queues":{"execution":{"totalTimeQueuedMicros":50}},"locks":{"Global":{"acquireCount":{"r":1}}},"remote":"192.168.1.100:12345","protocol":"op_msg","durationMillis":1234,"workingMillis":1200,"reslen":5000}}`

// The research's verified mongod lines (connections, authentication, the
// slow query), and mongod 7's own startup, storage and shutdown lines with
// their alignment padding, which the decoder must not mind.
var mongodbCases = []dbCase{
	{
		name: "a connection's life",
		lines: []string{
			`{"t":{"$date":"2026-09-27T10:00:00.000+00:00"},"s":"I","c":"NETWORK","id":22943,"ctx":"listener","msg":"Connection accepted","attr":{"remote":"10.0.1.9:32988","uuid":{"uuid":{"$uuid":"daf5fbdf-dc69-46b5-93ac-6be77ece6fc5"}},"connectionId":305,"connectionCount":1}}`,
			`{"t":{"$date":"2026-09-27T10:00:00.010+00:00"},"s":"I","c":"NETWORK","id":51800,"ctx":"conn305","msg":"client metadata","attr":{"remote":"10.0.1.9:32988","client":"conn305","doc":{"application":{"name":"MongoDB Shell"},"driver":{"name":"nodejs","version":"6.8.0"},"os":{"type":"Linux","name":"linux","architecture":"x64","version":"6.8.0-45-generic"}}}}`,
			`{"t":{"$date":"2022-06-28T18:13:14.754+03:00"},"s":"I","c":"ACCESS","id":20250,"ctx":"conn331646","msg":"Authentication succeeded","attr":{"mechanism":"SCRAM-SHA-1","speculative":true,"principalName":"user_name","authenticationDatabase":"database_name","remote":"10.10.10.10:34634","extraInfo":{}}}`,
			`{"t":{"$date":"2022-06-21T08:05:59.634+00:00"},"s":"I","c":"ACCESS","id":20249,"ctx":"conn18","msg":"Authentication failed","attr":{"mechanism":"SCRAM-SHA-256","speculative":true,"principalName":"root","authenticationDatabase":"admin","remote":"10.10.10.10:49440","extraInfo":{},"error":"UserNotFound: Could not find user \"root\" for db \"admin\""}}`,
			`{"t":{"$date":"2026-09-27T10:05:00.000+00:00"},"s":"I","c":"NETWORK","id":22944,"ctx":"conn305","msg":"Connection ended","attr":{"remote":"10.0.1.9:32988","uuid":{"uuid":{"$uuid":"daf5fbdf-dc69-46b5-93ac-6be77ece6fc5"}},"connectionId":305,"connectionCount":0}}`,
		},
		want: []dbWant{
			{level: "info", at: "2026-09-27 10:00:00Z", event: "connection", attrs: mongoAttrs("NETWORK", "listener",
				"client", "10.0.1.9", "port", "32988")},
			{level: "info", at: "2026-09-27 10:00:00.01Z", event: "client_metadata", attrs: mongoAttrs("NETWORK", "conn305",
				"client", "10.0.1.9", "port", "32988", "app", "MongoDB Shell")},
			{level: "info", at: "2022-06-28 15:13:14.754Z", event: "authorized", attrs: mongoAttrs("ACCESS", "conn331646",
				"client", "10.10.10.10", "port", "34634", "user", "user_name")},
			{level: "info", at: "2022-06-21 08:05:59.634Z", event: "auth_failed", attrs: mongoAttrs("ACCESS", "conn18",
				"client", "10.10.10.10", "port", "49440", "user", "root", "code", "UserNotFound")},
			{level: "info", at: "2026-09-27 10:05:00Z", event: "disconnection", attrs: mongoAttrs("NETWORK", "conn305",
				"client", "10.0.1.9", "port", "32988")},
		},
	},
	{
		name:  "a slow query",
		lines: []string{mongoSlowQuery},
		want: []dbWant{
			{level: "info", at: "2024-06-01 13:24:10.034Z", event: "slow", attrs: mongoAttrs("COMMAND", "conn3",
				"client", "192.168.1.100", "port", "12345", "ns", "db.coll", "app", "MongoDB Shell", "plan", "COLLSCAN",
				"duration_ms", "1234", "docs_examined", "20889", "keys_examined", "0", "rows", "0",
				"query", `{"find":"coll","filter":{"b":-1},"sort":{"splitPoint":1},"$db":"db"}`,
				// The keys stay and the values go: two finds on the same
				// fields with other values are one shape.
				"fp", Fingerprint("db.coll find filter:{b:?} find:? sort:{splitPoint:?}"))},
		},
	},
	{
		name: "start, storage, replication, index builds and shutdown",
		lines: []string{
			"about to fork child process, waiting until server is ready for connections.",
			`{"t":{"$date":"2026-09-27T09:59:58.100+00:00"},"s":"I",  "c":"CONTROL",  "id":4615611, "ctx":"initandlisten","msg":"MongoDB starting","attr":{"pid":1,"port":27017,"dbPath":"/data/db","architecture":"64-bit","host":"4b1e6f0c2a9d"}}`,
			`{"t":{"$date":"2026-09-27T09:59:58.900+00:00"},"s":"W",  "c":"CONTROL",  "id":22120,   "ctx":"initandlisten","msg":"Access control is not enabled for the database. Read and write access to data and configuration is unrestricted","tags":["startupWarnings"]}`,
			`{"t":{"$date":"2026-09-27T09:59:59.200+00:00"},"s":"I",  "c":"NETWORK",  "id":23016,   "ctx":"listener","msg":"Waiting for connections","attr":{"port":27017,"ssl":"off"}}`,
			`{"t":{"$date":"2026-09-27T10:01:00.456+00:00"},"s":"I",  "c":"WTCHKPT",  "id":22430,   "ctx":"Checkpointer","msg":"WiredTiger message","attr":{"message":{"ts_sec":1790503260,"ts_usec":456789,"thread":"1:0x7f1c2d3e4640","session_name":"WT_SESSION.checkpoint","category":"WT_VERB_CHECKPOINT_PROGRESS","category_id":6,"verbose_level":"DEBUG_1","verbose_level_id":1,"msg":"saving checkpoint snapshot min: 37, snapshot max: 37 snapshot count: 0, oldest timestamp: (0, 0) , meta checkpoint timestamp: (0, 0) base write gen: 1"}}}`,
			`{"t":{"$date":"2026-09-27T10:10:00.000+00:00"},"s":"I",  "c":"REPL",     "id":21358,   "ctx":"ReplCoord-0","msg":"Replica set state transition","attr":{"newState":"PRIMARY","oldState":"SECONDARY"}}`,
			`{"t":{"$date":"2026-09-27T10:11:00.000+00:00"},"s":"I",  "c":"INDEX",    "id":20345,   "ctx":"IndexBuildsCoordinatorMongod-0","msg":"Index build: done building","attr":{"buildUUID":null,"collectionUUID":{"uuid":{"$uuid":"0b5a3e2e-8a51-4f7b-9c3d-2f6e1a7b9c0d"}},"namespace":"shop.orders","index":"status_1","ident":"index-12-1234567890","collectionIdent":"collection-7-1234567890","commitTimestamp":null}}`,
			`{"t":{"$date":"2026-09-27T11:00:00.000+00:00"},"s":"I",  "c":"CONTROL",  "id":23377,   "ctx":"SignalHandler","msg":"Received signal","attr":{"signal":15,"error":"Terminated"}}`,
			`{"t":{"$date":"2026-09-27T11:00:00.500+00:00"},"s":"I",  "c":"CONTROL",  "id":23138,   "ctx":"SignalHandler","msg":"Shutting down","attr":{"exitCode":0}}`,
		},
		want: []dbWant{
			{},
			{level: "info", at: "2026-09-27 09:59:58.1Z", event: "startup", attrs: mongoAttrs("CONTROL", "initandlisten")},
			{level: "warn", at: "2026-09-27 09:59:58.9Z", attrs: mongoAttrs("CONTROL", "initandlisten")},
			{level: "info", at: "2026-09-27 09:59:59.2Z", event: "ready", attrs: mongoAttrs("NETWORK", "listener")},
			{level: "info", at: "2026-09-27 10:01:00.456Z", event: "checkpoint", attrs: mongoAttrs("WTCHKPT", "Checkpointer")},
			{level: "info", at: "2026-09-27 10:10:00Z", event: "replication", attrs: mongoAttrs("REPL", "ReplCoord-0")},
			{level: "info", at: "2026-09-27 10:11:00Z", event: "index", attrs: mongoAttrs("INDEX", "IndexBuildsCoordinatorMongod-0")},
			{level: "info", at: "2026-09-27 11:00:00Z", event: "shutdown", attrs: mongoAttrs("CONTROL", "SignalHandler")},
			{level: "info", at: "2026-09-27 11:00:00.5Z", event: "shutdown", attrs: mongoAttrs("CONTROL", "SignalHandler")},
		},
	},
	{
		name: "the severities, and an error that is an object",
		lines: []string{
			`{"t":{"$date":"2026-09-27T10:20:00.000+00:00"},"s":"E",  "c":"CONTROL",  "id":20557,   "ctx":"initandlisten","msg":"DBException in initAndListen, terminating","attr":{"error":"DBPathInUse: Unable to lock the lock file: /data/db/mongod.lock (Resource temporarily unavailable). Another mongod instance is already running on the /data/db directory"}}`,
			`{"t":{"$date":"2026-09-27T10:20:00.001+00:00"},"s":"F",  "c":"CONTROL",  "id":20574,   "ctx":"initandlisten","msg":"Error during global initialization","attr":{"error":{"code":98,"codeName":"DBPathInUse","errmsg":"Unable to lock the lock file: /data/db/mongod.lock (Resource temporarily unavailable)."}}}`,
			`{"t":{"$date":"2026-09-27T10:21:00.000+00:00"},"s":"D1", "c":"COMMAND",  "id":21965,   "ctx":"conn12","msg":"About to run the command","attr":{"db":"shop","client":"10.0.1.9:32988","commandArgs":{"find":"orders"}}}`,
		},
		want: []dbWant{
			{level: "error", at: "2026-09-27 10:20:00Z", attrs: mongoAttrs("CONTROL", "initandlisten")},
			{level: "critical", at: "2026-09-27 10:20:00.001Z", attrs: mongoAttrs("CONTROL", "initandlisten")},
			{level: "debug", at: "2026-09-27 10:21:00Z", attrs: mongoAttrs("COMMAND", "conn12")},
		},
	},
}

func TestMongodbLens(t *testing.T) {
	dbRun(t, "mongodb", mongodbCases)
}

// A line whose fields are not in mongod's order, or that carries the svc a
// sharded cluster adds, still reads the same.
func TestMongodbLensReadsLinesOutOfOrderAndWithService(t *testing.T) {
	got := readThrough(t, "mongodb",
		`{"t":{"$date":"2026-09-27T10:00:00.000Z"},"msg":"Connection accepted","s":"I","c":"NETWORK","id":22943,"ctx":"listener","attr":{"remote":"10.0.1.9:32988","connectionId":305,"connectionCount":1}}`,
		`{"t":{"$date":"2026-09-27T10:00:01.000+02:00"},"s":"I",  "c":"NETWORK",  "id":22944,   "ctx":"conn305","svc":"R","msg":"Connection ended","attr":{"remote":"[::1]:40112","connectionId":305,"connectionCount":0}}`,
	)
	want := []dbWant{
		{level: "info", at: "2026-09-27 10:00:00Z", event: "connection", attrs: mongoAttrs("NETWORK", "listener",
			"client", "10.0.1.9", "port", "32988")},
		{level: "info", at: "2026-09-27 08:00:01Z", event: "disconnection", attrs: mongoAttrs("NETWORK", "conn305",
			"client", "::1", "port", "40112")},
	}
	for i := range got {
		dbCheckLine(t, i, got[i], want[i])
	}
}

// A shape is the fields a command touches, not the values it passes, and not
// the session and cluster-time noise a driver adds to every command.
func TestMongodbLensShapesIgnoreValuesAndDriverNoise(t *testing.T) {
	shape := func(command string) string {
		return mongoShape("shop.orders", []byte(command))
	}
	a := shape(`{"find":"orders","filter":{"status":"paid","total":{"$gt":100}},"lsid":{"id":{"$uuid":"a"}},"$db":"shop"}`)
	b := shape(`{"find":"orders","filter":{"total":{"$gt":7},"status":"open"},"$clusterTime":{"clusterTime":{"$timestamp":{"t":1,"i":1}}},"$db":"shop"}`)
	if a != b {
		t.Fatalf("same fields, different shapes:\n%s\n%s", a, b)
	}
	if c := shape(`{"find":"orders","filter":{"customer":"x"},"$db":"shop"}`); c == a {
		t.Fatalf("different fields share a shape: %s", c)
	}
	in := shape(`{"find":"orders","filter":{"status":{"$in":["a","b","c"]}}}`)
	if in != "shop.orders find filter:{status:{$in:[?]}} find:?" {
		t.Fatalf("an $in list is its members' shape once: %s", in)
	}
}

func BenchmarkLensMongodb(b *testing.B) {
	var texts []string
	for _, c := range mongodbCases {
		texts = append(texts, c.lines...)
	}
	dbBench(b, "mongodb", texts)
}
