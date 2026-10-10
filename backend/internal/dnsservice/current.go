package dnsservice

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"reflect"
)

// CurrentChange reads the retained selection without claiming or replaying it.
// A consumed or expired review still identifies a useful native comparison.
func (s *Service) CurrentChange(ctx context.Context, id string) (View, error) {
	plan, err := s.Change(ctx, id)
	if err != nil {
		return View{}, err
	}
	if err = s.acquire(ctx); err != nil {
		return View{}, err
	}
	defer func() { <-s.active }()
	c, req, err := s.connection(ctx, plan.ConnectionID)
	if err != nil {
		return View{}, err
	}
	if c.Generation != plan.Generation || plan.Before == nil || plan.Before.Engine != c.Engine {
		return View{}, ErrConflict
	}
	var raw string
	if err = s.db.QueryRowContext(ctx, `SELECT request_json FROM network_dns_service_changes WHERE id=?`, id).Scan(&raw); err != nil {
		return View{}, err
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	var retained ChangeRequest
	if err = decoder.Decode(&retained); err != nil {
		return View{}, errors.New("retained native DNS selection is malformed or unsupported")
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF || !reflect.DeepEqual(retained, plan.Request) {
		return View{}, errors.New("retained native DNS selection changed or is unreadable")
	}
	if err = validateChange(plan.Request, req.Engine); err != nil {
		return View{}, errors.New("retained native DNS selection is malformed or unsupported")
	}
	if err = validatePolicyBaseline(plan.Request, plan.Before); err != nil {
		return View{}, errors.New("retained native DNS selection is malformed or unsupported")
	}
	snapshot, nativeErr := inspectNativeSelection(ctx, req, &plan.Request)
	// Sequential native reads can outlive a connection replacement. Do not return
	// that old credential's reading as the current generation or owned resource.
	latest, err := scanConnection(s.db.QueryRowContext(ctx, `SELECT `+connectionColumns+` FROM network_dns_services WHERE id=?`, c.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return View{}, ErrNotFound
	}
	if err != nil {
		return View{}, err
	}
	if latest.Generation != c.Generation || latest.Engine != c.Engine || latest.Endpoint != c.Endpoint || latest.ServerName != c.ServerName || latest.Ownership != c.Ownership || latest.ContainerID != c.ContainerID || latest.Management != c.Management {
		return View{}, ErrConflict
	}
	view := View{Connection: c, Snapshot: snapshot, State: "available"}
	if nativeErr != nil {
		view.State, view.Error = "unavailable", nativeErr.Error()
	}
	return view, nil
}
