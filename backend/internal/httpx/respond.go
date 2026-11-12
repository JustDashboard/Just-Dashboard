package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/storehealth"
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

	// Field is a pointer a UI control can attach itself to — "runtime.internalPort",
	// "checks[2].kind" — set only when the failure traces back to one field of
	// the request rather than the request as a whole.
	Field string `json:"field,omitempty"`

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
	// RequestID is the id of the request that failed, which is also in the
	// X-Request-Id header and on the log line that holds the cause. WriteError
	// sets it on a 5xx, where the message deliberately says nothing.
	RequestID string `json:"requestId,omitempty"`

	err error
	// retryAfter is how long until the same request would be admitted, for a
	// refusal by a rate limit; WriteError says it in Retry-After.
	retryAfter time.Duration
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
		WriteError(w, r, mapped(r, err))
	}
}

// DiskFull is the answer when the dashboard's own store was refused space. It
// is said in full: "internal error" on the sign-in page told an operator whose
// disk had filled nothing, at the moment the dashboard was the tool they
// needed to find what filled it.
func DiskFull(err error) *APIError {
	return &APIError{
		Status: http.StatusInsufficientStorage, Code: "disk_full",
		Message: "The server's disk is full, so the dashboard could not save this. Free some space on the server and try again.",
		err:     err,
	}
}

func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		// An error no handler named: the server's own failures have one
		// answer each (failures.go), and what is left is hidden as before.
		if apiErr = serverFailure(err); apiErr == nil {
			apiErr = Internal(err)
		}
	}
	// Whether a handler returned the store's error bare or wrapped it as
	// Internal itself, a write refused for space is not an unexplained 500.
	if apiErr.Status == http.StatusInternalServerError && apiErr.Code == "internal" && storehealth.IsDiskFull(err) {
		apiErr = DiskFull(err)
	}
	// A handler's own deadline, on a request whose caller is still waiting,
	// is 504 timeout whatever the mapper on the way made of the error: a
	// process tree that could not be read in 30 seconds was a 404, a grant a
	// 502 with "context deadline exceeded" for a sentence. Only an error that
	// still carries its cause is read (Wrap, Internal, Refused, a bare one),
	// and one a handler already marked as worth another try keeps its words.
	if !apiErr.Retryable && errors.Is(err, context.DeadlineExceeded) && r.Context().Err() == nil {
		late := Timeout(err)
		late.Operation, late.Resource = apiErr.Operation, apiErr.Resource
		apiErr = late
	}
	if apiErr.Status >= 500 {
		id := RequestIDFrom(r.Context())
		if requestGone(r, err) {
			// The caller left and the work was stopped for it: not a failure
			// of the server, and an ERROR for each closed tab buries the ones
			// that are.
			slog.Debug("request abandoned", "method", r.Method, "path", r.URL.Path, "request", id, "err", apiErr.Error())
		} else {
			slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "request", id, "err", apiErr.Error())
		}
		if id != "" {
			// A copy: the error may be one of the package's shared values.
			named := *apiErr
			named.RequestID = id
			apiErr = &named
		}
	}
	if p, ok := PrincipalFrom(r.Context()); ok {
		p.FailureReason = apiErr.Message
	}
	if apiErr.Status == http.StatusTooManyRequests && apiErr.retryAfter > 0 {
		// The real wait. It used to be a flat 30 seconds for a bucket that
		// refills in a tenth of one.
		w.Header().Set("Retry-After", retryAfterSeconds(apiErr.retryAfter))
		w.Header().Set("RateLimit-Remaining", "0")
	}
	// A refusal is often written before the body was read. What is left of
	// it is read here, within the client's allowance, so the server does not
	// wait on it without one (drainBody).
	drainBody(r)
	JSON(w, apiErr.Status, map[string]any{"error": apiErr})
}

// maxJSONBody bounds a JSON request body, unless a route says otherwise.
const maxJSONBody = 4 << 20

func DecodeJSON(r *http.Request, dst any) error {
	return decodeJSON(r, dst, false, maxJSONBody)
}

// DecodeJSONWithin is DecodeJSON for a route whose body may be larger, such as a
// question to the assistant with screenshots in it.
func DecodeJSONWithin(r *http.Request, dst any, limit int64) error {
	return decodeJSON(r, dst, false, max(limit, maxJSONBody))
}

