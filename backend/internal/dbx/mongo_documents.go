package dbx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/bsonx/bsoncore"
)

// Documents: reading them without losing their types, and writing them
// without touching more than was meant.
//
// The guarantees are the ones the SQL row editor makes, in Mongo's terms. A
// document is addressed by its _id, whatever type that is. A write that names
// one document changes one or reports that it could not. A write that names
// many says how many first when asked. And nothing here guesses a filter: an
// empty one is the caller's explicit choice, never a default.

const (
	mongoDefaultLimit = 50
	mongoMaxLimit     = 1000
	// mongoPageBytes bounds one page of documents by size as well as by
	// count. A thousand 16 MiB documents is a legal page and an unusable
	// response; past this many bytes of BSON the page ends early and says so.
	mongoPageBytes = 8 << 20

	mongoDefaultMaxTime = 30 * time.Second
	mongoMaxMaxTime     = 5 * time.Minute

	// mongoCountTime is how long an exact count may take before the answer
	// becomes the collection's estimated size instead. Counting a filter is a
	// scan when no index covers it, and a page of documents should not wait
	// on one.
	mongoCountTime = 5 * time.Second
	// mongoExactCountBelow is the collection size under which an unfiltered
	// count is done exactly. Above it the stored estimate is the answer: the
	// exact count of everything is a scan of everything.
	mongoExactCountBelow = 200_000
)

var (
	// ErrMongoNotFound is a write that named one document by _id and found
	// none.
	ErrMongoNotFound = errors.New("no document has this _id; it was deleted, or the _id is of a different type than the one sent")
	// ErrMongoWithheld is a request for a collection that holds credentials.
	ErrMongoWithheld = errors.New("it holds credentials and is not opened from here; accounts are listed, without them, under Access")
	// ErrMongoNoCollection is a request about a collection that is not
	// there.
	ErrMongoNoCollection = errors.New("there is no such collection")
	// ErrMongoChanged is a replace that carried the document as it was read,
	// and found it different.
	ErrMongoChanged = errors.New("the document changed after it was read; reload it and apply the edit again")
)

// MongoFindSpec is what the query bar holds. Every text field is Extended JSON
// or shell syntax; an empty one is absent.
type MongoFindSpec struct {
	Filter     string `json:"filter"`
	Projection string `json:"projection"`
	Sort       string `json:"sort"`
	Collation  string `json:"collation"`
	// Hint is an index name, or an index key pattern as a document.
	Hint      string `json:"hint"`
	Skip      int64  `json:"skip"`
	Limit     int64  `json:"limit"`
	MaxTimeMS int64  `json:"maxTimeMS"`
}

// MongoCounted is how many documents there are, and how far to trust it.
type MongoCounted struct {
	Value int64 `json:"value"`
	// Exact is true when the matching documents were counted.
	Exact bool `json:"exact"`
	// Scope is "filter" when Value counts what the filter matches, and
	// "collection" when it is the size of the whole collection — which is all
	// that could be had cheaply, and an upper bound on the other.
	Scope string `json:"scope"`
}

// MongoFindResult is one page of documents.
type MongoFindResult struct {
	Documents []MongoDoc `json:"documents"`
	Returned  int        `json:"returned"`
	Skip      int64      `json:"skip"`
	Limit     int64      `json:"limit"`
	// HasMore says another page follows this one.
	HasMore bool `json:"hasMore"`
	// Truncated says the page ended early because of its size in bytes
	// rather than at Limit. The next page starts at Skip + Returned.
	Truncated  bool          `json:"truncated"`
	Count      *MongoCounted `json:"count"`
	DurationMs int64         `json:"durationMs"`
	Statement  string        `json:"statement"`
}

// mongoParsedQuery is a MongoFindSpec with every field read.
type mongoParsedQuery struct {
	filter, projection, sort bson.Raw
	collationDoc             bson.Raw
	collation                *options.Collation
	hint                     any
	skip, limit              int64
	maxTime                  time.Duration
}

func (q MongoFindSpec) parse() (*mongoParsedQuery, error) {
	out := &mongoParsedQuery{skip: q.Skip, limit: q.Limit}
	var err error
	if out.filter, err = mongoParseOptionalDocument("filter", q.Filter); err != nil {
		return nil, err
	}
	if out.projection, err = mongoParseOptionalDocument("projection", q.Projection); err != nil {
		return nil, err
	}
	if out.sort, err = mongoParseOptionalDocument("sort", q.Sort); err != nil {
		return nil, err
	}
	if out.collationDoc, out.collation, err = mongoCollation(q.Collation); err != nil {
		return nil, err
	}
	if out.hint, err = mongoHint(q.Hint); err != nil {
		return nil, err
	}
	if out.skip < 0 {
		return nil, fmt.Errorf("skip cannot be negative")
	}
	out.limit = mongoClampLimit(out.limit)
	out.maxTime = mongoMaxTime(q.MaxTimeMS)
	return out, nil
}

