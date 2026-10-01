package dbx

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Indexes: what a collection has, what each costs and whether anything uses
// it, and the options an index is made with.
//
// Size and usage are the two figures that decide whether an index earns its
// keep, and neither is in the index's definition: size comes from the
// collection's statistics and usage from $indexStats, which counts since the
// server last started. Both are read alongside the list and either may be
// missing — a view has no statistics, a restricted account may not read
// them — so each is reported as unknown rather than as zero.

// MongoIndexKey is one field of an index's key pattern.
type MongoIndexKey struct {
	Field string `json:"field"`
	// Type is asc, desc, text, hashed, 2dsphere, 2d, or whatever other index
	// type the server names.
	Type string `json:"type"`
}

// MongoIndexUsage is how often queries have used an index.
type MongoIndexUsage struct {
	Ops int64 `json:"ops"`
	// Since is when counting started: the server's last start, or the
	// index's creation.
	Since string `json:"since"`
}

// MongoIndex is one index with its properties, size and usage.
type MongoIndex struct {
	Name string          `json:"name"`
	Keys []MongoIndexKey `json:"keys"`
	// Key is the key pattern as relaxed Extended JSON.
	Key string `json:"key"`
	// Kind is regular, text, geospatial, hashed or wildcard.
	Kind string `json:"kind"`
	// Primary marks the _id index, which cannot be dropped or hidden.
	Primary bool `json:"primary"`
	Unique  bool `json:"unique"`
	Sparse  bool `json:"sparse"`
	Hidden  bool `json:"hidden"`
	// ExpireAfterSeconds makes this a TTL index when set.
	ExpireAfterSeconds *int64 `json:"expireAfterSeconds"`
	// PartialFilterExpression, Collation, Weights and WildcardProjection are
	// relaxed Extended JSON, empty when the index has none.
	PartialFilterExpression string `json:"partialFilterExpression,omitempty"`
	Collation               string `json:"collation,omitempty"`
	Weights                 string `json:"weights,omitempty"`
	WildcardProjection      string `json:"wildcardProjection,omitempty"`
	DefaultLanguage         string `json:"defaultLanguage,omitempty"`
	// Size is in bytes, -1 when unknown.
	Size int64 `json:"size"`
	// Usage is nil when the server would not say.
	Usage *MongoIndexUsage `json:"usage"`
	// Building is true while the index is still being built.
	Building bool `json:"building"`
}

// MongoIndexList is a collection's indexes.
type MongoIndexList struct {
	Indexes []MongoIndex `json:"indexes"`
	// UsageNote says why usage is missing, when it is.
	UsageNote string `json:"usageNote,omitempty"`
}

// MongoListIndexes lists a collection's indexes with size and usage.
func MongoListIndexes(ctx context.Context, client *mongo.Client, dbName, collection string) (*MongoIndexList, error) {
	if err := mongoNamespace(dbName, collection); err != nil {
		return nil, err
	}
	db := client.Database(dbName)
	coll := db.Collection(collection)
	indexes, err := mongoIndexDefinitions(ctx, coll)
	if err != nil {
		return nil, err
	}
	out := &MongoIndexList{Indexes: indexes}

	if stats, err := mongoCollStats(ctx, db, collection); err == nil {
		if sizes, ok := stats.Lookup("indexSizes").DocumentOK(); ok {
			for i := range out.Indexes {
				if v, err := sizes.LookupErr(out.Indexes[i].Name); err == nil {
					out.Indexes[i].Size = mongoInt(v)
				}
			}
		}
	}

	usage, building, err := mongoIndexStats(ctx, coll)
	if err != nil {
		out.UsageNote = err.Error()
		return out, nil
	}
	for i := range out.Indexes {
		ix := &out.Indexes[i]
		if u, ok := usage[ix.Name]; ok {
			ix.Usage = &u
		}
		ix.Building = building[ix.Name]
	}
	return out, nil
}

// mongoIndexDefinitions lists a collection's indexes as defined, without the
// two figures that each cost a command of their own.
func mongoIndexDefinitions(ctx context.Context, coll *mongo.Collection) ([]MongoIndex, error) {
	cur, err := coll.Indexes().List(ctx)
	if err != nil {
		return nil, err
	}
	defer cur.Close(context.Background())
	out := []MongoIndex{}
	for cur.Next(ctx) {
		out = append(out, mongoReadIndex(cur.Current))
	}
	return out, cur.Err()
}

