package dbx

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

// Whether a pipeline writes is read off the parsed pipeline. Each case here
// is one way the old text search got it wrong, or one way a stage can hide.
func TestPipelineClassification(t *testing.T) {
	for _, c := range []struct {
		name, pipeline string
		writes         bool
		writeStage     string
		unknown        string
	}{
		{"plain read", `[{"$match":{"a":1}},{"$group":{"_id":"$k","n":{"$sum":1}}},{"$sort":{"n":-1}}]`, false, "", ""},
		{"$mergeObjects is an expression, not $merge",
			`[{"$group":{"_id":"$k","doc":{"$mergeObjects":"$$ROOT"}}},{"$replaceRoot":{"newRoot":{"$mergeObjects":["$doc",{"x":1}]}}}]`, false, "", ""},
		{"a field called out", `[{"$project":{"out":1,"merge":"$merge_count","$outer":"x"}}]`, false, "", ""},
		{"the text $out inside a string", `[{"$match":{"note":"\"$out\" and \"$merge\" are stages"}}]`, false, "", ""},
		{"$out", `[{"$match":{}},{"$out":"copy"}]`, true, "$out", ""},
		{"$merge", `[{"$merge":{"into":"copy"}}]`, true, "$merge", ""},
		{"$out spelled with an escape", `[{"$match":{}},{"\u0024out":"copy"}]`, true, "$out", ""},
		{"$out spelled with an escaped letter", `[{"$\u006fut":"copy"}]`, true, "$out", ""},
		{"shell syntax", `[ { $match: { a: 1 } }, { $out: 'copy' } ]`, true, "$out", ""},
		{"$out inside $lookup", `[{"$lookup":{"from":"b","as":"x","pipeline":[{"$out":"copy"}]}}]`, true, "$out", ""},
		{"$merge inside $unionWith", `[{"$unionWith":{"coll":"b","pipeline":[{"$merge":{"into":"c"}}]}}]`, true, "$merge", ""},
		{"$out inside $facet", `[{"$facet":{"a":[{"$count":"n"}],"b":[{"$out":"copy"}]}}]`, true, "$out", ""},
		{"$unionWith by name", `[{"$unionWith":"other"}]`, false, "", ""},
		{"a stage nobody listed", `[{"$match":{}},{"$inventedStage":{}}]`, false, "", "$inventedStage"},
		{"a stage nobody listed, nested", `[{"$lookup":{"from":"b","as":"x","pipeline":[{"$inventedStage":{}}]}}]`, false, "", "$inventedStage"},
		{"a stage nobody listed, in a pipeline said twice",
			`[{"$lookup":{"from":"b","as":"x","pipeline":[],"pipeline":[{"$inventedStage":{}}]}}]`, false, "", "$inventedStage"},
		{"a stage with two operators", `[{"$match":{},"$limit":1}]`, false, "", "a stage that is not a single operator"},
		{"an empty stage", `[{}]`, false, "", "a stage that is not a single operator"},
		{"empty pipeline", `[]`, false, "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			info, err := MongoClassifyPipeline(c.pipeline)
			if err != nil {
				t.Fatalf("classify: %v", err)
			}
			if info.Writes != c.writes || info.WriteStage != c.writeStage {
				t.Errorf("writes = %v (%q), want %v (%q)", info.Writes, info.WriteStage, c.writes, c.writeStage)
			}
			if got := strings.Join(info.Unknown, ","); got != c.unknown {
				t.Errorf("unknown = %q, want %q", got, c.unknown)
			}
			wantDestructive := c.writes || c.unknown != ""
			if info.Destructive() != wantDestructive || MongoWritesInPipeline(c.pipeline) != wantDestructive {
				t.Errorf("destructive = %v / %v, want %v", info.Destructive(), MongoWritesInPipeline(c.pipeline), wantDestructive)
			}
		})
	}
	// What cannot be read is not a read.
	for _, broken := range []string{``, `not json`, `{"$match":{}}`, `[{"$match":`, `[1,2]`} {
		if !MongoWritesInPipeline(broken) {
			t.Errorf("unparseable pipeline %q was classified as a read", broken)
		}
		if _, err := MongoClassifyPipeline(broken); err == nil {
			t.Errorf("unparseable pipeline %q classified without an error", broken)
		}
	}
	info, _ := MongoClassifyPipeline(`[{"$match":{}},{"$group":{"_id":1}},{"$out":"x"}]`)
	if strings.Join(info.Stages, ",") != "$match,$group,$out" {
		t.Errorf("stages = %v", info.Stages)
	}
}

