package dbx

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
	"go.mongodb.org/mongo-driver/mongo"
)

// The command console: one command document, run as written.
//
// A database command is named by its first key, and that key decides what
// running it takes. The table below sorts the commands this dashboard knows
// into four classes, and the rule the classes keep is the one the SQL console
// keeps: the console is never the cheaper way to do what a form gates.
// Deleting documents needs the destructive capability on its own route, so
// `delete` does here; managing accounts needs system.admin there, so
// `createUser` does here.
//
// It fails closed twice over. A command that is not in the table is treated
// as destructive, because nobody has said it is not. And a command whose
// class depends on its arguments — an aggregation that may end in $out, an
// update that may have an empty filter — is destructive whenever the
// arguments cannot be read.

// MongoCommandClass is what running a command takes.
type MongoCommandClass string

const (
	// MongoClassRead changes nothing.
	MongoClassRead MongoCommandClass = "read"
	// MongoClassWrite adds or changes, and can be undone by another write.
	MongoClassWrite MongoCommandClass = "write"
	// MongoClassDestructive removes data, stops work in flight, or is not
	// known to do neither.
	MongoClassDestructive MongoCommandClass = "destructive"
	// MongoClassBlocked is never run from here.
	MongoClassBlocked MongoCommandClass = "blocked"
)

// MongoVerdict is the classification of one command.
type MongoVerdict struct {
	// Command is the command's name: its first key.
	Command string            `json:"command"`
	Class   MongoCommandClass `json:"class"`
	// Admin says the command manages accounts or the server's own settings,
	// and needs system.admin on top of what Class needs.
	Admin bool `json:"admin"`
	// Known is false for a command the table does not list.
	Known bool `json:"known"`
	// Reason says why the class is what it is, when that is not obvious
	// from the command's name.
	Reason string `json:"reason,omitempty"`
	// Target is the collection the command names, when it names one.
	Target string `json:"target,omitempty"`
}

type mongoCommandRule struct {
	class MongoCommandClass
	admin bool
	// reason is given with a blocked or destructive verdict.
	reason string
	// inspect reads the command's arguments and may raise the class.
	inspect func(cmd bson.Raw, v *MongoVerdict)
}

const (
	mongoWhySession = "the console runs each command on a connection of its own, so a session or a transaction cannot span two commands"
	mongoWhyAuth    = "authentication belongs to the saved connection, not to a command"
	mongoWhyReplSet = "it reconfigures the replica set, which is not done from this console"
	mongoWhyScript  = "it runs arbitrary JavaScript on the server"
)

var mongoCommands = map[string]mongoCommandRule{}

