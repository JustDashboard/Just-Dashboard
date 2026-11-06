package hublink

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
)

// keyView is where the last heartbeat reply's fleet, notices and sign-in offer
// are kept, with the time they arrived. Not secret, and cleared with the link.
const keyView = "hub.link.view"

// The limits of protocol section 6.
const (
	maxFleet       = 500
	maxNotices     = 20
	maxSignInUsers = 50
)

var (
	serverIDPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z-]{7,63}$`)
	noticeIDPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._:-]{0,63}$`)
	hubUserPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

// FleetEntry is one server linked to the hub, as the dashboard serves it to its
// own pages.
type FleetEntry struct {
	ServerID   string     `json:"serverId"`
	Name       string     `json:"name"`
	Endpoint   string     `json:"endpoint,omitempty"`
	State      string     `json:"state"`
	Version    string     `json:"version"`
	Self       bool       `json:"self"`
	LastSeenAt *time.Time `json:"lastSeenAt,omitempty"`
}

// Notice is a short message the hub wants every linked dashboard to show. Its
// title and body are plain text and are rendered as text, never as markup.
type Notice struct {
	ID    string    `json:"id"`
	Level string    `json:"level"`
	Title string    `json:"title"`
	Body  string    `json:"body"`
	Href  string    `json:"href,omitempty"`
	At    time.Time `json:"at"`
}

// view is the cached reply. It names the link it came from, so a reply that
// lands after the link changed is never shown for the new one.
type view struct {
	ServerID string       `json:"serverId"`
	At       time.Time    `json:"at"`
	Fleet    []FleetEntry `json:"fleet"`
	Notices  []Notice     `json:"notices"`
	SignIn   *signInOffer `json:"signIn,omitempty"`
}

// signInOffer is the hub offering hub sign-in, and the hub users it names.
type signInOffer struct {
	Users []string `json:"users"`
}

// The reply's entries as they travel. Required fields are pointers so a
// missing one is told from a zero one; an entry missing one is dropped.
type wireFleet struct {
	ServerID   *string `json:"serverId"`
	Name       *string `json:"name"`
	Endpoint   *string `json:"endpoint"`
	State      *string `json:"state"`
	Version    *string `json:"version"`
	Self       *bool   `json:"self"`
	LastSeenAt *int64  `json:"lastSeenAt"`
}

type wireNotice struct {
	ID    *string `json:"id"`
	Level *string `json:"level"`
	Title *string `json:"title"`
	Body  *string `json:"body"`
	Href  *string `json:"href"`
	At    *int64  `json:"at"`
}

// strict decodes the way the rest of the reply is decoded: no unknown field and
// nothing after the value.
func strict(raw json.RawMessage, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("content after the JSON value")
	}
	return nil
}

// elements reads a list of the reply as its raw elements, so each can be
// decoded on its own. Only a value that is not a list fails.
func elements(raw json.RawMessage, what string) ([]json.RawMessage, error) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || t[0] != '[' {
		return nil, fmt.Errorf("the hub's %s is not a list, which protocol 1 requires", what)
	}
	var out []json.RawMessage
	if err := strict(t, &out); err != nil {
		return nil, fmt.Errorf("the hub's %s is not what protocol 1 describes: %w", what, err)
	}
	return out, nil
}

