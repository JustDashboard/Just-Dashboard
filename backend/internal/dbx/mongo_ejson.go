package dbx

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
)

// How a document crosses the HTTP boundary.
//
// A BSON document has more types than JSON has, and the order of its fields
// is part of it. The grid the first Mongo browser drew flattened both away —
// an ObjectId, a date and a Decimal128 all arrived as strings — so a document
// read, edited and saved came back with its types changed. Everything in the
// mongo_*.go files therefore moves documents as canonical Extended JSON v2:
// every value carries its type ({"$numberLong": "5"} is not 5), fields keep
// their order, and what is read can be written back byte for byte.
//
// Canonical text is exact and unpleasant to look at, so each document also
// travels in a second form meant only for drawing. That form is never parsed
// back into a write.

// MongoDoc is one document on the wire.
type MongoDoc struct {
	// ID is the canonical Extended JSON of the _id value on its own, which is
	// what the routes that address one document take back. It is empty when
	// the document has no _id, which only a projection can cause.
	ID string `json:"id"`
	// Canonical is the whole document as canonical Extended JSON. This is the
	// text an editor starts from and sends back.
	Canonical string `json:"canonical"`
	// Relaxed is the same document as relaxed Extended JSON, for display.
	Relaxed json.RawMessage `json:"relaxed"`
	// Size is the document's size in BSON bytes, which is what the 16 MiB
	// limit is measured in.
	Size int `json:"size"`
	// Digest is the SHA-256 of the document's BSON, in hex. A replace sends
	// it back to say which version of the document the edit was made on,
	// which costs 64 characters where sending the document back costs the
	// document.
	Digest string `json:"digest"`
}

// mongoDigest names one version of a document: any change to a value, a
// type or the order of the fields changes it.
func mongoDigest(raw bson.Raw) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// mongoSafeInteger is the largest integer a JavaScript number holds exactly.
const mongoSafeInteger = 1 << 53

func newMongoDoc(raw bson.Raw) (MongoDoc, error) {
	canonical, err := bson.MarshalExtJSON(raw, true, false)
	if err != nil {
		return MongoDoc{}, fmt.Errorf("a document could not be read: %w", err)
	}
	relaxed, err := mongoDisplayJSON(raw)
	if err != nil {
		return MongoDoc{}, fmt.Errorf("a document could not be read: %w", err)
	}
	doc := MongoDoc{Canonical: string(canonical), Relaxed: relaxed, Size: len(raw), Digest: mongoDigest(raw)}
	if id, err := raw.LookupErr("_id"); err == nil {
		doc.ID = mongoValueJSON(id, true)
	}
	return doc, nil
}

// mongoValueJSON renders one value as Extended JSON. The driver only marshals
// whole documents, so the value is wrapped in one and the wrapping cut away.
func mongoValueJSON(v bson.RawValue, canonical bool) string {
	out, err := bson.MarshalExtJSON(bson.D{{Key: "v", Value: v}}, canonical, false)
	if err != nil || len(out) < len(`{"v":}`) {
		return ""
	}
	return string(out[len(`{"v":`) : len(out)-1])
}

// mongoRelaxedJSON renders a whole document as relaxed Extended JSON text,
// for the places that show a document as a line rather than as a tree: a
// validator, a view's pipeline, a statement in the audit trail's place.
func mongoRelaxedJSON(doc any) string {
	out, err := bson.MarshalExtJSON(doc, false, false)
	if err != nil {
		return ""
	}
	return string(out)
}