func init() {
	add := func(rule mongoCommandRule, names ...string) {
		for _, name := range names {
			mongoCommands[name] = rule
		}
	}
	read := mongoCommandRule{class: MongoClassRead}
	write := mongoCommandRule{class: MongoClassWrite}

	add(read, "find", "count", "distinct", "listCollections", "listIndexes", "listDatabases",
		"collStats", "collstats", "dbStats", "dbstats", "serverStatus", "buildInfo", "buildinfo",
		"hello", "isMaster", "ismaster", "getParameter", "connectionStatus", "rolesInfo", "top",
		"ping", "hostInfo", "getCmdLineOpts", "getLog", "whatsmyuri", "currentOp", "dataSize",
		"dbHash", "listCommands", "features", "connPoolStats", "replSetGetStatus",
		"replSetGetConfig", "getDefaultRWConcern", "lockInfo", "getMore", "planCacheListFilters",
		"listShards", "balancerStatus", "getClusterParameter", "filemd5")
	add(mongoCommandRule{class: MongoClassRead, inspect: inspectAggregate}, "aggregate")
	add(mongoCommandRule{class: MongoClassRead, inspect: inspectExplain}, "explain")
	add(mongoCommandRule{class: MongoClassRead, inspect: inspectUsersInfo}, "usersInfo")
	add(mongoCommandRule{class: MongoClassRead, inspect: inspectValidate}, "validate")
	add(mongoCommandRule{class: MongoClassRead, inspect: inspectProfile}, "profile")

	add(write, "insert", "create", "killCursors", "planCacheClear", "planCacheSetFilter",
		"planCacheClearFilters", "fsyncUnlock", "setIndexCommitQuorum",
		"createSearchIndexes", "updateSearchIndex")
	add(mongoCommandRule{class: MongoClassWrite, inspect: inspectUpdate}, "update")
	add(mongoCommandRule{class: MongoClassWrite, inspect: inspectFindAndModify}, "findAndModify", "findandmodify")
	add(mongoCommandRule{class: MongoClassWrite, inspect: inspectCreateIndexes}, "createIndexes")
	add(mongoCommandRule{class: MongoClassWrite, inspect: inspectCollMod}, "collMod")
	add(mongoCommandRule{class: MongoClassWrite, inspect: inspectRename}, "renameCollection")
	add(mongoCommandRule{class: MongoClassWrite, inspect: inspectFsync}, "fsync")
	// Accounts and server-wide settings: the forms that do the same need
	// system.admin.
	add(mongoCommandRule{class: MongoClassWrite, admin: true},
		"createUser", "updateUser", "grantRolesToUser", "revokeRolesFromUser", "createRole",
		"updateRole", "grantPrivilegesToRole", "revokePrivilegesFromRole", "grantRolesToRole",
		"revokeRolesFromRole", "setDefaultRWConcern", "logRotate", "invalidateUserCache")

	add(mongoCommandRule{class: MongoClassDestructive, reason: "it removes documents"}, "delete")
	add(mongoCommandRule{class: MongoClassDestructive, reason: "it removes a collection and everything in it"}, "drop")
	add(mongoCommandRule{class: MongoClassDestructive, reason: "it removes indexes"},
		"dropIndexes", "deleteIndexes", "dropSearchIndex")
	add(mongoCommandRule{class: MongoClassDestructive, reason: "it stops work in flight"},
		"killOp", "killSessions", "killAllSessions", "killAllSessionsByPattern")
	add(mongoCommandRule{class: MongoClassDestructive, reason: "it rewrites the collection's data on disk and blocks it while it does"},
		"compact", "reIndex")
	add(mongoCommandRule{class: MongoClassDestructive, reason: "it replaces the collection with a capped copy, dropping what does not fit"},
		"convertToCapped", "cloneCollectionAsCapped")
	add(mongoCommandRule{class: MongoClassDestructive, admin: true, reason: "it removes a whole database"}, "dropDatabase")
	add(mongoCommandRule{class: MongoClassDestructive, admin: true, reason: "it changes how the running server behaves"},
		"setParameter", "setClusterParameter", "setFeatureCompatibilityVersion", "dropConnections")
	add(mongoCommandRule{class: MongoClassDestructive, admin: true, reason: "it removes accounts or roles"},
		"dropUser", "dropRole", "dropAllUsersFromDatabase", "dropAllRolesFromDatabase")

	add(mongoCommandRule{class: MongoClassBlocked, reason: "it stops the server; use the database's power controls"}, "shutdown")
	add(mongoCommandRule{class: MongoClassBlocked, reason: mongoWhyReplSet},
		"replSetReconfig", "replSetInitiate", "replSetStepDown", "replSetStepUp", "replSetFreeze",
		"replSetMaintenance", "replSetSyncFrom", "replSetResizeOplog", "replSetAbortPrimaryCatchUp",
		"replSetRequestVotes", "replSetHeartbeat", "replSetUpdatePosition", "resync")
	add(mongoCommandRule{class: MongoClassBlocked, reason: mongoWhyScript},
		"eval", "$eval", "mapReduce", "mapreduce", "group")
	add(mongoCommandRule{class: MongoClassBlocked, reason: "it replays raw oplog entries, which bypasses every check a write goes through"},
		"applyOps", "godinsert")
	add(mongoCommandRule{class: MongoClassBlocked, reason: "it is a test command"}, "sleep", "configureFailPoint", "emptycapped")
	add(mongoCommandRule{class: MongoClassBlocked, reason: mongoWhyAuth},
		"authenticate", "saslStart", "saslContinue", "logout", "getnonce", "copydbgetnonce")
	add(mongoCommandRule{class: MongoClassBlocked, reason: mongoWhySession},
		"startSession", "endSessions", "refreshSessions", "commitTransaction", "abortTransaction",
		"prepareTransaction")
}

// mongoSessionFields are the fields that tie a command to a session or a
// transaction. The driver adds its own; a command carrying them would be
// asking for one the console cannot keep.
var mongoSessionFields = map[string]bool{"lsid": true, "txnNumber": true, "startTransaction": true, "autocommit": true}