// readView turns a reply into what is cached (protocol section 6, "Decoding").
// The reply's top level is strict: fleet and notices must be lists, and signIn,
// when present, an object holding exactly a list users, or the heartbeat fails
// and nothing in it is used. Each element of those three lists is then decoded
// on its own, strictly, and checked against its rules; an element that fails is
// dropped and the rest kept, because the lists are for display and one
// server's bad value must not stop every other server's heartbeat.
//
// ownSuffix is this server's MagicDNS suffix, asked for only when the test
// origin's endpoint rule needs it.
func readView(st accountlink.State, r accountlink.Reply, testOrigin string, ownSuffix func() string) (view, error) {
	fleet, err := elements(r.Fleet, "fleet")
	if err != nil {
		return view{}, err
	}
	notices, err := elements(r.Notices, "notices")
	if err != nil {
		return view{}, err
	}
	if ownSuffix != nil {
		// Asked at most once per reply, and only if an entry needs it.
		ownSuffix = sync.OnceValue(ownSuffix)
	}
	v := view{ServerID: st.ServerID, At: r.At.UTC(), Fleet: []FleetEntry{}, Notices: []Notice{}}
	if raw := bytes.TrimSpace(r.SignIn); len(raw) > 0 {
		if raw[0] != '{' {
			return view{}, errors.New("the hub's sign-in offer is not an object, which protocol 1 requires")
		}
		var offer struct {
			Users json.RawMessage `json:"users"`
		}
		if err := strict(raw, &offer); err != nil {
			return view{}, fmt.Errorf("the hub's sign-in offer is not what protocol 1 describes: %w", err)
		}
		users, err := elements(offer.Users, "sign-in users")
		if err != nil {
			return view{}, err
		}
		v.SignIn = &signInOffer{Users: []string{}}
		for _, raw := range users {
			if len(v.SignIn.Users) == maxSignInUsers {
				break
			}
			var u string
			if strict(raw, &u) != nil || !hubUserPattern.MatchString(u) || slices.Contains(v.SignIn.Users, u) {
				continue
			}
			v.SignIn.Users = append(v.SignIn.Users, u)
		}
	}
	self := false
	for _, raw := range fleet {
		if len(v.Fleet) == maxFleet {
			break
		}
		var w wireFleet
		if strict(raw, &w) != nil {
			continue
		}
		e, ok := fleetEntry(w, st.URL, testOrigin, ownSuffix)
		// Exactly one entry is this server; a second claim is not believed.
		if !ok || (e.Self && self) {
			continue
		}
		self = self || e.Self
		v.Fleet = append(v.Fleet, e)
	}
	for _, raw := range notices {
		if len(v.Notices) == maxNotices {
			break
		}
		var w wireNotice
		if strict(raw, &w) != nil {
			continue
		}
		if n, ok := notice(w, st.URL, testOrigin); ok {
			v.Notices = append(v.Notices, n)
		}
	}
	return v, nil
}

func fleetEntry(w wireFleet, hub, testOrigin string, ownSuffix func() string) (FleetEntry, bool) {
	if w.ServerID == nil || w.Name == nil || w.State == nil || w.Version == nil || w.Self == nil {
		return FleetEntry{}, false
	}
	e := FleetEntry{ServerID: *w.ServerID, Name: *w.Name, State: *w.State, Version: *w.Version, Self: *w.Self}
	switch {
	case !serverIDPattern.MatchString(e.ServerID):
		return FleetEntry{}, false
	case !textRule(e.Name, 1, 64):
		return FleetEntry{}, false
	case e.State != "online" && e.State != "late" && e.State != "offline" && e.State != "updating":
		return FleetEntry{}, false
	case !textRule(e.Version, 0, 32):
		return FleetEntry{}, false
	}
	if w.LastSeenAt != nil && *w.LastSeenAt > 0 {
		t := time.Unix(*w.LastSeenAt, 0).UTC()
		e.LastSeenAt = &t
	}
	// An endpoint that fails its rule is removed; the entry stays, without a
	// link.
	if w.Endpoint != nil {
		e.Endpoint = fleetEndpoint(*w.Endpoint, hub, testOrigin, ownSuffix)
	}
	return e, true
}

// fleetEndpoint applies section 6.1's endpoint rule: an https origin with no
// path, query or fragment, at most 255 characters, whose host is one label
// under the hub URL's MagicDNS suffix. With the hub at the test origin (2.3) a
// loopback http or https origin passes instead, as does one that passes the
// rule with this server's own MagicDNS suffix when that can be read. Nothing
// else passes there: an https origin anywhere else is removed too.
func fleetEndpoint(raw, hub, testOrigin string, ownSuffix func() string) string {
	if len(raw) > 255 || !strings.Contains(raw, "://") {
		return ""
	}
	origin, u, err := readOrigin(raw)
	if err != nil {
		return ""
	}
	scheme, host := strings.ToLower(u.Scheme), hostOf(origin)
	if testOrigin != "" && hub == testOrigin {
		switch {
		case (scheme == "http" || scheme == "https") && loopbackHost(host):
			return origin
		case scheme == "https" && ownSuffix != nil && underSuffix(host, ownSuffix()):
			return origin
		}
		return ""
	}
	if scheme != "https" {
		return ""
	}
	if _, suffix, ok := strings.Cut(hostOf(hub), "."); ok && underSuffix(host, suffix) {
		return origin
	}
	return ""
}

