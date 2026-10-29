package gameserver

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// scripted is a connection whose answer is fixed and whose request is kept.
type scripted struct {
	request bytes.Buffer
	answer  *bytes.Reader
}

func (s *scripted) Write(p []byte) (int, error) { return s.request.Write(p) }
func (s *scripted) Read(p []byte) (int, error)  { return s.answer.Read(p) }

func statusAnswer(text string) []byte {
	var body bytes.Buffer
	body.WriteByte(0x00)
	writeString(&body, text)
	var packet bytes.Buffer
	writePacket(&packet, body.Bytes())
	return packet.Bytes()
}

func TestJavaStatusHandshakeReadsTheServersOwnAnswer(t *testing.T) {
	t.Parallel()
	conn := &scripted{answer: bytes.NewReader(statusAnswer(
		`{"version":{"name":"Paper 1.21.4\u001b[31m","protocol":769},"players":{"max":20,"online":3},"description":"x"}`))}
	status, err := JavaStatusHandshake(conn, "127.0.0.1", 25565)
	if err != nil {
		t.Fatal(err)
	}
	if status.Version != "Paper 1.21.4" || status.Protocol != 769 || status.Online != 3 || status.Maximum != 20 {
		t.Fatalf("status = %#v", status)
	}
	// The request is a handshake for the status state followed by a
	// status request: the client's server list, nothing that logs in.
	sent := conn.request.Bytes()
	if !bytes.HasSuffix(sent, []byte{0x01, 0x00}) || !bytes.Contains(sent, []byte("127.0.0.1")) ||
		!bytes.Contains(sent, []byte{0x63, 0xdd, 0x01}) {
		t.Fatalf("request = %x", sent)
	}
}

func TestJavaStatusHandshakeRefusesWhatIsNotAStatus(t *testing.T) {
	t.Parallel()
	for name, answer := range map[string][]byte{
		"nothing":       {},
		"a login error": statusAnswer(`{"text":"You are not whitelisted"}`),
		"not json":      statusAnswer(`<html>`),
		"a long length": {0xff, 0xff, 0xff, 0xff, 0x0f},
		"a cut packet":  statusAnswer(`{"version":{"name":"1.21"}}`)[:6],
		"another packet": func() []byte {
			var body bytes.Buffer
			body.WriteByte(0x01)
			body.WriteString(strings.Repeat("x", 8))
			var packet bytes.Buffer
			writePacket(&packet, body.Bytes())
			return packet.Bytes()
		}(),
	} {
		if _, err := JavaStatusHandshake(&scripted{answer: bytes.NewReader(answer)}, "localhost", 25565); !errors.Is(err, ErrHandshake) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestVarIntsRoundTripTheProtocolsEncoding(t *testing.T) {
	t.Parallel()
	for _, value := range []int32{0, 1, 127, 128, 255, 25565, 2097151, -1} {
		var buffer bytes.Buffer
		writeVarInt(&buffer, value)
		if value == -1 && buffer.Len() != 5 {
			t.Fatalf("-1 encodes as %x", buffer.Bytes())
		}
		decoded, err := readVarInt(&buffer)
		if err != nil || int32(decoded) != value {
			t.Fatalf("%d round-trips as %d, %v", value, decoded, err)
		}
	}
	if _, err := readVarInt(bytes.NewReader([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x01})); err == nil {
		t.Fatal("a six-byte varint was read")
	}
}
