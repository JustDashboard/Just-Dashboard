package deploy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type ComposeServicePlan struct {
	Name            string   `json:"name"`
	Image           string   `json:"image,omitempty"`
	BuildContext    string   `json:"buildContext,omitempty"`
	BuildDockerfile string   `json:"buildDockerfile,omitempty"`
	Ports           []string `json:"ports"`
	Mounts          []string `json:"mounts"`
	Healthcheck     bool     `json:"healthcheck"`
	Advanced        []string `json:"advanced"`

	// The rest of a service's build (compose_build.go): the stage, the
	// build arguments by name with the file's own value expression, the
	// platform it pins, its env_file paths, and what analysing it in its
	// checkout proved about building it.
	BuildTarget         string            `json:"buildTarget,omitempty"`
	BuildArgs           []ComposeBuildArg `json:"buildArgs,omitempty"`
	Platform            string            `json:"platform,omitempty"`
	ImagePlatforms      []string          `json:"imagePlatforms,omitempty"`
	EnvFiles            []ComposeEnvFile  `json:"envFiles,omitempty"`
	BuildContextMissing bool              `json:"buildContextMissing,omitempty"`
	DockerfileIssues    []ImageBuildIssue `json:"dockerfileIssues,omitempty"`
}

type ComposeAnalysis struct {
	Digest      string               `json:"digest"`
	Files       []string             `json:"files"`
	Services    []ComposeServicePlan `json:"services"`
	Variables   []string             `json:"variables"`
	Warnings    []string             `json:"warnings"`
	Unsupported []string             `json:"unsupported"`
	Preview     string               `json:"preview"`

	// PrimaryService is the service readiness and the release's container
	// identity follow: one that builds or publishes a port, never a database.
	PrimaryService string `json:"primaryService,omitempty"`
	// OptionalVariables are interpolated with a default (`${PORT:-3000}`), so
	// Compose runs without them; Variables are the ones it cannot.
	OptionalVariables []ComposeOptionalVariable `json:"optionalVariables,omitempty"`
}

