package assistant

import (
	"errors"
	"strings"
	"testing"
)

func TestReadEventsFollowsTheFormat(t *testing.T) {
	stream := ": keep-alive\n" +
		"event: message_start\n" +
		"data: {\"a\":1}\n" +
		"\n" +
		"data: first\n" +
		"data:second\n" +
		"\n" +
		"event: ping\n" +
		"\n" +
		"data: [DONE]"
	type got struct{ event, data string }
	var events []got
	err := readEvents(strings.NewReader(stream), func(event, data string) error {
		events = append(events, got{event, data})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []got{{"message_start", `{"a":1}`}, {"", "first\nsecond"}, {"", "[DONE]"}}
	if len(events) != len(want) {
		t.Fatalf("events %+v", events)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, events[i], want[i])
		}
	}
}

func TestReadEventsStopsAtTheHandlersError(t *testing.T) {
	stop := errors.New("stop")
	n := 0
	err := readEvents(strings.NewReader("data: 1\n\ndata: 2\n\ndata: 3\n\n"), func(string, string) error {
		n++
		if n == 2 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) || n != 2 {
		t.Errorf("read %d events, err %v", n, err)
	}
}

func TestReadEventsRefusesAnEventWithoutEnd(t *testing.T) {
	err := readEvents(strings.NewReader("data: "+strings.Repeat("x", maxEventBytes+10)), func(string, string) error { return nil })
	if err == nil {
		t.Error("an event past the bound was buffered")
	}
}
