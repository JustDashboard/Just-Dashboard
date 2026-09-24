package deploy

import (
	"context"
	"fmt"
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
	// Elsewhere says detection found nothing at the plan's root that builds
	// the plan's way. Candidate is then what it selected instead, shown for
	// information: its commands, output and port describe another directory
	// or builder, so none of them is offered for the plan.
	Elsewhere bool              `json:"elsewhere,omitempty"`
	Changes   []DetectionChange `json:"changes"`
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

// detectionProposal compares the saved plan with fresh detection: with the
// candidate detection finds at the plan's root building the plan's way, and
// no other. stored is what detection proposed for that root and method when
// the plan was saved, nil when nothing was stored.
func detectionProposal(
	stored *DetectedCandidate,
	detection *DetectionResult,
	build BuildPlanConfig,
	runtime RuntimePlanConfig,
	configured map[string]bool,
) DetectionProposal {
	proposal := DetectionProposal{
		Changes: []DetectionChange{}, Variables: []DetectedVariable{}, NewVariables: []string{},
		Databases: []DetectedDatabase{},
	}
	fresh := plannedDetectionCandidate(detection, build)
	if fresh == nil {
		selected := selectedDetectionCandidate(detection)
		if selected == nil {
			return proposal
		}
		copied := *selected
		proposal.Candidate, proposal.Elsewhere = &copied, true
		// The same directory's code reads the same variables whichever way
		// it is built; another directory's say nothing about this one.
		if sameBuildRoot(selected.Root, build.RootDirectory) {
			proposeDetectedNames(&proposal, stored, selected, configured)
		}
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
	// An empty command from detection means it could not tell — the start
	// command is asked for, a build step may be run from a script it does
	// not read — never that the plan's command should go.
	compareCommand := func(field, label, saved, detected string, previous func(*DetectedCandidate) string) {
		if detected != "" {
			compare(field, label, saved, detected, previous)
		}
	}
	if fresh.BuildMethod == BuildRecipe {
		// "Lockfile decides" already follows whatever the lockfile says.
		if build.PackageManager != "" && fresh.PackageManager != "" {
			compare("build.packageManager", "Package manager", build.PackageManager, fresh.PackageManager,
				func(c *DetectedCandidate) string { return c.PackageManager })
		}
		compareCommand("build.buildCommand", "Build command", build.BuildCommand, fresh.BuildCommand,
			func(c *DetectedCandidate) string { return c.BuildCommand })
		compareCommand("build.startCommand", "Start command", build.StartCommand, fresh.StartCommand,
			func(c *DetectedCandidate) string { return c.StartCommand })
		if build.GoPackage != "" && fresh.GoPackage != "" {
			compare("build.goPackage", "Go main package", build.GoPackage, fresh.GoPackage,
				func(c *DetectedCandidate) string { return c.GoPackage })
		}
	}
	if fresh.BuildMethod == BuildDockerfile && fresh.DockerfileTarget != "" {
		// A Dockerfile whose last stage is a development one names the stage
		// to build; no stage from detection is no opinion.
		compare("build.target", "Stage", build.Target, fresh.DockerfileTarget,
			func(c *DetectedCandidate) string { return c.DockerfileTarget })
	}
	if fresh.BuildMethod == BuildRecipe || fresh.BuildMethod == BuildStatic {
		compare("build.outputDirectory", "Output directory", build.OutputDirectory, fresh.OutputDirectory,
			func(c *DetectedCandidate) string { return c.OutputDirectory })
		compare("build.spaFallback", "Single-page fallback", strconv.FormatBool(build.SPAFallback),
			strconv.FormatBool(fresh.SPAFallback), func(c *DetectedCandidate) string { return strconv.FormatBool(c.SPAFallback) })
	}
	if fresh.Port > 0 {
		compare("runtime.internalPort", "Port", strconv.Itoa(runtime.InternalPort), strconv.Itoa(fresh.Port),
			func(c *DetectedCandidate) string { return strconv.Itoa(c.Port) })
	}
	proposeDetectedNames(&proposal, stored, fresh, configured)
	return proposal
}

// proposeDetectedNames lists the variables and databases a candidate's code
// reads that the plan does not set.
func proposeDetectedNames(proposal *DetectionProposal, stored, fresh *DetectedCandidate, configured map[string]bool) {
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
		if c.sources == nil {
			return nil, fmt.Errorf("%w: %w: source inspection is unavailable", ErrSourceUnavailable, ErrGitUnavailable)
		}
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
	proposal := detectionProposal(stored, detection, plan.Configuration.Build, plan.Configuration.Runtime,
		configuredVariableNames(plan.Configuration, nil))
	proposal.Revision, proposal.SourceRevision = revision, identity.Revision
	return &proposal, nil
}
