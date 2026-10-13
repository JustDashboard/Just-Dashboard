package remotedesk

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func TestInstructionLengthsCountCodePoints(t *testing.T) {
	got := Instruction{Opcode: "clipboard", Args: []string{"héllo 👍", ""}}.String()
	want := "9.clipboard,7.héllo 👍,0.;"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestReaderReadsWhatItWasSentWhateverTheChunking(t *testing.T) {
	stream := "4.size,4.1920,4.1080;" + "9.clipboard,7.héllo 👍,0.;" + "0.,4.ping,13.1697040000000;" + "4.sync,8.12345678;"
	for name, r := range map[string]io.Reader{
		"whole":       strings.NewReader(stream),
		"byte a time": iotest.OneByteReader(strings.NewReader(stream)),
		"half":        iotest.HalfReader(strings.NewReader(stream)),
	} {
		t.Run(name, func(t *testing.T) {
			rd := NewReader(r)
			var raws []string
			var ops []Instruction
			for {
				raw, in, err := rd.Read()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				raws = append(raws, string(raw))
				ops = append(ops, in)
			}
			if strings.Join(raws, "") != stream {
				t.Fatalf("raw bytes changed: %q", strings.Join(raws, ""))
			}
			if len(ops) != 4 || ops[1].Opcode != "clipboard" || ops[1].Args[0] != "héllo 👍" || ops[1].Args[1] != "" {
				t.Fatalf("decoded %#v", ops)
			}
			if ops[2].Opcode != "" || ops[2].Args[0] != "ping" {
				t.Fatalf("internal instruction decoded as %#v", ops[2])
			}
		})
	}
}

func TestReaderRefusesMalformedInput(t *testing.T) {
	for name, input := range map[string]string{
		"letters for a length": "x.size;",
		"no length":            ".size;",
		"bad terminator":       "4.size:",
		"length too short":     "3.size;",
		"absurd length":        "999999999.a;",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := NewReader(strings.NewReader(input)).Read(); err == nil || errors.Is(err, io.EOF) {
				t.Fatalf("accepted %q (err %v)", input, err)
			}
		})
	}
}

func TestReaderTellsACleanEndFromACutOne(t *testing.T) {
	if _, _, err := NewReader(strings.NewReader("")).Read(); !errors.Is(err, io.EOF) {
		t.Fatalf("empty stream: %v", err)
	}
	if _, _, err := NewReader(strings.NewReader("4.size,4.19")).Read(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("cut stream: %v", err)
	}
}

func TestGuacErrorKeepsTheDaemonsCode(t *testing.T) {
	e := errorFrom(Instruction{Opcode: "error", Args: []string{"Aborted. See logs.", "519"}})
	if e.Message != "Aborted. See logs." || e.Code != statusUpstreamNotFound {
		t.Fatalf("got %#v", e)
	}
	if got := string(e.instruction()); got != "5.error,18.Aborted. See logs.,3.519;" {
		t.Fatalf("encoded as %q", got)
	}
}
