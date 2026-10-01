package dbx

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// The server as a thing to watch: what it has done since it started, what it
// is doing now, what it found slow, and who it replicates with.
//
// Everything serverStatus reports is a running total, so nothing here is a
// rate. A snapshot carries the server's own clock instead, and whoever draws
// a chart takes two snapshots and divides — which keeps the arithmetic where
// the polling interval is known.

// mongoPlain turns a BSON value into what encoding/json writes plainly:
// maps, slices, numbers and strings, with a date as RFC 3339 text.
func mongoPlain(v bson.RawValue) any {
	switch v.Type {
	case bsontype.EmbeddedDocument:
		out := map[string]any{}
		elems, err := v.Document().Elements()
		if err != nil {
			return out
		}
		for _, e := range elems {
			out[e.Key()] = mongoPlain(e.Value())
		}
		return out
	case bsontype.Array:
		out := []any{}
		values, err := v.Array().Values()
		if err != nil {
			return out
		}
		for _, item := range values {
			out = append(out, mongoPlain(item))
		}
		return out
	case bsontype.String:
		return v.StringValue()
	case bsontype.Int32:
		return int64(v.Int32())
	case bsontype.Int64:
		return v.Int64()
	case bsontype.Double:
		f := v.Double()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return strconv.FormatFloat(f, 'g', -1, 64)
		}
		return f
	case bsontype.Boolean:
		return v.Boolean()
	case bsontype.Null, bsontype.Undefined:
		return nil
	case bsontype.DateTime:
		return v.Time().UTC().Format(time.RFC3339Nano)
	case bsontype.ObjectID:
		return v.ObjectID().Hex()
	default:
		return mongoValueJSON(v, false)
	}
}

// MongoServerStatus is the server's status as a map of plain values, for the
// routes that hand it on as a list of name and value. The first seven keys
// are the ones it always had; the rest are the counters a chart is drawn
// from, each one level deep so the same map still flattens into rows.
func MongoServerStatus(ctx context.Context, client *mongo.Client) (map[string]any, error) {
	raw, err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "serverStatus", Value: 1}}).Raw()
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	// Only the fields an operator watches are surfaced; serverStatus is
	// hundreds of keys deep and mostly irrelevant here.
	for _, key := range []string{"host", "version", "uptime", "connections", "network", "opcounters", "mem",
		"process", "pid", "uptimeMillis", "asserts"} {
		if v, err := raw.LookupErr(key); err == nil {
			out[key] = mongoPlain(v)
		}
	}
	snap := mongoSnapshot(raw)
	out["timestamp"] = snap.Timestamp
	out["role"] = snap.Role
	out["storageEngine"] = snap.StorageEngine
	out["documents"] = map[string]any{
		"inserted": snap.Documents.Inserted, "returned": snap.Documents.Returned,
		"updated": snap.Documents.Updated, "deleted": snap.Documents.Deleted,
	}
	out["scanned"] = map[string]any{"keys": snap.Scanned.Keys, "documents": snap.Scanned.Documents}
	out["queue"] = map[string]any{
		"activeReaders": snap.Queue.ActiveReaders, "activeWriters": snap.Queue.ActiveWriters,
		"queuedReaders": snap.Queue.QueuedReaders, "queuedWriters": snap.Queue.QueuedWriters,
	}
	if snap.Cache != nil {
		out["cache"] = map[string]any{
			"bytes": snap.Cache.Bytes, "maxBytes": snap.Cache.MaxBytes, "dirtyBytes": snap.Cache.DirtyBytes,
			"pagesRead": snap.Cache.PagesRead, "pagesWritten": snap.Cache.PagesWritten,
		}
	}
	if snap.SetName != "" {
		out["repl"] = map[string]any{"setName": snap.SetName, "role": snap.Role, "primary": snap.Primary}
	}
	return out, nil
}