// MongoClassifyCommand parses a command document and classifies it. The
// parsed command is returned so what is run is exactly what was classified.
func MongoClassifyCommand(text string) (bson.Raw, MongoVerdict, error) {
	cmd, err := mongoParseDocument("command", text)
	if err != nil {
		return nil, MongoVerdict{}, err
	}
	elems, err := cmd.Elements()
	if err != nil || len(elems) == 0 {
		return nil, MongoVerdict{}, fmt.Errorf(`the command is empty; a command is a document whose first field names it, like { "ping": 1 }`)
	}
	name := elems[0].Key()
	v := MongoVerdict{Command: name}
	if target, ok := elems[0].Value().StringValueOK(); ok {
		v.Target = target
	}
	for _, e := range elems[1:] {
		key := e.Key()
		if strings.HasPrefix(key, "$") {
			return nil, MongoVerdict{}, fmt.Errorf("%q cannot be set on a command here; choose the database in the console instead", key)
		}
		if mongoSessionFields[key] {
			v.Class, v.Known, v.Reason = MongoClassBlocked, true, mongoWhySession
			return cmd, v, nil
		}
	}
	rule, ok := mongoCommands[name]
	switch {
	case ok:
		v.Class, v.Admin, v.Known, v.Reason = rule.class, rule.admin, true, rule.reason
		if rule.inspect != nil {
			rule.inspect(cmd, &v)
		}
	case strings.HasPrefix(name, "_"):
		v.Class, v.Known = MongoClassBlocked, true
		v.Reason = "it is one of the server's internal commands"
	default:
		// The server matches command names exactly, so a name that differs
		// from a known one only in case would be refused there anyway.
		// Saying so here beats classifying a typo as destructive.
		if known := mongoCommandSpelling(name); known != "" {
			return nil, MongoVerdict{}, fmt.Errorf("there is no command %q; command names are case-sensitive — did you mean %q?", name, known)
		}
		v.Class = MongoClassDestructive
		v.Reason = "this dashboard does not know the command, so it is treated as one that cannot be undone"
	}
	return cmd, v, nil
}

// MongoGuardCommand applies the one rule that depends on where a command
// runs rather than on what it is: in the admin database, a command aimed at
// a collection that holds credentials is never run, whatever it would do
// there. Reading it would return password verifiers, and writing it is
// managing accounts behind the server's back.
func MongoGuardCommand(dbName string, cmd bson.Raw, v *MongoVerdict) {
	if dbName != "admin" || v.Class == MongoClassBlocked {
		return
	}
	block := func(collection string) {
		v.Class = MongoClassBlocked
		v.Reason = "admin." + collection + " holds credentials and is not opened from here"
	}
	target := strings.TrimPrefix(v.Target, "admin.")
	if mongoCredentialCollections[target] {
		block(target)
		return
	}
	// An aggregation reaches other collections through its stages, and an
	// explain carries the command it explains.
	explained, _ := cmd.Lookup("explain").DocumentOK()
	for _, doc := range []bson.Raw{cmd, explained} {
		pipeline, ok := doc.Lookup("pipeline").ArrayOK()
		if !ok {
			continue
		}
		values, err := pipeline.Values()
		if err != nil {
			continue
		}
		stages := []bson.Raw{}
		for _, item := range values {
			if stage, ok := item.DocumentOK(); ok {
				stages = append(stages, stage)
			}
		}
		for _, name := range mongoPipelineCollections(stages, 0) {
			if mongoCredentialCollections[name] {
				block(name)
				return
			}
		}
	}
}

func mongoCommandSpelling(name string) string {
	lower := strings.ToLower(name)
	matches := []string{}
	for known := range mongoCommands {
		if strings.ToLower(known) == lower {
			matches = append(matches, known)
		}
	}
	if len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return matches[0]
}

// MongoCommandNames lists the commands the console knows, with their class,
// for an editor to complete from.
func MongoCommandNames() []MongoVerdict {
	out := make([]MongoVerdict, 0, len(mongoCommands))
	for name, rule := range mongoCommands {
		out = append(out, MongoVerdict{Command: name, Class: rule.class, Admin: rule.admin, Known: true, Reason: rule.reason})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Command < out[j].Command })
	return out
}

// mongoTruthy reads a flag the way the server does: true, or any number but
// zero.
func mongoTruthy(v bson.RawValue) bool {
	switch v.Type {
	case bsontype.Boolean:
		return v.Boolean()
	case bsontype.Int32, bsontype.Int64:
		return mongoInt(v) != 0
	case bsontype.Double:
		return v.Double() != 0
	}
	return false
}

func (v *MongoVerdict) raise(class MongoCommandClass, reason string) {
	v.Class, v.Reason = class, reason
}

