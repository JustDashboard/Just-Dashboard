package httpx

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/Wayy01/Just-Dashboard/backend/internal/storehealth"
)

// Three failures are not about the request, and each has one answer wherever
// it happens: the server lacks something the work needs, the work did not
// finish inside the time its handler gave it, and the host itself failed.
// They used to be whatever the nearest mapper's last branch said, which was
// usually 400 bad_request: a client was told not to try again, and nothing was
// logged because a 400 is the caller's mistake.

// Absent is the answer for work that needs something this server does not
// have: a program that is not installed, an init system that is not running.
// It is 503 not_installed, the code the page already shows as "not on this
// server" rather than as something broken. sentence is the whole message;
// what names the missing thing in error.resource, and the subsystem's own
// words stay in error.raw.
func Absent(what, sentence string, err error) *APIError {
	out := &APIError{
		Status: http.StatusServiceUnavailable, Code: "not_installed",
		Message: sentence, Resource: what, err: err,
	}
	if err != nil {
		out.Raw = err.Error()
	}
	return out
}

// NotInstalled is Absent for a program.
func NotInstalled(program string, err error) *APIError {
	return Absent(program, program+" is not installed on this server.", err)
}

// Timeout is the answer for work that ran out of the time its handler gave
// it: 504 timeout, worth another try. The server is slow or busy; the request
// was not wrong and the resource is not missing.
func Timeout(err error) *APIError {
	return &APIError{
		Status: http.StatusGatewayTimeout, Code: "timeout",
		Message:   "The server did not finish this in time.",
		Retryable: true, err: err,
	}
}

// hostFailures are the errors of the machine rather than of what was asked of
// it. "No such file" and "is a directory" are about the request and stay with
// the mapper that knows the area.
var hostFailures = map[syscall.Errno]bool{
	syscall.EIO:    true,
	syscall.EMFILE: true,
	syscall.ENFILE: true,
	syscall.ENOMEM: true,
}

// serverFailure names an error that is not about the request, or returns nil
// for one that may be. A cancellation is not named: work a person stopped on
// purpose (a query, a job) comes back cancelled too, and its handler says so.
func serverFailure(err error) *APIError {
	var missing *exec.Error
	var errno syscall.Errno
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return Timeout(err)
	case storehealth.IsDiskFull(err):
		return DiskFull(err)
	case errors.As(err, &missing) && errors.Is(missing.Err, exec.ErrNotFound):
		return NotInstalled(filepath.Base(missing.Name), err)
	case errors.As(err, &errno) && hostFailures[errno]:
		// Said, where Internal says nothing: the person at a file manager
		// whose disk is failing needs the two words the kernel gave.
		return &APIError{
			Status: http.StatusInternalServerError, Code: "host_failure",
			Message: "The server could not do this: " + errno.Error() + ".", err: err,
		}
	}
	return nil
}

// Refused is the last branch of an error mapper, and the answer for any error
// a handler has no name for: the request is refused with the error's own
// sentence, 400 bad_request, as httpx.BadRequest("%v", err) did. The
// difference is what it does not call a bad request. An error that is already
// an APIError keeps its status, and a deadline, a missing program, a full disk
// or a failing host is answered as what it is (Timeout, NotInstalled, DiskFull,
// host_failure), so it reaches the log and the client knows whether to retry.
//
// TestNoHandlerCallsAServerFailureABadRequest keeps BadRequest("%v", err) out
// of the handlers.
func Refused(err error) *APIError {
	var api *APIError
	if errors.As(err, &api) {
		return api
	}
	if known := serverFailure(err); known != nil {
		return known
	}
	return &APIError{Status: http.StatusBadRequest, Code: "bad_request", Message: err.Error(), err: err}
}

// requestGone reports a request whose caller has left: nobody reads the
// answer, and the failure is the leaving itself.
func requestGone(r *http.Request, err error) bool {
	return errors.Is(r.Context().Err(), context.Canceled) &&
		(errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
}

type errorMapKey struct{}

// MapErrors is middleware for routes that share a cause of failure the
// handlers themselves cannot see: every route that drives nginx fails on a
// server without it, each in its own way (a 400 from the settings, a 404 from
// the file tree, a 409 from the resolver). fn is given the error a handler
// under it returned, before it is written, and returns the error to answer
// with. It runs only when a handler failed, so the routes pay nothing for it.
func MapErrors(fn func(error) error) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), errorMapKey{}, fn)))
		})
	}
}

// mapped is err as the request's MapErrors, if it has one, would answer it.
func mapped(r *http.Request, err error) error {
	if fn, ok := r.Context().Value(errorMapKey{}).(func(error) error); ok {
		if out := fn(err); out != nil {
			return out
		}
	}
	return err
}