// MongoServer is one snapshot of a server's counters and gauges.
type MongoServer struct {
	// Timestamp is the server's own clock at the snapshot, in milliseconds
	// since 1970. Rates are (counter₂ − counter₁) / (timestamp₂ − timestamp₁).
	Timestamp int64  `json:"timestamp"`
	Host      string `json:"host"`
	Version   string `json:"version"`
	// Process is mongod or mongos.
	Process       string `json:"process"`
	Uptime        int64  `json:"uptime"`
	StorageEngine string `json:"storageEngine"`
	// Topology is standalone, replicaset or sharded.
	Topology string `json:"topology"`
	// Role is standalone, primary, secondary, arbiter, mongos or other.
	Role    string `json:"role"`
	SetName string `json:"setName,omitempty"`
	Primary string `json:"primary,omitempty"`

	// Opcounters are operations received since start, by kind: insert,
	// query, update, delete, getmore, command.
	Opcounters  map[string]int64 `json:"opcounters"`
	Connections struct {
		Current      int64 `json:"current"`
		Available    int64 `json:"available"`
		Active       int64 `json:"active"`
		TotalCreated int64 `json:"totalCreated"`
	} `json:"connections"`
	Network struct {
		BytesIn     int64 `json:"bytesIn"`
		BytesOut    int64 `json:"bytesOut"`
		NumRequests int64 `json:"numRequests"`
	} `json:"network"`
	// Memory is in mebibytes, as the server reports it.
	Memory struct {
		Resident int64 `json:"resident"`
		Virtual  int64 `json:"virtual"`
	} `json:"memory"`
	// Cache is WiredTiger's, and absent on another storage engine.
	Cache *MongoCache `json:"cache"`
	// Documents are documents inserted, returned, updated and deleted since
	// start.
	Documents struct {
		Inserted int64 `json:"inserted"`
		Returned int64 `json:"returned"`
		Updated  int64 `json:"updated"`
		Deleted  int64 `json:"deleted"`
	} `json:"documents"`
	// Scanned is what queries examined to return those documents. Scanned
	// documents over returned documents is how well queries are targeted.
	Scanned struct {
		Keys      int64 `json:"keys"`
		Documents int64 `json:"documents"`
	} `json:"scanned"`
	// Queue is operations running and waiting right now.
	Queue struct {
		ActiveReaders int64 `json:"activeReaders"`
		ActiveWriters int64 `json:"activeWriters"`
		QueuedReaders int64 `json:"queuedReaders"`
		QueuedWriters int64 `json:"queuedWriters"`
	} `json:"queue"`
	// Latency is total microseconds and operation count per kind since
	// start; the average over an interval is the difference of the first
	// over the difference of the second.
	Latency struct {
		ReadsMicros    int64 `json:"readsMicros"`
		ReadsOps       int64 `json:"readsOps"`
		WritesMicros   int64 `json:"writesMicros"`
		WritesOps      int64 `json:"writesOps"`
		CommandsMicros int64 `json:"commandsMicros"`
		CommandsOps    int64 `json:"commandsOps"`
	} `json:"latency"`
	Cursors struct {
		Open     int64 `json:"open"`
		TimedOut int64 `json:"timedOut"`
	} `json:"cursors"`
	Asserts map[string]int64 `json:"asserts"`
}

// MongoCache is the WiredTiger cache: how full, how dirty, how busy.
type MongoCache struct {
	Bytes        int64 `json:"bytes"`
	MaxBytes     int64 `json:"maxBytes"`
	DirtyBytes   int64 `json:"dirtyBytes"`
	PagesRead    int64 `json:"pagesRead"`
	PagesWritten int64 `json:"pagesWritten"`
}

// MongoServerSnapshot reads the server's counters once.
func MongoServerSnapshot(ctx context.Context, client *mongo.Client) (*MongoServer, error) {
	raw, err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "serverStatus", Value: 1}}).Raw()
	if err != nil {
		return nil, err
	}
	return mongoSnapshot(raw), nil
}

