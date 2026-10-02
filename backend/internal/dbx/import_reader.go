package dbx

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Reading an import file.
//
// Everything here is a stream. An import is the one request whose body is as
// large as whatever the operator has on disk, so nothing reads it whole: each
// reader hands back one record at a time, and the only thing kept is the line
// it started on — because "row 48211 failed" is an answer and "a row failed"
// is not.

// ImportFormat is a file format an import can read.
type ImportFormat string

const (
	ImportFormatCSV    ImportFormat = "csv"
	ImportFormatTSV    ImportFormat = "tsv"
	ImportFormatJSON   ImportFormat = "json"
	ImportFormatNDJSON ImportFormat = "ndjson"
)

func (f ImportFormat) Valid() bool {
	switch f {
	case ImportFormatCSV, ImportFormatTSV, ImportFormatJSON, ImportFormatNDJSON:
		return true
	}
	return false
}

func (f ImportFormat) delimited() bool { return f == ImportFormatCSV || f == ImportFormatTSV }

// ImportFormats lists every format in the order the UI offers them.
func ImportFormats() []ImportFormat {
	return []ImportFormat{ImportFormatCSV, ImportFormatTSV, ImportFormatJSON, ImportFormatNDJSON}
}

// The encodings an import can be told a file is in.
const (
	EncodingUTF8   = "utf-8"
	EncodingUTF16  = "utf-16"
	EncodingLatin1 = "latin-1"
)

// normaliseEncoding accepts the spellings people use for the three encodings.
func normaliseEncoding(name string) (string, error) {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), "_", "-")) {
	case "", "utf-8", "utf8":
		return EncodingUTF8, nil
	case "utf-16", "utf16", "utf-16le", "utf-16be", "ucs-2":
		return EncodingUTF16, nil
	case "latin-1", "latin1", "iso-8859-1", "windows-1252", "cp1252":
		return EncodingLatin1, nil
	}
	return "", fmt.Errorf("encoding %q is not one of utf-8, utf-16, latin-1", name)
}

// decodeText returns r as UTF-8, and the encoding it was read as.
//
// A byte-order mark decides over what the caller said: it is the file stating
// its own encoding, and the commonest reason a UTF-16 file is called UTF-8 is
// that it came out of a spreadsheet and nobody was asked.
func decodeText(r io.Reader, encoding string) (io.Reader, string, error) {
	encoding, err := normaliseEncoding(encoding)
	if err != nil {
		return nil, "", err
	}
	br := bufio.NewReaderSize(r, 64<<10)
	head, _ := br.Peek(3)
	switch {
	case len(head) >= 3 && head[0] == 0xEF && head[1] == 0xBB && head[2] == 0xBF:
		br.Discard(3)
		return br, EncodingUTF8, nil
	case len(head) >= 2 && head[0] == 0xFF && head[1] == 0xFE:
		br.Discard(2)
		return &utf16Reader{r: br}, EncodingUTF16, nil
	case len(head) >= 2 && head[0] == 0xFE && head[1] == 0xFF:
		br.Discard(2)
		return &utf16Reader{r: br, bigEndian: true}, EncodingUTF16, nil
	}
	switch encoding {
	case EncodingUTF16:
		// No mark: little-endian, which is what the tools that write UTF-16
		// without one write.
		return &utf16Reader{r: br}, EncodingUTF16, nil
	case EncodingLatin1:
		return &latin1Reader{r: br}, EncodingLatin1, nil
	}
	return br, EncodingUTF8, nil
}

// utf16Reader re-encodes UTF-16 as UTF-8 as it is read.
type utf16Reader struct {
	r         *bufio.Reader
	bigEndian bool
	out       bytes.Buffer
	err       error
}

func (u *utf16Reader) unit() (uint16, error) {
	var b [2]byte
	if _, err := io.ReadFull(u.r, b[:]); err != nil {
		if err == io.ErrUnexpectedEOF {
			// An odd byte at the end is not a character.
			return 0, io.EOF
		}
		return 0, err
	}
	if u.bigEndian {
		return uint16(b[0])<<8 | uint16(b[1]), nil
	}
	return uint16(b[1])<<8 | uint16(b[0]), nil
}

func (u *utf16Reader) Read(p []byte) (int, error) {
	for u.out.Len() < len(p) && u.err == nil {
		c, err := u.unit()
		if err != nil {
			u.err = err
			break
		}
		r := rune(c)
		if utf16.IsSurrogate(r) {
			low, err := u.unit()
			if err != nil {
				u.out.WriteRune(utf8.RuneError)
				u.err = err
				break
			}
			r = utf16.DecodeRune(r, rune(low))
		}
		u.out.WriteRune(r)
	}
	if u.out.Len() == 0 {
		return 0, u.err
	}
	return u.out.Read(p)
}

