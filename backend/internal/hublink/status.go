package hublink

import (
	"context"
	"encoding/json"
	"errors"
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
	// HeartbeatSeconds is the interval the hub set, as this server clamps it
	// (protocol 4.8), sent only while linked: the page says how often it
	// reports in it rather than in the default.
	HeartbeatSeconds int `json:"heartbeatSeconds,omitempty"`
	// Idle is sent, as true, only while the stored link is idle (protocol 2.2
	// "When the rule runs", 2.4), and to every reader: the hub URL may now
	// answer on a machine this server refused, so no page offers it as a
	// link. The reason is the administrator's, in admin.link.lastError.
	Idle bool `json:"idle,omitempty"`
	// Fleet and Notices are [] while linked before the first reply, and left
	// out otherwise.
	Fleet   []FleetEntry  `json:"fleet,omitzero"`
	Notices []Notice      `json:"notices,omitzero"`
	SignIn  *SignInStatus `json:"signIn,omitempty"`
	Admin   *AdminStatus  `json:"admin,omitempty"`
}

// SignInStatus is where hub sign-in stands for the caller (section 9):
// enabled is JD_HUB_SIGNIN, offered and users what the hub's last reply said,
// and mine the caller's own grant, if it has one for this link.
type SignInStatus struct {
	Enabled bool     `json:"enabled"`
	Offered bool     `json:"offered"`
	Users   []string `json:"users"`
	Mine    *MyGrant `json:"mine,omitempty"`
}

// AdminStatus is the part only a system administrator is sent.
type AdminStatus struct {
	Fingerprint string                     `json:"fingerprint,omitempty"`
	TestOrigin  string                     `json:"testOrigin,omitempty"`
	Request     *RequestStatus             `json:"request,omitempty"`
	Link        *LinkStatus                `json:"link,omitempty"`
	LastOutcome *accountlink.OutcomeStatus `json:"lastOutcome,omitempty"`
	// Grants is every account's hub sign-in grant for this link, sent while
	// linked with JD_HUB_SIGNIN on.
	Grants []Grant `json:"grants,omitzero"`
}

type RequestStatus struct {
	HubURL     string    `json:"hubUrl"`
	UserCode   string    `json:"userCode"`
	ApproveURL string    `json:"approveUrl"`
	ExpiresAt  time.Time `json:"expiresAt"`
	Scope      string    `json:"scope"`
	// HubMachine is the hub node step 8 of the rule found, left out when it
	// was not checked.
	HubMachine *Machine `json:"hubMachine,omitempty"`
}

type LinkStatus struct {
	ServerID string    `json:"serverId"`
	LinkedAt time.Time `json:"linkedAt"`
	LinkedBy string    `json:"linkedBy"`
	// HubMachine is the pinned hub node (protocol 2.4), left out until one is
	// recorded.
	HubMachine   *Machine                   `json:"hubMachine,omitempty"`
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
	return s.StatusFor(ctx, admin, 0)
}

// StatusFor is Status for one account, whose own hub sign-in grant it names.
func (s *Service) StatusFor(ctx context.Context, admin bool, userID int64) (Status, error) {
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
		out.HeartbeatSeconds, out.Idle = heartbeatSeconds(st), st.Idle != nil
		out.Fleet, out.Notices = []FleetEntry{}, []Notice{}
		if v, ok := s.readStoredView(ctx, st); ok {
			out.Fleet, out.Notices, offer = v.Fleet, v.Notices, v.SignIn
		}
		if a != nil && es.Link != nil {
			l := es.Link
			a.Link = &LinkStatus{
				ServerID: l.ServerID, LinkedAt: l.LinkedAt, LinkedBy: l.LinkedBy, HubMachine: st.HubNode, LastError: l.LastError,
				LastCommand: l.LastCommand, HubKeys: []HubKeyStatus{}, UpdatePolicy: l.UpdatePolicy, LastSnapshot: l.LastSnapshot,
			}
			// An idle link says why, whatever the last exchange left behind.
			if st.Idle != nil {
				a.Link.LastError = refused(errors.New(st.Idle.Reason)).Error()
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
					ExpiresAt: req.ExpiresAt.UTC(), Scope: string(req.Scope), HubMachine: req.HubNode,
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
	out.SignIn = &SignInStatus{Enabled: s.SignInEnabled(), Users: []string{}}
	if offer != nil {
		out.SignIn.Offered, out.SignIn.Users = true, offer.Users
	}
	if out.State == StateLinked && s.SignInEnabled() {
		grants, err := s.Grants(ctx)
		if err != nil {
			return Status{}, err
		}
		for _, g := range grants {
			if g.UserID == userID && userID != 0 {
				out.SignIn.Mine = &MyGrant{HubUser: g.HubUser, Since: g.Since}
			}
		}
		if a != nil {
			a.Grants = grants
		}
	}
	out.Admin = a
	return out, nil
}

// reachable is the last heartbeat having succeeded, none having failed since,
// and it being younger than three intervals.
func (s *Service) reachable(st accountlink.State, h accountlink.Health) bool {
	if !s.enabled || st.Idle != nil || h.LastHeartbeatAt == nil || h.HeartbeatError != "" {
		return false
	}
	every := time.Duration(heartbeatSeconds(st)) * time.Second
	return s.now().Sub(*h.LastHeartbeatAt) < 3*every
}

// heartbeatSeconds is the interval the link reports at: the hub's, clamped to
// 10 to 600 seconds as the heartbeat loop clamps it.
func heartbeatSeconds(st accountlink.State) int {
	return min(max(st.HeartbeatSeconds, 10), 600)
}

// SessionHub is the hub field of GET /auth/session: just enough for the shell
// to decide, before its first paint, to show hub features.
type SessionHub struct {
	Linked    bool   `json:"linked"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Reachable bool   `json:"reachable"`
	// Idle is sent, as true, only while the link is idle: the shell then
	// offers no link to the hub URL (protocol 8.3).
	Idle bool `json:"idle,omitempty"`
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
	return &SessionHub{
		Linked: true, Name: st.Owner, URL: st.URL, Reachable: s.reachable(st, s.engine.Health(ctx)), Idle: st.Idle != nil,
	}
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
