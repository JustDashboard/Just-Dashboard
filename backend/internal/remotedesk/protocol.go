package remotedesk

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Instruction is one Guacamole protocol instruction. On the wire every
// element is "<length>.<value>", elements are joined by "," and the
// instruction ends in ";". The length counts Unicode code points, not bytes,
// which is why nothing here may slice by byte offsets alone.
type Instruction struct {
	Opcode string
	Args   []string
}

func (in Instruction) String() string {
	var b strings.Builder
	writeElement(&b, in.Opcode)
	for _, arg := range in.Args {
		b.WriteByte(',')
		writeElement(&b, arg)
	}
	b.WriteByte(';')
	return b.String()
}

func writeElement(b *strings.Builder, value string) {
	b.WriteString(strconv.Itoa(utf8.RuneCountInString(value)))
	b.WriteByte('.')
	b.WriteString(value)
}

func encode(opcode string, args ...string) []byte {
	return []byte(Instruction{Opcode: opcode, Args: args}.String())
}

const (
	// guacd splits image data into blobs of a few kilobytes, so an element
	// this long is a broken or hostile peer rather than a large screen.
	maxElementRunes = 1 << 20
	maxInstruction  = 4 << 20
)

var errMalformed = errors.New("malformed Guacamole instruction")

// Reader reads whole instructions and keeps their encoded bytes, so a relay
// forwards exactly what it received after deciding by the opcode.
type Reader struct {
	r *bufio.Reader
}

func NewReader(r io.Reader) *Reader {
	return &Reader{r: bufio.NewReaderSize(r, 64<<10)}
}

func newReaderSize(r io.Reader, size int) *Reader {
	return &Reader{r: bufio.NewReaderSize(r, size)}
}

// Buffered is how many bytes already arrived and wait to be read. A relay
// flushes its batch when this is zero, so a quiet screen is never held back
// waiting for more output.
func (rd *Reader) Buffered() int { return rd.r.Buffered() }

func (rd *Reader) Read() ([]byte, Instruction, error) {
	var raw []byte
	var in Instruction
	first := true
	for {
		length, digits, err := rd.readLength()
		if err != nil {
			if first && errors.Is(err, io.EOF) && len(digits) == 0 {
				return nil, in, io.EOF
			}
			return nil, in, err
		}
		raw = append(raw, digits...)
		raw = append(raw, '.')
		start := len(raw)
		raw, err = rd.readRunes(raw, length)
		if err != nil {
			return nil, in, err
		}
		value := string(raw[start:])
		if first {
			in.Opcode = value
			first = false
		} else {
			in.Args = append(in.Args, value)
		}
		terminator, err := rd.r.ReadByte()
		if err != nil {
			return nil, in, unexpected(err)
		}
		raw = append(raw, terminator)
		if len(raw) > maxInstruction {
			return nil, in, errMalformed
		}
		switch terminator {
		case ';':
			return raw, in, nil
		case ',':
		default:
			return nil, in, errMalformed
		}
	}
}

func (rd *Reader) readLength() (int, []byte, error) {
	var digits []byte
	for {
		c, err := rd.r.ReadByte()
		if err != nil {
			if len(digits) > 0 {
				return 0, digits, unexpected(err)
			}
			return 0, digits, err
		}
		if c == '.' {
			break
		}
		if c < '0' || c > '9' || len(digits) >= 8 {
			return 0, digits, errMalformed
		}
		digits = append(digits, c)
	}
	if len(digits) == 0 {
		return 0, digits, errMalformed
	}
	n, err := strconv.Atoi(string(digits))
	if err != nil || n > maxElementRunes {
		return 0, digits, errMalformed
	}
	return n, digits, nil
}

// readRunes appends exactly n code points. It scans whole buffered chunks
// rather than decoding rune by rune, because image blobs are megabytes of
// ASCII a second and a per-byte loop through the bufio API shows up.
func (rd *Reader) readRunes(dst []byte, n int) ([]byte, error) {
	for {
		if _, err := rd.r.Peek(1); err != nil {
			if n == 0 {
				return dst, nil
			}
			return dst, unexpected(err)
		}
		buf, _ := rd.r.Peek(rd.r.Buffered())
		i := 0
		for i < len(buf) {
			if buf[i]&0xC0 != 0x80 {
				if n == 0 {
					break
				}
				n--
			}
			i++
		}
		dst = append(dst, buf[:i]...)
		rd.r.Discard(i)
		if i < len(buf) {
			return dst, nil
		}
		if len(dst) > maxInstruction {
			return dst, errMalformed
		}
	}
}

func unexpected(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

// Status codes from the protocol reference. The browser client turns these
// into the message it shows, so the dashboard picks the closest one rather
// than inventing its own.
const (
	statusServerError         = 0x0200
	statusUpstreamTimeout     = 0x0202
	statusUpstreamError       = 0x0203
	statusResourceNotFound    = 0x0204
	statusUpstreamNotFound    = 0x0207
	statusUpstreamUnavailable = 0x0208
	statusClientUnauthorized  = 0x0301
	statusClientForbidden     = 0x0303
)

// GuacError is an "error" instruction from guacd, or one the dashboard sends
// in its place when it cannot get that far.
type GuacError struct {
	Message string
	Code    int
}

func (e *GuacError) Error() string {
	return fmt.Sprintf("%s (status 0x%04X)", e.Message, e.Code)
}

func (e *GuacError) instruction() []byte {
	return encode("error", e.Message, strconv.Itoa(e.Code))
}

func errorFrom(in Instruction) *GuacError {
	e := &GuacError{Message: "The remote desktop server reported an error", Code: statusServerError}
	if len(in.Args) > 0 && in.Args[0] != "" {
		e.Message = in.Args[0]
	}
	if len(in.Args) > 1 {
		if code, err := strconv.Atoi(in.Args[1]); err == nil {
			e.Code = code
		}
	}
	return e
}
