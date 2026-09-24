package deploy

import (
	"context"
	"strconv"
)

// Detection for a project that already exists.
//
// A plan is saved from what detection read then. When the source moves — a
// new branch, repository or subdirectory — or the code under it changes,
// what detection reads now can differ, and the plan keeps the old answer
// until someone looks. A proposal is that look: field by field, what the
// plan says, what detection now proposes, and whether detection changed its
// mind since the plan was saved. Nothing is applied here; the settings pages
// apply a field through the ordinary configuration save.

// DetectionProposal is fresh detection of a project's source, compared with
// its saved plan.
type DetectionProposal struct {
	// Revision is the desired revision the proposal was computed against,
	// the guard an Apply sends back with its save.
	Revision       int                `json:"revision"`
	SourceRevision string             `json:"sourceRevision,omitempty"`
	Candidate      *DetectedCandidate `json:"candidate,omitempty"`
	Changes        []DetectionChange  `json:"changes"`
	// Variables are the names the source reads that the plan does not set;
	// NewVariables are those detection had not read when the plan was saved.
	Variables    []DetectedVariable `json:"variables"`
	NewVariables []string           `json:"newVariables"`
	// Databases are the engines the source connects to whose variable the
	// plan does not set.
	Databases []DetectedDatabase `json:"databases"`
}

// DetectionChange is one plan field whose saved value differs from what
// detection proposes now. Changed says detection proposes something other
// than it did when the plan was saved: a field that differs only because it
// was edited on purpose has Changed false.
type DetectionChange struct {
	Field    string `json:"field"`
	Label    string `json:"label"`
	Saved    string `json:"saved"`
	Detected string `json:"detected"`
	Previous string `json:"previous,omitempty"`
	Changed  bool   `json:"changed"`
}

// detectionProposal compares the saved plan with fresh detection. stored is
// what detection proposed for the plan's root and method when the plan was
// saved, nil when nothing was stored.
func detectionProposal(
	stored, fresh *DetectedCandidate,
	build BuildPlanConfig,
	runtime RuntimePlanConfig,
	configured map[string]bool,
) DetectionProposal {
	proposal := DetectionProposal{
		Changes: []DetectionChange{}, Variables: []DetectedVariable{}, NewVariables: []string{},
		Databases: []DetectedDatabase{},
	}
	if fresh == nil {
		return proposal
	}
	copied := *fresh
	proposal.Candidate = &copied
	compare := func(field, label, saved, detected string, previous func(*DetectedCandidate) string) {
		if saved == detected {
			return
		}
		change := DetectionChange{Field: field, Label: label, Saved: saved, Detected: detected, Changed: true}
		if stored != nil {
			change.Previous = previous(stored)
			change.Changed = change.Previous != detected
		}
		proposal.Changes = append(proposal.Changes, change)
	}
	if fresh.BuildMethod == build.Method {
		if fresh.BuildMethod == BuildRecipe {
			// "Lockfile decides" already follows whatever the lockfile says.
			if build.PackageManager != "" && fresh.PackageManager != "" {
				compare("build.packageManager", "Package manager", build.PackageManager, fresh.PackageManager,
					func(c *DetectedCandidate) string { return c.PackageManager })
			}
			compare("build.buildCommand", "Build command", build.BuildCommand, fresh.BuildCommand,
				func(c *DetectedCandidate) string { return c.BuildCommand })
			compare("build.startCommand", "Start command", build.StartCommand, fresh.StartCommand,
				func(c *DetectedCandidate) string { return c.StartCommand })
			if build.GoPackage != "" && fresh.GoPackage != "" {
				compare("build.goPackage", "Go main package", build.GoPackage, fresh.GoPackage,
					func(c *DetectedCandidate) string { return c.GoPackage })
			}
		}
		if fresh.BuildMethod == BuildRecipe || fresh.BuildMethod == BuildStatic {
			compare("build.outputDirectory", "Output directory", build.OutputDirectory, fresh.OutputDirectory,
				func(c *DetectedCandidate) string { return c.OutputDirectory })
			compare("build.spaFallback", "Single-page fallback", strconv.FormatBool(build.SPAFallback),
				strconv.FormatBool(fresh.SPAFallback), func(c *DetectedCandidate) string { return strconv.FormatBool(c.SPAFallback) })
		}
	}
	if fresh.Port > 0 {
		compare("runtime.internalPort", "Port", strconv.Itoa(runtime.InternalPort), strconv.Itoa(fresh.Port),
			func(c *DetectedCandidate) string { return strconv.Itoa(c.Port) })
	}
	known := map[string]bool{}
	if stored != nil {
		for _, variable := range stored.Variables {
			known[variable.Name] = true
		}
	}
	for _, variable := range fresh.Variables {
		if configured[variable.Name] {
			continue
		}
		proposal.Variables = append(proposal.Variables, variable)
		if stored != nil && !known[variable.Name] {
			proposal.NewVariables = append(proposal.NewVariables, variable.Name)
		}
	}
	for _, database := range fresh.Databases {
		if !configured[database.Variable] {
			proposal.Databases = append(proposal.Databases, database)
		}
	}
	return proposal
}

// detectionCandidateFor is the candidate a fresh detection proposes for a
// plan: the one at the plan's root with its method, or else what detection
// selected — a proposal is still owed when the method no longer fits.
func detectionCandidateFor(detection *DetectionResult, build BuildPlanConfig) *DetectedCandidate {
	if detection == nil {
		return nil
	}
	if candidate := plannedDetectionCandidate(detection, build); candidate != nil {
		return candidate
	}
	return selectedDetectionCandidate(detection)
}

// Detect reads the environment's source again — the branch head, or a local
// checkout's recorded commit — and proposes what changed. It writes nothing.
func (c *DeploymentChecker) Detect(ctx context.Context, projectID, environmentID int64) (*DetectionProposal, error) {
	plan, revision, err := c.desiredPlan(ctx, projectID, environmentID)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return &DetectionProposal{Revision: revision, Changes: []DetectionChange{}, Variables: []DetectedVariable{},
			NewVariables: []string{}, Databases: []DetectedDatabase{}}, nil
	}
	if err := c.resolveRevision(ctx, plan, DeploymentCheckRequest{}); err != nil {
		return nil, err
	}
	var detection *DetectionResult
	identity := plan.Identity
	if sourceHasTree(plan.Source) {
		if err := c.sources.InspectRevision(ctx, plan.Source, plan.Identity, func(root string, inspected SourceIdentity) error {
			identity = inspected
			result, err := (Detector{}).DetectPath(ctx, root, inspected)
			if err != nil {
				return err
			}
			detection = &result
			return nil
		}); err != nil {
			return nil, err
		}
	} else {
		detection = &DetectionResult{Source: plan.Identity, Candidates: plan.Evidence.Candidates}
	}
	stored := plannedDetectionCandidate(&DetectionResult{Candidates: plan.Evidence.Candidates}, plan.Configuration.Build)
	proposal := detectionProposal(stored, detectionCandidateFor(detection, plan.Configuration.Build),
		plan.Configuration.Build, plan.Configuration.Runtime, configuredVariableNames(plan.Configuration, nil))
	proposal.Revision, proposal.SourceRevision = revision, identity.Revision
	return &proposal, nil
}
