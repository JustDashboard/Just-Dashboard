package dbx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Importing documents from a stream.
//
// The inline route hands MongoImport a string it already holds. An upload is
// not held: it is read a batch of documents at a time, and what a collection
// is asked to take is never more than one batch.
//
// Replacing a collection's contents is the one place this can do better than
// the SQL engines' "delete, then insert, in a transaction", because a
// standalone server has no transaction to offer. The documents are loaded into
// a collection of their own first and swapped in with $out, which replaces the
// target in one step and keeps its indexes: until the whole file has arrived
// and been accepted, the collection is exactly as it was.

// mongoUploadBatch is how many documents one insert carries.
const mongoUploadBatch = 500

// ValidateMongoImport reads data as an import would and reports the first
// thing wrong with it, without touching the server. It is what lets a caller
// refuse a file before it empties the collection the file was meant for.
func ValidateMongoImport(format string, data string) error {
	spec := ImportSpec{Table: "validation", Format: mongoInlineFormat(format, data)}
	if err := normaliseImportSpec(&spec); err != nil {
		return err
	}
	text, _, err := decodeText(strings.NewReader(data), "")
	if err != nil {
		return err
	}
	reader, err := newRecordReader(text, spec)
	if err != nil {
		return err
	}
	source := &mongoDocuments{reader: reader, spec: spec}
	count := 0
	for {
		_, rec, err := source.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if rec.err != nil {
			return fmt.Errorf("line %d is not a document: %w", rec.line, rec.err)
		}
		count++
	}
	if count == 0 {
		return fmt.Errorf("no documents found to import")
	}
	return nil
}

// mongoInlineFormat reads the inline route's format, which is "csv" or
// anything else — an array, or one document per line.
func mongoInlineFormat(format, data string) ImportFormat {
	if strings.EqualFold(format, "csv") {
		return ImportFormatCSV
	}
	if strings.HasPrefix(strings.TrimSpace(data), "[") {
		return ImportFormatJSON
	}
	return ImportFormatNDJSON
}

// MongoEmptyCollection removes every document and keeps the collection: its
// indexes, its validator, its options. Dropping it instead is what an import
// that replaces used to do, and the documents came back into a collection
// that no longer refused duplicates.
func MongoEmptyCollection(ctx context.Context, client *mongo.Client, database, collection string) error {
	// An import that replaces empties the collection before it writes one
	// document. The collections that hold what accounts sign in with are
	// refused here, first: refused only at the write, they would already be
	// empty.
	if err := mongoNamespace(database, collection); err != nil {
		return err
	}
	_, err := client.Database(database).Collection(collection).DeleteMany(ctx, bson.D{})
	return err
}

// mongoDocuments turns the records of a file into documents.
type mongoDocuments struct {
	reader recordReader
	spec   ImportSpec
	header []string
	primed bool
}

// next returns the next document, with the record it came from. A record that
// is not a document comes back with its error set and a nil document.
func (m *mongoDocuments) next() (bson.D, importRecord, error) {
	if !m.primed {
		m.primed = true
		if m.spec.Format.delimited() && (m.spec.Header == nil || *m.spec.Header) {
			header, err := m.reader.next()
			if err == io.EOF {
				return nil, importRecord{}, io.EOF
			}
			if err != nil {
				return nil, importRecord{}, fmt.Errorf("could not read the header row: %w", err)
			}
			if header.err != nil {
				return nil, importRecord{}, fmt.Errorf("could not read the header row: %w", header.err)
			}
			for _, v := range header.values {
				m.header = append(m.header, strings.TrimSpace(strings.TrimPrefix(v.text, "\ufeff")))
			}
		}
	}
	for {
		rec, err := m.reader.next()
		if err != nil {
			return nil, rec, err
		}
		if rec.err != nil {
			return nil, rec, nil
		}
		if !m.spec.Format.delimited() {
			if _, marker := isExportMarker(rec); marker {
				continue
			}
			doc := bson.D{}
			if err := bson.UnmarshalExtJSON(rec.raw, false, &doc); err != nil {
				rec.err = fmt.Errorf("not a document: %w", err)
				return nil, rec, nil
			}
			return doc, rec, nil
		}
		// Delimited values arrive as strings and are left that way: guessing
		// that "007" is the number 7, or that "true" is a boolean, silently
		// changes data on the way in, and a document store has no column type
		// to appeal to for the right answer.
		doc := bson.D{}
		for i, v := range rec.values {
			name := "column_" + fmt.Sprint(i+1)
			if i < len(m.header) {
				name = m.header[i]
			} else if len(m.header) > 0 {
				// More fields than the header names: there is nowhere to put
				// the rest.
				break
			}
			if name == "" {
				continue
			}
			if m.spec.NullToken != nil && !v.quoted && v.text == *m.spec.NullToken {
				doc = append(doc, bson.E{Key: name, Value: nil})
				continue
			}
			doc = append(doc, bson.E{Key: name, Value: v.text})
		}
		return doc, rec, nil
	}
}