// The console's table. Each row is a command, the class it must get and
// whether it needs system.admin; the rule the rows keep is that the console
// is never the cheaper way to do what a form gates.
func TestCommandClassification(t *testing.T) {
	for _, c := range []struct {
		command string
		class   MongoCommandClass
		admin   bool
	}{
		// Reads.
		{`{ find: "c", filter: { a: 1 } }`, MongoClassRead, false},
		{`{ count: "c" }`, MongoClassRead, false},
		{`{ distinct: "c", key: "k" }`, MongoClassRead, false},
		{`{ listCollections: 1 }`, MongoClassRead, false},
		{`{ listIndexes: "c" }`, MongoClassRead, false},
		{`{ collStats: "c" }`, MongoClassRead, false},
		{`{ dbStats: 1 }`, MongoClassRead, false},
		{`{ serverStatus: 1 }`, MongoClassRead, false},
		{`{ buildInfo: 1 }`, MongoClassRead, false},
		{`{ hello: 1 }`, MongoClassRead, false},
		{`{ getParameter: "*" }`, MongoClassRead, false},
		{`{ connectionStatus: 1 }`, MongoClassRead, false},
		{`{ usersInfo: 1 }`, MongoClassRead, false},
		{`{ rolesInfo: 1, showBuiltinRoles: true }`, MongoClassRead, false},
		{`{ top: 1 }`, MongoClassRead, false},
		{`{ validate: "c" }`, MongoClassRead, false},
		{`{ validate: "c", full: true }`, MongoClassRead, false},
		{`{ replSetGetStatus: 1 }`, MongoClassRead, false},
		{`{ profile: -1 }`, MongoClassRead, false},
		{`{ profile: -1, comment: "what is it now" }`, MongoClassRead, false},
		{`{ profile: NumberLong(-1), maxTimeMS: 500 }`, MongoClassRead, false},
		{`{ profile: -1.5 }`, MongoClassRead, false},
		{`{ profile: -Infinity }`, MongoClassRead, false},
		{`{ features: 1 }`, MongoClassRead, false},
		{`{ aggregate: "c", pipeline: [ { $match: {} }, { $group: { _id: null, d: { $mergeObjects: "$$ROOT" } } } ], cursor: {} }`, MongoClassRead, false},
		{`{ explain: { find: "c", filter: {} }, verbosity: "executionStats" }`, MongoClassRead, false},
		{`{ explain: { delete: "c", deletes: [ { q: {}, limit: 0 } ] } }`, MongoClassRead, false},
		// Writes.
		{`{ insert: "c", documents: [ { a: 1 } ] }`, MongoClassWrite, false},
		{`{ update: "c", updates: [ { q: { a: 1 }, u: { $set: { b: 2 } }, multi: true } ] }`, MongoClassWrite, false},
		{`{ update: "c", updates: [ { q: {}, u: { $set: { b: 2 } } } ] }`, MongoClassWrite, false},
		{`{ findAndModify: "c", query: { a: 1 }, update: { $set: { b: 2 } } }`, MongoClassWrite, false},
		{`{ createIndexes: "c", indexes: [ { key: { a: 1 }, name: "a_1" } ] }`, MongoClassWrite, false},
		{`{ create: "c", capped: true, size: 1024 }`, MongoClassWrite, false},
		{`{ collMod: "c", validator: {} }`, MongoClassWrite, false},
		{`{ collMod: "c", index: { name: "a_1", hidden: true } }`, MongoClassWrite, false},
		{`{ renameCollection: "d.a", to: "d.b" }`, MongoClassWrite, false},
		{`{ fsync: 1 }`, MongoClassWrite, false},
		{`{ features: 1, oidReset: 1 }`, MongoClassWrite, false},
		// Destructive by what they are.
		{`{ delete: "c", deletes: [ { q: { a: 1 }, limit: 1 } ] }`, MongoClassDestructive, false},
		{`{ drop: "c" }`, MongoClassDestructive, false},
		{`{ dropIndexes: "c", index: "a_1" }`, MongoClassDestructive, false},
		{`{ killOp: 1, op: 5 }`, MongoClassDestructive, false},
		{`{ compact: "c" }`, MongoClassDestructive, false},
		{`{ reIndex: "c" }`, MongoClassDestructive, false},
		// Destructive by what they carry.
		{`{ aggregate: "c", pipeline: [ { $out: "x" } ], cursor: {} }`, MongoClassDestructive, false},
		{`{ aggregate: "c", pipeline: [ { $inventedStage: {} } ], cursor: {} }`, MongoClassDestructive, false},
		{`{ aggregate: "c", pipeline: "nope", cursor: {} }`, MongoClassDestructive, false},
		{`{ aggregate: "c", cursor: {} }`, MongoClassDestructive, false},
		{`{ update: "c", updates: [ { q: {}, u: { $set: { b: 2 } }, multi: true } ] }`, MongoClassDestructive, false},
		{`{ update: "c", updates: [ { q: { a: 1 }, u: {} }, { u: { $unset: { b: 1 } }, multi: 1 } ] }`, MongoClassDestructive, false},
		{`{ update: "c" }`, MongoClassDestructive, false},
		{`{ update: "c", updates: [ 5 ] }`, MongoClassDestructive, false},
		{`{ findAndModify: "c", query: { a: 1 }, remove: true }`, MongoClassDestructive, false},
		{`{ createIndexes: "c", indexes: [ { key: { at: 1 }, name: "ttl", expireAfterSeconds: 60 } ] }`, MongoClassDestructive, false},
		{`{ createIndexes: "c" }`, MongoClassDestructive, false},
		{`{ collMod: "c", index: { name: "ttl", expireAfterSeconds: 60 } }`, MongoClassDestructive, false},
		{`{ collMod: "c", cappedSize: 1024 }`, MongoClassDestructive, false},
		{`{ renameCollection: "d.a", to: "d.b", dropTarget: true }`, MongoClassDestructive, false},
		{`{ fsync: 1, lock: true }`, MongoClassDestructive, false},
		{`{ validate: "c", repair: true }`, MongoClassDestructive, false},
		{`{ explain: { insert: "c", documents: [] } }`, MongoClassDestructive, false},
		// Explaining a pipeline with execution runs it, so one that writes, or
		// holds a stage nobody here knows, is a read only when it is planned
		// and nothing more. The verbosity left out is allPlansExecution.
		{`{ explain: { aggregate: "c", pipeline: [ { $out: "x" } ], cursor: {} }, verbosity: "executionStats" }`, MongoClassDestructive, false},
		{`{ explain: { aggregate: "c", pipeline: [ { $match: {} }, { $merge: { into: "x" } } ], cursor: {} } }`, MongoClassDestructive, false},
		{`{ explain: { aggregate: "c", pipeline: [ { $futureStage: {} } ], cursor: {} }, verbosity: "allPlansExecution" }`, MongoClassDestructive, false},
		{`{ explain: { aggregate: "c", pipeline: [ { $out: "x" } ], cursor: {} }, verbosity: 1 }`, MongoClassDestructive, false},
		{`{ explain: { aggregate: "c", pipeline: [ { $out: "x" } ], cursor: {} }, verbosity: "queryPlanner" }`, MongoClassRead, false},
		{`{ explain: { aggregate: "c", pipeline: [ { $match: { a: 1 } } ], cursor: {} }, verbosity: "executionStats" }`, MongoClassRead, false},
		{`{ explain: { aggregate: "c", pipeline: [ { $group: { _id: "$a" } } ], cursor: {} } }`, MongoClassRead, false},
		{`{ explain: "c" }`, MongoClassDestructive, false},
		// What the forms keep to administrators.
		{`{ createUser: "u", pwd: "p", roles: [] }`, MongoClassWrite, true},
		{`{ updateUser: "u", pwd: "p" }`, MongoClassWrite, true},
		{`{ grantRolesToUser: "u", roles: [ "read" ] }`, MongoClassWrite, true},
		{`{ revokeRolesFromUser: "u", roles: [ "read" ] }`, MongoClassWrite, true},
		{`{ createRole: "r", privileges: [], roles: [] }`, MongoClassWrite, true},
		{`{ profile: 2, slowms: 50 }`, MongoClassWrite, true},
		{`{ profile: 0 }`, MongoClassWrite, true},
		// The server applies what rides beside the level, whatever the level
		// is: -1 with a threshold moves the threshold.
		{`{ profile: -1, slowms: 101 }`, MongoClassWrite, true},
		{`{ profile: -1, sampleRate: 0.5 }`, MongoClassWrite, true},
		{`{ profile: -1, filter: { millis: { $gt: 5 } } }`, MongoClassWrite, true},
		{`{ profile: -1, filter: "unset" }`, MongoClassWrite, true},
		{`{ profile: -1, somethingNew: 1 }`, MongoClassWrite, true},
		{`{ profile: NumberDecimal("-1") }`, MongoClassWrite, true},
		{`{ profile: "-1" }`, MongoClassWrite, true},
		{`{ profile: -0.5 }`, MongoClassWrite, true},
		// The server reads a level that is not a number as 0, which turns the
		// profiler off (seen on 7.0).
		{`{ profile: NaN }`, MongoClassWrite, true},
		{`{ profile: Infinity }`, MongoClassWrite, true},
		{`{ dropUser: "u" }`, MongoClassDestructive, true},
		{`{ dropRole: "r" }`, MongoClassDestructive, true},
		{`{ dropDatabase: 1 }`, MongoClassDestructive, true},
		{`{ setParameter: 1, logLevel: 2 }`, MongoClassDestructive, true},
		{`{ setFeatureCompatibilityVersion: "7.0" }`, MongoClassDestructive, true},
		// Never.
		{`{ shutdown: 1 }`, MongoClassBlocked, false},
		{`{ replSetReconfig: {} }`, MongoClassBlocked, false},
		{`{ replSetStepDown: 60 }`, MongoClassBlocked, false},
		{`{ replSetInitiate: {} }`, MongoClassBlocked, false},
		{`{ eval: "db.c.drop()" }`, MongoClassBlocked, false},
		{`{ $eval: "1" }`, MongoClassBlocked, false},
		{`{ mapReduce: "c", map: "function(){}", reduce: "function(){}", out: "x" }`, MongoClassBlocked, false},
		{`{ applyOps: [] }`, MongoClassBlocked, false},
		{`{ usersInfo: 1, showCredentials: true }`, MongoClassBlocked, false},
		{`{ _configsvrAddShard: "x" }`, MongoClassBlocked, false},
		{`{ startSession: 1 }`, MongoClassBlocked, false},
		{`{ find: "c", lsid: { id: UUID("00112233-4455-6677-8899-aabbccddeeff") } }`, MongoClassBlocked, false},
		{`{ insert: "c", documents: [ {} ], txnNumber: NumberLong(1), autocommit: false }`, MongoClassBlocked, false},
		{`{ saslStart: 1, mechanism: "PLAIN" }`, MongoClassBlocked, false},
	} {
		cmd, v, err := MongoClassifyCommand(c.command)
		if err != nil {
			t.Errorf("%s: %v", c.command, err)
			continue
		}
		if v.Class != c.class || v.Admin != c.admin || !v.Known {
			t.Errorf("%s\n classified %s (admin %v, known %v), want %s (admin %v)", c.command, v.Class, v.Admin, v.Known, c.class, c.admin)
		}
		if v.Command == "" || mongoFirstKey(cmd) != v.Command {
			t.Errorf("%s: named %q", c.command, v.Command)
		}
		if (v.Class == MongoClassBlocked || v.Class == MongoClassDestructive) && v.Reason == "" {
			t.Errorf("%s: a %s verdict gives no reason", c.command, v.Class)
		}
	}
}

