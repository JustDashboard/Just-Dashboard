package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// The job routes are what turn a half-hour apt run from a request that hangs
// into something an operator can watch, leave, and come back to. These drive
// them through the real router with a real session.

func startTestJob(t *testing.T, s *Server, run jobs.Runner) jobs.Job {
	t.Helper()
	return s.modules.jobs.Start(jobs.Spec{Kind: "test", Title: "A test job"}, run)
}

func waitForJob(t *testing.T, s *Server, id string) jobs.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if job, _, ok := s.modules.jobs.Get(id); ok && job.Status != jobs.StatusRunning {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s never finished", id)
	return jobs.Job{}
}

func TestJobListAndGet(t *testing.T) {
	c, s := newClient(t)
	job := startTestJob(t, s, func(ctx context.Context, out jobs.Emitter) error {
		out.Line("stdout", "hello")
		return nil
	})
	waitForJob(t, s, job.ID)

	w := c.do(http.MethodGet, "/api/v1/jobs/", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list got %d: %s", w.Code, w.Body.String())
	}
	var list []jobs.Job
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) == 0 || list[0].ID != job.ID {
		t.Fatalf("list = %+v", list)
	}

	w = c.do(http.MethodGet, "/api/v1/jobs/"+job.ID, "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get got %d: %s", w.Code, w.Body.String())
	}
	var detail struct {
		Job   jobs.Job    `json:"job"`
		Lines []jobs.Line `json:"lines"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Job.Status != jobs.StatusSucceeded {
		t.Fatalf("status = %q", detail.Job.Status)
	}
	if len(detail.Lines) != 1 || detail.Lines[0].Text != "hello" {
		t.Fatalf("lines = %+v", detail.Lines)
	}
}

func TestJobGetRejectsAnUnknownID(t *testing.T) {
	c, _ := newClient(t)
	if w := c.do(http.MethodGet, "/api/v1/jobs/nope", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("got %d", w.Code)
	}
	if w := c.do(http.MethodGet, "/api/v1/jobs/nope/stream", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("stream got %d", w.Code)
	}
}

func TestJobCancel(t *testing.T) {
	c, s := newClient(t)
	job := startTestJob(t, s, func(ctx context.Context, out jobs.Emitter) error {
		<-ctx.Done()
		return ctx.Err()
	})
	w := c.do(http.MethodPost, "/api/v1/jobs/"+job.ID+"/cancel", `{}`, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	final := waitForJob(t, s, job.ID)
	if final.Status != jobs.StatusCancelled {
		t.Fatalf("status = %q", final.Status)
	}
	// Cancelling something already finished is a mistake worth reporting
	// rather than a silent no-op.
	if w := c.do(http.MethodPost, "/api/v1/jobs/"+job.ID+"/cancel", `{}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("second cancel got %d", w.Code)
	}
}

// Applying updates now answers 202 with a job rather than holding the
// connection open for up to half an hour.
func TestUpdatesApplyStartsAJob(t *testing.T) {
	c, s := newClient(t)
	w := c.do(http.MethodPost, "/api/v1/packages/upgrade", "",
		map[string]string{"X-Confirm": "upgrade packages"})
	if w.Code == http.StatusServiceUnavailable {
		t.Skip("no package manager on this host")
	}
	if w.Code != http.StatusAccepted {
		t.Fatalf("got %d, want 202: %s", w.Code, w.Body.String())
	}
	var job jobs.Job
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if job.ID == "" || job.Kind != "updates.apply" {
		t.Fatalf("job = %+v", job)
	}
	// Started by whoever asked, because a job outlives its request and the
	// audit entry alone would not say who owns something still running.
	if job.StartedBy != "tester" {
		t.Errorf("startedBy = %q", job.StartedBy)
	}
	s.modules.jobs.Cancel(job.ID)
}

