package deploy

import (
	"bytes"
	"encoding/xml"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// A Maven project is read from its pom.xml as XML data: the parent it
// inherits from, the modules it aggregates, its properties, dependencies and
// build plugins. A module of a multi-module build cannot be built alone — its
// parent POM and sibling modules are outside its directory — so the recipe
// builds it from the reactor that aggregates it, and the module's facts
// include what its in-repository parents declare.

type pomDocument struct {
	Parent struct {
		GroupID      string  `xml:"groupId"`
		ArtifactID   string  `xml:"artifactId"`
		RelativePath *string `xml:"relativePath"`
	} `xml:"parent"`
	GroupID            string          `xml:"groupId"`
	ArtifactID         string          `xml:"artifactId"`
	Packaging          string          `xml:"packaging"`
	Modules            []string        `xml:"modules>module"`
	Properties         pomProperties   `xml:"properties"`
	Dependencies       []pomDependency `xml:"dependencies>dependency"`
	Plugins            []pomPlugin     `xml:"build>plugins>plugin"`
	Repositories       []pomRepository `xml:"repositories>repository"`
	PluginRepositories []pomRepository `xml:"pluginRepositories>pluginRepository"`
	Profiles           []pomProfile    `xml:"profiles>profile"`
}

type pomProperties struct {
	Entries []pomProperty `xml:",any"`
}

type pomProperty struct {
	XMLName xml.Name
	Value   string `xml:",chardata"`
}

type pomDependency struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
}

type pomPlugin struct {
	GroupID       string         `xml:"groupId"`
	ArtifactID    string         `xml:"artifactId"`
	Configuration pomInner       `xml:"configuration"`
	Executions    []pomExecution `xml:"executions>execution"`
}

type pomExecution struct {
	Goals         []string `xml:"goals>goal"`
	Configuration pomInner `xml:"configuration"`
}

type pomInner struct {
	Inner string `xml:",innerxml"`
}

type pomRepository struct {
	ID  string `xml:"id"`
	URL string `xml:"url"`
}

type pomProfile struct {
	ID      string      `xml:"id"`
	Modules []string    `xml:"modules>module"`
	Plugins []pomPlugin `xml:"build>plugins>plugin"`
}

// pomFile is one pom.xml read from the checkout.
type pomFile struct {
	dir  string
	text string
	doc  pomDocument
	// parsed says the XML decoded; a POM that does not still gives its text
	// to the framework match, as it always has.
	parsed bool
}

// pomFileLimit bounds one POM; generated reactors with hundreds of
// dependencies stay well under it.
const pomFileLimit = 512 << 10

func parsePOM(dir string, content []byte) *pomFile {
	file := &pomFile{dir: dir, text: string(content)}
	decoder := xml.NewDecoder(bytes.NewReader(content))
	decoder.Strict = false
	// Tag names are ASCII in any declared encoding, and a value outside it
	// only ever reads as a slightly wrong string.
	decoder.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
	file.parsed = decoder.Decode(&file.doc) == nil
	return file
}

func (p *pomFile) path() string { return joinRootDir(p.dir, "pom.xml") }

