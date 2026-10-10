package netcapture

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// The reader accepts only classic PCAP 2.4 emitted by the fixed tcpdump argv.
// It retains complete records; reaching a cap never produces a corrupt suffix.
type pcapWriter struct {
	request  Request
	stop     func()
	progress func(int, int)
	data     []byte
	pending  []byte
	order    binary.ByteOrder
	nano     bool
	packets  int
	linkType uint32
	reason   string
	err      error
}

func (w *pcapWriter) fail(err error) { w.err = err; w.stop() }
func (w *pcapWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 && w.err == nil && w.reason == "" {
		need := 24
		if w.order != nil {
			need = 16
			if len(w.pending) >= 16 {
				need += int(w.order.Uint32(w.pending[8:12]))
			}
		}
		take := min(need-len(w.pending), len(p))
		w.pending = append(w.pending, p[:take]...)
		p = p[take:]
		if len(w.pending) < need {
			continue
		}
		if w.order == nil {
			switch {
			case bytes.Equal(w.pending[:4], []byte{0xd4, 0xc3, 0xb2, 0xa1}):
				w.order = binary.LittleEndian
			case bytes.Equal(w.pending[:4], []byte{0xa1, 0xb2, 0xc3, 0xd4}):
				w.order = binary.BigEndian
			case bytes.Equal(w.pending[:4], []byte{0x4d, 0x3c, 0xb2, 0xa1}):
				w.order, w.nano = binary.LittleEndian, true
			case bytes.Equal(w.pending[:4], []byte{0xa1, 0xb2, 0x3c, 0x4d}):
				w.order, w.nano = binary.BigEndian, true
			default:
				w.fail(fmt.Errorf("native capture did not emit supported classic PCAP"))
				continue
			}
			if w.order.Uint16(w.pending[4:6]) != 2 || w.order.Uint16(w.pending[6:8]) != 4 || int(w.order.Uint32(w.pending[16:20])) != w.request.SnapshotLength {
				w.fail(fmt.Errorf("native PCAP header differs from the requested snapshot contract"))
				continue
			}
			w.linkType = w.order.Uint32(w.pending[20:24])
			if w.linkType&0x03ff0000 != 0 || w.linkType&0x08000000 != 0 {
				w.fail(fmt.Errorf("native PCAP link header contains reserved flags"))
				continue
			}
			w.data = append(w.data, w.pending...)
			w.pending = w.pending[:0]
			continue
		}
		captured, original := w.order.Uint32(w.pending[8:12]), w.order.Uint32(w.pending[12:16])
		timestampLimit := uint32(1000000)
		if w.nano {
			timestampLimit = 1000000000
		}
		if captured > uint32(w.request.SnapshotLength) || captured > original || w.order.Uint32(w.pending[4:8]) >= timestampLimit {
			w.fail(fmt.Errorf("native PCAP packet length or timestamp is invalid"))
			continue
		}
		if len(w.pending) == 16 && captured > 0 {
			continue
		}
		if len(w.data)+len(w.pending) > w.request.MaxBytes {
			w.reason = "byte_limit"
			w.stop()
			continue
		}
		w.data = append(w.data, w.pending...)
		w.pending = w.pending[:0]
		w.packets++
		if w.progress != nil {
			w.progress(w.packets, len(w.data))
		}
		if w.packets >= w.request.Packets {
			w.reason = "packet_limit"
			w.stop()
		}
	}
	return n, nil
}

func (w *pcapWriter) result() Result {
	result := Result{Packets: w.packets, Bytes: len(w.data), LinkType: w.linkType, StopReason: w.reason, PartialPacket: len(w.pending) > 0 && w.reason == "", Artifact: w.data, ArtifactAvailable: len(w.data) >= 24 && w.err == nil}
	if result.ArtifactAvailable {
		digest := sha256.Sum256(w.data)
		result.SHA256 = hex.EncodeToString(digest[:])
	}
	return result
}