func mongoSnapshot(raw bson.Raw) *MongoServer {
	s := &MongoServer{Opcounters: map[string]int64{}, Asserts: map[string]int64{}}
	s.Host, _ = raw.Lookup("host").StringValueOK()
	s.Version, _ = raw.Lookup("version").StringValueOK()
	s.Process, _ = raw.Lookup("process").StringValueOK()
	s.Uptime = mongoLookupInt(raw, "uptime")
	if t, ok := raw.Lookup("localTime").TimeOK(); ok {
		s.Timestamp = t.UnixMilli()
	} else {
		s.Timestamp = time.Now().UnixMilli()
	}
	s.StorageEngine, _ = raw.Lookup("storageEngine", "name").StringValueOK()

	s.Topology, s.Role = "standalone", "standalone"
	if strings.HasPrefix(s.Process, "mongos") {
		s.Topology, s.Role = "sharded", "mongos"
	} else if repl, ok := raw.Lookup("repl").DocumentOK(); ok {
		s.Topology, s.Role = "replicaset", "other"
		s.SetName, _ = repl.Lookup("setName").StringValueOK()
		s.Primary, _ = repl.Lookup("primary").StringValueOK()
		primary, _ := repl.Lookup("isWritablePrimary").BooleanOK()
		if !primary {
			primary, _ = repl.Lookup("ismaster").BooleanOK()
		}
		secondary, _ := repl.Lookup("secondary").BooleanOK()
		arbiter, _ := repl.Lookup("arbiterOnly").BooleanOK()
		switch {
		case primary:
			s.Role = "primary"
		case secondary:
			s.Role = "secondary"
		case arbiter:
			s.Role = "arbiter"
		}
	}

	mongoIntMap(raw, "opcounters", s.Opcounters)
	mongoIntMap(raw, "asserts", s.Asserts)
	s.Connections.Current = mongoLookupInt(raw, "connections", "current")
	s.Connections.Available = mongoLookupInt(raw, "connections", "available")
	s.Connections.Active = mongoLookupInt(raw, "connections", "active")
	s.Connections.TotalCreated = mongoLookupInt(raw, "connections", "totalCreated")
	s.Network.BytesIn = mongoLookupInt(raw, "network", "bytesIn")
	s.Network.BytesOut = mongoLookupInt(raw, "network", "bytesOut")
	s.Network.NumRequests = mongoLookupInt(raw, "network", "numRequests")
	s.Memory.Resident = mongoLookupInt(raw, "mem", "resident")
	s.Memory.Virtual = mongoLookupInt(raw, "mem", "virtual")
	if cache, ok := raw.Lookup("wiredTiger", "cache").DocumentOK(); ok {
		s.Cache = &MongoCache{
			Bytes:        mongoLookupInt(cache, "bytes currently in the cache"),
			MaxBytes:     mongoLookupInt(cache, "maximum bytes configured"),
			DirtyBytes:   mongoLookupInt(cache, "tracked dirty bytes in the cache"),
			PagesRead:    mongoLookupInt(cache, "pages read into cache"),
			PagesWritten: mongoLookupInt(cache, "pages written from cache"),
		}
	}
	s.Documents.Inserted = mongoLookupInt(raw, "metrics", "document", "inserted")
	s.Documents.Returned = mongoLookupInt(raw, "metrics", "document", "returned")
	s.Documents.Updated = mongoLookupInt(raw, "metrics", "document", "updated")
	s.Documents.Deleted = mongoLookupInt(raw, "metrics", "document", "deleted")
	s.Scanned.Keys = mongoLookupInt(raw, "metrics", "queryExecutor", "scanned")
	s.Scanned.Documents = mongoLookupInt(raw, "metrics", "queryExecutor", "scannedObjects")
	s.Queue.ActiveReaders = mongoLookupInt(raw, "globalLock", "activeClients", "readers")
	s.Queue.ActiveWriters = mongoLookupInt(raw, "globalLock", "activeClients", "writers")
	s.Queue.QueuedReaders = mongoLookupInt(raw, "globalLock", "currentQueue", "readers")
	s.Queue.QueuedWriters = mongoLookupInt(raw, "globalLock", "currentQueue", "writers")
	s.Latency.ReadsMicros = mongoLookupInt(raw, "opLatencies", "reads", "latency")
	s.Latency.ReadsOps = mongoLookupInt(raw, "opLatencies", "reads", "ops")
	s.Latency.WritesMicros = mongoLookupInt(raw, "opLatencies", "writes", "latency")
	s.Latency.WritesOps = mongoLookupInt(raw, "opLatencies", "writes", "ops")
	s.Latency.CommandsMicros = mongoLookupInt(raw, "opLatencies", "commands", "latency")
	s.Latency.CommandsOps = mongoLookupInt(raw, "opLatencies", "commands", "ops")
	s.Cursors.Open = mongoLookupInt(raw, "metrics", "cursor", "open", "total")
	s.Cursors.TimedOut = mongoLookupInt(raw, "metrics", "cursor", "timedOut")
	return s
}

func mongoIntMap(raw bson.Raw, key string, into map[string]int64) {
	doc, ok := raw.Lookup(key).DocumentOK()
	if !ok {
		return
	}
	elems, err := doc.Elements()
	if err != nil {
		return
	}
	for _, e := range elems {
		if v := e.Value(); v.IsNumber() {
			into[e.Key()] = mongoInt(v)
		}
	}
}

