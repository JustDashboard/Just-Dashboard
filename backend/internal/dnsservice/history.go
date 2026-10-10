package dnsservice

import "context"

// Changes reads retained review metadata without opening native credentials or
// contacting an engine. Full snapshots remain an explicit single-plan read.
func (s *Service) Changes(ctx context.Context, connectionID string) ([]Change, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if !validID(connectionID) {
		return nil, ErrNotFound
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM network_dns_services WHERE id=?)`, connectionID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,connection_id,generation,request_json,before_json,after_json,state,created_at,expires_at,ended_at,error FROM network_dns_service_changes WHERE connection_id=? ORDER BY created_at DESC,id DESC LIMIT 64`, connectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Change{}
	for rows.Next() {
		change, err := readChange(rows)
		if err != nil {
			return nil, err
		}
		change.Before, change.After = nil, nil
		out = append(out, change)
	}
	return out, rows.Err()
}