func analyzeComposeDocuments(documents []ComposeDocument) (ComposeAnalysis, error) {
	if err := validateComposeDocuments(documents); err != nil {
		return ComposeAnalysis{}, err
	}
	documents = append([]ComposeDocument(nil), documents...)
	sort.SliceStable(documents, func(i, j int) bool {
		if documents[i].Order != documents[j].Order {
			return documents[i].Order < documents[j].Order
		}
		return documents[i].Path < documents[j].Path
	})
	analysis := ComposeAnalysis{
		Files: []string{}, Services: []ComposeServicePlan{}, Variables: []string{},
		Warnings: []string{}, Unsupported: []string{},
	}
	hash := sha256.New()
	serviceMap := map[string]ComposeServicePlan{}
	variableSet := map[string]bool{}
	optionalSet := map[string]composeVariableUse{}
	previews := make([]string, 0, len(documents))
	for _, document := range documents {
		analysis.Files = append(analysis.Files, document.Path)
		_, _ = hash.Write([]byte(document.Path))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(document.Content))
		_, _ = hash.Write([]byte{0})
		var root yaml.Node
		if err := yaml.Unmarshal([]byte(document.Content), &root); err != nil {
			return ComposeAnalysis{}, fmt.Errorf("%w: %s: %v", ErrInvalidCompose, document.Path, err)
		}
		if countYAMLNodes(&root, 0) > 100_000 {
			return ComposeAnalysis{}, fmt.Errorf("%w: %s has too many YAML nodes", ErrInvalidCompose, document.Path)
		}
		mapping := documentMapping(&root)
		if mapping == nil {
			return ComposeAnalysis{}, fmt.Errorf("%w: %s must contain a mapping", ErrInvalidCompose, document.Path)
		}
		services := mappingValue(mapping, "services")
		if services == nil || services.Kind != yaml.MappingNode {
			return ComposeAnalysis{}, fmt.Errorf("%w: %s has no services mapping", ErrInvalidCompose, document.Path)
		}
		for i := 0; i+1 < len(services.Content); i += 2 {
			nameNode, serviceNode := services.Content[i], services.Content[i+1]
			if !validComposeServiceName(nameNode.Value) || serviceNode.Kind != yaml.MappingNode {
				return ComposeAnalysis{}, fmt.Errorf("%w: invalid service %q in %s", ErrInvalidCompose, nameNode.Value, document.Path)
			}
			service, warnings, unsupported, err := composeServiceFromNode(nameNode.Value, serviceNode, document.Path)
			if err != nil {
				return ComposeAnalysis{}, fmt.Errorf("%w: %s service %s: %v", ErrInvalidCompose, document.Path, nameNode.Value, err)
			}
			serviceMap[nameNode.Value] = service
			for _, port := range service.Ports {
				if strings.Contains(port, "${") {
					unsupported = append(unsupported, "service "+nameNode.Value+" has a dynamic published port that cannot be collision-checked")
				}
			}
			analysis.Warnings = append(analysis.Warnings, warnings...)
			analysis.Unsupported = append(analysis.Unsupported, unsupported...)
		}
		pathWarnings, pathUnsupported, err := composeDocumentPaths(mapping)
		if err != nil {
			return ComposeAnalysis{}, fmt.Errorf("%w: %s: %v", ErrInvalidCompose, document.Path, err)
		}
		analysis.Warnings = append(analysis.Warnings, pathWarnings...)
		analysis.Unsupported = append(analysis.Unsupported, pathUnsupported...)
		if err := rejectComposeSecretLiterals(mapping, nil); err != nil {
			return ComposeAnalysis{}, fmt.Errorf("%w: %s: %v", ErrInvalidCompose, document.Path, err)
		}
		collectComposeVariables(mapping, variableSet)
		collectComposeDefaults(mapping, optionalSet)
		sanitized := cloneYAMLNode(&root)
		maskComposeSecrets(sanitized, nil)
		encoded, err := yaml.Marshal(sanitized)
		if err != nil {
			return ComposeAnalysis{}, err
		}
		previews = append(previews, "# "+document.Path+"\n"+strings.TrimSpace(string(encoded)))
	}
	names := make([]string, 0, len(serviceMap))
	for name := range serviceMap {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		service := serviceMap[name]
		sort.Strings(service.Ports)
		sort.Strings(service.Mounts)
		sort.Strings(service.Advanced)
		analysis.Services = append(analysis.Services, service)
	}
	analysis.Warnings = uniqueSorted(analysis.Warnings)
	analysis.Unsupported = uniqueSorted(analysis.Unsupported)
	for variable := range variableSet {
		analysis.Variables = append(analysis.Variables, variable)
	}
	sort.Strings(analysis.Variables)
	analysis.Variables, analysis.OptionalVariables = splitComposeVariables(analysis.Variables, optionalSet)
	analysis.PrimaryService = composePrimaryService(analysis.Services)
	analysis.Digest = "sha256:" + hex.EncodeToString(hash.Sum(nil))
	analysis.Preview = strings.Join(previews, "\n---\n")
	return analysis, nil
}