// MongoDatabaseStat is one database's size and contents.
type MongoDatabaseStat struct {
	Name        string `json:"name"`
	Collections int64  `json:"collections"`
	Views       int64  `json:"views"`
	Objects     int64  `json:"objects"`
	AvgObjSize  int64  `json:"avgObjSize"`
	DataSize    int64  `json:"dataSize"`
	StorageSize int64  `json:"storageSize"`
	Indexes     int64  `json:"indexes"`
	IndexSize   int64  `json:"indexSize"`
	// SizeOnDisk is what listDatabases reports, present even when dbStats
	// could not be read.
	SizeOnDisk int64 `json:"sizeOnDisk"`
	Empty      bool  `json:"empty"`
	// StatsKnown is false when this account may list the database but not
	// measure it.
	StatsKnown bool `json:"statsKnown"`
}

// mongoStatsDatabases bounds how many databases are measured.
const mongoStatsDatabases = 200

// MongoDatabaseStats lists every database with its dbStats.
func MongoDatabaseStats(ctx context.Context, client *mongo.Client) ([]MongoDatabaseStat, error) {
	res, err := client.ListDatabases(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	out := make([]MongoDatabaseStat, 0, len(res.Databases))
	for _, d := range res.Databases {
		out = append(out, MongoDatabaseStat{Name: d.Name, SizeOnDisk: d.SizeOnDisk, Empty: d.Empty})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	work := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < mongoStatsWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				raw, err := client.Database(out[i].Name).RunCommand(ctx, bson.D{{Key: "dbStats", Value: 1}}).Raw()
				if err != nil {
					continue
				}
				d := &out[i]
				d.StatsKnown = true
				d.Collections = mongoLookupInt(raw, "collections")
				d.Views = mongoLookupInt(raw, "views")
				d.Objects = mongoLookupInt(raw, "objects")
				d.AvgObjSize = mongoLookupInt(raw, "avgObjSize")
				d.DataSize = mongoLookupInt(raw, "dataSize")
				d.StorageSize = mongoLookupInt(raw, "storageSize")
				d.Indexes = mongoLookupInt(raw, "indexes")
				d.IndexSize = mongoLookupInt(raw, "indexSize")
			}
		}()
	}
	for i := range out {
		if i >= mongoStatsDatabases {
			break
		}
		work <- i
	}
	close(work)
	wg.Wait()
	return out, nil
}

// MongoOperation is one operation the server is running.
type MongoOperation struct {
	// OpID is what killOp takes. It is a number on a mongod and
	// "shard:number" through a mongos, so it travels as text.
	OpID string `json:"opId"`
	// Op is the kind: query, insert, update, remove, getmore, command, none.
	Op        string `json:"op"`
	Namespace string `json:"ns"`
	// Command is the command document as relaxed Extended JSON, cut short.
	Command          string  `json:"command"`
	SecondsRunning   float64 `json:"secondsRunning"`
	Client           string  `json:"client"`
	AppName          string  `json:"appName"`
	User             string  `json:"user"`
	Description      string  `json:"desc"`
	Active           bool    `json:"active"`
	WaitingForLock   bool    `json:"waitingForLock"`
	PlanSummary      string  `json:"planSummary"`
	Message          string  `json:"message"`
	KillPending      bool    `json:"killPending"`
	ConnectionID     int64   `json:"connectionId"`
	NumYields        int64   `json:"numYields"`
	CommandTruncated bool    `json:"commandTruncated"`
}

// mongoCommandPreview is how much of a command document a list row carries.
const mongoCommandPreview = 600

