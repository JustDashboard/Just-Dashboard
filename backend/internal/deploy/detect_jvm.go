package deploy

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// A JVM root is read the way its build tool would read it: from the
// reactor or settings root that owns it (detect_maven.go, detect_gradle.go),
// with the version catalog and in-repository parents resolved, so detection
// names the framework, the application module and the Java release the
// recipe will build, and the recipe (build_java.go) builds exactly that.

// How a project's runnable artifact is produced, which decides the task the
// recipe runs and what it copies into the runtime image.
const (
	javaPackagingSpringBoot    = "spring-boot"
	javaPackagingSpringBootWar = "spring-boot-war"
	javaPackagingQuarkus       = "quarkus"
	javaPackagingQuarkusUber   = "quarkus-uber-jar"
	javaPackagingKtor          = "ktor"
	javaPackagingShadow        = "shadow"
	javaPackagingApplication   = "application"
	javaPackagingFatJar        = "fat-jar"
	javaPackagingLibs          = "jar-with-libs"
	javaPackagingJar           = "jar"
	javaPackagingWar           = "war"
)

// javaRecipeReleases are the JDK releases the catalogue's images carry.
var javaRecipeReleases = []int{8, 11, 17, 21, 25}

// recipeNameRE is one name the recipes write unquoted into a generated
// Dockerfile or a command line: no spaces, quotes or shell syntax.
var recipeNameRE = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9._-]{0,127}$`)

// recipePath says a checkout path is plain names joined by slashes, so a
// generated Dockerfile can hold it unquoted.
func recipePath(value string) bool {
	return recipeNames(value, "/")
}

func recipeNames(value, separator string) bool {
	parts := strings.Split(value, separator)
	if len(parts) > 16 {
		return false
	}
	for _, part := range parts {
		if !recipeNameRE.MatchString(part) || part == "." || part == ".." {
			return false
		}
	}
	return true
}

type jvmReader struct {
	files       *buildFiles
	poms        map[string]*pomFile
	settings    map[string]*gradleSettings
	catalogs    map[string]*gradleCatalog
	conventions map[string]map[string]string
	projects    map[string]*jvmProject
}

func newJVMReader(files *buildFiles) *jvmReader {
	return &jvmReader{
		files: files, poms: map[string]*pomFile{}, settings: map[string]*gradleSettings{},
		catalogs: map[string]*gradleCatalog{}, conventions: map[string]map[string]string{}, projects: map[string]*jvmProject{},
	}
}

func (r *jvmReader) pom(dir string) *pomFile {
	if cached, ok := r.poms[dir]; ok {
		return cached
	}
	var file *pomFile
	if content, ok := r.files.read(joinRootDir(dir, "pom.xml"), pomFileLimit); ok {
		file = parsePOM(dir, content)
	}
	r.poms[dir] = file
	return file
}

// javaToolchainFacts are what the build files and version files say about
// the Java release, kept on the candidate (DetectedJavaBuild) so preflight
// can judge a plan's release without the tree.
type javaToolchainFacts struct {
	tool         string
	declared     int
	declaredFrom string
	// toolchain says the release is a Gradle toolchain: the exact JDK the
	// build compiles with, not a bytecode level any newer JDK can emit.
	toolchain  bool
	pinned     int
	pinnedFrom string
	// wrapper is the Gradle release the committed wrapper runs, and
	// wrapperUsable whether the wrapper can run at all (its jar committed).
	wrapper       string
	wrapperUsable bool
	foojay        bool
}

// jvmProject is what a JVM root's build files say about building it.
type jvmProject struct {
	tool string
	root string
	// context is the directory the build's files are all under: the Maven
	// reactor with its in-repository parents, or the Gradle settings root
	// with the builds it includes. reactor is the directory the build runs
	// from — the aggregator POM's, the settings root — and module how the
	// build selects root there: a Maven -pl selector, or a Gradle project
	// path.
	context   string
	reactor   string
	module    string
	buildFile string
	// text is root's build definition with what the build resolves
	// appended — in-repository parents' dependencies and plugins, and the
	// coordinates and plugin ids version-catalog aliases stand for — which
	// is what the passes that match build definitions by name read.
	text        string
	framework   string
	packaging   string
	packagingBy string
	// aggregator says root aggregates other modules or projects; member
	// says root is one of a wider build's.
	aggregator bool
	member     bool
	library    bool
	android    string
	// androidApp says the project itself applies the Android Gradle
	// plugin: an app module, which the not-deployable pass sets aside.
	androidApp bool
	// buildLogic says why a Gradle project builds the build's own plugins
	// (buildSrc, build-logic): tooling, never a deployment.
	buildLogic string
	toolchain  javaToolchainFacts
	// profiles are activated for a production build: -P for Maven, -P
	// properties for Gradle. vaadinDevMode says Vaadin would be built in
	// development mode, which needs Node at runtime.
	profiles      []string
	profilesBy    string
	vaadinDevMode bool
	mavenSettings string
	mavenWrapper  bool
	// wrapperJarMissing says gradlew is committed without
	// gradle/wrapper/gradle-wrapper.jar (a *.jar ignore rule), so the
	// image's own Gradle builds instead.
	wrapperJarMissing bool
	// springProfile is the production profile the application ships
	// configuration for (application-prod.yml), and where.
	springProfile     string
	springProfileFile string
	variables         []DetectedVariable
	evidence          []DetectionEvidence
}

func (p *jvmProject) runnable() bool {
	return p.packaging != "" && p.packaging != javaPackagingWar
}

// project reads the JVM build at a checkout directory, or nil.
func (r *jvmReader) project(root string) *jvmProject {
	root = checkoutRoot(root)
	if cached, ok := r.projects[root]; ok {
		return cached
	}
	var project *jvmProject
	if pom := r.pom(root); pom != nil {
		project = r.mavenProject(root, pom)
	} else if script, file := r.gradleProjectScript(root); file != "" {
		project = r.gradleProject(root, script, file)
	}
	if project != nil {
		project.framework = javaFramework(project.text)
		r.productionProfile(project)
	}
	r.projects[root] = project
	return project
}

func (r *jvmReader) mavenProject(root string, pom *pomFile) *jvmProject {
	project := &jvmProject{tool: "maven", root: root, buildFile: pom.path(), text: pom.text}
	parents := r.parentChain(pom)
	chain := append([]*pomFile{pom}, parents...)
	properties := mavenProperties(chain)
	reactor := r.mavenReactor(root, parents)
	dirs := []string{root, reactor}
	for _, parent := range parents {
		dirs = append(dirs, parent.dir)
	}
	project.context = commonDir(dirs)
	project.reactor = reactor
	project.member = reactor != root || len(parents) > 0
	// A POM-packaged project builds nothing but the modules it lists or the
	// children that inherit from it.
	project.aggregator = len(pom.modules()) > 0 || strings.TrimSpace(pom.doc.Packaging) == "pom"
	if reactor != root {
		artifact := strings.TrimSpace(expandMaven(pom.doc.ArtifactID, properties))
		if mavenArtifactRE.MatchString(artifact) {
			project.module = ":" + artifact
		} else {
			project.module = relativeBuildPath(reactor, root)
		}
		project.evidence = append(project.evidence, DetectionEvidence{Path: joinRootDir(reactor, "pom.xml"),
			Reason: "module of the Maven reactor in " + contextLabel(displayDir(reactor)) + "; built from there with its sibling modules and parent POM"})
	} else if len(parents) > 0 {
		project.evidence = append(project.evidence, DetectionEvidence{Path: parents[0].path(),
			Reason: "inherits the parent POM " + parents[0].path() + "; built from " + contextLabel(displayDir(project.context))})
	}
	project.text += mavenResolvedText(chain)
	project.packaging, project.packagingBy = mavenPackaging(pom, chain, properties, r.quarkusUberJar(root, properties))
	release, from := mavenJavaRelease(chain, properties)
	project.toolchain = javaToolchainFacts{tool: "maven", declared: release, declaredFrom: from}
	project.toolchain.pinned, project.toolchain.pinnedFrom = r.javaPin(root, project.context)
	jhipster := r.jhipster(project.context, project.text)
	project.profiles, project.profilesBy, project.vaadinDevMode = mavenProfiles(chain, jhipster)
	project.mavenWrapper = mavenWrapperMajor(r.files, project.context) >= 4
	project.mavenSettings, project.variables = mavenRegistryCredentials(r.files, project.context, chain)
	return project
}

var mavenArtifactRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)

func (r *jvmReader) gradleProject(root, script, file string) *jvmProject {
	project := &jvmProject{tool: "gradle", root: root, buildFile: file, reactor: root, module: ":"}
	settings := r.gradleSettingsFor(root)
	if settings != nil {
		if projectPath := settings.projectPath(root); projectPath != "" {
			project.reactor, project.module = settings.dir, projectPath
		} else {
			// The nearest settings belong to another build; this directory
			// builds on its own, from its own files.
			settings = nil
		}
	}
	// Gradle runs from the settings root; a composite build's included
	// builds are read from there too, so the context holds them wherever
	// they are.
	project.context = project.reactor
	if settings != nil {
		project.context = commonDir(append([]string{settings.dir}, settings.includedBuilds...))
	}
	project.member = project.module != ":"
	project.aggregator = settings != nil && settings.dir == root && len(settings.projects) > 0
	catalog := r.gradleCatalogFor(project.reactor)
	conventions := r.gradleConventionScripts(settings)
	ids, conventionTexts := gradleApplied(script, catalog, conventions)
	if project.member {
		// A legacy multi-project build applies its plugins to every
		// subproject from the root script's subprojects {} or allprojects {}.
		if rootScript, _ := r.gradleProjectScript(project.reactor); rootScript != "" {
			inherited := gradleInheritedBlocks(rootScript, project.module)
			for _, id := range gradlePluginIDs(inherited, catalog) {
				if !slices.Contains(ids, id) {
					ids = append(ids, id)
				}
			}
			conventionTexts = append(conventionTexts, inherited)
		}
	}
	own := strings.Join(append([]string{script}, conventionTexts...), "\n")
	resolved := append(append([]string(nil), ids...), catalog.resolve(stripGradleComments(own))...)
	project.text = script
	if len(resolved) > 0 {
		project.text += "\n// resolved: " + strings.Join(resolved, " ") + "\n"
	}
	project.packaging, project.packagingBy = gradlePackaging(ids, own)
	if project.packaging == javaPackagingQuarkus && r.quarkusUberJar(root, nil) {
		project.packaging, project.packagingBy = javaPackagingQuarkusUber, "the Quarkus plugin's uber-jar"
	}
	project.library = project.packaging == "" && pluginApplied(ids, "java-library")
	project.androidApp = gradleAndroid(ids)
	project.buildLogic = r.gradleBuildLogic(project, ids)
	if settings != nil {
		if project.member {
			project.evidence = append(project.evidence, DetectionEvidence{Path: settings.file,
				Reason: "project " + project.module + " of the Gradle build in " + contextLabel(displayDir(settings.dir)) + "; built from there with its settings, catalog and sibling projects"})
		}
		project.android = r.gradleAndroidDependency(project.module, own, settings, catalog, conventions)
	}
	release, toolchain := gradleJavaRelease(own, catalog)
	from := file
	if release == 0 && project.member {
		if rootScript, rootFile := r.gradleProjectScript(project.reactor); rootFile != "" {
			release, toolchain = gradleJavaRelease(rootScript, catalog)
			from = rootFile
		}
	}
	conventionIDs := make([]string, 0, len(conventions))
	for id := range conventions {
		conventionIDs = append(conventionIDs, id)
	}
	sort.Strings(conventionIDs)
	for _, id := range conventionIDs {
		if release != 0 {
			break
		}
		if value, exact := gradleJavaRelease(conventions[id], catalog); value > 0 {
			release, toolchain, from = value, exact, "convention plugin "+id
		}
	}
	if release == 0 && catalog != nil {
		for _, key := range []string{"java", "jdk", "jvm", "java.version", "javaVersion", "jvm.target", "jvmTarget"} {
			if value := javaRelease(catalog.versions[gradleAccessor(key)]); value > 0 {
				release, from = value, "gradle/libs.versions.toml "+key
				break
			}
		}
	}
	facts := javaToolchainFacts{tool: "gradle", declared: release, toolchain: toolchain && release > 0}
	if release > 0 {
		facts.declaredFrom = from
	}
	facts.pinned, facts.pinnedFrom = r.javaPin(root, project.reactor)
	if content, ok := r.files.read(joinRootDir(project.reactor, "gradle/wrapper/gradle-wrapper.properties"), 16<<10); ok &&
		r.files.regular(joinRootDir(project.reactor, "gradlew")) {
		facts.wrapper = gradleWrapperVersion(content)
		facts.wrapperUsable = r.files.regular(joinRootDir(project.reactor, "gradle/wrapper/gradle-wrapper.jar"))
		project.wrapperJarMissing = !facts.wrapperUsable
	}
	facts.foojay = settings != nil && settings.foojay
	project.toolchain = facts
	jhipster := r.jhipster(project.reactor, project.text)
	switch {
	case jhipster:
		project.profiles, project.profilesBy = []string{"prod"}, "JHipster's prod build"
	case strings.Contains(project.text, "com.vaadin") && !gradleVaadinProduction(own):
		project.profiles, project.profilesBy = []string{"vaadin.productionMode=true"}, "Vaadin's production build"
	}
	texts := map[string]string{file: script}
	if settings != nil {
		texts[settings.file] = settings.text
		if rootScript, rootFile := r.gradleProjectScript(settings.dir); rootFile != "" {
			texts[rootFile] = rootScript
		}
	}
	for id, convention := range conventions {
		texts["convention plugin "+id] = convention
	}
	project.variables = gradleRegistryCredentials(texts)
	return project
}

// gradlePluginDevelopment are the plugins a project applies to build Gradle
// plugins: convention plugins the build applies to itself.
var gradlePluginDevelopment = []string{"java-gradle-plugin", "groovy-gradle-plugin", "kotlin-dsl", "org.gradle.kotlin.kotlin-dsl"}

// gradleBuildLogic says why a Gradle project is the build's own logic
// rather than something it builds to run: buildSrc, a build-logic build,
// a build pluginManagement includes, or a project that builds plugins.
func (r *jvmReader) gradleBuildLogic(project *jvmProject, ids []string) string {
	if slices.Contains(strings.Split(project.root, "/"), "buildSrc") {
		return "Gradle build logic: buildSrc holds the convention plugins the build applies to itself, not an application"
	}
	if id := slices.IndexFunc(ids, func(id string) bool { return slices.Contains(gradlePluginDevelopment, id) }); id >= 0 {
		return "Gradle build logic: applies " + ids[id] + " to build the convention plugins the build applies, not an application"
	}
	for _, ancestor := range ancestorDirs(project.reactor)[1:] {
		if settings := r.gradleSettingsAt(ancestor); settings != nil && slices.Contains(settings.pluginBuilds, project.reactor) {
			return "Gradle build logic: " + settings.file + " includes it in pluginManagement for the plugins it provides, not as an application"
		}
	}
	return ""
}

var gradleVaadinProductionRE = regexp.MustCompile(`productionMode\s*(?:=|\.set\()\s*true`)

func gradleVaadinProduction(text string) bool {
	for _, block := range gradleBlocks(stripGradleComments(text), "vaadin") {
		if gradleVaadinProductionRE.MatchString(block) {
			return true
		}
	}
	return false
}

// gradleAndroidDependency names a project of the build that applies the
// Android Gradle plugin, which Gradle cannot configure without the Android
// SDK: one the application depends on — the Kotlin Multiplatform wizard's
// shared module — or, since Gradle configures every project of a build
// unless configure-on-demand is on, any other.
func (r *jvmReader) gradleAndroidDependency(projectPath, script string, settings *gradleSettings, catalog *gradleCatalog, conventions map[string]string) string {
	android := func(path string) (string, []string, bool) {
		dependency, file := r.gradleProjectScript(settings.projects[path])
		if file == "" {
			return "", nil, false
		}
		ids, texts := gradleApplied(dependency, catalog, conventions)
		return file, append([]string{dependency}, texts...), gradleAndroid(ids)
	}
	seen := map[string]bool{projectPath: true}
	pending := gradleProjectRefs(script, settings)
	for len(pending) > 0 && len(seen) < 32 {
		next := pending[0]
		pending = pending[1:]
		if seen[next] {
			continue
		}
		seen[next] = true
		file, texts, applies := android(next)
		if applies {
			return next + " (" + file + ") applies the Android Gradle plugin, and Gradle cannot configure it without the Android SDK"
		}
		pending = append(pending, gradleProjectRefs(strings.Join(texts, "\n"), settings)...)
	}
	if properties, ok := r.files.read(joinRootDir(settings.dir, "gradle.properties"), 64<<10); ok && gradleConfigureOnDemandRE.Match(properties) {
		return ""
	}
	paths := make([]string, 0, len(settings.projects))
	for path := range settings.projects {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if path == projectPath {
			continue
		}
		if file, _, applies := android(path); applies {
			return path + " (" + file + ") applies the Android Gradle plugin, and Gradle configures every project of the build, which it cannot do for that one without the Android SDK"
		}
	}
	return ""
}

var gradleConfigureOnDemandRE = regexp.MustCompile(`(?m)^\s*org\.gradle\.configureondemand\s*[=:]\s*true\s*$`)

// javaPin is the release the nearest version file pins, looking from the
// project's directory up to the build's.
func (r *jvmReader) javaPin(root, context string) (int, string) {
	for _, dir := range ancestorDirs(root) {
		if pin, release := javaVersionPin(r.files, dir); release > 0 {
			return release, pin.source
		}
		if dir == context || dir == "." {
			break
		}
	}
	return 0, ""
}

var quarkusUberRE = regexp.MustCompile(`(?m)^\s*quarkus\.package\.(?:jar\.)?type\s*=\s*uber-jar\s*$`)

// quarkusUberJar says Quarkus is configured to package one runner jar
// instead of its quarkus-app directory.
func (r *jvmReader) quarkusUberJar(root string, properties map[string]string) bool {
	for _, key := range []string{"quarkus.package.jar.type", "quarkus.package.type"} {
		if properties[key] == "uber-jar" {
			return true
		}
	}
	for _, name := range []string{"src/main/resources/application.properties", "gradle.properties"} {
		if content, ok := r.files.read(joinRootDir(root, name), 256<<10); ok && quarkusUberRE.Match(content) {
			return true
		}
	}
	return false
}

// jhipster says the build was generated by JHipster, whose production
// profile bakes the prod configuration and front end into the artifact.
func (r *jvmReader) jhipster(context, text string) bool {
	if strings.Contains(text, "tech.jhipster") || strings.Contains(text, "jhipster-framework") {
		return true
	}
	content, ok := r.files.read(joinRootDir(context, ".yo-rc.json"), 64<<10)
	return ok && strings.Contains(string(content), "generator-jhipster")
}

// productionProfile records the Spring profile the application ships
// production configuration for, which the operator is offered to activate.
func (r *jvmReader) productionProfile(project *jvmProject) {
	if project.framework != "spring-boot" {
		return
	}
	for _, profile := range []string{"prod", "production"} {
		for _, extension := range []string{"properties", "yml", "yaml"} {
			file := joinRootDir(project.root, "src/main/resources/application-"+profile+"."+extension)
			if r.files.regular(file) {
				project.springProfile, project.springProfileFile = profile, file
				return
			}
		}
	}
}

// displayDir is a reader directory as candidates name roots ("" for the top).
func displayDir(dir string) string {
	if dir == "." {
		return ""
	}
	return dir
}

// planJavaToolchain decides which JDK builds a project and which JRE runs
// it, from what its files declare and the operator's choice. A Gradle
// toolchain is exact: when Gradle cannot run on that JDK, it runs on one it
// supports and the toolchain JDK is provided beside it. A bytecode level any
// newer JDK can emit maps to the nearest release the catalogue carries.
func planJavaToolchain(facts javaToolchainFacts, override string) (javaToolchainPlan, error) {
	plan := javaToolchainPlan{tool: facts.tool}
	requested, from := 0, ""
	exact := facts.tool == "gradle" && facts.toolchain && facts.declared > 0
	switch {
	case override != "":
		requested, from = javaRelease(override), "the build settings"
	case facts.pinned > 0 && !exact && facts.declared > facts.pinned:
		// No JDK older than the release the build compiles for can build
		// it, whatever a version file says; the build files decide.
		requested, from = facts.declared, facts.declaredFrom+", over "+facts.pinnedFrom+"'s Java "+strconv.Itoa(facts.pinned)
	case facts.pinned > 0:
		requested, from = facts.pinned, facts.pinnedFrom
	case facts.declared > 0:
		requested, from = facts.declared, facts.declaredFrom
	}
	if requested == 0 {
		requested, from = 21, "the recipe's default"
		if facts.wrapperUsable && !gradleRunsOn(facts.wrapper, requested) {
			for _, release := range []int{17, 11, 8} {
				if gradleRunsOn(facts.wrapper, release) {
					requested, from = release, "the newest release Gradle "+facts.wrapper+" runs on"
					break
				}
			}
		}
	}
	if requested < 8 {
		return plan, javaVersionRefusal("java_version_unsupported", fmt.Sprintf("the Java recipe builds on Java 8, 11, 17, 21 or 25; %s asks for Java %d — use a Dockerfile for older releases", from, requested))
	}
	target := requested
	if exact && facts.declared > target {
		target = facts.declared
	}
	if !exact && facts.declared > requested {
		return plan, javaVersionRefusal("java_version_unsupported", fmt.Sprintf("the build compiles for Java %d (%s), and Java %d from %s cannot build it; choose Java %d or newer", facts.declared, facts.declaredFrom, requested, from, facts.declared))
	}
	release := 0
	for _, candidate := range javaRecipeReleases {
		if candidate >= target {
			release = candidate
			break
		}
	}
	if release == 0 {
		return plan, javaVersionRefusal("java_version_unsupported", fmt.Sprintf("the Java recipe builds on Java 8, 11, 17, 21 or 25; %s asks for Java %d — use a Dockerfile for newer releases", from, target))
	}
	if release != target {
		plan.mapped = fmt.Sprintf("Java %d (%s) is not a release the recipe carries; it builds and runs on Java %d, which runs Java %d bytecode", target, from, release, target)
	}
	plan.release, plan.from = release, from
	runner := release
	if facts.tool == "gradle" {
		gradle := facts.wrapper
		if !facts.wrapperUsable {
			// The image's own Gradle: 8.14 carries Java 8 to 21, 9 needs 17
			// and is the one that runs on 25. A wrapper whose jar is missing
			// still says which major the build was written for.
			plan.gradleImage = "8"
			if strings.HasPrefix(facts.wrapper, "9.") || runner >= 25 {
				plan.gradleImage = "9"
			}
			gradle = map[string]string{"8": "8.14", "9": "9.1"}[plan.gradleImage]
		}
		if !gradleRunsOn(gradle, runner) {
			supported := []int{}
			for _, candidate := range javaRecipeReleases {
				if gradleRunsOn(gradle, candidate) {
					supported = append(supported, candidate)
				}
			}
			switch {
			case len(supported) == 0:
				return plan, javaVersionRefusal("gradle_wrapper_incompatible", fmt.Sprintf("Gradle %s runs on none of the recipe's Java releases; upgrade the wrapper (./gradlew wrapper --gradle-version %s)", gradle, gradleMinimumFor(release)))
			case exact:
				runner = supported[len(supported)-1]
			case runner < supported[0]:
				// A newer JDK compiles for an older release, and the JRE
				// that runs the result stays the declared one.
				runner = supported[0]
			default:
				return plan, javaVersionRefusal("gradle_wrapper_incompatible", fmt.Sprintf("Gradle %s cannot run on Java %d, which the build needs (%s); upgrade the wrapper to %s or newer (./gradlew wrapper --gradle-version %s) or choose an older Java release", gradle, release, from, gradleMinimumFor(release), gradleMinimumFor(release)))
			}
		}
	}
	if exact && runner != facts.declared {
		switch {
		case slices.Contains(javaRecipeReleases, facts.declared):
			plan.provide = facts.declared
		case facts.foojay:
			plan.notes = append(plan.notes, fmt.Sprintf("the Gradle toolchain Java %d is downloaded by the foojay toolchain resolver the settings apply", facts.declared))
		default:
			return plan, javaVersionRefusal("java_version_unsupported", fmt.Sprintf("the Gradle toolchain asks for exactly Java %d (%s), which the recipe does not carry; declare Java 8, 11, 17, 21 or 25, or apply the org.gradle.toolchains.foojay-resolver-convention plugin in settings so Gradle downloads it", facts.declared, facts.declaredFrom))
		}
	}
	plan.runner = runner
	return plan, nil
}

// javaToolchainPlan is planJavaToolchain's decision.
type javaToolchainPlan struct {
	tool string
	// release is what the runtime JRE runs, and from where it came.
	release int
	from    string
	// runner is the JDK the build tool runs on, and provide a toolchain JDK
	// copied beside it (0 for none).
	runner  int
	provide int
	// gradleImage is the Gradle image's major when the build has no usable
	// wrapper ("" with one).
	gradleImage string
	mapped      string
	notes       []string
}

// buildImage, runtimeImage and provideImage are the catalogue images the
// plan builds with: Temurin's Ubuntu images, published for amd64 and arm64
// for every release (the Alpine JREs of 8, 11 and 17 are amd64-only).
func (p javaToolchainPlan) buildImage(wrapper bool) string {
	switch {
	case p.tool == "maven":
		return "maven:3-eclipse-temurin-" + strconv.Itoa(p.runner)
	case wrapper:
		return "eclipse-temurin:" + strconv.Itoa(p.runner) + "-jdk"
	}
	return "gradle:" + p.gradleImage + "-jdk" + strconv.Itoa(p.runner)
}

func (p javaToolchainPlan) runtimeImage() string {
	return "eclipse-temurin:" + strconv.Itoa(p.release) + "-jre"
}

func (p javaToolchainPlan) provideImage() string {
	if p.provide == 0 {
		return ""
	}
	return "eclipse-temurin:" + strconv.Itoa(p.provide) + "-jdk"
}

// toolchainVersionError is a JDK or .NET release the recipe cannot build
// with, which is an operator's setting rather than something wrong with the
// source: detection leaves it to preflight, which judges the plan's own
// release (preflight_compiled.go).
type toolchainVersionError struct {
	code, text string
}

func (e toolchainVersionError) Error() string { return ErrUnsupportedBuilder.Error() + ": " + e.text }
func (e toolchainVersionError) Unwrap() error { return ErrUnsupportedBuilder }

func javaVersionRefusal(code, text string) error {
	return toolchainVersionError{code: code, text: text}
}
