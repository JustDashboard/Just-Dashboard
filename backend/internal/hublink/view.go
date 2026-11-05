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
	"strings"
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

type wireSignIn struct {
	Users []string `json:"users"`
}

// strict decodes the way the rest of the reply is decoded: no unknown field and
// nothing after the value. A reply that does not decode is a failed heartbeat.
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

// readView turns a reply into what is cached. The lists are for display: an
// entry that breaks a rule is dropped and the rest are kept, while a reply
// that does not decode at all is an error.
func readView(st accountlink.State, r accountlink.Reply, testOrigin string) (view, error) {
	var fleet []wireFleet
	var notices []wireNotice
	if err := strict(r.Fleet, &fleet); err != nil {
		return view{}, fmt.Errorf("the hub's fleet is not what protocol 1 describes: %w", err)
	}
	if err := strict(r.Notices, &notices); err != nil {
		return view{}, fmt.Errorf("the hub's notices are not what protocol 1 describes: %w", err)
	}
	v := view{ServerID: st.ServerID, At: r.At.UTC(), Fleet: []FleetEntry{}, Notices: []Notice{}}
	if len(bytes.TrimSpace(r.SignIn)) > 0 {
		var offer wireSignIn
		if err := strict(r.SignIn, &offer); err != nil {
			return view{}, fmt.Errorf("the hub's sign-in offer is not what protocol 1 describes: %w", err)
		}
		v.SignIn = &signInOffer{Users: []string{}}
		for _, u := range offer.Users {
			if hubUserPattern.MatchString(u) && len(v.SignIn.Users) < maxSignInUsers {
				v.SignIn.Users = append(v.SignIn.Users, u)
			}
		}
	}
	self := false
	for _, w := range fleet {
		if len(v.Fleet) == maxFleet {
			break
		}
		e, ok := fleetEntry(w, st.URL, testOrigin)
		// Exactly one entry is this server; a second claim is not believed.
		if !ok || (e.Self && self) {
			continue
		}
		self = self || e.Self
		v.Fleet = append(v.Fleet, e)
	}
	for _, w := range notices {
		if len(v.Notices) == maxNotices {
			break
		}
		if n, ok := notice(w, st.URL, testOrigin); ok {
			v.Notices = append(v.Notices, n)
		}
	}
	return v, nil
}

func fleetEntry(w wireFleet, hub, testOrigin string) (FleetEntry, bool) {
	if w.ServerID == nil || w.Name == nil || w.State == nil || w.Version == nil || w.Self == nil {
		return FleetEntry{}, false
	}
	e := FleetEntry{ServerID: *w.ServerID, Name: *w.Name, State: *w.State, Version: *w.Version, Self: *w.Self}
	switch {
	case !serverIDPattern.MatchString(e.ServerID):
		return FleetEntry{}, false
	case !printable(e.Name, 1, 64, false):
		return FleetEntry{}, false
	case e.State != "online" && e.State != "late" && e.State != "offline" && e.State != "updating":
		return FleetEntry{}, false
	case utf8.RuneCountInString(e.Version) > 32 || !printable(e.Version, 0, 32, false):
		return FleetEntry{}, false
	}
	if w.LastSeenAt != nil && *w.LastSeenAt > 0 {
		t := time.Unix(*w.LastSeenAt, 0).UTC()
		e.LastSeenAt = &t
	}
	// An endpoint that fails its rule is removed; the entry stays, without a
	// link.
	if w.Endpoint != nil {
		e.Endpoint = fleetEndpoint(*w.Endpoint, hub, testOrigin)
	}
	return e, true
}

// fleetEndpoint applies section 6.1's endpoint rule: an https origin, at most
// 255 characters, one label under the hub's own MagicDNS suffix. A hub at the
// test origin may also name any https origin and any loopback http origin.
func fleetEndpoint(raw, hub, testOrigin string) string {
	if len(raw) > 255 || !strings.Contains(raw, "://") {
		return ""
	}
	origin, u, err := readOrigin(raw)
	if err != nil {
		return ""
	}
	scheme, host := strings.ToLower(u.Scheme), hostOf(origin)
	if testOrigin != "" && hub == testOrigin {
		if scheme == "https" || (scheme == "http" && loopbackHost(host)) {
			return origin
		}
		return ""
	}
	if scheme != "https" {
		return ""
	}
	_, suffix, ok := strings.Cut(hostOf(hub), ".")
	if !ok || !strings.HasSuffix(suffix, tailnetSuffixTail) {
		return ""
	}
	if label, rest, ok := strings.Cut(host, "."); !ok || rest != suffix || !labelPattern.MatchString(label) {
		return ""
	}
	return origin
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
	// A link anywhere but the hub's own origin is removed from the notice.
	if w.Href != nil && len(*w.Href) <= 512 && sameOriginAs(hub, *w.Href) {
		u, _ := url.Parse(*w.Href)
		if strings.EqualFold(u.Scheme, "https") || (testOrigin != "" && hub == testOrigin) {
			n.Href = *w.Href
		}
	}
	return n, true
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

// onReply is the engine's hook for every successful heartbeat reply: it checks
// the view and keeps it for the dashboard's own pages.
func (s *Service) onReply(ctx context.Context, st accountlink.State, r accountlink.Reply) error {
	v, err := readView(st, r, s.testOrigin)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := s.st.SetSetting(context.WithoutCancel(ctx), keyView, string(raw)); err != nil {
		s.log.Warn("could not keep what the hub sent with a heartbeat", "err", err)
	}
	return nil
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
