package dbx

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Dumps for the two engines that are not SQL.
//
// Both have an official tool — mongodump and redis-cli --rdb — and the image
// carries the first. Neither is depended on: mongodump is a separate package
// that a rebuilt image can lose, redis-cli's RDB path needs replication
// permissions the dashboard's login often does not have, and "the backup button
// works on this machine" is not a property worth leaving to what happened to be
// installed. The fallbacks below go through the drivers the dashboard is already
// connected with, so they work wherever the browser tabs do.
//
// The format is gzipped JSON Lines: a metadata line, then one line per document
// or key. It streams in both directions, it survives a truncated file (every
// line before the break is still readable), and it is greppable with zcat.

// dumpArchiveVersion is written into the header. A restore refuses a version it
// does not understand rather than half-reading it.
//
// Version 2 is where a Redis archive began to cover more than one numbered
// database and a Mongo one to carry each collection's options and indexes. A
// version 1 archive is still read: it has neither, and says so by having none.
const dumpArchiveVersion = 2

type dumpArchiveHeader struct {
	Format   string `json:"format"`
	Version  int    `json:"version"`
	Driver   Driver `json:"driver"`
	Database string `json:"database"`
	Taken    string `json:"taken"`
}

// --- Redis ----------------------------------------------------------------

// redisDumpEntry is one key, or — when only DB is set — the line that says
// which numbered database the keys after it belong to. The payload is Redis's
// own serialisation — the same bytes DUMP produces and RESTORE consumes — which
// is what makes this faithful for every type including the ones with no textual
// form: a stream's entry ids, a sorted set's scores, a hash field's TTL.
//
// Key and payload are base64 because both are binary as far as Redis is
// concerned; a key is a byte string, not text, and one holding invalid UTF-8 is
// legal and would not survive JSON.
type redisDumpEntry struct {
	DB      *int   `json:"db,omitempty"`
	Key     string `json:"k,omitempty"`
	Payload string `json:"p,omitempty"`
	TTLms   int64  `json:"t,omitempty"`
}