func mongoReadIndex(spec bson.Raw) MongoIndex {
	ix := MongoIndex{Keys: []MongoIndexKey{}, Size: -1, Kind: "regular"}
	ix.Name, _ = spec.Lookup("name").StringValueOK()
	ix.Primary = ix.Name == "_id_"
	ix.Unique, _ = spec.Lookup("unique").BooleanOK()
	ix.Sparse, _ = spec.Lookup("sparse").BooleanOK()
	ix.Hidden, _ = spec.Lookup("hidden").BooleanOK()
	ix.DefaultLanguage, _ = spec.Lookup("default_language").StringValueOK()
	if v, err := spec.LookupErr("expireAfterSeconds"); err == nil && v.IsNumber() {
		n := mongoInt(v)
		ix.ExpireAfterSeconds = &n
	}
	for field, dst := range map[string]*string{
		"partialFilterExpression": &ix.PartialFilterExpression,
		"collation":               &ix.Collation,
		"weights":                 &ix.Weights,
		"wildcardProjection":      &ix.WildcardProjection,
	} {
		if doc, ok := spec.Lookup(field).DocumentOK(); ok {
			*dst = mongoRelaxedJSON(doc)
		}
	}
	key, _ := spec.Lookup("key").DocumentOK()
	ix.Key = mongoRelaxedJSON(key)
	elems, _ := key.Elements()
	for _, e := range elems {
		field, kind := e.Key(), mongoIndexKeyType(e.Value())
		switch {
		case field == "_fts":
			// A text index stores its key as two internal fields and keeps
			// the fields it actually covers in weights.
			ix.Kind = "text"
			if weights, ok := spec.Lookup("weights").DocumentOK(); ok {
				if ws, err := weights.Elements(); err == nil {
					for _, w := range ws {
						ix.Keys = append(ix.Keys, MongoIndexKey{Field: w.Key(), Type: "text"})
					}
				}
			}
			continue
		case field == "_ftsx":
			continue
		case kind == "text":
			ix.Kind = "text"
		case kind == "hashed":
			ix.Kind = "hashed"
		case strings.HasPrefix(kind, "2d") || kind == "geoHaystack":
			ix.Kind = "geospatial"
		case field == "$**" || strings.HasSuffix(field, ".$**"):
			ix.Kind = "wildcard"
		}
		ix.Keys = append(ix.Keys, MongoIndexKey{Field: field, Type: kind})
	}
	return ix
}

// mongoIndexKeyType names the value beside a field in a key pattern: a
// direction when it is a number, an index type when it is a string.
func mongoIndexKeyType(v bson.RawValue) string {
	if s, ok := v.StringValueOK(); ok {
		return s
	}
	if f, ok := v.DoubleOK(); ok {
		if f < 0 {
			return "desc"
		}
		return "asc"
	}
	if v.IsNumber() && mongoInt(v) < 0 {
		return "desc"
	}
	return "asc"
}

func mongoIndexStats(ctx context.Context, coll *mongo.Collection) (map[string]MongoIndexUsage, map[string]bool, error) {
	cur, err := coll.Aggregate(ctx, bson.A{bson.D{{Key: "$indexStats", Value: bson.D{}}}},
		options.Aggregate().SetMaxTime(mongoDefaultMaxTime))
	if err != nil {
		return nil, nil, err
	}
	defer cur.Close(context.Background())
	usage := map[string]MongoIndexUsage{}
	building := map[string]bool{}
	since := map[string]time.Time{}
	for cur.Next(ctx) {
		doc := cur.Current
		name, _ := doc.Lookup("name").StringValueOK()
		u := usage[name]
		// A sharded collection answers once per shard; the counts add up and
		// the counting started when the last of them did.
		u.Ops += mongoLookupInt(doc, "accesses", "ops")
		if t, ok := doc.Lookup("accesses", "since").TimeOK(); ok && t.After(since[name]) {
			since[name] = t
			u.Since = t.UTC().Format(time.RFC3339)
		}
		usage[name] = u
		if b, ok := doc.Lookup("building").BooleanOK(); ok && b {
			building[name] = true
		}
	}
	return usage, building, cur.Err()
}