// MongoCurrentOps lists what the server is doing. With all set it includes
// idle connections and the server's own background work.
func MongoCurrentOps(ctx context.Context, client *mongo.Client, all bool) ([]MongoOperation, error) {
	cmd := bson.D{{Key: "currentOp", Value: 1}, {Key: "$all", Value: all}}
	raw, err := client.Database("admin").RunCommand(ctx, cmd).Raw()
	if err != nil {
		return nil, err
	}
	inprog, ok := raw.Lookup("inprog").ArrayOK()
	if !ok {
		return []MongoOperation{}, nil
	}
	values, err := inprog.Values()
	if err != nil {
		return nil, err
	}
	out := []MongoOperation{}
	for _, v := range values {
		doc, ok := v.DocumentOK()
		if !ok {
			continue
		}
		op := MongoOperation{OpID: mongoOpID(doc.Lookup("opid"))}
		op.Op, _ = doc.Lookup("op").StringValueOK()
		op.Namespace, _ = doc.Lookup("ns").StringValueOK()
		op.Client, _ = doc.Lookup("client").StringValueOK()
		if op.Client == "" {
			op.Client, _ = doc.Lookup("client_s").StringValueOK()
		}
		op.AppName, _ = doc.Lookup("appName").StringValueOK()
		op.Description, _ = doc.Lookup("desc").StringValueOK()
		op.Active, _ = doc.Lookup("active").BooleanOK()
		op.WaitingForLock, _ = doc.Lookup("waitingForLock").BooleanOK()
		op.PlanSummary, _ = doc.Lookup("planSummary").StringValueOK()
		op.Message, _ = doc.Lookup("msg").StringValueOK()
		op.KillPending, _ = doc.Lookup("killPending").BooleanOK()
		op.ConnectionID = mongoLookupInt(doc, "connectionId")
		op.NumYields = mongoLookupInt(doc, "numYields")
		if micros := mongoLookupInt(doc, "microsecs_running"); micros > 0 {
			op.SecondsRunning = float64(micros) / 1e6
		} else {
			op.SecondsRunning = float64(mongoLookupInt(doc, "secs_running"))
		}
		if users, ok := doc.Lookup("effectiveUsers").ArrayOK(); ok {
			if first, err := users.IndexErr(0); err == nil {
				if u, ok := first.Value().DocumentOK(); ok {
					name, _ := u.Lookup("user").StringValueOK()
					db, _ := u.Lookup("db").StringValueOK()
					op.User = name + "@" + db
				}
			}
		}
		if command, ok := doc.Lookup("command").DocumentOK(); ok {
			// The list this request is building is itself an operation.
			if mongoFirstKey(command) == "currentOp" {
				continue
			}
			op.Command, op.CommandTruncated = mongoCut(mongoRelaxedJSON(command), mongoCommandPreview)
		}
		out = append(out, op)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].SecondsRunning > out[j].SecondsRunning })
	return out, nil
}

// mongoFirstKey is the name of a document's first field, which is what a
// command document is named by.
func mongoFirstKey(doc bson.Raw) string {
	first, err := doc.IndexErr(0)
	if err != nil {
		return ""
	}
	return first.Key()
}

func mongoOpID(v bson.RawValue) string {
	if s, ok := v.StringValueOK(); ok {
		return s
	}
	if v.IsNumber() {
		return strconv.FormatInt(mongoInt(v), 10)
	}
	return ""
}

