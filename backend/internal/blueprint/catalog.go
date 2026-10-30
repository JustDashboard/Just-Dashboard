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
	everything  []*Blueprint
	catalogByID map[string]*Blueprint
	catalogErr  error
)

// Catalog returns every offered built-in, ordered by category then name. The
// files are parsed and validated once; a built-in that fails validation makes
// the whole catalogue fail, which is what turns the rules in validate.go into
// a build-time guarantee rather than a style guide.
//
// Retired definitions are not offered and so are not here. They are still
// shipped, still parsed and still held to every rule — see All.
func Catalog() ([]*Blueprint, error) {
	catalogOnce.Do(loadCatalog)
	if catalogErr != nil {
		return nil, catalogErr
	}
	return catalog, nil
}

// All returns every shipped built-in, retired ones included. The rules in
// validate.go apply to a retired definition exactly as they do to an offered
// one: it is still the thing a running deployment re-resolves on redeploy.
func All() ([]*Blueprint, error) {
	catalogOnce.Do(loadCatalog)
	if catalogErr != nil {
		return nil, catalogErr
	}
	return everything, nil
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
		if IsUserID(parsed.ID) {
			catalogErr = fmt.Errorf("builtin/%s: %q is the namespace of templates added on a dashboard, which a built-in cannot use", entry.Name(), UserPrefix)
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
		everything = append(everything, parsed)
		if parsed.Retired == "" {
			catalog = append(catalog, parsed)
		}
	}
	if len(catalog) == 0 {
		catalogErr = errors.New("no built-in blueprints are embedded")
		return
	}
	byCategoryThenName := func(entries []*Blueprint) func(i, j int) bool {
		return func(i, j int) bool {
			if entries[i].Category != entries[j].Category {
				return entries[i].Category < entries[j].Category
			}
			return entries[i].Name < entries[j].Name
		}
	}
	sort.Slice(catalog, byCategoryThenName(catalog))
	sort.Slice(everything, byCategoryThenName(everything))
}

// DeploymentSupport says whether this dashboard can deploy a blueprint end to
// end, and when it cannot, exactly why. A blueprint is deployed as an
// immutable image release: its inputs become variables, its secrets are
// generated at commit, its volumes become managed storage, its install files
// and pinned artifacts are written into those volumes by a helper before the
// container starts, its direct ports are published on the plan's bind address
// whichever protocol they speak, and its checks gate activation. A game server
// is that release with the console, players and settings screens on top.
//
// What remains refused is the one thing the release path cannot honestly do:
// place a download whose bytes nobody pinned. An artifact found through a
// version source is resolved at deploy time, and the helper has nothing to
// verify it against.
func DeploymentSupport(blueprint *Blueprint) (bool, string) {
	if blueprint == nil {
		return false, "Blueprint is unavailable."
	}
	for _, operation := range blueprint.Operations.Startup {
		if operation.Kind == OperationFetchArtifact && operation.Checksum == "" {
			return false, "This blueprint downloads an artifact found through a version source, which the deployment helper cannot verify against a pinned checksum."
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
	Access              Access   `json:"access"`
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
	// SourceKind is the deployment source this blueprint is deployed as:
	// "blueprint" for one reviewed image, "compose" for a multi-service
	// template that runs through the Compose release path. Image and
	// MemoryMB are then the public service's image and the whole stack's
	// footprint, and Services lists what the stack runs.
	SourceKind string           `json:"sourceKind"`
	Services   []ServiceSummary `json:"services,omitempty"`
	// Origin is "builtin" for a definition shipped with the dashboard and
	// "user" for one added on it (user.go), which is read from the id.
	Origin string `json:"origin"`
}

// ServiceSummary is one service of a Compose template as the picker lists it.
type ServiceSummary struct {
	Name     string      `json:"name"`
	Role     ServiceRole `json:"role"`
	Purpose  string      `json:"purpose"`
	Image    string      `json:"image"`
	MemoryMB int         `json:"memoryMb"`
	Public   bool        `json:"public,omitempty"`
}

func Summarize(blueprint *Blueprint) Summary {
	supported, reason := DeploymentSupport(blueprint)
	summary := Summary{DeploymentSupported: supported, UnavailableReason: reason,
		ID: blueprint.ID, Version: blueprint.Version, Access: blueprint.Access, Name: blueprint.Name,
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
	summary.SourceKind = "blueprint"
	summary.Origin = OriginBuiltIn
	if IsUserID(blueprint.ID) {
		summary.Origin = OriginUser
	}
	if blueprint.IsCompose() {
		summary.SourceKind = "compose"
		summary.MemoryMB = 0
		public := blueprint.PublicService()
		for _, service := range blueprint.Services {
			isPublic := public != nil && service.Name == public.Name
			if isPublic {
				summary.Image = service.Image.Reference
			}
			summary.MemoryMB += service.Resources.MemoryMB
			summary.Services = append(summary.Services, ServiceSummary{
				Name: service.Name, Role: service.Role, Purpose: service.Purpose,
				Image: service.Image.Reference, MemoryMB: service.Resources.MemoryMB, Public: isPublic,
			})
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
