package dbx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// Live tests for the MongoDB surface. Like the others they skip when the
// server is not there. The database is the one the connection string names,
// so several checkouts can share one server without sharing a database.

func liveMongoDB(t *testing.T) (*mongo.Client, string) {
	t.Helper()
	dsn := os.Getenv("JD_TEST_MONGO_DSN")
	if dsn == "" {
		dsn = "mongodb://127.0.0.1:27017/jdtest"
	}
	client, err := MongoClient(context.Background(), dsn)
	if err != nil {
		t.Skipf("MongoDB unreachable — set JD_TEST_MONGO_DSN to run these (%v)", err)
	}
	t.Cleanup(func() { client.Disconnect(context.Background()) })
	db := "jdtest"
	if info, err := ParseDSN(DriverMongo, dsn); err == nil && info.Database != "" {
		db = info.Database
	}
	return client, db
}

// liveMongoReplicaSet is a second server that is a replica set and that the
// tests may create accounts on. It is optional and separate so the tests
// that need neither run against any MongoDB.
func liveMongoReplicaSet(t *testing.T) (*mongo.Client, string) {
	t.Helper()
	dsn := os.Getenv("JD_TEST_MONGO_RS_DSN")
	if dsn == "" {
		t.Skip("set JD_TEST_MONGO_RS_DSN to a replica set to run this")
	}
	client, err := MongoClient(context.Background(), dsn)
	if err != nil {
		t.Skipf("MongoDB replica set unreachable (%v)", err)
	}
	t.Cleanup(func() { client.Disconnect(context.Background()) })
	db := "jdtest"
	if info, err := ParseDSN(DriverMongo, dsn); err == nil && info.Database != "" {
		db = info.Database
	}
	return client, db
}

func freshCollection(t *testing.T, client *mongo.Client, db, name string) {
	t.Helper()
	_ = MongoDropCollection(context.Background(), client, db, name)
	t.Cleanup(func() { _ = MongoDropCollection(context.Background(), client, db, name) })
}

// rawByID reads a document's stored bytes, bypassing everything under test.
func rawByID(t *testing.T, client *mongo.Client, db, coll string, id any) bson.Raw {
	t.Helper()
	raw, err := client.Database(db).Collection(coll).FindOne(context.Background(), bson.D{{Key: "_id", Value: id}}).Raw()
	if err != nil {
		t.Fatalf("reading %v back: %v", id, err)
	}
	return raw
}

// everyTypeDocument is canonical Extended JSON holding one value of each BSON
// type, in an order that is neither alphabetical nor the grid's.
const everyTypeDocument = `{
  "_id": {"$oid": "65f1c0ffee0123456789abcd"},
  "zeta": {"$numberDouble": "1.0"},
  "long": {"$numberLong": "9007199254740993"},
  "int": {"$numberInt": "7"},
  "decimal": {"$numberDecimal": "19.990"},
  "when": {"$date": {"$numberLong": "1700000000123"}},
  "ref": {"$oid": "65f1c0ffee0123456789abce"},
  "bytes": {"$binary": {"base64": "AQID", "subType": "00"}},
  "uuid": {"$binary": {"base64": "ABEiM0RVZneImaq7zN3u/w==", "subType": "04"}},
  "pattern": {"$regularExpression": {"pattern": "^a", "options": "i"}},
  "stamp": {"$timestamp": {"t": 5, "i": 6}},
  "nan": {"$numberDouble": "NaN"},
  "nothing": null,
  "flag": true,
  "list": [{"$numberInt": "1"}, "two", {"deep": {"$numberLong": "3"}}],
  "alpha": {"z": {"$numberInt": "1"}, "a": {"$numberInt": "2"}},
  "min": {"$minKey": 1},
  "max": {"$maxKey": 1}
}`

