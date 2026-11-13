package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// answerOn is answerTo for a request the test made itself.
func answerOn(t *testing.T, r *http.Request, err error) (int, APIError) {
	t.Helper()
	rec := httptest.NewRecorder()
	WriteError(rec, r, err)
	var body struct {
		Error APIError `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return rec.Code, body.Error
}

// passedDeadline is the error of work that outlived the time it was given, as
// a handler gets it back from a command or a query.
func passedDeadline(t *testing.T) error {
	t.Helper()
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	return fmt.Errorf("read the process tree: %w", ctx.Err())
}

// A handler's own deadline is one answer on every route. Each of these was
// what a real route said to a slow host: a 404 for a process tree, a 502 with
// "context deadline exceeded" as its sentence for a grant, a 400 from a
// mapper's last branch, a 500.
func TestAHandlersOwnDeadlineIsOneAnswerWhateverTheMapperChose(t *testing.T) {
	late := passedDeadline(t)
	for name, err := range map[string]error{
		"bare":                 late,
		"internal":             Internal(late),
		"a subsystem's 502":    Wrap(http.StatusBadGateway, "query_failed", late),
		"a mapper's fallback":  Refused(late),
		"a wrapped bad answer": Wrap(http.StatusNotFound, "not_found", late),
	} {
		status, body := answerTo(t, err)
		if status != http.StatusGatewayTimeout || body.Code != "timeout" || !body.Retryable {
			t.Errorf("%s: answered %d %q retryable=%v, want 504 timeout worth another try", name, status, body.Code, body.Retryable)
		}
		if strings.Contains(body.Message, "context deadline exceeded") {
			t.Errorf("%s: the message is Go's words: %q", name, body.Message)
		}
	}
	// What was being done is kept on the new answer.
	_, body := answerTo(t, Wrap(http.StatusBadGateway, "query_failed", late).Describe("grant", "role app"))
	if body.Operation != "grant" || body.Resource != "role app" {
		t.Errorf("the timeout lost what was being done: %+v", body)
	}
}

// A handler that looked at the deadline itself and wrote its own sentence, or
// marked its answer as worth another try, keeps both.
func TestADeadlineAHandlerAlreadyAnsweredKeepsItsWords(t *testing.T) {
	late := passedDeadline(t)
	status, body := answerTo(t, Err(http.StatusServiceUnavailable, "busy", "nginx could not be asked in time.").Retry())
	if status != http.StatusServiceUnavailable || body.Code != "busy" {
		t.Errorf("a handler's own sentence answered %d %q", status, body.Code)
	}
	status, body = answerTo(t, Wrap(http.StatusBadGateway, "test_unfinished", late).Retry())
	if status != http.StatusBadGateway || body.Code != "test_unfinished" {
		t.Errorf("an answer already marked retryable answered %d %q", status, body.Code)
	}
}

// When the caller has left, the work was stopped for it: that is neither a
// timeout to report nor a failure of the server to log as one.
func TestARequestItsCallerAbandonedIsNotLoggedAsAFailure(t *testing.T) {
	logged := captureDefaultLog(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	gone := httptest.NewRequest(http.MethodGet, "/api/v1/procs/1/tree", nil).WithContext(ctx)

	for _, err := range []error{
		fmt.Errorf("ps: %w", context.Canceled),
		Internal(fmt.Errorf("ps: %w", ctx.Err())),
		passedDeadline(t),
	} {
		answerOn(t, gone, err)
	}
	if strings.Contains(logged.String(), `"level":"ERROR"`) {
		t.Errorf("an abandoned request was logged as a failure:\n%s", logged.String())
	}
	if got := strings.Count(logged.String(), `"msg":"request abandoned"`); got != 3 {
		t.Errorf("%d abandoned requests on the debug log, want 3:\n%s", got, logged.String())
	}

	// A real failure on a request whose caller left is still one, and a
	// cancellation on a request that is still there is too.
	logged.Reset()
	answerOn(t, gone, Internal(errors.New("disk on fire")))
	answerTo(t, fmt.Errorf("ps: %w", context.Canceled))
	if got := strings.Count(logged.String(), `"level":"ERROR"`); got != 2 {
		t.Errorf("%d failures logged, want 2:\n%s", got, logged.String())
	}
}

// The last branch of a mapper refuses the request with the error's sentence,
// as BadRequest("%v", err) did, except for what is not about the request.
func TestAMappersLastBranchDoesNotBlameTheRequestForTheServer(t *testing.T) {
	_, missing := exec.LookPath("a-program-nobody-installed-for-this-test")
	if missing == nil {
		t.Fatal("the missing program was found")
	}
	for name, tt := range map[string]struct {
		err    error
		status int
		code   string
		logged bool
	}{
		"a validation sentence":  {errors.New("name must not be empty"), http.StatusBadRequest, "bad_request", false},
		"a path that is not one": {&fs.PathError{Op: "open", Path: "/srv/app", Err: syscall.EISDIR}, http.StatusBadRequest, "bad_request", false},
		"an answer already made": {fmt.Errorf("save: %w", Err(http.StatusConflict, "name_taken", "taken")), http.StatusConflict, "name_taken", false},
		"a deadline":             {passedDeadline(t), http.StatusGatewayTimeout, "timeout", true},
		"a missing program":      {fmt.Errorf("nginx -T: %w", missing), http.StatusServiceUnavailable, "not_installed", true},
		"a full disk":            {&fs.PathError{Op: "write", Path: "/srv/app/.env", Err: syscall.ENOSPC}, http.StatusInsufficientStorage, "disk_full", true},
		"a failing disk":         {&fs.PathError{Op: "read", Path: "/srv/app/.env", Err: syscall.EIO}, http.StatusInternalServerError, "host_failure", true},
		"no descriptors left":    {&fs.PathError{Op: "open", Path: "/srv/app/.env", Err: syscall.EMFILE}, http.StatusInternalServerError, "host_failure", true},
	} {
		logged := captureDefaultLog(t)
		status, body := answerTo(t, Refused(tt.err))
		if status != tt.status || body.Code != tt.code {
			t.Errorf("%s: answered %d %q, want %d %q", name, status, body.Code, tt.status, tt.code)
		}
		if got := strings.Contains(logged.String(), `"msg":"request failed"`); got != tt.logged {
			t.Errorf("%s: logged = %v, want %v", name, got, tt.logged)
		}
	}

	_, body := answerTo(t, Refused(errors.New("name must not be empty")))
	if body.Message != "name must not be empty" {
		t.Errorf("a refusal lost its sentence: %q", body.Message)
	}
	_, body = answerTo(t, Refused(fmt.Errorf("nginx -T: %w", missing)))
	if body.Resource != "a-program-nobody-installed-for-this-test" || !strings.Contains(body.Raw, "nginx -T") ||
		body.Message != "a-program-nobody-installed-for-this-test is not installed on this server." {
		t.Errorf("a missing program is not named: %+v", body)
	}
	_, body = answerTo(t, Refused(&fs.PathError{Op: "read", Path: "/srv/app/.env", Err: syscall.EIO}))
	if body.Message != "The server could not do this: input/output error." {
		t.Errorf("a failing disk said %q", body.Message)
	}
}

// The same failures returned bare, with no mapper at all, are named too; an
// error nothing recognises is hidden as it always was.
func TestABareServerFailureIsNamed(t *testing.T) {
	_, missing := exec.LookPath("a-program-nobody-installed-for-this-test")
	if status, body := answerTo(t, fmt.Errorf("systemctl: %w", missing)); status != http.StatusServiceUnavailable || body.Code != "not_installed" {
		t.Errorf("a missing program answered %d %q", status, body.Code)
	}
	if status, body := answerTo(t, errors.New("boom")); status != http.StatusInternalServerError || body.Code != "internal" {
		t.Errorf("an unknown error answered %d %q", status, body.Code)
	}
}

// A group's MapErrors sees what a handler under it returned and nothing else.
func TestMapErrorsAnswersForTheHandlersUnderIt(t *testing.T) {
	noNginx := MapErrors(func(err error) error { return NotInstalled("nginx", err) })
	failing := Handler(func(http.ResponseWriter, *http.Request) error {
		return Err(http.StatusNotFound, "not_found", "The nginx configuration directory is not there.")
	})
	working := Handler(func(w http.ResponseWriter, _ *http.Request) error {
		JSON(w, http.StatusOK, map[string]bool{"ok": true})
		return nil
	})

	rec := httptest.NewRecorder()
	noNginx(failing).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/proxy/files", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"not_installed"`) ||
		!strings.Contains(rec.Body.String(), `"resource":"nginx"`) {
		t.Errorf("a failure under the group answered %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	noNginx(working).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/proxy/files", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("a handler that worked answered %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	failing.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/proxy/files", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("a handler outside the group answered %d", rec.Code)
	}
}
