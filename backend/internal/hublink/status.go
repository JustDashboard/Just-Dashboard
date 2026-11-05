package hublink

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
)

// The states a hub link is in.
const (
	StateUnlinked = accountlink.StateUnlinked
	StatePending  = accountlink.StatePending
	StateLinked   = accountlink.StateLinked
)

// Status is what GET /api/v1/dashboard/hub answers (protocol section 8.2).
// Optional fields are left out, never null; times are RFC 3339 in UTC.
type Status struct {
	Enabled bool   `json:"enabled"`
	State   string `json:"state"`
	HubURL  string `json:"hubUrl,omitempty"`
	HubName string `json:"hubName,omitempty"`
	Scope   string `json:"scope,omitempty"`
	// Reachable is sent only while linked.
	Reachable       *bool      `json:"reachable,omitempty"`
	LastHeartbeatAt *time.Time `json:"lastHeartbeatAt,omitempty"`
	// Fleet and Notices are [] while linked before the first reply, and left
	// out otherwise.
	Fleet   []FleetEntry  `json:"fleet,omitzero"`
	Notices []Notice      `json:"notices,omitzero"`
	SignIn  *SignInStatus `json:"signIn,omitempty"`
	Admin   *AdminStatus  `json:"admin,omitempty"`
}

// SignInStatus is where hub sign-in stands for the caller. This release has
// no hub sign-in, so enabled is always false; offered and users report what
// the hub's last reply said.
type SignInStatus struct {
	Enabled bool     `json:"enabled"`
	Offered bool     `json:"offered"`
	Users   []string `json:"users"`
}

// AdminStatus is the part only a system administrator is sent.
type AdminStatus struct {
	Fingerprint string                     `json:"fingerprint,omitempty"`
	TestOrigin  string                     `json:"testOrigin,omitempty"`
	Request     *RequestStatus             `json:"request,omitempty"`
	Link        *LinkStatus                `json:"link,omitempty"`
	LastOutcome *accountlink.OutcomeStatus `json:"lastOutcome,omitempty"`
}

type RequestStatus struct {
	HubURL     string    `json:"hubUrl"`
	UserCode   string    `json:"userCode"`
	ApproveURL string    `json:"approveUrl"`
	ExpiresAt  time.Time `json:"expiresAt"`
	Scope      string    `json:"scope"`
}

type LinkStatus struct {
	ServerID     string                     `json:"serverId"`
	LinkedAt     time.Time                  `json:"linkedAt"`
	LinkedBy     string                     `json:"linkedBy"`
	LastError    string                     `json:"lastError,omitempty"`
	LastCommand  *accountlink.CommandStatus `json:"lastCommand,omitempty"`
	HubKeys      []HubKeyStatus             `json:"hubKeys"`
	UpdatePolicy *accountlink.UpdatePolicy  `json:"updatePolicy,omitempty"`
	// LastSnapshot is the exact body of the last heartbeat sent, so the page
	// shows what is shared and not a description of it.
	LastSnapshot json.RawMessage `json:"lastSnapshot,omitempty"`
}

type HubKeyStatus struct {
	Kid         string     `json:"kid"`
	Fingerprint string     `json:"fingerprint"`
	NotAfter    *time.Time `json:"notAfter,omitempty"`
}

