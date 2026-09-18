package deploy

import (
	"strings"
	"testing"
)

func limitPlan(mutate func(*RuntimePlanConfig)) PlanConfiguration {
	configuration := PlanConfiguration{
		Build: BuildPlanConfig{Method: BuildImage},
		Runtime: RuntimePlanConfig{
			Strategy: StrategyStopFirst, BindAddress: "127.0.0.1", InternalPort: 8080,
			Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
		},
		Variables: []PlannedVariable{}, Checks: []PlannedCheck{}, Domains: []PlannedDomain{}, Dependencies: []PlannedDependency{},
	}
	mutate(&configuration.Runtime)
	return configuration
}

func TestRuntimeResourceLimitValidation(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*RuntimePlanConfig)
		wantErr string
	}{
		{"unlimited by default", func(*RuntimePlanConfig) {}, ""},
		{"memory ok", func(r *RuntimePlanConfig) { r.MemoryMB = 512 }, ""},
		{"memory too small", func(r *RuntimePlanConfig) { r.MemoryMB = 8 }, "memory limit"},
		{"memory absurd", func(r *RuntimePlanConfig) { r.MemoryMB = MaxRuntimeMemoryMB + 1 }, "memory limit"},
		{"cpus fraction ok", func(r *RuntimePlanConfig) { r.CPUs = 0.5 }, ""},
		{"cpus negative", func(r *RuntimePlanConfig) { r.CPUs = -1 }, "CPU limit"},
		{"cpus too many", func(r *RuntimePlanConfig) { r.CPUs = MaxRuntimeCPUs + 1 }, "CPU limit"},
		{"pids ok", func(r *RuntimePlanConfig) { r.PidsLimit = 256 }, ""},
		{"pids too small", func(r *RuntimePlanConfig) { r.PidsLimit = 4 }, "PID limit"},
		{"restart policy ok", func(r *RuntimePlanConfig) { r.RestartPolicy = "on-failure" }, ""},
		{"restart policy unknown", func(r *RuntimePlanConfig) { r.RestartPolicy = "sometimes" }, "restart policy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := limitPlan(tc.mutate).Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestEffectiveRestartPolicyDefaultsToUnlessStopped(t *testing.T) {
	if got := (RuntimePlanConfig{}).EffectiveRestartPolicy(); got != "unless-stopped" {
		t.Fatalf("default = %q", got)
	}
	if got := (RuntimePlanConfig{RestartPolicy: "always"}).EffectiveRestartPolicy(); got != "always" {
		t.Fatalf("explicit = %q", got)
	}
}

func TestPreflightWarnsWhenLimitsExceedHostCapacity(t *testing.T) {
	candidate := newDetectedCandidate("", BuildImage, DetectedCandidate{
		Name: "limits", Profile: ProfileImage, Confidence: ConfidenceHigh,
		Evidence: []DetectionEvidence{}, NeedsDecision: []string{},
	})
	draft := &Draft{Data: DraftData{
		Intent: &DraftIntentConfig{Name: "limits", Profile: ProfileImage},
		Source: &DraftSourceConfig{Kind: SourceImage, Mode: SourceModeImageReference, Image: "nginx:1.27"},
		Detection: &DetectionResult{
			Source:     SourceIdentity{Kind: SourceImage, Repository: "nginx"},
			Candidates: []DetectedCandidate{candidate}, SelectedID: candidate.ID,
		},
	}}
	configuration := limitPlan(func(r *RuntimePlanConfig) { r.MemoryMB = 4096; r.CPUs = 8 })
	observation := HostObservation{
		Facilities:      map[string]FacilityObservation{"docker": {Available: true}},
		AvailableMemory: 2 << 30, CPUCount: 4,
	}
	findings := preflightFindings(draft, configuration, observation, false)
	codes := map[string]PreflightSeverity{}
	for _, finding := range findings {
		codes[finding.Code] = finding.Severity
	}
	if codes["runtime_memory_limit_exceeds_host"] != PreflightWarning || codes["runtime_cpu_limit_exceeds_host"] != PreflightWarning {
		t.Fatalf("findings = %+v", codes)
	}
	within := limitPlan(func(r *RuntimePlanConfig) { r.MemoryMB = 512; r.CPUs = 2 })
	for _, finding := range preflightFindings(draft, within, observation, false) {
		if strings.HasPrefix(finding.Code, "runtime_") && strings.HasSuffix(finding.Code, "_exceeds_host") {
			t.Fatalf("limits within capacity produced %s", finding.Code)
		}
	}
}

func TestComparisonLabelsResourceLimits(t *testing.T) {
	if memoryLimitLabel(0) != "unlimited" || memoryLimitLabel(512) != "512 MiB" {
		t.Fatal("memory label")
	}
	if cpuLimitLabel(0) != "unlimited" || cpuLimitLabel(1.5) != "1.5 CPU" {
		t.Fatal("cpu label")
	}
	if countLimitLabel(0) != "unlimited" || countLimitLabel(256) != "256" {
		t.Fatal("count label")
	}
}
