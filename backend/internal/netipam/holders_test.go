package netipam

import (
	"context"
	"testing"
)

func TestHeldForNamesTheReservationsAResourceStillHolds(t *testing.T) {
	s := testService(t, observedSnapshot())
	p := pool(t, s, "10.244.0.0/16", 24)
	kept := reserve(t, s, p, "lab")
	released := reserve(t, s, p, "lab")
	reserve(t, s, p, "edge")
	if e := s.Release(context.Background(), released.ID); e != nil {
		t.Fatal(e)
	}
	held, e := s.HeldFor(context.Background(), "docker_network", "lab", "")
	if e != nil {
		t.Fatal(e)
	}
	if len(held) != 1 || held[0].ID != kept.ID {
		t.Fatalf("only the unreleased reservation of that resource is held: %+v", held)
	}
	if held, _ := s.HeldFor(context.Background(), "wireguard", "lab", ""); len(held) != 0 {
		t.Fatalf("another owner's resource of the same name holds nothing here: %+v", held)
	}
	if held, _ := s.HeldFor(context.Background(), "docker_network", "renamed", ""); len(held) != 0 {
		t.Fatalf("a different name holds nothing: %+v", held)
	}
}