func mongoClampLimit(limit int64) int64 {
	if limit <= 0 {
		return mongoDefaultLimit
	}
	if limit > mongoMaxLimit {
		return mongoMaxLimit
	}
	return limit
}

// mongoMaxTime turns the query bar's Max time into a duration, inside bounds:
// none given is the default, and nobody gets to hold a server for an hour.
func mongoMaxTime(ms int64) time.Duration {
	if ms <= 0 {
		return mongoDefaultMaxTime
	}
	d := time.Duration(ms) * time.Millisecond
	if d > mongoMaxMaxTime {
		return mongoMaxMaxTime
	}
	return d
}

// MongoRequestTimeout is how long a handler should let a request run for a
// given Max time: the server's own limit, plus room to send the answer.
func MongoRequestTimeout(maxTimeMS int64) time.Duration {
	return mongoMaxTime(maxTimeMS) + 15*time.Second
}

// mongoCollation reads a collation both ways it is needed: as the document a
// raw command takes and as the struct the driver's helpers take.
func mongoCollation(text string) (bson.Raw, *options.Collation, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil, nil
	}
	doc, err := mongoParseDocument("collation", text)
	if err != nil {
		return nil, nil, err
	}
	if mongoIsEmpty(doc) {
		return nil, nil, nil
	}
	elems, err := doc.Elements()
	if err != nil {
		return nil, nil, fmt.Errorf("collation is not a valid document: %w", err)
	}
	c := &options.Collation{}
	for _, e := range elems {
		v := e.Value()
		ok := true
		switch e.Key() {
		case "locale":
			c.Locale, ok = v.StringValueOK()
		case "caseLevel":
			c.CaseLevel, ok = v.BooleanOK()
		case "caseFirst":
			c.CaseFirst, ok = v.StringValueOK()
		case "strength":
			var n int64
			n, ok = v.AsInt64OK()
			c.Strength = int(n)
		case "numericOrdering":
			c.NumericOrdering, ok = v.BooleanOK()
		case "alternate":
			c.Alternate, ok = v.StringValueOK()
		case "maxVariable":
			c.MaxVariable, ok = v.StringValueOK()
		case "normalization":
			c.Normalization, ok = v.BooleanOK()
		case "backwards":
			c.Backwards, ok = v.BooleanOK()
		default:
			return nil, nil, fmt.Errorf("collation has no option called %q", e.Key())
		}
		if !ok {
			return nil, nil, fmt.Errorf("collation option %q has the wrong type", e.Key())
		}
	}
	if c.Locale == "" {
		return nil, nil, fmt.Errorf(`a collation needs a locale, like { "locale": "en" }`)
	}
	return doc, c, nil
}

// mongoHint reads an index hint: a key pattern when it is a document, the
// index's name otherwise.
func mongoHint(text string) (any, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	if strings.HasPrefix(text, "{") {
		doc, err := mongoParseDocument("hint", text)
		if err != nil {
			return nil, err
		}
		if mongoIsEmpty(doc) {
			return nil, nil
		}
		return doc, nil
	}
	if len(text) >= 2 && (text[0] == '"' || text[0] == '\'') && text[len(text)-1] == text[0] {
		text = text[1 : len(text)-1]
	}
	return text, nil
}

// mongoNamespace checks a database and collection name before either is put
// in a command. Both travel as values, so this is not about injection; it is
// about refusing the names that mean something else to the server ("$cmd")
// and saying why in words rather than in an error code.
func mongoNamespace(dbName, collection string) error {
	if err := mongoDatabaseName(dbName); err != nil {
		return err
	}
	if err := mongoCollectionName(collection); err != nil {
		return err
	}
	return mongoGuardCredentials(dbName, collection)
}

// mongoCredentialCollections are the collections that hold what accounts and
// cluster members authenticate with, by database: password verifiers, the
// keys members sign with, and the replication log, whose entry for an account
// being made or changed carries the verifier that was written.
var mongoCredentialCollections = map[string]map[string]bool{
	"admin": {"system.users": true, "system.keys": true},
	"local": {"oplog.rs": true},
}

func mongoHoldsCredentials(dbName, collection string) bool {
	return mongoCredentialCollections[dbName][collection]
}

// mongoGuardCredentials refuses the collections that hold credentials. A
// document browser pointed at admin.system.users would hand every account's
// password verifier to whoever may read documents, which is every role; the
// accounts themselves are listed, without them, by MongoListUsers.
func mongoGuardCredentials(dbName, collection string) error {
	if mongoHoldsCredentials(dbName, collection) {
		return fmt.Errorf("%s.%s: %w", dbName, collection, ErrMongoWithheld)
	}
	return nil
}