// mongoCut shortens text to n bytes on a character boundary.
func mongoCut(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	for n > 0 && !isRuneStart(s[n]) {
		n--
	}
	return s[:n] + "…", true
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// MongoKillOp asks the server to stop an operation. The operation ends at
// its next interruption point, which is soon but not at once.
func MongoKillOp(ctx context.Context, client *mongo.Client, opID string) error {
	opID = strings.TrimSpace(opID)
	if opID == "" {
		return fmt.Errorf("an operation id is required")
	}
	var op any = opID
	if n, err := strconv.ParseInt(opID, 10, 64); err == nil {
		op = n
	} else if !strings.Contains(opID, ":") || len(opID) > 128 {
		// Through a mongos an id is "shard:number"; nothing else is one.
		return fmt.Errorf("%q is not an operation id", opID)
	}
	return client.Database("admin").RunCommand(ctx, bson.D{{Key: "killOp", Value: 1}, {Key: "op", Value: op}}).Err()
}

// MongoProfiler is a database's profiler setting.
type MongoProfiler struct {
	Database string `json:"database"`
	// Level is 0 (off), 1 (operations slower than SlowMs) or 2 (everything).
	Level      int     `json:"level"`
	SlowMs     int64   `json:"slowMs"`
	SampleRate float64 `json:"sampleRate"`
	// Filter, when set, replaces SlowMs and SampleRate as what is recorded.
	Filter string `json:"filter,omitempty"`
}

// MongoProfileEntry is one operation the profiler recorded.
type MongoProfileEntry struct {
	Time         string `json:"time"`
	Op           string `json:"op"`
	Namespace    string `json:"ns"`
	Millis       int64  `json:"millis"`
	Command      string `json:"command"`
	PlanSummary  string `json:"planSummary"`
	DocsExamined int64  `json:"docsExamined"`
	KeysExamined int64  `json:"keysExamined"`
	Returned     int64  `json:"returned"`
	Modified     int64  `json:"modified"`
	Deleted      int64  `json:"deleted"`
	Inserted     int64  `json:"inserted"`
	ResponseSize int64  `json:"responseLength"`
	NumYields    int64  `json:"numYields"`
	HasSortStage bool   `json:"hasSortStage"`
	UsedDisk     bool   `json:"usedDisk"`
	Client       string `json:"client"`
	User         string `json:"user"`
	AppName      string `json:"appName"`
	Error        string `json:"error,omitempty"`
	// Truncated says Command is only the beginning of the command.
	Truncated bool `json:"commandTruncated"`
}

// MongoProfile is the profiler's setting and what it has recorded.
type MongoProfile struct {
	MongoProfiler
	// Entries are newest first. The list is empty while the profiler is off
	// and nothing was recorded before.
	Entries []MongoProfileEntry `json:"entries"`
}

// MongoProfilerStatus reads a database's profiler setting.
func MongoProfilerStatus(ctx context.Context, client *mongo.Client, dbName string) (*MongoProfiler, error) {
	if err := mongoDatabaseName(dbName); err != nil {
		return nil, err
	}
	raw, err := client.Database(dbName).RunCommand(ctx, bson.D{{Key: "profile", Value: -1}}).Raw()
	if err != nil {
		return nil, err
	}
	p := &MongoProfiler{Database: dbName, SampleRate: 1}
	p.Level = int(mongoLookupInt(raw, "was"))
	p.SlowMs = mongoLookupInt(raw, "slowms")
	if v, err := raw.LookupErr("sampleRate"); err == nil {
		if f, ok := v.DoubleOK(); ok {
			p.SampleRate = f
		} else if v.IsNumber() {
			p.SampleRate = float64(mongoInt(v))
		}
	}
	if filter, ok := raw.Lookup("filter").DocumentOK(); ok {
		p.Filter = mongoRelaxedJSON(filter)
	}
	return p, nil
}

const (
	mongoProfileEntries    = 100
	mongoProfileMaxEntries = 500
)

// MongoProfileRead reads the profiler setting and the newest operations it
// recorded, optionally only those at least minMillis long.
func MongoProfileRead(ctx context.Context, client *mongo.Client, dbName string, limit int, minMillis int64) (*MongoProfile, error) {
	status, err := MongoProfilerStatus(ctx, client, dbName)
	if err != nil {
		return nil, err
	}
	out := &MongoProfile{MongoProfiler: *status, Entries: []MongoProfileEntry{}}
	if limit <= 0 {
		limit = mongoProfileEntries
	}
	if limit > mongoProfileMaxEntries {
		limit = mongoProfileMaxEntries
	}
	filter := bson.D{}
	if minMillis > 0 {
		filter = bson.D{{Key: "millis", Value: bson.D{{Key: "$gte", Value: minMillis}}}}
	}
	// system.profile is capped, so natural order is insertion order and the
	// reverse of it is newest first without a sort.
	cur, err := client.Database(dbName).Collection("system.profile").Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: "$natural", Value: -1}}).SetLimit(int64(limit)).SetMaxTime(mongoDefaultMaxTime))
	if err != nil {
		// The collection does not exist until the profiler has been on once.
		var cmdErr mongo.CommandError
		if errors.As(err, &cmdErr) && cmdErr.Code == 26 {
			return out, nil
		}
		return nil, err
	}
	defer cur.Close(context.Background())
	for cur.Next(ctx) {
		doc := cur.Current
		e := MongoProfileEntry{
			Millis:       mongoLookupInt(doc, "millis"),
			DocsExamined: mongoLookupInt(doc, "docsExamined"),
			KeysExamined: mongoLookupInt(doc, "keysExamined"),
			Returned:     mongoLookupInt(doc, "nreturned"),
			Modified:     mongoLookupInt(doc, "nModified"),
			Deleted:      mongoLookupInt(doc, "ndeleted"),
			Inserted:     mongoLookupInt(doc, "ninserted"),
			ResponseSize: mongoLookupInt(doc, "responseLength"),
			NumYields:    mongoLookupInt(doc, "numYield"),
		}
		if t, ok := doc.Lookup("ts").TimeOK(); ok {
			e.Time = t.UTC().Format(time.RFC3339Nano)
		}
		e.Op, _ = doc.Lookup("op").StringValueOK()
		e.Namespace, _ = doc.Lookup("ns").StringValueOK()
		e.PlanSummary, _ = doc.Lookup("planSummary").StringValueOK()
		e.HasSortStage, _ = doc.Lookup("hasSortStage").BooleanOK()
		e.UsedDisk, _ = doc.Lookup("usedDisk").BooleanOK()
		e.Client, _ = doc.Lookup("client").StringValueOK()
		e.User, _ = doc.Lookup("user").StringValueOK()
		e.AppName, _ = doc.Lookup("appName").StringValueOK()
		e.Error, _ = doc.Lookup("errMsg").StringValueOK()
		if command, ok := doc.Lookup("command").DocumentOK(); ok {
			e.Command, e.Truncated = mongoCut(mongoRelaxedJSON(command), mongoCommandPreview)
		}
		out.Entries = append(out.Entries, e)
	}
	return out, cur.Err()
}