func documentMapping(root *yaml.Node) *yaml.Node {
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return nil
	}
	return root
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func composeServiceFromNode(name string, node *yaml.Node, documentPath string) (ComposeServicePlan, []string, []string, error) {
	service := ComposeServicePlan{Name: name, Ports: []string{}, Mounts: []string{}, Advanced: []string{}}
	warnings := []string{}
	unsupported := []string{}
	for _, key := range []string{"command", "entrypoint"} {
		if err := rejectComposeCommandSecrets(mappingValue(node, key)); err != nil {
			return service, nil, nil, fmt.Errorf("%s: %w", key, err)
		}
	}
	if image := mappingValue(node, "image"); image != nil && image.Kind == yaml.ScalarNode {
		service.Image = image.Value
		if strings.Contains(image.Value, "${") {
			// Compose interpolation is resolved only against the scoped variable
			// snapshot; keeping the expression here is the secret-free preview.
		} else if _, err := normalizeImageReference(image.Value); err != nil {
			return service, nil, nil, fmt.Errorf("invalid image: %w", err)
		}
	}
	if build := mappingValue(node, "build"); build != nil {
		contextValue := "."
		dockerfileValue := "Dockerfile"
		switch build.Kind {
		case yaml.ScalarNode:
			contextValue = build.Value
		case yaml.MappingNode:
			if context := mappingValue(build, "context"); context != nil {
				contextValue = context.Value
			}
			if dockerfile := mappingValue(build, "dockerfile"); dockerfile != nil {
				dockerfileValue = dockerfile.Value
			}
		}
		if contextValue == "" || !validComposeBuildContextReference(contextValue, documentPath) {
			return service, nil, nil, fmt.Errorf("build context escapes the Compose source")
		}
		if dockerfileValue == "" || !validComposeRelativeReference(dockerfileValue) {
			return service, nil, nil, fmt.Errorf("Dockerfile path escapes the build context")
		}
		if build.Kind == yaml.MappingNode {
			var argsErr error
			service.BuildTarget = scalarMappingValue(build, "target")
			service.BuildArgs, argsErr = composeBuildArgs(mappingValue(build, "args"))
			if argsErr != nil {
				return service, nil, nil, argsErr
			}
			for _, key := range []string{"secrets", "ssh"} {
				if mappingValue(build, key) != nil {
					unsupported = append(unsupported, "service "+name+" build uses "+key+", which deployment builds do not provide")
				}
			}
		}
		if strings.Contains(contextValue, "$") || strings.Contains(dockerfileValue, "$") {
			unsupported = append(unsupported, "service "+name+" uses a dynamic build path that cannot be immutably contained")
			service.BuildContext = contextValue
			service.BuildDockerfile = dockerfileValue
		} else {
			contextPath := filepath.Clean(filepath.Join(filepath.Dir(documentPath), contextValue))
			if contextPath != "." && !safeRelativePath(filepath.ToSlash(contextPath)) {
				return service, nil, nil, fmt.Errorf("build context escapes the Compose source")
			}
			service.BuildContext = filepath.ToSlash(contextPath)
			service.BuildDockerfile = filepath.ToSlash(filepath.Clean(dockerfileValue))
		}
	}
	for _, key := range []string{"ports", "volumes"} {
		value := mappingValue(node, key)
		if value == nil || value.Kind != yaml.SequenceNode {
			continue
		}
		for _, item := range value.Content {
			if key == "ports" {
				if port := composePortValue(item); port != "" {
					service.Ports = append(service.Ports, port)
				}
				continue
			}
			mount, err := composeMountValue(item)
			if err != nil {
				return service, nil, nil, err
			}
			if mount != "" {
				service.Mounts = append(service.Mounts, mount)
				if strings.Contains(mount, "/var/run/docker.sock") {
					service.Advanced = append(service.Advanced, "docker_socket")
				}
			}
		}
	}
	service.Healthcheck = mappingValue(node, "healthcheck") != nil
	service.Platform = strings.ToLower(scalarMappingValue(node, "platform"))
	service.EnvFiles = composeEnvFiles(mappingValue(node, "env_file"))
	advancedKeys := map[string]string{
		"privileged": "privileged", "devices": "devices", "cap_add": "capabilities",
		"pid": "pid_namespace", "ipc": "ipc_namespace", "security_opt": "security_options",
	}
	for key, label := range advancedKeys {
		if value := mappingValue(node, key); value != nil {
			if key != "privileged" || strings.EqualFold(value.Value, "true") {
				service.Advanced = append(service.Advanced, label)
			}
		}
	}
	if mode := mappingValue(node, "network_mode"); mode != nil && mode.Value == "host" {
		service.Advanced = append(service.Advanced, "host_network")
	}
	if !service.Healthcheck {
		warnings = append(warnings, "service "+name+" has no Docker healthcheck")
	}
	for _, key := range []string{"extends", "include"} {
		if mappingValue(node, key) != nil {
			unsupported = append(unsupported, "service "+name+" uses "+key)
		}
	}
	return service, warnings, unsupported, nil
}