// dumpRedis writes every numbered database that holds a key, or the ones the
// caller chose.
//
// It used to write one: the connection string's. A Redis server is routinely
// used as several — a cache in 0, a queue in 1, sessions in 2 — and a backup
// of "the Redis server" that turned out to hold only the first of them is
// found out on the day the others are needed.
func dumpRedis(ctx context.Context, dsn, outDir string, opts DumpOptions) (*DumpResult, error) {
	client, err := RedisClient(ctx, dsn, 0)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	// One connection for the whole dump. SELECT is a property of a
	// connection, and a pool hands the next command to whichever one is free.
	conn := client.Conn()
	defer conn.Close()

	indexes, err := redisDumpDatabases(ctx, conn, dsn, opts)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(indexes))
	for i, n := range indexes {
		names[i] = strconv.Itoa(n)
	}
	label := "redis-db" + strings.Join(names, "_")
	if len(indexes) > 4 {
		label = "redis-all"
	}

	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return nil, err
	}
	start := time.Now()
	path := freeDumpPath(outDir, dumpFilename(label, "redis", "jsonl.gz", start))
	f, gz, w, cleanup, err := createArchive(path, dumpArchiveHeader{
		Format: "jd-redis", Version: dumpArchiveVersion, Driver: DriverRedis,
		Database: strings.Join(names, ","), Taken: start.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() { cleanup(&ok) }()

	enc := json.NewEncoder(w)
	var total int64
	for _, idx := range indexes {
		if err := conn.Select(ctx, idx).Err(); err != nil {
			return nil, fmt.Errorf("cannot select database %d: %w", idx, err)
		}
		db := idx
		if err := enc.Encode(redisDumpEntry{DB: &db}); err != nil {
			return nil, err
		}
		var (
			cursor uint64
			keys   int64
		)
		for {
			batch, next, err := conn.Scan(ctx, cursor, "*", 500).Result()
			if err != nil {
				return nil, err
			}
			for _, key := range batch {
				payload, err := conn.Dump(ctx, key).Result()
				if err == redis.Nil {
					// Expired between the SCAN and the DUMP. Not an error: it is
					// not in the database any more, so it does not belong in a
					// snapshot of the database.
					continue
				}
				if err != nil {
					return nil, fmt.Errorf("cannot dump key %q: %w", key, err)
				}
				ttl, err := conn.PTTL(ctx, key).Result()
				if err != nil {
					return nil, err
				}
				ms := int64(0)
				if ttl > 0 {
					ms = ttl.Milliseconds()
				}
				if err := enc.Encode(redisDumpEntry{
					Key:     base64.StdEncoding.EncodeToString([]byte(key)),
					Payload: base64.StdEncoding.EncodeToString([]byte(payload)),
					TTLms:   ms,
				}); err != nil {
					return nil, err
				}
				keys++
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
		total += keys
		opts.progress("db%d: %d keys", idx, keys)
	}
	if err := finishArchive(f, gz, w); err != nil {
		return nil, err
	}
	ok = true
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	summary := fmt.Sprintf("%d keys", total)
	if len(indexes) > 1 {
		summary = fmt.Sprintf("%d keys in %d databases", total, len(indexes))
	}
	return &DumpResult{
		Path: path, Size: st.Size(), Driver: DriverRedis, Database: strings.Join(names, ","),
		Duration:  time.Since(start).Round(time.Millisecond).String(),
		StartedAt: start.UTC(), Summary: summary,
	}, nil
}

// redisDumpDatabases decides which numbered databases a dump covers: the ones
// asked for, or the one named, or every one that holds a key. A server holding
// nothing at all still gets a dump — of the connection's own database, empty —
// rather than a refusal.
func redisDumpDatabases(ctx context.Context, conn *redis.Conn, dsn string, opts DumpOptions) ([]int, error) {
	if len(opts.RedisDatabases) > 0 {
		seen := map[int]bool{}
		out := []int{}
		for _, n := range opts.RedisDatabases {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
		sort.Ints(out)
		return out, nil
	}
	if strings.TrimSpace(opts.Database) != "" {
		idx, err := redisDatabaseIndex(dsn, opts.Database)
		if err != nil {
			return nil, err
		}
		return []int{idx}, nil
	}
	out := []int{}
	if info, err := conn.Info(ctx, "keyspace").Result(); err == nil {
		for _, line := range strings.Split(info, "\n") {
			name, _, found := strings.Cut(strings.TrimSpace(line), ":")
			if !found || !strings.HasPrefix(name, "db") {
				continue
			}
			if n, err := strconv.Atoi(strings.TrimPrefix(name, "db")); err == nil && n >= 0 {
				out = append(out, n)
			}
		}
	}
	if len(out) == 0 {
		idx, err := redisDatabaseIndex(dsn, "")
		if err != nil {
			return nil, err
		}
		out = append(out, idx)
	}
	sort.Ints(out)
	return out, nil
}

// restoreRedis loads an archive back. With no database named, every key goes
// back to the numbered database it came from; with one named, the archive has
// to be of a single database, and that is where its keys go.
func restoreRedis(ctx context.Context, dsn, database, path string, opts RestoreOptions) (string, error) {
	lines, header, closeArchive, err := openArchive(path, "jd-redis")
	if err != nil {
		return "", err
	}
	defer closeArchive()

	sources := strings.Split(header.Database, ",")
	target := -1
	if strings.TrimSpace(database) != "" {
		if len(sources) > 1 {
			return "", fmt.Errorf("this archive covers databases %s; it can only be restored to where each key came from",
				strings.Join(sources, ", "))
		}
		if target, err = redisDatabaseIndex(dsn, database); err != nil {
			return "", err
		}
	}

	client, err := RedisClient(ctx, dsn, 0)
	if err != nil {
		return "", err
	}
	defer client.Close()
	conn := client.Conn()
	defer conn.Close()

	// Where the keys go until the archive says otherwise: a version 1 archive
	// never does, and its header names its one database.
	current := target
	if current < 0 {
		if current, err = redisDatabaseIndex(dsn, sources[0]); err != nil {
			return "", err
		}
	}
	if err := conn.Select(ctx, current).Err(); err != nil {
		return "", fmt.Errorf("cannot select database %d: %w", current, err)
	}

	var restored int64
	touched := map[int]bool{}
	for lines.Scan() {
		var e redisDumpEntry
		if err := json.Unmarshal(lines.Bytes(), &e); err != nil {
			return "", fmt.Errorf("corrupt dump at key %d: %w", restored+1, err)
		}
		if e.DB != nil && e.Key == "" {
			if target < 0 && *e.DB != current {
				current = *e.DB
				if err := conn.Select(ctx, current).Err(); err != nil {
					return "", fmt.Errorf("cannot select database %d: %w", current, err)
				}
			}
			continue
		}
		key, err := base64.StdEncoding.DecodeString(e.Key)
		if err != nil {
			return "", err
		}
		payload, err := base64.StdEncoding.DecodeString(e.Payload)
		if err != nil {
			return "", err
		}
		// REPLACE, because a restore is a restore: without it every key that
		// already exists fails with BUSYKEY and the operator gets a half-loaded
		// database and a wall of errors.
		if err := conn.RestoreReplace(ctx, string(key), time.Duration(e.TTLms)*time.Millisecond, string(payload)).Err(); err != nil {
			return "", fmt.Errorf("cannot restore key %q: %w", key, err)
		}
		restored++
		touched[current] = true
		if restored%5000 == 0 {
			opts.progress("%d keys restored", restored)
		}
	}
	if err := lines.Err(); err != nil {
		return "", err
	}
	if len(touched) > 1 {
		return fmt.Sprintf("%d keys restored into %d databases", restored, len(touched)), nil
	}
	return fmt.Sprintf("%d keys restored into db%d", restored, current), nil
}

// redisDatabaseIndex resolves which numbered database to act on. Redis names
// them with integers, so the "database" the rest of the product passes around
// as a string is one here — and an empty one means the connection's own, which
// is what the operator sees when they open the browser.
func redisDatabaseIndex(dsn, database string) (int, error) {
	if strings.TrimSpace(database) != "" {
		n, err := strconv.Atoi(strings.TrimSpace(database))
		if err != nil || n < 0 {
			return 0, fmt.Errorf("redis databases are numbered; %q is not a number", database)
		}
		return n, nil
	}
	if opt, err := redis.ParseURL(dsn); err == nil {
		return opt.DB, nil
	}
	return 0, nil
}

// --- Mongo ----------------------------------------------------------------

// mongoDumpEntry is one document, or — when Collection is set — the line that
// opens a collection and carries what it was created with.
type mongoDumpEntry struct {
	Collection string          `json:"c,omitempty"`
	Document   json.RawMessage `json:"d,omitempty"`
	// Options is the collection's own: its validator, its collation, that it
	// is capped or a time series or a view and of what.
	Options json.RawMessage `json:"o,omitempty"`
	// Kind is "view" or "timeseries" where it is not an ordinary collection.
	Kind string `json:"k,omitempty"`
	// Indexes are the index definitions as the server lists them.
	Indexes []json.RawMessage `json:"x,omitempty"`
}

// dumpMongoDriver is the fallback for a machine with no mongodump. Documents go
// out as canonical Extended JSON, which is the format that survives the types
// BSON has and JSON does not — an ObjectId stays an ObjectId, a 64-bit integer
// does not become a float, a date does not become a string.
//
// Each collection travels with its options and its indexes. Without them the
// restore brought the documents back into bare collections: the unique index
// that had been refusing duplicates was gone, and so was the validator.
func dumpMongoDriver(ctx context.Context, dsn, outDir string, opts DumpOptions) (*DumpResult, error) {
	database := opts.Database
	client, err := MongoClient(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer client.Disconnect(context.Background())

	if database == "" {
		return nil, fmt.Errorf("no database named in the connection string; specify one explicitly")
	}
	sel, err := newDumpSelection(opts.Tables, opts.ExcludeTables)
	if err != nil {
		return nil, err
	}
	db := client.Database(database)
	specs, err := db.ListCollectionSpecifications(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	sort.Slice(specs, func(i, j int) bool {
		// Views last: each is created over collections that have to be there.
		if (specs[i].Type == "view") != (specs[j].Type == "view") {
			return specs[j].Type == "view"
		}
		return specs[i].Name < specs[j].Name
	})
	if missing := sel.missing(func(ref tableRef) bool {
		for _, spec := range specs {
			if spec.Name == ref.table {
				return true
			}
		}
		return false
	}); len(missing) > 0 {
		return nil, fmt.Errorf("no such collection to dump: %s", strings.Join(missing, ", "))
	}

	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return nil, err
	}
	start := time.Now()
	path := freeDumpPath(outDir, dumpFilename(database, "mongo", "jsonl.gz", start))
	f, gz, w, cleanup, err := createArchive(path, dumpArchiveHeader{
		Format: "jd-mongo", Version: dumpArchiveVersion, Driver: DriverMongo,
		Database: database, Taken: start.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() { cleanup(&ok) }()

	enc := json.NewEncoder(w)
	var (
		docs  int64
		colls int
	)
	for _, spec := range specs {
		name := spec.Name
		if strings.HasPrefix(name, "system.") || !sel.wants(database, name) {
			continue
		}
		entry := mongoDumpEntry{Collection: name}
		if spec.Type != "" && spec.Type != "collection" {
			entry.Kind = spec.Type
		}
		if len(spec.Options) > 0 {
			if ext, err := bson.MarshalExtJSON(spec.Options, true, false); err == nil && string(ext) != "{}" {
				entry.Options = ext
			}
		}
		if spec.Type != "view" {
			if entry.Indexes, err = mongoIndexSpecs(ctx, db.Collection(name)); err != nil {
				return nil, fmt.Errorf("cannot read the indexes of %s: %w", name, err)
			}
		}
		// The collection line comes first even when the collection is empty, so
		// a restore recreates it rather than silently dropping it.
		if err := enc.Encode(entry); err != nil {
			return nil, err
		}
		colls++
		if spec.Type == "view" {
			// A view's documents are a pipeline's answer, and the pipeline is
			// in its options.
			opts.progress("%s: view", name)
			continue
		}
		cur, err := db.Collection(name).Find(ctx, bson.D{}, options.Find().SetBatchSize(500))
		if err != nil {
			return nil, err
		}
		var n int64
		for cur.Next(ctx) {
			ext, err := bson.MarshalExtJSON(cur.Current, true, false)
			if err != nil {
				cur.Close(ctx)
				return nil, err
			}
			if err := enc.Encode(mongoDumpEntry{Document: ext}); err != nil {
				cur.Close(ctx)
				return nil, err
			}
			n++
		}
		err = cur.Err()
		cur.Close(ctx)
		if err != nil {
			return nil, err
		}
		docs += n
		opts.progress("%s: %d documents", name, n)
	}
	if err := finishArchive(f, gz, w); err != nil {
		return nil, err
	}
	ok = true
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return &DumpResult{
		Path: path, Size: st.Size(), Driver: DriverMongo, Database: database,
		Duration:  time.Since(start).Round(time.Millisecond).String(),
		StartedAt: start.UTC(),
		Summary:   fmt.Sprintf("%d collections, %d documents", colls, docs),
	}, nil
}

// mongoIndexSpecs lists a collection's indexes other than the one on _id,
// which every collection is given when it is made.
func mongoIndexSpecs(ctx context.Context, coll *mongo.Collection) ([]json.RawMessage, error) {
	cur, err := coll.Indexes().List(ctx)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := []json.RawMessage{}
	for cur.Next(ctx) {
		var spec bson.D
		if err := cur.Decode(&spec); err != nil {
			return nil, err
		}
		kept := bson.D{}
		isID := false
		for _, e := range spec {
			switch e.Key {
			case "ns":
				// Older servers record the namespace the index was built in.
				// Restored under another name it would be refused for it.
				continue
			case "name":
				isID = e.Value == "_id_"
			}
			kept = append(kept, e)
		}
		if isID {
			continue
		}
		ext, err := bson.MarshalExtJSON(kept, true, false)
		if err != nil {
			return nil, err
		}
		out = append(out, ext)
	}
	return out, cur.Err()
}

func restoreMongoDriver(ctx context.Context, dsn, database, path string, opts RestoreOptions) (string, error) {
	client, err := MongoClient(ctx, dsn)
	if err != nil {
		return "", err
	}
	defer client.Disconnect(context.Background())
	lines, _, closeArchive, err := openArchive(path, "jd-mongo")
	if err != nil {
		return "", err
	}
	defer closeArchive()

	db := client.Database(database)
	var (
		current *mongo.Collection
		indexes []json.RawMessage
		batch   []any
		docs    int64
		colls   int
	)
	flush := func() error {
		if current == nil || len(batch) == 0 {
			return nil
		}
		_, err := current.InsertMany(ctx, batch)
		batch = batch[:0]
		return err
	}
	// finish closes the collection being loaded: its last documents, then its
	// indexes — after the documents, so each is built once over all of them
	// rather than maintained through every insert.
	finish := func() error {
		if err := flush(); err != nil {
			return err
		}
		if current == nil || len(indexes) == 0 {
			return nil
		}
		specs := bson.A{}
		for _, raw := range indexes {
			var spec bson.D
			if err := bson.UnmarshalExtJSON(raw, true, &spec); err != nil {
				return fmt.Errorf("corrupt index definition for %s: %w", current.Name(), err)
			}
			specs = append(specs, spec)
		}
		indexes = nil
		return db.RunCommand(ctx, bson.D{
			{Key: "createIndexes", Value: current.Name()}, {Key: "indexes", Value: specs},
		}).Err()
	}
	for lines.Scan() {
		var e mongoDumpEntry
		if err := json.Unmarshal(lines.Bytes(), &e); err != nil {
			return "", fmt.Errorf("corrupt dump near document %d: %w", docs+1, err)
		}
		if e.Collection != "" {
			if err := finish(); err != nil {
				return "", err
			}
			// Dropping first is what makes the restore a restore rather than a
			// merge: without it the _id values in the dump collide with the
			// ones already there and every insert fails.
			if err := db.Collection(e.Collection).Drop(ctx); err != nil {
				return "", err
			}
			create := bson.D{{Key: "create", Value: e.Collection}}
			if len(e.Options) > 0 {
				var collOptions bson.D
				if err := bson.UnmarshalExtJSON(e.Options, true, &collOptions); err != nil {
					return "", fmt.Errorf("corrupt options for %s: %w", e.Collection, err)
				}
				create = append(create, collOptions...)
			}
			if err := db.RunCommand(ctx, create).Err(); err != nil &&
				!strings.Contains(err.Error(), "already exists") {
				return "", fmt.Errorf("cannot create %s: %w", e.Collection, err)
			}
			colls++
			if e.Kind == "view" {
				current, indexes = nil, nil
				continue
			}
			current, indexes = db.Collection(e.Collection), e.Indexes
			opts.progress("%s", e.Collection)
			continue
		}
		if current == nil {
			return "", fmt.Errorf("dump holds a document that belongs to no collection")
		}
		var doc bson.D
		if err := bson.UnmarshalExtJSON(e.Document, true, &doc); err != nil {
			return "", fmt.Errorf("corrupt document %d: %w", docs+1, err)
		}
		batch = append(batch, doc)
		docs++
		if len(batch) >= 500 {
			if err := flush(); err != nil {
				return "", err
			}
		}
	}
	if err := lines.Err(); err != nil {
		return "", err
	}
	if err := finish(); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d collections, %d documents restored into %s", colls, docs, database), nil
}

// mongoToolSelection turns a selection into mongodump's terms. The tool takes
// one collection to include or any number to leave out, so a selection of
// several is said as everything else being left out.
func mongoToolSelection(ctx context.Context, dsn, database string, sel dumpSelection) (string, []string, error) {
	exclude := []string{}
	for _, t := range sel.exclude {
		exclude = append(exclude, t.table)
	}
	switch len(sel.include) {
	case 0:
		return "", exclude, nil
	case 1:
		if len(exclude) == 0 {
			return sel.include[0].table, nil, nil
		}
	}
	client, err := MongoClient(ctx, dsn)
	if err != nil {
		return "", nil, err
	}
	defer client.Disconnect(context.Background())
	names, err := client.Database(database).ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return "", nil, err
	}
	have := map[string]bool{}
	out := []string{}
	for _, name := range names {
		have[name] = true
		if !sel.wants(database, name) && !strings.HasPrefix(name, "system.") {
			out = append(out, name)
		}
	}
	for _, t := range sel.include {
		if !have[t.table] {
			return "", nil, fmt.Errorf("no such collection to dump: %s", t.table)
		}
	}
	sort.Strings(out)
	return "", out, nil
}

// --- archive plumbing -----------------------------------------------------

// createArchive opens the file, wraps it in gzip and a buffer, and writes the
// header line. The returned cleanup removes the file unless the caller flips
// its flag, for the same reason the SQL dump does: a partial file that looks
// like a backup is the failure mode worth engineering against.
func createArchive(path string, header dumpArchiveHeader) (*os.File, *gzip.Writer, *bufio.Writer, func(*bool), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	gz := gzip.NewWriter(f)
	w := bufio.NewWriterSize(gz, 64<<10)
	cleanup := func(ok *bool) {
		f.Close()
		if !*ok {
			os.Remove(path)
		}
	}
	if err := json.NewEncoder(w).Encode(header); err != nil {
		ok := false
		cleanup(&ok)
		return nil, nil, nil, nil, err
	}
	return f, gz, w, cleanup, nil
}

func finishArchive(f *os.File, gz *gzip.Writer, w *bufio.Writer) error {
	if err := w.Flush(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return f.Sync()
}

// openArchive reads the header, checks it is the format the caller expects, and
// returns a scanner positioned on the first entry.
func openArchive(path, format string) (*bufio.Scanner, dumpArchiveHeader, func(), error) {
	var h dumpArchiveHeader
	f, err := os.Open(path)
	if err != nil {
		return nil, h, nil, err
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		return nil, h, nil, fmt.Errorf("%s is not a Just Dashboard archive: %w", filepath.Base(path), err)
	}
	closeAll := func() { gz.Close(); f.Close() }
	sc := bufio.NewScanner(gz)
	// A single document can be 16 MB in Mongo and a Redis value larger still,
	// so the default 64 KB line cap would refuse to read back what this wrote.
	// A Redis string may be 512 MB, and base64 makes it a third longer again.
	sc.Buffer(make([]byte, 0, 256<<10), 768<<20)
	if !sc.Scan() {
		closeAll()
		return nil, h, nil, fmt.Errorf("archive is empty")
	}
	if err := json.Unmarshal(sc.Bytes(), &h); err != nil {
		closeAll()
		return nil, h, nil, fmt.Errorf("archive has no header: %w", err)
	}
	if h.Format != format {
		closeAll()
		return nil, h, nil, fmt.Errorf("this is a %s archive, not %s", h.Format, format)
	}
	if h.Version > dumpArchiveVersion {
		closeAll()
		return nil, h, nil, fmt.Errorf("archive was written by a newer version of the dashboard")
	}
	return sc, h, closeAll, nil
}
