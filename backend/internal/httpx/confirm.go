package httpx

import (
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

// ConfirmHeader carries the phrase a client must echo back before an
// irreversible action runs. Requiring it in a header (rather than a body
// field) means the same guard applies uniformly to DELETE and POST alike,
// and a replayed URL alone can never trigger destruction.
const ConfirmHeader = "X-Confirm"

const ConfirmEncodingHeader = "X-Confirm-Encoding"

// ConfirmParam is the same phrase carried as a query parameter, accepted only
// by RequireTypedConfirmationWS.
const ConfirmParam = "confirm"

// RequireTypedConfirmation refuses the request unless the caller repeats the
// exact phrase. The frontend renders this as a "type <name> to confirm" input;
// scripted callers must send the header deliberately.
//
// The test for reaching for this is **frequency, not severity**. Every route
// that calls it is destructive, but so are a great many that deliberately do
// not: stopping a container, deleting a row, killing a process, removing an
// image, disabling a site. Those are done several times in a sitting, and a
// phrase in front of an everyday act is not read — it is typed, and the
// operator who has learned to type one without looking types the next one the
// same way. That habit is the thing this function is protecting on the routes
// that still call it, so widening the set is how you weaken it.
//
// What is left is the rare and unrecoverable: dropping a table or a column,
// emptying one, `compose down`, removing or pruning a Docker volume, deleting an
// account, restoring over live data, discarding uncommitted work, turning the
// firewall off, upgrading packages. A prune that leaves volumes alone is not on
// that list — a container, network or image comes back from a registry or a
// compose file — nor is deleting a proxy site, a stream or a branch, each of
// which is recreated from the same form or recovered from the reflog.
// Everything else keeps the capability check,
// the tighter destructive budget and the audit entry — which is what
// s.destructive is for — and pauses the operator with an ordinary dialog.
func RequireTypedConfirmation(w http.ResponseWriter, r *http.Request, phrase string) error {
	return requireConfirmation(r, phrase, false)
}

// RequireTypedConfirmationWS is the same guard for a WebSocket route, where
// the phrase may also arrive as a query parameter.
//
// A browser cannot set a header on a WebSocket handshake — the API simply has
// no way to do it — so a destructive action reachable only over a socket would
// otherwise be unconfirmable, and the alternative was to leave `compose down`
// as a request that hangs for a minute with no output. The relaxation costs
// nothing that mattered: the header was chosen because a replayed URL alone
// should never destroy anything, and for these routes that protection comes
// instead from wsx's origin check, which rejects a handshake from any page
// this dashboard did not serve. CORS never applied to WebSocket upgrades in
// the first place, which is why that check exists.
//
// A separate function rather than a flag on the original, so that a route
// accepting the weaker form has to say so at the call site.
func RequireTypedConfirmationWS(w http.ResponseWriter, r *http.Request, phrase string) error {
	return requireConfirmation(r, phrase, true)
}

func requireConfirmation(r *http.Request, phrase string, allowQuery bool) error {
	got := r.Header.Get(ConfirmHeader)
	switch r.Header.Get(ConfirmEncodingHeader) {
	case "uri":
		var err error
		got, err = url.PathUnescape(got)
		if err != nil || !utf8.ValidString(got) {
			return BadRequest("invalid confirmation encoding")
		}
	case "":
		// Preserve old scripts' whitespace tolerance only when whitespace is
		// not part of the object's identity. Encoded phrases are always exact.
		if strings.TrimSpace(phrase) == phrase {
			got = strings.TrimSpace(got)
		}
	default:
		return BadRequest("unsupported confirmation encoding")
	}
	if got == "" && allowQuery {
		got = r.URL.Query().Get(ConfirmParam)
	}
	if got == "" {
		return &APIError{
			Status:  http.StatusPreconditionRequired,
			Code:    "confirmation_required",
			Message: "type \"" + phrase + "\" to confirm this irreversible action",
			Phrase:  phrase,
		}
	}
	if got != phrase {
		return &APIError{
			Status:  http.StatusPreconditionFailed,
			Code:    "confirmation_mismatch",
			Message: "confirmation text does not match \"" + phrase + "\"",
			Phrase:  phrase,
		}
	}
	return nil
}
