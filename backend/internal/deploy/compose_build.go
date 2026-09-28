package deploy

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// ComposeBuildArg is one `build.args` entry: its name and the Compose
// file's own value expression (`${API_URL}`, `20`), or none when the value
// is to come from the environment. Values are resolved against the scoped
// build variables at build time and reach buildx only through its process
// environment.
type ComposeBuildArg struct {
	Name            string `json:"name"`
	Value           string `json:"value,omitempty"`
	FromEnvironment bool   `json:"fromEnvironment,omitempty"`
}

// ComposeEnvFile is one `env_file` path, relative to the Compose project.
type ComposeEnvFile struct {
	Path     string `json:"path"`
	Required bool   `json:"required"`
	// Missing is set when the checkout was analysed and lacks the file.
	Missing bool `json:"missing,omitempty"`
}

// ComposeOptionalVariable is a variable the file interpolates with a
// default, which it runs with when no value is set.
type ComposeOptionalVariable struct {
	Name    string `json:"name"`
	Default string `json:"default,omitempty"`
}

func composeBuildArgs(node *yaml.Node) ([]ComposeBuildArg, error) {
	args := []ComposeBuildArg{}
	add := func(name, value string, fromEnvironment bool) error {
		name = strings.TrimSpace(name)
		if !shellAssignmentRE.MatchString(name) || len(name) > 128 || len(value) > 4096 {
			return fmt.Errorf("build argument %q is malformed", name)
		}
		if !fromEnvironment && secretShapedKey(name) && !isVariableExpression(value) && value != "" {
			return fmt.Errorf("build argument %s must reference a scoped variable", name)
		}
		args = append(args, ComposeBuildArg{Name: name, Value: value, FromEnvironment: fromEnvironment})
		return nil
	}
	if node == nil {
		return nil, nil
	}
	switch node.Kind {
	case yaml.MappingNode:
		for index := 0; index+1 < len(node.Content); index += 2 {
			value := node.Content[index+1]
			fromEnvironment := value.Kind == yaml.ScalarNode && value.Tag == "!!null"
			if err := add(node.Content[index].Value, value.Value, fromEnvironment); err != nil {
				return nil, err
			}
		}
	case yaml.SequenceNode:
		for _, item := range node.Content {
			name, value, found := strings.Cut(item.Value, "=")
			if err := add(name, value, !found); err != nil {
				return nil, err
			}
		}
	}
	if len(args) > 64 {
		return nil, fmt.Errorf("more than 64 build arguments")
	}
	sort.Slice(args, func(i, j int) bool { return args[i].Name < args[j].Name })
	return args, nil
}

func composeEnvFiles(node *yaml.Node) []ComposeEnvFile {
	files := []ComposeEnvFile{}
	if node == nil {
		return nil
	}
	items := []*yaml.Node{node}
	if node.Kind == yaml.SequenceNode {
		items = node.Content
	}
	for _, item := range items {
		file := ComposeEnvFile{Required: true}
		switch item.Kind {
		case yaml.ScalarNode:
			file.Path = item.Value
		case yaml.MappingNode:
			file.Path = scalarMappingValue(item, "path")
			file.Required = !strings.EqualFold(scalarMappingValue(item, "required"), "false")
		}
		if file.Path != "" && len(files) < 16 {
			files = append(files, file)
		}
	}
	return files
}

var composeDefaultedRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:?[-+?])([^}]*)\}`)

type composeVariableUse struct {
	required   bool
	defaulted  bool
	defaultsTo string
}

// collectComposeDefaults records how each interpolated name is used:
// `${X:-d}` and `${X-d}` run with d, `${X:+a}` runs without X, while `${X}`,
// `$X` and `${X:?message}` need a value.
func collectComposeDefaults(node *yaml.Node, uses map[string]composeVariableUse) {
	if node == nil {
		return
	}
	if node.Kind == yaml.ScalarNode {
		value := strings.ReplaceAll(node.Value, "$$", "")
		for _, match := range composeInterpolationRE.FindAllStringSubmatch(value, -1) {
			name := match[1]
			if name == "" {
				name = match[2]
			}
			use := uses[name]
			defaulted := composeDefaultedRE.FindStringSubmatch(match[0])
			switch {
			case defaulted == nil || strings.HasSuffix(defaulted[2], "?"):
				use.required = true
			default:
				if !use.defaulted && strings.HasSuffix(defaulted[2], "-") {
					use.defaultsTo = defaulted[3]
				}
				use.defaulted = true
			}
			uses[name] = use
		}
	}
	for _, child := range node.Content {
		collectComposeDefaults(child, uses)
	}
}

func splitComposeVariables(names []string, uses map[string]composeVariableUse) ([]string, []ComposeOptionalVariable) {
	required := []string{}
	optional := []ComposeOptionalVariable{}
	for _, name := range names {
		use, ok := uses[name]
		if !ok || use.required || !use.defaulted {
			required = append(required, name)
			continue
		}
		example := ""
		if !secretShapedKey(name) {
			example = envExampleValue(use.defaultsTo)
		}
		optional = append(optional, ComposeOptionalVariable{Name: name, Default: example})
	}
	return required, optional
}

// composePrimaryService picks the service a Compose release is verified and
// identified by. The alphabetically first one was a database as often as
// not ("db" sorts before "web"), which made readiness probe Postgres.
func composePrimaryService(services []ComposeServicePlan) string {
	if len(services) == 0 {
		return ""
	}
	candidates := []ComposeServicePlan{}
	for _, service := range services {
		_, backing := composeImageFamily(service.Image)
		if service.BuildContext != "" || (len(service.Ports) > 0 && !backing) {
			candidates = append(candidates, service)
		}
	}
	for _, preferred := range []string{"web", "app", "frontend", "server", "api", "site", "www"} {
		for _, service := range candidates {
			if strings.EqualFold(service.Name, preferred) {
				return service.Name
			}
		}
	}
	if len(candidates) > 0 {
		return candidates[0].Name
	}
	for _, service := range services {
		if _, backing := composeImageFamily(service.Image); !backing {
			return service.Name
		}
	}
	return services[0].Name
}

// chosenComposePrimaryService is the service a release follows: the one the
// operator chose, when the stack still has it, else the analysis's.
func chosenComposePrimaryService(config BuildPlanConfig, analysis ComposeAnalysis) (string, error) {
	if config.PrimaryService == "" {
		return analysis.PrimaryService, nil
	}
	for _, service := range analysis.Services {
		if service.Name == config.PrimaryService {
			return service.Name, nil
		}
	}
	return "", fmt.Errorf("%w: primary service %s is not a service of the Compose stack", ErrUnsupportedBuilder, config.PrimaryService)
}

func validComposeBuildEvidence(analysis ComposeAnalysis) bool {
	if len(analysis.OptionalVariables) > 256 {
		return false
	}
	if analysis.PrimaryService != "" && !validComposeServiceName(analysis.PrimaryService) {
		return false
	}
	for _, variable := range analysis.OptionalVariables {
		if ValidateEnvKey(variable.Name) != nil || len(variable.Default) > 256 ||
			strings.ContainsAny(variable.Default, "\x00\r\n") || rejectPlanSecretLiteral("Compose default", variable.Default) != nil {
			return false
		}
	}
	for _, service := range analysis.Services {
		if len(service.BuildTarget) > 128 || (service.BuildTarget != "" && !strings.Contains(service.BuildTarget, "$") && !dockerfileStageNameRE.MatchString(service.BuildTarget)) ||
			len(service.BuildArgs) > 64 || len(service.EnvFiles) > 16 || len(service.ImagePlatforms) > 64 ||
			len(service.DockerfileIssues) > 32 || len(service.Platform) > 64 ||
			(service.Platform != "" && !strings.Contains(service.Platform, "$") && !validPlatform(service.Platform)) {
			return false
		}
		for _, arg := range service.BuildArgs {
			if !shellAssignmentRE.MatchString(arg.Name) || len(arg.Value) > 4096 || strings.ContainsAny(arg.Value, "\x00") {
				return false
			}
		}
		for _, file := range service.EnvFiles {
			if file.Path == "" || len(file.Path) > 4096 || strings.ContainsAny(file.Path, "\x00\r\n") {
				return false
			}
		}
		for _, platform := range service.ImagePlatforms {
			if !validPlatform(platform) {
				return false
			}
		}
		if validateCandidateImageFacts(DetectedCandidate{ImageBuildIssues: service.DockerfileIssues}) != nil {
			return false
		}
	}
	return true
}

// interpolateComposeValue resolves a build argument's expression against the
// scoped variables, with Compose's own operators; `${X:?message}` without a
// value is the file refusing to build.
func interpolateComposeValue(expression string, values map[string]string) (string, error) {
	var failure error
	escaped := strings.ReplaceAll(expression, "$$", "\x00")
	resolved := composeInterpolationRE.ReplaceAllStringFunc(escaped, func(match string) string {
		groups := composeDefaultedRE.FindStringSubmatch(match)
		if groups == nil {
			name := strings.Trim(strings.TrimPrefix(match, "$"), "{}")
			return values[name]
		}
		value, set := values[groups[1]]
		switch groups[2] {
		case ":-":
			if value == "" {
				return groups[3]
			}
		case "-":
			if !set {
				return groups[3]
			}
		case ":+":
			if value != "" {
				return groups[3]
			}
			return ""
		case "+":
			if set {
				return groups[3]
			}
			return ""
		case ":?", "?":
			if (groups[2] == ":?" && value == "") || !set {
				failure = fmt.Errorf("build argument needs %s: %s", groups[1], groups[3])
			}
		}
		return value
	})
	return strings.ReplaceAll(resolved, "\x00", "$"), failure
}

// analyzeComposeTree checks a Compose source's services against the checkout
// it was read from: a build context that does not exist (Laravel Sail's
// vendor/ runtime), an env_file the repository ignores, and the service
// Dockerfile's own build problems — each a certain `docker compose` failure.
func analyzeComposeTree(root string, analysis *ComposeAnalysis) {
	tree := openDetectionTree(root)
	defer tree.close()
	for index := range analysis.Services {
		service := &analysis.Services[index]
		for fileIndex := range service.EnvFiles {
			file := &service.EnvFiles[fileIndex]
			if !strings.Contains(file.Path, "$") && !tree.exists(path.Clean(file.Path)) {
				file.Missing = true
			}
		}
		if service.BuildContext == "" || strings.Contains(service.BuildContext, "$") || strings.Contains(service.BuildDockerfile, "$") {
			continue
		}
		context := strings.TrimPrefix(path.Clean("/"+service.BuildContext), "/")
		if info, ok := tree.lstat(context); !ok || !info.IsDir() {
			service.BuildContextMissing = true
			continue
		}
		dockerfile := joinRoot(context, service.BuildDockerfile)
		content, ok := tree.read(dockerfile, 2<<20)
		if !ok {
			continue
		}
		provided := map[string]bool{}
		for _, arg := range service.BuildArgs {
			provided[arg.Name] = true
		}
		model := modelDockerfile(content)
		target := service.BuildTarget
		if strings.Contains(target, "$") {
			target = ""
		}
		for _, issue := range dockerfileIssues(tree, model, content, dockerfile, context, target) {
			if issue.Code == "dockerfile_arg_required" && provided[issue.Subject] {
				continue
			}
			if len(service.DockerfileIssues) < 32 {
				service.DockerfileIssues = append(service.DockerfileIssues, issue)
			}
		}
	}
}

// resolveComposeImagePlatforms reads, from each image's registry manifest,
// the platforms it is published for, so an amd64-only image on an arm64
// host is a preflight finding rather than a failed pull. An image the
// registry does not answer for says nothing. The lookups run a few at a
// time under one deadline, so a slow registry costs the analysis seconds,
// not a registry round trip per service.
func resolveComposeImagePlatforms(ctx context.Context, docker PlanningDocker, analysis *ComposeAnalysis) {
	if docker == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, composeImageLookupBudget)
	defer cancel()
	pending := make(chan *ComposeServicePlan)
	var workers sync.WaitGroup
	for range composeImageLookupWorkers {
		workers.Go(func() {
			for service := range pending {
				lookup, cancelLookup := context.WithTimeout(ctx, 5*time.Second)
				resolved, err := docker.ResolveDistributionImage(lookup, service.Image, "")
				cancelLookup()
				if err != nil || resolved == nil {
					continue
				}
				platforms := []string{}
				for _, platform := range resolved.Platforms {
					platform = strings.ToLower(strings.TrimSpace(platform))
					if validPlatform(platform) {
						platforms = append(platforms, platform)
					}
				}
				service.ImagePlatforms = uniqueSorted(platforms)
				if len(service.ImagePlatforms) > 64 {
					service.ImagePlatforms = service.ImagePlatforms[:64]
				}
			}
		})
	}
	for index := range analysis.Services {
		service := &analysis.Services[index]
		if service.BuildContext != "" || service.Image == "" || strings.Contains(service.Image, "$") {
			continue
		}
		select {
		case pending <- service:
		case <-ctx.Done():
		}
	}
	close(pending)
	workers.Wait()
}

const (
	composeImageLookupWorkers = 4
	composeImageLookupBudget  = 15 * time.Second
)

// composeValidationDocuments is what `docker compose config` validates: the
// documents themselves, except that an env_file the checkout lacks is marked
// optional in the copy, so its absence becomes one named preflight finding
// instead of an analysis that refuses the whole source.
func composeValidationDocuments(documents []ComposeDocument, analysis ComposeAnalysis) []ComposeDocument {
	missing := map[string]map[string]bool{}
	for _, service := range analysis.Services {
		for _, file := range service.EnvFiles {
			if file.Missing && file.Required {
				if missing[service.Name] == nil {
					missing[service.Name] = map[string]bool{}
				}
				missing[service.Name][file.Path] = true
			}
		}
	}
	if len(missing) == 0 {
		return documents
	}
	optional := func(path string) *yaml.Node {
		return &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "path"}, {Kind: yaml.ScalarNode, Value: path},
			{Kind: yaml.ScalarNode, Value: "required"}, {Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"},
		}}
	}
	patched := make([]ComposeDocument, 0, len(documents))
	for _, document := range documents {
		var root yaml.Node
		if yaml.Unmarshal([]byte(document.Content), &root) != nil {
			patched = append(patched, document)
			continue
		}
		services := mappingValue(documentMapping(&root), "services")
		changed := false
		for index := 0; services != nil && index+1 < len(services.Content); index += 2 {
			paths := missing[services.Content[index].Value]
			service := services.Content[index+1]
			if paths == nil || service.Kind != yaml.MappingNode {
				continue
			}
			for key := 0; key+1 < len(service.Content); key += 2 {
				if service.Content[key].Value != "env_file" {
					continue
				}
				node := service.Content[key+1]
				switch node.Kind {
				case yaml.ScalarNode:
					if paths[node.Value] {
						service.Content[key+1] = &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{optional(node.Value)}}
						changed = true
					}
				case yaml.SequenceNode:
					for item := range node.Content {
						entry := node.Content[item]
						if entry.Kind == yaml.ScalarNode && paths[entry.Value] {
							node.Content[item] = optional(entry.Value)
							changed = true
						} else if entry.Kind == yaml.MappingNode && paths[scalarMappingValue(entry, "path")] {
							node.Content[item] = optional(scalarMappingValue(entry, "path"))
							changed = true
						}
					}
				}
			}
		}
		if !changed {
			patched = append(patched, document)
			continue
		}
		encoded, err := yaml.Marshal(&root)
		if err != nil {
			patched = append(patched, document)
			continue
		}
		document.Content = string(encoded)
		patched = append(patched, document)
	}
	return patched
}

func composeSourceEvidence(analysis ComposeAnalysis) []DetectionEvidence {
	evidence := []DetectionEvidence{{Path: strings.Join(analysis.Files, ", "), Reason: analysis.Digest}}
	if analysis.PrimaryService != "" {
		evidence = append(evidence, DetectionEvidence{Path: strings.Join(analysis.Files, ", "),
			Reason: "primary service " + analysis.PrimaryService + ": readiness and the release's container follow it"})
	}
	return evidence
}

// composeOptionalDetectedVariables lists the defaulted variables as the
// form's detected rows, with the file's default as the example: they are
// settable, never required.
func composeOptionalDetectedVariables(analysis ComposeAnalysis) []DetectedVariable {
	variables := []DetectedVariable{}
	for _, variable := range analysis.OptionalVariables {
		if len(variables) >= 64 {
			break
		}
		variables = append(variables, DetectedVariable{Name: variable.Name, Example: variable.Default, Sources: []string{analysis.Files[0]}})
	}
	return variables
}
