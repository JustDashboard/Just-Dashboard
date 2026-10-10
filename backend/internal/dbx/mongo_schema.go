package dbx

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Schema analysis: what a collection's documents actually look like.
//
// A collection has no declared schema, so the only way to say what is in one
// is to look. This reads a random sample and reports, for every field path it
// met, how many documents carry it, which types it holds and in what
// proportion, and a few figures per type. It is a description of the sample:
// a field one document in a million has will usually not be in it, and the
// result says how many documents it is drawn from so nobody mistakes it for a
// census.
//
// Paths are dotted. An array contributes one path of its own, typed "array",
// and a second one ending in [] for what its elements are; documents inside
// an array continue from there (items[].sku).

const (
	mongoSchemaSample    = 1000
	mongoSchemaMaxSample = 10000
	// mongoSchemaFields bounds how many distinct paths are reported. A
	// collection whose keys are data (one field per user id) has no schema
	// to speak of, and would otherwise answer with all of them.
	mongoSchemaFields = 2000
	mongoSchemaDepth  = 16
	// mongoSchemaArrayItems is how many elements of one array are looked at.
	// Its length is always counted in full.
	mongoSchemaArrayItems = 100
	// mongoSchemaValues is how many distinct values of one field are counted
	// before the count stops being exact.
	mongoSchemaValues   = 1000
	mongoSchemaTop      = 10
	mongoSchemaValueCut = 160
	// mongoSchemaBytes stops a sample of very large documents early.
	mongoSchemaBytes = 256 << 20
)

// MongoSchemaSpec asks for a collection's schema by sample.
type MongoSchemaSpec struct {
	Sample    int64  `json:"sample"`
	Filter    string `json:"filter"`
	MaxTimeMS int64  `json:"maxTimeMS"`
}

// MongoSchemaValue is one value and how often it occurred.
type MongoSchemaValue struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// MongoSchemaType is one of the types a field holds.
type MongoSchemaType struct {
	// Type is the BSON type's name as $type spells it: string, int, long,
	// double, decimal, bool, date, objectId, object, array, null, binData…
	Type  string `json:"type"`
	Count int    `json:"count"`
	// Share is this type's part of the field's occurrences, 0 to 1.
	Share float64 `json:"share"`

	// Min and Max are the smallest and largest value: of a number, of a
	// date (RFC 3339), or of an ObjectId's creation time.
	Min string `json:"min,omitempty"`
	Max string `json:"max,omitempty"`
	// Avg is the mean of a number, or of a date as milliseconds since 1970.
	Avg *float64 `json:"avg,omitempty"`

	// The three lengths describe a string (in bytes) or an array (in
	// elements).
	MinLength *int     `json:"minLength,omitempty"`
	MaxLength *int     `json:"maxLength,omitempty"`
	AvgLength *float64 `json:"avgLength,omitempty"`

	// Top are the most frequent values, for strings, booleans and whole
	// numbers. Distinct is how many different values were seen; when
	// DistinctCapped is set there were more than were counted, and Top is
	// drawn from the ones that were.
	Top            []MongoSchemaValue `json:"top,omitempty"`
	Distinct       int                `json:"distinct,omitempty"`
	DistinctCapped bool               `json:"distinctCapped,omitempty"`
}

// MongoSchemaField is one field path.
type MongoSchemaField struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Depth int    `json:"depth"`
	// Documents is how many sampled documents hold the path at least once,
	// and Presence that number over the sample, 0 to 1.
	Documents int     `json:"documents"`
	Presence  float64 `json:"presence"`
	// Occurrences is how many values were seen at the path, which exceeds
	// Documents under an array.
	Occurrences int               `json:"occurrences"`
	Types       []MongoSchemaType `json:"types"`
	// Indexed says an index covers the field; Indexes names them.
	Indexed bool     `json:"indexed"`
	Indexes []string `json:"indexes"`
}

// MongoSchema is the analysis of one sample.
type MongoSchema struct {
	Database   string `json:"database"`
	Collection string `json:"collection"`
	// Sampled is how many documents were analysed; Requested how many were
	// asked for. Total is the collection's estimated size, -1 when unknown.
	Sampled   int   `json:"sampled"`
	Requested int64 `json:"requested"`
	Total     int64 `json:"total"`
	// Truncated says the sample held more distinct paths, or deeper ones,
	// than are reported.
	Truncated  bool               `json:"truncated"`
	Fields     []MongoSchemaField `json:"fields"`
	DurationMs int64              `json:"durationMs"`
}