// windows1252 is what the bytes 0x80–0x9F mean in the encoding every program
// that says "Latin-1" on Windows actually writes. ISO 8859-1 proper has only
// control codes there, which no text file contains, so reading the range this
// way is what a browser does with the same label and loses nothing.
var windows1252 = [32]rune{
	0x20AC, 0x0081, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x008D, 0x017D, 0x008F,
	0x0090, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x009D, 0x017E, 0x0178,
}

// latin1Reader re-encodes Latin-1 as UTF-8 as it is read.
type latin1Reader struct {
	r   *bufio.Reader
	out bytes.Buffer
	err error
}

func (l *latin1Reader) Read(p []byte) (int, error) {
	for l.out.Len() < len(p) && l.err == nil {
		b, err := l.r.ReadByte()
		if err != nil {
			l.err = err
			break
		}
		switch {
		case b < 0x80:
			l.out.WriteByte(b)
		case b < 0xA0:
			l.out.WriteRune(windows1252[b-0x80])
		default:
			l.out.WriteRune(rune(b))
		}
	}
	if l.out.Len() == 0 {
		return 0, l.err
	}
	return l.out.Read(p)
}

// importValue is one field of one record as the file gave it: text from a
// delimited file, or a JSON scalar. quoted is what lets an empty field and an
// empty string be told apart, which a delimited file can only say with quotes.
type importValue struct {
	text   string
	quoted bool
	// null is a JSON null, or a JSON key the object did not carry.
	null bool
	// raw is the JSON value where it was not a string: a number kept exact,
	// or a boolean.
	raw any
}

// importRecord is one row of the file.
type importRecord struct {
	// line is where the record starts, counted from 1.
	line   int
	values []importValue
	// keys are a JSON object's own, in the order it gave them, and raw is the
	// object as the file wrote it — which a document store takes as it is.
	keys []string
	raw  []byte
	// err is a record that could be found but not read: the row is bad, and
	// the reader has gone on to the next.
	err error
}

// recordReader yields records until io.EOF. An error other than io.EOF means
// the rest of the file cannot be read at all.
type recordReader interface {
	next() (importRecord, error)
}

// maxImportRecordBytes bounds one record. The bound is not about a legitimate
// row: it is what stops a file with one unbalanced quote from being read to
// its end as a single field.
const maxImportRecordBytes = 16 << 20

// delimitedReader reads CSV and its relatives: any one-character delimiter,
// any one-character quote or none.
//
// It is this package's own rather than encoding/csv because that reader's
// quote is fixed, it cannot say whether a field was quoted, and both are
// things an import is asked about.
type delimitedReader struct {
	r        *bufio.Reader
	comma    rune
	quote    rune
	hasQuote bool
	line     int
	eof      bool
}

func newDelimitedReader(r io.Reader, comma, quote rune, hasQuote bool) *delimitedReader {
	return &delimitedReader{r: bufio.NewReaderSize(r, 64<<10), comma: comma, quote: quote, hasQuote: hasQuote, line: 1}
}

func (d *delimitedReader) readRune() (rune, error) {
	c, _, err := d.r.ReadRune()
	if err != nil {
		return 0, err
	}
	if c == '\n' {
		d.line++
	}
	return c, nil
}

func (d *delimitedReader) peekRune() (rune, bool) {
	c, _, err := d.r.ReadRune()
	if err != nil {
		return 0, false
	}
	d.r.UnreadRune()
	return c, true
}

// next reads one record. A line with nothing on it is not a record.
func (d *delimitedReader) next() (importRecord, error) {
	for {
		if d.eof {
			return importRecord{}, io.EOF
		}
		rec, blank, err := d.record()
		if err != nil {
			return importRecord{}, err
		}
		if blank {
			continue
		}
		return rec, nil
	}
}

