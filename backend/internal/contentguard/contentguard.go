// Package contentguard prevents a stale editor from replacing a newer reading.
package contentguard

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash/fnv"
	"io"
	"sync"
)

var ErrConflict = errors.New("the content changed since it was opened; review the current version before saving")

// Revision identifies bytes, independent of timestamp precision or file size.
func Revision(content string) string {
	return RevisionBytes([]byte(content))
}

func RevisionBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// RevisionReader hashes binary content without retaining another copy of it.
func RevisionReader(reader io.Reader) (string, int64, error) {
	hash := sha256.New()
	n, err := io.Copy(hash, reader)
	if err != nil {
		return "", n, err
	}
	return hex.EncodeToString(hash.Sum(nil)), n, nil
}

// Check permits older API clients that do not supply a precondition.
func Check(content string, expected *string) error {
	if expected != nil && Revision(content) != *expected {
		return ErrConflict
	}
	return nil
}

// The bounded lock set is shared by Files and Compose, which can edit the same
// path. Serializing the comparison and publish prevents two dashboard saves
// from both accepting the same revision.
var locks [256]sync.Mutex

func Lock(path string) func() {
	h := fnv.New32a()
	_, _ = h.Write([]byte(path))
	lock := &locks[h.Sum32()%uint32(len(locks))]
	lock.Lock()
	return lock.Unlock
}
