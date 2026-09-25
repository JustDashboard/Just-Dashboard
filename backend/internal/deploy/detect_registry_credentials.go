package deploy

import (
	"bytes"
	"encoding/xml"
	"io"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// A private Maven, Gradle or NuGet registry is authenticated by variables
// the build's own configuration names: ${env.X} in a Maven settings.xml,
// System.getenv("X") in a Gradle repository's credentials, %X% in a
// NuGet.config's packageSourceCredentials. Each name is a detected variable
// with step "install", so a value given for it reaches only the step that
// downloads dependencies, through a BuildKit secret mount, and preflight
// asks for the ones the build cannot restore without (registry_token_missing).

var (
	mavenEnvRefRE   = regexp.MustCompile(`\$\{env\.([A-Za-z_][A-Za-z0-9_]*)\}`)
	gradleEnvRefREs = []*regexp.Regexp{
		regexp.MustCompile(`System\.getenv\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["']\s*\)`),
		regexp.MustCompile(`providers\.environmentVariable\(\s*["']([A-Za-z_][A-Za-z0-9_]*)["']\s*\)`),
		regexp.MustCompile(`System\.env\.([A-Za-z_][A-Za-z0-9_]*)`),
		regexp.MustCompile(`System\.env\[\s*["']([A-Za-z_][A-Za-z0-9_]*)["']\s*\]`),
	}
	gradleRepositoryNameRE = regexp.MustCompile(`\bname\s*(?:=|\()\s*["']([A-Za-z][A-Za-z0-9]*)["']`)
	gradleTypedCredentials = regexp.MustCompile(`credentials\(\s*PasswordCredentials(?:::class)?\s*\)`)
	nugetEnvRefRE          = regexp.MustCompile(`%([A-Za-z_][A-Za-z0-9_]*)%`)
)

// registryVariable is one credential a registry configuration names.
func registryVariable(name, source string, required bool) (DetectedVariable, bool) {
	if ValidateEnvKey(name) != nil || rejectPlanSecretLiteral("registry credential", name) != nil {
		return DetectedVariable{}, false
	}
	return DetectedVariable{Name: name, Sources: []string{source}, Step: "install", InstallRequired: required}, true
}

// mergeRegistryVariables keeps one row per name, required when any
// reference requires it, in name order.
func mergeRegistryVariables(variables []DetectedVariable) []DetectedVariable {
	byName := map[string]*DetectedVariable{}
	for _, variable := range variables {
		if existing := byName[variable.Name]; existing != nil {
			existing.InstallRequired = existing.InstallRequired || variable.InstallRequired
			for _, source := range variable.Sources {
				if !strings.Contains(strings.Join(existing.Sources, "\x00"), source) && len(existing.Sources) < 8 {
					existing.Sources = append(existing.Sources, source)
				}
			}
			continue
		}
		copied := variable
		byName[variable.Name] = &copied
	}
	merged := make([]DetectedVariable, 0, len(byName))
	for _, variable := range byName {
		merged = append(merged, *variable)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Name < merged[j].Name })
	if len(merged) > 16 {
		merged = merged[:16]
	}
	return merged
}

type mavenSettingsDocument struct {
	Servers []struct {
		ID    string `xml:"id"`
		Inner string `xml:",innerxml"`
	} `xml:"servers>server"`
	Mirrors []struct {
		ID string `xml:"id"`
	} `xml:"mirrors>mirror"`
	Profiles []struct {
		Repositories       []pomRepository `xml:"repositories>repository"`
		PluginRepositories []pomRepository `xml:"pluginRepositories>pluginRepository"`
	} `xml:"profiles>profile"`
}

// mavenRegistryCredentials reads the settings.xml the build uses — the one
// .mvn/maven.config passes with -s, or a committed .mvn/settings.xml or
// settings.xml that names ${env.…} credentials, which the recipe then passes
// itself — for its servers' credentials. A server is required when its id is
// a repository or mirror the build downloads from.
func mavenRegistryCredentials(files *buildFiles, context string, chain []*pomFile) (string, []DetectedVariable) {
	settings, passed := "", ""
	if content, ok := files.read(joinRootDir(context, ".mvn/maven.config"), 16<<10); ok {
		fields := strings.Fields(string(content))
		for index, field := range fields {
			switch {
			case (field == "-s" || field == "--settings") && index+1 < len(fields):
				settings = fields[index+1]
			case strings.HasPrefix(field, "--settings="):
				settings = strings.TrimPrefix(field, "--settings=")
			}
		}
	}
	if settings == "" {
		for _, name := range []string{".mvn/settings.xml", "settings.xml"} {
			if content, ok := files.read(joinRootDir(context, name), 256<<10); ok && mavenEnvRefRE.Match(content) {
				settings, passed = name, name
				break
			}
		}
	}
	if settings == "" {
		return "", nil
	}
	file, ok := joinBuildPath(context, strings.TrimPrefix(settings, "./"))
	if !ok {
		return "", nil
	}
	content, ok := files.read(file, 256<<10)
	if !ok {
		return "", nil
	}
	var document mavenSettingsDocument
	decoder := xml.NewDecoder(bytes.NewReader(content))
	decoder.Strict = false
	decoder.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
	if decoder.Decode(&document) != nil {
		return passed, nil
	}
	used := map[string]bool{}
	for _, mirror := range document.Mirrors {
		used[strings.TrimSpace(mirror.ID)] = true
	}
	for _, profile := range document.Profiles {
		for _, repository := range append(profile.Repositories, profile.PluginRepositories...) {
			used[strings.TrimSpace(repository.ID)] = true
		}
	}
	for _, pom := range chain {
		for _, repository := range append(pom.doc.Repositories, pom.doc.PluginRepositories...) {
			used[strings.TrimSpace(repository.ID)] = true
		}
	}
	var variables []DetectedVariable
	for _, server := range document.Servers {
		for _, match := range mavenEnvRefRE.FindAllStringSubmatch(server.Inner, 8) {
			if variable, ok := registryVariable(match[1], file, used[strings.TrimSpace(server.ID)]); ok {
				variables = append(variables, variable)
			}
		}
	}
	return passed, mergeRegistryVariables(variables)
}