// mongoReadable is mongoNamespace for a request that answers with what a
// collection holds, or with a count of it: it also refuses a view that ends
// at a credential collection.
func mongoReadable(ctx context.Context, client *mongo.Client, dbName, collection string) error {
	if err := mongoNamespace(dbName, collection); err != nil {
		return err
	}
	return mongoGuardRead(ctx, client, dbName, collection)
}

// mongoViewDef is what a view reads: a collection or another view, through a
// pipeline. unreadable marks a definition this code could not follow.
type mongoViewDef struct {
	on         string
	pipeline   []bson.Raw
	unreadable bool
}

// mongoGuardRead refuses a read that would reach a credential collection
// under another name. A view is a second name for what it reads, and one is
// made with a single command by anyone who can reach the server, so refusing
// only the collection's own name would refuse only the honest spelling.
//
// The views are asked for when the request arrives rather than remembered,
// and only in the databases that have such a collection, so a read anywhere
// else costs nothing. A database whose views cannot be listed is not read:
// "could not tell" is not "it is not one".
func mongoGuardRead(ctx context.Context, client *mongo.Client, dbName string, collections ...string) error {
	if len(mongoCredentialCollections[dbName]) == 0 {
		return nil
	}
	entries, err := mongoCollectionEntries(ctx, client.Database(dbName), bson.D{{Key: "type", Value: "view"}})
	if err != nil {
		return fmt.Errorf("%s holds credentials, and its views could not be listed to see that this is not one over them: %w", dbName, err)
	}
	views := map[string]mongoViewDef{}
	for _, e := range entries {
		def := mongoViewDef{on: e.ViewOn}
		if def.pipeline, err = mongoParseArray("pipeline", e.Pipeline); err != nil {
			def.unreadable = true
		}
		views[e.Name] = def
	}
	seen := map[string]bool{}
	for _, name := range collections {
		if err := mongoViewReaches(dbName, name, views, seen); err != nil {
			return err
		}
	}
	return nil
}

// mongoViewReaches follows a name through the views it is made of and
// reports the credential collection it ends at, if it does. seen keeps a
// definition that names itself from being followed forever.
func mongoViewReaches(dbName, name string, views map[string]mongoViewDef, seen map[string]bool) error {
	if err := mongoGuardCredentials(dbName, name); err != nil {
		return err
	}
	view, isView := views[name]
	if !isView || seen[name] {
		return nil
	}
	seen[name] = true
	if view.unreadable {
		return fmt.Errorf("%s.%s is a view whose definition could not be read, in a database that holds credentials: %w", dbName, name, ErrMongoWithheld)
	}
	if err := mongoViewReaches(dbName, view.on, views, seen); err != nil {
		return fmt.Errorf("%s.%s is a view over %w", dbName, name, err)
	}
	for _, ref := range mongoPipelineCollections(view.pipeline, 0) {
		var err error
		if ref.db != "" && ref.db != dbName {
			err = mongoGuardCredentials(ref.db, ref.collection)
		} else {
			err = mongoViewReaches(dbName, ref.collection, views, seen)
		}
		if err != nil {
			return fmt.Errorf("%s.%s is a view over %w", dbName, name, err)
		}
	}
	return nil
}

func mongoDatabaseName(name string) error {
	if name == "" {
		return fmt.Errorf("a database is required")
	}
	if len(name) > 63 {
		return fmt.Errorf("database name is too long (63 bytes at most)")
	}
	if strings.ContainsAny(name, "/\\. \"$*<>:|?\x00") {
		return fmt.Errorf("database name %q holds a character MongoDB does not allow in one", name)
	}
	return nil
}

func mongoCollectionName(name string) error {
	if name == "" {
		return fmt.Errorf("a collection is required")
	}
	if len(name) > 255 {
		return fmt.Errorf("collection name is too long (255 bytes at most)")
	}
	if strings.ContainsAny(name, "$\x00") {
		return fmt.Errorf("collection name %q holds a character MongoDB does not allow in one", name)
	}
	return nil
}

// mongoCollRef writes a collection the way the shell would address it, for
// the statement shown beside a result.
func mongoCollRef(collection string) string {
	plain := collection != ""
	for i := 0; i < len(collection); i++ {
		c := collection[i]
		if !(c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (i > 0 && c >= '0' && c <= '9')) {
			plain = false
			break
		}
	}
	if plain {
		return "db." + collection
	}
	return "db.getCollection(" + strconv.Quote(collection) + ")"
}