// mongoDisplayJSON renders a document for display.
//
// It is relaxed Extended JSON with one deviation. The relaxed form writes a
// 64-bit integer as a bare number, and a browser reads a bare number into a
// double: 9007199254740993 becomes 9007199254740992 on screen, a wrong digit
// in an identifier nobody would think to doubt. An integer too large for a
// double is therefore left in its canonical {"$numberLong": "…"} form, which
// every Extended JSON reader accepts in a relaxed document.
func mongoDisplayJSON(raw bson.Raw) (json.RawMessage, error) {
	var buf bytes.Buffer
	if err := mongoDisplayDocument(&buf, raw, false, 0); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// mongoMaxDepth bounds every recursive walk over a document. The server
// refuses nesting deeper than 100 levels, so a deeper document is not one it
// produced.
const mongoMaxDepth = 200

func mongoDisplayDocument(buf *bytes.Buffer, doc bson.Raw, array bool, depth int) error {
	if depth > mongoMaxDepth {
		return fmt.Errorf("document is nested too deeply")
	}
	elems, err := doc.Elements()
	if err != nil {
		return err
	}
	if array {
		buf.WriteByte('[')
	} else {
		buf.WriteByte('{')
	}
	for i, e := range elems {
		if i > 0 {
			buf.WriteByte(',')
		}
		if !array {
			buf.Write(mongoJSONString(e.Key()))
			buf.WriteByte(':')
		}
		if err := mongoDisplayValue(buf, e.Value(), depth); err != nil {
			return err
		}
	}
	if array {
		buf.WriteByte(']')
	} else {
		buf.WriteByte('}')
	}
	return nil
}

func mongoDisplayValue(buf *bytes.Buffer, v bson.RawValue, depth int) error {
	switch v.Type {
	case bsontype.EmbeddedDocument:
		return mongoDisplayDocument(buf, v.Document(), false, depth+1)
	case bsontype.Array:
		return mongoDisplayDocument(buf, bson.Raw(v.Array()), true, depth+1)
	case bsontype.String:
		buf.Write(mongoJSONString(v.StringValue()))
	case bsontype.Int32:
		buf.WriteString(strconv.FormatInt(int64(v.Int32()), 10))
	case bsontype.Int64:
		n := v.Int64()
		if n > mongoSafeInteger || n < -mongoSafeInteger {
			buf.WriteString(`{"$numberLong":"` + strconv.FormatInt(n, 10) + `"}`)
		} else {
			buf.WriteString(strconv.FormatInt(n, 10))
		}
	case bsontype.Double:
		f := v.Double()
		switch {
		case math.IsNaN(f):
			buf.WriteString(`{"$numberDouble":"NaN"}`)
		case math.IsInf(f, 1):
			buf.WriteString(`{"$numberDouble":"Infinity"}`)
		case math.IsInf(f, -1):
			buf.WriteString(`{"$numberDouble":"-Infinity"}`)
		default:
			s := strconv.FormatFloat(f, 'g', -1, 64)
			// A double that happens to be whole still reads as a double.
			if !strings.ContainsAny(s, ".e") {
				s += ".0"
			}
			buf.WriteString(s)
		}
	case bsontype.Boolean:
		buf.WriteString(strconv.FormatBool(v.Boolean()))
	case bsontype.Null:
		buf.WriteString("null")
	default:
		s := mongoValueJSON(v, false)
		if s == "" {
			return fmt.Errorf("a %s value could not be rendered", v.Type)
		}
		buf.WriteString(s)
	}
	return nil
}

// mongoJSONString quotes a string for JSON without the HTML escaping the
// standard encoder applies: the text is data for an API client, and a filter
// on "<" should read as one.
func mongoJSONString(s string) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// --- Reading what an operator typed ---------------------------------------
//
// Input is Extended JSON, relaxed or canonical. The shell's spelling is
// accepted as well, because it is what every MongoDB example on the internet
// is written in and what an operator pastes: ObjectId("…"), ISODate("…"),
// unquoted keys, single quotes. mongoShellToExtJSON rewrites that into
// Extended JSON; it is tried only when the text is not already valid JSON, so
// valid Extended JSON is never reinterpreted.

// mongoParseDocument reads one document. what names the field in the error:
// "filter", "sort", "document".
func mongoParseDocument(what, text string) (bson.Raw, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("a %s is required", what)
	}
	ext, err := mongoExtJSON(text)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	if !strings.HasPrefix(ext, "{") {
		return nil, fmt.Errorf("%s must be a document, like { \"field\": value }", what)
	}
	var raw bson.Raw
	if err := bson.UnmarshalExtJSON([]byte(ext), false, &raw); err != nil {
		return nil, fmt.Errorf("%s is not valid Extended JSON: %w", what, err)
	}
	return raw, nil
}

