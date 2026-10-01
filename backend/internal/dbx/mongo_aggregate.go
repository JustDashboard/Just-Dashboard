package dbx

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Aggregation: classifying a pipeline, running it, and previewing it a stage
// at a time.
//
// A pipeline is a read until one of its stages writes. Which stages those are
// used to be decided by looking for "$out" and "$merge" in the request text,
// before the text was parsed — so the check and the parser could disagree
// about what a key said ("$out" is $out to one and not the other), and
// $mergeObjects looked like a write to anything matching more loosely. The
// pipeline is parsed first now, and the stages are read off the parsed form.
//
// The classification fails closed. A stage this file does not list is not
// assumed to be a read: a server newer than the list may have taught it to
// write.

// mongoReadStages are the aggregation stages that only read. The value says
// where a stage keeps pipelines of its own, which are classified with it.
var mongoReadStages = map[string]string{
	"$addFields": "", "$bucket": "", "$bucketAuto": "", "$collStats": "", "$count": "",
	"$currentOp": "", "$densify": "", "$documents": "", "$fill": "", "$geoNear": "",
	"$graphLookup": "", "$group": "", "$indexStats": "", "$limit": "",
	"$listLocalSessions": "", "$listSampledQueries": "", "$listSearchIndexes": "",
	"$listSessions": "", "$match": "", "$planCacheStats": "", "$project": "",
	"$querySettings": "", "$queryStats": "", "$redact": "", "$replaceRoot": "",
	"$replaceWith": "", "$sample": "", "$search": "", "$searchMeta": "", "$set": "",
	"$setWindowFields": "", "$shardedDataDistribution": "", "$skip": "", "$sort": "",
	"$sortByCount": "", "$unset": "", "$unwind": "", "$vectorSearch": "",
	"$lookup": "pipeline", "$unionWith": "pipeline", "$facet": "*",
	"$rankFusion": "input.pipelines.*", "$scoreFusion": "input.pipelines.*",
}

// mongoWriteStages are the stages that write a collection.
var mongoWriteStages = map[string]bool{"$out": true, "$merge": true}

// mongoGroupingStages are the stages a preview caps the input of: each reads
// its whole input before it emits anything.
var mongoGroupingStages = map[string]bool{"$group": true, "$bucket": true, "$bucketAuto": true, "$sortByCount": true}

// MongoPipelineInfo is what a pipeline's stages say about it.
type MongoPipelineInfo struct {
	// Stages are the top-level stage operators in order.
	Stages []string `json:"stages"`
	// Writes is true when the pipeline holds a stage that writes a
	// collection; WriteStage names it.
	Writes     bool   `json:"writes"`
	WriteStage string `json:"writeStage,omitempty"`
	// Unknown lists stages this dashboard does not know to be reads. A
	// pipeline holding one is treated as writing.
	Unknown []string `json:"unknown"`
}

// Destructive reports whether running the pipeline needs the destructive
// capability: it writes, or it holds a stage nobody can vouch for.
func (i MongoPipelineInfo) Destructive() bool { return i.Writes || len(i.Unknown) > 0 }

// MongoClassifyPipeline parses a pipeline and reads its stages.
func MongoClassifyPipeline(pipelineText string) (MongoPipelineInfo, error) {
	stages, err := mongoParseArray("pipeline", pipelineText)
	if err != nil {
		return MongoPipelineInfo{}, err
	}
	return mongoClassifyStages(stages), nil
}

// MongoWritesInPipeline reports whether running a pipeline must be treated as
// a write. A pipeline that cannot be parsed is one: the caller is asking
// whether it is safe, and "cannot tell" is not yes.
func MongoWritesInPipeline(pipelineText string) bool {
	info, err := MongoClassifyPipeline(pipelineText)
	return err != nil || info.Destructive()
}

func mongoClassifyStages(stages []bson.Raw) MongoPipelineInfo {
	info := MongoPipelineInfo{Stages: []string{}, Unknown: []string{}}
	unknown := map[string]bool{}
	for _, stage := range stages {
		info.Stages = append(info.Stages, mongoStageName(stage))
		mongoClassifyStage(stage, &info, unknown, 0)
	}
	for name := range unknown {
		info.Unknown = append(info.Unknown, name)
	}
	sort.Strings(info.Unknown)
	return info
}