func TestLiveMongoDocuments(t *testing.T) {
	client, db := liveMongoDB(t)
	ctx := context.Background()
	const coll = "jd_documents"
	freshCollection(t, client, db, coll)

	t.Run("a document read and saved back unchanged is byte-identical", func(t *testing.T) {
		ins, err := MongoInsertDocuments(ctx, client, db, coll, everyTypeDocument, true)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		if ins.Inserted != 1 || len(ins.InsertedIDs) != 1 || ins.InsertedIDs[0] != `{"$oid":"65f1c0ffee0123456789abcd"}` {
			t.Fatalf("insert result = %+v", ins)
		}
		res, err := MongoFindDocuments(ctx, client, db, coll, MongoFindSpec{}, true)
		if err != nil {
			t.Fatalf("find: %v", err)
		}
		if res.Returned != 1 || res.HasMore {
			t.Fatalf("find returned %d, hasMore %v", res.Returned, res.HasMore)
		}
		doc := res.Documents[0]
		before := rawByID(t, client, db, coll, idValue(t, doc.ID))
		// Field order is the stored order, not alphabetical.
		if !strings.HasPrefix(doc.Canonical, `{"_id":{"$oid":"65f1c0ffee0123456789abcd"},"zeta":`) {
			t.Errorf("field order was not preserved: %s", doc.Canonical)
		}
		if !json.Valid(doc.Relaxed) {
			t.Errorf("relaxed form is not JSON: %s", doc.Relaxed)
		}
		if _, err := MongoReplaceDocument(ctx, client, db, coll, doc.ID, doc.Canonical, doc.Canonical); err != nil {
			t.Fatalf("replace with what was read: %v", err)
		}
		after := rawByID(t, client, db, coll, idValue(t, doc.ID))
		if !bytes.Equal(before, after) {
			t.Errorf("saving a document back changed it\n was %s\n now %s", before, after)
		}
		if res.Count == nil || !res.Count.Exact || res.Count.Value != 1 || res.Count.Scope != "filter" {
			t.Errorf("count = %+v, want exactly 1", res.Count)
		}
	})

	t.Run("an _id of any type addresses its document", func(t *testing.T) {
		// A string of 24 hexadecimal characters is a string, not an ObjectId:
		// the first browser coerced it and could never match such a document.
		for _, id := range []string{
			`"65f1c0ffee0123456789abcd"`, `42`, `{"$numberLong":"42000000000"}`,
			`{"a":{"$numberInt":"1"},"b":"two"}`, `{"$date":{"$numberLong":"1700000000000"}}`,
			`{"$binary":{"base64":"AQID","subType":"00"}}`, `{"$numberDecimal":"1.5"}`,
		} {
			if _, err := MongoInsertDocuments(ctx, client, db, coll, `{"_id":`+id+`,"kind":"typed-id"}`, true); err != nil {
				t.Fatalf("insert _id %s: %v", id, err)
			}
			got, err := MongoGetDocument(ctx, client, db, coll, id)
			if err != nil {
				t.Errorf("get _id %s: %v", id, err)
				continue
			}
			if !sameValue(idValue(t, got.ID), idValue(t, id)) {
				t.Errorf("get _id %s returned %s", id, got.ID)
			}
			if _, err := MongoReplaceDocument(ctx, client, db, coll, id, `{"kind":"typed-id","edited":true}`, ""); err != nil {
				t.Errorf("replace _id %s: %v", id, err)
			}
			res, err := MongoDeleteDocuments(ctx, client, db, coll, MongoDeletion{ID: id})
			if err != nil || res.Deleted != 1 {
				t.Errorf("delete _id %s: %+v, %v", id, res, err)
			}
		}
		if _, err := MongoGetDocument(ctx, client, db, coll, `"no-such-id"`); !errors.Is(err, ErrMongoNotFound) {
			t.Errorf("get of a missing _id: %v, want ErrMongoNotFound", err)
		}
		if _, err := MongoDeleteDocuments(ctx, client, db, coll, MongoDeletion{ID: `"no-such-id"`}); !errors.Is(err, ErrMongoNotFound) {
			t.Errorf("delete of a missing _id: %v, want ErrMongoNotFound", err)
		}
	})

	t.Run("replace notices a document that changed or went away", func(t *testing.T) {
		if _, err := MongoInsertDocuments(ctx, client, db, coll, `{"_id":"guarded","n":1}`, true); err != nil {
			t.Fatal(err)
		}
		read, err := MongoGetDocument(ctx, client, db, coll, `"guarded"`)
		if err != nil {
			t.Fatal(err)
		}
		// Somebody else edits it.
		if _, err := MongoUpdateDocuments(ctx, client, db, coll, MongoUpdate{
			Filter: `{_id: "guarded"}`, Update: `{$inc: {n: 1}}`,
		}); err != nil {
			t.Fatal(err)
		}
		_, err = MongoReplaceDocument(ctx, client, db, coll, `"guarded"`, `{"_id":"guarded","n":100}`, read.Canonical)
		if !errors.Is(err, ErrMongoChanged) {
			t.Errorf("replace over a changed document: %v, want ErrMongoChanged", err)
		}
		if n := rawByID(t, client, db, coll, "guarded").Lookup("n"); mongoInt(n) != 2 {
			t.Errorf("the stale replace was applied: n = %v", n)
		}
		// Reloaded, the same edit goes through.
		fresh, _ := MongoGetDocument(ctx, client, db, coll, `"guarded"`)
		if _, err := MongoReplaceDocument(ctx, client, db, coll, `"guarded"`, `{"_id":"guarded","n":100}`, fresh.Canonical); err != nil {
			t.Errorf("replace over the current document: %v", err)
		}
		if _, err := MongoReplaceDocument(ctx, client, db, coll, `"gone"`, `{"n":1}`, `{"_id":"gone","n":0}`); !errors.Is(err, ErrMongoNotFound) {
			t.Errorf("replace of a missing document: %v, want ErrMongoNotFound", err)
		}
		if _, err := MongoReplaceDocument(ctx, client, db, coll, `"guarded"`, `{"_id":"other","n":1}`, ""); err == nil ||
			!strings.Contains(err.Error(), "cannot be changed") {
			t.Errorf("replace carrying another _id: %v", err)
		}
	})

	// A known set for the query bar.
	const people = "jd_people_query"
	freshCollection(t, client, db, people)
	seed := `[
	  { _id: 1, name: "Ann", age: 31, city: "Cluj", joined: ISODate("2023-01-10") },
	  { _id: 2, name: "bo", age: 24, city: "Iași", joined: ISODate("2024-03-02") },
	  { _id: 3, name: "Cy", age: 45, city: "Cluj", joined: ISODate("2022-07-19") },
	  { _id: 4, name: "Di", age: 38, city: "Brașov", joined: ISODate("2024-06-30") },
	  { _id: 5, name: "eve", age: 29, city: "Cluj", joined: ISODate("2021-11-05") }
	]`
	if res, err := MongoInsertDocuments(ctx, client, db, people, seed, true); err != nil || res.Inserted != 5 {
		t.Fatalf("seeding: %+v, %v", res, err)
	}
	ids := func(res *MongoFindResult) string {
		out := []string{}
		for _, d := range res.Documents {
			out = append(out, d.ID)
		}
		return strings.Join(out, ",")
	}

	t.Run("the query bar: filter, projection, sort, skip, limit, collation, hint", func(t *testing.T) {
		res, err := MongoFindDocuments(ctx, client, db, people, MongoFindSpec{
			Filter: `{ city: 'Cluj', joined: { $gte: ISODate("2022-01-01") } }`,
			Sort:   `{ age: -1 }`, Projection: `{ name: 1 }`,
		}, true)
		if err != nil {
			t.Fatalf("find: %v", err)
		}
		if got := ids(res); got != `{"$numberInt":"3"},{"$numberInt":"1"}` {
			t.Errorf("filtered, sorted ids = %s", got)
		}
		if strings.Contains(res.Documents[0].Canonical, "age") {
			t.Errorf("projection was not applied: %s", res.Documents[0].Canonical)
		}
		if res.Count == nil || res.Count.Value != 2 || !res.Count.Exact {
			t.Errorf("count = %+v, want exactly 2", res.Count)
		}
		if !strings.Contains(res.Statement, people+".find(") || !strings.Contains(res.Statement, ".sort(") {
			t.Errorf("statement = %s", res.Statement)
		}

		page, err := MongoFindDocuments(ctx, client, db, people, MongoFindSpec{Sort: `{_id: 1}`, Skip: 1, Limit: 2}, true)
		if err != nil {
			t.Fatalf("paged find: %v", err)
		}
		if got := ids(page); got != `{"$numberInt":"2"},{"$numberInt":"3"}` || !page.HasMore {
			t.Errorf("page = %s, hasMore %v", got, page.HasMore)
		}
		if page.Count == nil || page.Count.Value != 5 {
			t.Errorf("count of a page is the count of everything: %+v", page.Count)
		}
		last, _ := MongoFindDocuments(ctx, client, db, people, MongoFindSpec{Sort: `{_id: 1}`, Skip: 3, Limit: 2}, false)
		if last.HasMore || last.Returned != 2 || last.Count != nil {
			t.Errorf("last page: returned %d, hasMore %v, count %+v", last.Returned, last.HasMore, last.Count)
		}

		// Without a collation "bo" and "eve" sort after the capitals.
		plain, _ := MongoFindDocuments(ctx, client, db, people, MongoFindSpec{Sort: `{name: 1}`, Projection: `{_id: 1}`}, false)
		folded, err := MongoFindDocuments(ctx, client, db, people, MongoFindSpec{
			Sort: `{name: 1}`, Projection: `{_id: 1}`, Collation: `{ locale: "en", strength: 2 }`,
		}, false)
		if err != nil {
			t.Fatalf("find with collation: %v", err)
		}
		if ids(plain) == ids(folded) {
			t.Errorf("collation changed nothing: %s", ids(folded))
		}
		if got := ids(folded); got != `{"$numberInt":"1"},{"$numberInt":"2"},{"$numberInt":"3"},{"$numberInt":"4"},{"$numberInt":"5"}` {
			t.Errorf("case-insensitive order = %s", got)
		}

		if _, err := MongoFindDocuments(ctx, client, db, people, MongoFindSpec{Hint: "_id_"}, false); err != nil {
			t.Errorf("hint by index name: %v", err)
		}
		if _, err := MongoFindDocuments(ctx, client, db, people, MongoFindSpec{Hint: `{ _id: 1 }`}, false); err != nil {
			t.Errorf("hint by key pattern: %v", err)
		}
		if _, err := MongoFindDocuments(ctx, client, db, people, MongoFindSpec{Hint: "no_such_index"}, false); err == nil {
			t.Error("a hint naming no index was accepted")
		}
		if _, err := MongoFindDocuments(ctx, client, db, people, MongoFindSpec{Filter: `{ age: { $bogus: 1 } }`}, false); err == nil {
			t.Error("an unknown operator was accepted")
		}
		if _, err := MongoFindDocuments(ctx, client, db, people, MongoFindSpec{Filter: `{ age: `}, false); err == nil ||
			!strings.Contains(err.Error(), "filter") {
			t.Errorf("a broken filter: %v", err)
		}
	})

	t.Run("max time is the server's limit, and running past it is told apart", func(t *testing.T) {
		_, err := MongoFindDocuments(ctx, client, db, people, MongoFindSpec{
			Filter: `{ $where: "sleep(400) || true" }`, MaxTimeMS: 100,
		}, false)
		if !MongoTimedOut(err) {
			t.Errorf("a query past its max time: %v, want a timeout", err)
		}
	})

	t.Run("update operators, one and many, with a dry run first", func(t *testing.T) {
		dry, err := MongoUpdateDocuments(ctx, client, db, people, MongoUpdate{
			Filter: `{ city: "Cluj" }`, Update: `{ $set: { region: "CJ" } }`, Many: true, DryRun: true,
		})
		if err != nil || !dry.DryRun || dry.Matched != 3 {
			t.Fatalf("dry run = %+v, %v; want 3 matched", dry, err)
		}
		if n := rawByID(t, client, db, people, int32(1)).Lookup("region"); n.Type != 0 {
			t.Errorf("the dry run wrote: region = %v", n)
		}
		res, err := MongoUpdateDocuments(ctx, client, db, people, MongoUpdate{
			Filter: `{ city: "Cluj" }`, Update: `{ $set: { region: "CJ" }, $inc: { age: NumberInt(1) } }`, Many: true,
		})
		if err != nil || res.Matched != 3 || res.Modified != 3 {
			t.Fatalf("updateMany = %+v, %v", res, err)
		}
		// $inc by an int32 leaves an int32: the operand's type is the one sent.
		if age := rawByID(t, client, db, people, int32(1)).Lookup("age"); age.Type.String() != "32-bit integer" || age.Int32() != 32 {
			t.Errorf("age = %v (%s)", age, age.Type)
		}
		one, err := MongoUpdateDocuments(ctx, client, db, people, MongoUpdate{
			Filter: `{ city: "Cluj" }`, Update: `{ $set: { first: true } }`,
		})
		if err != nil || one.Matched != 1 || one.Modified != 1 {
			t.Errorf("updateOne = %+v, %v", one, err)
		}
		dryOne, _ := MongoUpdateDocuments(ctx, client, db, people, MongoUpdate{
			Filter: `{ city: "Cluj" }`, Update: `{ $set: { x: 1 } }`, DryRun: true,
		})
		if dryOne == nil || dryOne.Matched != 1 {
			t.Errorf("a dry run of updateOne reaches one document: %+v", dryOne)
		}

		pipeline, err := MongoUpdateDocuments(ctx, client, db, people, MongoUpdate{
			Filter: `{ _id: 2 }`, Update: `[ { $set: { upper: { $toUpper: "$name" } } } ]`,
		})
		if err != nil || pipeline.Modified != 1 {
			t.Fatalf("pipeline update = %+v, %v", pipeline, err)
		}
		if s, _ := rawByID(t, client, db, people, int32(2)).Lookup("upper").StringValueOK(); s != "BO" {
			t.Errorf("pipeline update wrote %q", s)
		}

		up, err := MongoUpdateDocuments(ctx, client, db, people, MongoUpdate{
			Filter: `{ _id: 99 }`, Update: `{ $set: { name: "New" } }`, Upsert: true,
		})
		if err != nil || up.UpsertedID != `{"$numberInt":"99"}` {
			t.Errorf("upsert = %+v, %v", up, err)
		}

		arr, err := MongoUpdateDocuments(ctx, client, db, coll, MongoUpdate{
			Filter: `{ _id: { $oid: "65f1c0ffee0123456789abcd" } }`,
			Update: `{ $set: { "list.$[n]": 100 } }`, ArrayFilters: `[ { n: 1 } ]`,
		})
		if err != nil || arr.Modified != 1 {
			t.Errorf("update with arrayFilters = %+v, %v", arr, err)
		}

		for name, u := range map[string]MongoUpdate{
			"a replacement document": {Filter: `{_id: 1}`, Update: `{ name: "x" }`},
			"an empty update":        {Filter: `{_id: 1}`, Update: `{}`},
			"no update":              {Filter: `{_id: 1}`},
			"one with no filter":     {Update: `{ $set: { a: 1 } }`},
			"an empty pipeline":      {Filter: `{_id: 1}`, Update: `[]`},
		} {
			if res, err := MongoUpdateDocuments(ctx, client, db, people, u); err == nil {
				t.Errorf("%s was accepted: %+v", name, res)
			}
		}
	})

	t.Run("insert many reports what landed and what did not", func(t *testing.T) {
		res, err := MongoInsertDocuments(ctx, client, db, people, `[ {_id: 200}, {_id: 1}, {_id: 201} ]`, false)
		if err != nil {
			t.Fatalf("unordered insert with one duplicate: %v", err)
		}
		if res.Inserted != 2 || len(res.Errors) != 1 || res.Errors[0].Index != 1 || res.Errors[0].Code != 11000 {
			t.Errorf("unordered insert = %+v", res)
		}
		ordered, err := MongoInsertDocuments(ctx, client, db, people, `[ {_id: 300}, {_id: 1}, {_id: 301} ]`, true)
		if err != nil || ordered.Inserted != 1 || len(ordered.Errors) != 1 {
			t.Errorf("ordered insert stops at the duplicate: %+v, %v", ordered, err)
		}
		if _, err := MongoInsertDocuments(ctx, client, db, people, `{_id: 1}`, true); err == nil {
			t.Error("a duplicate _id on its own was not an error")
		}
		if _, err := MongoInsertDocuments(ctx, client, db, people, `[]`, true); err == nil {
			t.Error("an empty list was accepted")
		}
	})

	t.Run("clone copies every field under a new _id", func(t *testing.T) {
		clone, err := MongoCloneDocument(ctx, client, db, coll, `{"$oid":"65f1c0ffee0123456789abcd"}`)
		if err != nil {
			t.Fatalf("clone: %v", err)
		}
		if clone.ID == `{"$oid":"65f1c0ffee0123456789abcd"}` || !strings.HasPrefix(clone.ID, `{"$oid":`) {
			t.Errorf("clone id = %s", clone.ID)
		}
		original := rawByID(t, client, db, coll, idValue(t, `{"$oid":"65f1c0ffee0123456789abcd"}`))
		copied := rawByID(t, client, db, coll, idValue(t, clone.ID))
		if !bytes.Equal(withoutID(t, original), withoutID(t, copied)) {
			t.Errorf("clone differs from its original\n was %s\n now %s", original, copied)
		}
		if _, err := MongoCloneDocument(ctx, client, db, coll, `"no-such-id"`); !errors.Is(err, ErrMongoNotFound) {
			t.Errorf("clone of a missing document: %v", err)
		}
	})

	t.Run("delete one and many, with a dry run first", func(t *testing.T) {
		dry, err := MongoDeleteDocuments(ctx, client, db, people, MongoDeletion{Filter: `{ _id: { $gte: 200 } }`, Many: true, DryRun: true})
		if err != nil || !dry.DryRun || dry.Matched != 3 || dry.Deleted != 0 {
			t.Fatalf("dry run = %+v, %v", dry, err)
		}
		res, err := MongoDeleteDocuments(ctx, client, db, people, MongoDeletion{Filter: `{ _id: { $gte: 200 } }`, Many: true})
		if err != nil || res.Deleted != 3 {
			t.Errorf("deleteMany = %+v, %v", res, err)
		}
		one, err := MongoDeleteDocuments(ctx, client, db, people, MongoDeletion{Filter: `{ city: "Cluj" }`})
		if err != nil || one.Deleted != 1 {
			t.Errorf("deleteOne = %+v, %v", one, err)
		}
		if _, err := MongoDeleteDocuments(ctx, client, db, people, MongoDeletion{}); err == nil {
			t.Error("deleteOne with no filter was accepted")
		}
		if _, err := MongoDeleteDocuments(ctx, client, db, people, MongoDeletion{ID: `1`, Filter: `{a: 1}`}); err == nil {
			t.Error("an id and a filter together were accepted")
		}
		all, err := MongoDeleteDocuments(ctx, client, db, people, MongoDeletion{Many: true})
		if err != nil || all.Deleted == 0 {
			t.Errorf("deleteMany of everything = %+v, %v", all, err)
		}
		left, _ := MongoCountDocuments(ctx, client, db, people, MongoFindSpec{})
		if left == nil || left.Value != 0 {
			t.Errorf("documents left = %+v", left)
		}
	})
}

