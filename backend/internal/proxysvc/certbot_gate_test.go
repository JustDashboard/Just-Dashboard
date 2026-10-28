package proxysvc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const pageRenewal = "Renewing app.example.com (started by alice from the Certificates page)"
const runOrder = "a deployment run issuing a certificate for app.example.com"

// The turn is one: a second taker is told whom it waits for, waits, and goes
// once the first lets go. One whose time runs out is refused with the
// holder's name, and reads as the deadline it is underneath.
func TestCertbotTurnIsTakenOnceAndWaitedFor(t *testing.T) {
	gate := &certbotGate{turn: make(chan struct{}, 1)}
	release, err := gate.hold(context.Background(), pageRenewal, nil)
	if err != nil {
		t.Fatal(err)
	}
	told := make(chan string, 1)
	went := make(chan error, 1)
	go func() {
		again, err := gate.hold(context.Background(), runOrder, func(holder string) { told <- holder })
		if err == nil {
			again()
		}
		went <- err
	}()
	select {
	case holder := <-told:
		if holder != pageRenewal {
			t.Fatalf("the second taker was told it waits for %q", holder)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second taker was not told whom it waits for")
	}
	select {
	case err := <-went:
		t.Fatalf("the second taker went through while the first held the turn: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	release() // a second release is nothing
	select {
	case err := <-went:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second taker did not go once the turn was free")
	}

	release, err = gate.hold(context.Background(), pageRenewal, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	told = make(chan string, 1)
	_, err = gate.hold(ctx, runOrder, func(holder string) { told <- holder })
	var busy *CertbotBusyError
	if !errors.As(err, &busy) || busy.Holder != pageRenewal || busy.Waited <= 0 ||
		!errors.Is(err, ErrCertbotBusy) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a wait that ran out = %v", err)
	}
	if !strings.HasPrefix(err.Error(), "waited ") || !strings.Contains(err.Error(), "busy with "+pageRenewal+", and ran out of time") {
		t.Fatalf("a wait that ran out reads %q", err)
	}
	if len(told) != 1 {
		t.Fatal("the taker that ran out was not told whom it waited for")
	}

	// A context already over takes nothing and names nobody.
	over, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if _, err := gate.hold(over, runOrder, func(string) { t.Error("told a holder for a wait that never began") }); !errors.Is(err, context.Canceled) {
		t.Fatalf("an ended context = %v", err)
	}
}