// mongoStageName is a stage's operator: its one key.
func mongoStageName(stage bson.Raw) string {
	elems, err := stage.Elements()
	if err != nil || len(elems) != 1 {
		return ""
	}
	return elems[0].Key()
}

func mongoClassifyStage(stage bson.Raw, info *MongoPipelineInfo, unknown map[string]bool, depth int) {
	name := mongoStageName(stage)
	if depth > mongoMaxDepth {
		unknown["a pipeline nested too deeply to read"] = true
		return
	}
	if mongoWriteStages[name] {
		info.Writes = true
		if info.WriteStage == "" {
			info.WriteStage = name
		}
		return
	}
	nested, known := mongoReadStages[name]
	if !known {
		if name == "" {
			name = "a stage that is not a single operator"
		}
		unknown[name] = true
		return
	}
	spec := stage.Lookup(name)
	// The stages known to hold pipelines of their own have them classified
	// where they are kept. Everything else in a stage is searched for the
	// two writing operators by key as a second net: neither is a valid key
	// anywhere but at the head of a stage, so finding one elsewhere means
	// this file's idea of the stage is out of date.
	for _, sub := range mongoNestedPipelines(spec, nested) {
		for _, s := range sub {
			mongoClassifyStage(s, info, unknown, depth+1)
		}
	}
	if key := mongoFindWriteKey(spec, 0); key != "" {
		info.Writes = true
		if info.WriteStage == "" {
			info.WriteStage = key
		}
	}
}

// mongoNestedPipelines returns the pipelines a stage keeps at path. "*"
// stands for every field of a document.
func mongoNestedPipelines(spec bson.RawValue, path string) [][]bson.Raw {
	if path == "" {
		return nil
	}
	values := []bson.RawValue{spec}
	for _, part := range strings.Split(path, ".") {
		next := []bson.RawValue{}
		for _, v := range values {
			doc, ok := v.DocumentOK()
			if !ok {
				continue
			}
			if part != "*" {
				if inner, err := doc.LookupErr(part); err == nil {
					next = append(next, inner)
				}
				continue
			}
			if elems, err := doc.Elements(); err == nil {
				for _, e := range elems {
					next = append(next, e.Value())
				}
			}
		}
		values = next
	}
	out := [][]bson.Raw{}
	for _, v := range values {
		arr, ok := v.ArrayOK()
		if !ok {
			continue
		}
		items, err := arr.Values()
		if err != nil {
			continue
		}
		stages := []bson.Raw{}
		for _, item := range items {
			if doc, ok := item.DocumentOK(); ok {
				stages = append(stages, doc)
			}
		}
		out = append(out, stages)
	}
	return out
}

// mongoFindWriteKey looks for a writing stage's name used as a key anywhere
// inside a value.
func mongoFindWriteKey(v bson.RawValue, depth int) string {
	if depth > mongoMaxDepth {
		return "$out"
	}
	var doc bson.Raw
	switch v.Type {
	case bsontype.EmbeddedDocument:
		doc = v.Document()
	case bsontype.Array:
		doc = v.Array()
	default:
		return ""
	}
	elems, err := doc.Elements()
	if err != nil {
		return ""
	}
	for _, e := range elems {
		if v.Type == bsontype.EmbeddedDocument && mongoWriteStages[e.Key()] {
			return e.Key()
		}
		if key := mongoFindWriteKey(e.Value(), depth+1); key != "" {
			return key
		}
	}
	return ""
}

// mongoGuardPipeline refuses a pipeline that names a credential collection in
// a stage that reads another collection, which would otherwise be a way
// around the refusal to open it directly. It reads the pipeline only; what
// the names it finds are views of is mongoGuardPipelineRead's to ask.
func mongoGuardPipeline(dbName string, stages []bson.Raw) error {
	for _, ref := range mongoPipelineCollections(stages, 0) {
		if ref.db == "" {
			ref.db = dbName
		}
		if err := mongoGuardCredentials(ref.db, ref.collection); err != nil {
			return err
		}
	}
	return nil
}