// DecodeJSONNumbers keeps arbitrary database values exact until the database
// adapter binds them. JSON numbers otherwise round through float64 in any fields.
func DecodeJSONNumbers(r *http.Request, dst any) error {
	return decodeJSON(r, dst, true, maxJSONBody)
}

func decodeJSON(r *http.Request, dst any, exactNumbers bool, limit int64) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return Err(http.StatusUnsupportedMediaType, "json_content_type_required",
			"request body must use application/json")
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, limit))
	if exactNumbers {
		dec.UseNumber()
	}
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return decodeRefusal(err, limit)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if tooLarge := (*http.MaxBytesError)(nil); errors.As(err, &tooLarge) {
			return decodeRefusal(err, limit)
		}
		return BadRequest("request body must contain exactly one JSON value")
	}
	return nil
}

// decodeRefusal says what was wrong with a body in the request's own terms.
//
// encoding/json's sentences are written for the programmer holding the struct:
// "cannot unmarshal number into Go struct field updateProfileRequest.displayName
// of type string" names a Go type the client never sees and no field the form
// could mark. So the refusals a client can act on are restated, each with the
// field it is about: a value of the wrong kind, a field the route does not
// take, a body that is missing, and a body past the limit, which is a 413 and
// not a malformed request. Anything else keeps the decoder's own words, which
// for a syntax error are the offending character and nothing of ours.
func decodeRefusal(err error, limit int64) *APIError {
	var tooLarge *http.MaxBytesError
	var wrongKind *json.UnmarshalTypeError
	var said *APIError
	switch {
	case errors.As(err, &said):
		// The body's own reader refused: a client that stopped sending is
		// 408 body_timeout (BodyDeadline), not a malformed request.
		return said
	case errors.As(err, &tooLarge):
		return Err(http.StatusRequestEntityTooLarge, "body_too_large",
			"the request body is larger than the "+byteLimit(limit)+" this route accepts")
	case errors.Is(err, io.EOF):
		return BadRequest("a JSON body is required")
	case errors.Is(err, io.ErrUnexpectedEOF):
		return BadRequest("malformed request body: it ends before the JSON value does")
	case errors.As(err, &wrongKind):
		got := jsonValueKind(wrongKind.Value)
		if wrongKind.Field == "" {
			return BadRequest("the request body must be %s, not %s", jsonKindWanted(wrongKind.Type), got)
		}
		refusal := BadRequest("%s must be %s, not %s", wrongKind.Field, jsonKindWanted(wrongKind.Type), got)
		refusal.Field = wrongKind.Field
		return refusal
	}
	if name, ok := unknownJSONField(err); ok {
		refusal := BadRequest("unknown field %q", name)
		refusal.Field = name
		return refusal
	}
	return BadRequest("malformed request body: %v", err)
}

// unknownJSONField is the field a DisallowUnknownFields refusal names. The
// decoder has no type for it, only the sentence.
func unknownJSONField(err error) (string, bool) {
	quoted, ok := strings.CutPrefix(err.Error(), "json: unknown field ")
	if !ok {
		return "", false
	}
	name, unquoteErr := strconv.Unquote(quoted)
	return name, unquoteErr == nil
}

// jsonKindWanted names the JSON a Go type is decoded from.
func jsonKindWanted(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "true or false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "a whole number"
	case reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.Slice, reflect.Array:
		return "a list"
	case reflect.Map, reflect.Struct:
		return "an object"
	}
	return "a different kind of value"
}

// jsonValueKind names what the body held, from the decoder's description of it
// ("number", "number 1.5", "string", "array", "object", "bool").
func jsonValueKind(value string) string {
	kind, _, _ := strings.Cut(value, " ")
	switch kind {
	case "number":
		// "number 1.5" into a whole number, "number 300" into a small one.
		if value != kind {
			return "the " + value
		}
		return "a number"
	case "string":
		return "a string"
	case "array":
		return "a list"
	case "object":
		return "an object"
	case "bool":
		return "true or false"
	}
	return "what was sent"
}

// byteLimit writes a body limit the way an operator would say it.
func byteLimit(n int64) string {
	const mb = 1 << 20
	if n >= mb && n%mb == 0 {
		return strconv.FormatInt(n/mb, 10) + " MB"
	}
	if n >= 1<<10 {
		return strconv.FormatInt(n>>10, 10) + " KB"
	}
	return strconv.FormatInt(n, 10) + " bytes"
}
