package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
)

// APIError carries an HTTP status alongside a message safe to show the client.
type APIError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
	// Phrase is the exact text a client must echo in X-Confirm. It is sent as
	// its own field because the client used to recover it by matching the
	// first quoted run in Message, which broke on any phrase containing a
	// double quote — a file named `my"file` could not be deleted from the UI
	// at all, because the dialog asked for `my` and the server wanted the
	// whole name.
	Phrase string `json:"phrase,omitempty"`

	// What the request was trying to do, to what, and why it did not work.
	//
	// "Something went wrong" is what a client shows when the server sent it a
	// sentence and nothing else. These fields let it show the sentence a
	// person would have written — which resource, which operation, the likely
	// reason, and the raw error from the subsystem underneath, kept for the
	// expandable details rather than pasted into the headline.
	Resource  string `json:"resource,omitempty"`
	Operation string `json:"operation,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Raw       string `json:"raw,omitempty"`
	// Retryable marks an error worth offering a retry for: a timeout or a
	// daemon that was briefly unreachable, as opposed to a request that will
	// fail identically every time.
	Retryable bool `json:"retryable,omitempty"`

	err error
}

// Describe attaches what was being attempted to an error.
//
// Chained rather than set at construction because the layer that knows the
// resource and the operation is usually the handler, and the layer that
// produced the error is usually below it.
func (e *APIError) Describe(operation, resource string) *APIError {
	out := *e
	out.Operation, out.Resource = operation, resource
	return &out
}

// Because records the likely reason and the raw error behind it.
func (e *APIError) Because(reason, raw string) *APIError {
	out := *e
	out.Reason, out.Raw = reason, raw
	return &out
}

// Retry marks the error as worth trying again.
func (e *APIError) Retry() *APIError {
	out := *e
	out.Retryable = true
	return &out
}

func (e *APIError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return e.Message
}

func (e *APIError) Unwrap() error { return e.err }

func Err(status int, code, msg string) *APIError {
	return &APIError{Status: status, Code: code, Message: msg}
}

func Wrap(status int, code string, err error) *APIError {
	return &APIError{Status: status, Code: code, Message: err.Error(), err: err}
}

var (
	ErrUnauthorized = Err(http.StatusUnauthorized, "unauthorized", "authentication required")
	ErrForbidden    = Err(http.StatusForbidden, "forbidden", "your role does not permit this action")
	ErrNotFound     = Err(http.StatusNotFound, "not_found", "not found")
)

func BadRequest(format string, a ...any) *APIError {
	return Err(http.StatusBadRequest, "bad_request", fmt.Sprintf(format, a...))
}

func Internal(err error) *APIError {
	return &APIError{Status: http.StatusInternalServerError, Code: "internal", Message: "internal error", err: err}
}

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write json response", "err", err)
	}
}

func NoContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

// Handler is a http.Handler whose errors are rendered centrally, so no route
// can accidentally leak an internal error string to the client.
type Handler func(w http.ResponseWriter, r *http.Request) error

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := h(w, r); err != nil {
		WriteError(w, r, err)
	}
}

func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		apiErr = Internal(err)
	}
	if apiErr.Status >= 500 {
		slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", apiErr.Error())
	}
	if p, ok := PrincipalFrom(r.Context()); ok {
		p.FailureReason = apiErr.Message
	}
	JSON(w, apiErr.Status, map[string]any{"error": apiErr})
}

func DecodeJSON(r *http.Request, dst any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return Err(http.StatusUnsupportedMediaType, "json_content_type_required",
			"request body must use application/json")
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return BadRequest("malformed request body: %v", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return BadRequest("request body must contain exactly one JSON value")
	}
	return nil
}