// mongoGuardPipelineRead is mongoGuardPipeline for a pipeline about to be
// run: the collection it starts from and every one it joins are also
// followed through the views they may be.
func mongoGuardPipelineRead(ctx context.Context, client *mongo.Client, dbName, collection string, stages []bson.Raw) error {
	if err := mongoGuardPipeline(dbName, stages); err != nil {
		return err
	}
	names := []string{collection}
	for _, ref := range mongoPipelineCollections(stages, 0) {
		if ref.db == "" || ref.db == dbName {
			names = append(names, ref.collection)
		}
	}
	return mongoGuardRead(ctx, client, dbName, names...)
}

// mongoCollRefTo is a collection a pipeline stage reads besides its own. db
// is empty for one in the database the pipeline runs in.
type mongoCollRefTo struct{ db, collection string }

// mongoPipelineCollections lists the collections a pipeline names: the ones
// it joins, unions with or walks, at any depth, and the one it writes into.
func mongoPipelineCollections(stages []bson.Raw, depth int) []mongoCollRefTo {
	out := []mongoCollRefTo{}
	if depth > mongoMaxDepth {
		return out
	}
	// named reads a collection given as its name or as { db, coll }, which
	// reaches into another database. $lookup takes that form for a short list
	// of the server's own collections, and the replication log is on it.
	named := func(v bson.RawValue) {
		if coll, ok := v.StringValueOK(); ok {
			out = append(out, mongoCollRefTo{collection: coll})
		} else if other, ok := v.DocumentOK(); ok {
			ref := mongoCollRefTo{}
			ref.db, _ = other.Lookup("db").StringValueOK()
			ref.collection, _ = other.Lookup("coll").StringValueOK()
			out = append(out, ref)
		}
	}
	for _, stage := range stages {
		name := mongoStageName(stage)
		spec := stage.Lookup(name)
		switch name {
		case "$lookup", "$graphLookup":
			if doc, ok := spec.DocumentOK(); ok {
				named(doc.Lookup("from"))
			}
		case "$unionWith":
			if doc, ok := spec.DocumentOK(); ok {
				named(doc.Lookup("coll"))
			} else {
				named(spec)
			}
		case "$out":
			named(spec)
		case "$merge":
			if doc, ok := spec.DocumentOK(); ok {
				named(doc.Lookup("into"))
			} else {
				named(spec)
			}
		}
		for _, sub := range mongoNestedPipelines(spec, mongoReadStages[name]) {
			out = append(out, mongoPipelineCollections(sub, depth+1)...)
		}
	}
	return out
}

// MongoAggregateSpec is a pipeline and how to run it.
type MongoAggregateSpec struct {
	Pipeline     string `json:"pipeline"`
	Limit        int64  `json:"limit"`
	MaxTimeMS    int64  `json:"maxTimeMS"`
	AllowDiskUse bool   `json:"allowDiskUse"`
	Collation    string `json:"collation"`
	Hint         string `json:"hint"`
}

// MongoAggregateResult is a pipeline's output.
type MongoAggregateResult struct {
	Documents  []MongoDoc `json:"documents"`
	Returned   int        `json:"returned"`
	HasMore    bool       `json:"hasMore"`
	Truncated  bool       `json:"truncated"`
	DurationMs int64      `json:"durationMs"`
	Statement  string     `json:"statement"`
	// Grid is the same output flattened to columns, which is the shape the
	// first aggregation page drew and still receives.
	Grid *QueryResult `json:"-"`
}

// MongoRunPipeline runs a pipeline and returns up to Limit of what it emits.
func MongoRunPipeline(ctx context.Context, client *mongo.Client, dbName, collection string, spec MongoAggregateSpec) (*MongoAggregateResult, error) {
	if err := mongoNamespace(dbName, collection); err != nil {
		return nil, err
	}
	stages, err := mongoParseArray("pipeline", spec.Pipeline)
	if err != nil {
		return nil, err
	}
	if err := mongoGuardPipelineRead(ctx, client, dbName, collection, stages); err != nil {
		return nil, err
	}
	limit := mongoClampLimit(spec.Limit)
	statement := mongoCollRef(collection) + ".aggregate(" + mongoRelaxedArray(stages) + ")"
	run := stages
	// The server is told where the page ends, so it can stop there instead
	// of producing rows that would be thrown away. A pipeline that writes
	// has to end in the stage that writes, and returns nothing anyway.
	if !mongoClassifyStages(stages).Destructive() {
		run = append(append([]bson.Raw{}, stages...), mongoLimitStage(limit+1))
	}
	res, err := mongoAggregate(ctx, client.Database(dbName).Collection(collection), run, spec, limit)
	if err != nil {
		return nil, err
	}
	res.Statement = statement
	res.Grid.Statement = statement
	return res, nil
}