// MongoIndexSpec is what an index is created with.
type MongoIndexSpec struct {
	Keys []MongoIndexKey `json:"keys"`
	// Name is optional; the server's usual field_direction name is used
	// without one.
	Name   string `json:"name"`
	Unique bool   `json:"unique"`
	Sparse bool   `json:"sparse"`
	Hidden bool   `json:"hidden"`
	// ExpireAfterSeconds makes a TTL index: documents are deleted that many
	// seconds after the date in the indexed field.
	ExpireAfterSeconds      *int64 `json:"expireAfterSeconds"`
	PartialFilterExpression string `json:"partialFilterExpression"`
	Collation               string `json:"collation"`
	Weights                 string `json:"weights"`
	DefaultLanguage         string `json:"defaultLanguage"`
	WildcardProjection      string `json:"wildcardProjection"`
}

// Expires reports whether the index is a TTL index. Building one starts
// deleting every document already older than the limit, so the handler asks
// for more than it does for an ordinary index.
func (s MongoIndexSpec) Expires() bool { return s.ExpireAfterSeconds != nil }

// mongoIndexTypes are the key types an index can be created with here.
var mongoIndexTypes = map[string]any{
	"asc": int32(1), "1": int32(1), "desc": int32(-1), "-1": int32(-1),
	"text": "text", "hashed": "hashed", "2dsphere": "2dsphere", "2d": "2d",
}

func (s MongoIndexSpec) model() (mongo.IndexModel, error) {
	if len(s.Keys) == 0 {
		return mongo.IndexModel{}, fmt.Errorf("an index needs at least one field")
	}
	if len(s.Keys) > 32 {
		return mongo.IndexModel{}, fmt.Errorf("an index holds 32 fields at most")
	}
	keys := bson.D{}
	seen := map[string]bool{}
	for _, k := range s.Keys {
		field := strings.TrimSpace(k.Field)
		if field == "" || strings.ContainsRune(field, 0) {
			return mongo.IndexModel{}, fmt.Errorf("an index field has no name")
		}
		// "$**" is the wildcard; no other field name may start with a
		// dollar sign.
		if strings.HasPrefix(field, "$") && field != "$**" {
			return mongo.IndexModel{}, fmt.Errorf("%q is not a field name", field)
		}
		if seen[field] {
			return mongo.IndexModel{}, fmt.Errorf("field %q is in the index twice", field)
		}
		seen[field] = true
		value, ok := mongoIndexTypes[k.Type]
		if !ok {
			return mongo.IndexModel{}, fmt.Errorf("index type %q is not one of asc, desc, text, hashed, 2dsphere, 2d", k.Type)
		}
		keys = append(keys, bson.E{Key: field, Value: value})
	}
	opts := options.Index()
	if name := strings.TrimSpace(s.Name); name != "" {
		if strings.ContainsAny(name, "$\x00") || len(name) > 127 {
			return mongo.IndexModel{}, fmt.Errorf("index name %q is not usable", name)
		}
		opts.SetName(name)
	}
	if s.Unique {
		opts.SetUnique(true)
	}
	if s.Sparse {
		opts.SetSparse(true)
	}
	if s.Hidden {
		opts.SetHidden(true)
	}
	if s.ExpireAfterSeconds != nil {
		if *s.ExpireAfterSeconds < 0 || *s.ExpireAfterSeconds > math.MaxInt32 {
			return mongo.IndexModel{}, fmt.Errorf("expireAfterSeconds must be between 0 and %d", math.MaxInt32)
		}
		if len(s.Keys) != 1 {
			return mongo.IndexModel{}, fmt.Errorf("a TTL index covers exactly one field, which must hold a date")
		}
		opts.SetExpireAfterSeconds(int32(*s.ExpireAfterSeconds))
	}
	for _, f := range []struct {
		what, text string
		set        func(bson.Raw)
	}{
		{"partialFilterExpression", s.PartialFilterExpression, func(d bson.Raw) { opts.SetPartialFilterExpression(d) }},
		{"weights", s.Weights, func(d bson.Raw) { opts.SetWeights(d) }},
		{"wildcardProjection", s.WildcardProjection, func(d bson.Raw) { opts.SetWildcardProjection(d) }},
	} {
		if strings.TrimSpace(f.text) == "" {
			continue
		}
		doc, err := mongoParseDocument(f.what, f.text)
		if err != nil {
			return mongo.IndexModel{}, err
		}
		if !mongoIsEmpty(doc) {
			f.set(doc)
		}
	}
	_, collation, err := mongoCollation(s.Collation)
	if err != nil {
		return mongo.IndexModel{}, err
	}
	if collation != nil {
		opts.SetCollation(collation)
	}
	if s.DefaultLanguage != "" {
		opts.SetDefaultLanguage(s.DefaultLanguage)
	}
	return mongo.IndexModel{Keys: keys, Options: opts}, nil
}

