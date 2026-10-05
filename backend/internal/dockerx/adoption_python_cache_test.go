package dockerx

import (
	"encoding/binary"
	"testing"
)

func TestPythonCacheNeedsMatchingTimestampSourceEvidence(t *testing.T) {
	header := make([]byte, 16)
	header[2], header[3] = '\r', '\n'
	binary.LittleEndian.PutUint32(header[8:12], 1700000000)
	binary.LittleEndian.PutUint32(header[12:16], 123)
	if !pythonCacheMatchesSource(header, 1700000000, 123) {
		t.Fatal("matching source rejected")
	}
	if pythonCacheMatchesSource(header, 1700000001, 123) || pythonCacheMatchesSource(header, 1700000000, 124) || pythonCacheMatchesSource(header[:15], 1700000000, 123) {
		t.Fatal("unverified source accepted")
	}
	binary.LittleEndian.PutUint32(header[4:8], 3)
	if pythonCacheMatchesSource(header, 1700000000, 123) {
		t.Fatal("hash-based cache accepted without source hash proof")
	}
}