func mongoLimitStage(n int64) bson.Raw {
	raw, _ := bson.Marshal(bson.D{{Key: "$limit", Value: n}})
	return raw
}

func mongoRelaxedArray(stages []bson.Raw) string {
	parts := make([]string, 0, len(stages))
	for _, s := range stages {
		parts = append(parts, mongoRelaxedJSON(s))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func mongoAggregate(ctx context.Context, coll *mongo.Collection, stages []bson.Raw, spec MongoAggregateSpec, limit int64) (*MongoAggregateResult, error) {
	_, collation, err := mongoCollation(spec.Collation)
	if err != nil {
		return nil, err
	}
	hint, err := mongoHint(spec.Hint)
	if err != nil {
		return nil, err
	}
	opts := options.Aggregate().SetMaxTime(mongoMaxTime(spec.MaxTimeMS)).SetAllowDiskUse(spec.AllowDiskUse)
	if collation != nil {
		opts.SetCollation(collation)
	}
	if hint != nil {
		opts.SetHint(hint)
	}
	start := time.Now()
	cur, err := coll.Aggregate(ctx, mongoArray(stages), opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(context.Background())
	page := &MongoFindResult{Documents: []MongoDoc{}}
	raws := []bson.Raw{}
	size := 0
	for cur.Next(ctx) {
		if int64(len(page.Documents)) >= limit {
			page.HasMore = true
			break
		}
		if len(page.Documents) > 0 && size+len(cur.Current) > mongoPageBytes {
			page.HasMore, page.Truncated = true, true
			break
		}
		doc, err := newMongoDoc(cur.Current)
		if err != nil {
			return nil, err
		}
		size += len(cur.Current)
		page.Documents = append(page.Documents, doc)
		raws = append(raws, append(bson.Raw(nil), cur.Current...))
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	res := &MongoAggregateResult{
		Documents: page.Documents, Returned: len(page.Documents),
		HasMore: page.HasMore, Truncated: page.Truncated,
		DurationMs: time.Since(start).Milliseconds(),
	}
	res.Grid = mongoGrid(raws, page.HasMore)
	res.Grid.Duration = time.Since(start).Round(time.Microsecond).String()
	return res, nil
}

// MongoPreviewSpec asks for the output of a pipeline's first stages.
type MongoPreviewSpec struct {
	Pipeline string `json:"pipeline"`
	// Stage is the index of the last stage to run. -1 runs none and shows
	// the documents the pipeline starts from.
	Stage int `json:"stage"`
	// Sample is how many documents of the stage's output to return.
	Sample int64 `json:"sample"`
	// InputLimit caps how many documents reach the first grouping stage, so
	// a preview of $group does not read the whole collection. Absent, it is
	// 100000; zero turns it off.
	InputLimit *int64 `json:"inputLimit"`
	MaxTimeMS  int64  `json:"maxTimeMS"`
	Collation  string `json:"collation"`
}

// MongoPreviewResult is what a pipeline has produced by one of its stages.
type MongoPreviewResult struct {
	Documents []MongoDoc `json:"documents"`
	Returned  int        `json:"returned"`
	Stage     int        `json:"stage"`
	// Stages is how many stages the pipeline has in all.
	Stages int `json:"stages"`
	// InputLimited says the result came from a capped input and may differ
	// from a full run.
	InputLimited bool  `json:"inputLimited"`
	InputLimit   int64 `json:"inputLimit"`
	// WriteStage is set when the stage asked for is one that writes. It was
	// not run: the documents are what it would have been handed.
	WriteStage string `json:"writeStage,omitempty"`
	DurationMs int64  `json:"durationMs"`
}

const (
	mongoPreviewSample     = 10
	mongoPreviewMaxSample  = 100
	mongoPreviewInputLimit = 100_000
	mongoPreviewMaxTime    = 10 * time.Second
	mongoPreviewMaxMaxTime = 60 * time.Second
)

// MongoPreviewPipeline runs a pipeline as far as one stage and returns a few
// documents of that stage's output. It never writes: a stage that would is
// left out, and a stage it cannot classify is refused.
func MongoPreviewPipeline(ctx context.Context, client *mongo.Client, dbName, collection string, spec MongoPreviewSpec) (*MongoPreviewResult, error) {
	if err := mongoNamespace(dbName, collection); err != nil {
		return nil, err
	}
	stages, err := mongoParseArray("pipeline", spec.Pipeline)
	if err != nil {
		return nil, err
	}
	if err := mongoGuardPipelineRead(ctx, client, dbName, collection, stages); err != nil {
		return nil, err
	}
	if spec.Stage < -1 || spec.Stage >= len(stages) {
		return nil, fmt.Errorf("the pipeline has %d stages; there is no stage %d", len(stages), spec.Stage)
	}
	res := &MongoPreviewResult{Stage: spec.Stage, Stages: len(stages)}
	run := []bson.Raw{}
	for i := 0; i <= spec.Stage; i++ {
		info := mongoClassifyStages(stages[i : i+1])
		if len(info.Unknown) > 0 {
			return nil, fmt.Errorf("stage %d (%s) is not one a preview can run; run the whole pipeline to use it", i, info.Unknown[0])
		}
		if info.Writes {
			res.WriteStage = info.WriteStage
			break
		}
		run = append(run, stages[i])
	}
	inputLimit := int64(mongoPreviewInputLimit)
	if spec.InputLimit != nil {
		inputLimit = *spec.InputLimit
	}
	if inputLimit > 0 {
		for i, stage := range run {
			if mongoGroupingStages[mongoStageName(stage)] {
				capped := append([]bson.Raw{}, run[:i]...)
				capped = append(capped, mongoLimitStage(inputLimit))
				run = append(capped, run[i:]...)
				res.InputLimited, res.InputLimit = true, inputLimit
				break
			}
		}
	}
	sample := spec.Sample
	if sample <= 0 {
		sample = mongoPreviewSample
	}
	if sample > mongoPreviewMaxSample {
		sample = mongoPreviewMaxSample
	}
	run = append(run, mongoLimitStage(sample))
	maxTime := mongoPreviewMaxTime
	if spec.MaxTimeMS > 0 {
		maxTime = time.Duration(spec.MaxTimeMS) * time.Millisecond
		if maxTime > mongoPreviewMaxMaxTime {
			maxTime = mongoPreviewMaxMaxTime
		}
	}
	out, err := mongoAggregate(ctx, client.Database(dbName).Collection(collection), run, MongoAggregateSpec{
		MaxTimeMS: maxTime.Milliseconds(), AllowDiskUse: true, Collation: spec.Collation,
	}, sample)
	if err != nil {
		return nil, err
	}
	res.Documents, res.Returned, res.DurationMs = out.Documents, out.Returned, out.DurationMs
	return res, nil
}

// mongoGrid flattens documents into the column-and-row shape a SQL result
// has. The column set is the union of the keys seen, _id first.
func mongoGrid(docs []bson.Raw, truncated bool) *QueryResult {
	columns := []string{}
	seen := map[string]bool{}
	flat := make([]map[string]any, 0, len(docs))
	for _, raw := range docs {
		var doc bson.M
		if err := bson.Unmarshal(raw, &doc); err != nil {
			continue
		}
		row := map[string]any{}
		for k, v := range doc {
			row[k] = normaliseBSON(v)
			if !seen[k] {
				seen[k] = true
				columns = append(columns, k)
			}
		}
		flat = append(flat, row)
	}
	sortMongoColumns(columns)
	res := &QueryResult{Columns: columns, Types: []string{}, Rows: [][]any{}}
	for _, doc := range flat {
		row := make([]any, len(columns))
		for i, c := range columns {
			row[i] = doc[c]
		}
		res.Rows = append(res.Rows, row)
	}
	res.RowCount = len(res.Rows)
	res.Truncated = truncated
	return res
}