// MongoFindDocuments runs the query bar: one page of documents and, when
// asked, how many there are in all.
func MongoFindDocuments(ctx context.Context, client *mongo.Client, dbName, collection string, q MongoFindSpec, withCount bool) (*MongoFindResult, error) {
	if err := mongoReadable(ctx, client, dbName, collection); err != nil {
		return nil, err
	}
	pq, err := q.parse()
	if err != nil {
		return nil, err
	}
	coll := client.Database(dbName).Collection(collection)

	// The count runs beside the page rather than before it: it is the slower
	// of the two on anything large, and the page does not depend on it.
	counted := make(chan *MongoCounted, 1)
	if withCount {
		go func() { counted <- mongoCount(ctx, coll, pq) }()
	}

	// One more than the page, so "is there another page" is known without
	// asking for a count.
	find := options.Find().SetSkip(pq.skip).SetLimit(pq.limit + 1).SetMaxTime(pq.maxTime)
	if !mongoIsEmpty(pq.projection) {
		find.SetProjection(pq.projection)
	}
	if !mongoIsEmpty(pq.sort) {
		find.SetSort(pq.sort)
	}
	if pq.collation != nil {
		find.SetCollation(pq.collation)
	}
	if pq.hint != nil {
		find.SetHint(pq.hint)
	}
	start := time.Now()
	cur, err := coll.Find(ctx, pq.filter, find)
	if err != nil {
		return nil, err
	}
	defer cur.Close(context.Background())

	res := &MongoFindResult{Documents: []MongoDoc{}, Skip: pq.skip, Limit: pq.limit}
	if err := mongoReadPage(ctx, cur, pq.limit, res); err != nil {
		return nil, err
	}
	res.DurationMs = time.Since(start).Milliseconds()
	res.Statement = mongoFindStatement(collection, pq)
	if withCount {
		res.Count = <-counted
	}
	return res, nil
}

// mongoReadPage drains a cursor into a page, stopping at the limit or at the
// byte budget, whichever comes first.
func mongoReadPage(ctx context.Context, cur *mongo.Cursor, limit int64, res *MongoFindResult) error {
	size := 0
	for cur.Next(ctx) {
		if int64(len(res.Documents)) >= limit {
			res.HasMore = true
			break
		}
		if len(res.Documents) > 0 && size+len(cur.Current) > mongoPageBytes {
			res.HasMore, res.Truncated = true, true
			break
		}
		doc, err := newMongoDoc(cur.Current)
		if err != nil {
			return err
		}
		size += len(cur.Current)
		res.Documents = append(res.Documents, doc)
	}
	// A cursor that failed part-way is a failed read, not a short page.
	if err := cur.Err(); err != nil {
		return err
	}
	res.Returned = len(res.Documents)
	return nil
}

func mongoFindStatement(collection string, pq *mongoParsedQuery) string {
	var b strings.Builder
	b.WriteString(mongoCollRef(collection) + ".find(" + mongoRelaxedJSON(pq.filter))
	if !mongoIsEmpty(pq.projection) {
		b.WriteString(", " + mongoRelaxedJSON(pq.projection))
	}
	b.WriteString(")")
	if !mongoIsEmpty(pq.sort) {
		b.WriteString(".sort(" + mongoRelaxedJSON(pq.sort) + ")")
	}
	if pq.collationDoc != nil {
		b.WriteString(".collation(" + mongoRelaxedJSON(pq.collationDoc) + ")")
	}
	if pq.skip > 0 {
		b.WriteString(".skip(" + strconv.FormatInt(pq.skip, 10) + ")")
	}
	b.WriteString(".limit(" + strconv.FormatInt(pq.limit, 10) + ")")
	return b.String()
}

// mongoCount answers "of how many". It never fails the request it rides
// with: no answer at all is nil.
func mongoCount(ctx context.Context, coll *mongo.Collection, pq *mongoParsedQuery) *MongoCounted {
	ctx, cancel := context.WithTimeout(ctx, mongoCountTime+2*time.Second)
	defer cancel()
	estimate, estErr := coll.EstimatedDocumentCount(ctx, options.EstimatedDocumentCount().SetMaxTime(mongoCountTime))
	if mongoIsEmpty(pq.filter) && estErr == nil && estimate > mongoExactCountBelow {
		return &MongoCounted{Value: estimate, Scope: "collection"}
	}
	opts := options.Count().SetMaxTime(mongoCountTime)
	if pq.collation != nil {
		opts.SetCollation(pq.collation)
	}
	if pq.hint != nil {
		opts.SetHint(pq.hint)
	}
	if n, err := coll.CountDocuments(ctx, pq.filter, opts); err == nil {
		return &MongoCounted{Value: n, Exact: true, Scope: "filter"}
	}
	if estErr != nil {
		return nil
	}
	return &MongoCounted{Value: estimate, Scope: "collection"}
}