func inspectAggregate(cmd bson.Raw, v *MongoVerdict) {
	pipeline, ok := cmd.Lookup("pipeline").ArrayOK()
	if !ok {
		v.raise(MongoClassDestructive, "its pipeline could not be read")
		return
	}
	values, err := pipeline.Values()
	if err != nil {
		v.raise(MongoClassDestructive, "its pipeline could not be read")
		return
	}
	stages := make([]bson.Raw, 0, len(values))
	for _, item := range values {
		doc, ok := item.DocumentOK()
		if !ok {
			v.raise(MongoClassDestructive, "its pipeline could not be read")
			return
		}
		stages = append(stages, doc)
	}
	info := mongoClassifyStages(stages)
	switch {
	case info.Writes:
		v.raise(MongoClassDestructive, "its pipeline holds "+info.WriteStage+", which writes a collection")
	case len(info.Unknown) > 0:
		v.raise(MongoClassDestructive, "its pipeline holds "+info.Unknown[0]+", which this dashboard does not know to be a read")
	}
}

// mongoExplainable are the commands explain takes. Explaining a write plans
// it and, with execution, counts what it would touch; it does not write.
var mongoExplainable = map[string]bool{
	"find": true, "aggregate": true, "count": true, "distinct": true,
	"update": true, "delete": true, "findAndModify": true, "findandmodify": true,
}

func inspectExplain(cmd bson.Raw, v *MongoVerdict) {
	inner, ok := cmd.Lookup("explain").DocumentOK()
	if !ok || !mongoExplainable[mongoFirstKey(inner)] {
		v.raise(MongoClassDestructive, "what it explains could not be read")
		return
	}
	if target, ok := inner.Index(0).Value().StringValueOK(); ok {
		v.Target = target
	}
}

func inspectUsersInfo(cmd bson.Raw, v *MongoVerdict) {
	if flag, err := cmd.LookupErr("showCredentials"); err == nil && mongoTruthy(flag) {
		v.raise(MongoClassBlocked, "showCredentials returns password hashes, which this dashboard never does")
	}
}

func inspectValidate(cmd bson.Raw, v *MongoVerdict) {
	for _, key := range []string{"repair", "fixMultikey"} {
		if flag, err := cmd.LookupErr(key); err == nil && mongoTruthy(flag) {
			v.raise(MongoClassDestructive, "with "+key+" it rewrites the collection instead of only checking it")
			return
		}
	}
}

func inspectProfile(cmd bson.Raw, v *MongoVerdict) {
	level := cmd.Lookup("profile")
	if level.IsNumber() && mongoInt(level) < 0 {
		return
	}
	v.Class, v.Admin = MongoClassWrite, true
	v.Reason = "it changes the profiler, which the server keeps one slow threshold of for every database"
}

func inspectUpdate(cmd bson.Raw, v *MongoVerdict) {
	updates, ok := cmd.Lookup("updates").ArrayOK()
	if !ok {
		v.raise(MongoClassDestructive, "its updates could not be read")
		return
	}
	values, err := updates.Values()
	if err != nil {
		v.raise(MongoClassDestructive, "its updates could not be read")
		return
	}
	for _, item := range values {
		doc, ok := item.DocumentOK()
		if !ok {
			v.raise(MongoClassDestructive, "its updates could not be read")
			return
		}
		filter, hasFilter := doc.Lookup("q").DocumentOK()
		multi, err := doc.LookupErr("multi")
		if (!hasFilter || mongoIsEmpty(filter)) && err == nil && mongoTruthy(multi) {
			v.raise(MongoClassDestructive, "it updates every document in the collection: multi with an empty filter")
			return
		}
	}
}

func inspectFindAndModify(cmd bson.Raw, v *MongoVerdict) {
	if flag, err := cmd.LookupErr("remove"); err == nil && mongoTruthy(flag) {
		v.raise(MongoClassDestructive, "with remove it deletes the document it finds")
	}
}

func inspectCreateIndexes(cmd bson.Raw, v *MongoVerdict) {
	indexes, ok := cmd.Lookup("indexes").ArrayOK()
	if !ok {
		v.raise(MongoClassDestructive, "its indexes could not be read")
		return
	}
	values, err := indexes.Values()
	if err != nil {
		v.raise(MongoClassDestructive, "its indexes could not be read")
		return
	}
	for _, item := range values {
		doc, ok := item.DocumentOK()
		if !ok {
			v.raise(MongoClassDestructive, "its indexes could not be read")
			return
		}
		if _, err := doc.LookupErr("expireAfterSeconds"); err == nil {
			v.raise(MongoClassDestructive, "a TTL index starts deleting every document already older than its limit")
			return
		}
	}
}

