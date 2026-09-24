package deploy

import (
	"strings"
	"testing"
)

func findingCodes(diagnosis Diagnosis) []string {
	codes := make([]string, 0, len(diagnosis.Findings))
	for _, finding := range diagnosis.Findings {
		codes = append(codes, finding.Code)
	}
	return codes
}

func silenceSubjects(diagnosis Diagnosis) []string {
	subjects := make([]string, 0, len(diagnosis.Silences))
	for _, silence := range diagnosis.Silences {
		subjects = append(subjects, silence.Subject)
	}
	return subjects
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func healthyDiagnosisInput() DiagnosisInput {
	return DiagnosisInput{
		LiveReleaseID: 7, RuntimeRecorded: true, DesiredRevision: 3, LivePlanRevision: 3,
		Runtime: RuntimeServices{Status: statusAvailable, Services: []RuntimeService{{
			ContainerID: "c0ffee0000ff", Name: "jd-e1-r3", LiveRelease: true, State: "running", Health: "healthy",
		}}},
		Domains: DomainSummary{Status: statusAvailable, SiteName: "just-dashboard-env-1.conf", Domains: []DomainRoute{{
			Hostname: "app.example.test", HTTPS: true, Route: "served", ServedBy: "just-dashboard-env-1.conf",
			Certificate: "valid", CertificateName: "app.example.test",
		}}},
		Storage: StorageSummary{Status: statusAvailable, Mounts: []StorageMount{{
			Source: "app-data", Target: "/data", Kind: "volume", Status: "present",
		}}},
		Backups: BackupSummary{Status: statusAvailable, Jobs: []BackupJob{{
			ResourceID: "4", Required: true, Status: "present", LastStatus: "success", Fresh: true,
		}}},
		Dependencies: DependencySummary{Status: statusAvailable, Items: []DependencyObservation{{
			Kind: "database", ResourceKind: "database_connection", ResourceID: "2", Available: true, Status: "primary",
		}}},
	}
}

// A complete, healthy observation must produce no finding at all. A diagnosis
// that always has something to say is not evidence, it is decoration.
func TestDiagnosisStaysSilentWhenEveryOwnerReportsHealthyEvidence(t *testing.T) {
	t.Parallel()
	diagnosis := Diagnose(healthyDiagnosisInput())
	if diagnosis.Status != "assessed" {
		t.Fatalf("status = %q, want assessed", diagnosis.Status)
	}
	if len(diagnosis.Findings) != 0 {
		t.Fatalf("healthy evidence produced findings %v", findingCodes(diagnosis))
	}
	if len(diagnosis.Silences) != 0 {
		t.Fatalf("healthy evidence produced silences %v", silenceSubjects(diagnosis))
	}
}

// Every unavailable owner is a silence, never a claim. An absent module must
// not be reported as a healthy result and must not be reported as a problem.
func TestUnavailableOwnersProduceSilencesAndNeverClaims(t *testing.T) {
	t.Parallel()
	input := healthyDiagnosisInput()
	input.Runtime = RuntimeServices{Status: statusUnavailable, Reason: "Docker runtime evidence is unavailable."}
	input.Domains = DomainSummary{Status: statusUnavailable, Reason: "Proxy site inventory could not be read."}
	input.Storage = StorageSummary{Status: statusUnavailable, Reason: "Storage inventory is unavailable."}
	input.Backups = BackupSummary{Status: statusUnavailable, Reason: "Backups inventory is unavailable."}
	input.Dependencies = DependencySummary{Status: statusUnavailable, Reason: "Dependency inventory is unavailable."}
	diagnosis := Diagnose(input)
	if len(diagnosis.Findings) != 0 {
		t.Fatalf("unavailable owners produced findings %v", findingCodes(diagnosis))
	}
	if diagnosis.Status != "partial" {
		t.Fatalf("status = %q, want partial", diagnosis.Status)
	}
	for _, subject := range []string{"runtime", "domains", "storage", "backups", "dependencies"} {
		if !contains(silenceSubjects(diagnosis), subject) {
			t.Fatalf("missing silence for %q in %v", subject, silenceSubjects(diagnosis))
		}
	}
	for _, silence := range diagnosis.Silences {
		if strings.TrimSpace(silence.Reason) == "" {
			t.Fatalf("silence %q carries no reason", silence.Subject)
		}
	}
}

// A certificate reader that is absent must silence HTTPS rather than claim the
// certificate is missing, while the route claim beside it still holds.
func TestMissingCertificateInventorySilencesHTTPSWithoutSuppressingRouteFindings(t *testing.T) {
	t.Parallel()
	input := healthyDiagnosisInput()
	input.Domains.Domains[0].Route = "missing"
	input.Domains.Domains[0].Certificate = statusUnavailable
	input.Domains.Domains[0].CertificateName = ""
	diagnosis := Diagnose(input)
	if !contains(findingCodes(diagnosis), "domain_unrouted") {
		t.Fatalf("expected the unrouted domain claim, got %v", findingCodes(diagnosis))
	}
	for _, code := range findingCodes(diagnosis) {
		if strings.HasPrefix(code, "certificate_") {
			t.Fatalf("certificate claim %q was made without a certificate reader", code)
		}
	}
	if !contains(silenceSubjects(diagnosis), "certificates") {
		t.Fatalf("expected a certificate silence, got %v", silenceSubjects(diagnosis))
	}
}

func TestDiagnosisClaimsAreExactlyPinnedPerObservation(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name   string
		mutate func(*DiagnosisInput)
		want   []string
	}{
		{"stopped live container", func(input *DiagnosisInput) {
			input.Runtime.Services[0].State = "exited"
		}, []string{"runtime_not_running"}},
		{"unhealthy live container", func(input *DiagnosisInput) {
			input.Runtime.Services[0].Health = "unhealthy"
		}, []string{"runtime_unhealthy"}},
		{"no container for the live release", func(input *DiagnosisInput) {
			input.Runtime.Services = []RuntimeService{}
		}, []string{"runtime_absent"}},
		{"another site owns the hostname", func(input *DiagnosisInput) {
			input.Domains.Domains[0].Route = "foreign"
			input.Domains.Domains[0].ServedBy = "legacy.conf"
		}, []string{"domain_foreign_route"}},
		{"two sites claim the hostname", func(input *DiagnosisInput) {
			input.Domains.Domains[0].Route = "conflict"
			input.Domains.Domains[0].ServedBy = "legacy.conf"
		}, []string{"domain_conflict"}},
		{"expired certificate", func(input *DiagnosisInput) {
			input.Domains.Domains[0].Certificate = "expired"
		}, []string{"certificate_expired"}},
		{"expiring certificate", func(input *DiagnosisInput) {
			input.Domains.Domains[0].Certificate = "expiring"
			input.Domains.Domains[0].CertificateDaysLeft = 9
		}, []string{"certificate_expiring"}},
		{"missing certificate for an HTTPS domain", func(input *DiagnosisInput) {
			input.Domains.Domains[0].Certificate = "missing"
		}, []string{"certificate_missing"}},
		{"plain HTTP domain wants no certificate", func(input *DiagnosisInput) {
			input.Domains.Domains[0].HTTPS = false
			input.Domains.Domains[0].Certificate = "not requested"
		}, nil},
		{"missing persistent storage", func(input *DiagnosisInput) {
			input.Storage.Mounts[0].Status = "missing"
		}, []string{"storage_missing"}},
		{"deleted backup job", func(input *DiagnosisInput) {
			input.Backups.Jobs[0].Status = "missing"
		}, []string{"backup_missing"}},
		{"stale required backup", func(input *DiagnosisInput) {
			input.Backups.Jobs[0].Fresh = false
		}, []string{"backup_stale"}},
		{"stale optional backup is not a claim", func(input *DiagnosisInput) {
			input.Backups.Jobs[0].Required, input.Backups.Jobs[0].Fresh = false, false
		}, nil},
		{"unavailable database dependency", func(input *DiagnosisInput) {
			input.Dependencies.Items[0].Available = false
			input.Dependencies.Items[0].Detail = "database connection was not found"
		}, []string{"dependency_unavailable"}},
		{"saved changes are not live", func(input *DiagnosisInput) {
			input.PendingChanges, input.DesiredRevision = true, 4
		}, []string{"plan_pending"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			input := healthyDiagnosisInput()
			testCase.mutate(&input)
			codes := findingCodes(Diagnose(input))
			if len(codes) != len(testCase.want) {
				t.Fatalf("findings = %v, want exactly %v", codes, testCase.want)
			}
			for index, wanted := range testCase.want {
				if codes[index] != wanted {
					t.Fatalf("findings = %v, want exactly %v", codes, testCase.want)
				}
			}
		})
	}
}

