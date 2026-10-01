package dbx

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestValueKind(t *testing.T) {
	cases := []struct {
		driver Driver
		name   string
		want   string
	}{
		// What drivers report for a result column.
		{DriverPostgres, "INT8", KindInteger},
		{DriverPostgres, "_INT4", KindArray},
		{DriverPostgres, "BPCHAR", KindText},
		{DriverPostgres, "TIMESTAMPTZ", KindDateTime},
		{DriverPostgres, "JSONB", KindJSON},
		{DriverPostgres, "BYTEA", KindBinary},
		{DriverPostgres, "NUMERIC", KindDecimal},
		{DriverPostgres, "BOOL", KindBoolean},
		{DriverMySQL, "UNSIGNED BIGINT", KindInteger},
		{DriverMySQL, "VARBINARY", KindBinary},
		{DriverMySQL, "MEDIUMBLOB", KindBinary},
		{DriverMySQL, "BIT", KindBinary},
		{DriverMSSQL, "BIT", KindBoolean},
		{DriverMSSQL, "TIMESTAMP", KindBinary},
		{DriverMSSQL, "UNIQUEIDENTIFIER", KindUUID},
		// What catalogues report for a table column.
		{DriverPostgres, "character varying(255)", KindText},
		{DriverPostgres, "timestamp(3) with time zone", KindDateTime},
		{DriverPostgres, "double precision", KindFloat},
		{DriverPostgres, "integer[]", KindArray},
		{DriverPostgres, "interval", KindInterval},
		{DriverPostgres, "point", KindOther},
		{DriverMySQL, "int unsigned", KindInteger},
		{DriverMySQL, "decimal(10,2)", KindDecimal},
		{DriverMySQL, "enum('a','b')", KindText},
		{DriverOracle, "BINARY_DOUBLE", KindFloat},
		{DriverOracle, "RAW(16)", KindBinary},
		{DriverOracle, "TIMESTAMP(6) WITH TIME ZONE", KindDateTime},
		{DriverOracle, "NUMBER(10,0)", KindInteger},
		{DriverOracle, "NUMBER(10,2)", KindDecimal},
		{DriverOracle, "NUMBER", KindDecimal},
		{DriverOracle, "XMLTYPE", KindText},
		{DriverOracle, "BOOLEAN", KindBoolean},
		{DriverOracle, "JSON", KindJSON},
		// go-ora's names for a result column, which are its wire types.
		{DriverOracle, "IBDouble", KindFloat},
		{DriverOracle, "IBFloat", KindFloat},
		{DriverOracle, "LongRaw", KindBinary},
		{DriverOracle, "LongVarChar", KindText},
		{DriverOracle, "OCIClobLocator", KindText},
		{DriverOracle, "OCIBlobLocator", KindOther},
		{DriverOracle, "OCIFileLocator", KindBinary},
		{DriverOracle, "TimeStampTZ_DTY", KindDateTime},
		{DriverOracle, "IntervalDS_DTY", KindInterval},
		{DriverOracle, "NCHAR", KindText},
		{DriverMySQL, "LONGBLOB", KindBinary},
		{DriverPostgres, "NUMBER(10,0)", KindDecimal},
		{DriverClickHouse, "Nullable(DateTime64(3))", KindDateTime},
		{DriverClickHouse, "LowCardinality(Nullable(String))", KindText},
		{DriverClickHouse, "UInt64", KindInteger},
		{DriverClickHouse, "Array(String)", KindArray},
		{DriverSQLite, "", KindOther},
	}
	for _, c := range cases {
		if got := ValueKind(c.driver, c.name); got != c.want {
			t.Errorf("ValueKind(%s, %q) = %q, want %q", c.driver, c.name, got, c.want)
		}
	}
}

