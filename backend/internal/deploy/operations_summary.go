package deploy

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// CertificateObserver is the Proxy module's certificate inventory. It is a
// separate owner from PlanningProxy because a host can serve a route without
// certbot present, and a missing certificate reader must read as unassessed
// rather than as a missing certificate.
type CertificateObserver interface {
	ListCertificates(context.Context) ([]proxysvc.Certificate, error)
}

// OperationsOwners names the feature owners an operational summary reads from.
// Every field is optional: a host without Docker, nginx or Backups still
// produces a summary, and the sections that needed the absent owner say so.
type OperationsOwners struct {
	Runtime      RuntimeObserver
	Proxy        PlanningProxy
	Certificates CertificateObserver
	Dependencies PlanningDependencies
}

// OperationsSummary is the deployment workspace's read of what its live release
// currently depends on. It never restates a feature owner's inventory from
// deployment records; each section carries the owner's own availability.
type OperationsSummary struct {
	ObservedAt   time.Time         `json:"observedAt"`
	ReleaseID    int64             `json:"releaseId,omitempty"`
	Evidence     string            `json:"evidence"`
	Reason       string            `json:"reason,omitempty"`
	Runtime      RuntimeServices   `json:"runtime"`
	Domains      DomainSummary     `json:"domains"`
	Storage      StorageSummary    `json:"storage"`
	Backups      BackupSummary     `json:"backups"`
	Dependencies DependencySummary `json:"dependencies"`
	Diagnosis    Diagnosis         `json:"diagnosis"`
}

type DomainSummary struct {
	Status   string        `json:"status"`
	Reason   string        `json:"reason,omitempty"`
	SiteName string        `json:"siteName,omitempty"`
	Domains  []DomainRoute `json:"domains"`
}

// DomainRoute separates the three facts a saved domain never proves on its
// own: a route serves it, a certificate covers it, and nothing else claims it.
type DomainRoute struct {
	Hostname            string        `json:"hostname"`
	HTTPS               bool          `json:"https"`
	Ownership           OwnershipMode `json:"ownership"`
	Route               string        `json:"route"`
	ServedBy            string        `json:"servedBy,omitempty"`
	Certificate         string        `json:"certificate"`
	Protected           bool          `json:"protected,omitempty"`
	CertificateName     string        `json:"certificateName,omitempty"`
	CertificateDaysLeft int           `json:"certificateDaysLeft,omitempty"`
	DeepLink            string        `json:"deepLink,omitempty"`
	CertificateLink     string        `json:"certificateLink,omitempty"`
}

type StorageSummary struct {
	Status string         `json:"status"`
	Reason string         `json:"reason,omitempty"`
	Mounts []StorageMount `json:"mounts"`
}

type StorageMount struct {
	Source    string        `json:"source"`
	Target    string        `json:"target"`
	Kind      string        `json:"kind"`
	ReadOnly  bool          `json:"readOnly,omitempty"`
	Ownership OwnershipMode `json:"ownership"`
	Status    string        `json:"status"`
	Detail    string        `json:"detail,omitempty"`
	DeepLink  string        `json:"deepLink,omitempty"`
}

type BackupSummary struct {
	Status string      `json:"status"`
	Reason string      `json:"reason,omitempty"`
	Jobs   []BackupJob `json:"jobs"`
}

type BackupJob struct {
	ResourceID string `json:"resourceId"`
	Required   bool   `json:"required"`
	Status     string `json:"status"`
	LastStatus string `json:"lastStatus,omitempty"`
	Fresh      bool   `json:"fresh"`
	Detail     string `json:"detail,omitempty"`
	DeepLink   string `json:"deepLink,omitempty"`
}

type DependencySummary struct {
	Status string                  `json:"status"`
	Reason string                  `json:"reason,omitempty"`
	Items  []DependencyObservation `json:"items"`
}

const (
	statusAvailable   = "available"
	statusUnavailable = "unavailable"
)