func composePortValue(node *yaml.Node) string {
	if node == nil {
		return ""
	}
	if node.Kind == yaml.ScalarNode {
		return node.Value
	}
	if node.Kind != yaml.MappingNode {
		return ""
	}
	target := scalarMappingValue(node, "target")
	published := scalarMappingValue(node, "published")
	if target == "" {
		return ""
	}
	value := target
	if published != "" {
		value = published + ":" + target
		if host := scalarMappingValue(node, "host_ip"); host != "" {
			value = host + ":" + value
		}
	}
	if protocol := scalarMappingValue(node, "protocol"); protocol != "" && protocol != "tcp" {
		value += "/" + protocol
	}
	return value
}

func rejectComposeCommandSecrets(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.ScalarNode {
		if rejectPlanSecretLiteral("Compose command", node.Value) != nil || secretCommandFlagRE.MatchString(node.Value) {
			return fmt.Errorf("credential material must not be passed through argv")
		}
		return nil
	}
	if node.Kind != yaml.SequenceNode {
		return nil
	}
	arguments := make([]string, 0, len(node.Content))
	for _, child := range node.Content {
		if child.Kind == yaml.ScalarNode {
			arguments = append(arguments, child.Value)
		}
	}
	for index, argument := range arguments {
		if rejectPlanSecretLiteral("Compose command", argument) != nil || commandArgumentContainsSecret(arguments, index) {
			return fmt.Errorf("credential material must not be passed through argv")
		}
	}
	return nil
}

func composeMountValue(node *yaml.Node) (string, error) {
	if node == nil {
		return "", nil
	}
	if node.Kind == yaml.ScalarNode {
		if err := validateComposeMountSource(node.Value); err != nil {
			return "", err
		}
		return node.Value, nil
	}
	if node.Kind != yaml.MappingNode {
		return "", nil
	}
	source := scalarMappingValue(node, "source")
	target := scalarMappingValue(node, "target")
	if source != "" {
		if err := validateComposeMountSource(source + ":" + target); err != nil {
			return "", err
		}
	}
	if source == "" || target == "" {
		return target, nil
	}
	value := source + ":" + target
	if strings.EqualFold(scalarMappingValue(node, "read_only"), "true") {
		value += ":ro"
	}
	return value, nil
}

func validateComposeMountSource(value string) error {
	if strings.Contains(value, "${") {
		return fmt.Errorf("dynamic mount sources cannot be contained during preflight")
	}
	parts := strings.Split(value, ":")
	if len(parts) < 2 {
		return nil
	}
	source := strings.TrimSpace(parts[0])
	if source == "" || filepath.IsAbs(source) {
		return nil
	}
	if strings.HasPrefix(source, "~") || ((strings.HasPrefix(source, ".") || strings.Contains(source, "/")) && !safeRelativePath(source)) {
		return fmt.Errorf("bind mount source escapes the Compose source")
	}
	return nil
}

func scalarMappingValue(node *yaml.Node, key string) string {
	value := mappingValue(node, key)
	if value == nil || value.Kind != yaml.ScalarNode {
		return ""
	}
	return value.Value
}