// MongoCountDocuments counts what a filter matches, on its own request, for
// the page that wants the number after the documents.
func MongoCountDocuments(ctx context.Context, client *mongo.Client, dbName, collection string, q MongoFindSpec) (*MongoCounted, error) {
	if err := mongoReadable(ctx, client, dbName, collection); err != nil {
		return nil, err
	}
	pq, err := q.parse()
	if err != nil {
		return nil, err
	}
	count := mongoCount(ctx, client.Database(dbName).Collection(collection), pq)
	if count == nil {
		return nil, fmt.Errorf("the documents could not be counted")
	}
	return count, nil
}

func mongoIDFilter(idText string) (bson.D, bson.RawValue, error) {
	id, err := mongoParseValue("id", idText)
	if err != nil {
		return nil, bson.RawValue{}, err
	}
	// An array cannot be an _id, and an "_id: [..]" filter would mean "any of
	// these" rather than "this one".
	if id.Type == bsontype.Array || id.Type == bsontype.Undefined || id.Type == bsontype.Regex {
		return nil, bson.RawValue{}, fmt.Errorf("a value of type %s cannot be an _id", id.Type)
	}
	return bson.D{{Key: "_id", Value: bson.D{{Key: "$eq", Value: id}}}}, id, nil
}

// MongoGetDocument reads one document by its _id.
func MongoGetDocument(ctx context.Context, client *mongo.Client, dbName, collection, idText string) (*MongoDoc, error) {
	if err := mongoReadable(ctx, client, dbName, collection); err != nil {
		return nil, err
	}
	filter, _, err := mongoIDFilter(idText)
	if err != nil {
		return nil, err
	}
	raw, err := client.Database(dbName).Collection(collection).FindOne(ctx, filter).Raw()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrMongoNotFound
	}
	if err != nil {
		return nil, err
	}
	doc, err := newMongoDoc(raw)
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

