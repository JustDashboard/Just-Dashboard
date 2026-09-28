package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ReleasedHostnames is every hostname a release on this host was built to
// serve, retired releases included: any of them can be rolled
// back to, and on a Docker Caddy host the certificate copy kept for its
// hostname is what activation resolves. The Certificates page prunes only
// copies no name here claims, so HTTP-only names count too, and a snapshot
// it cannot read is an error rather than a release that names nothing.
func (s *OrchestrationStore) ReleasedHostnames(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.release_id, a.metadata_json
		  FROM deploy_release_artifacts a
		  JOIN deploy_releases r ON r.id = a.release_id
		 WHERE a.kind = ?`, string(ArtifactRuntimeConfig))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	out := []string{}
	for rows.Next() {
		var releaseID int64
		var metadata string
		if err := rows.Scan(&releaseID, &metadata); err != nil {
			return nil, err
		}
		var envelope struct {
			Snapshot struct {
				Domains []PlannedDomain `json:"domains"`
			} `json:"snapshot"`
		}
		if err := json.Unmarshal([]byte(metadata), &envelope); err != nil {
			return nil, fmt.Errorf("release %d's runtime snapshot could not be read: %w", releaseID, err)
		}
		for _, domain := range envelope.Snapshot.Domains {
			hostname := strings.ToLower(strings.TrimSpace(domain.Hostname))
			if hostname != "" && !seen[hostname] {
				seen[hostname] = true
				out = append(out, hostname)
			}
		}
	}
	return out, rows.Err()
}