// A flag decides a command's class, and servers do not agree on what a flag
// set looks like: 7.0 takes a Decimal128 for showCredentials and a string for
// repair. So a flag is set unless it is something no server reads as set.
// Every flag the table reads is tried with every spelling of both.
func TestCommandFlagsFailClosed(t *testing.T) {
	set := []string{
		`true`, `1`, `-1`, `NumberLong(1)`, `NumberInt(2)`, `1.5`, `NumberDecimal("1")`, `NumberDecimal("0.1")`,
		`NumberDecimal("NaN")`, `NaN`, `"yes"`, `"true"`, `"false"`, `""`, `{}`, `{ a: 1 }`, `[]`, `[ false ]`,
		`ObjectId("65f1c0ffee0123456789abcd")`, `ISODate("2024-01-01")`, `undefined`,
	}
	unset := []string{`false`, `0`, `NumberLong(0)`, `NumberInt(0)`, `0.0`, `-0.0`, `NumberDecimal("0")`, `NumberDecimal("0.00")`, `NumberDecimal("-0")`, `null`}
	for _, c := range []struct {
		name, command  string // %s is where the flag's value goes
		without, with  MongoCommandClass
		withoutAdmin   bool
		blockedOnAdmin bool
	}{
		{"usersInfo showCredentials", `{ usersInfo: 1, showCredentials: %s }`, MongoClassRead, MongoClassBlocked, false, false},
		{"findAndModify remove", `{ findAndModify: "c", query: { a: 1 }, remove: %s }`, MongoClassWrite, MongoClassDestructive, false, false},
		{"validate repair", `{ validate: "c", repair: %s }`, MongoClassRead, MongoClassDestructive, false, false},
		{"validate fixMultikey", `{ validate: "c", fixMultikey: %s }`, MongoClassRead, MongoClassDestructive, false, false},
		{"fsync lock", `{ fsync: 1, lock: %s }`, MongoClassWrite, MongoClassDestructive, false, false},
		{"renameCollection dropTarget", `{ renameCollection: "d.a", to: "d.b", dropTarget: %s }`, MongoClassWrite, MongoClassDestructive, false, false},
		{"update multi", `{ update: "c", updates: [ { q: {}, u: { $set: { a: 1 } }, multi: %s } ] }`, MongoClassWrite, MongoClassDestructive, false, false},
		{"features oidReset", `{ features: 1, oidReset: %s }`, MongoClassRead, MongoClassWrite, false, false},
	} {
		for _, value := range set {
			text := strings.Replace(c.command, "%s", value, 1)
			_, v, err := MongoClassifyCommand(text)
			if err != nil {
				t.Errorf("%s: %v", text, err)
				continue
			}
			if v.Class != c.with {
				t.Errorf("%s = %s: classified %s, want %s\n %s", c.name, value, v.Class, c.with, text)
			}
		}
		for _, value := range unset {
			text := strings.Replace(c.command, "%s", value, 1)
			_, v, err := MongoClassifyCommand(text)
			if err != nil {
				t.Errorf("%s: %v", text, err)
				continue
			}
			if v.Class != c.without {
				t.Errorf("%s = %s: classified %s, want %s\n %s", c.name, value, v.Class, c.without, text)
			}
		}
	}
	// The values themselves, so a flag added to the table later inherits a
	// reading that has been checked.
	value := func(text string) bson.RawValue {
		v, err := mongoParseValue("flag", text)
		if err != nil {
			t.Fatalf("%s: %v", text, err)
		}
		return v
	}
	for _, text := range set {
		if !mongoFlagSet(value(text)) {
			t.Errorf("mongoFlagSet(%s) = false", text)
		}
	}
	for _, text := range unset {
		if mongoFlagSet(value(text)) {
			t.Errorf("mongoFlagSet(%s) = true", text)
		}
	}
}

