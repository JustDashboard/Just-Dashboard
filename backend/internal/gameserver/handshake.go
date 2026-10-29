package gameserver

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// JavaStatus is what a Minecraft Java server says about itself in answer to
// the status handshake the client's server list sends: the protocol it
// speaks, and how full it is. It is what a readiness check asks, because a
// process that is up but still generating its world does not answer it.
type JavaStatus struct {
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
	Online   int    `json:"online"`
	Maximum  int    `json:"maximum"`
}

var ErrHandshake = errors.New("the server did not complete a Minecraft status handshake")

// maxStatusBytes bounds the answer. A status carries the server icon as
// base64, tens of kilobytes; nothing a readiness check reads needs more.
const maxStatusBytes = 256 << 10

// JavaStatusHandshake runs the Server List Ping over an open connection: a
// handshake naming the address the client dialled and the status state, a
// status request, and one status answer read and decoded. The caller owns
// the connection and its deadline.
func JavaStatusHandshake(conn io.ReadWriter, host string, port int) (JavaStatus, error) {
	var handshake bytes.Buffer
	handshake.WriteByte(0x00)
	// -1 is the protocol a client sends when it is only asking: the server
	// answers with its own instead of refusing a mismatch.
	writeVarInt(&handshake, -1)
	writeString(&handshake, host)
	_ = binary.Write(&handshake, binary.BigEndian, uint16(port))
	writeVarInt(&handshake, 1)
	var request bytes.Buffer
	writePacket(&request, handshake.Bytes())
	writePacket(&request, []byte{0x00})
	if _, err := conn.Write(request.Bytes()); err != nil {
		return JavaStatus{}, fmt.Errorf("%w: %v", ErrHandshake, err)
	}
	reader := bufio.NewReader(io.LimitReader(conn, maxStatusBytes+16))
	length, err := readVarInt(reader)
	if err != nil || length <= 0 || length > maxStatusBytes {
		return JavaStatus{}, fmt.Errorf("%w: no status answer", ErrHandshake)
	}
	packet := make([]byte, length)
	if _, err := io.ReadFull(reader, packet); err != nil {
		return JavaStatus{}, fmt.Errorf("%w: the answer stopped part way", ErrHandshake)
	}
	body := bytes.NewReader(packet)
	if id, err := readVarInt(body); err != nil || id != 0x00 {
		return JavaStatus{}, fmt.Errorf("%w: the answer is not a status packet", ErrHandshake)
	}
	size, err := readVarInt(body)
	if err != nil || size <= 0 || size > body.Len() {
		return JavaStatus{}, fmt.Errorf("%w: the status text is malformed", ErrHandshake)
	}
	text := make([]byte, size)
	if _, err := io.ReadFull(body, text); err != nil {
		return JavaStatus{}, fmt.Errorf("%w: the status text is malformed", ErrHandshake)
	}
	var decoded struct {
		Version struct {
			Name     string `json:"name"`
			Protocol int    `json:"protocol"`
		} `json:"version"`
		Players struct {
			Max    int `json:"max"`
			Online int `json:"online"`
		} `json:"players"`
	}
	if err := json.Unmarshal(text, &decoded); err != nil || strings.TrimSpace(decoded.Version.Name) == "" {
		return JavaStatus{}, fmt.Errorf("%w: the status names no version", ErrHandshake)
	}
	return JavaStatus{
		Version: sanitizeConsoleOutput(decoded.Version.Name), Protocol: decoded.Version.Protocol,
		Online: decoded.Players.Online, Maximum: decoded.Players.Max,
	}, nil
}

func writePacket(out *bytes.Buffer, payload []byte) {
	writeVarInt(out, int32(len(payload)))
	out.Write(payload)
}

func writeString(out *bytes.Buffer, value string) {
	writeVarInt(out, int32(len(value)))
	out.WriteString(value)
}

// writeVarInt is the protocol's LEB128 over the two's-complement bits, so -1
// is five bytes.
func writeVarInt(out *bytes.Buffer, value int32) {
	unsigned := uint32(value)
	for {
		if unsigned&^0x7f == 0 {
			out.WriteByte(byte(unsigned))
			return
		}
		out.WriteByte(byte(unsigned&0x7f) | 0x80)
		unsigned >>= 7
	}
}

func readVarInt(reader io.ByteReader) (int, error) {
	var result uint32
	for shift := 0; shift < 35; shift += 7 {
		next, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		result |= uint32(next&0x7f) << shift
		if next&0x80 == 0 {
			return int(int32(result)), nil
		}
	}
	return 0, errors.New("varint is longer than five bytes")
}
