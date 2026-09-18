package deploy

import (
	"fmt"
	"sort"
)

// DiagnosisSeverity orders findings by what an operator must do about them.
type DiagnosisSeverity string

const (
	DiagnosisCritical DiagnosisSeverity = "critical"
	DiagnosisWarning  DiagnosisSeverity = "warning"
	DiagnosisNotice   DiagnosisSeverity = "notice"
)

// DiagnosisFinding follows the existing finding vocabulary: what was measured,
// what it means, and the action that follows. Owner names the feature that owns
// the remedy, because Deployments summarizes a finding but never re-implements
// another module's fix.
type DiagnosisFinding struct {
	Code     string            `json:"code"`
	Severity DiagnosisSeverity `json:"severity"`
	Title    string            `json:"title"`
	Measured string            `json:"measured"`
	Means    string            `json:"means"`
	Action   string            `json:"action"`
	Owner    string            `json:"owner"`
	DeepLink string            `json:"deepLink,omitempty"`
	External bool              `json:"external,omitempty"`
}

// DiagnosisSilence records a question that was deliberately not answered. An
// unavailable owner must never be rendered as a clean result, and it must never
// be rendered as a problem either.
type DiagnosisSilence struct {
	Subject string `json:"subject"`
	Reason  string `json:"reason"`
}

type Diagnosis struct {
	Status   string             `json:"status"`
	Findings []DiagnosisFinding `json:"findings"`
	Silences []DiagnosisSilence `json:"silences"`
}

// DiagnosisInput is the complete evidence Diagnose is allowed to reason from.
// It carries no owner handles on purpose: diagnosis is a pure function over
// observations already made, so every claim and every silence is testable.
type DiagnosisInput struct {
	LiveReleaseID    int64
	RuntimeRecorded  bool
	PendingChanges   bool
	DesiredRevision  int
	LivePlanRevision int
	Runtime          RuntimeServices
	Domains          DomainSummary
	Storage          StorageSummary
	Backups          BackupSummary
	Dependencies     DependencySummary
}

