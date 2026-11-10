package httpx

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Query parameters are read through this file, and a value that is there and
// cannot be read is a 400 that names the parameter, never the default.
//
// The rule exists because of what the default did. A boolean flag used to be
// read three ways (`== "true"` on most routes, `== "1"` on some, either on a
// few), so ?preview=true on a route that tested for "1" was not a preview: it
// granted. A filter that did not parse was dropped the same way, and
// ?since=tomorrow on the audit log answered the whole log with a 200. Every
// reader here answers the same thing for the same spelling on every route.

// QueryBool reads a flag that is off when it is absent.
//
// It is 1, 0, true or false, in any case. Anything else is refused: "yes", an
// empty value, and the flag given twice, which says two things at once. A
// preview or a dry run that failed to parse must never fall through to the
// change it was meant to show.
func QueryBool(r *http.Request, name string) (bool, error) {
	return QueryBoolOr(r, name, false)
}

// QueryBoolOr is QueryBool for a flag whose absence means def.
func QueryBoolOr(r *http.Request, name string, def bool) (bool, error) {
	return queryBool(r.URL.Query(), name, def)
}

// QueryInt reads a whole number from lo to hi, def when it is absent or
// empty. A value that is not a number, or is outside the range, is refused
// rather than replaced: a page asked for with limit=abc is not the first page.
func QueryInt(r *http.Request, name string, def, lo, hi int) (int, error) {
	return queryInt(r.URL.Query(), name, def, lo, hi)
}

// QueryTime reads an RFC 3339 instant. ok is false when the parameter is absent
// or empty; a value that is there and is not an instant is refused, so a time
// filter is applied or the request fails, and is never quietly left out.
func QueryTime(r *http.Request, name string) (at time.Time, ok bool, err error) {
	return queryTime(r.URL.Query(), name)
}

// QueryTimeOf is QueryTime over values a helper was handed in place of the
// request.
func QueryTimeOf(values url.Values, name string) (at time.Time, ok bool, err error) {
	return queryTime(values, name)
}

// QueryReader reads several parameters of one request and keeps the first
// refusal, for a handler that fills a struct of options from the address.
// Every reader answers the zero value (or its default) after a refusal, so the
// caller must ask Err before using what it read; a test in the api package
// holds every function that makes a reader to that.
type QueryReader struct {
	values url.Values
	err    error
}

// Query is a reader over r's query string.
func Query(r *http.Request) *QueryReader {
	return &QueryReader{values: r.URL.Query()}
}

// QueryOf is a reader over values a helper was handed in place of the request.
func QueryOf(values url.Values) *QueryReader {
	return &QueryReader{values: values}
}

// Err is the first parameter that could not be read, as a 400 naming it.
func (q *QueryReader) Err() error { return q.err }

func (q *QueryReader) keep(err error) {
	if q.err == nil {
		q.err = err
	}
}

// Bool is QueryBool.
func (q *QueryReader) Bool(name string) bool { return q.BoolOr(name, false) }

// BoolOr is QueryBoolOr.
func (q *QueryReader) BoolOr(name string, def bool) bool {
	v, err := queryBool(q.values, name, def)
	if err != nil {
		q.keep(err)
		return def
	}
	return v
}

// Int is QueryInt.
func (q *QueryReader) Int(name string, def, lo, hi int) int {
	v, err := queryInt(q.values, name, def, lo, hi)
	if err != nil {
		q.keep(err)
		return def
	}
	return v
}

// Time is QueryTime, the zero time when the parameter is absent.
func (q *QueryReader) Time(name string) time.Time {
	at, _, err := queryTime(q.values, name)
	if err != nil {
		q.keep(err)
		return time.Time{}
	}
	return at
}

// String is the parameter as it was sent, for the fields read beside the
// typed ones.
func (q *QueryReader) String(name string) string { return q.values.Get(name) }

func queryBool(values url.Values, name string, def bool) (bool, error) {
	given, ok := values[name]
	if !ok {
		return def, nil
	}
	if len(given) > 1 {
		return def, queryRefusal(name, "%s is given more than once", name)
	}
	switch strings.ToLower(given[0]) {
	case "1", "true":
		return true, nil
	case "0", "false":
		return false, nil
	}
	return def, queryRefusal(name, "%s must be 1, 0, true or false", name)
}

func queryInt(values url.Values, name string, def, lo, hi int) (int, error) {
	raw, err := queryOne(values, name)
	if err != nil || raw == "" {
		return def, err
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def, queryRefusal(name, "%s must be a whole number", name)
	}
	if n < lo || n > hi {
		return def, queryRefusal(name, "%s must be between %d and %d", name, lo, hi)
	}
	return n, nil
}

func queryTime(values url.Values, name string) (time.Time, bool, error) {
	raw, err := queryOne(values, name)
	if err != nil || raw == "" {
		return time.Time{}, false, err
	}
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false, queryRefusal(name,
			"%s must be an RFC 3339 time, such as 2026-01-02T15:04:05Z", name)
	}
	return at, true, nil
}

// queryOne is the one value a parameter has: empty when it is absent or empty,
// which a form that submits an unfilled box sends, and refused when the
// parameter is repeated.
func queryOne(values url.Values, name string) (string, error) {
	given := values[name]
	if len(given) > 1 {
		return "", queryRefusal(name, "%s is given more than once", name)
	}
	if len(given) == 0 {
		return "", nil
	}
	return strings.TrimSpace(given[0]), nil
}

func queryRefusal(name, format string, a ...any) *APIError {
	return &APIError{
		Status: http.StatusBadRequest, Code: "bad_request",
		Message: fmt.Sprintf(format, a...), Field: name,
	}
}