// MongoAnalyseSchema samples a collection and describes its fields.
func MongoAnalyseSchema(ctx context.Context, client *mongo.Client, dbName, collection string, spec MongoSchemaSpec) (*MongoSchema, error) {
	if err := mongoReadable(ctx, client, dbName, collection); err != nil {
		return nil, err
	}
	filter, err := mongoParseOptionalDocument("filter", spec.Filter)
	if err != nil {
		return nil, err
	}
	size := spec.Sample
	if size <= 0 {
		size = mongoSchemaSample
	}
	if size > mongoSchemaMaxSample {
		size = mongoSchemaMaxSample
	}
	pipeline := bson.A{}
	if !mongoIsEmpty(filter) {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: filter}})
	}
	pipeline = append(pipeline, bson.D{{Key: "$sample", Value: bson.D{{Key: "size", Value: size}}}})

	coll := client.Database(dbName).Collection(collection)
	start := time.Now()
	cur, err := coll.Aggregate(ctx, pipeline,
		options.Aggregate().SetMaxTime(mongoMaxTime(spec.MaxTimeMS)).SetAllowDiskUse(true))
	if err != nil {
		return nil, err
	}
	defer cur.Close(context.Background())

	a := newMongoSchemaAnalysis()
	read := 0
	for cur.Next(ctx) {
		a.document(cur.Current)
		read += len(cur.Current)
		if read > mongoSchemaBytes {
			break
		}
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}

	out := &MongoSchema{
		Database: dbName, Collection: collection,
		Sampled: a.docs, Requested: size, Total: -1, Truncated: a.truncated,
	}
	if n, err := coll.EstimatedDocumentCount(ctx, options.EstimatedDocumentCount().SetMaxTime(mongoCountTime)); err == nil {
		out.Total = n
	}
	out.Fields = a.render()
	// Which fields an index covers. A view has no indexes to list, and that
	// is not a reason to fail the analysis.
	if indexes, err := mongoIndexDefinitions(ctx, coll); err == nil {
		mongoMarkIndexed(out.Fields, indexes)
	}
	out.DurationMs = time.Since(start).Milliseconds()
	return out, nil
}

// mongoMarkIndexed sets Indexed on every field an index's key names. A
// path's array markers are dropped for the comparison: an index on
// "items.sku" covers what the analysis calls items[].sku.
func mongoMarkIndexed(fields []MongoSchemaField, indexes []MongoIndex) {
	for i := range fields {
		f := &fields[i]
		f.Indexes = []string{}
		plain := strings.ReplaceAll(f.Path, "[]", "")
		for _, ix := range indexes {
			for _, k := range ix.Keys {
				covers := k.Field == plain || k.Field == "$**" ||
					(strings.HasSuffix(k.Field, ".$**") && strings.HasPrefix(plain, strings.TrimSuffix(k.Field, "$**")))
				if covers {
					f.Indexes = append(f.Indexes, ix.Name)
					break
				}
			}
		}
		f.Indexed = len(f.Indexes) > 0
	}
}

type mongoSchemaAnalysis struct {
	docs      int
	fields    map[string]*mongoFieldStats
	truncated bool
}

type mongoFieldStats struct {
	path        string
	depth       int
	occurrences int
	documents   int
	// lastDoc is the last document the path was counted for, so a path that
	// repeats inside one document's array is one document.
	lastDoc int
	types   map[string]*mongoTypeStats
}

type mongoTypeStats struct {
	count int

	numbers                int
	numMin, numMax, numSum float64
	minText, maxText       string

	lengths        int
	lenMin, lenMax int
	lenSum         int64

	values map[string]int
	capped bool
}

func newMongoSchemaAnalysis() *mongoSchemaAnalysis {
	return &mongoSchemaAnalysis{fields: map[string]*mongoFieldStats{}}
}

func (a *mongoSchemaAnalysis) document(doc bson.Raw) {
	a.docs++
	a.walk(doc, "", 0)
}