func (d *delimitedReader) record() (importRecord, bool, error) {
	rec := importRecord{line: d.line}
	var (
		field   strings.Builder
		quoted  bool
		size    int
		started bool
	)
	end := func() {
		rec.values = append(rec.values, importValue{text: field.String(), quoted: quoted})
		field.Reset()
		quoted = false
	}
	for {
		c, err := d.readRune()
		if err == io.EOF {
			d.eof = true
			if !started && len(rec.values) == 0 {
				return rec, true, nil
			}
			end()
			return rec, false, nil
		}
		if err != nil {
			return rec, false, err
		}
		size += utf8.RuneLen(c)
		if size > maxImportRecordBytes {
			return rec, false, fmt.Errorf("the record starting on line %d is larger than %d MiB — is a quote unbalanced?",
				rec.line, maxImportRecordBytes>>20)
		}
		switch {
		case d.hasQuote && c == d.quote && field.Len() == 0 && !quoted:
			started, quoted = true, true
			if err := d.quotedField(&field, &size, rec.line); err != nil {
				if errors.Is(err, errUnterminatedQuote) {
					d.eof = true
					rec.err = fmt.Errorf("a quoted field is never closed")
					return rec, false, nil
				}
				return rec, false, err
			}
			// What follows a closing quote is the delimiter or the end of the
			// line. Anything else is kept as part of the field: a stray quote
			// in the middle of a value is a commoner mistake than a
			// deliberately half-quoted one, and dropping the text would be
			// the import deciding what the file meant.
		case c == d.comma:
			started = true
			end()
		case c == '\r':
			if next, ok := d.peekRune(); ok && next == '\n' {
				continue
			}
			field.WriteRune(c)
		case c == '\n':
			if !started && field.Len() == 0 && len(rec.values) == 0 {
				return rec, true, nil
			}
			end()
			return rec, false, nil
		default:
			started = true
			field.WriteRune(c)
		}
	}
}

var errUnterminatedQuote = errors.New("unterminated quoted field")

// quotedField reads up to the closing quote. A doubled quote inside is one
// quote; a line break inside is part of the value.
func (d *delimitedReader) quotedField(field *strings.Builder, size *int, startLine int) error {
	for {
		c, err := d.readRune()
		if err == io.EOF {
			return errUnterminatedQuote
		}
		if err != nil {
			return err
		}
		*size += utf8.RuneLen(c)
		if *size > maxImportRecordBytes {
			return fmt.Errorf("the record starting on line %d is larger than %d MiB — is a quote unbalanced?",
				startLine, maxImportRecordBytes>>20)
		}
		if c != d.quote {
			field.WriteRune(c)
			continue
		}
		if next, ok := d.peekRune(); ok && next == d.quote {
			d.readRune()
			field.WriteRune(d.quote)
			continue
		}
		return nil
	}
}

// lineCounter counts the newlines in what has been read through it, and can
// say which line a byte offset is on. The JSON decoder reads ahead of what it
// has decoded, so the count alone would name a line some way past the value.
type lineCounter struct {
	r io.Reader
	// breaks are the offsets of newlines not yet claimed by a lookup, in
	// order; passed is how many came before them.
	breaks []int64
	passed int
	offset int64
}

func (l *lineCounter) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	for i := 0; i < n; i++ {
		if p[i] == '\n' {
			l.breaks = append(l.breaks, l.offset+int64(i))
		}
	}
	l.offset += int64(n)
	return n, err
}

// lineAt returns the line a byte offset is on. Offsets must not go backwards.
func (l *lineCounter) lineAt(offset int64) int {
	i := 0
	for i < len(l.breaks) && l.breaks[i] < offset {
		i++
	}
	l.passed += i
	l.breaks = l.breaks[i:]
	return l.passed + 1
}

// jsonArrayReader reads a JSON array of objects one element at a time. A file
// that is a run of objects with no array around them is read the same way: it
// is what several tools write and call JSON.
type jsonArrayReader struct {
	src     *bufio.Reader
	lines   *lineCounter
	dec     *json.Decoder
	inArray bool
	done    bool
}

func newJSONArrayReader(r io.Reader) *jsonArrayReader {
	return &jsonArrayReader{src: bufio.NewReaderSize(r, 64<<10)}
}

// open looks at the first thing in the file. An opening bracket is the array's
// and is taken; anything else is the first record and is left where it is.
func (j *jsonArrayReader) open() error {
	skipped := 0
	for {
		b, err := j.src.Peek(1)
		if err != nil {
			return err
		}
		if b[0] != ' ' && b[0] != '\t' && b[0] != '\r' && b[0] != '\n' {
			j.inArray = b[0] == '['
			break
		}
		if b[0] == '\n' {
			skipped++
		}
		j.src.Discard(1)
	}
	j.lines = &lineCounter{r: j.src, passed: skipped}
	j.dec = json.NewDecoder(j.lines)
	j.dec.UseNumber()
	if j.inArray {
		if _, err := j.dec.Token(); err != nil {
			return err
		}
	}
	return nil
}

