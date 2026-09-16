package blueprint

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
)

//go:embed builtin/*.json
var builtinFS embed.FS

var (
	ErrNotFound = errors.New("blueprint not found")

	catalogOnce sync.Once
	catalog     []*Blueprint
	catalogByID map[string]*Blueprint
	catalogErr  error
)

// Catalog returns every reviewed built-in, ordered by category then name. The
// files are parsed and validated once; a built-in that fails validation makes
// the whole catalogue fail, which is what turns the rules in validate.go into
// a build-time guarantee rather than a style guide.
func Catalog() ([]*Blueprint, error) {
	catalogOnce.Do(loadCatalog)
	if catalogErr != nil {
		return nil, catalogErr
	}
	return catalog, nil
}

func Get(id string) (*Blueprint, error) {
	catalogOnce.Do(loadCatalog)
	if catalogErr != nil {
		return nil, catalogErr
	}
	found, ok := catalogByID[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return found, nil
}

// GetVersion refuses a request for a version this dashboard does not ship.
// Silently serving a different version would break the promise that a plan
// digest identifies exactly one reviewed definition.
func GetVersion(id, version string) (*Blueprint, error) {
	found, err := Get(id)
	if err != nil {
		return nil, err
	}
	if version != "" && version != found.Version {
		return nil, fmt.Errorf("%w: %s version %s is not shipped with this dashboard", ErrNotFound, id, version)
	}
	return found, nil
}

func loadCatalog() {
	entries, err := fs.ReadDir(builtinFS, "builtin")
	if err != nil {
		catalogErr = err
		return
	}
	catalogByID = map[string]*Blueprint{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, readErr := builtinFS.ReadFile(path.Join("builtin", entry.Name()))
		if readErr != nil {
			catalogErr = readErr
			return
		}
		parsed, parseErr := Parse(raw)
		if parseErr != nil {
			catalogErr = fmt.Errorf("builtin/%s: %w", entry.Name(), parseErr)
			return
		}
		expected := parsed.ID + ".json"
		if entry.Name() != expected {
			catalogErr = fmt.Errorf("builtin/%s declares id %q; the file must be named %s", entry.Name(), parsed.ID, expected)
			return
		}
		if _, duplicate := catalogByID[parsed.ID]; duplicate {
			catalogErr = fmt.Errorf("blueprint %q is shipped twice", parsed.ID)
			return
		}
		catalogByID[parsed.ID] = parsed
		catalog = append(catalog, parsed)
	}
	if len(catalog) == 0 {
		catalogErr = errors.New("no built-in blueprints are embedded")
		return
	}
	sort.Slice(catalog, func(i, j int) bool {
		if catalog[i].Category != catalog[j].Category {
			return catalog[i].Category < catalog[j].Category
		}
		return catalog[i].Name < catalog[j].Name
	})
}

// DeploymentSupport says whether this dashboard can deploy a blueprint end to
// end, and when it cannot, exactly why. A blueprint is deployed as an
// immutable image release: its inputs become variables, its secrets are
// generated at commit, its volumes become managed storage and its checks gate
// activation. What that release path cannot yet do is write configuration
// files or downloaded artifacts into a volume before the container starts, or
// run the game-server integration; those blueprints stay in the catalogue as
// previews and say so.
func DeploymentSupport(blueprint *Blueprint) (bool, string) {
	if blueprint == nil {
		return false, "Blueprint is unavailable."
	}
	if blueprint.Profile == ProfileGame {
		return false, "Game servers deploy through the reviewed game-server integration, which is not available in this release."
	}
	if len(blueprint.Files) > 0 {
		return false, "This blueprint installs configuration files before start, which runtime materialization does not support yet."
	}
	for _, operation := range blueprint.Operations.Startup {
		if operation.Kind == OperationFetchArtifact {
			return false, "This blueprint downloads an install-time artifact, which runtime materialization does not support yet."
		}
	}
	for _, port := range blueprint.Ports {
		if strings.ToLower(port.Protocol) == "udp" {
			return false, "UDP ports are not supported by the deployment runtime yet."
		}
	}
	return true, ""
}

// Summary is the listing shape. It carries enough to choose a blueprint and
// nothing that would let a client render one without asking the server.

type Summary struct {
	DeploymentSupported bool     `json:"deploymentSupported"`
	UnavailableReason   string   `json:"unavailableReason"`
	ID                  string   `json:"id"`
	Version             string   `json:"version"`
	Name                string   `json:"name"`
	Category            Category `json:"category"`
	Profile             Profile  `json:"profile"`
	Description         string   `json:"description"`
	IconID              string   `json:"iconId"`
	DocsURL             string   `json:"docsUrl"`
	License             string   `json:"license"`
	Maintainer          string   `json:"maintainer"`
	ReviewedAt          string   `json:"reviewedAt"`
	Image               string   `json:"image"`
	MemoryMB            int      `json:"memoryMb"`
	RequiresAcceptance  bool     `json:"requiresAcceptance,omitempty"`
	Privileged          bool     `json:"privileged,omitempty"`
}

func Summarize(blueprint *Blueprint) Summary {
	supported, reason := DeploymentSupport(blueprint)
	summary := Summary{DeploymentSupported: supported, UnavailableReason: reason,
		ID: blueprint.ID, Version: blueprint.Version, Name: blueprint.Name,
		Category: blueprint.Category, Profile: blueprint.Profile, Description: blueprint.Description,
		IconID: blueprint.IconID, DocsURL: blueprint.DocsURL, License: blueprint.Provenance.License,
		Maintainer: blueprint.Provenance.Maintainer, ReviewedAt: blueprint.Provenance.ReviewedAt,
		Image: blueprint.Image.Reference, MemoryMB: blueprint.Resources.MemoryMB,
		Privileged: blueprint.Security.Privileged || blueprint.Security.HostNetwork ||
			len(blueprint.Security.Capabilities) > 0 || len(blueprint.Security.Devices) > 0,
	}
	for _, input := range blueprint.Inputs {
		if input.Kind == InputAccept {
			summary.RequiresAcceptance = true
		}
	}
	return summary
}

func Summaries() ([]Summary, error) {
	entries, err := Catalog()
	if err != nil {
		return nil, err
	}
	summaries := make([]Summary, 0, len(entries))
	for _, entry := range entries {
		summaries = append(summaries, Summarize(entry))
	}
	return summaries, nil
}