func composeDocumentPaths(mapping *yaml.Node) ([]string, []string, error) {
	warnings := []string{}
	unsupported := []string{}
	services := mappingValue(mapping, "services")
	if services != nil && services.Kind == yaml.MappingNode {
		for index := 0; index+1 < len(services.Content); index += 2 {
			name, service := services.Content[index].Value, services.Content[index+1]
			if envFiles := mappingValue(service, "env_file"); envFiles != nil {
				if err := validateComposePathNodes(envFiles); err != nil {
					return nil, nil, fmt.Errorf("service %s env_file: %w", name, err)
				}
				warnings = append(warnings, "service "+name+" reads an external environment file whose values are not previewed")
			}
			if extends := mappingValue(service, "extends"); extends != nil {
				if file := mappingValue(extends, "file"); file != nil && !validComposeRelativeReference(file.Value) {
					return nil, nil, fmt.Errorf("service %s extends file escapes the Compose source", name)
				}
			}
		}
	}
	for _, key := range []string{"configs", "secrets"} {
		entries := mappingValue(mapping, key)
		if entries == nil || entries.Kind != yaml.MappingNode {
			continue
		}
		for index := 0; index+1 < len(entries.Content); index += 2 {
			entryName, entry := entries.Content[index].Value, entries.Content[index+1]
			if file := mappingValue(entry, "file"); file != nil && !validComposeRelativeReference(file.Value) {
				return nil, nil, fmt.Errorf("%s %s file escapes the Compose source", key, entryName)
			}
		}
	}
	if include := mappingValue(mapping, "include"); include != nil {
		if err := validateComposePathNodes(include); err != nil {
			return nil, nil, fmt.Errorf("include: %w", err)
		}
		unsupported = append(unsupported, "top-level include requires explicit lifecycle review")
	}
	return warnings, unsupported, nil
}

func validateComposePathNodes(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.ScalarNode {
		if !validComposeRelativeReference(node.Value) {
			return fmt.Errorf("path escapes the Compose source")
		}
		return nil
	}
	if node.Kind == yaml.SequenceNode {
		for _, child := range node.Content {
			if child.Kind == yaml.MappingNode {
				child = mappingValue(child, "path")
			}
			if err := validateComposePathNodes(child); err != nil {
				return err
			}
		}
	}
	if node.Kind == yaml.MappingNode {
		return validateComposePathNodes(mappingValue(node, "path"))
	}
	return nil
}

func validComposeRelativeReference(value string) bool {
	value = strings.ReplaceAll(value, "$$", "escaped-dollar")
	value = composeInterpolationRE.ReplaceAllString(value, "placeholder")
	if strings.Contains(value, "$") || strings.Contains(value, "://") {
		return false
	}
	return value == "." || safeRelativePath(value)
}

func validComposeBuildContextReference(value, documentPath string) bool {
	value = strings.ReplaceAll(value, "$$", "escaped-dollar")
	value = composeInterpolationRE.ReplaceAllString(value, "placeholder")
	if strings.Contains(value, "$") || strings.Contains(value, "://") || filepath.IsAbs(value) || strings.HasPrefix(value, "~") {
		return false
	}
	resolved := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(documentPath), value)))
	return resolved == "." || safeRelativePath(resolved)
}

func rejectComposeSecretLiterals(node *yaml.Node, path []string) error {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i].Value, node.Content[i+1]
			current := append(path, strings.ToLower(key))
			if secretShapedKey(key) && scalarContainsLiteral(value) {
				return fmt.Errorf("secret-shaped field %s must reference a scoped variable", strings.Join(current, "."))
			}
			if strings.EqualFold(key, "environment") {
				if err := rejectLiteralSecretEnvironment(value); err != nil {
					return err
				}
			}
			if err := rejectComposeSecretLiterals(value, current); err != nil {
				return err
			}
		}
	}
	if node.Kind == yaml.SequenceNode {
		for _, child := range node.Content {
			if err := rejectComposeSecretLiterals(child, path); err != nil {
				return err
			}
		}
	}
	if node.Kind == yaml.ScalarNode {
		lower := strings.ToLower(node.Value)
		if strings.Contains(lower, "-----begin private key-----") || containsURLCredentials(node.Value) {
			return fmt.Errorf("embedded credential material is not allowed; use a credential or variable reference")
		}
	}
	return nil
}