// modules lists the directories the POM aggregates, its profiles' included,
// as checkout paths that stay inside the checkout.
func (p *pomFile) modules() []string {
	references := append([]string(nil), p.doc.Modules...)
	for _, profile := range p.doc.Profiles {
		references = append(references, profile.Modules...)
	}
	var dirs []string
	for _, reference := range references {
		reference = strings.TrimSpace(reference)
		if strings.HasSuffix(strings.ToLower(reference), ".xml") {
			reference = path.Dir(reference)
		}
		if dir, ok := joinBuildPath(p.dir, reference); ok && len(dirs) < 256 {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// parentDir is where the POM's parent is in the checkout, when the parent
// resolves there: <relativePath> (default ../pom.xml) names a POM whose
// artifactId is the parent's.
func (r *jvmReader) parentPOM(p *pomFile) *pomFile {
	if !p.parsed || p.doc.Parent.ArtifactID == "" {
		return nil
	}
	relative := "../pom.xml"
	if p.doc.Parent.RelativePath != nil {
		relative = strings.TrimSpace(*p.doc.Parent.RelativePath)
		if relative == "" {
			return nil
		}
	}
	if !strings.HasSuffix(strings.ToLower(relative), ".xml") {
		relative = path.Join(relative, "pom.xml")
	}
	target, ok := joinBuildPath(p.dir, relative)
	if !ok || path.Base(target) != "pom.xml" {
		return nil
	}
	parent := r.pom(path.Dir(target))
	if parent == nil || !parent.parsed || strings.TrimSpace(parent.doc.ArtifactID) != strings.TrimSpace(p.doc.Parent.ArtifactID) {
		return nil
	}
	return parent
}

// parentChain is the POM's in-repository parents, nearest first.
func (r *jvmReader) parentChain(p *pomFile) []*pomFile {
	var chain []*pomFile
	seen := map[string]bool{p.dir: true}
	for current := r.parentPOM(p); current != nil && !seen[current.dir] && len(chain) < 8; current = r.parentPOM(current) {
		seen[current.dir] = true
		chain = append(chain, current)
	}
	return chain
}

// mavenReactor finds the aggregator whose modules reach root, following
// nested aggregators to the top: the directory the reactor build runs from.
// It is root itself when nothing aggregates it.
func (r *jvmReader) mavenReactor(root string, parents []*pomFile) string {
	current := root
	for steps := 0; steps < 8; steps++ {
		owner := ""
		candidates := ancestorDirs(current)[1:]
		for _, parent := range parents {
			candidates = append(candidates, parent.dir)
		}
		for _, dir := range candidates {
			aggregator := r.pom(dir)
			if aggregator == nil || !aggregator.parsed || dir == current {
				continue
			}
			for _, module := range aggregator.modules() {
				if module == current {
					owner = dir
					break
				}
			}
			if owner != "" {
				break
			}
		}
		if owner == "" {
			return current
		}
		current = owner
	}
	return current
}

// properties merges the in-repository parents' properties under the
// POM's own, the way Maven's inheritance does.
func mavenProperties(chain []*pomFile) map[string]string {
	properties := map[string]string{}
	for index := len(chain) - 1; index >= 0; index-- {
		for _, entry := range chain[index].doc.Properties.Entries {
			properties[entry.XMLName.Local] = strings.TrimSpace(entry.Value)
		}
	}
	return properties
}

var mavenPropertyRefRE = regexp.MustCompile(`\$\{([\w.-]+)\}`)

// expandMaven resolves ${property} references a few levels deep; what stays
// unresolved stays in the text.
func expandMaven(value string, properties map[string]string) string {
	for depth := 0; depth < 4 && strings.Contains(value, "${"); depth++ {
		value = mavenPropertyRefRE.ReplaceAllStringFunc(value, func(reference string) string {
			if resolved, ok := properties[reference[2:len(reference)-1]]; ok {
				return resolved
			}
			return reference
		})
	}
	return value
}

var (
	mavenWrapperReleaseRE = regexp.MustCompile(`apache-maven-([0-9]+)\.`)
	mavenReleaseTagRE     = regexp.MustCompile(`<release>\s*([^<\s]+)\s*</release>`)
	mavenTargetTagRE      = regexp.MustCompile(`<(?:target|source)>\s*([^<\s]+)\s*</(?:target|source)>`)
)

// mavenJavaRelease is the release a module compiles for: the compiler
// properties, then maven-compiler-plugin's own configuration, then Kotlin's
// JVM target — resolved through the properties the module inherits.
func mavenJavaRelease(chain []*pomFile, properties map[string]string) (int, string) {
	for _, key := range []string{"maven.compiler.release", "java.version", "maven.compiler.target", "maven.compiler.source", "kotlin.compiler.jvmTarget"} {
		if value, ok := properties[key]; ok {
			if release := javaRelease(expandMaven(value, properties)); release > 0 {
				return release, key + " in pom.xml"
			}
		}
	}
	for _, pom := range chain {
		for _, plugin := range pom.doc.Plugins {
			if plugin.ArtifactID != "maven-compiler-plugin" {
				continue
			}
			configuration := plugin.Configuration.Inner
			for _, pattern := range []*regexp.Regexp{mavenReleaseTagRE, mavenTargetTagRE} {
				if match := pattern.FindStringSubmatch(configuration); match != nil {
					if release := javaRelease(expandMaven(match[1], properties)); release > 0 {
						return release, "maven-compiler-plugin in " + pom.path()
					}
				}
			}
		}
	}
	return 0, ""
}

// mavenPlugins are the build plugins a module runs: its own and those its
// in-repository parents declare outside pluginManagement.
func mavenPlugins(chain []*pomFile) []pomPlugin {
	var plugins []pomPlugin
	for _, pom := range chain {
		plugins = append(plugins, pom.doc.Plugins...)
	}
	return plugins
}

// mavenResolvedText is what the module's inheritance adds to its own text
// for the passes that match build definitions by name: the dependencies and
// plugins its in-repository parents declare.
func mavenResolvedText(chain []*pomFile) string {
	var lines []string
	for _, pom := range chain[1:] {
		for _, dependency := range pom.doc.Dependencies {
			lines = append(lines, dependency.GroupID+":"+dependency.ArtifactID)
		}
		for _, plugin := range pom.doc.Plugins {
			lines = append(lines, plugin.GroupID+":"+plugin.ArtifactID)
		}
		if parent := pom.doc.Parent.ArtifactID; parent != "" {
			lines = append(lines, pom.doc.Parent.GroupID+":"+parent)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "\n<!-- inherited: " + strings.Join(lines, " ") + " -->\n"
}

// mavenPackaging decides how a module's runnable artifact is produced, from
// the plugins it runs and what it inherits.
func mavenPackaging(module *pomFile, chain []*pomFile, properties map[string]string, quarkusUber bool) (string, string) {
	plugins := mavenPlugins(chain)
	has := func(artifact string) *pomPlugin {
		for index := range plugins {
			if plugins[index].ArtifactID == artifact {
				return &plugins[index]
			}
		}
		return nil
	}
	packaging := strings.TrimSpace(expandMaven(module.doc.Packaging, properties))
	parent := strings.TrimSpace(module.doc.Parent.GroupID) + ":" + strings.TrimSpace(module.doc.Parent.ArtifactID)
	for _, pom := range chain {
		if strings.HasPrefix(pom.doc.Parent.GroupID, "io.helidon.applications") {
			parent = "io.helidon.applications:" + pom.doc.Parent.ArtifactID
		}
	}
	mainClass := false
	for _, pom := range chain {
		if strings.Contains(pom.text, "<mainClass>") || strings.Contains(pom.text, "Main-Class") {
			mainClass = true
		}
	}
	switch {
	case packaging == "pom":
		return "", ""
	case has("quarkus-maven-plugin") != nil && quarkusUber:
		return javaPackagingQuarkusUber, "quarkus-maven-plugin with an uber-jar package type"
	case has("quarkus-maven-plugin") != nil:
		return javaPackagingQuarkus, "quarkus-maven-plugin"
	case has("spring-boot-maven-plugin") != nil && packaging == "war":
		return javaPackagingSpringBootWar, "spring-boot-maven-plugin repackages the war"
	case has("spring-boot-maven-plugin") != nil:
		return javaPackagingSpringBoot, "spring-boot-maven-plugin repackages the jar"
	case has("micronaut-maven-plugin") != nil:
		return javaPackagingFatJar, "micronaut-maven-plugin"
	case strings.HasPrefix(parent, "io.helidon.applications:") || has("helidon-maven-plugin") != nil:
		return javaPackagingLibs, "Helidon application parent: the jar and target/libs"
	case has("maven-shade-plugin") != nil:
		return javaPackagingFatJar, "maven-shade-plugin"
	case has("maven-assembly-plugin") != nil && strings.Contains(pluginText(has("maven-assembly-plugin")), "jar-with-dependencies"):
		return javaPackagingFatJar, "maven-assembly-plugin jar-with-dependencies"
	case has("vertx-maven-plugin") != nil:
		return javaPackagingFatJar, "vertx-maven-plugin"
	case mainClass:
		return javaPackagingJar, "a main class in the jar's manifest"
	case packaging == "war":
		return javaPackagingWar, ""
	}
	return "", ""
}

func pluginText(plugin *pomPlugin) string {
	if plugin == nil {
		return ""
	}
	text := plugin.Configuration.Inner
	for _, execution := range plugin.Executions {
		text += execution.Configuration.Inner + strings.Join(execution.Goals, " ")
	}
	return text
}

// mavenProfiles is the -P a module's production build needs: Vaadin's
// production profile, which compiles the frontend bundle instead of leaving
// the application in development mode, and JHipster's prod profile.
func mavenProfiles(chain []*pomFile, jhipster bool) ([]string, string, bool) {
	ids := map[string]bool{}
	vaadin, buildsFrontend := false, false
	for _, pom := range chain {
		for _, profile := range pom.doc.Profiles {
			ids[strings.TrimSpace(profile.ID)] = true
		}
		if strings.Contains(pom.text, "com.vaadin") {
			vaadin = true
		}
		for _, plugin := range pom.doc.Plugins {
			if plugin.ArtifactID == "vaadin-maven-plugin" && strings.Contains(pluginText(&plugin), "build-frontend") {
				buildsFrontend = true
			}
		}
	}
	switch {
	case jhipster && ids["prod"]:
		return []string{"prod"}, "JHipster's prod profile", false
	case vaadin && ids["production"] && !buildsFrontend:
		return []string{"production"}, "Vaadin's production profile", false
	case vaadin && !buildsFrontend:
		return nil, "", true
	}
	return nil, "", false
}

// mavenWrapperMajor is the Maven major the wrapper pins, or 0.
func mavenWrapperMajor(files *buildFiles, dir string) int {
	content, ok := files.read(joinRootDir(dir, ".mvn/wrapper/maven-wrapper.properties"), 16<<10)
	if !ok || !files.regular(joinRootDir(dir, "mvnw")) {
		return 0
	}
	if match := mavenWrapperReleaseRE.FindStringSubmatch(string(content)); match != nil {
		major, _ := strconv.Atoi(match[1])
		return major
	}
	return 0
}
