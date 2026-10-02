package dbx

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// The pre-parser's whole job is to produce Extended JSON that means what the
// shell text meant, so each case states the shell text and the Extended JSON
// it must become, compared as parsed BSON rather than as text.
func TestShellSyntaxBecomesExtendedJSON(t *testing.T) {
	for _, c := range []struct{ name, shell, ext string }{
		{"unquoted keys", `{ name: "Ann", age: 31 }`, `{"name":"Ann","age":31}`},
		{"operator keys", `{ age: { $gt: 30, $lte: 40 } }`, `{"age":{"$gt":30,"$lte":40}}`},
		{"dotted key in quotes", `{ "address.city": 'Cluj' }`, `{"address.city":"Cluj"}`},
		{"single quotes", `{ 'name': 'O\'Neil' }`, `{"name":"O'Neil"}`},
		{"double quote inside single", `{ a: 'say "hi"' }`, `{"a":"say \"hi\""}`},
		{"js escapes", `{ a: "\x41B\u{43}\n\t" }`, `{"a":"ABC\n\t"}`},
		{"surrogate pair", `{ a: "😀" }`, `{"a":"😀"}`},
		{"non-ascii key", `{ ţară: "România" }`, `{"ţară":"România"}`},
		{"numeric key", `{ 1: "a" }`, `{"1":"a"}`},
		{"trailing commas", `{ a: [1, 2, ], b: 1, }`, `{"a":[1,2],"b":1}`},
		{"line comment", "{ a: 1, // why\n b: 2 }", `{"a":1,"b":2}`},
		{"block comment", `{ a: /* inline */ 1 }`, `{"a":1}`},
		{"trailing semicolon", `{ a: 1 };`, `{"a":1}`},
		{"object id", `{ _id: ObjectId("65f1c0ffee0123456789abcd") }`, `{"_id":{"$oid":"65f1c0ffee0123456789abcd"}}`},
		{"object id upper case", `{ _id: ObjectId('65F1C0FFEE0123456789ABCD') }`, `{"_id":{"$oid":"65f1c0ffee0123456789abcd"}}`},
		{"iso date", `{ at: ISODate("2024-05-01T12:00:00Z") }`, `{"at":{"$date":"2024-05-01T12:00:00Z"}}`},
		{"iso date with millis and offset", `{ at: ISODate("2024-05-01T14:00:00.250+02:00") }`, `{"at":{"$date":"2024-05-01T12:00:00.25Z"}}`},
		{"iso date without zone is utc", `{ at: ISODate("2024-05-01T12:00:00") }`, `{"at":{"$date":"2024-05-01T12:00:00Z"}}`},
		{"date only", `{ at: ISODate("2024-05-01") }`, `{"at":{"$date":"2024-05-01T00:00:00Z"}}`},
		{"new Date string", `{ at: new Date("2024-05-01T12:00:00Z") }`, `{"at":{"$date":"2024-05-01T12:00:00Z"}}`},
		{"new Date millis", `{ at: new Date(1714564800000) }`, `{"at":{"$date":"2024-05-01T12:00:00Z"}}`},
		{"number long", `{ n: NumberLong(9007199254740993) }`, `{"n":{"$numberLong":"9007199254740993"}}`},
		{"number long quoted", `{ n: NumberLong("-5") }`, `{"n":{"$numberLong":"-5"}}`},
		{"long", `{ n: Long("5") }`, `{"n":{"$numberLong":"5"}}`},
		{"number int", `{ n: NumberInt(42) }`, `{"n":{"$numberInt":"42"}}`},
		{"int32", `{ n: Int32("42") }`, `{"n":{"$numberInt":"42"}}`},
		{"number decimal", `{ n: NumberDecimal("19.99") }`, `{"n":{"$numberDecimal":"19.99"}}`},
		{"decimal128", `{ n: Decimal128("1E+3") }`, `{"n":{"$numberDecimal":"1E+3"}}`},
		{"double", `{ n: Double(5) }`, `{"n":{"$numberDouble":"5"}}`},
		{"timestamp", `{ t: Timestamp(1700000000, 3) }`, `{"t":{"$timestamp":{"t":1700000000,"i":3}}}`},
		{"timestamp object", `{ t: Timestamp({ t: 1700000000, i: 3 }) }`, `{"t":{"$timestamp":{"t":1700000000,"i":3}}}`},
		{"uuid", `{ u: UUID("00112233-4455-6677-8899-aabbccddeeff") }`, `{"u":{"$binary":{"base64":"ABEiM0RVZneImaq7zN3u/w==","subType":"04"}}}`},
		{"bindata", `{ b: BinData(0, "aGVsbG8=") }`, `{"b":{"$binary":{"base64":"aGVsbG8=","subType":"00"}}}`},
		{"hexdata", `{ b: HexData(128, "68656c6c6f") }`, `{"b":{"$binary":{"base64":"aGVsbG8=","subType":"80"}}}`},
		{"min and max key", `{ a: MinKey(), b: MaxKey }`, `{"a":{"$minKey":1},"b":{"$maxKey":1}}`},
		{"regex literal", `{ name: /^an\/n/i }`, `{"name":{"$regularExpression":{"pattern":"^an/n","options":"i"}}}`},
		{"regex flags are sorted and g dropped", `{ name: /a[/]b/gxi }`, `{"name":{"$regularExpression":{"pattern":"a[/]b","options":"ix"}}}`},
		{"regexp constructor", `{ name: RegExp("^a", "i") }`, `{"name":{"$regularExpression":{"pattern":"^a","options":"i"}}}`},
		{"undefined", `{ a: undefined }`, `{"a":{"$undefined":true}}`},
		{"nan and infinity", `{ a: NaN, b: Infinity, c: -Infinity }`, `{"a":{"$numberDouble":"NaN"},"b":{"$numberDouble":"Infinity"},"c":{"$numberDouble":"-Infinity"}}`},
		{"js numbers", `{ a: .5, b: 5., c: +3, d: 0x1F, e: -1e3, f: -0 }`, `{"a":0.5,"b":5.0,"c":3,"d":31,"e":-1e3,"f":-0}`},
		{"keywords", `{ a: true, b: false, c: null }`, `{"a":true,"b":false,"c":null}`},
		{"constructors nest", `{ $or: [ { _id: ObjectId("65f1c0ffee0123456789abcd") }, { at: { $gte: ISODate("2024-01-01") } } ] }`,
			`{"$or":[{"_id":{"$oid":"65f1c0ffee0123456789abcd"}},{"at":{"$gte":{"$date":"2024-01-01T00:00:00Z"}}}]}`},
		{"pipeline", `[ { $match: { n: { $gt: NumberLong(1) } } }, { $group: { _id: '$k', n: { $sum: 1 } } } ]`,
			`[{"$match":{"n":{"$gt":{"$numberLong":"1"}}}},{"$group":{"_id":"$k","n":{"$sum":1}}}]`},
		{"bare value", `ObjectId("65f1c0ffee0123456789abcd")`, `{"$oid":"65f1c0ffee0123456789abcd"}`},
		{"bare string", `'abc'`, `"abc"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := mongoShellToExtJSON(c.shell)
			if err != nil {
				t.Fatalf("mongoShellToExtJSON(%s): %v", c.shell, err)
			}
			if !json.Valid([]byte(got)) {
				t.Fatalf("output is not JSON: %s", got)
			}
			if a, b := bsonOf(t, got), bsonOf(t, c.ext); !bytes.Equal(a, b) {
				t.Errorf("shell %s\n became %s\n   want %s", c.shell, got, c.ext)
			}
		})
	}
}

// bsonOf parses Extended JSON of any shape by wrapping it in a document.
func bsonOf(t *testing.T, ext string) []byte {
	t.Helper()
	var raw bson.Raw
	if err := bson.UnmarshalExtJSON([]byte(`{"v":`+ext+`}`), false, &raw); err != nil {
		t.Fatalf("not Extended JSON: %s: %v", ext, err)
	}
	return raw
}

// What the pre-parser refuses, and that the refusal says something an
// operator can act on: where, and what to write instead.
func TestShellSyntaxRefusals(t *testing.T) {
	for _, c := range []struct{ name, shell, want string }{
		{"unknown constructor", `{ a: Foo("x") }`, "Extended JSON"},
		{"variable", `{ a: someVar }`, "needs quotes"},
		{"expression", `{ a: 1 + 2 }`, "expected , or }"},
		{"function body", `{ $where: function() { return true } }`, "function"},
		{"template string", "{ a: `x` }", "template"},
		{"unclosed object", `{ a: 1`, "not closed"},
		{"unclosed array", `[1, 2`, "not closed"},
		{"unclosed string", `{ a: "x }`, "not closed"},
		{"missing colon", `{ a 1 }`, "expected :"},
		{"missing value", `{ a: }`, "value was expected"},
		{"dotted key unquoted", `{ a.b: 1 }`, "expected :"},
		{"bad object id", `{ _id: ObjectId("xyz") }`, "24 hexadecimal"},
		{"object id number", `{ _id: ObjectId(5) }`, "24 hexadecimal"},
		{"bad date", `{ at: ISODate("yesterday") }`, "not a date"},
		{"long overflow", `{ n: NumberLong("99999999999999999999") }`, "64-bit"},
		{"long fraction", `{ n: NumberLong(1.5) }`, "64-bit"},
		{"int overflow", `{ n: NumberInt(4294967296) }`, "32-bit"},
		{"bad decimal", `{ n: NumberDecimal("abc") }`, "not a decimal"},
		{"bad uuid", `{ u: UUID("1234") }`, "not a UUID"},
		{"bad base64", `{ b: BinData(0, "!!") }`, "could not be decoded"},
		{"bad regex flag", `{ a: /x/q }`, "flag"},
		{"leading zero", `{ a: 007 }`, "leading zeros"},
		{"number then letters", `{ a: 12abc }`, "not a number"},
		{"trailing garbage", `{ a: 1 } extra`, "after the end"},
		{"two values", `{ a: 1 } { b: 2 }`, "after the end"},
		{"empty", `   `, "nothing to read"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := mongoShellToExtJSON(c.shell)
			if err == nil {
				t.Fatalf("%s was accepted as %s", c.shell, got)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error for %s = %q, want it to mention %q", c.shell, err, c.want)
			}
		})
	}
}

func TestShellSyntaxErrorsCarryAPosition(t *testing.T) {
	_, err := mongoShellToExtJSON("{\n  a: 1,\n  b: oops\n}")
	if err == nil || !strings.Contains(err.Error(), "line 3, column 6") {
		t.Errorf("error = %v, want it to point at line 3, column 6", err)
	}
}

// The constructors that take no argument mean "now" and "a new one", so their
// output cannot be compared with a constant.
func TestShellConstructorsWithoutArguments(t *testing.T) {
	before := time.Now().Add(-time.Second)
	raw, err := mongoParseDocument("document", `{ _id: ObjectId(), at: new Date(), also: ISODate() }`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := raw.Lookup("_id").ObjectIDOK(); !ok {
		t.Errorf("ObjectId() did not make an ObjectId: %s", raw)
	}
	for _, key := range []string{"at", "also"} {
		at, ok := raw.Lookup(key).TimeOK()
		if !ok || at.Before(before) || at.After(time.Now().Add(time.Second)) {
			t.Errorf("%s = %v, want now", key, at)
		}
	}
}

// Valid Extended JSON never goes through the pre-parser, and text that is not
// JSON is never silently truncated to its first value.
func TestExtendedJSONIsNotReinterpreted(t *testing.T) {
	doc, err := mongoParseDocument("filter", `{"a":{"$numberLong":"5"},"b":{"$date":{"$numberLong":"0"}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := doc.Lookup("a").Int64OK(); !ok || n != 5 {
		t.Errorf("a = %v, want int64 5", doc.Lookup("a"))
	}
	if _, ok := doc.Lookup("b").DateTimeOK(); !ok {
		t.Errorf("b = %v, want a date", doc.Lookup("b"))
	}
	for _, text := range []string{`{"a":1} trailing`, `{"a":1}{"b":2}`} {
		if got, err := mongoParseDocument("filter", text); err == nil {
			t.Errorf("%s was accepted as %s", text, got)
		}
	}
	if _, err := mongoParseDocument("filter", `[1,2]`); err == nil {
		t.Error("a list was accepted as a filter")
	}
	if _, err := mongoParseArray("pipeline", `{"a":1}`); err == nil {
		t.Error("a document was accepted as a pipeline")
	}
	if _, err := mongoParseArray("pipeline", `[{"$match":{}}, 5]`); err == nil {
		t.Error("a pipeline holding a number was accepted")
	}
}

// Every BSON type survives being rendered as canonical Extended JSON and read
// back, byte for byte. This is the property the document editor rests on.
func TestCanonicalRoundTripIsByteIdentical(t *testing.T) {
	dec, _ := primitive.ParseDecimal128("1.50")
	doc := bson.D{
		{Key: "_id", Value: primitive.NewObjectID()},
		{Key: "double", Value: 1.0},
		{Key: "nan", Value: math.NaN()},
		{Key: "inf", Value: math.Inf(-1)},
		{Key: "negZero", Value: math.Copysign(0, -1)},
		{Key: "string", Value: "<&>   \x00 ţ"},
		{Key: "int32", Value: int32(7)},
		{Key: "int64Small", Value: int64(5)},
		{Key: "int64Large", Value: int64(math.MaxInt64)},
		{Key: "decimal", Value: dec},
		{Key: "date", Value: primitive.NewDateTimeFromTime(time.Unix(1700000000, 123e6))},
		{Key: "dateBeforeEpoch", Value: primitive.DateTime(-1000)},
		{Key: "dateFarFuture", Value: primitive.DateTime(253402300800000)},
		{Key: "binary", Value: primitive.Binary{Subtype: 0, Data: []byte{1, 2, 3}}},
		{Key: "binaryOld", Value: primitive.Binary{Subtype: 2, Data: []byte{3, 0, 0, 0, 1, 2, 3}}},
		{Key: "uuid", Value: primitive.Binary{Subtype: 4, Data: bytes.Repeat([]byte{9}, 16)}},
		{Key: "regex", Value: primitive.Regex{Pattern: "^a/b", Options: "im"}},
		{Key: "timestamp", Value: primitive.Timestamp{T: 5, I: 6}},
		{Key: "minKey", Value: primitive.MinKey{}},
		{Key: "maxKey", Value: primitive.MaxKey{}},
		{Key: "undefined", Value: primitive.Undefined{}},
		{Key: "null", Value: nil},
		{Key: "javascript", Value: primitive.JavaScript("x = 1")},
		{Key: "codeWithScope", Value: primitive.CodeWithScope{Code: "x", Scope: bson.D{{Key: "a", Value: int32(1)}}}},
		{Key: "symbol", Value: primitive.Symbol("sym")},
		{Key: "dbPointer", Value: primitive.DBPointer{DB: "a.b", Pointer: primitive.NewObjectID()}},
		{Key: "bool", Value: true},
		{Key: "array", Value: bson.A{int32(1), "two", bson.D{{Key: "z", Value: int64(3)}}, bson.A{}}},
		{Key: "nested", Value: bson.D{{Key: "z", Value: 1.5}, {Key: "a", Value: int32(1)}, {Key: "", Value: "empty key"}}},
		{Key: "10", Value: "numeric keys keep their order"},
		{Key: "2", Value: "after 10"},
		{Key: "$dollar", Value: bson.D{{Key: "a.b", Value: 1.0}}},
	}
	original, err := bson.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := newMongoDoc(original)
	if err != nil {
		t.Fatal(err)
	}
	back, err := mongoParseDocument("document", wire.Canonical)
	if err != nil {
		t.Fatalf("canonical form did not parse: %v\n%s", err, wire.Canonical)
	}
	if !bytes.Equal(original, back) {
		t.Errorf("round trip changed the document\n was %x\n now %x", original, back)
	}
	if wire.Size != len(original) {
		t.Errorf("size = %d, want %d", wire.Size, len(original))
	}
	id, err := mongoParseValue("id", wire.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := bson.Raw(original).Lookup("_id"); id.Type != want.Type || !bytes.Equal(id.Value, want.Value) {
		t.Errorf("id %s did not round-trip", wire.ID)
	}
}

// The display form is for a browser, which reads every number into a double.
// Whatever a double cannot hold must not be written as a bare number.
func TestDisplayFormIsSafeForABrowser(t *testing.T) {
	doc := bson.D{
		{Key: "safe", Value: int64(1) << 53},
		{Key: "unsafe", Value: int64(1)<<53 + 1},
		{Key: "negative", Value: -(int64(1)<<53 + 1)},
		{Key: "small", Value: int32(7)},
		{Key: "whole", Value: 5.0},
		{Key: "nan", Value: math.NaN()},
		{Key: "list", Value: bson.A{int64(math.MaxInt64), "<b>"}},
		{Key: "when", Value: primitive.NewDateTimeFromTime(time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC))},
	}
	raw, _ := bson.Marshal(doc)
	out, err := mongoDisplayJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"safe":9007199254740992,"unsafe":{"$numberLong":"9007199254740993"},` +
		`"negative":{"$numberLong":"-9007199254740993"},"small":7,"whole":5.0,` +
		`"nan":{"$numberDouble":"NaN"},"list":[{"$numberLong":"9223372036854775807"},"<b>"],` +
		`"when":{"$date":"2024-05-01T12:00:00Z"}}`
	if string(out) != want {
		t.Errorf("display form\n got %s\nwant %s", out, want)
	}
	// It is still Extended JSON: reading it back gives the same values, with
	// only the integer widths relaxed.
	var back bson.D
	if err := bson.UnmarshalExtJSON(out, false, &back); err != nil {
		t.Fatalf("display form is not Extended JSON: %v", err)
	}
}

func TestFilterEmptiness(t *testing.T) {
	for text, want := range map[string]bool{
		"": true, "  ": true, "{}": true, "{ }": true, "{ /* nothing */ }": true,
		`{"a":1}`: false, `{ a: 1 }`: false, `{"$comment":"x"}`: false,
	} {
		got, err := MongoFilterIsEmpty(text)
		if err != nil || got != want {
			t.Errorf("MongoFilterIsEmpty(%q) = %v, %v; want %v", text, got, err, want)
		}
	}
	if _, err := MongoFilterIsEmpty("{"); err == nil {
		t.Error("a broken filter was not an error")
	}
}
