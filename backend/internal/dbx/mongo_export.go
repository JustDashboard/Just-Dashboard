package dbx

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Exporting documents as documents.
//
// The export every engine shares writes a collection through the grid: the
// types are flattened to strings and what comes out cannot be loaded back as
// what went in. This one writes Extended JSON, one document per line — the
// form mongoexport writes and mongoimport and this dashboard's own import
// read — so an export is also a copy.
//
// It is split in two because of what an HTTP response can still say at each
// point. Everything that can fail for a reason worth a sentence — a filter
// that does not parse, a server that is not there, a hint naming no index —
// happens in MongoOpenExport, before a byte is written, and can still be an
// error response. Only then does Write start the file.

const (
	mongoExportRows    = 100_000
	mongoExportMaxRows = 1_000_000
)

// MongoExportSpec is a query to export and the form to write it in.
type MongoExportSpec struct {
	MongoFindSpec
	// Format is ndjson (one document per line), json (one array) or csv.
	Format string
	// Relaxed writes relaxed Extended JSON, which reads more easily and
	// loses the width of numbers. Canonical is the default: it loses nothing.
	Relaxed bool
}

// MongoDocumentExport is an export ready to be written.
type MongoDocumentExport struct {
	// Limit is the most documents the export will write, and Total how many
	// the query matches when that could be counted in time; nil otherwise.
	Limit int64
	Total *int64

	cursor  *mongo.Cursor
	format  string
	relaxed bool
	columns []string
}

// Truncated reports whether the export stops short of everything the query
// matches: "true", "false", or "unknown" when the matches could not be
// counted.
func (e *MongoDocumentExport) Truncated() string {
	if e.Total == nil {
		return "unknown"
	}
	return strconv.FormatBool(*e.Total > e.Limit)
}

func (e *MongoDocumentExport) ContentType() string {
	switch e.format {
	case "csv":
		return "text/csv"
	case "json":
		return "application/json"
	}
	return "application/x-ndjson"
}

func (e *MongoDocumentExport) Extension() string { return e.format }

// Close releases the server-side cursor.
func (e *MongoDocumentExport) Close() { e.cursor.Close(context.Background()) }

// MongoOpenExport validates an export and opens its cursor.
func MongoOpenExport(ctx context.Context, client *mongo.Client, dbName, collection string, spec MongoExportSpec) (*MongoDocumentExport, error) {
	if err := mongoReadable(ctx, client, dbName, collection); err != nil {
		return nil, err
	}
	format := spec.Format
	if format == "" {
		format = "ndjson"
	}
	if format != "ndjson" && format != "json" && format != "csv" {
		return nil, fmt.Errorf("export format is ndjson, json or csv")
	}
	limit := spec.Limit
	spec.Limit = 0
	pq, err := spec.MongoFindSpec.parse()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = mongoExportRows
	}
	if limit > mongoExportMaxRows {
		limit = mongoExportMaxRows
	}
	coll := client.Database(dbName).Collection(collection)
	// No Max time on the find itself: an export is expected to run for as
	// long as the collection is large, and the request's own deadline ends it.
	find := options.Find().SetSkip(pq.skip).SetLimit(limit)
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
	out := &MongoDocumentExport{Limit: limit, format: format, relaxed: spec.Relaxed}
	if count := mongoCount(ctx, coll, pq); count != nil && count.Exact {
		total := max(count.Value-pq.skip, 0)
		out.Total = &total
	}
	if format == "csv" {
		// A CSV needs its header before its first row, and documents do not
		// share one shape, so the keys are learnt in a pass of their own.
		if out.columns, err = mongoColumnUnion(ctx, coll, pq.filter, find, int(limit)); err != nil {
			return nil, err
		}
	}
	if out.cursor, err = coll.Find(ctx, pq.filter, find); err != nil {
		return nil, err
	}
	return out, nil
}

// Write streams the export and returns how many documents it wrote. An error
// here arrives after the response has started, so the caller cannot report
// it in a body; it has to break the response off instead.
func (e *MongoDocumentExport) Write(ctx context.Context, w io.Writer) (int64, error) {
	if e.format == "csv" {
		return e.writeCSV(ctx, w)
	}
	bw := bufio.NewWriterSize(w, 64<<10)
	array := e.format == "json"
	if array {
		bw.WriteString("[\n")
	}
	var count int64
	for e.cursor.Next(ctx) {
		line, err := bson.MarshalExtJSON(e.cursor.Current, !e.relaxed, false)
		if err != nil {
			return count, err
		}
		if array && count > 0 {
			bw.WriteString(",\n")
		}
		if _, err := bw.Write(line); err != nil {
			return count, err
		}
		if !array {
			bw.WriteByte('\n')
		}
		count++
	}
	if err := e.cursor.Err(); err != nil {
		return count, err
	}
	if array {
		bw.WriteString("\n]\n")
	}
	return count, bw.Flush()
}

func (e *MongoDocumentExport) writeCSV(ctx context.Context, w io.Writer) (int64, error) {
	cw := csv.NewWriter(w)
	if err := cw.Write(e.columns); err != nil {
		return 0, err
	}
	record := make([]string, len(e.columns))
	var count int64
	for e.cursor.Next(ctx) {
		for i, column := range e.columns {
			record[i] = ""
			if v, err := e.cursor.Current.LookupErr(column); err == nil {
				record[i] = mongoCSVCell(v, e.relaxed)
			}
		}
		if err := cw.Write(record); err != nil {
			return count, err
		}
		count++
	}
	if err := e.cursor.Err(); err != nil {
		return count, err
	}
	cw.Flush()
	return count, cw.Error()
}

// mongoCSVCell renders one value for a spreadsheet: the scalars a cell can
// hold as themselves, and everything else as Extended JSON so nothing is
// silently dropped.
func mongoCSVCell(v bson.RawValue, relaxed bool) string {
	switch v.Type {
	case bsontype.String:
		return v.StringValue()
	case bsontype.Int32:
		return strconv.FormatInt(int64(v.Int32()), 10)
	case bsontype.Int64:
		return strconv.FormatInt(v.Int64(), 10)
	case bsontype.Double:
		return strconv.FormatFloat(v.Double(), 'g', -1, 64)
	case bsontype.Decimal128:
		return v.Decimal128().String()
	case bsontype.Boolean:
		return strconv.FormatBool(v.Boolean())
	case bsontype.Null, bsontype.Undefined:
		return ""
	case bsontype.ObjectID:
		return v.ObjectID().Hex()
	case bsontype.DateTime:
		return v.Time().UTC().Format(time.RFC3339Nano)
	}
	return mongoValueJSON(v, !relaxed)
}
