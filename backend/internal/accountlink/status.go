package accountlink

import (
	"context"
	"encoding/json"
	"time"
)

// Status is what GET /dashboard/link answers, field for field. Optional fields
// are left out rather than null.
type Status struct {
	Enabled     bool           `json:"enabled"`
	SiteURL     string         `json:"siteUrl"`
	State       string         `json:"state"`
	Fingerprint string         `json:"fingerprint,omitempty"`
	Request     *RequestStatus `json:"request,omitempty"`
	Link        *LinkStatus    `json:"link,omitempty"`
	LastOutcome *OutcomeStatus `json:"lastOutcome,omitempty"`
}

type RequestStatus struct {
	UserCode   string    `json:"userCode"`
	ApproveURL string    `json:"approveUrl"`
	ExpiresAt  time.Time `json:"expiresAt"`
	Scope      Scope     `json:"scope"`
}

type LinkStatus struct {
	ServerID        string          `json:"serverId"`
	Owner           string          `json:"owner"`
	Scope           Scope           `json:"scope"`
	LinkedAt        time.Time       `json:"linkedAt"`
	LinkedBy        string          `json:"linkedBy"`
	LastHeartbeatAt *time.Time      `json:"lastHeartbeatAt,omitempty"`
	LastError       string          `json:"lastError,omitempty"`
	LastCommand     *CommandStatus  `json:"lastCommand,omitempty"`
	SiteKeys        []SiteKeyStatus `json:"siteKeys"`
	UpdatePolicy    *UpdatePolicy   `json:"updatePolicy,omitempty"`
	// LastSnapshot is the exact body of the last heartbeat sent, so the page
	// shows what is shared and not a description of it.
	LastSnapshot json.RawMessage `json:"lastSnapshot,omitempty"`
}

type CommandStatus struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Status   string    `json:"status"`
	IssuedBy string    `json:"issuedBy"`
	At       time.Time `json:"at"`
}

type SiteKeyStatus struct {
	Kid         string     `json:"kid"`
	Fingerprint string     `json:"fingerprint"`
	NotAfter    *time.Time `json:"notAfter,omitempty"`
}

type OutcomeStatus struct {
	Result  string    `json:"result"`
	At      time.Time `json:"at"`
	Message string    `json:"message,omitempty"`
}

const (
	StateUnlinked = "unlinked"
	StatePending  = "pending"
	StateLinked   = "linked"
)

func at(unix int64) time.Time { return time.Unix(unix, 0).UTC() }

// Status reads the link as it is stored. It makes nothing and contacts no one,
// except that a request past its expiry is closed first.
func (s *Service) Status(ctx context.Context) (Status, error) {
	out := Status{Enabled: s.enabled, SiteURL: s.siteURL, State: StateUnlinked}
	if s.sealer != nil {
		if id, _, err := loadIdentity(ctx, s.st, s.sealer); err == nil && id != nil {
			out.Fingerprint = id.Fingerprint()
		}
	}

	st, linked, err := s.kv.state(ctx)
	if err != nil {
		return out, err
	}
	if linked {
		out.State = StateLinked
		out.Link = s.linkStatus(ctx, st)
		return out, nil
	}

	req, pending, err := s.kv.request(ctx)
	if err != nil {
		return out, err
	}
	if pending && !s.now().Before(req.ExpiresAt) {
		s.endRequest(ctx, outcomeExpired, "the request was not approved in time")
		pending = false
	}
	if pending {
		out.State = StatePending
		out.Request = &RequestStatus{UserCode: req.UserCode, ApproveURL: req.ApproveURL, ExpiresAt: req.ExpiresAt.UTC(), Scope: req.Scope}
		return out, nil
	}
	if o, ok, err := s.kv.outcome(ctx); err == nil && ok {
		out.LastOutcome = &OutcomeStatus{Result: o.Result, At: o.At.UTC(), Message: o.Message}
	}
	return out, nil
}

func (s *Service) linkStatus(ctx context.Context, st State) *LinkStatus {
	rs := s.kv.status(ctx)
	l := &LinkStatus{
		ServerID: st.ServerID, Owner: st.Owner, Scope: st.Scope, LinkedAt: st.LinkedAt.UTC(), LinkedBy: st.LinkedBy,
		LastHeartbeatAt: rs.LastHeartbeatAt, LastError: rs.lastError(), SiteKeys: []SiteKeyStatus{},
	}
	for _, k := range st.SiteKeys {
		ks := SiteKeyStatus{Kid: k.Kid}
		if pub, err := k.key(); err == nil {
			ks.Fingerprint = Fingerprint(pub)
		}
		if k.NotAfter != 0 {
			t := at(k.NotAfter)
			ks.NotAfter = &t
		}
		l.SiteKeys = append(l.SiteKeys, ks)
	}
	if p, ok := s.policy(ctx); ok && p.Auto != AutoOff {
		l.UpdatePolicy = &p
	}
	l.LastCommand = s.lastCommand(ctx, st.LinkedAt.Unix())
	s.mu.Lock()
	l.LastSnapshot = append(json.RawMessage(nil), s.lastSnapshot...)
	s.mu.Unlock()
	return l
}

// lastCommand is the newest command that has finished since this link was
// made; one from an earlier link to the site is not this link's history.
func (s *Service) lastCommand(ctx context.Context, since int64) *CommandStatus {
	var c CommandStatus
	var received int64
	var finished *int64
	err := s.st.DB.QueryRowContext(ctx,
		`SELECT id, kind, status, issued_by, received_at, finished_at FROM account_link_commands
		 WHERE status <> 'running' AND received_at >= ? ORDER BY received_at DESC, rowid DESC LIMIT 1`, since).
		Scan(&c.ID, &c.Kind, &c.Status, &c.IssuedBy, &received, &finished)
	if err != nil {
		return nil
	}
	c.At = at(received)
	if finished != nil {
		c.At = at(*finished)
	}
	return &c
}