// MongoImportStream loads a stream of documents into a collection.
//
// The modes are insert and replace. A dry run reads the start of the file and
// reports the fields it found; nothing is written.
func MongoImportStream(ctx context.Context, client *mongo.Client, database, collection string, r io.Reader, spec ImportSpec) (*ImportReport, error) {
	if err := mongoNamespace(database, collection); err != nil {
		return nil, err
	}
	spec.Table = collection
	if err := normaliseImportSpec(&spec); err != nil {
		return nil, err
	}
	switch {
	case spec.Mode == ImportModeUpsert:
		return nil, fmt.Errorf("a document import adds or replaces; it has no upsert")
	case spec.Create != nil:
		return nil, fmt.Errorf("a collection is created by its first document; there is nothing to ask for")
	case len(spec.Mapping) > 0 || len(spec.Columns) > 0:
		return nil, fmt.Errorf("a document keeps the field names the file gives it; there is no column mapping")
	}
	text, encoding, err := decodeText(r, spec.Encoding)
	if err != nil {
		return nil, err
	}
	reader, err := newRecordReader(text, spec)
	if err != nil {
		return nil, err
	}
	report := &ImportReport{
		DryRun: spec.DryRun, Schema: database, Table: collection, Format: spec.Format, Encoding: encoding,
		Mode: spec.Mode, Columns: []ImportColumn{}, Errors: []ImportRowError{}, Warnings: []string{},
		Statement: fmt.Sprintf("db.%s.insertMany(…)", collection),
		// Only the swap is all-or-nothing. Documents added to a collection
		// that is kept are added as they arrive.
		Atomic: spec.Mode == ImportModeReplace,
	}
	source := &mongoDocuments{reader: reader, spec: spec}
	if spec.DryRun {
		return mongoImportPreview(source, report)
	}

	db := client.Database(database)
	target := db.Collection(collection)
	staging := (*mongo.Collection)(nil)
	if spec.Mode == ImportModeReplace {
		suffix := make([]byte, 6)
		if _, err := rand.Read(suffix); err != nil {
			return nil, err
		}
		staging = db.Collection("jd_import_" + hex.EncodeToString(suffix))
		// Whatever happens, the staging collection does not outlive the call.
		defer staging.Drop(context.WithoutCancel(ctx))
		target = staging
	}

	// pending is a document read and not yet inserted, with where it came from.
	type pending struct {
		row  int
		line int
	}
	var (
		batch   []any
		origins []pending
		row     int
	)
	bad := func(at pending, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if !spec.SkipBadRows {
			if spec.Mode == ImportModeInsert && report.Inserted > 0 {
				return fmt.Errorf("document %d (line %d) was refused: %v — the %d before it are in the collection; "+
					"a standalone server has no transaction to take them back with", at.row, at.line, err, report.Inserted)
			}
			return &importRowFailure{row: at.row, line: at.line, err: err}
		}
		report.Skipped++
		if len(report.Errors) < spec.MaxErrors {
			report.Errors = append(report.Errors, ImportRowError{Row: at.row, Line: at.line, Message: err.Error()})
		} else {
			report.ErrorsTruncated = true
		}
		return nil
	}
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		docs, from := batch, origins
		batch, origins = nil, nil
		// Unordered when bad documents are being skipped, so one refused
		// document does not stop the ones after it in the batch. Ordered
		// otherwise, so that everything before the refused one is known to
		// have gone in and nothing after it has.
		_, err := target.InsertMany(ctx, docs, options.InsertMany().SetOrdered(!spec.SkipBadRows))
		if err == nil {
			report.Inserted += len(docs)
			return nil
		}
		var bulk mongo.BulkWriteException
		if !errors.As(err, &bulk) || len(bulk.WriteErrors) == 0 {
			return err
		}
		if !spec.SkipBadRows {
			first := bulk.WriteErrors[0]
			if first.Index < 0 || first.Index >= len(from) {
				return err
			}
			report.Inserted += first.Index
			return bad(from[first.Index], errors.New(first.Message))
		}
		report.Inserted += len(docs) - len(bulk.WriteErrors)
		for _, we := range bulk.WriteErrors {
			if we.Index < 0 || we.Index >= len(from) {
				return err
			}
			if berr := bad(from[we.Index], errors.New(we.Message)); berr != nil {
				return berr
			}
		}
		return nil
	}
	for {
		doc, rec, err := source.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		row++
		report.RowsRead = row
		at := pending{row: row, line: rec.line}
		if rec.err != nil {
			if err := bad(at, rec.err); err != nil {
				return nil, err
			}
			continue
		}
		batch = append(batch, doc)
		origins = append(origins, at)
		if len(batch) >= mongoUploadBatch {
			if err := flush(); err != nil {
				return nil, err
			}
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if report.RowsRead == 0 {
		return nil, fmt.Errorf("no documents found to import")
	}
	if staging != nil {
		// $out replaces the collection in one step and carries its indexes
		// over. A document the collection's own rules refuse — a duplicate
		// under a unique index, one its validator rejects — fails the swap,
		// and the collection is as it was.
		cur, err := staging.Aggregate(ctx, mongo.Pipeline{{{Key: "$out", Value: collection}}})
		if err != nil {
			return nil, fmt.Errorf("the collection was not replaced: %w", err)
		}
		cur.Close(ctx)
	}
	return report, nil
}

// mongoImportPreview reads the start of a file and reports its fields.
func mongoImportPreview(source *mongoDocuments, report *ImportReport) (*ImportReport, error) {
	fields := []string{}
	seen := map[string]int{}
	profiles := []*columnProfile{}
	var docs []bson.D
	for len(docs) < importSampleRows {
		doc, rec, err := source.next()
		if err == io.EOF || errors.Is(err, errTruncatedInput) {
			break
		}
		if err != nil {
			return nil, err
		}
		if rec.err != nil {
			continue
		}
		docs = append(docs, doc)
		for _, e := range doc {
			at, ok := seen[e.Key]
			if !ok {
				at = len(fields)
				seen[e.Key] = at
				fields = append(fields, e.Key)
				profiles = append(profiles, newColumnProfile())
			}
			if e.Value != nil {
				profiles[at].add(importValue{text: mongoPreviewText(e.Value)}, false, rec.line)
			}
		}
	}
	report.RowsRead = len(docs)
	report.Preview = &ImportPreview{Columns: fields, Rows: [][]*string{}}
	for i, name := range fields {
		profiles[i].settle()
		examples := profiles[i].examples
		if examples == nil {
			examples = []string{}
		}
		report.Columns = append(report.Columns, ImportColumn{
			Source: name, Index: i, Target: name, Inferred: profiles[i].kind, Examples: examples,
		})
	}
	for _, doc := range docs {
		if len(report.Preview.Rows) >= importPreviewRows {
			break
		}
		row := make([]*string, len(fields))
		for _, e := range doc {
			if e.Value == nil {
				continue
			}
			text := clipText(mongoPreviewText(e.Value), 200)
			row[seen[e.Key]] = &text
		}
		report.Preview.Rows = append(report.Preview.Rows, row)
	}
	return report, nil
}

// mongoPreviewText renders one field's value for a preview: a string as it
// is, anything else as the Extended JSON it would be written back out as.
func mongoPreviewText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	ext, err := bson.MarshalExtJSON(bson.D{{Key: "v", Value: v}}, false, false)
	if err != nil {
		return fmt.Sprint(v)
	}
	text := strings.TrimSuffix(strings.TrimPrefix(string(ext), `{"v":`), "}")
	return strings.Trim(text, `"`)
}
