package dbx

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// Collections: what a database holds, what each one costs, and the options a
// collection is made with.
//
// A collection, a view and a time-series collection all answer to "find", and
// differ in everything else: a view stores nothing and cannot be written, a
// time-series collection is a view over buckets it will not let you index
// freely. The type therefore travels with every entry, so the page can leave
// out the controls a kind does not have instead of offering them and failing.

// MongoTimeSeries is the definition of a time-series collection.
type MongoTimeSeries struct {
	TimeField string `json:"timeField"`
	MetaField string `json:"metaField,omitempty"`
	// Granularity is seconds, minutes or hours. The two bucket fields are the
	// newer, exact way to say the same thing and replace it when set.
	Granularity           string `json:"granularity,omitempty"`
	BucketMaxSpanSeconds  int64  `json:"bucketMaxSpanSeconds,omitempty"`
	BucketRoundingSeconds int64  `json:"bucketRoundingSeconds,omitempty"`
}

// MongoCollection is one entry of a database: a collection, a view or a
// time-series collection, with its options and its figures.
type MongoCollection struct {
	Name string `json:"name"`
	// Type is collection, view or timeseries.
	Type string `json:"type"`
	// System marks the collections the server keeps for itself (system.views,
	// system.profile, the buckets behind a time-series collection).
	System   bool `json:"system"`
	ReadOnly bool `json:"readOnly"`

	// StatsKnown is false when the figures below could not be read: a view
	// has none, and a database with very many collections is not asked for
	// all of them.
	StatsKnown  bool  `json:"statsKnown"`
	Count       int64 `json:"count"`
	Size        int64 `json:"size"`
	AvgObjSize  int64 `json:"avgObjSize"`
	StorageSize int64 `json:"storageSize"`
	IndexCount  int64 `json:"indexCount"`
	IndexSize   int64 `json:"indexSize"`

	Capped     bool  `json:"capped"`
	CappedSize int64 `json:"cappedSize,omitempty"`
	CappedMax  int64 `json:"cappedMax,omitempty"`
	Clustered  bool  `json:"clustered"`

	// ViewOn and Pipeline define a view. Pipeline is relaxed Extended JSON.
	ViewOn   string `json:"viewOn,omitempty"`
	Pipeline string `json:"pipeline,omitempty"`

	TimeSeries         *MongoTimeSeries `json:"timeseries,omitempty"`
	ExpireAfterSeconds *int64           `json:"expireAfterSeconds,omitempty"`

	// Validator is relaxed Extended JSON, empty when there is none.
	Validator        string `json:"validator,omitempty"`
	ValidationLevel  string `json:"validationLevel,omitempty"`
	ValidationAction string `json:"validationAction,omitempty"`
	Collation        string `json:"collation,omitempty"`
}

// MongoCollectionList is a database's collections.
type MongoCollectionList struct {
	Database    string            `json:"database"`
	Collections []MongoCollection `json:"collections"`
	// StatsTruncated says some entries carry no figures because there were
	// too many collections to ask about each.
	StatsTruncated bool `json:"statsTruncated"`
}

const (
	// mongoStatsCollections bounds how many collections are asked for their
	// figures. Each is a command of its own; a database with thousands of
	// collections lists them all and measures the first of them.
	mongoStatsCollections = 400
	mongoStatsWorkers     = 8
)

// MongoListCollections lists a database's collections with their options and
// figures, sorted by name.
func MongoListCollections(ctx context.Context, client *mongo.Client, dbName string) (*MongoCollectionList, error) {
	if err := mongoDatabaseName(dbName); err != nil {
		return nil, err
	}
	cols, err := mongoCollectionEntries(ctx, client.Database(dbName), bson.D{})
	if err != nil {
		return nil, err
	}
	sort.Slice(cols, func(i, j int) bool { return cols[i].Name < cols[j].Name })
	out := &MongoCollectionList{Database: dbName, Collections: cols}

	// The figures come one command per collection, so they are asked for a
	// few at a time rather than in a row.
	measurable := []int{}
	for i := range cols {
		if cols[i].Type == "view" {
			continue
		}
		if len(measurable) >= mongoStatsCollections {
			out.StatsTruncated = true
			break
		}
		measurable = append(measurable, i)
	}
	db := client.Database(dbName)
	work := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < mongoStatsWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				mongoFillStats(ctx, db, &cols[i])
			}
		}()
	}
	for _, i := range measurable {
		work <- i
	}
	close(work)
	wg.Wait()
	return out, nil
}

