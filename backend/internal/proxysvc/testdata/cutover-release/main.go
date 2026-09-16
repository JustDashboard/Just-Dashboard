// Command cutover-release is the release process the cutover continuity test
// puts behind nginx. It is deliberately stdlib-only, including its WebSocket
// handling, so the live test can build and run it inside a container without a
// module download.
package main

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func main() {
	id := os.Getenv("JD_RELEASE_ID")
	if id == "" {
		id = "unknown"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, id)
	})
	// A long streamed response is the case a reload is most likely to cut:
	// the headers are already sent, so a lost response cannot be retried.
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 8; i++ {
			if _, err := io.WriteString(w, id+"\n"); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(150 * time.Millisecond):
			}
		}
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		serveWebSocket(w, r, id)
	})
	server := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := server.ListenAndServe(); err != nil {
		os.Exit(1)
	}
}

func serveWebSocket(w http.ResponseWriter, r *http.Request, id string) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "expected websocket", http.StatusBadRequest)
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "not hijackable", http.StatusInternalServerError)
		return
	}
	conn, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	sum := sha1.Sum([]byte(key + websocketGUID))
	accept := base64.StdEncoding.EncodeToString(sum[:])
	response := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := buffered.WriteString(response); err != nil {
		return
	}
	if err := buffered.Flush(); err != nil {
		return
	}
	for {
		opcode, payload, err := readFrame(buffered)
		if err != nil {
			return
		}
		switch opcode {
		case 0x8:
			writeFrame(conn, 0x8, nil)
			return
		case 0x9:
			if err := writeFrame(conn, 0xA, payload); err != nil {
				return
			}
		case 0x1, 0x2:
			if err := writeFrame(conn, opcode, []byte(id+":"+string(payload))); err != nil {
				return
			}
		}
	}
}

func readFrame(r io.Reader) (byte, []byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	opcode := header[0] & 0x0F
	masked := header[1]&0x80 != 0
	length := uint64(header[1] & 0x7F)
	switch length {
	case 126:
		extended := make([]byte, 2)
		if _, err := io.ReadFull(r, extended); err != nil {
			return 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(extended))
	case 127:
		extended := make([]byte, 8)
		if _, err := io.ReadFull(r, extended); err != nil {
			return 0, nil, err
		}
		length = binary.BigEndian.Uint64(extended)
	}
	if length > 1<<20 {
		return 0, nil, errors.New("frame too large")
	}
	mask := make([]byte, 4)
	if masked {
		if _, err := io.ReadFull(r, mask); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return opcode, payload, nil
}

// writeFrame sends an unmasked server frame. Payloads here are short, so the
// two extended-length forms a client may send are not produced.
func writeFrame(w net.Conn, opcode byte, payload []byte) error {
	if len(payload) > 125 {
		return errors.New("payload too large for this fixture")
	}
	frame := append([]byte{0x80 | opcode, byte(len(payload))}, payload...)
	_, err := w.Write(frame)
	return err
}