// The confirmation still has to come first: making it a job must not become a
// way around the typed phrase.
func TestUpdatesApplyStillDemandsTheTypedPhrase(t *testing.T) {
	c, _ := newClient(t)
	w := c.do(http.MethodPost, "/api/v1/packages/upgrade", "", nil)
	if w.Code != http.StatusPreconditionRequired {
		t.Fatalf("got %d, want 428: %s", w.Code, w.Body.String())
	}
}

// An SSH apply plans synchronously — a lockout is the answer to the click, not
// a job that fails a second later.
func TestSSHApplyRefusesALockoutBeforeStartingAJob(t *testing.T) {
	c, s := newClient(t)
	before := len(s.modules.jobs.List())
	w := c.do(http.MethodPost, "/api/v1/ssh/config",
		`{"settings":{"passwordauthentication":"no","pubkeyauthentication":"no"}}`,
		map[string]string{"X-Confirm": "change ssh"})
	if w.Code != http.StatusConflict && w.Code != http.StatusBadRequest {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	if len(s.modules.jobs.List()) != before {
		t.Fatal("a job was started for a change that was refused")
	}
}

// Certificate issuance answers with a job, so the console can attach to the
// ACME exchange rather than the browser waiting on it.
func TestCertIssueStartsAJob(t *testing.T) {
	// No certbot on PATH, whatever this host has: a real one would try the
	// exchange for as long as it takes.
	t.Setenv("PATH", t.TempDir())
	t.Cleanup(proxysvc.UseCertificateDirsForTest(t.TempDir(), t.TempDir()))
	c, s := newClient(t)
	w := c.do(http.MethodPost, "/api/v1/certificates/issue",
		`{"domains":["example.com"],"email":"ops@example.com","method":"webroot","webRoot":"/var/www/html","staging":true}`,
		nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("got %d, want 202: %s", w.Code, w.Body.String())
	}
	var job jobs.Job
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if job.Kind != "certbot.issue" || !strings.Contains(job.Title, "example.com") {
		t.Fatalf("job = %+v", job)
	}
	// certbot is not installed here, so it fails — which is the point: the
	// failure is recorded on the job rather than lost with the request.
	final := waitForJob(t, s, job.ID)
	if final.Status == jobs.StatusRunning {
		t.Fatal("still running")
	}
	if final.Status == jobs.StatusFailed && final.Error == "" {
		t.Error("a failed job should say why")
	}
}

// Cancelling needs a capability, and reading does not — a readonly principal
// can watch an upgrade without being able to stop it.
func TestJobRoutesRespectCapabilities(t *testing.T) {
	s := testServer(t)
	job := startTestJob(t, s, func(ctx context.Context, out jobs.Emitter) error {
		<-ctx.Done()
		return ctx.Err()
	})
	defer s.modules.jobs.Cancel(job.ID)

	c := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "viewer", auth.RoleReadOnly)}
	if w := c.do(http.MethodGet, "/api/v1/jobs/"+job.ID, "", nil); w.Code != http.StatusOK {
		t.Errorf("readonly could not read a job: %d", w.Code)
	}
	if w := c.do(http.MethodPost, "/api/v1/jobs/"+job.ID+"/cancel", `{}`, nil); w.Code != http.StatusForbidden {
		t.Errorf("readonly cancel = %d, want 403", w.Code)
	}
}