// MongoCollectionInfo describes one collection.
func MongoCollectionInfo(ctx context.Context, client *mongo.Client, dbName, collection string) (*MongoCollection, error) {
	if err := mongoNamespace(dbName, collection); err != nil {
		return nil, err
	}
	db := client.Database(dbName)
	cols, err := mongoCollectionEntries(ctx, db, bson.D{{Key: "name", Value: collection}})
	if err != nil {
		return nil, err
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("%w: %q in %s", ErrMongoNoCollection, collection, dbName)
	}
	if cols[0].Type != "view" {
		mongoFillStats(ctx, db, &cols[0])
	}
	return &cols[0], nil
}

func mongoCollectionEntries(ctx context.Context, db *mongo.Database, filter bson.D) ([]MongoCollection, error) {
	cur, err := db.ListCollections(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cur.Close(context.Background())
	out := []MongoCollection{}
	for cur.Next(ctx) {
		var entry struct {
			Name    string   `bson:"name"`
			Type    string   `bson:"type"`
			Options bson.Raw `bson:"options"`
			Info    struct {
				ReadOnly bool `bson:"readOnly"`
			} `bson:"info"`
		}
		if err := cur.Decode(&entry); err != nil {
			return nil, err
		}
		c := MongoCollection{
			Name: entry.Name, Type: entry.Type, ReadOnly: entry.Info.ReadOnly,
			System: strings.HasPrefix(entry.Name, "system."),
		}
		if c.Type == "" {
			c.Type = "collection"
		}
		mongoReadCollectionOptions(&c, entry.Options)
		out = append(out, c)
	}
	return out, cur.Err()
}

func mongoReadCollectionOptions(c *MongoCollection, opts bson.Raw) {
	if len(opts) == 0 {
		return
	}
	if v, err := opts.LookupErr("capped"); err == nil {
		c.Capped, _ = v.BooleanOK()
	}
	c.CappedSize = mongoLookupInt(opts, "size")
	c.CappedMax = mongoLookupInt(opts, "max")
	if _, err := opts.LookupErr("clusteredIndex"); err == nil {
		c.Clustered = true
	}
	if v, err := opts.LookupErr("viewOn"); err == nil {
		c.ViewOn, _ = v.StringValueOK()
	}
	if v, err := opts.LookupErr("pipeline"); err == nil {
		c.Pipeline = mongoValueJSON(v, false)
	}
	if v, err := opts.LookupErr("timeseries"); err == nil {
		if doc, ok := v.DocumentOK(); ok {
			ts := &MongoTimeSeries{
				BucketMaxSpanSeconds:  mongoLookupInt(doc, "bucketMaxSpanSeconds"),
				BucketRoundingSeconds: mongoLookupInt(doc, "bucketRoundingSeconds"),
			}
			ts.TimeField, _ = doc.Lookup("timeField").StringValueOK()
			ts.MetaField, _ = doc.Lookup("metaField").StringValueOK()
			ts.Granularity, _ = doc.Lookup("granularity").StringValueOK()
			c.TimeSeries = ts
		}
	}
	if v, err := opts.LookupErr("expireAfterSeconds"); err == nil {
		if n, ok := v.AsInt64OK(); ok {
			c.ExpireAfterSeconds = &n
		} else if f, ok := v.DoubleOK(); ok {
			n := int64(f)
			c.ExpireAfterSeconds = &n
		}
	}
	if v, err := opts.LookupErr("validator"); err == nil {
		if doc, ok := v.DocumentOK(); ok && !mongoIsEmpty(doc) {
			c.Validator = mongoRelaxedJSON(doc)
		}
	}
	c.ValidationLevel, _ = opts.Lookup("validationLevel").StringValueOK()
	c.ValidationAction, _ = opts.Lookup("validationAction").StringValueOK()
	if v, err := opts.LookupErr("collation"); err == nil {
		c.Collation = mongoValueJSON(v, false)
	}
}

// mongoLookupInt reads a number the server may have stored as any of its
// numeric types. Sizes arrive as int32 on a small collection and as int64 or
// double on a large one.
func mongoLookupInt(doc bson.Raw, key ...string) int64 {
	v, err := doc.LookupErr(key...)
	if err != nil {
		return 0
	}
	return mongoInt(v)
}

func mongoInt(v bson.RawValue) int64 {
	if n, ok := v.AsInt64OK(); ok {
		return n
	}
	if f, ok := v.DoubleOK(); ok {
		return int64(f)
	}
	return 0
}

// mongoCollStats asks for one collection's figures. The command form is
// tried first because every server and every MongoDB-compatible one has it;
// the aggregation stage is what replaces it where it has been removed.
func mongoCollStats(ctx context.Context, db *mongo.Database, collection string) (bson.Raw, error) {
	raw, err := db.RunCommand(ctx, bson.D{{Key: "collStats", Value: collection}}).Raw()
	if err == nil {
		return raw, nil
	}
	var cmdErr mongo.CommandError
	if !(errors.As(err, &cmdErr) && cmdErr.Code == 59) { // CommandNotFound
		return nil, err
	}
	cur, aerr := db.Collection(collection).Aggregate(ctx, bson.A{
		bson.D{{Key: "$collStats", Value: bson.D{{Key: "storageStats", Value: bson.D{}}}}},
	})
	if aerr != nil {
		return nil, err
	}
	defer cur.Close(context.Background())
	if !cur.Next(ctx) {
		return nil, err
	}
	stats, ok := cur.Current.Lookup("storageStats").DocumentOK()
	if !ok {
		return nil, err
	}
	return append(bson.Raw(nil), stats...), nil
}

func mongoFillStats(ctx context.Context, db *mongo.Database, c *MongoCollection) {
	raw, err := mongoCollStats(ctx, db, c.Name)
	if err != nil {
		return
	}
	c.StatsKnown = true
	c.Count = mongoLookupInt(raw, "count")
	c.Size = mongoLookupInt(raw, "size")
	c.AvgObjSize = mongoLookupInt(raw, "avgObjSize")
	c.StorageSize = mongoLookupInt(raw, "storageSize")
	c.IndexCount = mongoLookupInt(raw, "nindexes")
	c.IndexSize = mongoLookupInt(raw, "totalIndexSize")
	if c.Capped {
		if n := mongoLookupInt(raw, "maxSize"); n > 0 {
			c.CappedSize = n
		}
		if n := mongoLookupInt(raw, "max"); n > 0 {
			c.CappedMax = n
		}
	}
}

// MongoCollectionSpec is what a collection is created with. Leaving every
// field out makes a plain collection.
type MongoCollectionSpec struct {
	Capped bool `json:"capped"`
	// Size is a capped collection's limit in bytes, Max its limit in
	// documents.
	Size int64 `json:"size"`
	Max  int64 `json:"max"`

	TimeSeries         *MongoTimeSeries `json:"timeseries"`
	ExpireAfterSeconds *int64           `json:"expireAfterSeconds"`
	ClusteredIndex     bool             `json:"clusteredIndex"`

	Validator        string `json:"validator"`
	ValidationLevel  string `json:"validationLevel"`
	ValidationAction string `json:"validationAction"`

	// ViewOn and Pipeline make a view instead of a collection.
	ViewOn   string `json:"viewOn"`
	Pipeline string `json:"pipeline"`

	Collation string `json:"collation"`
}

var (
	mongoValidationLevels  = map[string]bool{"off": true, "strict": true, "moderate": true}
	mongoValidationActions = map[string]bool{"error": true, "warn": true, "errorAndLog": true}
	mongoGranularities     = map[string]bool{"seconds": true, "minutes": true, "hours": true}
)

// command builds the create command. The checks here are the ones whose
// refusal the server words badly; everything else is left to it.
func (s MongoCollectionSpec) command(collection string) (bson.D, error) {
	cmd := bson.D{{Key: "create", Value: collection}}
	view := strings.TrimSpace(s.ViewOn) != ""
	if view {
		if s.Capped || s.TimeSeries != nil || s.ClusteredIndex || strings.TrimSpace(s.Validator) != "" {
			return nil, fmt.Errorf("a view stores nothing, so it cannot be capped, clustered, time-series or validated")
		}
		if err := mongoCollectionName(s.ViewOn); err != nil {
			return nil, err
		}
		stages := []bson.Raw{}
		if strings.TrimSpace(s.Pipeline) != "" {
			var err error
			if stages, err = mongoParseArray("pipeline", s.Pipeline); err != nil {
				return nil, err
			}
		}
		if info := mongoClassifyStages(stages); info.Writes {
			return nil, fmt.Errorf("a view's pipeline cannot hold %s; a view only reads", info.WriteStage)
		}
		cmd = append(cmd, bson.E{Key: "viewOn", Value: s.ViewOn}, bson.E{Key: "pipeline", Value: mongoArray(stages)})
	} else if strings.TrimSpace(s.Pipeline) != "" {
		return nil, fmt.Errorf("a pipeline defines a view; name the collection it reads in viewOn")
	}
	if s.Capped {
		if s.Size <= 0 {
			return nil, fmt.Errorf("a capped collection needs a size in bytes")
		}
		cmd = append(cmd, bson.E{Key: "capped", Value: true}, bson.E{Key: "size", Value: s.Size})
		if s.Max > 0 {
			cmd = append(cmd, bson.E{Key: "max", Value: s.Max})
		}
	} else if s.Size > 0 || s.Max > 0 {
		return nil, fmt.Errorf("size and max apply to a capped collection only")
	}
	if ts := s.TimeSeries; ts != nil {
		if s.Capped {
			return nil, fmt.Errorf("a time-series collection cannot be capped")
		}
		if strings.TrimSpace(ts.TimeField) == "" {
			return nil, fmt.Errorf("a time-series collection needs the name of its time field")
		}
		doc := bson.D{{Key: "timeField", Value: ts.TimeField}}
		if ts.MetaField != "" {
			doc = append(doc, bson.E{Key: "metaField", Value: ts.MetaField})
		}
		switch {
		case ts.BucketMaxSpanSeconds > 0 || ts.BucketRoundingSeconds > 0:
			if ts.Granularity != "" {
				return nil, fmt.Errorf("set a granularity or the bucket span, not both")
			}
			doc = append(doc,
				bson.E{Key: "bucketMaxSpanSeconds", Value: ts.BucketMaxSpanSeconds},
				bson.E{Key: "bucketRoundingSeconds", Value: ts.BucketRoundingSeconds})
		case ts.Granularity != "":
			if !mongoGranularities[ts.Granularity] {
				return nil, fmt.Errorf("granularity is seconds, minutes or hours")
			}
			doc = append(doc, bson.E{Key: "granularity", Value: ts.Granularity})
		}
		cmd = append(cmd, bson.E{Key: "timeseries", Value: doc})
	}
	if s.ClusteredIndex {
		cmd = append(cmd, bson.E{Key: "clusteredIndex", Value: bson.D{
			{Key: "key", Value: bson.D{{Key: "_id", Value: 1}}}, {Key: "unique", Value: true},
		}})
	}
	if s.ExpireAfterSeconds != nil {
		if s.TimeSeries == nil && !s.ClusteredIndex {
			return nil, fmt.Errorf("expireAfterSeconds applies to a time-series or clustered collection; elsewhere expiry is a TTL index")
		}
		if *s.ExpireAfterSeconds < 0 {
			return nil, fmt.Errorf("expireAfterSeconds cannot be negative")
		}
		cmd = append(cmd, bson.E{Key: "expireAfterSeconds", Value: *s.ExpireAfterSeconds})
	}
	rules, err := mongoValidationFields(s.Validator, s.ValidationLevel, s.ValidationAction)
	if err != nil {
		return nil, err
	}
	cmd = append(cmd, rules...)
	collation, _, err := mongoCollation(s.Collation)
	if err != nil {
		return nil, err
	}
	if collation != nil {
		cmd = append(cmd, bson.E{Key: "collation", Value: collation})
	}
	return cmd, nil
}

// mongoValidationFields reads the three validation settings as the fields
// create and collMod both take. An empty validator is left out.
func mongoValidationFields(validator, level, action string) (bson.D, error) {
	out := bson.D{}
	if strings.TrimSpace(validator) != "" {
		doc, err := mongoParseDocument("validator", validator)
		if err != nil {
			return nil, err
		}
		if !mongoIsEmpty(doc) {
			out = append(out, bson.E{Key: "validator", Value: doc})
		}
	}
	if level != "" {
		if !mongoValidationLevels[level] {
			return nil, fmt.Errorf("validation level is off, strict or moderate")
		}
		out = append(out, bson.E{Key: "validationLevel", Value: level})
	}
	if action != "" {
		if !mongoValidationActions[action] {
			return nil, fmt.Errorf("validation action is error or warn")
		}
		out = append(out, bson.E{Key: "validationAction", Value: action})
	}
	return out, nil
}

// MongoCreateCollectionWith makes a collection, a capped or time-series one,
// or a view.
func MongoCreateCollectionWith(ctx context.Context, client *mongo.Client, dbName, collection string, spec MongoCollectionSpec) error {
	if err := mongoNamespace(dbName, collection); err != nil {
		return err
	}
	if strings.HasPrefix(collection, "system.") {
		return fmt.Errorf("names starting with system. are the server's own")
	}
	cmd, err := spec.command(collection)
	if err != nil {
		return err
	}
	if err := mongoGuardView(dbName, spec.ViewOn, spec.Pipeline); err != nil {
		return err
	}
	return client.Database(dbName).RunCommand(ctx, cmd).Err()
}

// mongoGuardView refuses a view over a collection that holds credentials: a
// view is a second name for what it reads, and would be a way to open it.
func mongoGuardView(dbName, viewOn, pipeline string) error {
	if viewOn == "" {
		return nil
	}
	if err := mongoGuardCredentials(dbName, viewOn); err != nil {
		return err
	}
	if strings.TrimSpace(pipeline) == "" {
		return nil
	}
	stages, err := mongoParseArray("pipeline", pipeline)
	if err != nil {
		return err
	}
	return mongoGuardPipeline(dbName, stages)
}

// MongoRenameCollection renames a collection within its database. With
// dropTarget the collection already holding the new name is dropped first,
// which is why the handler asks more of the caller for it.
func MongoRenameCollection(ctx context.Context, client *mongo.Client, dbName, from, to string, dropTarget bool) error {
	if err := mongoNamespace(dbName, from); err != nil {
		return err
	}
	if err := mongoCollectionName(to); err != nil {
		return err
	}
	if from == to {
		return fmt.Errorf("the new name is the same as the old one")
	}
	if strings.HasPrefix(to, "system.") {
		return fmt.Errorf("names starting with system. are the server's own")
	}
	// renameCollection takes full namespaces and runs against admin, which is
	// how it can move a collection between databases. Both halves name the
	// same database here: a move between two is a copy, and is not this.
	cmd := bson.D{
		{Key: "renameCollection", Value: dbName + "." + from},
		{Key: "to", Value: dbName + "." + to},
		{Key: "dropTarget", Value: dropTarget},
	}
	return client.Database("admin").RunCommand(ctx, cmd).Err()
}

// MongoCollectionChange is a collMod: only the fields that are set are sent.
type MongoCollectionChange struct {
	// Validator replaces the validator; an empty document removes it.
	Validator        *string `json:"validator"`
	ValidationLevel  string  `json:"validationLevel"`
	ValidationAction string  `json:"validationAction"`

	// ViewOn and Pipeline redefine a view.
	ViewOn   string  `json:"viewOn"`
	Pipeline *string `json:"pipeline"`

	// CappedSize and CappedMax resize a capped collection.
	CappedSize *int64 `json:"cappedSize"`
	CappedMax  *int64 `json:"cappedMax"`

	// ExpireAfterSeconds sets how long a time-series or clustered collection
	// keeps a document; a negative value turns expiry off.
	ExpireAfterSeconds *int64 `json:"expireAfterSeconds"`
	// Granularity coarsens a time-series collection's buckets.
	Granularity string `json:"granularity"`
}

// RemovesData reports whether the change can delete documents: shrinking a
// capped collection drops its oldest, and an expiry removes whatever is
// already older than it.
func (c MongoCollectionChange) RemovesData() bool {
	if c.CappedSize != nil || c.CappedMax != nil {
		return true
	}
	return c.ExpireAfterSeconds != nil && *c.ExpireAfterSeconds >= 0
}

func (c MongoCollectionChange) command(collection string) (bson.D, error) {
	cmd := bson.D{{Key: "collMod", Value: collection}}
	if c.Validator != nil {
		validator := bson.Raw(mongoEmptyDocument)
		if strings.TrimSpace(*c.Validator) != "" {
			doc, err := mongoParseDocument("validator", *c.Validator)
			if err != nil {
				return nil, err
			}
			validator = doc
		}
		cmd = append(cmd, bson.E{Key: "validator", Value: validator})
	}
	rules, err := mongoValidationFields("", c.ValidationLevel, c.ValidationAction)
	if err != nil {
		return nil, err
	}
	cmd = append(cmd, rules...)
	if c.Pipeline != nil || c.ViewOn != "" {
		if c.Pipeline == nil || c.ViewOn == "" {
			return nil, fmt.Errorf("redefining a view takes both viewOn and its pipeline")
		}
		if err := mongoCollectionName(c.ViewOn); err != nil {
			return nil, err
		}
		stages := []bson.Raw{}
		if strings.TrimSpace(*c.Pipeline) != "" {
			if stages, err = mongoParseArray("pipeline", *c.Pipeline); err != nil {
				return nil, err
			}
		}
		if info := mongoClassifyStages(stages); info.Writes {
			return nil, fmt.Errorf("a view's pipeline cannot hold %s; a view only reads", info.WriteStage)
		}
		cmd = append(cmd, bson.E{Key: "viewOn", Value: c.ViewOn}, bson.E{Key: "pipeline", Value: mongoArray(stages)})
	}
	if c.CappedSize != nil {
		if *c.CappedSize <= 0 {
			return nil, fmt.Errorf("a capped collection's size must be more than zero bytes")
		}
		cmd = append(cmd, bson.E{Key: "cappedSize", Value: *c.CappedSize})
	}
	if c.CappedMax != nil {
		if *c.CappedMax <= 0 {
			return nil, fmt.Errorf("a capped collection's document limit must be more than zero")
		}
		cmd = append(cmd, bson.E{Key: "cappedMax", Value: *c.CappedMax})
	}
	if c.ExpireAfterSeconds != nil {
		if *c.ExpireAfterSeconds < 0 {
			cmd = append(cmd, bson.E{Key: "expireAfterSeconds", Value: "off"})
		} else {
			cmd = append(cmd, bson.E{Key: "expireAfterSeconds", Value: *c.ExpireAfterSeconds})
		}
	}
	if c.Granularity != "" {
		if !mongoGranularities[c.Granularity] {
			return nil, fmt.Errorf("granularity is seconds, minutes or hours")
		}
		cmd = append(cmd, bson.E{Key: "timeseries", Value: bson.D{{Key: "granularity", Value: c.Granularity}}})
	}
	if len(cmd) == 1 {
		return nil, fmt.Errorf("nothing to change")
	}
	return cmd, nil
}

// MongoModifyCollection applies a collMod.
func MongoModifyCollection(ctx context.Context, client *mongo.Client, dbName, collection string, change MongoCollectionChange) error {
	if err := mongoNamespace(dbName, collection); err != nil {
		return err
	}
	cmd, err := change.command(collection)
	if err != nil {
		return err
	}
	if change.Pipeline != nil {
		if err := mongoGuardView(dbName, change.ViewOn, *change.Pipeline); err != nil {
			return err
		}
	}
	return client.Database(dbName).RunCommand(ctx, cmd).Err()
}