// mongoParseOptionalDocument reads a document that may be left out: an absent
// filter matches everything, an absent projection returns every field.
func mongoParseOptionalDocument(what, text string) (bson.Raw, error) {
	if strings.TrimSpace(text) == "" {
		return mongoEmptyDocument, nil
	}
	return mongoParseDocument(what, text)
}

// mongoEmptyDocument is {} in BSON: a length and a terminator.
var mongoEmptyDocument = bson.Raw{5, 0, 0, 0, 0}

func mongoIsEmpty(doc bson.Raw) bool { return len(doc) <= len(mongoEmptyDocument) }

// MongoFilterIsEmpty reports whether a filter matches every document by
// being empty. A handler asks before connecting, because an update or delete
// of everything needs more than one of something.
func MongoFilterIsEmpty(text string) (bool, error) {
	doc, err := mongoParseOptionalDocument("filter", text)
	if err != nil {
		return false, err
	}
	return mongoIsEmpty(doc), nil
}

// mongoParseValue reads one value of any type, which is what an _id is.
func mongoParseValue(what, text string) (bson.RawValue, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return bson.RawValue{}, fmt.Errorf("%s is required", what)
	}
	ext, err := mongoExtJSON(text)
	if err != nil {
		return bson.RawValue{}, fmt.Errorf("%s: %w", what, err)
	}
	var raw bson.Raw
	if err := bson.UnmarshalExtJSON([]byte(`{"v":`+ext+`}`), false, &raw); err != nil {
		return bson.RawValue{}, fmt.Errorf("%s is not valid Extended JSON: %w", what, err)
	}
	return raw.Lookup("v"), nil
}

// mongoParseArray reads a list of documents: a pipeline, or several documents
// to insert.
func mongoParseArray(what, text string) ([]bson.Raw, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("a %s is required", what)
	}
	ext, err := mongoExtJSON(text)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	if !strings.HasPrefix(ext, "[") {
		return nil, fmt.Errorf("%s must be a list, like [ { … }, { … } ]", what)
	}
	var raw bson.Raw
	if err := bson.UnmarshalExtJSON([]byte(`{"v":`+ext+`}`), false, &raw); err != nil {
		return nil, fmt.Errorf("%s is not valid Extended JSON: %w", what, err)
	}
	values, err := raw.Lookup("v").Array().Values()
	if err != nil {
		return nil, fmt.Errorf("%s is not valid Extended JSON: %w", what, err)
	}
	out := make([]bson.Raw, 0, len(values))
	for i, v := range values {
		doc, ok := v.DocumentOK()
		if !ok {
			return nil, fmt.Errorf("%s entry %d is not a document", what, i+1)
		}
		out = append(out, bson.Raw(doc))
	}
	return out, nil
}

// mongoIsArrayText reports whether text is a list rather than a document,
// which is how an update says it is a pipeline and an insert that it carries
// several documents.
func mongoIsArrayText(text string) bool {
	ext, err := mongoExtJSON(strings.TrimSpace(text))
	return err == nil && strings.HasPrefix(ext, "[")
}

// mongoExtJSON returns text as Extended JSON, rewriting shell syntax when the
// text is not JSON already. The result is trimmed, so its first byte says
// whether it is a document, a list or a scalar.
func mongoExtJSON(text string) (string, error) {
	text = strings.TrimSpace(text)
	// json.Valid rather than the driver's own reader decides what counts as
	// JSON: the driver stops at the end of the first value and ignores
	// whatever follows it, so `{} garbage` would pass as an empty filter.
	if json.Valid([]byte(text)) {
		return text, nil
	}
	return mongoShellToExtJSON(text)
}

func mongoArray(docs []bson.Raw) bson.A {
	out := make(bson.A, 0, len(docs))
	for _, d := range docs {
		out = append(out, d)
	}
	return out
}

// jsonUnmarshalStrict decodes plain JSON into dst, refusing a field dst does
// not have.
func jsonUnmarshalStrict(text string, dst any) error {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}