func inspectCollMod(cmd bson.Raw, v *MongoVerdict) {
	const why = "it can delete documents: a new expiry removes what is already older, and a smaller cap drops the oldest"
	for _, key := range []string{"expireAfterSeconds", "cappedSize", "cappedMax"} {
		if _, err := cmd.LookupErr(key); err == nil {
			v.raise(MongoClassDestructive, why)
			return
		}
	}
	if _, err := cmd.LookupErr("index", "expireAfterSeconds"); err == nil {
		v.raise(MongoClassDestructive, why)
	}
}

func inspectRename(cmd bson.Raw, v *MongoVerdict) {
	if flag, err := cmd.LookupErr("dropTarget"); err == nil && mongoTruthy(flag) {
		v.raise(MongoClassDestructive, "with dropTarget it drops the collection that already has the new name")
	}
}

func inspectFsync(cmd bson.Raw, v *MongoVerdict) {
	if flag, err := cmd.LookupErr("lock"); err == nil && mongoTruthy(flag) {
		v.raise(MongoClassDestructive, "with lock it blocks every write on the server until it is unlocked")
	}
}

// MongoCommandReply is what the server answered.
type MongoCommandReply struct {
	// Canonical is the reply as canonical Extended JSON.
	Canonical string `json:"canonical"`
	// Relaxed is the same reply for display. It is left out of a reply too
	// large to be worth sending twice.
	Relaxed json.RawMessage `json:"relaxed"`
	// Size is the reply's size in BSON bytes.
	Size int `json:"size"`
	// More says the command opened a cursor that had more than its first
	// batch. The rest cannot be fetched from here — the next command runs on
	// another connection — so the cursor was closed; a larger batchSize or a
	// limit on the command brings back what is wanted in one reply.
	More       bool  `json:"more"`
	DurationMs int64 `json:"durationMs"`
}

// mongoReplyDisplayBytes is the reply size past which only the canonical
// form is returned.
const mongoReplyDisplayBytes = 4 << 20

// MongoRunCommand runs a command document against a database. It classifies
// nothing: the caller has, with MongoClassifyCommand, on this same document.
func MongoRunCommand(ctx context.Context, client *mongo.Client, dbName string, cmd bson.Raw) (*MongoCommandReply, error) {
	if err := mongoDatabaseName(dbName); err != nil {
		return nil, err
	}
	db := client.Database(dbName)
	out := &MongoCommandReply{}
	var raw bson.Raw
	run := func(ctx context.Context) error {
		var err error
		if raw, err = db.RunCommand(ctx, cmd).Raw(); err != nil {
			return err
		}
		out.More = mongoCloseReplyCursor(ctx, db, raw)
		return nil
	}
	start := time.Now()
	// One session for the command and for closing the cursor it may leave
	// open: a cursor is closed by the session that opened it. A server
	// without sessions runs the command bare.
	var err error
	if sess, serr := client.StartSession(); serr == nil {
		defer sess.EndSession(context.Background())
		err = mongo.WithSession(ctx, sess, func(sc mongo.SessionContext) error { return run(sc) })
	} else {
		err = run(ctx)
	}
	if err != nil {
		return nil, err
	}
	canonical, err := bson.MarshalExtJSON(raw, true, false)
	if err != nil {
		return nil, fmt.Errorf("the reply could not be read: %w", err)
	}
	out.Canonical, out.Size = string(canonical), len(raw)
	out.DurationMs = time.Since(start).Milliseconds()
	if len(raw) <= mongoReplyDisplayBytes {
		if relaxed, err := mongoDisplayJSON(raw); err == nil {
			out.Relaxed = relaxed
		}
	}
	return out, nil
}

// mongoCloseReplyCursor closes the cursor a reply left open on the server,
// and reports whether there was one. Left alone it would hold its resources
// until the server timed it out, ten minutes later.
func mongoCloseReplyCursor(ctx context.Context, db *mongo.Database, reply bson.Raw) bool {
	cursor, ok := reply.Lookup("cursor").DocumentOK()
	if !ok {
		return false
	}
	id := mongoLookupInt(cursor, "id")
	if id == 0 {
		return false
	}
	ns, _ := cursor.Lookup("ns").StringValueOK()
	if _, collection, ok := strings.Cut(ns, "."); ok {
		_ = db.RunCommand(ctx, bson.D{
			{Key: "killCursors", Value: collection},
			{Key: "cursors", Value: bson.A{id}},
		}).Err()
	}
	return true
}
