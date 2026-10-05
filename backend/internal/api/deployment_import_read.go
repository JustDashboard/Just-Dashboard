package api

import (
	"context"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// Imported runtimes have no release pointer. Refresh one inventory for the
// whole response, so monitoring a fleet never scans the host once per project.
func (s *Server) refreshImportedDeployments(ctx context.Context, summaries []deploy.DeploymentSummary) {
	needed := false
	for _, summary := range summaries {
		needed = needed || summary.ImportedWorkload != nil
	}
	if !needed {
		return
	}
	report, err := s.discoverWorkloads(ctx)
	current := map[string]deploy.WorkloadCandidate{}
	if err == nil {
		for _, candidate := range report.Items {
			current[candidate.Key] = candidate
		}
	}
	for index := range summaries {
		summary := &summaries[index]
		stored := summary.ImportedWorkload
		if stored == nil {
			continue
		}
		candidate, found := current[stored.Key]
		if !found {
			candidate = *stored
			candidate.State = "unavailable"
			candidate.Running = 0
			candidate.Warnings = append(append([]string(nil), stored.Warnings...),
				"The original workload could not be observed now. It may have been removed or its manager may be unavailable; its saved inventory is shown.")
		}
		summary.ImportedWorkload = &candidate
		summary.PendingChanges = false
		summary.ServiceCount = candidate.Total
		summary.Images = []string{}
		for _, service := range candidate.Services {
			if service.Image != "" {
				summary.Images = append(summary.Images, service.Image)
			}
		}
	}
}