// Diagnose is conservative by construction. It claims a problem only from
// evidence an owner actually returned, and records a silence for every subject
// whose owner was unavailable. "Assessed" means every subject was answered.
func Diagnose(input DiagnosisInput) Diagnosis {
	result := Diagnosis{Status: "assessed", Findings: []DiagnosisFinding{}, Silences: []DiagnosisSilence{}}
	silence := func(subject, reason string) {
		result.Status = "partial"
		result.Silences = append(result.Silences, DiagnosisSilence{Subject: subject, Reason: reason})
	}
	find := func(finding DiagnosisFinding) { result.Findings = append(result.Findings, finding) }

	if input.LiveReleaseID <= 0 {
		// A deployment that has never deployed has nothing to be wrong with.
		// Its saved plan is not "not live yet"; it has simply not run.
		silence("runtime", "This deployment has no live release, so no runtime, domain or storage claim can be made.")
		return result
	}

	switch {
	case input.Runtime.Status != statusAvailable:
		silence("runtime", input.Runtime.Reason)
	case !input.RuntimeRecorded:
		silence("runtime", "The live release has no recorded runtime, so its containers cannot be identified.")
	default:
		live := []RuntimeService{}
		for _, service := range input.Runtime.Services {
			if service.LiveRelease {
				live = append(live, service)
			}
		}
		if len(live) == 0 {
			find(DiagnosisFinding{
				Code: "runtime_absent", Severity: DiagnosisCritical,
				Title:    "The live release has no running container",
				Measured: "Docker returned no managed container for the live release.",
				Means:    "Whatever this deployment serves is not being served by the release the dashboard believes is live.",
				Action:   "Open Docker to check whether the container was removed, then redeploy or roll back.",
				Owner:    "docker", DeepLink: "/docker/containers",
			})
		}
		for _, service := range live {
			if service.State != "running" {
				find(DiagnosisFinding{
					Code: "runtime_not_running", Severity: DiagnosisCritical,
					Title:    fmt.Sprintf("%s is not running", serviceLabel(service)),
					Measured: fmt.Sprintf("Docker reports state %q for this container of the live release.", service.State),
					Means:    "This service of the live release is not serving traffic.",
					Action:   "Open the container in Docker to read its exit reason and logs.",
					Owner:    "docker", DeepLink: containerDeepLink(service.ContainerID),
				})
				continue
			}
			if service.Health == "unhealthy" {
				find(DiagnosisFinding{
					Code: "runtime_unhealthy", Severity: DiagnosisCritical,
					Title:    fmt.Sprintf("%s reports an unhealthy check", serviceLabel(service)),
					Measured: "Docker reports this running container's own health check as unhealthy.",
					Means:    "The container is up but its image says it is not ready to serve.",
					Action:   "Open the container in Docker to read the failing health check output.",
					Owner:    "docker", DeepLink: containerDeepLink(service.ContainerID),
				})
			}
		}
	}

	if input.Domains.Status != statusAvailable {
		silence("domains", domainSilenceReason(input.Domains))
	} else {
		for _, domain := range input.Domains.Domains {
			switch domain.Route {
			case "missing":
				find(DiagnosisFinding{
					Code: "domain_unrouted", Severity: DiagnosisWarning,
					Title:    fmt.Sprintf("No proxy site serves %s", domain.Hostname),
					Measured: "The proxy inventory contains no enabled site with this server name.",
					Means:    "Requests for this hostname do not reach this deployment through the dashboard's proxy.",
					Action:   "Deploy this release again to reapply its route, or add the site in Proxy.",
					Owner:    "proxy", DeepLink: "/proxy/sites",
				})
			case "foreign":
				find(DiagnosisFinding{
					Code: "domain_foreign_route", Severity: DiagnosisCritical,
					Title:    fmt.Sprintf("%s is served by another site", domain.Hostname),
					Measured: fmt.Sprintf("Site %s claims this server name; this deployment's generated site does not.", domain.ServedBy),
					Means:    "Traffic for this hostname reaches whatever that site points at, not this release.",
					Action:   "Open the conflicting site in Proxy and decide which one owns the hostname.",
					Owner:    "proxy", DeepLink: domain.DeepLink,
				})
			case "conflict":
				find(DiagnosisFinding{
					Code: "domain_conflict", Severity: DiagnosisCritical,
					Title:    fmt.Sprintf("Two proxy sites claim %s", domain.Hostname),
					Measured: fmt.Sprintf("Both this deployment's generated site and %s declare this server name.", domain.ServedBy),
					Means:    "Which site answers is decided by the proxy's own ordering, not by this deployment.",
					Action:   "Open the conflicting site in Proxy and remove the duplicate server name.",
					Owner:    "proxy", DeepLink: domain.DeepLink,
				})
			}
			switch domain.Certificate {
			case statusUnavailable:
				if domain.HTTPS {
					silence("certificates", fmt.Sprintf("Certificate inventory is unavailable, so HTTPS for %s was not assessed.", domain.Hostname))
				}
			case "missing":
				find(DiagnosisFinding{
					Code: "certificate_missing", Severity: DiagnosisWarning,
					Title:    fmt.Sprintf("No certificate covers %s", domain.Hostname),
					Measured: "This release requests HTTPS and no installed certificate lists this domain.",
					Means:    "Browsers reaching this hostname over HTTPS see a certificate error.",
					Action:   "Issue a certificate for this domain in Proxy.",
					Owner:    "proxy", DeepLink: "/proxy/certificates",
				})
			case "expired":
				find(DiagnosisFinding{
					Code: "certificate_expired", Severity: DiagnosisCritical,
					Title:    fmt.Sprintf("The certificate for %s has expired", domain.Hostname),
					Measured: fmt.Sprintf("Certificate %s covers this domain and is past its expiry date.", domain.CertificateName),
					Means:    "Every HTTPS request to this hostname is rejected by the browser.",
					Action:   "Renew the certificate in Proxy.",
					Owner:    "proxy", DeepLink: "/proxy/certificates",
				})
			case "expiring":
				find(DiagnosisFinding{
					Code: "certificate_expiring", Severity: DiagnosisWarning,
					Title:    fmt.Sprintf("The certificate for %s expires in %d days", domain.Hostname, domain.CertificateDaysLeft),
					Measured: fmt.Sprintf("Certificate %s covers this domain and is inside its renewal window.", domain.CertificateName),
					Means:    "HTTPS keeps working until it expires, and then stops.",
					Action:   "Confirm automatic renewal or renew the certificate in Proxy.",
					Owner:    "proxy", DeepLink: "/proxy/certificates",
				})
			}
		}
	}

	if input.Storage.Status != statusAvailable {
		silence("storage", storageSilenceReason(input.Storage))
	} else {
		for _, mount := range input.Storage.Mounts {
			switch mount.Status {
			case "missing":
				find(DiagnosisFinding{
					Code: "storage_missing", Severity: DiagnosisCritical,
					Title:    fmt.Sprintf("Persistent storage for %s is not present", mount.Target),
					Measured: fmt.Sprintf("The storage owner could not find %s.", mount.Source),
					Means:    "Data written to this path is not in the location the release declared.",
					Action:   "Open the storage owner to confirm whether the volume or path was removed.",
					Owner:    "docker", DeepLink: mount.DeepLink,
				})
			case statusUnavailable:
				silence("storage", fmt.Sprintf("No owner observation was returned for %s.", mount.Source))
			}
		}
	}

	if input.Backups.Status != statusAvailable {
		silence("backups", backupSilenceReason(input.Backups))
	} else {
		for _, job := range input.Backups.Jobs {
			switch {
			case job.Status == "missing":
				find(DiagnosisFinding{
					Code: "backup_missing", Severity: DiagnosisCritical,
					Title:    "A declared backup job no longer exists",
					Measured: fmt.Sprintf("Backups has no job %s.", job.ResourceID),
					Means:    "This release believes its data is protected by a job that is gone.",
					Action:   "Recreate the backup job in Backups or remove the dependency from this deployment.",
					Owner:    "backups", DeepLink: job.DeepLink,
				})
			case job.Status == statusUnavailable:
				silence("backups", fmt.Sprintf("No owner observation was returned for backup job %s.", job.ResourceID))
			case job.Required && !job.Fresh:
				find(DiagnosisFinding{
					Code: "backup_stale", Severity: DiagnosisWarning,
					Title:    "The required backup is not within its maximum age",
					Measured: backupStaleMeasurement(job),
					Means:    "The next deployment of this release is blocked until a fresh backup succeeds.",
					Action:   "Run the backup job in Backups and read its result.",
					Owner:    "backups", DeepLink: job.DeepLink,
				})
			}
		}
	}

	if input.Dependencies.Status != statusAvailable {
		silence("dependencies", dependencySilenceReason(input.Dependencies))
	} else {
		for _, item := range input.Dependencies.Items {
			if item.Available {
				continue
			}
			if item.Detail == "" {
				silence("dependencies", fmt.Sprintf("No owner observation was returned for %s %s.", item.ResourceKind, item.ResourceID))
				continue
			}
			find(DiagnosisFinding{
				Code: "dependency_unavailable", Severity: DiagnosisWarning,
				Title:    fmt.Sprintf("A %s dependency is unavailable", humanResourceKind(item.ResourceKind)),
				Measured: item.Detail,
				Means:    "This release names a resource its owner cannot currently confirm.",
				Action:   "Open the owning section to confirm the resource still exists.",
				Owner:    dependencyOwner(PlannedDependency{Kind: item.Kind}), DeepLink: item.DeepLink,
				External: item.DeepLink == "",
			})
		}
	}

	if input.PendingChanges {
		find(pendingChangeFinding(input))
	}

	sort.SliceStable(result.Findings, func(i, j int) bool {
		return severityRank(result.Findings[i].Severity) < severityRank(result.Findings[j].Severity)
	})
	return result
}