func rejectLiteralSecretEnvironment(node *yaml.Node) error {
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i].Value, node.Content[i+1]
			if secretShapedKey(key) && scalarContainsLiteral(value) {
				return fmt.Errorf("environment variable %s must reference a scoped variable", key)
			}
		}
	case yaml.SequenceNode:
		for _, item := range node.Content {
			key, value, found := strings.Cut(item.Value, "=")
			if secretShapedKey(key) && (!found || !isVariableExpression(value)) {
				return fmt.Errorf("environment variable %s must reference a scoped variable", key)
			}
		}
	}
	return nil
}

func secretShapedKey(key string) bool {
	key = strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	for _, fragment := range []string{"password", "passwd", "secret", "token", "private_key", "api_key", "access_key"} {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return false
}

func scalarContainsLiteral(node *yaml.Node) bool {
	return node != nil && node.Kind == yaml.ScalarNode && !isVariableExpression(node.Value)
}

func isVariableExpression(value string) bool {
	value = strings.TrimSpace(value)
	return composeVariableRE.MatchString(value)
}

// `${NAME:?message}` is a reference that refuses to run without a value, so
// it carries no literal any more than `${NAME}` does.
var composeVariableRE = regexp.MustCompile(`^\$\{[A-Za-z_][A-Za-z0-9_]*(?::?\?[^}]*)?\}$`)

func URLHasCredentials(value string) bool {
	if !strings.Contains(value, "://") {
		return false
	}
	u, err := url.Parse(value)
	if err != nil || u.User == nil {
		return false
	}
	return u.User.Username() != ""
}

var embeddedURLRE = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^[:space:]'"}]+`)

func containsURLCredentials(value string) bool {
	if URLHasCredentials(value) {
		return true
	}
	for _, candidate := range embeddedURLRE.FindAllString(value, -1) {
		if URLHasCredentials(candidate) {
			return true
		}
	}
	return false
}

var composeInterpolationRE = regexp.MustCompile(`\$(?:\{([A-Za-z_][A-Za-z0-9_]*)(?:(?::?[-+?])[^}]*)?\}|([A-Za-z_][A-Za-z0-9_]*))`)

func collectComposeVariables(node *yaml.Node, variables map[string]bool) {
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
			if name != "" {
				variables[name] = true
			}
		}
	}
	for _, child := range node.Content {
		collectComposeVariables(child, variables)
	}
}

func maskComposeSecrets(node *yaml.Node, path []string) {
	if node == nil {
		return
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i].Value, node.Content[i+1]
			if secretShapedKey(key) && value.Kind == yaml.ScalarNode && !isVariableExpression(value.Value) {
				value.Value = "***"
			}
			maskComposeSecrets(value, append(path, key))
		}
	} else {
		for _, child := range node.Content {
			maskComposeSecrets(child, path)
		}
	}
}

func cloneYAMLNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	copy := *node
	copy.Content = make([]*yaml.Node, len(node.Content))
	for i, child := range node.Content {
		copy.Content[i] = cloneYAMLNode(child)
	}
	return &copy
}

func countYAMLNodes(node *yaml.Node, count int) int {
	if node == nil || count > 100_000 {
		return count
	}
	count++
	for _, child := range node.Content {
		count = countYAMLNodes(child, count)
	}
	return count
}

func validComposeServiceName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_.-", r)) {
			return false
		}
	}
	return true
}

func uniqueSorted(values []string) []string {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = true
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func composePort(value string) (host, container int) {
	parts := strings.Split(value, ":")
	if len(parts) == 1 {
		container, _ = strconv.Atoi(strings.Split(parts[0], "/")[0])
		return
	}
	host, _ = strconv.Atoi(parts[len(parts)-2])
	container, _ = strconv.Atoi(strings.Split(parts[len(parts)-1], "/")[0])
	return
}

func cleanComposePath(path string) string { return filepath.ToSlash(filepath.Clean(path)) }