// MongoCreateIndex builds an index and returns its name. The build takes as
// long as the collection is large; the caller's context is its only limit.
func MongoCreateIndex(ctx context.Context, client *mongo.Client, dbName, collection string, spec MongoIndexSpec) (string, error) {
	if err := mongoNamespace(dbName, collection); err != nil {
		return "", err
	}
	model, err := spec.model()
	if err != nil {
		return "", err
	}
	return client.Database(dbName).Collection(collection).Indexes().CreateOne(ctx, model)
}

// mongoIndexName checks the name of an existing index to act on.
func mongoIndexName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return fmt.Errorf("an index name is required")
	case name == "*":
		// To dropIndexes, "*" means every index on the collection.
		return fmt.Errorf("an index is dropped by its name, one at a time")
	case name == "_id_":
		return fmt.Errorf("the _id index is part of the collection and cannot be changed or dropped")
	case strings.ContainsRune(name, 0):
		return fmt.Errorf("index name is not usable")
	}
	return nil
}

// MongoDropIndex drops one index by name.
func MongoDropIndex(ctx context.Context, client *mongo.Client, dbName, collection, name string) error {
	if err := mongoNamespace(dbName, collection); err != nil {
		return err
	}
	if err := mongoIndexName(name); err != nil {
		return err
	}
	_, err := client.Database(dbName).Collection(collection).Indexes().DropOne(ctx, name)
	return err
}

// MongoIndexChange is what can be changed on an index without rebuilding it.
type MongoIndexChange struct {
	Name string `json:"name"`
	// Hidden hides the index from the query planner, or shows it again. A
	// hidden index is still maintained, so hiding one is how to find out
	// what dropping it would do before dropping it.
	Hidden *bool `json:"hidden"`
	// ExpireAfterSeconds changes a TTL index's limit.
	ExpireAfterSeconds *int64 `json:"expireAfterSeconds"`
}

// RemovesData reports whether the change can delete documents, which a new
// TTL limit does to everything already older than it.
func (c MongoIndexChange) RemovesData() bool { return c.ExpireAfterSeconds != nil }

// MongoModifyIndex hides or unhides an index, or changes its TTL.
func MongoModifyIndex(ctx context.Context, client *mongo.Client, dbName, collection string, change MongoIndexChange) error {
	if err := mongoNamespace(dbName, collection); err != nil {
		return err
	}
	if err := mongoIndexName(change.Name); err != nil {
		return err
	}
	index := bson.D{{Key: "name", Value: change.Name}}
	if change.Hidden != nil {
		index = append(index, bson.E{Key: "hidden", Value: *change.Hidden})
	}
	if change.ExpireAfterSeconds != nil {
		if *change.ExpireAfterSeconds < 0 || *change.ExpireAfterSeconds > math.MaxInt32 {
			return fmt.Errorf("expireAfterSeconds must be between 0 and %d", math.MaxInt32)
		}
		index = append(index, bson.E{Key: "expireAfterSeconds", Value: *change.ExpireAfterSeconds})
	}
	if len(index) == 1 {
		return fmt.Errorf("nothing to change")
	}
	cmd := bson.D{{Key: "collMod", Value: collection}, {Key: "index", Value: index}}
	return client.Database(dbName).RunCommand(ctx, cmd).Err()
}