// gradleRegistryCredentials reads the credentials of the repositories a
// Gradle build downloads from, in its scripts and settings: environment
// reads inside a repository's credentials block, and typed
// PasswordCredentials, which Gradle fills from ORG_GRADLE_PROJECT_ variables
// named for the repository. A publishing block's repositories only receive
// what the build publishes, so their credentials are not the build's.
func gradleRegistryCredentials(texts map[string]string) []DetectedVariable {
	sources := make([]string, 0, len(texts))
	for source := range texts {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	var variables []DetectedVariable
	for _, source := range sources {
		text := withoutGradleBlocks(stripGradleComments(texts[source]), "publishing")
		for _, repositories := range gradleBlocks(text, "repositories") {
			for _, credentials := range gradleBlocks(repositories, "credentials") {
				for _, pattern := range gradleEnvRefREs {
					for _, match := range pattern.FindAllStringSubmatch(credentials, 8) {
						if variable, ok := registryVariable(match[1], source, true); ok {
							variables = append(variables, variable)
						}
					}
				}
			}
			for _, repository := range gradleBlocks(repositories, "maven") {
				name := gradleRepositoryNameRE.FindStringSubmatch(repository)
				if name == nil || !gradleTypedCredentials.MatchString(repository) {
					continue
				}
				for _, suffix := range []string{"Username", "Password"} {
					if variable, ok := registryVariable("ORG_GRADLE_PROJECT_"+name[1]+suffix, source, true); ok {
						variables = append(variables, variable)
					}
				}
			}
		}
	}
	return mergeRegistryVariables(variables)
}

// nugetRegistryCredentials reads the packageSourceCredentials of the
// NuGet.config files that apply to a project. NuGet expands %NAME% in them
// from the environment; a source the configurations enable is one restore
// reads from, so its credentials are required.
func nugetRegistryCredentials(files *buildFiles, configs []string) []DetectedVariable {
	type reference struct {
		name, source, feed string
	}
	enabled := map[string]bool{}
	var references []reference
	for _, file := range configs {
		content, ok := files.read(file, 256<<10)
		if !ok {
			continue
		}
		decoder := xml.NewDecoder(bytes.NewReader(content))
		decoder.Strict = false
		decoder.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
		var stack []string
		for tokens := 0; tokens < 100_000; tokens++ {
			token, err := decoder.Token()
			if err != nil {
				break
			}
			switch element := token.(type) {
			case xml.StartElement:
				name := strings.ToLower(element.Name.Local)
				attribute := func(key string) string {
					for _, attr := range element.Attr {
						if strings.EqualFold(attr.Name.Local, key) {
							return strings.TrimSpace(attr.Value)
						}
					}
					return ""
				}
				switch {
				case name == "add" && len(stack) >= 1 && stack[len(stack)-1] == "packagesources":
					enabled[strings.ToLower(attribute("key"))] = true
				case name == "add" && len(stack) >= 1 && stack[len(stack)-1] == "disabledpackagesources" && strings.EqualFold(attribute("value"), "true"):
					delete(enabled, strings.ToLower(attribute("key")))
				case name == "add" && len(stack) >= 2 && stack[len(stack)-2] == "packagesourcecredentials":
					feed := strings.ReplaceAll(stack[len(stack)-1], "_x0020_", " ")
					for _, match := range nugetEnvRefRE.FindAllStringSubmatch(attribute("value"), 4) {
						references = append(references, reference{name: match[1], source: file, feed: feed})
					}
				}
				stack = append(stack, name)
			case xml.EndElement:
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
			}
		}
	}
	var variables []DetectedVariable
	for _, found := range references {
		if variable, ok := registryVariable(found.name, found.source, enabled[found.feed]); ok {
			variables = append(variables, variable)
		}
	}
	return mergeRegistryVariables(variables)
}

// registryConfigSource says a detected variable's source is a registry
// configuration — a package manager's file, a Maven settings.xml, a Gradle
// script's repository block, a NuGet.config — which only the dependency
// install reads, so the running application never receives the value.
func registryConfigSource(source string) bool {
	if strings.HasPrefix(source, "convention plugin ") {
		return true
	}
	name := path.Base(source)
	switch name {
	case "settings.xml", "maven.config", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts":
		return true
	}
	return slices.Contains(nodeRegistryConfigFiles, name) || slices.Contains(nugetConfigNames, name)
}