// idValue parses an _id as the routes receive it.
func idValue(t *testing.T, ext string) bson.RawValue {
	t.Helper()
	v, err := mongoParseValue("id", ext)
	if err != nil {
		t.Fatalf("parsing %s: %v", ext, err)
	}
	return v
}

func sameValue(a, b bson.RawValue) bool {
	return a.Type == b.Type && bytes.Equal(a.Value, b.Value)
}

func withoutID(t *testing.T, doc bson.Raw) []byte {
	t.Helper()
	elems, err := doc.Elements()
	if err != nil {
		t.Fatal(err)
	}
	out := []byte{}
	for _, e := range elems {
		if e.Key() != "_id" {
			out = append(out, e...)
		}
	}
	return out
}

func TestLiveMongoCollections(t *testing.T) {
	client, db := liveMongoDB(t)
	ctx := context.Background()
	names := []string{"jd_c_plain", "jd_c_capped", "jd_c_series", "jd_c_view", "jd_c_rules", "jd_c_renamed", "jd_c_target"}
	for _, n := range names {
		freshCollection(t, client, db, n)
	}
	size := int64(1 << 20)
	expire := int64(3600)

	create := func(name string, spec MongoCollectionSpec) {
		t.Helper()
		if err := MongoCreateCollectionWith(ctx, client, db, name, spec); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	create("jd_c_plain", MongoCollectionSpec{})
	create("jd_c_capped", MongoCollectionSpec{Capped: true, Size: size, Max: 100})
	create("jd_c_series", MongoCollectionSpec{
		TimeSeries:         &MongoTimeSeries{TimeField: "at", MetaField: "sensor", Granularity: "minutes"},
		ExpireAfterSeconds: &expire,
	})
	create("jd_c_rules", MongoCollectionSpec{
		Validator:       `{ $jsonSchema: { bsonType: "object", required: ["name"], properties: { name: { bsonType: "string" } } } }`,
		ValidationLevel: "moderate", ValidationAction: "warn",
		Collation: `{ locale: "en", strength: 2 }`,
	})
	if _, err := MongoInsertDocuments(ctx, client, db, "jd_c_plain", `[ {n: 1, kind: "a"}, {n: 2, kind: "b"}, {n: 3, kind: "a"} ]`, true); err != nil {
		t.Fatal(err)
	}
	create("jd_c_view", MongoCollectionSpec{ViewOn: "jd_c_plain", Pipeline: `[ { $match: { kind: "a" } } ]`})

	t.Run("list reports type, options and figures", func(t *testing.T) {
		list, err := MongoListCollections(ctx, client, db)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		byName := map[string]MongoCollection{}
		for _, c := range list.Collections {
			byName[c.Name] = c
		}
		plain := byName["jd_c_plain"]
		if plain.Type != "collection" || !plain.StatsKnown || plain.Count != 3 || plain.Size == 0 || plain.IndexCount != 1 || plain.IndexSize == 0 || plain.StorageSize == 0 {
			t.Errorf("plain = %+v", plain)
		}
		capped := byName["jd_c_capped"]
		if !capped.Capped || capped.CappedSize != size || capped.CappedMax != 100 {
			t.Errorf("capped = %+v", capped)
		}
		series := byName["jd_c_series"]
		if series.Type != "timeseries" || series.TimeSeries == nil || series.TimeSeries.TimeField != "at" ||
			series.TimeSeries.MetaField != "sensor" || series.ExpireAfterSeconds == nil || *series.ExpireAfterSeconds != expire {
			t.Errorf("series = %+v (timeseries %+v)", series, series.TimeSeries)
		}
		view := byName["jd_c_view"]
		if view.Type != "view" || view.ViewOn != "jd_c_plain" || !strings.Contains(view.Pipeline, `"$match"`) || view.StatsKnown {
			t.Errorf("view = %+v", view)
		}
		rules := byName["jd_c_rules"]
		if !strings.Contains(rules.Validator, "$jsonSchema") || rules.ValidationLevel != "moderate" ||
			rules.ValidationAction != "warn" || !strings.Contains(rules.Collation, `"locale":"en"`) {
			t.Errorf("rules = %+v", rules)
		}
		if b, ok := byName["system.buckets.jd_c_series"]; ok && !b.System {
			t.Errorf("the buckets collection is not marked as the server's: %+v", b)
		}
		// The shared table list says what each entry is, too.
		tables, err := MongoCollections(ctx, client, db)
		if err != nil {
			t.Fatal(err)
		}
		for _, tb := range tables {
			if tb.Name == "jd_c_view" && tb.Type != "view" {
				t.Errorf("table list calls the view a %s", tb.Type)
			}
			if tb.Name == "jd_c_plain" && (tb.Type != "collection" || tb.Rows != 3) {
				t.Errorf("table list entry = %+v", tb)
			}
		}
	})

	t.Run("a view reads through its pipeline and one collection is described", func(t *testing.T) {
		res, err := MongoFindDocuments(ctx, client, db, "jd_c_view", MongoFindSpec{}, true)
		if err != nil || res.Returned != 2 {
			t.Fatalf("find on the view: %+v, %v", res, err)
		}
		info, err := MongoCollectionInfo(ctx, client, db, "jd_c_view")
		if err != nil || info.Type != "view" {
			t.Errorf("info = %+v, %v", info, err)
		}
		if _, err := MongoCollectionInfo(ctx, client, db, "jd_c_missing"); err == nil {
			t.Error("a missing collection was described")
		}
	})

	t.Run("what create refuses", func(t *testing.T) {
		for name, spec := range map[string]MongoCollectionSpec{
			"capped without a size":      {Capped: true},
			"a size without capped":      {Size: 100},
			"a view that writes":         {ViewOn: "jd_c_plain", Pipeline: `[ { $out: "x" } ]`},
			"a capped view":              {ViewOn: "jd_c_plain", Capped: true, Size: 1},
			"a pipeline without viewOn":  {Pipeline: `[]`},
			"a series without a field":   {TimeSeries: &MongoTimeSeries{}},
			"an unknown level":           {ValidationLevel: "lenient"},
			"an expiry on a plain one":   {ExpireAfterSeconds: &expire},
			"a broken validator":         {Validator: `{ $jsonSchema: `},
			"granularity and a span":     {TimeSeries: &MongoTimeSeries{TimeField: "at", Granularity: "hours", BucketMaxSpanSeconds: 60, BucketRoundingSeconds: 60}},
			"an unknown granularity":     {TimeSeries: &MongoTimeSeries{TimeField: "at", Granularity: "days"}},
			"a collation with no locale": {Collation: `{ strength: 2 }`},
		} {
			if err := MongoCreateCollectionWith(ctx, client, db, "jd_c_refused", spec); err == nil {
				_ = MongoDropCollection(ctx, client, db, "jd_c_refused")
				t.Errorf("%s was accepted", name)
			}
		}
		if err := MongoCreateCollectionWith(ctx, client, db, "system.mine", MongoCollectionSpec{}); err == nil {
			t.Error("a system. name was accepted")
		}
		if err := MongoCreateCollectionWith(ctx, client, db, "a$b", MongoCollectionSpec{}); err == nil {
			t.Error("a name holding $ was accepted")
		}
	})

	t.Run("collMod changes rules, a view and a cap", func(t *testing.T) {
		empty := ""
		if err := MongoModifyCollection(ctx, client, db, "jd_c_rules", MongoCollectionChange{ValidationAction: "error"}); err != nil {
			t.Fatalf("collMod action: %v", err)
		}
		got, _ := MongoCollectionInfo(ctx, client, db, "jd_c_rules")
		if got.ValidationAction != "error" || got.Validator == "" {
			t.Errorf("after changing only the action: %+v", got)
		}
		if err := MongoModifyCollection(ctx, client, db, "jd_c_rules", MongoCollectionChange{Validator: &empty}); err != nil {
			t.Fatalf("removing the validator: %v", err)
		}
		if got, _ := MongoCollectionInfo(ctx, client, db, "jd_c_rules"); got.Validator != "" {
			t.Errorf("validator after removal: %s", got.Validator)
		}

		pipeline := `[ { $match: { kind: "b" } } ]`
		if err := MongoModifyCollection(ctx, client, db, "jd_c_view", MongoCollectionChange{ViewOn: "jd_c_plain", Pipeline: &pipeline}); err != nil {
			t.Fatalf("redefining the view: %v", err)
		}
		if res, _ := MongoFindDocuments(ctx, client, db, "jd_c_view", MongoFindSpec{}, false); res == nil || res.Returned != 1 {
			t.Errorf("the redefined view returned %+v", res)
		}

		bigger := int64(2 << 20)
		change := MongoCollectionChange{CappedSize: &bigger}
		if !change.RemovesData() {
			t.Error("resizing a capped collection is not flagged as removing data")
		}
		if err := MongoModifyCollection(ctx, client, db, "jd_c_capped", change); err != nil {
			t.Fatalf("resizing the capped collection: %v", err)
		}
		if got, _ := MongoCollectionInfo(ctx, client, db, "jd_c_capped"); got.CappedSize != bigger {
			t.Errorf("capped size = %d, want %d", got.CappedSize, bigger)
		}
		off := int64(-1)
		if (MongoCollectionChange{ExpireAfterSeconds: &off}).RemovesData() {
			t.Error("turning an expiry off is flagged as removing data")
		}
		if err := MongoModifyCollection(ctx, client, db, "jd_c_series", MongoCollectionChange{ExpireAfterSeconds: &off}); err != nil {
			t.Fatalf("turning the expiry off: %v", err)
		}
		if got, _ := MongoCollectionInfo(ctx, client, db, "jd_c_series"); got.ExpireAfterSeconds != nil {
			t.Errorf("expiry after turning it off: %v", *got.ExpireAfterSeconds)
		}
		if err := MongoModifyCollection(ctx, client, db, "jd_c_plain", MongoCollectionChange{}); err == nil {
			t.Error("a collMod of nothing was accepted")
		}
	})

	t.Run("rename, with and without dropping the target", func(t *testing.T) {
		if err := MongoRenameCollection(ctx, client, db, "jd_c_capped", "jd_c_renamed", false); err != nil {
			t.Fatalf("rename: %v", err)
		}
		if _, err := MongoCollectionInfo(ctx, client, db, "jd_c_renamed"); err != nil {
			t.Errorf("the renamed collection: %v", err)
		}
		if err := MongoCreateCollectionWith(ctx, client, db, "jd_c_target", MongoCollectionSpec{}); err != nil {
			t.Fatal(err)
		}
		if err := MongoRenameCollection(ctx, client, db, "jd_c_renamed", "jd_c_target", false); err == nil {
			t.Error("renaming over an existing collection without dropTarget succeeded")
		}
		if err := MongoRenameCollection(ctx, client, db, "jd_c_renamed", "jd_c_target", true); err != nil {
			t.Errorf("renaming over an existing collection with dropTarget: %v", err)
		}
		if got, err := MongoCollectionInfo(ctx, client, db, "jd_c_target"); err != nil || !got.Capped {
			t.Errorf("the target is not the renamed collection: %+v, %v", got, err)
		}
		for _, to := range []string{"", "jd_c_target", "system.x", "a$b"} {
			if err := MongoRenameCollection(ctx, client, db, "jd_c_target", to, false); err == nil {
				t.Errorf("rename to %q was accepted", to)
			}
		}
	})
}

func TestLiveMongoIndexes(t *testing.T) {
	client, db := liveMongoDB(t)
	ctx := context.Background()
	const coll = "jd_indexes"
	freshCollection(t, client, db, coll)
	docs := []string{}
	for i := 0; i < 200; i++ {
		docs = append(docs, fmt.Sprintf(
			`{ n: %d, email: "u%d@example.com", bio: "words about user %d", tags: ["a", "b"], at: ISODate("2024-01-01"), loc: { type: "Point", coordinates: [%d, 45] }, extra: { k%d: 1 } }`,
			i, i, i, i%90, i%5))
	}
	if _, err := MongoInsertDocuments(ctx, client, db, coll, "["+strings.Join(docs, ",")+"]", true); err != nil {
		t.Fatal(err)
	}
	ttl := int64(86400 * 365 * 50)
	specs := map[string]MongoIndexSpec{
		"n_1_email_-1":  {Keys: []MongoIndexKey{{Field: "n", Type: "asc"}, {Field: "email", Type: "desc"}}},
		"email_unique":  {Keys: []MongoIndexKey{{Field: "email", Type: "asc"}}, Name: "email_unique", Unique: true, Sparse: true},
		"partial_n":     {Keys: []MongoIndexKey{{Field: "n", Type: "desc"}}, Name: "partial_n", PartialFilterExpression: `{ n: { $gt: 100 } }`},
		"bio_text":      {Keys: []MongoIndexKey{{Field: "bio", Type: "text"}}, Name: "bio_text", Weights: `{ bio: 5 }`, DefaultLanguage: "english"},
		"tags_hashed":   {Keys: []MongoIndexKey{{Field: "n", Type: "hashed"}}, Name: "tags_hashed"},
		"loc_2dsphere":  {Keys: []MongoIndexKey{{Field: "loc", Type: "2dsphere"}}},
		"extra_wild":    {Keys: []MongoIndexKey{{Field: "extra.$**", Type: "asc"}}, Name: "extra_wild"},
		"at_ttl":        {Keys: []MongoIndexKey{{Field: "at", Type: "asc"}}, Name: "at_ttl", ExpireAfterSeconds: &ttl},
		"hidden_tags":   {Keys: []MongoIndexKey{{Field: "tags", Type: "asc"}}, Name: "hidden_tags", Hidden: true},
		"collated_mail": {Keys: []MongoIndexKey{{Field: "email", Type: "asc"}}, Name: "collated_mail", Collation: `{ locale: "en", strength: 2 }`},
	}
	for want, spec := range specs {
		name, err := MongoCreateIndex(ctx, client, db, coll, spec)
		if err != nil {
			t.Fatalf("create %s: %v", want, err)
		}
		if spec.Name != "" && name != spec.Name {
			t.Errorf("created %q, asked for %q", name, spec.Name)
		}
		if spec.Name == "" && name != want {
			t.Errorf("created %q, expected the default name %q", name, want)
		}
	}
	if !specs["at_ttl"].Expires() || specs["partial_n"].Expires() {
		t.Error("Expires does not tell a TTL index from another")
	}

	// Something uses the compound index, so it has a usage to report.
	for i := 0; i < 3; i++ {
		if _, err := MongoFindDocuments(ctx, client, db, coll, MongoFindSpec{Filter: `{ n: 7 }`, Hint: "n_1_email_-1"}, false); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("list reports properties, size and usage", func(t *testing.T) {
		list, err := MongoListIndexes(ctx, client, db, coll)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if list.UsageNote != "" {
			t.Errorf("usage could not be read: %s", list.UsageNote)
		}
		byName := map[string]MongoIndex{}
		for _, ix := range list.Indexes {
			byName[ix.Name] = ix
			if ix.Size <= 0 {
				t.Errorf("index %s has no size", ix.Name)
			}
			if ix.Usage == nil || ix.Usage.Since == "" {
				t.Errorf("index %s has no usage", ix.Name)
			}
		}
		if len(byName) != len(specs)+1 {
			t.Errorf("%d indexes listed, want %d", len(byName), len(specs)+1)
		}
		if id := byName["_id_"]; !id.Primary || id.Kind != "regular" || len(id.Keys) != 1 || id.Keys[0] != (MongoIndexKey{Field: "_id", Type: "asc"}) {
			t.Errorf("_id index = %+v", id)
		}
		compound := byName["n_1_email_-1"]
		if len(compound.Keys) != 2 || compound.Keys[1] != (MongoIndexKey{Field: "email", Type: "desc"}) || compound.Key != `{"n":1,"email":-1}` {
			t.Errorf("compound index = %+v", compound)
		}
		if compound.Usage == nil || compound.Usage.Ops < 3 {
			t.Errorf("compound index usage = %+v, want at least 3", compound.Usage)
		}
		if u := byName["email_unique"]; !u.Unique || !u.Sparse {
			t.Errorf("unique sparse index = %+v", u)
		}
		if p := byName["partial_n"]; !strings.Contains(p.PartialFilterExpression, "$gt") {
			t.Errorf("partial index = %+v", p)
		}
		if tx := byName["bio_text"]; tx.Kind != "text" || len(tx.Keys) != 1 || tx.Keys[0] != (MongoIndexKey{Field: "bio", Type: "text"}) ||
			!strings.Contains(tx.Weights, `"bio":5`) || tx.DefaultLanguage != "english" {
			t.Errorf("text index = %+v", tx)
		}
		if h := byName["tags_hashed"]; h.Kind != "hashed" {
			t.Errorf("hashed index = %+v", h)
		}
		if g := byName["loc_2dsphere"]; g.Kind != "geospatial" || g.Keys[0].Type != "2dsphere" {
			t.Errorf("geospatial index = %+v", g)
		}
		if w := byName["extra_wild"]; w.Kind != "wildcard" {
			t.Errorf("wildcard index = %+v", w)
		}
		if tl := byName["at_ttl"]; tl.ExpireAfterSeconds == nil || *tl.ExpireAfterSeconds != ttl {
			t.Errorf("ttl index = %+v", tl)
		}
		if hd := byName["hidden_tags"]; !hd.Hidden {
			t.Errorf("hidden index = %+v", hd)
		}
		if c := byName["collated_mail"]; !strings.Contains(c.Collation, `"locale":"en"`) {
			t.Errorf("collated index = %+v", c)
		}
	})

	t.Run("hide, unhide and a new ttl", func(t *testing.T) {
		yes, no := true, false
		find := func(name string) MongoIndex {
			list, err := MongoListIndexes(ctx, client, db, coll)
			if err != nil {
				t.Fatal(err)
			}
			for _, ix := range list.Indexes {
				if ix.Name == name {
					return ix
				}
			}
			t.Fatalf("index %s is gone", name)
			return MongoIndex{}
		}
		if err := MongoModifyIndex(ctx, client, db, coll, MongoIndexChange{Name: "partial_n", Hidden: &yes}); err != nil {
			t.Fatalf("hide: %v", err)
		}
		if !find("partial_n").Hidden {
			t.Error("the index is not hidden")
		}
		if err := MongoModifyIndex(ctx, client, db, coll, MongoIndexChange{Name: "partial_n", Hidden: &no}); err != nil {
			t.Fatalf("unhide: %v", err)
		}
		if find("partial_n").Hidden {
			t.Error("the index is still hidden")
		}
		longer := ttl + 1
		change := MongoIndexChange{Name: "at_ttl", ExpireAfterSeconds: &longer}
		if !change.RemovesData() || (MongoIndexChange{Name: "x", Hidden: &yes}).RemovesData() {
			t.Error("RemovesData does not tell a TTL change from hiding")
		}
		if err := MongoModifyIndex(ctx, client, db, coll, change); err != nil {
			t.Fatalf("ttl change: %v", err)
		}
		if got := find("at_ttl").ExpireAfterSeconds; got == nil || *got != longer {
			t.Errorf("ttl after change = %v", got)
		}
		for name, c := range map[string]MongoIndexChange{
			"the _id index": {Name: "_id_", Hidden: &yes},
			"no name":       {Hidden: &yes},
			"nothing":       {Name: "partial_n"},
		} {
			if err := MongoModifyIndex(ctx, client, db, coll, c); err == nil {
				t.Errorf("modifying %s was accepted", name)
			}
		}
	})

	t.Run("drop one, never all and never _id", func(t *testing.T) {
		if err := MongoDropIndex(ctx, client, db, coll, "hidden_tags"); err != nil {
			t.Fatalf("drop: %v", err)
		}
		for _, name := range []string{"*", "_id_", "", "no_such_index"} {
			if err := MongoDropIndex(ctx, client, db, coll, name); err == nil {
				t.Errorf("dropping %q was accepted", name)
			}
		}
		list, _ := MongoListIndexes(ctx, client, db, coll)
		if len(list.Indexes) != len(specs) {
			t.Errorf("%d indexes left, want %d", len(list.Indexes), len(specs))
		}
	})

	t.Run("what create refuses", func(t *testing.T) {
		neg := int64(-1)
		for name, spec := range map[string]MongoIndexSpec{
			"no fields":           {},
			"an unknown type":     {Keys: []MongoIndexKey{{Field: "n", Type: "sideways"}}},
			"an operator field":   {Keys: []MongoIndexKey{{Field: "$where", Type: "asc"}}},
			"a field twice":       {Keys: []MongoIndexKey{{Field: "n", Type: "asc"}, {Field: "n", Type: "desc"}}},
			"a negative ttl":      {Keys: []MongoIndexKey{{Field: "at", Type: "asc"}}, ExpireAfterSeconds: &neg},
			"a compound ttl":      {Keys: []MongoIndexKey{{Field: "at", Type: "asc"}, {Field: "n", Type: "asc"}}, ExpireAfterSeconds: &ttl},
			"a broken filter":     {Keys: []MongoIndexKey{{Field: "n", Type: "asc"}}, PartialFilterExpression: `{ n: `},
			"a duplicate, unique": {Keys: []MongoIndexKey{{Field: "tags", Type: "asc"}}, Unique: true},
		} {
			if got, err := MongoCreateIndex(ctx, client, db, coll, spec); err == nil {
				t.Errorf("%s was accepted as %s", name, got)
			}
		}
	})
}

func TestLiveMongoAggregation(t *testing.T) {
	client, db := liveMongoDB(t)
	ctx := context.Background()
	const coll, out = "jd_agg", "jd_agg_out"
	freshCollection(t, client, db, coll)
	freshCollection(t, client, db, out)
	docs := []string{}
	for i := 0; i < 60; i++ {
		docs = append(docs, fmt.Sprintf(`{ _id: %d, kind: "k%d", n: %d, tags: ["x", "y"] }`, i, i%3, i))
	}
	if _, err := MongoInsertDocuments(ctx, client, db, coll, "["+strings.Join(docs, ",")+"]", true); err != nil {
		t.Fatal(err)
	}
	pipeline := `[
	  { $match: { n: { $gte: 10 } } },
	  { $group: { _id: "$kind", total: { $sum: "$n" }, docs: { $sum: 1 } } },
	  { $sort: { _id: 1 } }
	]`

	t.Run("run returns typed documents and the legacy grid", func(t *testing.T) {
		res, err := MongoRunPipeline(ctx, client, db, coll, MongoAggregateSpec{Pipeline: pipeline})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if res.Returned != 3 || res.HasMore {
			t.Fatalf("returned %d, hasMore %v", res.Returned, res.HasMore)
		}
		if res.Documents[0].Canonical != `{"_id":"k0","total":{"$numberInt":"552"},"docs":{"$numberInt":"16"}}` {
			t.Errorf("first group = %s", res.Documents[0].Canonical)
		}
		if res.Grid == nil || res.Grid.RowCount != 3 || len(res.Grid.Columns) != 3 || res.Grid.Columns[0] != "_id" {
			t.Errorf("grid = %+v", res.Grid)
		}
		if !strings.Contains(res.Statement, coll+".aggregate([") {
			t.Errorf("statement = %s", res.Statement)
		}
		limited, err := MongoRunPipeline(ctx, client, db, coll, MongoAggregateSpec{Pipeline: `[ { $sort: { _id: 1 } } ]`, Limit: 10})
		if err != nil || limited.Returned != 10 || !limited.HasMore || !limited.Grid.Truncated {
			t.Errorf("limited run: %+v, %v", limited, err)
		}
		old, err := MongoAggregate(ctx, client, db, coll, pipeline, 0)
		if err != nil || old.RowCount != 3 {
			t.Errorf("the original entry point: %+v, %v", old, err)
		}
	})

	t.Run("preview shows each stage's output and never writes", func(t *testing.T) {
		writing := `[
		  { $match: { n: { $gte: 10 } } },
		  { $group: { _id: "$kind", total: { $sum: "$n" } } },
		  { $out: "` + out + `" }
		]`
		input, err := MongoPreviewPipeline(ctx, client, db, coll, MongoPreviewSpec{Pipeline: writing, Stage: -1, Sample: 5})
		if err != nil || input.Returned != 5 || input.Stages != 3 {
			t.Fatalf("stage -1: %+v, %v", input, err)
		}
		match, err := MongoPreviewPipeline(ctx, client, db, coll, MongoPreviewSpec{Pipeline: writing, Stage: 0, Sample: 100})
		if err != nil || match.Returned != 50 || match.InputLimited {
			t.Fatalf("stage 0: returned %d, %v", match.Returned, err)
		}
		group, err := MongoPreviewPipeline(ctx, client, db, coll, MongoPreviewSpec{Pipeline: writing, Stage: 1})
		if err != nil || group.Returned != 3 || !group.InputLimited || group.InputLimit != mongoPreviewInputLimit {
			t.Fatalf("stage 1: %+v, %v", group, err)
		}
		// The writing stage is asked for and not run: what comes back is
		// what it would have been handed, and the collection is not made.
		last, err := MongoPreviewPipeline(ctx, client, db, coll, MongoPreviewSpec{Pipeline: writing, Stage: 2})
		if err != nil || last.WriteStage != "$out" || last.Returned != 3 {
			t.Fatalf("stage 2: %+v, %v", last, err)
		}
		if _, err := MongoCollectionInfo(ctx, client, db, out); err == nil {
			t.Fatal("previewing a pipeline that ends in $out wrote the collection")
		}

		// A capped input changes what a group sees, and says so.
		capInput := int64(20)
		capped, err := MongoPreviewPipeline(ctx, client, db, coll, MongoPreviewSpec{
			Pipeline: `[ { $group: { _id: null, docs: { $sum: 1 } } } ]`, Stage: 0, InputLimit: &capInput,
		})
		if err != nil || !capped.InputLimited || capped.Documents[0].Canonical != `{"_id":null,"docs":{"$numberInt":"20"}}` {
			t.Errorf("capped input: %+v, %v", capped, err)
		}
		none := int64(0)
		full, err := MongoPreviewPipeline(ctx, client, db, coll, MongoPreviewSpec{
			Pipeline: `[ { $group: { _id: null, docs: { $sum: 1 } } } ]`, Stage: 0, InputLimit: &none,
		})
		if err != nil || full.InputLimited || full.Documents[0].Canonical != `{"_id":null,"docs":{"$numberInt":"60"}}` {
			t.Errorf("uncapped input: %+v, %v", full, err)
		}

		if _, err := MongoPreviewPipeline(ctx, client, db, coll, MongoPreviewSpec{Pipeline: writing, Stage: 3}); err == nil {
			t.Error("a stage past the end was accepted")
		}
		if _, err := MongoPreviewPipeline(ctx, client, db, coll, MongoPreviewSpec{Pipeline: `[ { $futureStage: {} } ]`, Stage: 0}); err == nil ||
			!strings.Contains(err.Error(), "$futureStage") {
			t.Errorf("an unknown stage in a preview: %v", err)
		}
	})

	t.Run("a pipeline that writes does, when run", func(t *testing.T) {
		res, err := MongoRunPipeline(ctx, client, db, coll, MongoAggregateSpec{
			Pipeline: `[ { $match: { kind: "k1" } }, { $out: "` + out + `" } ]`,
		})
		if err != nil || res.Returned != 0 {
			t.Fatalf("writing run: %+v, %v", res, err)
		}
		count, _ := MongoCountDocuments(ctx, client, db, out, MongoFindSpec{})
		if count == nil || count.Value != 20 {
			t.Errorf("$out wrote %+v documents, want 20", count)
		}
	})

	t.Run("explain a find: scan, then index", func(t *testing.T) {
		scan, err := MongoExplain(ctx, client, db, coll, MongoExplainSpec{
			MongoFindSpec: MongoFindSpec{Filter: `{ n: { $gte: 50 } }`, Sort: `{ kind: 1 }`}, Verbosity: "executionStats",
		})
		if err != nil {
			t.Fatalf("explain: %v", err)
		}
		s := scan.Summary
		if !s.Executed || !s.CollectionScan || !s.InMemorySort || len(s.IndexesUsed) != 0 {
			t.Errorf("summary of a scan = %+v", s)
		}
		if s.Returned == nil || *s.Returned != 10 || s.DocsExamined == nil || *s.DocsExamined != 60 || s.KeysExamined == nil || *s.KeysExamined != 0 {
			t.Errorf("figures of a scan = returned %v, docs %v, keys %v", deref(s.Returned), deref(s.DocsExamined), deref(s.KeysExamined))
		}
		if s.Namespace != db+"."+coll || scan.Raw == "" {
			t.Errorf("namespace = %q", s.Namespace)
		}
		if !planHas(scan.Plan, "COLLSCAN") || !planHas(scan.Plan, "SORT") {
			t.Errorf("plan = %+v", scan.Plan)
		}

		if _, err := MongoCreateIndex(ctx, client, db, coll, MongoIndexSpec{Keys: []MongoIndexKey{{Field: "n", Type: "asc"}}}); err != nil {
			t.Fatal(err)
		}
		indexed, err := MongoExplain(ctx, client, db, coll, MongoExplainSpec{
			MongoFindSpec: MongoFindSpec{Filter: `{ n: { $gte: 50 } }`, Sort: `{ n: -1 }`, Limit: 5}, Verbosity: "executionStats",
		})
		if err != nil {
			t.Fatalf("explain with an index: %v", err)
		}
		s = indexed.Summary
		if s.CollectionScan || s.InMemorySort || len(s.IndexesUsed) != 1 || s.IndexesUsed[0] != "n_1" {
			t.Errorf("summary of an index scan = %+v", s)
		}
		if deref(s.Returned) != 5 || deref(s.DocsExamined) != 5 || deref(s.KeysExamined) != 5 {
			t.Errorf("figures of an index scan = returned %d, docs %d, keys %d", deref(s.Returned), deref(s.DocsExamined), deref(s.KeysExamined))
		}
		ix := planFind(indexed.Plan, "IXSCAN")
		if ix == nil || ix.Index != "n_1" || ix.Direction != "backward" || !strings.Contains(ix.KeyPattern, `"n":1`) || ix.IndexBounds == "" {
			t.Errorf("index scan node = %+v", ix)
		}
		if ix != nil && (ix.Returned == nil || ix.KeysExamined == nil) {
			t.Errorf("index scan node has no figures: %+v", ix)
		}

		planned, err := MongoExplain(ctx, client, db, coll, MongoExplainSpec{MongoFindSpec: MongoFindSpec{Filter: `{ n: 1 }`}})
		if err != nil {
			t.Fatal(err)
		}
		if planned.Summary.Executed || planned.Summary.Returned != nil || planned.Summary.Verbosity != "queryPlanner" {
			t.Errorf("a plan without execution claims figures: %+v", planned.Summary)
		}
		if _, err := MongoExplain(ctx, client, db, coll, MongoExplainSpec{Verbosity: "everything"}); err == nil {
			t.Error("an unknown verbosity was accepted")
		}
	})

	t.Run("explain an aggregation, and never run one that writes", func(t *testing.T) {
		res, err := MongoExplain(ctx, client, db, coll, MongoExplainSpec{Pipeline: pipeline, Verbosity: "executionStats"})
		if err != nil {
			t.Fatalf("explain: %v", err)
		}
		if !res.Summary.Executed || deref(res.Summary.Returned) != 3 || len(res.Summary.IndexesUsed) != 1 {
			t.Errorf("summary = %+v (returned %d)", res.Summary, deref(res.Summary.Returned))
		}
		if !planHas(res.Plan, "IXSCAN") {
			t.Errorf("plan has no index scan: %+v", res.Plan)
		}
		_ = MongoDropCollection(ctx, client, db, out)
		writing, err := MongoExplain(ctx, client, db, coll, MongoExplainSpec{
			Pipeline: `[ { $match: { kind: "k1" } }, { $out: "` + out + `" } ]`, Verbosity: "executionStats",
		})
		if err != nil {
			t.Fatalf("explain of a writing pipeline: %v", err)
		}
		if writing.Summary.Verbosity != "queryPlanner" || writing.Summary.Executed || writing.Note == "" {
			t.Errorf("a writing pipeline was explained with execution: %+v, note %q", writing.Summary, writing.Note)
		}
		if _, err := MongoCollectionInfo(ctx, client, db, out); err == nil {
			t.Error("explaining a pipeline that ends in $out wrote the collection")
		}
	})
}

func deref(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

func planHas(node MongoPlanNode, stage string) bool { return planFind(node, stage) != nil }

func planFind(node MongoPlanNode, stage string) *MongoPlanNode {
	if node.Stage == stage {
		return &node
	}
	for _, c := range node.Children {
		if found := planFind(c, stage); found != nil {
			return found
		}
	}
	return nil
}

func TestLiveMongoSchema(t *testing.T) {
	client, db := liveMongoDB(t)
	ctx := context.Background()
	const coll = "jd_schema"
	freshCollection(t, client, db, coll)
	docs := []string{}
	for i := 0; i < 100; i++ {
		doc := fmt.Sprintf(`{ _id: %d, name: "user%d", status: "%s", age: %d, score: %d.5, address: { city: "%s", zip: %d }, tags: ["a", "b", "c"], items: [ { sku: "s%d", qty: NumberLong(%d) } ], joined: ISODate("2024-01-%02d")`,
			i, i, []string{"active", "active", "active", "blocked"}[i%4], 20+i%50, i, []string{"Cluj", "Iași"}[i%2], 400000+i, i, i, 1+i%28)
		if i%4 == 0 {
			doc += `, nickname: "n"`
		}
		if i%10 == 0 {
			// The same field under another type, which is what an evolving
			// collection looks like.
			doc = strings.Replace(doc, fmt.Sprintf(`age: %d`, 20+i%50), `age: "unknown"`, 1)
		}
		docs = append(docs, doc+" }")
	}
	if _, err := MongoInsertDocuments(ctx, client, db, coll, "["+strings.Join(docs, ",")+"]", true); err != nil {
		t.Fatal(err)
	}
	if _, err := MongoCreateIndex(ctx, client, db, coll, MongoIndexSpec{Keys: []MongoIndexKey{{Field: "address.city", Type: "asc"}, {Field: "items.sku", Type: "asc"}}}); err != nil {
		t.Fatal(err)
	}

	schema, err := MongoAnalyseSchema(ctx, client, db, coll, MongoSchemaSpec{Sample: 5000})
	if err != nil {
		t.Fatalf("analyse: %v", err)
	}
	if schema.Sampled != 100 || schema.Requested != 5000 || schema.Total != 100 || schema.Truncated {
		t.Errorf("sampled %d of %d requested, total %d, truncated %v", schema.Sampled, schema.Requested, schema.Total, schema.Truncated)
	}
	byPath := map[string]MongoSchemaField{}
	order := []string{}
	for _, f := range schema.Fields {
		byPath[f.Path] = f
		order = append(order, f.Path)
	}
	if order[0] != "_id" {
		t.Errorf("first field = %s, want _id", order[0])
	}
	typeOf := func(path, name string) MongoSchemaType {
		t.Helper()
		for _, ty := range byPath[path].Types {
			if ty.Type == name {
				return ty
			}
		}
		t.Errorf("field %s has no type %s: %+v", path, name, byPath[path].Types)
		return MongoSchemaType{}
	}

	if f := byPath["name"]; f.Presence != 1 || f.Documents != 100 || len(f.Types) != 1 {
		t.Errorf("name = %+v", f)
	}
	if f := byPath["nickname"]; f.Presence != 0.25 || f.Documents != 25 {
		t.Errorf("nickname presence = %v (%d documents)", f.Presence, f.Documents)
	}
	age := byPath["age"]
	if len(age.Types) != 2 || age.Types[0].Type != "int" || age.Types[0].Share != 0.9 || age.Types[1].Type != "string" {
		t.Errorf("age type mix = %+v", age.Types)
	}
	if ints := typeOf("age", "int"); ints.Min != "21" || ints.Max != "69" || ints.Avg == nil {
		t.Errorf("age figures = %+v", ints)
	}
	status := typeOf("status", "string")
	if len(status.Top) != 2 || status.Top[0] != (MongoSchemaValue{Value: "active", Count: 75}) || status.Distinct != 2 ||
		status.MinLength == nil || *status.MinLength != 6 || *status.MaxLength != 7 {
		t.Errorf("status figures = %+v", status)
	}
	if score := typeOf("score", "double"); score.Min != "0.5" || score.Max != "99.5" || score.Avg == nil || *score.Avg != 50 {
		t.Errorf("score figures = %+v", score)
	}
	if joined := typeOf("joined", "date"); joined.Min != "2024-01-01T00:00:00Z" || joined.Max != "2024-01-28T00:00:00Z" || joined.Avg == nil {
		t.Errorf("joined figures = %+v", joined)
	}
	if tags := typeOf("tags", "array"); tags.MinLength == nil || *tags.MinLength != 3 || *tags.MaxLength != 3 || *tags.AvgLength != 3 {
		t.Errorf("tags figures = %+v", tags)
	}
	if el := byPath["tags[]"]; el.Occurrences != 300 || el.Documents != 100 || el.Name != "[]" || el.Depth != 1 {
		t.Errorf("tags[] = %+v", el)
	}
	if city := byPath["address.city"]; !city.Indexed || len(city.Indexes) != 1 || city.Depth != 1 || city.Name != "city" {
		t.Errorf("address.city = %+v", city)
	}
	if sku := byPath["items[].sku"]; !sku.Indexed || sku.Depth != 2 {
		t.Errorf("items[].sku = %+v", sku)
	}
	if qty := typeOf("items[].qty", "long"); qty.Min != "0" || qty.Max != "99" {
		t.Errorf("items[].qty = %+v", qty)
	}
	if byPath["name"].Indexed || !byPath["_id"].Indexed {
		t.Errorf("indexed flags: name %v, _id %v", byPath["name"].Indexed, byPath["_id"].Indexed)
	}
	if typeOf("address", "object").Count != 100 {
		t.Errorf("address = %+v", byPath["address"])
	}

	filtered, err := MongoAnalyseSchema(ctx, client, db, coll, MongoSchemaSpec{Sample: 10, Filter: `{ status: "blocked" }`})
	if err != nil {
		t.Fatalf("analyse with a filter: %v", err)
	}
	if filtered.Sampled != 10 {
		t.Errorf("sampled %d, want 10", filtered.Sampled)
	}
	for _, f := range filtered.Fields {
		if f.Path == "status" && (len(f.Types[0].Top) != 1 || f.Types[0].Top[0].Value != "blocked") {
			t.Errorf("the filter was not applied: %+v", f.Types[0].Top)
		}
	}
}

func TestLiveMongoValidation(t *testing.T) {
	client, db := liveMongoDB(t)
	ctx := context.Background()
	const coll = "jd_validation"
	freshCollection(t, client, db, coll)
	if _, err := MongoInsertDocuments(ctx, client, db, coll,
		`[ { _id: 1, name: "Ann", age: 31 }, { _id: 2, name: "Bo" }, { _id: 3, age: "old" }, { _id: 4, name: 7 } ]`, true); err != nil {
		t.Fatal(err)
	}
	rule := `{ $jsonSchema: { bsonType: "object", required: ["name"], properties: { name: { bsonType: "string" }, age: { bsonType: "int" } } } }`

	none, err := MongoReadValidation(ctx, client, db, coll)
	if err != nil || none.Validator != "" || none.Level != "strict" || none.Action != "error" {
		t.Fatalf("a collection without a rule: %+v, %v", none, err)
	}
	check, err := MongoCheckValidation(ctx, client, db, coll, "", 0, 0)
	if err != nil || check.Failing != 0 || check.Proposed || check.Total != 4 {
		t.Errorf("checking no rule: %+v, %v", check, err)
	}

	// Before saving: how many would it reject, and which.
	proposed, err := MongoCheckValidation(ctx, client, db, coll, rule, 1, 0)
	if err != nil {
		t.Fatalf("checking a proposed rule: %v", err)
	}
	if !proposed.Proposed || !proposed.Exact || proposed.Failing != 2 || len(proposed.Samples) != 1 {
		t.Errorf("proposed rule: %+v", proposed)
	}
	if after, _ := MongoReadValidation(ctx, client, db, coll); after.Validator != "" {
		t.Error("checking a proposed rule saved it")
	}

	if err := MongoWriteValidation(ctx, client, db, coll, &rule, "moderate", "warn"); err != nil {
		t.Fatalf("saving the rule: %v", err)
	}
	saved, err := MongoReadValidation(ctx, client, db, coll)
	if err != nil || saved.Level != "moderate" || saved.Action != "warn" || !strings.Contains(saved.ValidatorRelaxed, "$jsonSchema") {
		t.Fatalf("the saved rule: %+v, %v", saved, err)
	}
	// What is read back can be sent back unchanged.
	if err := MongoWriteValidation(ctx, client, db, coll, &saved.Validator, "", ""); err != nil {
		t.Errorf("saving the rule as read: %v", err)
	}
	current, err := MongoCheckValidation(ctx, client, db, coll, "", 10, 0)
	if err != nil || current.Proposed || current.Failing != 2 || len(current.Samples) != 2 {
		t.Errorf("checking the saved rule: %+v, %v", current, err)
	}

	// Only the level, leaving the rule.
	if err := MongoWriteValidation(ctx, client, db, coll, nil, "strict", "error"); err != nil {
		t.Fatalf("changing the level: %v", err)
	}
	if got, _ := MongoReadValidation(ctx, client, db, coll); got.Validator == "" || got.Level != "strict" || got.Action != "error" {
		t.Errorf("after changing the level: %+v", got)
	}
	if _, err := MongoInsertDocuments(ctx, client, db, coll, `{ _id: 5, age: 3 }`, true); err == nil {
		t.Error("a strict rule let a failing document in")
	}
	empty := ""
	if err := MongoWriteValidation(ctx, client, db, coll, &empty, "", ""); err != nil {
		t.Fatalf("removing the rule: %v", err)
	}
	if got, _ := MongoReadValidation(ctx, client, db, coll); got.Validator != "" {
		t.Errorf("the rule after removal: %s", got.Validator)
	}
	if err := MongoWriteValidation(ctx, client, db, coll, nil, "lenient", ""); err == nil {
		t.Error("an unknown level was accepted")
	}
	if _, err := MongoCheckValidation(ctx, client, db, coll, `{ $jsonSchema: `, 0, 0); err == nil {
		t.Error("a broken rule was checked")
	}
}

func TestLiveMongoServer(t *testing.T) {
	client, db := liveMongoDB(t)
	ctx := context.Background()
	const coll = "jd_server"
	freshCollection(t, client, db, coll)
	if _, err := MongoInsertDocuments(ctx, client, db, coll, `[ {a: 1}, {a: 2} ]`, true); err != nil {
		t.Fatal(err)
	}

	t.Run("a snapshot carries counters and the server's clock", func(t *testing.T) {
		first, err := MongoServerSnapshot(ctx, client)
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		if first.Version == "" || first.Host == "" || first.Process == "" || first.Uptime <= 0 || first.StorageEngine == "" {
			t.Errorf("identity = %+v", first)
		}
		if skew := time.Since(time.UnixMilli(first.Timestamp)); skew < -time.Minute || skew > time.Minute {
			t.Errorf("timestamp %d is not now", first.Timestamp)
		}
		for _, key := range []string{"insert", "query", "update", "delete", "getmore", "command"} {
			if _, ok := first.Opcounters[key]; !ok {
				t.Errorf("opcounters lack %s: %v", key, first.Opcounters)
			}
		}
		if first.Connections.Current <= 0 || first.Connections.Available <= 0 || first.Network.BytesIn <= 0 || first.Memory.Resident <= 0 {
			t.Errorf("gauges = %+v %+v %+v", first.Connections, first.Network, first.Memory)
		}
		if first.Cache == nil || first.Cache.MaxBytes <= 0 || first.Cache.Bytes <= 0 {
			t.Errorf("cache = %+v", first.Cache)
		}
		if first.Role == "" || first.Topology == "" {
			t.Errorf("role %q, topology %q", first.Role, first.Topology)
		}
		// Counters move: work done between two snapshots shows in the second.
		for i := 0; i < 5; i++ {
			_, _ = MongoFindDocuments(ctx, client, db, coll, MongoFindSpec{}, false)
		}
		second, err := MongoServerSnapshot(ctx, client)
		if err != nil {
			t.Fatal(err)
		}
		if second.Opcounters["query"] < first.Opcounters["query"]+5 || second.Documents.Returned < first.Documents.Returned+10 {
			t.Errorf("counters did not move: query %d → %d, returned %d → %d",
				first.Opcounters["query"], second.Opcounters["query"], first.Documents.Returned, second.Documents.Returned)
		}
		if second.Timestamp < first.Timestamp {
			t.Errorf("the server's clock went backwards: %d → %d", first.Timestamp, second.Timestamp)
		}
	})

	t.Run("the status map keeps its keys and gains the counters", func(t *testing.T) {
		status, err := MongoServerStatus(ctx, client)
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		for _, key := range []string{"host", "version", "uptime", "connections", "network", "opcounters", "mem",
			"timestamp", "documents", "scanned", "queue", "cache", "role", "storageEngine"} {
			if _, ok := status[key]; !ok {
				t.Errorf("status lacks %q", key)
			}
		}
		if _, ok := status["version"].(string); !ok {
			t.Errorf("version is a %T", status["version"])
		}
		conns, ok := status["connections"].(map[string]any)
		if !ok {
			t.Fatalf("connections is a %T", status["connections"])
		}
		if n, ok := conns["current"].(int64); !ok || n <= 0 {
			t.Errorf("connections.current = %v (%T)", conns["current"], conns["current"])
		}
		if _, err := json.Marshal(status); err != nil {
			t.Errorf("the status map does not encode: %v", err)
		}
	})

	t.Run("databases are listed with their figures", func(t *testing.T) {
		dbs, err := MongoDatabaseStats(ctx, client)
		if err != nil {
			t.Fatalf("database stats: %v", err)
		}
		found := false
		for _, d := range dbs {
			if d.Name == db {
				found = true
				if !d.StatsKnown || d.Collections < 1 || d.Objects < 2 || d.DataSize <= 0 || d.Indexes < 1 {
					t.Errorf("stats of %s = %+v", db, d)
				}
			}
		}
		if !found {
			t.Errorf("%s is not among %d databases", db, len(dbs))
		}
	})

	t.Run("a running operation is listed and can be killed", func(t *testing.T) {
		done := make(chan error, 1)
		go func() {
			_, err := MongoFindDocuments(context.Background(), client, db, coll, MongoFindSpec{
				Filter: `{ $where: "sleep(8000) || true" }`, MaxTimeMS: 30000,
			}, false)
			done <- err
		}()
		var victim *MongoOperation
		deadline := time.Now().Add(10 * time.Second)
		for victim == nil && time.Now().Before(deadline) {
			ops, err := MongoCurrentOps(ctx, client, false)
			if err != nil {
				t.Fatalf("currentOp: %v", err)
			}
			for i, op := range ops {
				if op.Namespace == db+"."+coll && strings.Contains(op.Command, "$where") {
					victim = &ops[i]
				}
				if strings.Contains(op.Command, `"currentOp"`) {
					t.Errorf("the listing lists itself: %+v", op)
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		if victim == nil {
			t.Fatal("the running find never appeared in currentOp")
		}
		if victim.OpID == "" || !victim.Active || victim.Client == "" || victim.Op == "" {
			t.Errorf("operation = %+v", victim)
		}
		if err := MongoKillOp(ctx, client, victim.OpID); err != nil {
			t.Fatalf("killOp: %v", err)
		}
		select {
		case err := <-done:
			if err == nil || MongoTimedOut(err) {
				t.Errorf("the killed find ended with %v, want an interruption", err)
			}
		case <-time.After(7 * time.Second):
			t.Error("the find kept running after killOp")
		}
		for _, bad := range []string{"", "abc", "1; drop"} {
			if err := MongoKillOp(ctx, client, bad); err == nil {
				t.Errorf("killOp(%q) was accepted", bad)
			}
		}
		if all, err := MongoCurrentOps(ctx, client, true); err != nil || len(all) == 0 {
			t.Errorf("currentOp with idle connections: %d, %v", len(all), err)
		}
	})

	t.Run("the profiler is read, set and read back", func(t *testing.T) {
		before, err := MongoProfilerStatus(ctx, client, db)
		if err != nil {
			t.Fatalf("profiler status: %v", err)
		}
		// Whatever this sets on a shared server is put back.
		t.Cleanup(func() {
			_, _ = MongoSetProfiler(context.Background(), client, db, MongoProfilerChange{Level: before.Level, SlowMs: &before.SlowMs, SampleRate: &before.SampleRate})
			_ = MongoDropCollection(context.Background(), client, db, "system.profile")
		})
		set, err := MongoSetProfiler(ctx, client, db, MongoProfilerChange{Level: 2, SlowMs: &before.SlowMs})
		if err != nil || set.Level != 2 || set.SlowMs != before.SlowMs {
			t.Fatalf("set profiler: %+v, %v", set, err)
		}
		if _, err := MongoFindDocuments(ctx, client, db, coll, MongoFindSpec{Filter: `{ a: { $gte: 1 } }`}, false); err != nil {
			t.Fatal(err)
		}
		profile, err := MongoProfileRead(ctx, client, db, 50, 0)
		if err != nil {
			t.Fatalf("read profile: %v", err)
		}
		var seen *MongoProfileEntry
		for i, e := range profile.Entries {
			if e.Namespace == db+"."+coll && e.Op == "query" {
				seen = &profile.Entries[i]
				break
			}
		}
		if seen == nil {
			t.Fatalf("the find is not in the profile (%d entries)", len(profile.Entries))
		}
		if seen.Time == "" || seen.PlanSummary != "COLLSCAN" || seen.DocsExamined != 2 || seen.Returned != 2 || !strings.Contains(seen.Command, `"find"`) {
			t.Errorf("profile entry = %+v", *seen)
		}
		if slow, err := MongoProfileRead(ctx, client, db, 50, 1<<40); err != nil || len(slow.Entries) != 0 {
			t.Errorf("entries slower than forever: %d, %v", len(slow.Entries), err)
		}
		off, err := MongoSetProfiler(ctx, client, db, MongoProfilerChange{Level: 0})
		if err != nil || off.Level != 0 {
			t.Errorf("turning the profiler off: %+v, %v", off, err)
		}
		for _, bad := range []MongoProfilerChange{{Level: 3}, {Level: -1}} {
			if _, err := MongoSetProfiler(ctx, client, db, bad); err == nil {
				t.Errorf("profiler level %d was accepted", bad.Level)
			}
		}
	})

	t.Run("replication reports what the server is", func(t *testing.T) {
		status, err := MongoReplicationStatus(ctx, client)
		if err != nil {
			t.Fatalf("replication status: %v", err)
		}
		snap, _ := MongoServerSnapshot(ctx, client)
		if status.ReplicaSet != (snap.Topology == "replicaset") {
			t.Errorf("replication says replica set %v, the snapshot says %s", status.ReplicaSet, snap.Topology)
		}
		if !status.ReplicaSet && status.Reason == "" {
			t.Error("a standalone server gives no reason")
		}
	})
}

func TestLiveMongoReplicaSet(t *testing.T) {
	client, _ := liveMongoReplicaSet(t)
	ctx := context.Background()
	status, err := MongoReplicationStatus(ctx, client)
	if err != nil {
		t.Fatalf("replication status: %v", err)
	}
	if !status.ReplicaSet || status.SetName == "" || status.MyState != "PRIMARY" || len(status.Members) != 1 {
		t.Fatalf("status = %+v", status)
	}
	m := status.Members[0]
	if !m.Self || !m.Health || m.State != "PRIMARY" || m.Name == "" || m.OptimeDate == "" || m.LagSeconds == nil || *m.LagSeconds != 0 {
		t.Errorf("member = %+v", m)
	}
	if status.Oplog == nil || status.Oplog.SizeBytes <= 0 || status.Oplog.First == "" || status.Oplog.Last == "" {
		t.Errorf("oplog = %+v", status.Oplog)
	}
	snap, err := MongoServerSnapshot(ctx, client)
	if err != nil || snap.Topology != "replicaset" || snap.Role != "primary" || snap.SetName != status.SetName {
		t.Errorf("snapshot of a replica set = %+v, %v", snap, err)
	}
}

func TestLiveMongoUsersAndRoles(t *testing.T) {
	// Accounts are made on the private server only: the shared one is other
	// people's too.
	client, db := liveMongoReplicaSet(t)
	ctx := context.Background()
	const user, role = "jd_test_user", "jd_test_role"
	drop := func() {
		_ = MongoDropUserIn(context.Background(), client, db, user)
		_ = MongoDropUserIn(context.Background(), client, "admin", user)
		_ = client.Database(db).RunCommand(context.Background(), bson.D{{Key: "dropRole", Value: role}}).Err()
	}
	drop()
	t.Cleanup(drop)

	if err := client.Database(db).RunCommand(ctx, bson.D{
		{Key: "createRole", Value: role},
		{Key: "privileges", Value: bson.A{bson.D{
			{Key: "resource", Value: bson.D{{Key: "db", Value: db}, {Key: "collection", Value: "orders"}}},
			{Key: "actions", Value: bson.A{"find", "insert"}},
		}}},
		{Key: "roles", Value: bson.A{bson.D{{Key: "role", Value: "read"}, {Key: "db", Value: db}}}},
	}).Err(); err != nil {
		t.Fatalf("createRole: %v", err)
	}

	t.Run("roles: built in and custom", func(t *testing.T) {
		roles, err := MongoListRoles(ctx, client, db)
		if err != nil {
			t.Fatalf("list roles: %v", err)
		}
		if len(roles) < 5 || roles[0].Role != role || roles[0].Builtin {
			t.Fatalf("roles = %d, first %+v; custom roles come first", len(roles), roles[0])
		}
		custom := roles[0]
		if len(custom.Privileges) != 1 || !strings.Contains(custom.Privileges[0].Resource, `"collection":"orders"`) ||
			strings.Join(custom.Privileges[0].Actions, ",") != "find,insert" || len(custom.Roles) != 1 || custom.Roles[0].Role != "read" {
			t.Errorf("custom role = %+v", custom)
		}
		names := map[string]bool{}
		for _, r := range roles {
			names[r.Role] = r.Builtin
			if r.Builtin && len(r.Privileges) != 0 {
				t.Errorf("built-in role %s lists privileges", r.Role)
			}
		}
		for _, want := range []string{"read", "readWrite", "dbAdmin", "dbOwner"} {
			if !names[want] {
				t.Errorf("built-in role %s is missing or not marked built in", want)
			}
		}
		admin, err := MongoListRoles(ctx, client, "admin")
		if err != nil {
			t.Fatal(err)
		}
		hasRoot := false
		for _, r := range admin {
			hasRoot = hasRoot || r.Role == "root"
		}
		if !hasRoot {
			t.Error("admin's roles lack root")
		}
	})

	t.Run("users in any database, without credentials", func(t *testing.T) {
		if err := MongoCreateUserIn(ctx, client, db, user, "s3cret-pass", []MongoRoleRef{{Role: "read", DB: db}}); err != nil {
			t.Fatalf("create user in %s: %v", db, err)
		}
		if err := MongoCreateUserIn(ctx, client, "admin", user, "s3cret-pass", []MongoRoleRef{{Role: "root", DB: "admin"}}); err != nil {
			t.Fatalf("create user in admin: %v", err)
		}
		all, err := MongoListUsers(ctx, client, "")
		if err != nil {
			t.Fatalf("list users: %v", err)
		}
		found := map[string]MongoUser{}
		for _, u := range all {
			if u.User == user {
				found[u.DB] = u
			}
		}
		if len(found) != 2 {
			t.Fatalf("the account was found in %d databases, want 2: %+v", len(found), all)
		}
		if u := found[db]; u.Superuser || len(u.Roles) != 1 || u.Roles[0] != (MongoRoleRef{Role: "read", DB: db}) || len(u.Mechanisms) == 0 {
			t.Errorf("user in %s = %+v", db, u)
		}
		if u := found["admin"]; !u.Superuser {
			t.Errorf("user in admin = %+v", u)
		}
		encoded, _ := json.Marshal(all)
		for _, secret := range []string{"s3cret-pass", "credentials", "storedKey", "salt"} {
			if strings.Contains(string(encoded), secret) {
				t.Errorf("the user list carries %q: %s", secret, encoded)
			}
		}
		one, err := MongoListUsers(ctx, client, db)
		if err != nil || len(one) != 1 || one[0].User != user {
			t.Errorf("users of %s = %+v, %v", db, one, err)
		}

		if err := MongoGrantRoles(ctx, client, db, user, []MongoRoleRef{{Role: role, DB: db}, {Role: "readWrite", DB: db}}); err != nil {
			t.Fatalf("grant: %v", err)
		}
		if got, _ := MongoListUsers(ctx, client, db); len(got) != 1 || len(got[0].Roles) != 3 {
			t.Errorf("roles after grant = %+v", got)
		}
		if err := MongoRevokeRoles(ctx, client, db, user, []MongoRoleRef{{Role: "readWrite", DB: db}}); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		got, _ := MongoListUsers(ctx, client, db)
		if len(got) != 1 || len(got[0].Roles) != 2 {
			t.Errorf("roles after revoke = %+v", got)
		}
		if err := MongoSetUserPassword(ctx, client, db, user, "an0ther-pass"); err != nil {
			t.Errorf("password change: %v", err)
		}
		// A password change is only a password change.
		if after, _ := MongoListUsers(ctx, client, db); len(after) != 1 || len(after[0].Roles) != 2 {
			t.Errorf("the password change touched the roles: %+v", after)
		}

		for name, err := range map[string]error{
			"an empty password":  MongoCreateUserIn(ctx, client, db, "jd_x", "", nil),
			"an empty name":      MongoCreateUserIn(ctx, client, db, "", "pw123456", nil),
			"a bad database":     MongoCreateUserIn(ctx, client, "a.b", "jd_x", "pw123456", nil),
			"a role with no db":  MongoGrantRoles(ctx, client, db, user, []MongoRoleRef{{Role: "read"}}),
			"a grant of nothing": MongoGrantRoles(ctx, client, db, user, nil),
			"a missing user":     MongoSetUserPassword(ctx, client, db, "jd_nobody", "pw123456"),
		} {
			if err == nil {
				t.Errorf("%s was accepted", name)
			}
		}

		if err := MongoDropUserIn(ctx, client, db, user); err != nil {
			t.Errorf("drop: %v", err)
		}
		if left, _ := MongoListUsers(ctx, client, db); len(left) != 0 {
			t.Errorf("users left in %s: %+v", db, left)
		}
		// The admin-only list the shared roles page reads still works.
		legacy, err := MongoUsers(ctx, client)
		if err != nil || len(legacy) != 1 || legacy[0].Name != user || !legacy[0].Superuser {
			t.Errorf("legacy list = %+v, %v", legacy, err)
		}
	})
}

func TestLiveMongoCommand(t *testing.T) {
	client, db := liveMongoDB(t)
	ctx := context.Background()
	const coll = "jd_command"
	freshCollection(t, client, db, coll)

	run := func(text string) (*MongoCommandReply, MongoVerdict, error) {
		t.Helper()
		cmd, verdict, err := MongoClassifyCommand(text)
		if err != nil {
			t.Fatalf("classify %s: %v", text, err)
		}
		reply, err := MongoRunCommand(ctx, client, db, cmd)
		return reply, verdict, err
	}

	reply, verdict, err := run(`{ ping: 1 }`)
	if err != nil || verdict.Class != MongoClassRead || !strings.Contains(reply.Canonical, `"ok":{"$numberDouble":"1.0"}`) {
		t.Fatalf("ping: %+v, %+v, %v", reply, verdict, err)
	}
	if !json.Valid(reply.Relaxed) || reply.Size == 0 {
		t.Errorf("reply = %+v", reply)
	}

	if _, v, err := run(`{ insert: "` + coll + `", documents: [ { _id: 1, at: ISODate("2024-01-01"), n: NumberLong(5) } ] }`); err != nil || v.Class != MongoClassWrite || v.Target != coll {
		t.Fatalf("insert: %+v, %v", v, err)
	}
	found, v, err := run(`{ find: "` + coll + `", filter: { _id: 1 } }`)
	if err != nil || v.Class != MongoClassRead {
		t.Fatalf("find: %+v, %v", v, err)
	}
	// The reply is canonical: the date and the long come back as what they are.
	for _, want := range []string{`"at":{"$date":{"$numberLong":"1704067200000"}}`, `"n":{"$numberLong":"5"}`} {
		if !strings.Contains(found.Canonical, want) {
			t.Errorf("find reply lacks %s: %s", want, found.Canonical)
		}
	}
	if _, v, err := run(`{ delete: "` + coll + `", deletes: [ { q: { _id: 1 }, limit: 1 } ] }`); err != nil || v.Class != MongoClassDestructive {
		t.Errorf("delete: %+v, %v", v, err)
	}
	// A cursor left open is closed, and the reply says there was more.
	if _, _, err := run(`{ insert: "` + coll + `", documents: [ {a: 1}, {a: 2}, {a: 3} ] }`); err != nil {
		t.Fatal(err)
	}
	first, _, err := run(`{ find: "` + coll + `", batchSize: 1 }`)
	if err != nil || !first.More || !strings.Contains(first.Canonical, `"firstBatch"`) {
		t.Fatalf("find with a small batch: %+v, %v", first, err)
	}
	// Asking the server to close it again finds it already gone.
	id := regexp.MustCompile(`"id":\{"\$numberLong":"(\d+)"\}`).FindStringSubmatch(first.Canonical)
	if id == nil {
		t.Fatalf("no cursor id in %s", first.Canonical)
	}
	again, _, err := run(`{ killCursors: "` + coll + `", cursors: [ NumberLong("` + id[1] + `") ] }`)
	if err != nil || !strings.Contains(again.Canonical, `"cursorsNotFound":[{"$numberLong":"`+id[1]+`"}]`) {
		t.Errorf("the console left its cursor open: %+v, %v", again, err)
	}
	whole, _, err := run(`{ find: "` + coll + `" }`)
	if err != nil || whole.More {
		t.Errorf("find that fits one batch: more=%v, %v", whole.More, err)
	}
	if _, _, err := run(`{ find: "` + coll + `", filter: { $bogus: 1 } }`); err == nil {
		t.Error("a command the server refuses returned no error")
	}
	ping, _ := mongoParseDocument("command", `{"ping":1}`)
	if _, err := MongoRunCommand(ctx, client, "a.b", ping); err == nil {
		t.Error("a bad database name was accepted")
	}
}
