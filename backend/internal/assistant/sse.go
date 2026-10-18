package assistant

import (
	"bufio"
	"io"
	"strings"
)

// maxEventBytes bounds one server-sent event. The biggest a provider sends is
// a thinking block's signature or a tool's input, far below this; a stream
// that goes past it is not one this reader should keep buffering.
const maxEventBytes = 1 << 20

// readEvents reads a server-sent event stream and hands each event's name and
// data to fn, the data lines of one event joined by newlines as the format
// says. It ends at the end of the stream or at fn's first error.
func readEvents(r io.Reader, fn func(event, data string) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), maxEventBytes)
	var (
		event string
		data  []string
	)
	flush := func() error {
		if len(data) == 0 {
			event = ""
			return nil
		}
		err := fn(event, strings.Join(data, "\n"))
		event, data = "", data[:0]
		return err
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				return err
			}
		case strings.HasPrefix(line, ":"):
			// A comment, which servers send to keep a quiet connection open.
		default:
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "event":
				event = value
			case "data":
				data = append(data, value)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}
