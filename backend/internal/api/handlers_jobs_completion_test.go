package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
)

func TestJobStreamClosesAfterCompletedBacklog(t *testing.T) {
	s := testServer(t)
	cookie := signIn(t, s)
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()
	job := startTestJob(t, s, func(_ context.Context, out jobs.Emitter) error {
		out.Line("stdout", "complete")
		return nil
	})
	waitForJob(t, s, job.ID)
	conn := dialJobStream(t, srv, cookie, job.ID, 0)
	defer conn.Close()
	for _, want := range []string{"job", "output", "job"} {
		kind, payload := readFrame(t, conn)
		if kind != want {
			t.Fatalf("frame = %q, want %q", kind, want)
		}
		if kind == "output" {
			var lines []jobs.Line
			if err := json.Unmarshal(payload, &lines); err != nil {
				t.Fatal(err)
			}
			if len(lines) != 1 || lines[0].Text != "complete" {
				t.Fatalf("lost completed output: %+v", lines)
			}
		}
	}
	conn.SetReadDeadline(time.Now().Add(time.Second))
	_, _, err := conn.ReadMessage()
	var networkError net.Error
	if err == nil || (errors.As(err, &networkError) && networkError.Timeout()) {
		t.Fatalf("completed stream did not close: %v", err)
	}
}