func (a *mongoSchemaAnalysis) walk(doc bson.Raw, prefix string, depth int) {
	elems, err := doc.Elements()
	if err != nil {
		return
	}
	for _, e := range elems {
		a.value(prefix+e.Key(), e.Value(), depth)
	}
}

func (a *mongoSchemaAnalysis) value(path string, v bson.RawValue, depth int) {
	f := a.fields[path]
	if f == nil {
		if len(a.fields) >= mongoSchemaFields {
			a.truncated = true
			return
		}
		f = &mongoFieldStats{path: path, depth: depth, types: map[string]*mongoTypeStats{}}
		a.fields[path] = f
	}
	f.occurrences++
	if f.lastDoc != a.docs {
		f.lastDoc = a.docs
		f.documents++
	}
	name := mongoTypeName(v.Type)
	t := f.types[name]
	if t == nil {
		t = &mongoTypeStats{}
		f.types[name] = t
	}
	t.count++
	t.observe(v)

	switch v.Type {
	case bsontype.EmbeddedDocument:
		if depth >= mongoSchemaDepth {
			a.truncated = true
			return
		}
		a.walk(v.Document(), path+".", depth+1)
	case bsontype.Array:
		if depth >= mongoSchemaDepth {
			a.truncated = true
			return
		}
		items, err := v.Array().Values()
		if err != nil {
			return
		}
		for i, item := range items {
			if i >= mongoSchemaArrayItems {
				break
			}
			a.value(path+"[]", item, depth+1)
		}
	}
}

// mongoTypeName is the alias the $type operator uses, so what the analysis
// calls a type can be pasted into a filter.
func mongoTypeName(t bsontype.Type) string {
	switch t {
	case bsontype.Double:
		return "double"
	case bsontype.String:
		return "string"
	case bsontype.EmbeddedDocument:
		return "object"
	case bsontype.Array:
		return "array"
	case bsontype.Binary:
		return "binData"
	case bsontype.Undefined:
		return "undefined"
	case bsontype.ObjectID:
		return "objectId"
	case bsontype.Boolean:
		return "bool"
	case bsontype.DateTime:
		return "date"
	case bsontype.Null:
		return "null"
	case bsontype.Regex:
		return "regex"
	case bsontype.DBPointer:
		return "dbPointer"
	case bsontype.JavaScript:
		return "javascript"
	case bsontype.Symbol:
		return "symbol"
	case bsontype.CodeWithScope:
		return "javascriptWithScope"
	case bsontype.Int32:
		return "int"
	case bsontype.Timestamp:
		return "timestamp"
	case bsontype.Int64:
		return "long"
	case bsontype.Decimal128:
		return "decimal"
	case bsontype.MinKey:
		return "minKey"
	case bsontype.MaxKey:
		return "maxKey"
	}
	return t.String()
}

func (t *mongoTypeStats) observe(v bson.RawValue) {
	switch v.Type {
	case bsontype.Int32:
		n := int64(v.Int32())
		t.number(float64(n), strconv.FormatInt(n, 10))
		t.tally(strconv.FormatInt(n, 10))
	case bsontype.Int64:
		n := v.Int64()
		t.number(float64(n), strconv.FormatInt(n, 10))
		t.tally(strconv.FormatInt(n, 10))
	case bsontype.Double:
		f := v.Double()
		// NaN compares with nothing and would poison the mean.
		if !math.IsNaN(f) && !math.IsInf(f, 0) {
			t.number(f, strconv.FormatFloat(f, 'g', -1, 64))
		}
	case bsontype.Decimal128:
		text := v.Decimal128().String()
		if f, err := strconv.ParseFloat(text, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			t.number(f, text)
		}
	case bsontype.DateTime:
		ms := v.DateTime()
		t.number(float64(ms), time.UnixMilli(ms).UTC().Format(time.RFC3339Nano))
	case bsontype.ObjectID:
		created := v.ObjectID().Timestamp().UTC()
		// The mean of creation times says nothing, so only the range is
		// kept: numbers stays at zero and no average is reported.
		t.span(float64(created.Unix()), created.Format(time.RFC3339))
	case bsontype.String:
		s := v.StringValue()
		t.length(len(s))
		if cut, shortened := mongoCut(s, mongoSchemaValueCut); shortened {
			t.tally(cut)
		} else {
			t.tally(s)
		}
	case bsontype.Boolean:
		t.tally(strconv.FormatBool(v.Boolean()))
	case bsontype.Array:
		n := 0
		if items, err := v.Array().Values(); err == nil {
			n = len(items)
		}
		t.length(n)
	}
}