func pendingChangeFinding(input DiagnosisInput) DiagnosisFinding {
	measured := "Saved configuration has not been deployed."
	if input.LiveReleaseID > 0 && input.DesiredRevision != input.LivePlanRevision {
		measured = fmt.Sprintf("Saved plan revision %d is not the live release's revision %d.",
			input.DesiredRevision, input.LivePlanRevision)
	}
	return DiagnosisFinding{
		Code: "plan_pending", Severity: DiagnosisNotice,
		Title:    "Saved changes are not live",
		Measured: measured,
		Means:    "What this page shows as configuration is not what is currently running.",
		Action:   "Review the pending changes and deploy when they are ready.",
		Owner:    "deploy",
	}
}

func severityRank(severity DiagnosisSeverity) int {
	switch severity {
	case DiagnosisCritical:
		return 0
	case DiagnosisWarning:
		return 1
	default:
		return 2
	}
}

func serviceLabel(service RuntimeService) string {
	if service.Name != "" {
		return service.Name
	}
	if len(service.ContainerID) > 12 {
		return service.ContainerID[:12]
	}
	return service.ContainerID
}

func containerDeepLink(containerID string) string {
	if containerID == "" {
		return "/docker/containers"
	}
	return "/docker/containers?container=" + containerID
}

func humanResourceKind(resourceKind string) string {
	switch resourceKind {
	case "database_connection":
		return "database"
	case "docker_volume":
		return "volume"
	case "bind_path":
		return "path"
	case "backup_job":
		return "backup"
	default:
		return resourceKind
	}
}

func backupStaleMeasurement(job BackupJob) string {
	if job.LastStatus == "" || job.LastStatus == "never run" {
		return fmt.Sprintf("Backup job %s has no successful run on record.", job.ResourceID)
	}
	return fmt.Sprintf("The last run of backup job %s finished with status %q outside the configured maximum age.",
		job.ResourceID, job.LastStatus)
}

func domainSilenceReason(summary DomainSummary) string {
	if summary.Reason != "" {
		return summary.Reason
	}
	return "Proxy evidence for these domains is unavailable."
}

func storageSilenceReason(summary StorageSummary) string {
	if summary.Reason != "" {
		return summary.Reason
	}
	return "Storage evidence for this release is unavailable."
}

func backupSilenceReason(summary BackupSummary) string {
	if summary.Reason != "" {
		return summary.Reason
	}
	return "Backup evidence for this release is unavailable."
}

func dependencySilenceReason(summary DependencySummary) string {
	if summary.Reason != "" {
		return summary.Reason
	}
	return "Dependency evidence for this release is unavailable."
}
