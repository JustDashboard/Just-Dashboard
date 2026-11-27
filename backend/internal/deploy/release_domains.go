package deploy

import "context"

// ReleaseDomains are the domains a release serves, as its verified runtime
// snapshot records them: the names a smart stack sets its applications up
// for once the release is live (internal/stacks).
func (s *OrchestrationStore) ReleaseDomains(ctx context.Context, releaseID int64) ([]PlannedDomain, error) {
	release, err := s.Release(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	snapshot, err := decodeReleaseRuntimeSnapshot(release)
	if err != nil {
		return nil, err
	}
	return append([]PlannedDomain(nil), snapshot.Domains...), nil
}
