package githubrepos_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/githubrepos"
	"github.com/Wayy01/Just-Dashboard/backend/internal/githubrepos/githubfake"
)

func asked(fake *githubfake.Fake, method, path string) int {
	n := 0
	for _, request := range fake.Requests() {
		if request.Method == method && request.Path == path {
			n++
		}
	}
	return n
}

// The account's own reads are kept for their time and no longer: the profile
// five minutes, an inbox page and the activity one.
func TestTheAccountReadsAreKeptOnlyForTheirTime(t *testing.T) {
	h := newHarness(t)
	h.createApp()
	c := h.connect(h.alice, 1001)
	ctx := context.Background()
	h.fake.Reset()
	query, err := githubrepos.ParseInboxQuery(githubrepos.InboxPulls, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	read := func() {
		t.Helper()
		if _, err := h.s.Profile(ctx, h.alice, c.ID, false); err != nil {
			t.Fatal(err)
		}
		if _, err := h.s.Inbox(ctx, h.alice, c.ID, query, false); err != nil {
			t.Fatal(err)
		}
		if _, err := h.s.Events(ctx, h.alice, c.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	counts := func() [3]int {
		return [3]int{asked(h.fake, "POST", "/graphql"), asked(h.fake, "GET", "/search/issues"), asked(h.fake, "GET", "/users/octo-dev/events")}
	}
	read()
	h.clock = h.clock.Add(59 * time.Second)
	read()
	if got := counts(); got != [3]int{1, 1, 1} {
		t.Fatalf("within a minute GitHub was asked %v", got)
	}
	h.clock = h.clock.Add(2 * time.Second)
	read()
	if got := counts(); got != [3]int{1, 2, 2} {
		t.Fatalf("after a minute GitHub was asked %v", got)
	}
	h.clock = h.clock.Add(4*time.Minute + 30*time.Second)
	read()
	if got := counts(); got != [3]int{2, 3, 3} {
		t.Fatalf("after five minutes GitHub was asked %v", got)
	}

	// Another dashboard account is never answered from what was kept for
	// this one's connection.
	if _, err := h.s.Profile(ctx, h.bob, c.ID, false); err != githubrepos.ErrConnectionNotFound {
		t.Fatalf("bob's read of alice's profile: %v", err)
	}
	if _, err := h.s.Inbox(ctx, h.bob, c.ID, query, false); err != githubrepos.ErrConnectionNotFound {
		t.Fatalf("bob's read of alice's inbox: %v", err)
	}
	if _, err := h.s.Events(ctx, h.bob, c.ID, false); err != githubrepos.ErrConnectionNotFound {
		t.Fatalf("bob's read of alice's events: %v", err)
	}
}

// A profile that came back without its calendar is asked for again after a
// minute rather than five, so one bad answer from GitHub does not hide the
// calendar for long.
func TestAProfileWithoutItsCalendarIsAskedForAgainSooner(t *testing.T) {
	h := newHarness(t)
	h.createApp()
	c := h.connect(h.alice, 1001)
	ctx := context.Background()
	h.fake.Override = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/graphql" {
			return false
		}
		w.WriteHeader(http.StatusBadGateway)
		return true
	}
	profile, err := h.s.Profile(ctx, h.alice, c.ID, false)
	if err != nil || profile.Contributions != nil || profile.ContributionsMessage == "" {
		t.Fatalf("profile = %+v %v", profile, err)
	}
	h.fake.Override = nil
	h.clock = h.clock.Add(30 * time.Second)
	if profile, _ = h.s.Profile(ctx, h.alice, c.ID, false); profile.Contributions != nil {
		t.Fatal("asked again within the minute")
	}
	h.clock = h.clock.Add(31 * time.Second)
	profile, err = h.s.Profile(ctx, h.alice, c.ID, false)
	if err != nil || profile.Contributions == nil || profile.ContributionsMessage != "" {
		t.Fatalf("after a minute: %+v %v", profile, err)
	}

	// Disconnecting drops what was kept: nothing of it is left to answer.
	if _, err := h.s.Disconnect(ctx, h.alice, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.s.Profile(ctx, h.alice, c.ID, false); err != githubrepos.ErrConnectionNotFound {
		t.Fatalf("after disconnect: %v", err)
	}
}
