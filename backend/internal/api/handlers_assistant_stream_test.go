package api

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A press that says nothing for a while — a command running, a terminal's wait,
// a model thinking — still writes a line now and then, or a proxy in front of
// the dashboard closes the connection and the page reads the answer as failed.
// The line is not kept in the saved chat, and nothing is written before the
// answer has begun or after keepAlive has stopped.
func TestAssistantStreamKeepsAQuietAnswerAlive(t *testing.T) {
	rec := httptest.NewRecorder()
	var kept []string
	st := &assistantStream{w: rec, keep: func(line []byte) { kept = append(kept, string(line)) }, start: map[string]any{"type": "start"}}

	stop := st.keepAlive(20 * time.Millisecond)
	time.Sleep(80 * time.Millisecond)
	st.mu.Lock()
	before := rec.Body.Len()
	st.mu.Unlock()
	if before != 0 {
		t.Fatalf("wrote %d bytes before the answer began", before)
	}

	st.send(map[string]any{"type": "thinking"})
	sent := time.Now()
	waitFor(t, "two pings in a 20ms quiet", func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		return strings.Count(rec.Body.String(), `{"type":"ping"}`) >= 2
	})
	// However loaded the machine, two pings of a 20ms quiet come long before
	// this; a stream that ignored the quiet it was given and kept the 15s one
	// would take half a minute, which waitFor alone would still wait out.
	if waited := time.Since(sent); waited > 5*time.Second {
		t.Fatalf("two pings of a 20ms quiet took %s", waited.Round(time.Millisecond))
	}
	stop()
	after := rec.Body.String()
	for _, line := range strings.Split(strings.TrimSpace(after), "\n") {
		if !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
			t.Fatalf("a line is not whole JSON: %q", line)
		}
	}
	for _, line := range kept {
		if strings.Contains(line, "ping") {
			t.Fatalf("the saved chat kept a ping: %v", kept)
		}
	}
	if len(kept) != 2 {
		t.Fatalf("kept = %v, want the start and the thinking line", kept)
	}

	time.Sleep(60 * time.Millisecond)
	if rec.Body.String() != after {
		t.Fatal("a ping was written after keepAlive stopped")
	}
}

// A stream that is talking is not interrupted.
func TestAssistantStreamDoesNotPingWhileTalking(t *testing.T) {
	rec := httptest.NewRecorder()
	st := &assistantStream{w: rec, start: map[string]any{"type": "start"}}
	stop := st.keepAlive(60 * time.Millisecond)
	for range 10 {
		st.send(map[string]any{"type": "text", "text": "a"})
		time.Sleep(15 * time.Millisecond)
	}
	stop()
	if strings.Contains(rec.Body.String(), "ping") {
		t.Fatalf("pinged while lines were going out:\n%s", rec.Body.String())
	}
}
