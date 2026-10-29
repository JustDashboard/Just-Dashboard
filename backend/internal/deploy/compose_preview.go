package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ComposePreviewFacts is what a pull request's preview of a Compose release
// gets, read from the analysis saved with production's plan, so the dialog
// can say it before anything is built. The start reads the stack again as
// Compose resolves it and is the authority (dockerx.ComposePreviewOverride);
// this is the same rule applied to the files as planning saw them.
type ComposePreviewFacts struct {
	// Public is the service the preview's route and tailnet address reach.
	Public   string                  `json:"public"`
	Services []ComposePreviewService `json:"services"`
	// Refusals are what no preview may have. Any one refuses the preview.
	Refusals []string `json:"refusals"`
}

type ComposePreviewService struct {
	Name   string `json:"name"`
	Public bool   `json:"public"`
	// Ports production publishes on the host. The preview publishes none of
	// them; the public service answers on loopback for its route alone.
	Ports []string `json:"ports"`
	// Volumes are mount targets that start empty on storage of the preview's
	// own, never production's.
	Volumes []string `json:"volumes"`
	// Checkout are read-only files of the commit itself the preview mounts.
	Checkout []string `json:"checkout"`
	// LeftOut are read-only paths of this server the preview does not get.
	LeftOut []string `json:"leftOut"`
}

// composePreviewHostAccess are the analysis's findings no preview runs with.
// A pid, ipc or security option is not here: those are refused at start
// only when they reach the server or another container, which the files
// alone cannot say before interpolation.
var composePreviewHostAccess = map[string]string{
	"docker_socket": "mounts the Docker socket",
	"privileged":    "runs privileged",
	"devices":       "uses devices of this server",
	"capabilities":  "adds Linux capabilities",
	"host_network":  "uses the host's network",
}

func composePreviewFacts(build BuildPlanConfig, analysis *ComposeAnalysis) ComposePreviewFacts {
	facts := ComposePreviewFacts{Services: []ComposePreviewService{}, Refusals: []string{}}
	if analysis == nil || len(analysis.Services) == 0 {
		facts.Refusals = append(facts.Refusals, "production's plan has no Compose analysis to isolate; save its build settings again")
		return facts
	}
	public, err := chosenComposePrimaryService(build, *analysis)
	if err != nil || public == "" {
		public = composePrimaryService(analysis.Services)
	}
	facts.Public = public
	for _, service := range analysis.Services {
		item := ComposePreviewService{
			Name: service.Name, Public: service.Name == public,
			Ports: append([]string{}, service.Ports...), Volumes: []string{}, Checkout: []string{}, LeftOut: []string{},
		}
		for _, mount := range service.Mounts {
			parts := strings.Split(mount, ":")
			if len(parts) == 1 {
				item.Volumes = append(item.Volumes, parts[0])
				continue
			}
			source, target := parts[0], parts[1]
			readOnly := false
			if len(parts) > 2 {
				for _, option := range strings.Split(parts[2], ",") {
					readOnly = readOnly || option == "ro" || option == "readonly"
				}
			}
			switch {
			case !readOnly:
				item.Volumes = append(item.Volumes, target)
			case strings.HasPrefix(source, "/"):
				item.LeftOut = append(item.LeftOut, target)
			case strings.HasPrefix(source, ".") || strings.Contains(source, "/"):
				item.Checkout = append(item.Checkout, target)
			default:
				item.Volumes = append(item.Volumes, target)
			}
		}
		advanced := append([]string(nil), service.Advanced...)
		sort.Strings(advanced)
		seen := map[string]bool{}
		for _, finding := range advanced {
			if reason, refused := composePreviewHostAccess[finding]; refused && !seen[reason] {
				seen[reason] = true
				facts.Refusals = append(facts.Refusals, "service "+service.Name+" "+reason)
			}
		}
		facts.Services = append(facts.Services, item)
	}
	sort.SliceStable(facts.Services, func(i, j int) bool {
		if facts.Services[i].Public != facts.Services[j].Public {
			return facts.Services[i].Public
		}
		return facts.Services[i].Name < facts.Services[j].Name
	})
	return facts
}

// ComposePreviewFacts reads what a preview of this environment's desired
// Compose plan would get. It answers nil for any other build.
func (s *PlanningStore) ComposePreviewFacts(ctx context.Context, environmentID int64) (*ComposePreviewFacts, error) {
	var method, buildRaw, evidenceRaw string
	if err := s.db.QueryRowContext(ctx, `SELECT b.method,b.config_json,b.evidence_json FROM deploy_environments e JOIN deploy_build_plans b ON b.environment_id=e.id AND b.revision=e.desired_revision WHERE e.id=?`, environmentID).Scan(&method, &buildRaw, &evidenceRaw); err != nil {
		return nil, err
	}
	if BuildMethod(method) != BuildCompose {
		return nil, nil
	}
	var build BuildPlanConfig
	var evidence StoredBuildEvidence
	if json.Unmarshal([]byte(buildRaw), &build) != nil {
		return nil, ErrInvalidPlan
	}
	_ = json.Unmarshal([]byte(evidenceRaw), &evidence)
	facts := composePreviewFacts(build, evidence.Compose)
	return &facts, nil
}

// composePreviewRefusal refuses, while the preview's plans are written, a
// Compose stack that asks for what no preview gets.
func composePreviewRefusal(build BuildPlanConfig, evidenceRaw string) error {
	var evidence StoredBuildEvidence
	_ = json.Unmarshal([]byte(evidenceRaw), &evidence)
	facts := composePreviewFacts(build, evidence.Compose)
	if len(facts.Refusals) > 0 {
		return fmt.Errorf("%w: a preview of this Compose stack would need what no preview gets: %s", ErrPreviewIsolation, strings.Join(facts.Refusals, "; "))
	}
	return nil
}