// Operations reads the live release's own snapshot and asks each feature owner
// about the resources that release named. A deployment with no live release
// reports that, rather than describing a desired plan as if it were running.
func (s *OrchestrationStore) Operations(
	ctx context.Context,
	owners OperationsOwners,
	summary DeploymentSummary,
) (*OperationsSummary, error) {
	result := &OperationsSummary{
		ObservedAt: s.now().UTC(), Evidence: "none",
		Runtime:      observeRuntimeServices(ctx, owners.Runtime, summary.EnvironmentID, summary.LiveReleaseID, 0),
		Domains:      DomainSummary{Status: statusUnavailable, Domains: []DomainRoute{}},
		Storage:      StorageSummary{Status: statusUnavailable, Mounts: []StorageMount{}},
		Backups:      BackupSummary{Status: statusUnavailable, Jobs: []BackupJob{}},
		Dependencies: DependencySummary{Status: statusUnavailable, Items: []DependencyObservation{}},
	}
	input := DiagnosisInput{
		LiveReleaseID: summary.LiveReleaseID, PendingChanges: summary.PendingChanges,
		DesiredRevision: summary.DesiredRevision, LivePlanRevision: summary.LivePlanRevision,
		Runtime: result.Runtime,
	}
	if summary.LiveReleaseID <= 0 {
		result.Reason = "This deployment has no live release. Deploy it to record the runtime, domain and storage evidence this page reads."
		noEvidence := "No live release names them."
		result.Domains.Reason, result.Storage.Reason = noEvidence, noEvidence
		result.Backups.Reason, result.Dependencies.Reason = noEvidence, noEvidence
		result.Diagnosis = Diagnose(input)
		return result, nil
	}
	release, err := s.Release(ctx, summary.LiveReleaseID)
	if err != nil {
		return nil, err
	}
	if release.Release.ProjectID != summary.ID || release.Release.EnvironmentID != summary.EnvironmentID {
		return nil, ErrInvalidPlan
	}
	snapshot, err := decodeReleaseRuntimeSnapshot(release)
	if err != nil {
		result.Reason = "The live release runtime snapshot is unavailable, so its dependencies cannot be named. Review release history."
		unreadable := "The live release runtime snapshot could not be read."
		result.Domains.Reason, result.Storage.Reason = unreadable, unreadable
		result.Backups.Reason, result.Dependencies.Reason = unreadable, unreadable
		result.Diagnosis = Diagnose(input)
		return result, nil
	}
	result.Evidence, result.ReleaseID = "release", summary.LiveReleaseID
	observed := observeDependencies(ctx, owners.Dependencies, snapshot)
	result.Domains = observeDomainRoutes(ctx, owners, snapshot.Domains, summary.EnvironmentID)
	result.Storage = storageSummary(snapshot, observed)
	result.Backups = backupSummary(snapshot, observed)
	result.Dependencies = otherDependencySummary(snapshot, observed)
	input.Domains, input.Storage = result.Domains, result.Storage
	input.Backups, input.Dependencies = result.Backups, result.Dependencies
	input.RuntimeRecorded = s.releaseRuntimeRecorded(ctx, summary.LiveReleaseID)
	result.Diagnosis = Diagnose(input)
	return result, nil
}

func (s *OrchestrationStore) releaseRuntimeRecorded(ctx context.Context, releaseID int64) bool {
	runtime, err := s.RuntimeForRelease(ctx, releaseID)
	return err == nil && runtime.State == "live"
}

type dependencyObservations struct {
	available bool
	byKey     map[string]DependencyObservation
}

func dependencyKey(resourceKind, resourceID string) string {
	return resourceKind + "\x00" + resourceID
}

