package api

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// auditReleaseImagePrune records each release image retention removed, or
// tried to and could not, as the system: a removal nobody pressed a button
// for is exactly what the audit log is read back for.
func (s *Server) auditReleaseImagePrune(ctx context.Context, event deploy.ArtifactPruneEvent) {
	decision := event.Decision
	detail := map[string]any{
		"releaseId": decision.Artifact.ReleaseID, "kind": decision.Artifact.Kind,
		"digest": decision.Artifact.Digest, "sizeBytes": decision.Artifact.SizeBytes,
		"reason": decision.Reason, "trigger": event.Trigger,
	}
	if decision.Error != "" {
		detail["error"] = decision.Error
	}
	encoded, _ := json.Marshal(detail)
	target := decision.Artifact.Reference
	if target == "" {
		target = "artifact " + strconv.FormatInt(decision.Artifact.ID, 10)
	}
	s.Audit.Record(ctx, audit.Entry{
		Actor: "system", Action: "deploy.release.artifact.prune", Target: target,
		Success: decision.Removed, Detail: string(encoded),
	})
}