// underSuffix is host being exactly one MagicDNS label under suffix, a
// tailnet's .ts.net name.
func underSuffix(host, suffix string) bool {
	if suffix == "" || !strings.HasSuffix(suffix, tailnetSuffixTail) {
		return false
	}
	label, rest, ok := strings.Cut(host, ".")
	return ok && rest == suffix && labelPattern.MatchString(label)
}

func notice(w wireNotice, hub, testOrigin string) (Notice, bool) {
	if w.ID == nil || w.Level == nil || w.Title == nil || w.Body == nil || w.At == nil {
		return Notice{}, false
	}
	n := Notice{ID: *w.ID, Level: *w.Level, Title: *w.Title, Body: *w.Body, At: time.Unix(*w.At, 0).UTC()}
	switch {
	case !noticeIDPattern.MatchString(n.ID):
		return Notice{}, false
	case n.Level != "info" && n.Level != "warning" && n.Level != "critical":
		return Notice{}, false
	case !printable(n.Title, 1, 120, false), !printable(n.Body, 0, 1000, true):
		return Notice{}, false
	}
	// A link anywhere but the hub's own origin is removed from the notice. At
	// the test origin, that origin's own scheme is the one allowed (2.3).
	if w.Href != nil && len(*w.Href) <= 512 && sameOriginAs(hub, *w.Href) {
		u, _ := url.Parse(*w.Href)
		if strings.EqualFold(u.Scheme, "https") || (testOrigin != "" && hub == testOrigin) {
			n.Href = *w.Href
		}
	}
	return n, true
}

// textRule is protocol 5.3's text rule with a length: valid UTF-8, min to max
// characters, and no character in the Unicode categories Cc (control, CR and
// LF among them), Cf (format, the bidirectional controls among them), Zl or
// Zp, any of which can make a name read as something it is not.
func textRule(s string, min, max int) bool {
	if !utf8.ValidString(s) {
		return false
	}
	if n := utf8.RuneCountInString(s); n < min || n > max {
		return false
	}
	for _, r := range s {
		if unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return true
}

// printable is text of min to max characters with no control characters; a
// body may also break lines.
func printable(s string, min, max int, lines bool) bool {
	if !utf8.ValidString(s) {
		return false
	}
	n := utf8.RuneCountInString(s)
	if n < min || n > max {
		return false
	}
	if min > 0 && strings.TrimSpace(s) == "" {
		return false
	}
	for _, r := range s {
		if lines && (r == '\n' || r == '\t') {
			continue
		}
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ownSuffix is this server's MagicDNS suffix, or "" when it cannot be read.
func (s *Service) ownSuffix(ctx context.Context) string {
	st, err := s.tailnet.Status(ctx)
	if err != nil || !st.Running {
		return ""
	}
	return st.Suffix
}

// onReply is the engine's hook for every successful heartbeat reply: it checks
// the view and hands back its write, which the engine runs in the transaction
// that records the heartbeat and only while the stored link is still the one
// the heartbeat was sent for (section 8.9, "Writes belong to the link they
// describe"). A reply that lands after a disconnect therefore cannot bring
// back the old hub's fleet, notices or sign-in users.
func (s *Service) onReply(ctx context.Context, st accountlink.State, r accountlink.Reply) (accountlink.ReplyWrite, error) {
	v, err := readView(st, r, s.testOrigin, func() string { return s.ownSuffix(ctx) })
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, tx *sql.Tx) (func(), error) {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			keyView, string(raw))
		return nil, err
	}, nil
}

// onClear forgets the view with the link it belongs to. Hub sign-in's grants
// and tickets are cleared here too once they exist.
func (s *Service) onClear(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, keyView)
	return err
}

// readStoredView is the cached reply for the link st, if there is one.
func (s *Service) readStoredView(ctx context.Context, st accountlink.State) (view, bool) {
	raw, ok, err := s.st.Setting(ctx, keyView)
	if err != nil || !ok {
		return view{}, false
	}
	var v view
	if err := json.Unmarshal([]byte(raw), &v); err != nil || v.ServerID != st.ServerID {
		return view{}, false
	}
	return v, true
}
