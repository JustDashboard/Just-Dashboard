package netipam

import "context"

// HeldFor is the unreleased reservations a native owner holds: those handed
// to the resource by name, or recorded with its native ID. Removing that
// resource leaves them held, which the owner's removal preview says.
func (s *Service) HeldFor(ctx context.Context, owner, resource, nativeID string) ([]Reservation, error) {
	if e := s.Ready(); e != nil {
		return nil, e
	}
	all, e := listReservations(ctx, s.db)
	if e != nil {
		return nil, e
	}
	out := []Reservation{}
	for _, r := range all {
		if r.Owner != owner || r.State == "released" {
			continue
		}
		if r.Resource == resource || nativeID != "" && r.NativeID == nativeID {
			out = append(out, r)
		}
	}
	return out, nil
}
