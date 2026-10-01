package dbx

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// The commands built from a form, checked as the documents the server would
// receive. No server is needed to know a create command is right.

func commandJSON(t *testing.T, cmd bson.D) string {
	t.Helper()
	out, err := bson.MarshalExtJSON(cmd, false, false)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestCreateCollectionCommand(t *testing.T) {
	expire := int64(3600)
	for _, c := range []struct {
		name string
		spec MongoCollectionSpec
		want string
	}{
		{"plain", MongoCollectionSpec{}, `{"create":"c"}`},
		{"capped", MongoCollectionSpec{Capped: true, Size: 1024, Max: 10}, `{"create":"c","capped":true,"size":1024,"max":10}`},
		{"view", MongoCollectionSpec{ViewOn: "src", Pipeline: `[ { $match: { a: 1 } } ]`},
			`{"create":"c","viewOn":"src","pipeline":[{"$match":{"a":1}}]}`},
		{"view with no pipeline", MongoCollectionSpec{ViewOn: "src"}, `{"create":"c","viewOn":"src","pipeline":[]}`},
		{"time series", MongoCollectionSpec{TimeSeries: &MongoTimeSeries{TimeField: "at", MetaField: "m", Granularity: "hours"}, ExpireAfterSeconds: &expire},
			`{"create":"c","timeseries":{"timeField":"at","metaField":"m","granularity":"hours"},"expireAfterSeconds":3600}`},
		{"time series with a bucket span", MongoCollectionSpec{TimeSeries: &MongoTimeSeries{TimeField: "at", BucketMaxSpanSeconds: 300, BucketRoundingSeconds: 300}},
			`{"create":"c","timeseries":{"timeField":"at","bucketMaxSpanSeconds":300,"bucketRoundingSeconds":300}}`},
		{"clustered", MongoCollectionSpec{ClusteredIndex: true}, `{"create":"c","clusteredIndex":{"key":{"_id":1},"unique":true}}`},
		{"rules and collation", MongoCollectionSpec{Validator: `{ a: { $type: "string" } }`, ValidationLevel: "moderate", ValidationAction: "warn", Collation: `{ locale: "en" }`},
			`{"create":"c","validator":{"a":{"$type":"string"}},"validationLevel":"moderate","validationAction":"warn","collation":{"locale":"en"}}`},
		{"an empty validator is no validator", MongoCollectionSpec{Validator: `{}`}, `{"create":"c"}`},
	} {
		cmd, err := c.spec.command("c")
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got := commandJSON(t, cmd); got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

func TestCollModCommand(t *testing.T) {
	rule, none, pipeline := `{ a: 1 }`, ``, `[ { $limit: 1 } ]`
	size, off, keep := int64(4096), int64(-1), int64(60)
	for _, c := range []struct {
		name    string
		change  MongoCollectionChange
		want    string
		removes bool
	}{
		{"a rule", MongoCollectionChange{Validator: &rule, ValidationLevel: "strict"}, `{"collMod":"c","validator":{"a":1},"validationLevel":"strict"}`, false},
		{"removing the rule", MongoCollectionChange{Validator: &none}, `{"collMod":"c","validator":{}}`, false},
		{"only the action", MongoCollectionChange{ValidationAction: "warn"}, `{"collMod":"c","validationAction":"warn"}`, false},
		{"a view", MongoCollectionChange{ViewOn: "src", Pipeline: &pipeline}, `{"collMod":"c","viewOn":"src","pipeline":[{"$limit":1}]}`, false},
		{"a cap", MongoCollectionChange{CappedSize: &size}, `{"collMod":"c","cappedSize":4096}`, true},
		{"an expiry", MongoCollectionChange{ExpireAfterSeconds: &keep}, `{"collMod":"c","expireAfterSeconds":60}`, true},
		{"no expiry", MongoCollectionChange{ExpireAfterSeconds: &off}, `{"collMod":"c","expireAfterSeconds":"off"}`, false},
		{"granularity", MongoCollectionChange{Granularity: "hours"}, `{"collMod":"c","timeseries":{"granularity":"hours"}}`, false},
	} {
		cmd, err := c.change.command("c")
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got := commandJSON(t, cmd); got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.name, got, c.want)
		}
		if c.change.RemovesData() != c.removes {
			t.Errorf("%s: RemovesData = %v", c.name, c.change.RemovesData())
		}
	}
	zero := int64(0)
	for name, change := range map[string]MongoCollectionChange{
		"nothing":                   {},
		"a view without its source": {Pipeline: &pipeline},
		"a view that writes":        {ViewOn: "src", Pipeline: ptr(`[ { $merge: { into: "x" } } ]`)},
		"a cap of zero":             {CappedSize: &zero},
		"an unknown action":         {ValidationAction: "ignore"},
	} {
		if cmd, err := change.command("c"); err == nil {
			t.Errorf("%s was accepted: %s", name, commandJSON(t, cmd))
		}
	}
}

// ptr is a pointer to a value, for the request fields that tell an absent
// value from an empty one. Every test in the package uses this one.
func ptr[T any](v T) *T { return &v }

func TestIndexModel(t *testing.T) {
	ttl := int64(60)
	model, err := MongoIndexSpec{
		Keys: []MongoIndexKey{{Field: " at ", Type: "desc"}},
		Name: "at_ttl", Unique: true, Sparse: true, Hidden: true,
		ExpireAfterSeconds:      &ttl,
		PartialFilterExpression: `{ at: { $exists: true } }`,
		Collation:               `{ locale: "en", strength: 2 }`,
	}.model()
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := bson.MarshalExtJSON(model.Keys, false, false)
	if string(keys) != `{"at":-1}` {
		t.Errorf("keys = %s", keys)
	}
	o := model.Options
	if *o.Name != "at_ttl" || !*o.Unique || !*o.Sparse || !*o.Hidden || *o.ExpireAfterSeconds != 60 ||
		o.Collation.Locale != "en" || o.Collation.Strength != 2 || o.PartialFilterExpression == nil {
		t.Errorf("options = %+v", o)
	}
	// An index made with no options sends none: "unique: false" is not the
	// same request as no "unique" at all on every server.
	bare, err := MongoIndexSpec{Keys: []MongoIndexKey{{Field: "a", Type: "1"}, {Field: "b.$**", Type: "asc"}, {Field: "$**", Type: "asc"}}}.model()
	if err != nil {
		t.Fatal(err)
	}
	if b := bare.Options; b.Name != nil || b.Unique != nil || b.Sparse != nil || b.Hidden != nil || b.ExpireAfterSeconds != nil {
		t.Errorf("an index with no options carries some: %+v", b)
	}
	many := MongoIndexSpec{}
	for i := 0; i < 33; i++ {
		many.Keys = append(many.Keys, MongoIndexKey{Field: "f" + itoa(i), Type: "asc"})
	}
	if _, err := many.model(); err == nil {
		t.Error("an index of 33 fields was accepted")
	}
}

func TestCollationAndHint(t *testing.T) {
	doc, opts, err := mongoCollation(`{ locale: "ro", strength: 1, caseLevel: true, numericOrdering: true, alternate: "shifted", backwards: false }`)
	if err != nil || doc == nil {
		t.Fatalf("collation: %v", err)
	}
	if opts.Locale != "ro" || opts.Strength != 1 || !opts.CaseLevel || !opts.NumericOrdering || opts.Alternate != "shifted" || opts.Backwards {
		t.Errorf("collation = %+v", opts)
	}
	for _, empty := range []string{"", "  ", "{}"} {
		if doc, opts, err := mongoCollation(empty); err != nil || doc != nil || opts != nil {
			t.Errorf("mongoCollation(%q) = %v, %v, %v", empty, doc, opts, err)
		}
	}
	for _, bad := range []string{`{ strength: 2 }`, `{ locale: 5 }`, `{ locale: "en", speed: 1 }`, `{ locale: "en", strength: "two" }`, `[]`} {
		if _, _, err := mongoCollation(bad); err == nil {
			t.Errorf("collation %s was accepted", bad)
		}
	}

	for text, want := range map[string]any{"": nil, "a_1": "a_1", `"a_1"`: "a_1", `'a_1'`: "a_1", "{}": nil} {
		if got, err := mongoHint(text); err != nil || got != want {
			t.Errorf("mongoHint(%q) = %v, %v; want %v", text, got, err, want)
		}
	}
	hint, err := mongoHint(`{ a: 1, b: -1 }`)
	if raw, ok := hint.(bson.Raw); err != nil || !ok || mongoRelaxedJSON(raw) != `{"a":1,"b":-1}` {
		t.Errorf("hint by key pattern = %v, %v", hint, err)
	}
}

func TestNamesAndBounds(t *testing.T) {
	for name, ok := range map[string]bool{
		"shop": true, "my-db_2": true, "": false, "a.b": false, "a b": false, "a$b": false, "a/b": false,
		strings.Repeat("x", 63): true, strings.Repeat("x", 64): false,
	} {
		if (mongoDatabaseName(name) == nil) != ok {
			t.Errorf("database name %q: accepted %v, want %v", name, !ok, ok)
		}
	}
	for name, ok := range map[string]bool{
		"orders": true, "orders.archive": true, "with space": true, "system.profile": true,
		"": false, "$cmd": false, "a$b": false, "a\x00b": false, strings.Repeat("x", 256): false,
	} {
		if (mongoCollectionName(name) == nil) != ok {
			t.Errorf("collection name %q: accepted %v, want %v", name, !ok, ok)
		}
	}
	for limit, want := range map[int64]int64{0: 50, -5: 50, 1: 1, 1000: 1000, 1001: 1000, 1 << 40: 1000} {
		if got := mongoClampLimit(limit); got != want {
			t.Errorf("mongoClampLimit(%d) = %d, want %d", limit, got, want)
		}
	}
	for ms, want := range map[int64]time.Duration{0: 30 * time.Second, -1: 30 * time.Second, 250: 250 * time.Millisecond, 1 << 40: 5 * time.Minute} {
		if got := mongoMaxTime(ms); got != want {
			t.Errorf("mongoMaxTime(%d) = %s, want %s", ms, got, want)
		}
	}
	for coll, want := range map[string]string{"orders": "db.orders", "a.b": `db.getCollection("a.b")`, "9lives": `db.getCollection("9lives")`, "x y": `db.getCollection("x y")`} {
		if got := mongoCollRef(coll); got != want {
			t.Errorf("mongoCollRef(%q) = %s, want %s", coll, got, want)
		}
	}
	if cut, was := mongoCut("ţţţţ", 5); cut != "ţţ…" || !was {
		t.Errorf("mongoCut split a character: %q", cut)
	}
	if cut, was := mongoCut("short", 10); cut != "short" || was {
		t.Errorf("mongoCut touched a short string: %q", cut)
	}
}

func TestIDFilter(t *testing.T) {
	for _, id := range []string{`{"$oid":"65f1c0ffee0123456789abcd"}`, `"text"`, `42`, `{"a":1}`, `ObjectId("65f1c0ffee0123456789abcd")`, `null`} {
		filter, _, err := mongoIDFilter(id)
		if err != nil {
			t.Errorf("mongoIDFilter(%s): %v", id, err)
			continue
		}
		// $eq keeps a document _id whose first key starts with a dollar sign
		// from being read as an operator.
		if out := commandJSON(t, filter); !strings.HasPrefix(out, `{"_id":{"$eq":`) {
			t.Errorf("filter for %s = %s", id, out)
		}
	}
	for _, id := range []string{``, `[1,2]`, `/re/`, `undefined`, `{`} {
		if _, _, err := mongoIDFilter(id); err == nil {
			t.Errorf("an _id of %q was accepted", id)
		}
	}
}

// The grid the first browser drew still has to encode: a NaN used to fail
// the whole response after its status had been sent, and a 64-bit integer
// arrived in the browser as a different number.
func TestLegacyGridValuesEncode(t *testing.T) {
	doc, _ := bson.Marshal(bson.D{
		{Key: "_id", Value: primitive.ObjectID{1}},
		{Key: "nan", Value: math.NaN()},
		{Key: "inf", Value: math.Inf(1)},
		{Key: "big", Value: int64(math.MaxInt64)},
		{Key: "small", Value: int64(7)},
		{Key: "at", Value: primitive.NewDateTimeFromTime(time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC))},
		{Key: "nested", Value: bson.D{{Key: "nan", Value: math.NaN()}}},
	})
	grid := mongoGrid([]bson.Raw{doc}, false)
	out, err := json.Marshal(grid)
	if err != nil {
		t.Fatalf("the grid does not encode: %v", err)
	}
	for _, want := range []string{`"NaN"`, `"+Inf"`, `"9223372036854775807"`, `,7]`, `"2024-05-01T00:00:00Z"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("grid lacks %s: %s", want, out)
		}
	}
	if grid.Columns[0] != "_id" || len(grid.Columns) != 7 {
		t.Errorf("columns = %v", grid.Columns)
	}
}

func TestMongoPlainValues(t *testing.T) {
	doc, _ := bson.Marshal(bson.D{
		{Key: "current", Value: int32(5)}, {Key: "total", Value: int64(9)}, {Key: "ratio", Value: 0.5},
		{Key: "nan", Value: math.NaN()}, {Key: "name", Value: "x"}, {Key: "ok", Value: true}, {Key: "none", Value: nil},
		{Key: "at", Value: primitive.NewDateTimeFromTime(time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC))},
		{Key: "list", Value: bson.A{int32(1), bson.D{{Key: "k", Value: "v"}}}},
		{Key: "ts", Value: primitive.Timestamp{T: 1, I: 2}},
	})
	plain, ok := mongoPlain(bson.RawValue{Type: bson.TypeEmbeddedDocument, Value: doc}).(map[string]any)
	if !ok {
		t.Fatal("a document did not become a map")
	}
	if plain["current"] != int64(5) || plain["total"] != int64(9) || plain["ratio"] != 0.5 || plain["nan"] != "NaN" ||
		plain["at"] != "2024-05-01T00:00:00Z" || plain["none"] != nil || plain["ok"] != true {
		t.Errorf("plain = %#v", plain)
	}
	if _, err := json.Marshal(plain); err != nil {
		t.Errorf("plain values do not encode: %v", err)
	}
}
