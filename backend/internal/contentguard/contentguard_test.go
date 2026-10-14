package contentguard

import (
	"errors"
	"testing"
)

func TestRevisionChecksBytesRatherThanSize(t *testing.T) {
	expected := Revision("old\n")
	if err := Check("old\n", &expected); err != nil {
		t.Fatal(err)
	}
	if err := Check("new\n", &expected); !errors.Is(err, ErrConflict) {
		t.Fatalf("same-length edit: %v", err)
	}
	if err := Check("new\n", nil); err != nil {
		t.Fatalf("legacy write: %v", err)
	}
}