// A deployment that has never deployed must not be described as broken. It has
// no live release, so there is nothing yet to be wrong.
func TestDeploymentWithoutLiveReleaseClaimsNothingAboutRuntime(t *testing.T) {
	t.Parallel()
	diagnosis := Diagnose(DiagnosisInput{
		Runtime: RuntimeServices{Status: statusAvailable, Services: []RuntimeService{}},
	})
	if len(diagnosis.Findings) != 0 {
		t.Fatalf("undeployed project produced findings %v", findingCodes(diagnosis))
	}
	if !contains(silenceSubjects(diagnosis), "runtime") {
		t.Fatalf("expected a runtime silence, got %v", silenceSubjects(diagnosis))
	}
}

// A live release whose runtime record is gone cannot be judged by the presence
// or absence of containers: the identities to look for were never recorded.
func TestUnrecordedRuntimeSilencesInsteadOfClaimingAnAbsentContainer(t *testing.T) {
	t.Parallel()
	input := healthyDiagnosisInput()
	input.RuntimeRecorded = false
	input.Runtime.Services = []RuntimeService{}
	diagnosis := Diagnose(input)
	if contains(findingCodes(diagnosis), "runtime_absent") {
		t.Fatalf("claimed an absent container without a recorded runtime: %v", findingCodes(diagnosis))
	}
	if !contains(silenceSubjects(diagnosis), "runtime") {
		t.Fatalf("expected a runtime silence, got %v", silenceSubjects(diagnosis))
	}
}

