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
		"dbHash", "listCommands", "connPoolStats", "replSetGetStatus",
		"replSetGetConfig", "getDefaultRWConcern", "lockInfo", "getMore", "planCacheListFilters",
		"listShards", "balancerStatus", "getClusterParameter", "filemd5")
	add(mongoCommandRule{class: MongoClassRead, inspect: inspectAggregate}, "aggregate")
	add(mongoCommandRule{class: MongoClassRead, inspect: inspectExplain}, "explain")
	add(mongoCommandRule{class: MongoClassRead, inspect: inspectUsersInfo}, "usersInfo")
	add(mongoCommandRule{class: MongoClassRead, inspect: inspectValidate}, "validate")
	add(mongoCommandRule{class: MongoClassRead, inspect: inspectProfile}, "profile")
	add(mongoCommandRule{class: MongoClassRead, inspect: inspectFeatures}, "features")

	add(write, "insert", "create", "planCacheClear", "planCacheSetFilter",
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
		"killOp", "killSessions", "killAllSessions", "killAllSessionsByPattern", "killCursors")
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
	// A field given twice is read as its first value by one server and as its
	// last by another, and refused by a third. Whichever this code read, a
	// server could read the other, so the command is not classified at all.
	fields := map[string]bool{name: true}
	for _, e := range elems[1:] {
		key := e.Key()
		if fields[key] {
			return nil, MongoVerdict{}, fmt.Errorf("the command sets %q twice; say it once", key)
		}
		fields[key] = true
		if strings.HasPrefix(key, "$") {
			return nil, MongoVerdict{}, fmt.Errorf("%q cannot be set on a command here; choose the database in the console instead", key)
		}
		if mongoSessionFields[key] {
			v.Class, v.Known, v.Reason = MongoClassBlocked, true, mongoWhySession
			return cmd, v, nil
		}
	}
	// The command an explain carries is aimed like any other, and the guards
	// read where it is aimed off its fields, so it is held to the same rule.
	if explained, ok := elems[0].Value().DocumentOK(); ok && name == "explain" {
		inner, _ := explained.Elements()
		seen := map[string]bool{}
		for _, e := range inner {
			if seen[e.Key()] {
				return nil, MongoVerdict{}, fmt.Errorf("the command sets %q twice; say it once", e.Key())
			}
			seen[e.Key()] = true
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
// runs rather than on what it is: a command aimed at a collection that holds
// credentials is never run, whatever it would do there. Reading it would
// return password verifiers, and writing it is managing accounts behind the
// server's back.
//
// It reads the command only. What the collections it names are views of is
// asked of the server, by MongoGuardCommandViews, once there is a connection.
func MongoGuardCommand(dbName string, cmd bson.Raw, v *MongoVerdict) {
	if v.Class == MongoClassBlocked {
		return
	}
	block := func(reason string) { v.Class, v.Reason = MongoClassBlocked, reason }
	withheld := func(db, collection string) bool {
		if !mongoHoldsCredentials(db, collection) {
			return false
		}
		block(db + "." + collection + " holds credentials and is not opened from here")
		return true
	}
	guarded := len(mongoCredentialCollections[dbName]) > 0
	// An explain carries the command it explains, and that one is aimed too.
	explained, _ := cmd.Lookup("explain").DocumentOK()
	for _, doc := range []bson.Raw{cmd, explained} {
		first, err := doc.IndexErr(0)
		if err != nil {
			continue
		}
		switch target := first.Value(); target.Type {
		case bsontype.String:
			name := target.StringValue()
			if withheld(dbName, name) {
				return
			}
			// renameCollection and a few others name a collection with its
			// database, from whichever database they are sent to.
			if db, collection, ok := strings.Cut(name, "."); ok && withheld(db, collection) {
				return
			}
			// A view is defined by a document in system.views, and writing
			// that document by hand is making the view without saying so.
			if guarded && name == "system.views" && v.Class != MongoClassRead {
				block(dbName + ".system.views defines the views of a database that holds credentials; it is not written from here")
				return
			}
		case bsontype.Binary:
			// find, count and distinct take a collection's UUID in place of
			// its name, and a UUID says nothing about which collection it is.
			if guarded {
				block("it names a collection by UUID, which in " + dbName + " could be one that holds credentials; name the collection instead")
				return
			}
		}
		for _, key := range []string{"viewOn", "to"} {
			name, ok := doc.Lookup(key).StringValueOK()
			if !ok {
				continue
			}
			if withheld(dbName, name) {
				return
			}
			if db, collection, ok := strings.Cut(name, "."); ok && withheld(db, collection) {
				return
			}
		}
		// An aggregation, and a view's definition, reach other collections
		// through their stages.
		for _, ref := range mongoPipelineCollections(mongoCommandPipeline(doc), 0) {
			if ref.db == "" {
				ref.db = dbName
			}
			if withheld(ref.db, ref.collection) {
				return
			}
		}
	}
}

// mongoCommandPipeline is the pipeline a command carries, as far as it can
// be read.
func mongoCommandPipeline(cmd bson.Raw) []bson.Raw {
	pipeline, ok := cmd.Lookup("pipeline").ArrayOK()
	if !ok {
		return nil
	}
	values, err := pipeline.Values()
	if err != nil {
		return nil
	}
	stages := []bson.Raw{}
	for _, item := range values {
		if stage, ok := item.DocumentOK(); ok {
			stages = append(stages, stage)
		}
	}
	return stages
}

// MongoGuardCommandViews is the half of MongoGuardCommand that needs the
// server: the collections a command names are followed through the views
// they may be, and a command that would end at a credential collection that
// way is refused with ErrMongoWithheld.
func MongoGuardCommandViews(ctx context.Context, client *mongo.Client, dbName string, cmd bson.Raw) error {
	names := []string{}
	explained, _ := cmd.Lookup("explain").DocumentOK()
	for _, doc := range []bson.Raw{cmd, explained} {
		first, err := doc.IndexErr(0)
		if err != nil {
			continue
		}
		if name, ok := first.Value().StringValueOK(); ok {
			names = append(names, name)
		}
		if name, ok := doc.Lookup("viewOn").StringValueOK(); ok {
			names = append(names, name)
		}
		for _, ref := range mongoPipelineCollections(mongoCommandPipeline(doc), 0) {
			if ref.db == "" || ref.db == dbName {
				names = append(names, ref.collection)
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	return mongoGuardRead(ctx, client, dbName, names...)
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

// mongoFlagSet reads a flag so that it cannot be wrong in the dangerous
// direction. Servers differ in what they take for true — MongoDB 7.0 reads a
// Decimal128 as one on usersInfo and a string as one on validate, where an
// older one wanted a boolean — so the only values read as "not set" are the
// ones no server reads as set: null, false, and a zero of any numeric type.
// Everything else counts as set, including what the server would refuse.
func mongoFlagSet(v bson.RawValue) bool {
	switch v.Type {
	case bsontype.Null:
		return false
	case bsontype.Boolean:
		return v.Boolean()
	case bsontype.Int32, bsontype.Int64:
		return mongoInt(v) != 0
	case bsontype.Double:
		return v.Double() != 0
	case bsontype.Decimal128:
		n, _, err := v.Decimal128().BigInt()
		return err != nil || n.Sign() != 0
	}
	return true
}

// mongoHasFlag reports whether a document sets a flag. Every field of that
// name is read, so one given twice is set when either says so.
func mongoHasFlag(doc bson.Raw, key string) bool {
	elems, err := doc.Elements()
	if err != nil {
		return true
	}
	for _, e := range elems {
		if e.Key() == key && mongoFlagSet(e.Value()) {
			return true
		}
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
	// Explaining a pipeline with execution runs it. Current servers refuse to
	// execute one that writes; an older or a compatible one need not, and the
	// console does not leave that line to the server: only the plan of such a
	// pipeline is a read, as on the explain page (mongo_explain.go).
	if strings.EqualFold(mongoFirstKey(inner), "aggregate") && !mongoOnlyPlans(cmd) {
		var pipeline MongoVerdict
		if inspectAggregate(inner, &pipeline); pipeline.Class != "" {
			v.raise(MongoClassDestructive, "it explains a pipeline by running it, and "+pipeline.Reason)
		}
	}
}

// mongoOnlyPlans reports whether an explain asks for the plan alone. Left out,
// the verbosity is allPlansExecution.
func mongoOnlyPlans(cmd bson.Raw) bool {
	word, ok := cmd.Lookup("verbosity").StringValueOK()
	return ok && word == "queryPlanner"
}

func inspectUsersInfo(cmd bson.Raw, v *MongoVerdict) {
	if mongoHasFlag(cmd, "showCredentials") {
		v.raise(MongoClassBlocked, "showCredentials returns password hashes, which this dashboard never does")
	}
}

func inspectValidate(cmd bson.Raw, v *MongoVerdict) {
	for _, key := range []string{"repair", "fixMultikey"} {
		if mongoHasFlag(cmd, key) {
			v.raise(MongoClassDestructive, "with "+key+" it rewrites the collection instead of only checking it")
			return
		}
	}
}

// mongoProfileReads are the fields a profile command can carry and still
// only read. The server applies every other one it is given — slowms,
// sampleRate, filter — whatever the level beside it says.
var mongoProfileReads = map[string]bool{"profile": true, "comment": true, "maxTimeMS": true}

// mongoProfileAsks reports whether a profile level only asks what the
// profiler is set to. The server cuts the level down to an integer and sets
// the profiler when what is left is 0, 1 or 2, so a level has to still be
// negative after that: -0.5 is level 0, and so is NaN, which a conversion to
// an integer here would have read as the most negative number there is.
func mongoProfileAsks(level bson.RawValue) bool {
	switch level.Type {
	case bsontype.Int32, bsontype.Int64:
		return mongoInt(level) < 0
	case bsontype.Double:
		return level.Double() <= -1
	}
	return false
}

func inspectProfile(cmd bson.Raw, v *MongoVerdict) {
	reads := mongoProfileAsks(cmd.Lookup("profile"))
	if elems, err := cmd.Elements(); err == nil {
		for _, e := range elems {
			reads = reads && mongoProfileReads[e.Key()]
		}
	} else {
		reads = false
	}
	if reads {
		return
	}
	v.Class, v.Admin = MongoClassWrite, true
	v.Reason = "it changes the profiler, which the server keeps one slow threshold of for every database"
}

func inspectFeatures(cmd bson.Raw, v *MongoVerdict) {
	if mongoHasFlag(cmd, "oidReset") {
		v.raise(MongoClassWrite, "with oidReset it changes the machine part of the ids the server generates")
	}
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
		// Every q and every multi of the entry is read: one given twice is
		// taken at its widest.
		fields, err := doc.Elements()
		if err != nil {
			v.raise(MongoClassDestructive, "its updates could not be read")
			return
		}
		filtered, everything := false, false
		for _, f := range fields {
			if f.Key() != "q" {
				continue
			}
			filter, ok := f.Value().DocumentOK()
			filtered = true
			everything = everything || !ok || mongoIsEmpty(filter)
		}
		if (everything || !filtered) && mongoHasFlag(doc, "multi") {
			v.raise(MongoClassDestructive, "it updates every document in the collection: multi with an empty filter")
			return
		}
	}
}

func inspectFindAndModify(cmd bson.Raw, v *MongoVerdict) {
	if mongoHasFlag(cmd, "remove") {
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
	if mongoHasFlag(cmd, "dropTarget") {
		v.raise(MongoClassDestructive, "with dropTarget it drops the collection that already has the new name")
	}
}

func inspectFsync(cmd bson.Raw, v *MongoVerdict) {
	if mongoHasFlag(cmd, "lock") {
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
