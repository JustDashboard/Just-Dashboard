package deploy

import "fmt"

// Each acknowledgement follows its evidence, not its position in a warning list.
// Grouping in the browser never replaces the individual server-required codes.
func adoptionPreflightFindings(adoption *WorkloadAdoption) []PreflightFinding {
	findings := []PreflightFinding{}
	represented := map[string]bool{}
	seen := map[string]bool{}
	for _, issue := range adoption.Issues {
		message := issue.Message
		if issue.Service != "" {
			message = issue.Service + ": " + message
		}
		represented[message] = true
		code := "adoption_" + issue.Code + "_" + digestBytes(mustJSON(issue))[:12]
		if seen[code] {
			continue
		}
		seen[code] = true
		severity := PreflightWarning
		title, action := adoptionIssueReading(issue.Code)
		means := "Adoption preserves the current runtime. This limitation applies when deploying changes."
		if issue.Blocking {
			severity = PreflightBlocked
			means = "The replacement cannot yet reproduce this part of the existing application."
		}
		item := finding(code, severity, title, message, means, action, "deploy", "adoption")
		item.IssueCode, item.Service = issue.Code, issue.Service
		findings = append(findings, item)
	}
	// Drafts captured before structured issues shipped keep their original acknowledgements.
	for index, message := range adoption.Warnings {
		if !represented[message] {
			findings = append(findings, finding(fmt.Sprintf("adoption_warning_%d", index+1), PreflightWarning,
				"Review recovered runtime behavior", message,
				"Import registers the current runtime. A later Deploy applies the reviewed recipe and may restart services.",
				"Review this limitation before adopting the workload.", "deploy", "adoption"))
		}
	}
	for index, message := range adoption.Blockers {
		if !represented[message] {
			findings = append(findings, finding(fmt.Sprintf("adoption_blocked_%d", index+1), PreflightBlocked,
				"Recovery has an unresolved limitation", message, "A complete replacement cannot yet be reproduced safely.",
				"Resolve the original configuration and inspect again.", "deploy", "adoption"))
		}
	}
	return findings
}

func adoptionIssueReading(code string) (string, string) {
	switch code {
	case "persistent_data_reused":
		return "Existing data needs a recovery plan", "Verify a backup before deploying changes; image rollback does not restore data."
	case "regenerable_python_cache", "regenerable_n8n_editor_cache":
		return "Verified generated files will be recreated", "Review the verified generated paths; other writable-layer changes remain blockers."
	case "original_image_captured", "recovered_image", "original_image_missing", "recovered_runtime_image", "missing_image_snapshotted":
		return "A private recovery image preserves the current filesystem", "Keep the recovery image available for deployment and rollback."
	case "original_build_preserved", "original_build_definition", "build_source_unavailable", "build_source_retained":
		return "The current image remains available for redeployment", "Review the source evidence for future builds."
	case "declared_service_excluded", "inactive_service_excluded", "compose_services_excluded":
		return "Absent declared services are excluded", "Verify that the existing services do not depend on an excluded service."
	case "container_replacement":
		return "Deploy changes replaces the current container", "Allow for the stop-first interruption; failed replacements restore the baseline."
	default:
		return "Review recovered runtime behavior", "Review this limitation before adopting the workload."
	}
}