// MongoProfilerChange is a new profiler setting. A nil field is left alone,
// the level included: a change of the slow threshold that named no level must
// not be read as "level 0" and turn the profiler off.
type MongoProfilerChange struct {
	Level      *int     `json:"level"`
	SlowMs     *int64   `json:"slowMs"`
	SampleRate *float64 `json:"sampleRate"`
}

// MongoSetProfiler changes a database's profiler level, and with it the slow
// threshold — which, unlike the level, the server keeps one of for the whole
// instance and also applies to what it writes to its log.
func MongoSetProfiler(ctx context.Context, client *mongo.Client, dbName string, change MongoProfilerChange) (*MongoProfiler, error) {
	if err := mongoDatabaseName(dbName); err != nil {
		return nil, err
	}
	if change.Level == nil && change.SlowMs == nil && change.SampleRate == nil {
		return nil, fmt.Errorf("nothing to change: send a level, a slow threshold or a sample rate")
	}
	// -1 is the level that asks for the current settings, and the server
	// still applies whatever else the command carries: it is how the
	// threshold is moved with the level left where it is.
	level := -1
	if change.Level != nil {
		if level = *change.Level; level < 0 || level > 2 {
			return nil, fmt.Errorf("profiler level is 0 (off), 1 (slow operations) or 2 (every operation)")
		}
	}
	cmd := bson.D{{Key: "profile", Value: level}}
	if change.SlowMs != nil {
		if *change.SlowMs < 0 {
			return nil, fmt.Errorf("the slow threshold cannot be negative")
		}
		cmd = append(cmd, bson.E{Key: "slowms", Value: *change.SlowMs})
	}
	if change.SampleRate != nil {
		if *change.SampleRate < 0 || *change.SampleRate > 1 {
			return nil, fmt.Errorf("the sample rate is a fraction from 0 to 1")
		}
		cmd = append(cmd, bson.E{Key: "sampleRate", Value: *change.SampleRate})
	}
	if err := client.Database(dbName).RunCommand(ctx, cmd).Err(); err != nil {
		return nil, err
	}
	return MongoProfilerStatus(ctx, client, dbName)
}

// MongoReplicaMember is one member of a replica set.
type MongoReplicaMember struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
	// Health is true when the member answers heartbeats.
	Health bool  `json:"health"`
	Self   bool  `json:"self"`
	Uptime int64 `json:"uptime"`
	// OptimeDate is the time of the last operation the member has applied.
	OptimeDate string `json:"optimeDate,omitempty"`
	// LagSeconds is how far the member's last applied operation is behind
	// the primary's; nil when there is no primary to compare against.
	LagSeconds    *float64 `json:"lagSeconds"`
	PingMs        int64    `json:"pingMs"`
	SyncSource    string   `json:"syncSource,omitempty"`
	LastHeartbeat string   `json:"lastHeartbeat,omitempty"`
	Message       string   `json:"message,omitempty"`
}

// MongoReplication is a replica set as one of its members sees it.
type MongoReplication struct {
	// ReplicaSet is false on a standalone server, where nothing else here is
	// set; Reason then says why in the server's words.
	ReplicaSet bool                 `json:"replicaSet"`
	Reason     string               `json:"reason,omitempty"`
	SetName    string               `json:"setName,omitempty"`
	MyState    string               `json:"myState,omitempty"`
	Members    []MongoReplicaMember `json:"members"`
	// Oplog is how much history the set keeps, which is how long a member
	// can be away and still catch up.
	Oplog *MongoOplog `json:"oplog"`
}

// MongoOplog is the replication log's size and the time it spans.
type MongoOplog struct {
	SizeBytes     int64  `json:"sizeBytes"`
	UsedBytes     int64  `json:"usedBytes"`
	First         string `json:"first,omitempty"`
	Last          string `json:"last,omitempty"`
	WindowSeconds int64  `json:"windowSeconds"`
}