// A field given twice is read as its first value here and may be read as its
// last by the server. The command is refused rather than classified by
// either, and inside an update, where it cannot be refused as cheaply, the
// widest reading is the one taken.
func TestCommandFieldsGivenTwice(t *testing.T) {
	for _, text := range []string{
		`{"findAndModify":"c","query":{"a":1},"remove":false,"remove":true}`,
		`{"aggregate":"c","pipeline":[],"pipeline":[{"$out":"x"}],"cursor":{}}`,
		`{"validate":"c","repair":false,"repair":true}`,
		`{"collMod":"c","index":{"name":"a","hidden":true},"index":{"name":"t","expireAfterSeconds":1}}`,
		`{"profile":-1,"profile":2}`,
		`{"find":"c","find":"system.users"}`,
		// The command an explain carries is read by the same guards.
		`{"explain":{"find":"c","find":"system.users"},"verbosity":"executionStats"}`,
		`{"explain":{"aggregate":"c","pipeline":[],"pipeline":[{"$unionWith":"system.users"}],"cursor":{}}}`,
	} {
		if _, v, err := MongoClassifyCommand(text); err == nil || !strings.Contains(err.Error(), "twice") {
			t.Errorf("%s: classified %+v, %v; want it refused for a field given twice", text, v, err)
		}
	}
	for text, want := range map[string]MongoCommandClass{
		`{"update":"c","updates":[{"q":{"a":1},"q":{},"u":{"$set":{"b":1}},"multi":true}]}`:                 MongoClassDestructive,
		`{"update":"c","updates":[{"q":{},"u":{"$set":{"b":1}},"multi":false,"multi":true}]}`:               MongoClassDestructive,
		`{"update":"c","updates":[{"q":{},"u":{"$set":{"b":1}},"multi":true,"multi":false}]}`:               MongoClassDestructive,
		`{"update":"c","updates":[{"q":"everything","u":{"$set":{"b":1}},"multi":true}]}`:                   MongoClassDestructive,
		`{"update":"c","updates":[{"q":{"a":1},"u":{"$set":{"b":1}},"multi":false,"multi":true}]}`:          MongoClassWrite,
		`{"update":"c","updates":[{"q":{},"u":{"$set":{"b":1}},"multi":false},{"q":{"a":1},"multi":true}]}`: MongoClassWrite,
	} {
		if _, v, err := MongoClassifyCommand(text); err != nil || v.Class != want {
			t.Errorf("%s: classified %s, %v; want %s", text, v.Class, err, want)
		}
	}
}

