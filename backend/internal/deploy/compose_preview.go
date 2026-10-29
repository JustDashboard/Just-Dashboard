package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"gopkg.in/yaml.v3"
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

// composePreviewSources reads the pull request's own Compose files, which
// are what the preview runs: production's analysis, which the preview's plan
// carries, says nothing about them, and Compose reads some files on this
// server itself — an env_file, a config's or secret's file — before any
// container exists. So the files are analysed as planning analyses a stack
// (a path outside the commit, an include or an extends refused there), a
// service that needs the server itself is refused, and every file Compose
// would read must be the commit's own, with no symbolic link on the way out
// of it.
func composePreviewSources(root string, files []string) error {
	refuse := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrPreviewIsolation, fmt.Sprintf(format, args...))
	}
	documents := make([]ComposeDocument, 0, len(files))
	for index, file := range files {
		path, inside := dockerx.CheckoutPath(root, filepath.Join(root, filepath.FromSlash(file)))
		if !inside {
			return refuse("the Compose file %s is not a file of the commit", file)
		}
		handle, err := os.Open(path)
		if err != nil {
			return refuse("the Compose file %s could not be read", file)
		}
		content, err := io.ReadAll(io.LimitReader(handle, 4<<20+1))
		handle.Close()
		if err != nil || len(content) > 4<<20 {
			return refuse("the Compose file %s could not be read", file)
		}
		documents = append(documents, ComposeDocument{Path: file, Content: string(content), Order: index})
	}
	analysis, err := analyzeComposeDocuments(documents)
	if err != nil {
		return refuse("the pull request's Compose files are refused: %s", strings.TrimPrefix(err.Error(), ErrInvalidCompose.Error()+": "))
	}
	if len(analysis.Unsupported) > 0 {
		return refuse("the pull request's Compose files ask for what a deployment does not run: %s", strings.Join(analysis.Unsupported, "; "))
	}
	if facts := composePreviewFacts(BuildPlanConfig{}, &analysis); len(facts.Refusals) > 0 {
		return refuse("%s, which no preview gets", strings.Join(facts.Refusals, "; "))
	}
	for _, document := range documents {
		for _, reference := range composeReadFiles(document.Content) {
			if strings.Contains(reference.path, "$") {
				return refuse("%s names a file by a variable, which cannot be checked before Compose reads it", document.Path)
			}
			// Compose resolves the path from the project directory or from
			// the file's own; both have to be the commit's.
			for _, base := range []string{root, filepath.Join(root, filepath.Dir(filepath.FromSlash(document.Path)))} {
				candidate := filepath.Join(base, filepath.FromSlash(reference.path))
				if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
					continue
				}
				if _, inside := dockerx.CheckoutPath(root, candidate); !inside {
					return refuse("%s reads %s, which is not a file of the commit", document.Path, reference.path)
				}
			}
		}
	}
	return nil
}

type composeReadFile struct{ path string }

// composeReadFiles are the files a Compose document has Compose read on the
// host: each service's env_file and label_file, and each config's and
// secret's file.
func composeReadFiles(content string) []composeReadFile {
	var root yaml.Node
	if yaml.Unmarshal([]byte(content), &root) != nil {
		return nil
	}
	mapping := documentMapping(&root)
	if mapping == nil {
		return nil
	}
	out := []composeReadFile{}
	add := func(node *yaml.Node) {
		if node == nil {
			return
		}
		items := []*yaml.Node{node}
		if node.Kind == yaml.SequenceNode {
			items = node.Content
		}
		for _, item := range items {
			if item.Kind == yaml.MappingNode {
				item = mappingValue(item, "path")
			}
			if item != nil && item.Kind == yaml.ScalarNode && item.Value != "" {
				out = append(out, composeReadFile{path: item.Value})
			}
		}
	}
	if services := mappingValue(mapping, "services"); services != nil && services.Kind == yaml.MappingNode {
		for index := 0; index+1 < len(services.Content); index += 2 {
			service := services.Content[index+1]
			add(mappingValue(service, "env_file"))
			add(mappingValue(service, "label_file"))
		}
	}
	for _, key := range []string{"configs", "secrets"} {
		entries := mappingValue(mapping, key)
		if entries == nil || entries.Kind != yaml.MappingNode {
			continue
		}
		for index := 0; index+1 < len(entries.Content); index += 2 {
			add(mappingValue(entries.Content[index+1], "file"))
		}
	}
	return out
}