func TestFindingsAreOrderedBySeverityAndCarryAnActionableOwner(t *testing.T) {
	t.Parallel()
	input := healthyDiagnosisInput()
	input.PendingChanges, input.DesiredRevision = true, 4
	input.Domains.Domains[0].Certificate = "expiring"
	input.Runtime.Services[0].State = "exited"
	diagnosis := Diagnose(input)
	if got := findingCodes(diagnosis); len(got) != 3 ||
		got[0] != "runtime_not_running" || got[1] != "certificate_expiring" || got[2] != "plan_pending" {
		t.Fatalf("findings = %v, want critical, warning, notice order", got)
	}
	for _, finding := range diagnosis.Findings {
		if finding.Owner == "" || finding.Action == "" || finding.Means == "" || finding.Measured == "" {
			t.Fatalf("finding %q is missing evidence or an action: %#v", finding.Code, finding)
		}
		if finding.DeepLink == "" && !finding.External && finding.Owner != "deploy" {
			t.Fatalf("finding %q offers neither a link nor an external-action statement", finding.Code)
		}
	}
}

// A first deploy that failed is the one thing a never-live project has to
// say, and a failed deploy after a live release is news until a later one
// replaces it. Both name the cause and link to the run.
func TestDiagnosisNamesTheLastFailedDeployment(t *testing.T) {
	t.Parallel()
	failed := &EngineRun{
		ID: 41, EnvironmentID: 1, State: RunFailed, TerminalCode: "build_lockfile_out_of_sync",
		TerminalReason: "The install step (`npm ci`) exited with code 1: package-lock.json is out of sync with package.json.",
	}
	never := Diagnose(DiagnosisInput{ProjectID: 9, LastRun: failed})
	if len(never.Findings) != 1 || never.Status != "partial" {
		t.Fatalf("never-live diagnosis = %+v", never)
	}
	finding := never.Findings[0]
	if finding.Code != "last_deploy_failed" || finding.Severity != DiagnosisCritical ||
		finding.Title != "The last deployment failed — Lockfile out of sync" ||
		finding.DeepLink != "/deploy/9/runs/41" || !strings.Contains(finding.Measured, "package-lock.json") {
		t.Fatalf("finding = %+v", finding)
	}

	live := healthyDiagnosisInput()
	live.ProjectID = 9
	live.LastRun = &EngineRun{ID: 42, State: RunRolledBack, TerminalCode: "runtime_env_missing", CandidateReleaseID: 8}
	diagnosis := Diagnose(live)
	if len(diagnosis.Findings) != 1 || diagnosis.Findings[0].Severity != DiagnosisWarning ||
		!strings.Contains(diagnosis.Findings[0].Measured, "runtime_env_missing") {
		t.Fatalf("live diagnosis = %+v", diagnosis.Findings)
	}

	for code, want := range map[string]string{
		"build_copy_source_missing":     "The last deployment failed — COPY source missing",
		"build_php_extension_missing":   "The last deployment failed — PHP extension missing",
		"build_bundle_platform_missing": "The last deployment failed — Gemfile.lock lacks Linux",
		"builder_missing":               "The last deployment failed — Docker Buildx missing",
		"internal_error":                "The last deployment failed",
	} {
		named := Diagnose(DiagnosisInput{ProjectID: 9, LastRun: &EngineRun{ID: 44, State: RunFailed, TerminalCode: code}})
		if len(named.Findings) != 1 || named.Findings[0].Title != want {
			t.Fatalf("%s titled %+v, want %q", code, named.Findings, want)
		}
	}

	replaced := healthyDiagnosisInput()
	replaced.LastRun = &EngineRun{ID: 43, State: RunSucceeded, ReleaseID: 7}
	if findings := Diagnose(replaced).Findings; len(findings) != 0 {
		t.Fatalf("a succeeded last run produced %+v", findings)
	}
}

func TestDiagnosisNamesWhyAutomaticDeploysStopped(t *testing.T) {
	t.Parallel()
	for reason, want := range map[string]string{
		"ref_not_found":             "main no longer exists",
		"source_auth_failed":        "credential can no longer read",
		"source_repository_missing": "repository was not found",
		"source_unreachable":        "remote is unreachable",
	} {
		input := healthyDiagnosisInput()
		input.ProjectID = 3
		input.GitWatch = &GitWatchStatus{Status: "unavailable", Reason: reason, Branch: "main"}
		findings := Diagnose(input).Findings
		if len(findings) != 1 || findings[0].Code != "auto_deploy_stopped" || !strings.Contains(findings[0].Title, want) {
			t.Fatalf("%s = %+v", reason, findings)
		}
		if reason == "source_auth_failed" && findings[0].DeepLink != "/deploy/credentials" {
			t.Fatalf("credential finding links to %q", findings[0].DeepLink)
		}
	}
	input := healthyDiagnosisInput()
	input.GitWatch = &GitWatchStatus{Status: "unavailable", Reason: "source_unavailable"}
	if findings := Diagnose(input).Findings; len(findings) != 0 {
		t.Fatalf("an unexplained miss produced %+v", findings)
	}
}