// The collections that hold credentials are not opened: not directly, not
// through a stage that reads another collection, not through the console,
// and not by making a view over them.
func TestCredentialCollectionsAreWithheld(t *testing.T) {
	for ns, withheld := range map[string]bool{
		"admin.system.users": true, "admin.system.keys": true, "local.oplog.rs": true,
		"admin.system.roles": false, "admin.orders": false, "shop.system.users": false,
		"shop.oplog.rs": false, "local.startup_log": false,
	} {
		db, coll, _ := strings.Cut(ns, ".")
		err := mongoNamespace(db, coll)
		if withheld != errors.Is(err, ErrMongoWithheld) {
			t.Errorf("%s: %v, want withheld=%v", ns, err, withheld)
		}
	}
	stages := func(text string) []bson.Raw {
		out, err := mongoParseArray("pipeline", text)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for text, withheld := range map[string]bool{
		`[ { $lookup: { from: "system.users", localField: "a", foreignField: "user", as: "u" } } ]`: true,
		`[ { $unionWith: "system.users" } ]`:                        true,
		`[ { $unionWith: { coll: "system.keys", pipeline: [] } } ]`: true,
		`[ { $graphLookup: { from: "system.users", startWith: "$a", connectFromField: "a", connectToField: "b", as: "x" } } ]`: true,
		`[ { $facet: { a: [ { $lookup: { from: "system.users", as: "u", pipeline: [] } } ] } } ]`:                              true,
		`[ { $lookup: { from: "orders", as: "o", pipeline: [ { $unionWith: "system.users" } ] } } ]`:                           true,
		`[ { $lookup: { from: "orders", localField: "a", foreignField: "b", as: "o" } } ]`:                                     false,
		`[ { $match: { from: "system.users" } } ]`:                                                                             false,
		`[ { $lookup: "system.users" } ]`:                                                                                      false,
		// A field said twice is read at both: which of them a server keeps is
		// the server's business.
		`[{"$lookup":{"from":"orders","from":"system.users","as":"u","pipeline":[]}}]`:                                                     true,
		`[{"$lookup":{"from":"system.keys","from":"orders","as":"u","pipeline":[]}}]`:                                                      true,
		`[{"$lookup":{"from":"orders","as":"u","pipeline":[],"pipeline":[{"$unionWith":"system.users"}]}}]`:                                true,
		`[{"$unionWith":{"coll":"orders","coll":"system.users"}}]`:                                                                         true,
		`[{"$graphLookup":{"from":"orders","from":"system.users","startWith":"$a","connectFromField":"a","connectToField":"b","as":"x"}}]`: true,
		`[{"$merge":{"into":"copy","into":"system.users"}}]`:                                                                               true,
		`[{"$lookup":{"from":"orders","from":"items","as":"u","pipeline":[]}}]`:                                                            false,
		// Writing one is managing accounts behind the server's back.
		`[ { $out: "system.users" } ]`:            true,
		`[ { $merge: "system.users" } ]`:          true,
		`[ { $merge: { into: "system.keys" } } ]`: true,
		`[ { $out: "copy" } ]`:                    false,
	} {
		if err := mongoGuardPipeline("admin", stages(text)); withheld != errors.Is(err, ErrMongoWithheld) {
			t.Errorf("pipeline %s in admin: %v, want withheld=%v", text, err, withheld)
		}
		if err := mongoGuardPipeline("shop", stages(text)); err != nil {
			t.Errorf("pipeline %s in another database: %v", text, err)
		}
	}
	// The replication log is reached from any database: the server takes a
	// { db, coll } in $lookup for it.
	for text, withheld := range map[string]bool{
		`[ { $lookup: { from: { db: "local", coll: "oplog.rs" }, as: "o", pipeline: [] } } ]`:                        true,
		`[ { $facet: { a: [ { $lookup: { from: { db: "local", coll: "oplog.rs" }, as: "o", pipeline: [] } } ] } } ]`: true,
		`[ { $lookup: { from: { db: "admin", coll: "system.users" }, as: "o", pipeline: [] } } ]`:                    true,
		`[ { $out: { db: "admin", coll: "system.users" } } ]`:                                                        true,
		`[ { $merge: { into: { db: "admin", coll: "system.users" } } } ]`:                                            true,
		`[ { $merge: { into: { db: "reports", coll: "daily" } } } ]`:                                                 false,
		`[ { $lookup: { from: { db: "config", coll: "collections" }, as: "o", pipeline: [] } } ]`:                    false,
		`[ { $lookup: { from: "oplog.rs", as: "o", pipeline: [] } } ]`:                                               false,
		`[ { $lookup: { from: "orders", as: "o", pipeline: [ { $match: { ns: "local.oplog.rs" } } ] } } ]`:           false,
		// The database or the collection said twice.
		`[{"$lookup":{"from":{"db":"reports","db":"local","coll":"oplog.rs"},"as":"o","pipeline":[]}}]`:               true,
		`[{"$lookup":{"from":{"db":"local","coll":"startup_log","coll":"oplog.rs"},"as":"o","pipeline":[]}}]`:         true,
		`[{"$lookup":{"from":{"db":"reports","db":"config","coll":"daily","coll":"weekly"},"as":"o","pipeline":[]}}]`: false,
	} {
		if err := mongoGuardPipeline("shop", stages(text)); withheld != errors.Is(err, ErrMongoWithheld) {
			t.Errorf("pipeline %s in shop: %v, want withheld=%v", text, err, withheld)
		}
	}
	if err := mongoGuardPipeline("local", stages(`[ { $unionWith: "oplog.rs" } ]`)); !errors.Is(err, ErrMongoWithheld) {
		t.Errorf("a union with the replication log, in local: %v", err)
	}
	for text, blocked := range map[string]bool{
		`{ find: "system.users" }`:                                                          true,
		`{ count: "system.keys" }`:                                                          true,
		`{ aggregate: "system.users", pipeline: [], cursor: {} }`:                           true,
		`{ aggregate: "orders", pipeline: [ { $unionWith: "system.users" } ], cursor: {} }`: true,
		`{ explain: { find: "system.users" } }`:                                             true,
		`{ explain: { aggregate: "x", pipeline: [ { $lookup: { from: "system.users", as: "u", pipeline: [] } } ], cursor: {} } }`: true,
		`{ insert: "system.users", documents: [ {} ] }`:                true,
		`{ renameCollection: "admin.system.users", to: "admin.copy" }`: true,
		`{ renameCollection: "admin.copy", to: "admin.system.users" }`: true,
		// A view is a second name for what it reads.
		`{ create: "v", viewOn: "system.users", pipeline: [] }`:                                                         true,
		`{ create: "v", viewOn: "system.keys" }`:                                                                        true,
		`{ collMod: "v", viewOn: "system.users", pipeline: [] }`:                                                        true,
		`{ create: "v", viewOn: "orders", pipeline: [ { $unionWith: "system.users" } ] }`:                               true,
		`{ collMod: "v", viewOn: "orders", pipeline: [ { $lookup: { from: "system.keys", as: "k", pipeline: [] } } ] }`: true,
		`{ insert: "system.views", documents: [ { _id: "admin.v", viewOn: "system.users", pipeline: [] } ] }`:           true,
		`{ update: "system.views", updates: [ { q: { _id: "admin.v" }, u: { $set: { viewOn: "system.users" } } } ] }`:   true,
		// find, count and distinct take a UUID in place of a name.
		`{ find: UUID("00112233-4455-6677-8899-aabbccddeeff") }`:                  true,
		`{ count: UUID("00112233-4455-6677-8899-aabbccddeeff") }`:                 true,
		`{ distinct: UUID("00112233-4455-6677-8899-aabbccddeeff"), key: "user" }`: true,
		`{ explain: { find: UUID("00112233-4455-6677-8899-aabbccddeeff") } }`:     true,
		`{ create: "v", viewOn: "orders", pipeline: [] }`:                         false,
		`{ find: "system.views" }`:                                                false,
		`{ find: "system.roles" }`:                                                false,
		`{ usersInfo: 1 }`:                                                        false,
		`{ ping: 1 }`:                                                             false,
	} {
		cmd, v, err := MongoClassifyCommand(text)
		if err != nil {
			t.Fatal(err)
		}
		// The same names mean nothing in another database — unless the
		// command spells the database out, as renameCollection does.
		elsewhere := v
		MongoGuardCommand("shop", cmd, &elsewhere)
		if spelled := strings.HasPrefix(text, `{ renameCollection:`) && blocked; (elsewhere.Class == MongoClassBlocked) != spelled {
			t.Errorf("%s in another database: %s, want blocked=%v", text, elsewhere.Class, spelled)
		}
		MongoGuardCommand("admin", cmd, &v)
		if blocked != (v.Class == MongoClassBlocked) || (blocked && v.Reason == "") {
			t.Errorf("%s in admin: %s (%s), want blocked=%v", text, v.Class, v.Reason, blocked)
		}
	}
	// The replication log, from the database it is in and from any other.
	for _, c := range []struct {
		db, text string
		blocked  bool
	}{
		{"local", `{ find: "oplog.rs" }`, true},
		{"local", `{ aggregate: "oplog.rs", pipeline: [], cursor: {} }`, true},
		{"local", `{ create: "v", viewOn: "oplog.rs", pipeline: [] }`, true},
		{"local", `{ find: UUID("00112233-4455-6677-8899-aabbccddeeff") }`, true},
		{"local", `{ find: "startup_log" }`, false},
		{"shop", `{ aggregate: "c", pipeline: [ { $lookup: { from: { db: "local", coll: "oplog.rs" }, as: "o", pipeline: [] } } ], cursor: {} }`, true},
		{"shop", `{ create: "v", viewOn: "c", pipeline: [ { $lookup: { from: { db: "local", coll: "oplog.rs" }, as: "o", pipeline: [] } } ] }`, true},
		{"shop", `{ find: "oplog.rs" }`, false},
		{"shop", `{ find: UUID("00112233-4455-6677-8899-aabbccddeeff") }`, false},
		{"shop", `{ insert: "system.views", documents: [ {} ] }`, false},
	} {
		cmd, v, err := MongoClassifyCommand(c.text)
		if err != nil {
			t.Fatal(err)
		}
		MongoGuardCommand(c.db, cmd, &v)
		if c.blocked != (v.Class == MongoClassBlocked) || (c.blocked && v.Reason == "") {
			t.Errorf("%s in %s: %s (%s), want blocked=%v", c.text, c.db, v.Class, v.Reason, c.blocked)
		}
	}
	if err := mongoGuardView("admin", "system.users", ""); !errors.Is(err, ErrMongoWithheld) {
		t.Errorf("a view over system.users: %v", err)
	}
	if err := mongoGuardView("admin", "orders", `[ { $unionWith: "system.users" } ]`); !errors.Is(err, ErrMongoWithheld) {
		t.Errorf("a view reaching system.users: %v", err)
	}
	if err := mongoGuardView("admin", "orders", `[ { $match: {} } ]`); err != nil {
		t.Errorf("an ordinary view in admin: %v", err)
	}
}

// A view that already exists is followed to what it reads, through other
// views and through the stages of its pipeline, before it is read.
func TestViewsOverCredentialsAreFollowed(t *testing.T) {
	stages := func(text string) []bson.Raw {
		out, err := mongoParseArray("pipeline", text)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	views := map[string]mongoViewDef{
		"direct":   {on: "system.users"},
		"second":   {on: "direct"},
		"third":    {on: "second", pipeline: stages(`[ { $project: { user: 1 } } ]`)},
		"joined":   {on: "orders", pipeline: stages(`[ { $lookup: { from: "system.keys", as: "k", pipeline: [] } } ]`)},
		"viaView":  {on: "orders", pipeline: stages(`[ { $unionWith: "second" } ]`)},
		"nested":   {on: "orders", pipeline: stages(`[ { $facet: { a: [ { $lookup: { from: "orders", as: "o", pipeline: [ { $unionWith: "direct" } ] } } ] } } ]`)},
		"oplog":    {on: "orders", pipeline: stages(`[ { $lookup: { from: { db: "local", coll: "oplog.rs" }, as: "o", pipeline: [] } } ]`)},
		"clean":    {on: "orders", pipeline: stages(`[ { $match: { paid: true } }, { $lookup: { from: "items", as: "i", pipeline: [] } } ]`)},
		"onClean":  {on: "clean"},
		"loopA":    {on: "loopB"},
		"loopB":    {on: "loopA"},
		"selfJoin": {on: "orders", pipeline: stages(`[ { $unionWith: "selfJoin" } ]`)},
		"broken":   {on: "orders", unreadable: true},
		"onBroken": {on: "broken"},
	}
	for name, withheld := range map[string]bool{
		"system.users": true, "system.keys": true,
		"direct": true, "second": true, "third": true, "joined": true, "viaView": true, "nested": true, "oplog": true,
		"broken": true, "onBroken": true,
		"clean": false, "onClean": false, "orders": false, "nothing-by-this-name": false,
		"loopA": false, "loopB": false, "selfJoin": false,
	} {
		err := mongoViewReaches("admin", name, views, map[string]bool{})
		if withheld != errors.Is(err, ErrMongoWithheld) {
			t.Errorf("admin.%s: %v, want withheld=%v", name, err, withheld)
		}
	}
	// The refusal says what the view is over, so it can be found and dropped.
	err := mongoViewReaches("admin", "third", views, map[string]bool{})
	if err == nil || !strings.Contains(err.Error(), "admin.third is a view over admin.second is a view over admin.direct is a view over admin.system.users") {
		t.Errorf("the refusal reads %v", err)
	}
}

// The routes the first Mongo browser wrote through refuse the credential
// collections before they touch the server, as the newer ones do. A nil
// client is enough to show it: none of them gets as far as using one.
func TestLegacyWritersRefuseCredentialCollections(t *testing.T) {
	ctx := context.Background()
	for name, err := range map[string]error{
		"insert": func() error { _, err := MongoInsert(ctx, nil, "admin", "system.users", `{"user":"x"}`); return err }(),
		"replace": func() error {
			_, err := MongoReplace(ctx, nil, "admin", "system.users", `{"user":"x"}`, `{}`)
			return err
		}(),
		"delete": func() error {
			_, err := MongoDelete(ctx, nil, "admin", "system.users", `{"user":"x"}`, true)
			return err
		}(),
		"drop":   MongoDropCollection(ctx, nil, "admin", "system.keys"),
		"create": MongoCreateCollection(ctx, nil, "admin", "system.users"),
		"import": func() error {
			_, err := MongoImport(ctx, nil, "admin", "system.users", "json", `{"user":"x"}`, true)
			return err
		}(),
		// The streamed import, and the emptying a replacing import does first:
		// refused only at the write, the collection would already be empty.
		"import stream": func() error {
			_, err := MongoImportStream(ctx, nil, "admin", "system.users", strings.NewReader(`{"user":"x"}`), ImportSpec{Format: ImportFormatNDJSON})
			return err
		}(),
		"empty": MongoEmptyCollection(ctx, nil, "admin", "system.users"),
		"count": func() error { _, err := MongoCount(ctx, nil, "admin", "system.users", ``); return err }(),
		"query": func() error { _, err := MongoQuery(ctx, nil, "local", "oplog.rs", MongoFindOptions{}); return err }(),
		"export": func() error {
			_, _, err := MongoExport(ctx, nil, "local", "oplog.rs", MongoFindOptions{}, ExportJSON, nil, 0)
			return err
		}(),
	} {
		if !errors.Is(err, ErrMongoWithheld) {
			t.Errorf("%s: %v, want ErrMongoWithheld", name, err)
		}
	}
}

// A command nobody listed is not assumed to be harmless.
func TestUnknownCommandFailsClosed(t *testing.T) {
	_, v, err := MongoClassifyCommand(`{ someNewCommand: 1, option: true }`)
	if err != nil {
		t.Fatal(err)
	}
	if v.Class != MongoClassDestructive || v.Known || v.Reason == "" {
		t.Errorf("verdict = %+v", v)
	}
}

func TestCommandParsing(t *testing.T) {
	for text, want := range map[string]string{
		``:                            "required",
		`{}`:                          "empty",
		`[ { ping: 1 } ]`:             "must be a document",
		`{ ping: `:                    "command",
		`{ Find: "c" }`:               `did you mean "find"`,
		`{ listcollections: 1 }`:      `did you mean "listCollections"`,
		`{ find: "c", $db: "other" }`: "choose the database",
		`{ ping: 1, $readPreference: { mode: "secondary" } }`: "cannot be set",
	} {
		if _, v, err := MongoClassifyCommand(text); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("MongoClassifyCommand(%q) = %+v, %v; want an error mentioning %q", text, v, err, want)
		}
	}
	// The collection a command names is reported, for the audit trail.
	for text, target := range map[string]string{
		`{ find: "orders" }`:                     "orders",
		`{ explain: { count: "orders" } }`:       "orders",
		`{ ping: 1 }`:                            "",
		`{ renameCollection: "d.a", to: "d.b" }`: "d.a",
	} {
		if _, v, err := MongoClassifyCommand(text); err != nil || v.Target != target {
			t.Errorf("%s: target %q, %v; want %q", text, v.Target, err, target)
		}
	}
	// What is classified is what is returned to be run, types and all.
	cmd, _, err := MongoClassifyCommand(`{ insert: "c", documents: [ { n: NumberLong(5) } ] }`)
	if err != nil {
		t.Fatal(err)
	}
	if n := cmd.Lookup("documents", "0", "n"); n.Type.String() != "64-bit integer" {
		t.Errorf("the parsed command lost a type: %s", cmd)
	}
}

func TestCommandNamesAreListed(t *testing.T) {
	names := MongoCommandNames()
	if len(names) < 100 {
		t.Fatalf("%d commands listed", len(names))
	}
	seen := map[string]MongoVerdict{}
	for i, v := range names {
		if i > 0 && names[i-1].Command >= v.Command {
			t.Errorf("list is not sorted at %q", v.Command)
		}
		seen[v.Command] = v
	}
	if seen["shutdown"].Class != MongoClassBlocked || seen["find"].Class != MongoClassRead || !seen["createUser"].Admin {
		t.Errorf("list entries: %+v %+v %+v", seen["shutdown"], seen["find"], seen["createUser"])
	}
}

// The schema analysis is arithmetic over documents, so it is checked on
// documents built here rather than on a server.
func TestSchemaAnalysis(t *testing.T) {
	a := newMongoSchemaAnalysis()
	for _, ext := range []string{
		`{"_id":1,"name":"a","n":1,"tags":["x","y"],"sub":{"k":true},"items":[{"sku":"s1"},{"sku":"s2","qty":{"$numberLong":"9007199254740993"}}]}`,
		`{"_id":2,"name":"a","n":{"$numberDouble":"2.5"},"tags":[],"sub":{"k":false,"deep":{"x":null}}}`,
		`{"_id":3,"name":"b","n":"three","sub":7,"when":{"$date":"2024-05-01T00:00:00Z"},"oid":{"$oid":"65f1c0ffee0123456789abcd"},"dec":{"$numberDecimal":"1.50"},"nan":{"$numberDouble":"NaN"}}`,
		`{"_id":4,"nested":[[1,2],[3]]}`,
	} {
		var raw bson.Raw
		if err := bson.UnmarshalExtJSON([]byte(ext), false, &raw); err != nil {
			t.Fatal(err)
		}
		a.document(raw)
	}
	fields := a.render()
	byPath := map[string]MongoSchemaField{}
	paths := []string{}
	for _, f := range fields {
		byPath[f.Path] = f
		paths = append(paths, f.Path)
	}
	want := "_id,dec,items,items[],items[].qty,items[].sku,n,name,nan,nested,nested[],nested[][],oid,sub,sub.deep,sub.deep.x,sub.k,tags,tags[],when"
	if got := strings.Join(paths, ","); got != want {
		t.Errorf("paths\n got %s\nwant %s", got, want)
	}
	types := func(path string) string {
		out := []string{}
		for _, ty := range byPath[path].Types {
			out = append(out, ty.Type)
		}
		return strings.Join(out, ",")
	}
	kind := func(path, name string) MongoSchemaType {
		for _, ty := range byPath[path].Types {
			if ty.Type == name {
				return ty
			}
		}
		t.Errorf("%s has no %s among %s", path, name, types(path))
		return MongoSchemaType{}
	}
	if f := byPath["name"]; f.Documents != 3 || f.Presence != 0.75 || f.Depth != 0 {
		t.Errorf("name = %+v", f)
	}
	if got := types("n"); got != "double,int,string" {
		t.Errorf("n types = %s", got)
	}
	if got := types("sub"); got != "object,int" {
		t.Errorf("sub types, most common first = %s", got)
	}
	if top := kind("name", "string").Top; len(top) != 2 || top[0] != (MongoSchemaValue{"a", 2}) || top[1] != (MongoSchemaValue{"b", 1}) {
		t.Errorf("name top values = %+v", top)
	}
	if b := kind("sub.k", "bool"); b.Distinct != 2 || byPath["sub.k"].Presence != 0.5 {
		t.Errorf("sub.k = %+v, presence %v", b, byPath["sub.k"].Presence)
	}
	arr := kind("tags", "array")
	if *arr.MinLength != 0 || *arr.MaxLength != 2 || *arr.AvgLength != 1 {
		t.Errorf("tags lengths = %d..%d avg %v", *arr.MinLength, *arr.MaxLength, *arr.AvgLength)
	}
	if el := byPath["tags[]"]; el.Occurrences != 2 || el.Documents != 1 || el.Name != "[]" {
		t.Errorf("tags[] = %+v", el)
	}
	if sku := byPath["items[].sku"]; sku.Occurrences != 2 || sku.Documents != 1 || sku.Depth != 2 || sku.Name != "sku" {
		t.Errorf("items[].sku = %+v", sku)
	}
	// A 64-bit integer is shown exactly, though it was compared as a float.
	if q := kind("items[].qty", "long"); q.Min != "9007199254740993" || q.Max != "9007199254740993" {
		t.Errorf("qty = %+v", q)
	}
	if got := types("nested[]"); got != "array" || byPath["nested[][]"].Occurrences != 3 {
		t.Errorf("nested arrays: %s, %+v", got, byPath["nested[][]"])
	}
	if w := kind("when", "date"); w.Min != "2024-05-01T00:00:00Z" || w.Avg == nil || *w.Avg != 1714521600000 {
		t.Errorf("when = %+v", w)
	}
	if o := kind("oid", "objectId"); o.Min != "2024-03-13T15:06:39Z" || o.Avg != nil {
		t.Errorf("oid = %+v", o)
	}
	if d := kind("dec", "decimal"); d.Min != "1.50" || *d.Avg != 1.5 {
		t.Errorf("dec = %+v", d)
	}
	// A NaN is counted and described by nothing: it has no order and no mean.
	if n := kind("nan", "double"); n.Count != 1 || n.Min != "" || n.Avg != nil {
		t.Errorf("nan = %+v", n)
	}
	if x := kind("sub.deep.x", "null"); x.Count != 1 {
		t.Errorf("sub.deep.x = %+v", x)
	}

	mongoMarkIndexed(fields, []MongoIndex{
		{Name: "_id_", Keys: []MongoIndexKey{{Field: "_id", Type: "asc"}}},
		{Name: "sku_name", Keys: []MongoIndexKey{{Field: "items.sku", Type: "asc"}, {Field: "name", Type: "desc"}}},
		{Name: "sub_wild", Keys: []MongoIndexKey{{Field: "sub.$**", Type: "asc"}}},
	})
	indexed := []string{}
	for _, f := range fields {
		if f.Indexed {
			indexed = append(indexed, f.Path+"="+strings.Join(f.Indexes, "+"))
		}
	}
	if got := strings.Join(indexed, " "); got != "_id=_id_ items[].sku=sku_name name=sku_name sub.deep=sub_wild sub.deep.x=sub_wild sub.k=sub_wild" {
		t.Errorf("indexed fields = %s", got)
	}
}

// The analysis is bounded: a collection whose keys are data does not answer
// with all of them, and says it stopped.
func TestSchemaAnalysisIsBounded(t *testing.T) {
	a := newMongoSchemaAnalysis()
	doc := bson.D{}
	for i := 0; i < mongoSchemaFields+50; i++ {
		doc = append(doc, bson.E{Key: "user_" + itoa(i), Value: int32(i)})
	}
	raw, _ := bson.Marshal(doc)
	a.document(raw)
	if len(a.render()) != mongoSchemaFields || !a.truncated {
		t.Errorf("%d fields reported, truncated %v", len(a.render()), a.truncated)
	}

	values := newMongoSchemaAnalysis()
	for i := 0; i < mongoSchemaValues+10; i++ {
		one, _ := bson.Marshal(bson.D{{Key: "v", Value: "value-" + itoa(i)}})
		values.document(one)
	}
	ty := values.render()[0].Types[0]
	if ty.Distinct != mongoSchemaValues || !ty.DistinctCapped || len(ty.Top) != mongoSchemaTop {
		t.Errorf("distinct %d, capped %v, top %d", ty.Distinct, ty.DistinctCapped, len(ty.Top))
	}

	// Deeper than the limit is not descended into.
	deep := bson.D{{Key: "leaf", Value: 1}}
	for i := 0; i < mongoSchemaDepth+3; i++ {
		deep = bson.D{{Key: "d", Value: deep}}
	}
	rawDeep, _ := bson.Marshal(deep)
	nested := newMongoSchemaAnalysis()
	nested.document(rawDeep)
	if !nested.truncated {
		t.Error("a document nested past the limit was not reported as truncated")
	}
}
