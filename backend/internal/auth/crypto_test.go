package auth

import (
	"bytes"
	"sync"
	"testing"

	"golang.org/x/crypto/argon2"
)

func TestIDKeyIsArgon2id(t *testing.T) {
	p := argonParams{time: 1, memory: 64, threads: 1, keyLen: 32}
	salt := []byte("0123456789abcdef")
	want := argon2.IDKey([]byte("hunter2"), salt, p.time, p.memory, p.threads, p.keyLen)
	if got := idKey([]byte("hunter2"), salt, p); !bytes.Equal(got, want) {
		t.Fatalf("idKey = %x, want %x", got, want)
	}
}

// More sign-ins than slots must queue, not deadlock, and every one must still
// verify against its own hash.
func TestConcurrentHashesAllComplete(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 3*cap(argonSlots); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			encoded, err := HashPassword("correct horse")
			if err != nil {
				t.Error(err)
				return
			}
			if !VerifyPassword("correct horse", encoded) || VerifyPassword("wrong", encoded) {
				t.Error("a hash made under contention did not verify as it should")
			}
		}()
	}
	wg.Wait()
}