// MongoWriteError is one document of a batch the server refused.
type MongoWriteError struct {
	Index   int    `json:"index"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MongoInsertResult reports what landed. A batch can land in part — a
// standalone server has no transaction to wrap it in — so the count and the
// refusals are both stated rather than one implied by the other.
type MongoInsertResult struct {
	Inserted    int               `json:"inserted"`
	InsertedIDs []string          `json:"insertedIds"`
	Errors      []MongoWriteError `json:"errors"`
}

// mongoMaxInsert bounds one insert request. More than this is an import.
const mongoMaxInsert = 1000

// MongoInsertDocuments inserts one document, or several when the text is a
// list. With ordered set the server stops at the first refusal; without it
// every document is tried.
func MongoInsertDocuments(ctx context.Context, client *mongo.Client, dbName, collection, text string, ordered bool) (*MongoInsertResult, error) {
	if err := mongoNamespace(dbName, collection); err != nil {
		return nil, err
	}
	var docs []bson.Raw
	if mongoIsArrayText(text) {
		var err error
		if docs, err = mongoParseArray("documents", text); err != nil {
			return nil, err
		}
	} else {
		doc, err := mongoParseDocument("document", text)
		if err != nil {
			return nil, err
		}
		docs = []bson.Raw{doc}
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("there are no documents to insert")
	}
	if len(docs) > mongoMaxInsert {
		return nil, fmt.Errorf("%d documents is more than one insert takes (%d); use Import for a file", len(docs), mongoMaxInsert)
	}
	batch := make([]any, 0, len(docs))
	for _, d := range docs {
		batch = append(batch, d)
	}
	res, err := client.Database(dbName).Collection(collection).
		InsertMany(ctx, batch, options.InsertMany().SetOrdered(ordered))
	out := &MongoInsertResult{InsertedIDs: []string{}, Errors: []MongoWriteError{}}
	if res != nil {
		for _, id := range res.InsertedIDs {
			out.InsertedIDs = append(out.InsertedIDs, mongoGoValueJSON(id))
		}
		out.Inserted = len(res.InsertedIDs)
	}
	var bulk mongo.BulkWriteException
	if errors.As(err, &bulk) && len(bulk.WriteErrors) > 0 {
		for _, we := range bulk.WriteErrors {
			out.Errors = append(out.Errors, MongoWriteError{Index: we.Index, Code: we.Code, Message: we.Message})
		}
		// Nothing landed: that is a refused request, and the first refusal is
		// the reason.
		if out.Inserted == 0 {
			return nil, err
		}
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// mongoGoValueJSON renders a value the driver handed back as a Go value — an
// inserted or upserted _id — as canonical Extended JSON.
func mongoGoValueJSON(v any) string {
	out, err := bson.MarshalExtJSON(bson.D{{Key: "v", Value: v}}, true, false)
	if err != nil || len(out) < len(`{"v":}`) {
		return ""
	}
	return string(out[len(`{"v":`) : len(out)-1])
}

// MongoWriteResult is what an update, replace or delete did — or, for a dry
// run, what it would reach.
type MongoWriteResult struct {
	// DryRun is true when nothing was written and Matched is a count taken
	// instead.
	DryRun   bool  `json:"dryRun"`
	Matched  int64 `json:"matched"`
	Modified int64 `json:"modified"`
	Deleted  int64 `json:"deleted"`
	// UpsertedID is the canonical Extended JSON of the _id an upsert created.
	UpsertedID string `json:"upsertedId,omitempty"`
}

// MongoReplacement is one document's replacement, as its editor sends it.
type MongoReplacement struct {
	// ID is the _id of the document being replaced, as Extended JSON.
	ID string `json:"id"`
	// Document is the whole replacement as Extended JSON.
	Document string `json:"document"`
	// ExpectedDigest is the digest the document had when it was read. When
	// sent, the replacement is made only if the stored document still has it.
	ExpectedDigest string `json:"expectedDigest"`
	// Expected says the same with the whole document as it was read. It is
	// the older of the two ways and doubles the request; one or the other.
	Expected string `json:"expected"`
}

// mongoAtomicReplaceBytes is how much the stored document and its
// replacement may come to together for the comparison to ride in the same
// operation as the write. That operation is one BSON document holding both,
// and the server takes none over 16 MiB.
const mongoAtomicReplaceBytes = 15 << 20

// MongoReplaceDocument replaces the one document with this _id.
//
// With ExpectedDigest or Expected the replace happens only if the stored
// document is still the one the editor read, so an edit never silently
// overwrites a change somebody else made in between. The stored document is
// read and compared here, byte for byte, and then compared again by the
// server in the same operation as the write — unless the two documents are
// too large to travel in one operation, when the first comparison is the
// only one and a change made in the moment between it and the write is
// overwritten.
func MongoReplaceDocument(ctx context.Context, client *mongo.Client, dbName, collection string, r MongoReplacement) (*MongoWriteResult, error) {
	if err := mongoNamespace(dbName, collection); err != nil {
		return nil, err
	}
	filter, id, err := mongoIDFilter(r.ID)
	if err != nil {
		return nil, err
	}
	doc, err := mongoParseDocument("document", r.Document)
	if err != nil {
		return nil, err
	}
	// _id is immutable. A replacement carrying another one is refused by the
	// server with a message about the "_id field being immutable", which does
	// not say what to do instead.
	if own, err := doc.LookupErr("_id"); err == nil && !(own.Type == id.Type && bytes.Equal(own.Value, id.Value)) {
		return nil, fmt.Errorf("the document's _id is not the one being replaced; an _id cannot be changed — clone the document and delete the original instead")
	}
	var unchanged func(stored bson.Raw) bool
	digest := strings.ToLower(strings.TrimSpace(r.ExpectedDigest))
	switch {
	case digest != "" && strings.TrimSpace(r.Expected) != "":
		return nil, fmt.Errorf("send expectedDigest or expected, not both")
	case digest != "":
		if raw, err := hex.DecodeString(digest); err != nil || len(raw) != sha256.Size {
			return nil, fmt.Errorf("expectedDigest is not a document's digest; send the one the document was read with")
		}
		unchanged = func(stored bson.Raw) bool { return mongoDigest(stored) == digest }
	case strings.TrimSpace(r.Expected) != "":
		expected, err := mongoParseDocument("expected", r.Expected)
		if err != nil {
			return nil, err
		}
		unchanged = func(stored bson.Raw) bool { return bytes.Equal(stored, expected) }
	}
	coll := client.Database(dbName).Collection(collection)
	byID := filter
	if unchanged != nil {
		stored, err := coll.FindOne(ctx, byID).Raw()
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrMongoNotFound
		}
		if err != nil {
			return nil, err
		}
		if !unchanged(stored) {
			return nil, ErrMongoChanged
		}
		if len(stored)+len(doc) <= mongoAtomicReplaceBytes {
			// $literal keeps a stored value that starts with "$" from being
			// read as a field path.
			filter = append(bson.D{}, byID...)
			filter = append(filter, bson.E{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{
				"$$ROOT", bson.D{{Key: "$literal", Value: stored}},
			}}}})
		}
	}
	res, err := coll.ReplaceOne(ctx, filter, doc)
	if err != nil {
		return nil, err
	}
	if res.MatchedCount == 0 {
		if len(filter) > len(byID) {
			// Told apart by asking again without the comparison: gone, or
			// only different.
			err := coll.FindOne(ctx, byID, options.FindOne().SetProjection(bson.D{{Key: "_id", Value: 1}})).Err()
			if err == nil {
				return nil, ErrMongoChanged
			}
			if !errors.Is(err, mongo.ErrNoDocuments) {
				return nil, err
			}
		}
		return nil, ErrMongoNotFound
	}
	return &MongoWriteResult{Matched: res.MatchedCount, Modified: res.ModifiedCount}, nil
}

// MongoUpdate is an update as the bulk-update form holds it.
type MongoUpdate struct {
	Filter string `json:"filter"`
	// Update is a document of update operators ({ "$set": … }) or, as a
	// list, an aggregation pipeline.
	Update       string `json:"update"`
	ArrayFilters string `json:"arrayFilters"`
	Collation    string `json:"collation"`
	Hint         string `json:"hint"`
	Many         bool   `json:"many"`
	Upsert       bool   `json:"upsert"`
	DryRun       bool   `json:"dryRun"`
}

// MongoUpdateDocuments applies update operators, or a pipeline, to the first
// document a filter matches or to all of them. With DryRun it only counts.
func MongoUpdateDocuments(ctx context.Context, client *mongo.Client, dbName, collection string, u MongoUpdate) (*MongoWriteResult, error) {
	if err := mongoReadable(ctx, client, dbName, collection); err != nil {
		return nil, err
	}
	filter, err := mongoParseOptionalDocument("filter", u.Filter)
	if err != nil {
		return nil, err
	}
	if mongoIsEmpty(filter) && !u.Many {
		return nil, fmt.Errorf("a filter is required to update one document; an empty filter would change whichever document comes first")
	}
	update, err := mongoParseUpdate(u.Update)
	if err != nil {
		return nil, err
	}
	_, collation, err := mongoCollation(u.Collation)
	if err != nil {
		return nil, err
	}
	hint, err := mongoHint(u.Hint)
	if err != nil {
		return nil, err
	}
	var arrayFilters []bson.Raw
	if strings.TrimSpace(u.ArrayFilters) != "" {
		if arrayFilters, err = mongoParseArray("arrayFilters", u.ArrayFilters); err != nil {
			return nil, err
		}
	}
	coll := client.Database(dbName).Collection(collection)
	if u.DryRun {
		return mongoDryRun(ctx, coll, filter, collation, hint, u.Many)
	}
	opts := options.Update().SetUpsert(u.Upsert)
	if collation != nil {
		opts.SetCollation(collation)
	}
	if hint != nil {
		opts.SetHint(hint)
	}
	if len(arrayFilters) > 0 {
		filters := make([]any, 0, len(arrayFilters))
		for _, f := range arrayFilters {
			filters = append(filters, f)
		}
		opts.SetArrayFilters(options.ArrayFilters{Filters: filters})
	}
	var res *mongo.UpdateResult
	if u.Many {
		res, err = coll.UpdateMany(ctx, filter, update, opts)
	} else {
		res, err = coll.UpdateOne(ctx, filter, update, opts)
	}
	if err != nil {
		return nil, err
	}
	out := &MongoWriteResult{Matched: res.MatchedCount, Modified: res.ModifiedCount}
	if res.UpsertedID != nil {
		out.UpsertedID = mongoGoValueJSON(res.UpsertedID)
	}
	return out, nil
}

// mongoParseUpdate reads the update half of an update. A document of plain
// fields is a replacement, and replacing through this route would let "update
// many" overwrite every matched document with one body; the server refuses
// that too, in fewer words.
func mongoParseUpdate(text string) (any, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf(`an update is required, like { "$set": { "field": value } }`)
	}
	if mongoIsArrayText(text) {
		stages, err := mongoParseArray("update", text)
		if err != nil {
			return nil, err
		}
		if len(stages) == 0 {
			return nil, fmt.Errorf("the update pipeline has no stages")
		}
		return mongoArray(stages), nil
	}
	doc, err := mongoParseDocument("update", text)
	if err != nil {
		return nil, err
	}
	elems, err := doc.Elements()
	if err != nil {
		return nil, fmt.Errorf("update is not a valid document: %w", err)
	}
	if len(elems) == 0 {
		return nil, fmt.Errorf(`the update is empty; it needs an operator, like { "$set": { "field": value } }`)
	}
	for _, e := range elems {
		if !strings.HasPrefix(e.Key(), "$") {
			return nil, fmt.Errorf(`%q is not an update operator; wrap the fields to change in one, like { "$set": { %q: … } }, or replace the whole document from its editor`, e.Key(), e.Key())
		}
	}
	return doc, nil
}

// mongoDryRun counts what a write would reach, with the same filter,
// collation and hint the write would use.
func mongoDryRun(ctx context.Context, coll *mongo.Collection, filter any, collation *options.Collation, hint any, many bool) (*MongoWriteResult, error) {
	opts := options.Count().SetMaxTime(mongoDefaultMaxTime)
	if collation != nil {
		opts.SetCollation(collation)
	}
	if hint != nil {
		opts.SetHint(hint)
	}
	// One is all a single-document write can reach, so one is all that is
	// counted.
	if !many {
		opts.SetLimit(1)
	}
	n, err := coll.CountDocuments(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	return &MongoWriteResult{DryRun: true, Matched: n}, nil
}

// MongoDeletion is a delete as the bulk-delete form holds it. ID addresses
// one document and takes the place of Filter.
type MongoDeletion struct {
	ID        string `json:"id"`
	Filter    string `json:"filter"`
	Collation string `json:"collation"`
	Hint      string `json:"hint"`
	Many      bool   `json:"many"`
	DryRun    bool   `json:"dryRun"`
}

// MongoDeleteDocuments removes the first document a filter matches, or all of
// them. With DryRun it only counts.
func MongoDeleteDocuments(ctx context.Context, client *mongo.Client, dbName, collection string, d MongoDeletion) (*MongoWriteResult, error) {
	if err := mongoReadable(ctx, client, dbName, collection); err != nil {
		return nil, err
	}
	var filter any
	byID := strings.TrimSpace(d.ID) != ""
	switch {
	case byID && strings.TrimSpace(d.Filter) != "":
		return nil, fmt.Errorf("send an id or a filter, not both")
	case byID && d.Many:
		return nil, fmt.Errorf("an id names one document; many needs a filter")
	case byID:
		f, _, err := mongoIDFilter(d.ID)
		if err != nil {
			return nil, err
		}
		filter = f
	default:
		raw, err := mongoParseOptionalDocument("filter", d.Filter)
		if err != nil {
			return nil, err
		}
		if mongoIsEmpty(raw) && !d.Many {
			return nil, fmt.Errorf("a filter is required to delete one document; an empty filter would delete whichever document comes first")
		}
		filter = raw
	}
	_, collation, err := mongoCollation(d.Collation)
	if err != nil {
		return nil, err
	}
	hint, err := mongoHint(d.Hint)
	if err != nil {
		return nil, err
	}
	coll := client.Database(dbName).Collection(collection)
	if d.DryRun {
		return mongoDryRun(ctx, coll, filter, collation, hint, d.Many)
	}
	opts := options.Delete()
	if collation != nil {
		opts.SetCollation(collation)
	}
	if hint != nil {
		opts.SetHint(hint)
	}
	var res *mongo.DeleteResult
	if d.Many {
		res, err = coll.DeleteMany(ctx, filter, opts)
	} else {
		res, err = coll.DeleteOne(ctx, filter, opts)
	}
	if err != nil {
		return nil, err
	}
	if byID && res.DeletedCount == 0 {
		return nil, ErrMongoNotFound
	}
	return &MongoWriteResult{Matched: res.DeletedCount, Deleted: res.DeletedCount}, nil
}

// MongoCloneDocument inserts a copy of one document under a new _id and
// returns the copy as stored.
func MongoCloneDocument(ctx context.Context, client *mongo.Client, dbName, collection, idText string) (*MongoDoc, error) {
	if err := mongoNamespace(dbName, collection); err != nil {
		return nil, err
	}
	filter, _, err := mongoIDFilter(idText)
	if err != nil {
		return nil, err
	}
	coll := client.Database(dbName).Collection(collection)
	raw, err := coll.FindOne(ctx, filter).Raw()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrMongoNotFound
	}
	if err != nil {
		return nil, err
	}
	elems, err := raw.Elements()
	if err != nil {
		return nil, err
	}
	// The copy is assembled from the original's own bytes, every field but
	// _id, so nothing about it is re-encoded on the way through.
	kept := make([][]byte, 0, len(elems))
	for _, e := range elems {
		if e.Key() != "_id" {
			kept = append(kept, e)
		}
	}
	copied := bson.Raw(bsoncore.BuildDocumentFromElements(nil, kept...))
	res, err := coll.InsertOne(ctx, copied)
	if err != nil {
		return nil, err
	}
	stored, err := coll.FindOne(ctx, bson.D{{Key: "_id", Value: res.InsertedID}}).Raw()
	if err != nil {
		return nil, err
	}
	doc, err := newMongoDoc(stored)
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

// MongoTimedOut reports whether an operation ran past its Max time, which the
// operator can raise, as opposed to failing.
func MongoTimedOut(err error) bool {
	var cmd mongo.CommandError
	if errors.As(err, &cmd) && cmd.Code == 50 {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded) || mongo.IsTimeout(err)
}

// MongoUnreachable reports whether an error is the connection's rather than
// the request's: the server went away, or no server could be chosen.
func MongoUnreachable(err error) bool {
	var srv mongo.ServerError
	if errors.As(err, &srv) {
		return false
	}
	return mongo.IsNetworkError(err) || errors.Is(err, mongo.ErrClientDisconnected) ||
		strings.Contains(err.Error(), "server selection error")
}