// MongoReplicationStatus reads replSetGetStatus. A server that is not in a
// replica set answers with an error, which is reported as "not one" rather
// than as a failure.
func MongoReplicationStatus(ctx context.Context, client *mongo.Client) (*MongoReplication, error) {
	out := &MongoReplication{Members: []MongoReplicaMember{}}
	raw, err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}}).Raw()
	if err != nil {
		var cmdErr mongo.CommandError
		// NoReplicationEnabled, NotYetInitialized, and a mongos, which has
		// no such command.
		if errors.As(err, &cmdErr) && (cmdErr.Code == 76 || cmdErr.Code == 94 || cmdErr.Code == 59) {
			out.Reason = cmdErr.Message
			return out, nil
		}
		return nil, err
	}
	out = mongoReplicationFrom(raw)
	out.Oplog = mongoOplog(ctx, client)
	return out, nil
}

// mongoReplicationFrom reads a replSetGetStatus reply.
func mongoReplicationFrom(raw bson.Raw) *MongoReplication {
	out := &MongoReplication{ReplicaSet: true, Members: []MongoReplicaMember{}}
	out.SetName, _ = raw.Lookup("set").StringValueOK()
	members, _ := raw.Lookup("members").ArrayOK()
	values, _ := members.Values()
	var primaryOptime time.Time
	// One per member kept, so the two stay in step past an entry that is
	// not a document.
	optimes := []time.Time{}
	for _, v := range values {
		doc, ok := v.DocumentOK()
		if !ok {
			continue
		}
		var optime time.Time
		m := MongoReplicaMember{
			ID: mongoLookupInt(doc, "_id"), Uptime: mongoLookupInt(doc, "uptime"),
			PingMs: mongoLookupInt(doc, "pingMs"), Health: mongoLookupInt(doc, "health") == 1,
		}
		m.Name, _ = doc.Lookup("name").StringValueOK()
		m.State, _ = doc.Lookup("stateStr").StringValueOK()
		m.Self, _ = doc.Lookup("self").BooleanOK()
		m.SyncSource, _ = doc.Lookup("syncSourceHost").StringValueOK()
		m.Message, _ = doc.Lookup("lastHeartbeatMessage").StringValueOK()
		if t, ok := doc.Lookup("optimeDate").TimeOK(); ok {
			m.OptimeDate = t.UTC().Format(time.RFC3339)
			optime = t
			if m.State == "PRIMARY" {
				primaryOptime = t
			}
		}
		if t, ok := doc.Lookup("lastHeartbeat").TimeOK(); ok {
			m.LastHeartbeat = t.UTC().Format(time.RFC3339)
		}
		if m.Self {
			out.MyState = m.State
		}
		out.Members = append(out.Members, m)
		optimes = append(optimes, optime)
	}
	if !primaryOptime.IsZero() {
		for i := range out.Members {
			if !optimes[i].IsZero() {
				lag := math.Max(0, primaryOptime.Sub(optimes[i]).Seconds())
				out.Members[i].LagSeconds = &lag
			}
		}
	}
	return out
}

// mongoOplog measures the replication log. It needs read access to the local
// database, which an application account usually lacks; nil then.
func mongoOplog(ctx context.Context, client *mongo.Client) *MongoOplog {
	local := client.Database("local")
	stats, err := mongoCollStats(ctx, local, "oplog.rs")
	if err != nil {
		return nil
	}
	out := &MongoOplog{SizeBytes: mongoLookupInt(stats, "maxSize"), UsedBytes: mongoLookupInt(stats, "size")}
	edge := func(direction int) (time.Time, bool) {
		raw, err := local.Collection("oplog.rs").FindOne(ctx, bson.D{},
			options.FindOne().SetSort(bson.D{{Key: "$natural", Value: direction}}).
				SetProjection(bson.D{{Key: "ts", Value: 1}})).Raw()
		if err != nil {
			return time.Time{}, false
		}
		t, _, ok := raw.Lookup("ts").TimestampOK()
		return time.Unix(int64(t), 0), ok
	}
	first, okFirst := edge(1)
	last, okLast := edge(-1)
	if okFirst && okLast {
		out.First, out.Last = first.UTC().Format(time.RFC3339), last.UTC().Format(time.RFC3339)
		out.WindowSeconds = int64(last.Sub(first).Seconds())
	}
	return out
}