// observeDependencies asks the owner about every resource the release named in
// one call. Rendering each section separately must not turn into one inventory
// read per row.
func observeDependencies(
	ctx context.Context,
	owner PlanningDependencies,
	snapshot runtimeReleaseSnapshot,
) dependencyObservations {
	result := dependencyObservations{byKey: map[string]DependencyObservation{}}
	requested := make([]PlannedDependency, 0, len(snapshot.Dependencies)+len(snapshot.Plan.Mounts))
	seen := map[string]bool{}
	add := func(dependency PlannedDependency) {
		key := dependencyKey(dependency.ResourceKind, dependency.ResourceID)
		if seen[key] || dependency.ResourceKind == "" || dependency.ResourceID == "" {
			return
		}
		seen[key] = true
		requested = append(requested, dependency)
	}
	for _, dependency := range snapshot.Dependencies {
		add(dependency)
	}
	for _, mount := range snapshot.Plan.Mounts {
		add(PlannedDependency{
			Kind: "storage", Ownership: mount.Ownership,
			ResourceKind: mountResourceKind(mount.Source), ResourceID: mount.Source,
		})
	}
	if owner == nil || len(requested) == 0 {
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	observations, err := owner.ObserveDependencies(ctx, requested)
	if err != nil {
		return result
	}
	result.available = true
	for _, observation := range observations {
		result.byKey[dependencyKey(observation.ResourceKind, observation.ResourceID)] = observation
	}
	return result
}

func mountResourceKind(source string) string {
	if filepath.IsAbs(source) {
		return "bind_path"
	}
	return "docker_volume"
}

func storageSummary(snapshot runtimeReleaseSnapshot, observed dependencyObservations) StorageSummary {
	result := StorageSummary{Status: statusUnavailable, Mounts: []StorageMount{}}
	if len(snapshot.Plan.Mounts) == 0 {
		result.Status = statusAvailable
		return result
	}
	if !observed.available {
		result.Reason = "Storage inventory is unavailable. Open Docker to check the connection."
		return result
	}
	result.Status = statusAvailable
	for _, mount := range snapshot.Plan.Mounts {
		kind := mountResourceKind(mount.Source)
		row := StorageMount{
			Source: mount.Source, Target: mount.Target, Kind: strings.TrimSuffix(kind, "_path"),
			ReadOnly: mount.ReadOnly, Ownership: mount.Ownership, Status: statusUnavailable,
		}
		if kind == "docker_volume" {
			row.Kind = "volume"
			row.DeepLink = "/docker/volumes?" + url.Values{"volume": {mount.Source}}.Encode()
		} else {
			row.Kind = "bind"
			row.DeepLink = "/files?" + url.Values{"path": {mount.Source}}.Encode()
		}
		observation, found := observed.byKey[dependencyKey(kind, mount.Source)]
		switch {
		case !found:
			row.Detail = "The storage owner returned no observation for this path."
		case observation.Available:
			row.Status = "present"
		default:
			row.Status = "missing"
			row.Detail = observation.Detail
		}
		result.Mounts = append(result.Mounts, row)
	}
	sort.Slice(result.Mounts, func(i, j int) bool { return result.Mounts[i].Target < result.Mounts[j].Target })
	return result
}

func backupSummary(snapshot runtimeReleaseSnapshot, observed dependencyObservations) BackupSummary {
	result := BackupSummary{Status: statusUnavailable, Jobs: []BackupJob{}}
	declared := []PlannedDependency{}
	for _, dependency := range snapshot.Dependencies {
		if dependency.Kind == "backup" || dependency.ResourceKind == "backup_job" {
			declared = append(declared, dependency)
		}
	}
	if len(declared) == 0 {
		result.Status = statusAvailable
		result.Reason = "This release declares no backup policy. Persistent storage is not a backup."
		return result
	}
	if !observed.available {
		result.Reason = "Backups inventory is unavailable. Open Backups to check the module."
		return result
	}
	result.Status = statusAvailable
	for _, dependency := range declared {
		config, _ := decodeBackupDependencyConfig(dependency.Config)
		job := BackupJob{
			ResourceID: dependency.ResourceID, Required: config.RequiredBeforeDeploy,
			Status: statusUnavailable, DeepLink: "/backups",
		}
		observation, found := observed.byKey[dependencyKey(dependency.ResourceKind, dependency.ResourceID)]
		switch {
		case !found:
			job.Detail = "The Backups owner returned no observation for this job."
		case !observation.Available:
			job.Status, job.Detail = "missing", observation.Detail
		default:
			job.Status, job.LastStatus, job.Fresh = "present", observation.Status, observation.Fresh
			if observation.DeepLink != "" {
				job.DeepLink = observation.DeepLink
			}
		}
		result.Jobs = append(result.Jobs, job)
	}
	sort.Slice(result.Jobs, func(i, j int) bool { return result.Jobs[i].ResourceID < result.Jobs[j].ResourceID })
	return result
}

func otherDependencySummary(snapshot runtimeReleaseSnapshot, observed dependencyObservations) DependencySummary {
	result := DependencySummary{Status: statusUnavailable, Items: []DependencyObservation{}}
	declared := []PlannedDependency{}
	for _, dependency := range snapshot.Dependencies {
		switch {
		case dependency.Kind == "backup" || dependency.ResourceKind == "backup_job":
		case dependency.ResourceKind == "docker_volume" || dependency.ResourceKind == "bind_path":
		default:
			declared = append(declared, dependency)
		}
	}
	if len(declared) == 0 {
		result.Status = statusAvailable
		return result
	}
	if !observed.available {
		result.Reason = "Dependency inventory is unavailable. Open the owning section to check it."
		return result
	}
	result.Status = statusAvailable
	for _, dependency := range declared {
		observation, found := observed.byKey[dependencyKey(dependency.ResourceKind, dependency.ResourceID)]
		if !found {
			observation = DependencyObservation{
				Kind: dependency.Kind, ResourceKind: dependency.ResourceKind, ResourceID: dependency.ResourceID,
				Detail: "The owning module returned no observation for this resource.",
			}
		}
		result.Items = append(result.Items, observation)
	}
	sort.Slice(result.Items, func(i, j int) bool {
		if result.Items[i].ResourceKind != result.Items[j].ResourceKind {
			return result.Items[i].ResourceKind < result.Items[j].ResourceKind
		}
		return result.Items[i].ResourceID < result.Items[j].ResourceID
	})
	return result
}

// observeDomainRoutes answers three separate questions from the Proxy owner:
// which site serves the hostname, whether anything else claims it, and which
// certificate covers it. A saved domain proves none of them.
func observeDomainRoutes(
	ctx context.Context,
	owners OperationsOwners,
	domains []PlannedDomain,
	environmentID int64,
) DomainSummary {
	result := DomainSummary{Status: statusUnavailable, Domains: []DomainRoute{}}
	if len(domains) == 0 {
		result.Status = statusAvailable
		result.Reason = "This release serves no public domain."
		return result
	}
	result.SiteName = deploymentRouteName(environmentID)
	if owners.Proxy == nil {
		result.Reason = "The Proxy module is unavailable, so no route or certificate evidence exists for these domains."
		for _, domain := range domains {
			result.Domains = append(result.Domains, DomainRoute{
				Hostname: domain.Hostname, HTTPS: domain.HTTPS, Ownership: domain.Ownership, Protected: domain.Protection != nil,
				Route: statusUnavailable, Certificate: statusUnavailable,
			})
		}
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	availability := owners.Proxy.Availability(ctx)
	vhosts, err := owners.Proxy.ListVHosts(ctx)
	if err != nil {
		result.Reason = "Proxy site inventory could not be read. Open Proxy to check the connection."
		return result
	}
	if !availability.Nginx && !availability.Caddy {
		result.Reason = "No proxy server is installed on this host. These domains are not served by the dashboard."
	}
	certificates, certificatesRead := []proxysvc.Certificate{}, false
	if owners.Certificates != nil {
		if listed, listErr := owners.Certificates.ListCertificates(ctx); listErr == nil {
			certificates, certificatesRead = listed, true
		}
	}
	result.Status = statusAvailable
	for _, domain := range domains {
		hostname := strings.ToLower(strings.TrimSpace(domain.Hostname))
		row := DomainRoute{
			Hostname: domain.Hostname, HTTPS: domain.HTTPS, Ownership: domain.Ownership, Protected: domain.Protection != nil,
			Route: "missing", Certificate: statusUnavailable,
		}
		owned, foreign := "", ""
		for _, vhost := range vhosts {
			if !vhostServes(vhost, hostname) {
				continue
			}
			if vhost.Name == result.SiteName {
				owned = vhost.Name
				continue
			}
			if foreign == "" || vhost.Name < foreign {
				foreign = vhost.Name
			}
		}
		switch {
		case owned != "" && foreign != "":
			row.Route, row.ServedBy = "conflict", foreign
		case owned != "":
			row.Route, row.ServedBy = "served", owned
		case foreign != "":
			row.Route, row.ServedBy = "foreign", foreign
		}
		if row.ServedBy != "" {
			row.DeepLink = "/proxy/sites?" + url.Values{"site": {row.ServedBy}}.Encode()
		}
		switch {
		case !domain.HTTPS:
			row.Certificate = "not requested"
		case !certificatesRead:
			row.Certificate = statusUnavailable
		default:
			row.Certificate = "missing"
			for _, certificate := range certificates {
				if certificate.Error != "" || !certificateCoversDomain(certificate.Domains, hostname) {
					continue
				}
				row.CertificateName, row.CertificateDaysLeft = certificate.Name, certificate.DaysLeft
				row.CertificateLink = "/proxy/certificates"
				switch {
				case certificate.Expired:
					row.Certificate = "expired"
				case certificate.Expiring:
					row.Certificate = "expiring"
				default:
					row.Certificate = "valid"
				}
				break
			}
		}
		result.Domains = append(result.Domains, row)
	}
	sort.Slice(result.Domains, func(i, j int) bool { return result.Domains[i].Hostname < result.Domains[j].Hostname })
	return result
}

func vhostServes(vhost proxysvc.VHost, hostname string) bool {
	if !vhost.Enabled {
		return false
	}
	for _, name := range vhost.ServerNames {
		if certificateCoversDomain([]string{name}, hostname) {
			return true
		}
	}
	return false
}

// deploymentRouteName is the single spelling of the generated proxy site an
// environment owns. Activation, removal, preflight conflict exclusion and the
// operational summary must all mean the same file.
func deploymentRouteName(environmentID int64) string {
	return fmt.Sprintf("just-dashboard-env-%d.conf", environmentID)
}