// The end-to-end proof: a real WebSocket against a real server, watching a
// real job produce real output.
//
// Everything above tests the pieces. This tests the thing the operator
// actually does — open a console on something long-running and watch it — and
// it is the only test that would catch a frame shape the client cannot read.
func TestJobStreamDeliversOutputAndTheFinalState(t *testing.T) {
	s := testServer(t)
	cookie := signIn(t, s)
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()

	release := make(chan struct{})
	job := startTestJob(t, s, func(ctx context.Context, out jobs.Emitter) error {
		out.Status("starting")
		out.Line("stdout", "first")
		<-release
		out.Line("stdout", "second")
		return nil
	})

	conn := dialJobStream(t, srv, cookie, job.ID, 0)
	defer conn.Close()

	// The job comes first, so a client attaching to something already finished
	// sees the outcome before the output rather than after it.
	if kind, payload := readFrame(t, conn); kind != "job" {
		t.Fatalf("first frame was %q: %s", kind, payload)
	}

	var got []jobs.Line
	var final jobs.Job
	released := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		kind, payload := readFrame(t, conn)
		switch kind {
		case "output":
			var batch []jobs.Line
			if err := json.Unmarshal(payload, &batch); err != nil {
				t.Fatalf("output frame is not a line batch: %v", err)
			}
			got = append(got, batch...)
			if !released && len(got) >= 2 {
				// Only once the first half has actually arrived, so this
				// proves the lines were streamed rather than sent in one go
				// at the end.
				released = true
				close(release)
			}
		case "job":
			if err := json.Unmarshal(payload, &final); err != nil {
				t.Fatal(err)
			}
			if final.Status != jobs.StatusRunning {
				goto done
			}
		}
	}
	t.Fatal("timed out before the job reported a final state")

done:
	if !released {
		t.Fatal("the job finished before the first lines arrived")
	}
	if final.Status != jobs.StatusSucceeded {
		t.Fatalf("final status = %q, error %q", final.Status, final.Error)
	}
	var texts []string
	for _, l := range got {
		texts = append(texts, l.Text)
	}
	joined := strings.Join(texts, "|")
	for _, want := range []string{"starting", "first", "second"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q from %q", want, joined)
		}
	}
}

// Reconnecting is the case the compose runner cannot handle: there, the socket
// owns the command and reattaching would run it again. Here the job is already
// running, so a second client picks up exactly where the first left off.
func TestJobStreamResumesAfterASequence(t *testing.T) {
	s := testServer(t)
	cookie := signIn(t, s)
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()

	job := startTestJob(t, s, func(ctx context.Context, out jobs.Emitter) error {
		for i := 1; i <= 4; i++ {
			out.Line("stdout", "line "+strconv.Itoa(i))
		}
		return nil
	})
	waitForJob(t, s, job.ID)

	conn := dialJobStream(t, srv, cookie, job.ID, 2)
	defer conn.Close()
	if kind, _ := readFrame(t, conn); kind != "job" {
		t.Fatalf("first frame was %q", kind)
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	kind, payload := readFrame(t, conn)
	if kind != "output" {
		t.Fatalf("expected a backlog, got %q", kind)
	}
	var backlog []jobs.Line
	if err := json.Unmarshal(payload, &backlog); err != nil {
		t.Fatal(err)
	}
	if len(backlog) != 2 || backlog[0].Text != "line 3" {
		t.Fatalf("resumed wrongly: %+v", backlog)
	}
}

func dialJobStream(t *testing.T, srv *httptest.Server, cookie, id string, after int) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") +
		"/api/v1/jobs/" + id + "/stream?after=" + strconv.Itoa(after)
	conn, resp, err := websocket.DefaultDialer.Dial(url, http.Header{"Cookie": {cookie}})
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("could not open the stream (%d): %v", status, err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	return conn
}

// readFrame reads one envelope. The shape is wsx's, and reading it here rather
// than trusting it is the point of an end-to-end test.
func readFrame(t *testing.T, conn *websocket.Conn) (string, json.RawMessage) {
	t.Helper()
	var envelope struct {
		Type  string          `json:"type"`
		Data  json.RawMessage `json:"data"`
		Error string          `json:"error"`
	}
	if err := conn.ReadJSON(&envelope); err != nil {
		t.Fatalf("reading a frame: %v", err)
	}
	if envelope.Error != "" {
		t.Fatalf("stream reported an error: %s", envelope.Error)
	}
	return envelope.Type, envelope.Data
}