// A binary column is hex whatever its bytes spell, and a text column is text.
// Deciding by content made one column both.
func TestEncodeCellDecidesByColumnType(t *testing.T) {
	if got, _ := encodeCell(DriverPostgres, KindBinary, "BYTEA", []byte("hello"), 0); got != `\x68656c6c6f` {
		t.Errorf("readable bytes in a binary column = %v, want hex", got)
	}
	if got, _ := encodeCell(DriverMySQL, KindText, "VARCHAR", []byte("hello"), 0); got != "hello" {
		t.Errorf("a MySQL text column = %v, want text", got)
	}
	if got, _ := encodeCell(DriverSQLite, KindOther, "", []byte("hello"), 0); got != `\x68656c6c6f` {
		t.Errorf("a SQLite blob = %v, want hex", got)
	}
	// A value past the preview is flagged with its real size.
	big := bytes.Repeat([]byte{0xab}, maxHexPreview+10)
	got, size := encodeCell(DriverPostgres, KindBinary, "BYTEA", big, 0)
	if size != int64(len(big)) || !strings.HasSuffix(got.(string), "bytes)") {
		t.Errorf("a long blob = %q size %d", got, size)
	}
	// SQL Server hands a uniqueidentifier over byte-swapped.
	wire := []byte{0x33, 0x22, 0x11, 0x00, 0x55, 0x44, 0x77, 0x66, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	if got, _ := encodeCell(DriverMSSQL, KindUUID, "UNIQUEIDENTIFIER", wire, 0); got != "00112233-4455-6677-8899-AABBCCDDEEFF" {
		t.Errorf("uniqueidentifier = %v", got)
	}
}

// Two values a driver hands over in a form no reader should be shown: an
// Oracle BOOLEAN as the number 1 or 0, and a SQL Server uniqueidentifier as
// sixteen bytes in wire order.
func TestCellsTheDriverDoesNotSpell(t *testing.T) {
	for _, v := range []any{int64(1), "1", float64(1)} {
		if got, size := encodeCell(DriverOracle, KindBoolean, "NUMBER", v, 0); got != true || size != 0 {
			t.Errorf("an Oracle boolean %#v is shown as %#v", v, got)
		}
	}
	if got, _ := encodeCell(DriverOracle, KindBoolean, "NUMBER", "0", 0); got != false {
		t.Errorf("an Oracle false is shown as %#v", got)
	}
	// Only where the catalogue said boolean, and only on Oracle.
	if got, _ := encodeCell(DriverOracle, KindDecimal, "NUMBER", "1", 0); got != "1" {
		t.Errorf("an Oracle number is shown as %#v", got)
	}
	if got, _ := encodeCell(DriverMySQL, KindBoolean, "TINYINT", int64(1), 0); got != "1" {
		t.Errorf("a MySQL integer is shown as %#v", got)
	}
	flag := &CellValue{Kind: KindBoolean}
	if err := flag.fill(DriverOracle, "0"); err != nil || flag.Encoding != "json" || flag.Value != false {
		t.Errorf("an Oracle boolean cell = %+v %v", flag, err)
	}

	wire := []byte{0x33, 0x22, 0x11, 0x00, 0x55, 0x44, 0x77, 0x66, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	const guid = "00112233-4455-6677-8899-AABBCCDDEEFF"
	if got, _ := encodeCell(DriverMSSQL, KindUUID, "UNIQUEIDENTIFIER", wire, 0); got != guid {
		t.Errorf("a uniqueidentifier is shown as %#v", got)
	}
	// The whole cell is the same text, not the bytes it travelled as.
	id := &CellValue{Kind: KindUUID, Type: "uniqueidentifier"}
	if err := id.fill(DriverMSSQL, wire); err != nil || id.Encoding != "text" || id.Value != guid || id.Size != 16 {
		t.Errorf("a uniqueidentifier cell = %+v %v", id, err)
	}
	raw := &CellValue{Kind: KindBinary, Type: "varbinary(16)"}
	if err := raw.fill(DriverMSSQL, wire); err != nil || raw.Encoding != "base64" {
		t.Errorf("sixteen bytes of a binary column = %+v %v", raw, err)
	}
}

func TestClipStringCutsOnACharacter(t *testing.T) {
	text := strings.Repeat("é", 10) // two bytes each
	got, size := clipString(text, 5)
	if got != "éé" || size != 20 {
		t.Errorf("clipString = %q (%d), want two whole characters and the full size", got, size)
	}
	if got, size := clipString(text, 0); got != text || size != 0 {
		t.Errorf("no limit cut the value: %q %d", got, size)
	}
	if got, size := clipString("short", 100); got != "short" || size != 0 {
		t.Errorf("a short value was flagged: %q %d", got, size)
	}
}

func TestCellArgument(t *testing.T) {
	binary := Column{Name: "data", Type: "bytea"}
	text := Column{Name: "note", Type: "text"}
	doc := Column{Name: "doc", Type: "jsonb"}

	for _, c := range []struct {
		name   string
		column Column
		in     any
		want   any
	}{
		{"hex envelope", text, map[string]any{"$hex": "00ff"}, []byte{0x00, 0xff}},
		{"base64 envelope", text, map[string]any{"$base64": "AP8="}, []byte{0x00, 0xff}},
		{"the grid's hex form, binary column", binary, `\x00ff`, []byte{0x00, 0xff}},
		{"the same text in a text column", text, `\x00ff`, `\x00ff`},
		{"empty binary", binary, `\x`, []byte{}},
		{"object", doc, map[string]any{"a": json.Number("9007199254740993")}, `{"a":9007199254740993}`},
		{"array", doc, []any{json.Number("1"), "two"}, `[1,"two"]`},
		{"number", text, json.Number("12.50"), json.Number("12.50")},
		{"null", text, nil, nil},
	} {
		got, err := cellArgument(c.column, DriverPostgres, c.in)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if b, ok := c.want.([]byte); ok {
			if gb, ok := got.([]byte); !ok || !bytes.Equal(gb, b) {
				t.Errorf("%s = %#v, want %#v", c.name, got, c.want)
			}
			continue
		}
		if got != c.want {
			t.Errorf("%s = %#v, want %#v", c.name, got, c.want)
		}
	}
	for _, bad := range []any{map[string]any{"$hex": "zz"}, map[string]any{"$base64": "!!"}} {
		if _, err := cellArgument(text, DriverPostgres, bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}