// Status reads the hub link as it is stored and cached. It contacts no one.
// admin adds the part only a system administrator may read.
func (s *Service) Status(ctx context.Context, admin bool) (Status, error) {
	es, err := s.engine.Status(ctx)
	if err != nil {
		return Status{}, err
	}
	out := Status{Enabled: s.enabled, State: es.State}
	if !s.enabled {
		return out, nil
	}
	var a *AdminStatus
	if admin {
		a = &AdminStatus{Fingerprint: es.Fingerprint, TestOrigin: s.testOrigin}
	}
	offer := (*signInOffer)(nil)
	switch es.State {
	case StateLinked:
		st, linked, err := s.engine.Linked(ctx)
		if err != nil {
			return Status{}, err
		}
		if !linked {
			// Unlinked between the two reads; the next read says so.
			out.State = StateUnlinked
			break
		}
		h := s.engine.Health(ctx)
		reachable := s.reachable(st, h)
		out.HubURL, out.HubName, out.Scope = st.URL, st.Owner, string(st.Scope)
		out.Reachable, out.LastHeartbeatAt = &reachable, h.LastHeartbeatAt
		out.Fleet, out.Notices = []FleetEntry{}, []Notice{}
		if v, ok := s.readStoredView(ctx, st); ok {
			out.Fleet, out.Notices, offer = v.Fleet, v.Notices, v.SignIn
		}
		if a != nil && es.Link != nil {
			l := es.Link
			a.Link = &LinkStatus{
				ServerID: l.ServerID, LinkedAt: l.LinkedAt, LinkedBy: l.LinkedBy, LastError: l.LastError,
				LastCommand: l.LastCommand, HubKeys: []HubKeyStatus{}, UpdatePolicy: l.UpdatePolicy, LastSnapshot: l.LastSnapshot,
			}
			for _, k := range l.SiteKeys {
				a.Link.HubKeys = append(a.Link.HubKeys, HubKeyStatus{Kid: k.Kid, Fingerprint: k.Fingerprint, NotAfter: k.NotAfter})
			}
		}
	case StatePending:
		if req, ok, err := s.engine.Pending(ctx); err == nil && ok {
			out.HubURL = req.URL
			if a != nil {
				a.Request = &RequestStatus{
					HubURL: req.URL, UserCode: req.UserCode, ApproveURL: req.ApproveURL,
					ExpiresAt: req.ExpiresAt.UTC(), Scope: string(req.Scope),
				}
			}
		} else {
			out.State = StateUnlinked
		}
	default:
		if a != nil {
			a.LastOutcome = es.LastOutcome
		}
	}
	out.SignIn = &SignInStatus{Users: []string{}}
	if offer != nil {
		out.SignIn.Offered, out.SignIn.Users = true, offer.Users
	}
	out.Admin = a
	return out, nil
}

// reachable is the last heartbeat having succeeded, none having failed since,
// and it being younger than three intervals.
func (s *Service) reachable(st accountlink.State, h accountlink.Health) bool {
	if !s.enabled || h.LastHeartbeatAt == nil || h.HeartbeatError != "" {
		return false
	}
	every := time.Duration(max(st.HeartbeatSeconds, 10)) * time.Second
	return s.now().Sub(*h.LastHeartbeatAt) < 3*every
}

// SessionHub is the hub field of GET /auth/session: just enough for the shell
// to decide, before its first paint, to show hub features.
type SessionHub struct {
	Linked    bool   `json:"linked"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Reachable bool   `json:"reachable"`
}

// Session is the hub field for a fully signed-in session, or nil when hub
// features have nothing to show: switched off, or no hub link stored.
func (s *Service) Session(ctx context.Context) *SessionHub {
	if s == nil || !s.enabled {
		return nil
	}
	st, linked, err := s.engine.Linked(ctx)
	if err != nil || !linked {
		return nil
	}
	return &SessionHub{Linked: true, Name: st.Owner, URL: st.URL, Reachable: s.reachable(st, s.engine.Health(ctx))}
}

// The hello states (protocol section 8.1).
const (
	HelloAvailable = "available"
	HelloPending   = "pending"
	HelloLinked    = "linked"
)

// Hello is what GET /api/v1/hub/hello says about this server: whether it can
// be linked, is waiting for an approval, or is linked. Nothing else.
func (s *Service) Hello(ctx context.Context) string {
	if _, linked, err := s.engine.Linked(ctx); err == nil && linked {
		return HelloLinked
	}
	if _, pending, err := s.engine.Pending(ctx); err == nil && pending {
		return HelloPending
	}
	return HelloAvailable
}

// HelloBody is the exact body for a hello state. It is written by hand rather
// than encoded so the bytes are the protocol's, field order and all.
func HelloBody(state string) []byte {
	return []byte(`{"product":"just-dashboard","protocol":1,"hub":"` + state + `"}`)
}