func (j *jsonArrayReader) next() (importRecord, error) {
	if j.done {
		return importRecord{}, io.EOF
	}
	if j.dec == nil {
		if err := j.open(); err != nil {
			j.done = true
			if err == io.EOF {
				return importRecord{}, io.EOF
			}
			return importRecord{}, fmt.Errorf("could not parse JSON: %w", err)
		}
	}
	if !j.dec.More() {
		j.done = true
		return importRecord{}, io.EOF
	}
	var raw json.RawMessage
	if err := j.dec.Decode(&raw); err != nil {
		j.done = true
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return importRecord{}, errTruncatedInput
		}
		return importRecord{}, fmt.Errorf("could not parse JSON near line %d: %w",
			j.lines.lineAt(j.dec.InputOffset()), err)
	}
	// The value ends where the decoder now stands, so it began its own length
	// before that — which is the line a person would say it is on.
	rec := importRecord{line: j.lines.lineAt(j.dec.InputOffset() - int64(len(raw)) + 1)}
	parseJSONRecord(raw, &rec)
	return rec, nil
}

// errTruncatedInput is a file that stops in the middle of a record. A preview
// is taken from the first part of a file, so there it is the expected end; in
// an import it is a file that did not arrive whole.
var errTruncatedInput = errors.New("the file ends in the middle of a record")

// ndjsonReader reads one JSON object per line. A line that does not parse is
// a bad row, and the next line is still a row.
type ndjsonReader struct {
	r    *bufio.Reader
	line int
}

func newNDJSONReader(r io.Reader) *ndjsonReader {
	return &ndjsonReader{r: bufio.NewReaderSize(r, 64<<10)}
}

func (n *ndjsonReader) next() (importRecord, error) {
	for {
		raw, err := n.readLine()
		if err != nil {
			return importRecord{}, err
		}
		n.line++
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 {
			continue
		}
		rec := importRecord{line: n.line}
		parseJSONRecord(trimmed, &rec)
		return rec, nil
	}
}

// readLine returns the next line without its terminator, or io.EOF once
// nothing is left.
func (n *ndjsonReader) readLine() ([]byte, error) {
	var line []byte
	for {
		chunk, err := n.r.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > maxImportRecordBytes {
			return nil, fmt.Errorf("line %d is larger than %d MiB", n.line+1, maxImportRecordBytes>>20)
		}
		switch err {
		case nil:
			return line, nil
		case bufio.ErrBufferFull:
			continue
		case io.EOF:
			if len(line) == 0 {
				return nil, io.EOF
			}
			return line, nil
		default:
			return nil, err
		}
	}
}

// parseJSONRecord fills a record from one JSON object, keeping its keys in
// the order the file has them. Numbers stay as written: a 64-bit identifier
// read through a float comes back as a different identifier.
func parseJSONRecord(raw []byte, rec *importRecord) {
	rec.raw = raw
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		rec.err = fmt.Errorf("not valid JSON: %w", err)
		return
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		rec.err = fmt.Errorf("expected an object, found %s", describeJSONToken(tok))
		return
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			rec.err = fmt.Errorf("not valid JSON: %w", err)
			return
		}
		key, _ := keyTok.(string)
		var value any
		if err := dec.Decode(&value); err != nil {
			rec.err = fmt.Errorf("not valid JSON: %w", err)
			return
		}
		rec.keys = append(rec.keys, key)
		rec.values = append(rec.values, jsonImportValue(value))
	}
	if _, err := dec.Token(); err != nil {
		rec.err = fmt.Errorf("not valid JSON: %w", err)
		return
	}
	if dec.More() {
		rec.err = fmt.Errorf("more than one value on the line")
	}
}

func describeJSONToken(tok json.Token) string {
	switch t := tok.(type) {
	case json.Delim:
		if t == '[' {
			return "an array"
		}
		return "a " + t.String()
	case string:
		return "a string"
	case json.Number:
		return "a number"
	case bool:
		return "a boolean"
	case nil:
		return "null"
	}
	return "something else"
}

// jsonImportValue turns a decoded JSON value into a field. A nested object or
// array cannot be bound as a scalar, so it goes in as its JSON text — which is
// what a json column wants anyway and what a text column can at least hold.
func jsonImportValue(v any) importValue {
	switch t := v.(type) {
	case nil:
		return importValue{null: true}
	case string:
		return importValue{text: t, quoted: true}
	case json.Number:
		return importValue{text: t.String(), raw: t}
	case bool:
		text := "false"
		if t {
			text = "true"
		}
		return importValue{text: text, raw: t}
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return importValue{null: true}
		}
		return importValue{text: string(b), quoted: true}
	}
}

// isExportMarker reports a record that is not a row but an export's closing
// remark: an object whose only key is ExportMarkerKey. It is returned with what
// the remark said, so an import of a cut-short export can say it was one.
func isExportMarker(rec importRecord) (string, bool) {
	if len(rec.keys) != 1 || rec.keys[0] != ExportMarkerKey {
		return "", false
	}
	var marker struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal([]byte(rec.values[0].text), &marker)
	return marker.Status, true
}