func (t *mongoTypeStats) number(f float64, text string) {
	t.span(f, text)
	t.numbers++
	t.numSum += f
}

// span widens the range to include f. minText and maxText are the values as
// they should be shown, which for a 64-bit integer is more exact than the
// float they were compared as.
func (t *mongoTypeStats) span(f float64, text string) {
	if t.minText == "" || f < t.numMin {
		t.numMin, t.minText = f, text
	}
	if t.maxText == "" || f > t.numMax {
		t.numMax, t.maxText = f, text
	}
}

func (t *mongoTypeStats) length(n int) {
	if t.lengths == 0 || n < t.lenMin {
		t.lenMin = n
	}
	if t.lengths == 0 || n > t.lenMax {
		t.lenMax = n
	}
	t.lengths++
	t.lenSum += int64(n)
}

func (t *mongoTypeStats) tally(value string) {
	if t.values == nil {
		t.values = map[string]int{}
	}
	if _, seen := t.values[value]; !seen && len(t.values) >= mongoSchemaValues {
		t.capped = true
		return
	}
	t.values[value]++
}

// render lists the analysis: paths in order, _id first, each type by how
// common it is.
func (a *mongoSchemaAnalysis) render() []MongoSchemaField {
	out := make([]MongoSchemaField, 0, len(a.fields))
	for _, f := range a.fields {
		field := MongoSchemaField{
			Path: f.path, Name: mongoSchemaName(f.path), Depth: f.depth,
			Documents: f.documents, Occurrences: f.occurrences,
			Types: []MongoSchemaType{}, Indexes: []string{},
		}
		if a.docs > 0 {
			field.Presence = float64(f.documents) / float64(a.docs)
		}
		for name, t := range f.types {
			field.Types = append(field.Types, t.render(name, f.occurrences))
		}
		sort.Slice(field.Types, func(i, j int) bool {
			if field.Types[i].Count != field.Types[j].Count {
				return field.Types[i].Count > field.Types[j].Count
			}
			return field.Types[i].Type < field.Types[j].Type
		})
		out = append(out, field)
	}
	sort.Slice(out, func(i, j int) bool {
		// _id and everything under it first, then by path, which keeps a
		// field's children directly after it.
		ai, aj := mongoSchemaUnderID(out[i].Path), mongoSchemaUnderID(out[j].Path)
		if ai != aj {
			return ai
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func mongoSchemaUnderID(path string) bool {
	return path == "_id" || strings.HasPrefix(path, "_id.") || strings.HasPrefix(path, "_id[")
}

// mongoSchemaName is the last segment of a path: the field's own name, or []
// for an array's elements.
func mongoSchemaName(path string) string {
	if strings.HasSuffix(path, "[]") {
		return "[]"
	}
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[i+1:]
	}
	return path
}

func (t *mongoTypeStats) render(name string, occurrences int) MongoSchemaType {
	out := MongoSchemaType{Type: name, Count: t.count, Min: t.minText, Max: t.maxText}
	if occurrences > 0 {
		out.Share = float64(t.count) / float64(occurrences)
	}
	if t.numbers > 0 {
		avg := t.numSum / float64(t.numbers)
		out.Avg = &avg
	}
	if t.lengths > 0 {
		lo, hi := t.lenMin, t.lenMax
		avg := float64(t.lenSum) / float64(t.lengths)
		out.MinLength, out.MaxLength, out.AvgLength = &lo, &hi, &avg
	}
	if len(t.values) > 0 {
		out.Distinct, out.DistinctCapped = len(t.values), t.capped
		for value, count := range t.values {
			out.Top = append(out.Top, MongoSchemaValue{Value: value, Count: count})
		}
		sort.Slice(out.Top, func(i, j int) bool {
			if out.Top[i].Count != out.Top[j].Count {
				return out.Top[i].Count > out.Top[j].Count
			}
			return out.Top[i].Value < out.Top[j].Value
		})
		if len(out.Top) > mongoSchemaTop {
			out.Top = out.Top[:mongoSchemaTop]
		}
	}
	return out
}
