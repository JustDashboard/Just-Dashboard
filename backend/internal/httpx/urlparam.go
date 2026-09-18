package httpx

import (
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
)

// URLParam is chi.URLParam with the percent-decoding chi does not do.
//
// chi routes on r.URL.RawPath whenever the request carried one — that is, on
// every URL with an escaped character in it — and it slices the parameter out
// of the same string it routed on. So a handler asking for {id} on
// /docker/images/sha256%3Aab… receives the literal "sha256%3Aab…", escape and
// all, and hands it to the Engine, which answers
//
//	invalid reference format: repository name (library/sha256%3aab…) must be lowercase
//
// because %3A is not a character a Docker reference may contain. The same
// applies to every other path parameter a client escapes: a volume name, a
// stack name, a container name.
//
// Decoding is applied only when chi actually routed on the raw path. When
// RawPath is empty the segment was already decoded by net/http and unescaping
// it a second time would turn a literal "%41" inside a name into "A".
func URLParam(r *http.Request, key string) string {
	raw := chi.URLParam(r, key)
	if r.URL.RawPath == "" || raw == "" {
		return raw
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		// A malformed escape is the client's problem, not something to
		// silently repair: hand back what was sent and let the validation the
		// handler already does reject it with its own message.
		return raw
	}
	return decoded
}
