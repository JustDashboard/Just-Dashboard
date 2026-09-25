package deploy

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// A Gradle build is read from its scripts as text: settings.gradle(.kts)
// names the build's projects, the version catalog (gradle/libs.versions.toml)
// gives the aliases scripts use their real coordinates and plugin ids, and
// each project's script says which plugins it applies. A subproject cannot be
// built alone — its settings, catalog and sibling projects are outside its
// directory — so the recipe builds it from the settings root by its project
// path. Nothing in a script is evaluated; a script that computes what it
// applies yields fewer facts, never a wrong one.

// gradleSettings is a settings file: its directory and the directory each
// included project lives in, by project path.
type gradleSettings struct {
	dir      string
	file     string
	text     string
	projects map[string]string
	// foojay says the settings apply the toolchain resolver, which lets
	// Gradle download a toolchain JDK the image does not carry.
	foojay bool
	// includedBuilds are directories of builds the settings include, where
	// convention plugins usually live.
	includedBuilds []string
}

var (
	gradleIncludeRE      = regexp.MustCompile(`\binclude\s*\(?\s*((?:["'][^"'\n]+["']\s*,?\s*)+)\)?`)
	gradleQuotedRE       = regexp.MustCompile(`["']([^"'\n]+)["']`)
	gradleProjectDirRE   = regexp.MustCompile(`project\(\s*["'](:[^"'\n]+)["']\s*\)\.projectDir\s*=\s*(?:file|new\s+File)\(\s*(?:(?:settingsDir|rootDir)\s*,\s*)?["'](?:\$\{?(?:rootDir|settingsDir)\}?/)?([^"'\n$]+)["']`)
	gradleIncludeBuildRE = regexp.MustCompile(`\bincludeBuild\s*\(?\s*["']([^"'\n]+)["']`)
	gradleWrapperURLRE   = regexp.MustCompile(`gradle-([0-9]+(?:\.[0-9]+)*)(?:-[0-9A-Za-z.-]+)?-(?:bin|all)\.zip`)
)

// stripGradleComments removes // and /* */ comments outside strings, so a
// commented-out plugin or dependency is not read as applied.
func stripGradleComments(text string) string {
	var out strings.Builder
	out.Grow(len(text))
	quote := byte(0)
	for index := 0; index < len(text); index++ {
		character := text[index]
		switch {
		case quote != 0:
			out.WriteByte(character)
			if character == '\\' && index+1 < len(text) {
				index++
				out.WriteByte(text[index])
			} else if character == quote || character == '\n' {
				quote = 0
			}
		case character == '"' || character == '\'':
			quote = character
			out.WriteByte(character)
		case character == '/' && index+1 < len(text) && text[index+1] == '/':
			for index < len(text) && text[index] != '\n' {
				index++
			}
			out.WriteByte('\n')
		case character == '/' && index+1 < len(text) && text[index+1] == '*':
			end := strings.Index(text[index+2:], "*/")
			if end < 0 {
				return out.String()
			}
			index += end + 3
			out.WriteByte(' ')
		default:
			out.WriteByte(character)
		}
	}
	return out.String()
}

// gradleBlockREs open the blocks the readers look into, by name.
var gradleBlockREs = func() map[string]*regexp.Regexp {
	patterns := map[string]*regexp.Regexp{}
	for _, name := range []string{"plugins", "pluginManagement", "repositories", "credentials", "publishing", "vaadin", "maven", "subprojects", "allprojects"} {
		patterns[name] = regexp.MustCompile(`(?:^|[^\w.])` + name + `\s*(?:\([^)]*\))?\s*\{`)
	}
	return patterns
}()

// gradleBlocks returns the bodies of every `name {` block in a script,
// matched by braces outside strings.
func gradleBlocks(text, name string) []string {
	var blocks []string
	for _, loc := range gradleBlockREs[name].FindAllStringIndex(text, 64) {
		if body, ok := gradleBlockBody(text, loc[1]); ok {
			blocks = append(blocks, body)
		}
	}
	return blocks
}

// gradleBlockBody is the text from start to the brace that closes the block
// opened just before it.
func gradleBlockBody(text string, start int) (string, bool) {
	depth, quote := 1, byte(0)
	for index := start; index < len(text); index++ {
		character := text[index]
		switch {
		case quote != 0:
			if character == '\\' {
				index++
			} else if character == quote || character == '\n' {
				quote = 0
			}
		case character == '"' || character == '\'':
			quote = character
		case character == '{':
			depth++
		case character == '}':
			depth--
			if depth == 0 {
				return text[start:index], true
			}
		}
	}
	return "", false
}

var gradleProjectBlockRE = regexp.MustCompile(`(?:^|[^\w.])project\(\s*["'](:[^"'\n]*)["']\s*\)\s*\{`)

// gradleProjectBlocks are the root script's project(':x') { … } blocks, the
// configuration it gives one project, by project path.
func gradleProjectBlocks(text string) map[string][]string {
	blocks := map[string][]string{}
	for _, match := range gradleProjectBlockRE.FindAllStringSubmatchIndex(text, 64) {
		if body, ok := gradleBlockBody(text, match[1]); ok {
			path := text[match[2]:match[3]]
			blocks[path] = append(blocks[path], body)
		}
	}
	return blocks
}

// withoutGradleBlocks blanks every `name {` block's body, for reading what
// a script declares outside it.
func withoutGradleBlocks(text, name string) string {
	for _, block := range gradleBlocks(text, name) {
		text = strings.Replace(text, block, strings.Repeat(" ", len(block)), 1)
	}
	return text
}

func parseGradleSettings(dir, file, content string) *gradleSettings {
	settings := &gradleSettings{dir: dir, file: file, text: content, projects: map[string]string{}}
	text := stripGradleComments(content)
	settings.foojay = strings.Contains(text, "org.gradle.toolchains.foojay-resolver")
	for _, match := range gradleIncludeRE.FindAllStringSubmatch(text, 512) {
		for _, quoted := range gradleQuotedRE.FindAllStringSubmatch(match[1], 64) {
			projectPath := strings.TrimSpace(quoted[1])
			if !strings.HasPrefix(projectPath, ":") {
				projectPath = ":" + projectPath
			}
			relative := strings.ReplaceAll(strings.TrimPrefix(projectPath, ":"), ":", "/")
			if projectDir, ok := joinBuildPath(dir, relative); ok && len(settings.projects) < 256 {
				settings.projects[projectPath] = projectDir
			}
		}
	}
	for _, match := range gradleProjectDirRE.FindAllStringSubmatch(text, 256) {
		if _, included := settings.projects[match[1]]; !included {
			continue
		}
		if projectDir, ok := joinBuildPath(dir, match[2]); ok {
			settings.projects[match[1]] = projectDir
		}
	}
	for _, match := range gradleIncludeBuildRE.FindAllStringSubmatch(text, 16) {
		if included, ok := joinBuildPath(dir, match[1]); ok {
			settings.includedBuilds = append(settings.includedBuilds, included)
		}
	}
	return settings
}

// projectPath is the path the settings give a directory, "" for one they do
// not include.
func (s *gradleSettings) projectPath(dir string) string {
	if dir == s.dir {
		return ":"
	}
	paths := make([]string, 0, len(s.projects))
	for projectPath := range s.projects {
		paths = append(paths, projectPath)
	}
	sort.Strings(paths)
	for _, projectPath := range paths {
		if s.projects[projectPath] == dir {
			return projectPath
		}
	}
	return ""
}

// gradleCatalog is gradle/libs.versions.toml: aliases as scripts spell
// their accessors (dots between words), to coordinates and plugin ids.
type gradleCatalog struct {
	libraries map[string]string
	plugins   map[string]string
	versions  map[string]string
	bundles   map[string][]string
}

func gradleAccessor(key string) string {
	return strings.NewReplacer("-", ".", "_", ".").Replace(key)
}

func parseGradleCatalog(content []byte) *gradleCatalog {
	catalog := &gradleCatalog{libraries: map[string]string{}, plugins: map[string]string{}, versions: map[string]string{}, bundles: map[string][]string{}}
	type parts struct{ module, group, name, id string }
	libraries, plugins := map[string]*parts{}, map[string]*parts{}
	for _, entry := range readTOML(content) {
		alias, field, _ := strings.Cut(entry.key, ".")
		switch entry.table {
		case "versions":
			catalog.versions[gradleAccessor(entry.key)] = entry.value.text
		case "libraries":
			part := libraries[alias]
			if part == nil {
				part = &parts{}
				libraries[alias] = part
			}
			switch field {
			case "":
				part.module = entry.value.text
			case "module":
				part.module = entry.value.text
			case "group":
				part.group = entry.value.text
			case "name":
				part.name = entry.value.text
			}
		case "plugins":
			part := plugins[alias]
			if part == nil {
				part = &parts{}
				plugins[alias] = part
			}
			switch field {
			case "":
				part.id, _, _ = strings.Cut(entry.value.text, ":")
			case "id":
				part.id = entry.value.text
			}
		case "bundles":
			if entry.value.isList {
				catalog.bundles[gradleAccessor(entry.key)] = entry.value.list
			}
		}
	}
	for alias, part := range libraries {
		module := part.module
		if module == "" && part.group != "" && part.name != "" {
			module = part.group + ":" + part.name
		}
		if module != "" {
			catalog.libraries[gradleAccessor(alias)] = module
		}
	}
	for alias, part := range plugins {
		if part.id != "" {
			catalog.plugins[gradleAccessor(alias)] = part.id
		}
	}
	return catalog
}

var (
	gradleCatalogRefRE    = regexp.MustCompile(`\blibs\.((?:plugins|bundles|versions)\.)?([A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+)*)`)
	gradleAccessorSuffixe = regexp.MustCompile(`\.(?:get|asProvider|toInt|orNull)$`)
)

// resolve lists what the catalog aliases a script uses stand for, as the
// coordinates and plugin ids a build definition would otherwise spell.
func (c *gradleCatalog) resolve(script string) []string {
	if c == nil {
		return nil
	}
	seen := map[string]bool{}
	var resolved []string
	add := func(value string) {
		if value != "" && !seen[value] && len(resolved) < 256 {
			seen[value] = true
			resolved = append(resolved, value)
		}
	}
	for _, match := range gradleCatalogRefRE.FindAllStringSubmatch(script, 1024) {
		accessor := match[2]
		for gradleAccessorSuffixe.MatchString(accessor) {
			accessor = gradleAccessorSuffixe.ReplaceAllString(accessor, "")
		}
		switch match[1] {
		case "plugins.":
			add(c.plugins[accessor])
		case "bundles.":
			for _, alias := range c.bundles[accessor] {
				add(c.libraries[gradleAccessor(alias)])
			}
		case "":
			add(c.libraries[accessor])
		}
	}
	return resolved
}

// gradlePluginIDs are the plugins a script applies: its plugins {} block
// — ids, kotlin("…") shorthands, catalog aliases and core plugins by name —
// and `apply plugin:` lines. A plugin declared `apply false` is only put on
// the classpath for another project, and is not applied here.
func gradlePluginIDs(script string, catalog *gradleCatalog) []string {
	// What a root script applies to its subprojects, or to one project by
	// path, is not applied to the root project itself.
	text := withoutGradleBlocks(stripGradleComments(script), "subprojects")
	for _, blocks := range gradleProjectBlocks(text) {
		for _, block := range blocks {
			text = strings.Replace(text, block, strings.Repeat(" ", len(block)), 1)
		}
	}
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] && len(ids) < 128 {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, block := range gradleBlocks(withoutGradleBlocks(text, "pluginManagement"), "plugins") {
		for _, statement := range strings.FieldsFunc(block, func(r rune) bool { return r == '\n' || r == ';' }) {
			statement = strings.TrimSpace(statement)
			if statement == "" || strings.Contains(statement, "apply false") || strings.Contains(statement, "apply(false)") {
				continue
			}
			switch {
			case strings.HasPrefix(statement, "id"):
				if match := gradleQuotedRE.FindStringSubmatch(statement); match != nil {
					add(match[1])
				}
			case strings.HasPrefix(statement, "kotlin("):
				if match := gradleQuotedRE.FindStringSubmatch(statement); match != nil {
					add("org.jetbrains.kotlin." + match[1])
				}
			case strings.HasPrefix(statement, "alias("):
				if catalog != nil {
					for _, id := range catalog.resolve(statement) {
						add(id)
					}
				}
			default:
				name := strings.Trim(strings.Fields(statement)[0], "`")
				if gradleCorePluginRE.MatchString(name) {
					add(name)
				}
			}
		}
	}
	for _, match := range gradleApplyPluginRE.FindAllStringSubmatch(text, 64) {
		add(match[1])
	}
	return ids
}

var (
	gradleCorePluginRE  = regexp.MustCompile(`^(?:java|java-library|application|war|groovy|scala|java-platform|distribution)$`)
	gradleApplyPluginRE = regexp.MustCompile(`apply\s*\(?\s*plugin\s*(?::|=)\s*["']([\w.-]+)["']`)
)

// gradleInheritedBlocks is what a root script's subprojects {} and
// allprojects {} blocks configure for every project of the build, and its
// project(':x') {} block for this one.
func gradleInheritedBlocks(rootScript, projectPath string) string {
	text := stripGradleComments(rootScript)
	blocks := append(gradleBlocks(text, "subprojects"), gradleBlocks(text, "allprojects")...)
	return strings.Join(append(blocks, gradleProjectBlocks(text)[projectPath]...), "\n")
}

// gradleWrapperVersion is the Gradle release gradle-wrapper.properties
// downloads, or "".
func gradleWrapperVersion(content []byte) string {
	for _, line := range strings.Split(string(content), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found || strings.TrimSpace(key) != "distributionUrl" {
			continue
		}
		if match := gradleWrapperURLRE.FindStringSubmatch(value); match != nil {
			return match[1]
		}
	}
	return ""
}

// gradleVersionAtLeast compares a Gradle release with major.minor.
func gradleVersionAtLeast(version string, major, minor int) bool {
	parts := strings.Split(version, ".")
	have, _ := strconv.Atoi(parts[0])
	haveMinor := 0
	if len(parts) > 1 {
		haveMinor, _ = strconv.Atoi(parts[1])
	}
	return have > major || (have == major && haveMinor >= minor)
}

// gradleRunsOn says whether a Gradle release runs on a JDK, from Gradle's
// compatibility matrix: Java 17 needs 7.3, 21 needs 8.5 and 25 needs 9.1,
// and Gradle 9 needs Java 17 or newer to run at all.
func gradleRunsOn(version string, jdk int) bool {
	if version == "" {
		return true
	}
	switch {
	case jdk <= 11:
		return !gradleVersionAtLeast(version, 9, 0) && (jdk < 11 || gradleVersionAtLeast(version, 5, 0))
	case jdk <= 17:
		return gradleVersionAtLeast(version, 7, 3)
	case jdk <= 21:
		return gradleVersionAtLeast(version, 8, 5)
	case jdk <= 24:
		return gradleVersionAtLeast(version, 8, 14)
	case jdk == 25:
		return gradleVersionAtLeast(version, 9, 1)
	}
	return false
}

// gradleMinimumFor names the Gradle release a JDK needs, for an upgrade
// instruction.
func gradleMinimumFor(jdk int) string {
	switch {
	case jdk <= 17:
		return "7.3"
	case jdk <= 21:
		return "8.5"
	case jdk <= 24:
		return "8.14"
	}
	return "9.1"
}

var (
	gradleToolchainRE      = regexp.MustCompile(`(?:JavaLanguageVersion\.of|jvmToolchain)\(\s*["']?([0-9]{1,2})["']?\s*\)`)
	gradleToolchainCatalog = regexp.MustCompile(`(?:JavaLanguageVersion\.of|jvmToolchain)\(\s*libs\.versions\.([A-Za-z0-9_.]+?)(?:\.get\(\))?(?:\.(?:toInt|toString)\(\))?\s*\)`)
	gradleCompatibilityRE  = regexp.MustCompile(`(?:(?:source|target)Compatibility|release)\s*(?:=|\.set\()\s*(?:JavaVersion\.VERSION_(?:1_)?([0-9]{1,2})|JavaVersion\.toVersion\(\s*["']?(?:1\.)?([0-9]{1,2})["']?\s*\)|["']?(?:1\.)?([0-9]{1,2})\b)`)
	gradleJvmTargetRE      = regexp.MustCompile(`jvmTarget\s*(?:=|\.set\()\s*(?:JvmTarget\.JVM_(?:1_)?([0-9]{1,2})|["'](?:1\.)?([0-9]{1,2})["'])`)
)

// gradleJavaRelease reads the release a script builds for. A toolchain is an
// exact JDK Gradle compiles with; compatibility settings only say which
// bytecode level a newer JDK emits.
func gradleJavaRelease(script string, catalog *gradleCatalog) (release int, toolchain bool) {
	text := stripGradleComments(script)
	if match := gradleToolchainRE.FindStringSubmatch(text); match != nil {
		release, _ = strconv.Atoi(match[1])
		return release, true
	}
	if match := gradleToolchainCatalog.FindStringSubmatch(text); match != nil && catalog != nil {
		if value := javaRelease(catalog.versions[match[1]]); value > 0 {
			return value, true
		}
	}
	for _, pattern := range []*regexp.Regexp{gradleCompatibilityRE, gradleJvmTargetRE} {
		for _, match := range pattern.FindAllStringSubmatch(text, 16) {
			for _, group := range match[1:] {
				if value, err := strconv.Atoi(group); err == nil && value > release && value >= 5 && value <= 40 {
					release = value
				}
			}
		}
	}
	return release, false
}

// gradleProjectRefs are the projects a script depends on: project(":x")
// and the typesafe projects.x accessors, which spell kebab-case names in
// camelCase.
func gradleProjectRefs(script string, settings *gradleSettings) []string {
	text := stripGradleComments(script)
	byAccessor := map[string]string{}
	for projectPath := range settings.projects {
		accessor := strings.ToLower(strings.NewReplacer("-", "", "_", "", ":", ".").Replace(strings.TrimPrefix(projectPath, ":")))
		byAccessor[accessor] = projectPath
	}
	seen := map[string]bool{}
	var refs []string
	for _, match := range gradleProjectRefRE.FindAllStringSubmatch(text, 128) {
		if _, ok := settings.projects[match[1]]; ok && !seen[match[1]] {
			seen[match[1]] = true
			refs = append(refs, match[1])
		}
	}
	for _, match := range gradleTypesafeRefRE.FindAllStringSubmatch(text, 128) {
		accessor := strings.ToLower(match[1])
		for accessor != "" {
			if projectPath, ok := byAccessor[accessor]; ok {
				if !seen[projectPath] {
					seen[projectPath] = true
					refs = append(refs, projectPath)
				}
				break
			}
			cut := strings.LastIndex(accessor, ".")
			if cut < 0 {
				break
			}
			accessor = accessor[:cut]
		}
	}
	sort.Strings(refs)
	return refs
}

var (
	gradleProjectRefRE  = regexp.MustCompile(`project\(\s*(?:path\s*[=:]\s*)?["'](:[^"'\n]+)["']`)
	gradleTypesafeRefRE = regexp.MustCompile(`\bprojects\.([A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+)*)`)
)

// gradleConventionScripts finds the precompiled script plugins a build keeps
// in buildSrc, build-logic or an included build, by plugin id.
func (r *jvmReader) gradleConventionScripts(settings *gradleSettings) map[string]string {
	if settings == nil {
		return nil
	}
	if cached, ok := r.conventions[settings.dir]; ok {
		return cached
	}
	scripts := map[string]string{}
	roots := []string{joinRootDir(settings.dir, "buildSrc"), joinRootDir(settings.dir, "build-logic")}
	roots = append(roots, settings.includedBuilds...)
	var sources []string
	for _, root := range roots {
		if !r.files.directory(root) {
			continue
		}
		sources = append(sources, root)
		for _, name := range r.files.names(root) {
			if r.files.directory(joinRootDir(root, name)) && len(sources) < 16 {
				sources = append(sources, joinRootDir(root, name))
			}
		}
	}
	for _, source := range sources {
		for _, language := range []string{"kotlin", "groovy"} {
			dir := joinRootDir(source, "src/main/"+language)
			for _, name := range r.files.names(dir) {
				id := strings.TrimSuffix(strings.TrimSuffix(name, ".kts"), ".gradle")
				if id == name || len(scripts) >= 32 {
					continue
				}
				if content, ok := r.files.read(joinRootDir(dir, name), 128<<10); ok {
					scripts[id] = string(content)
				}
			}
		}
	}
	r.conventions[settings.dir] = scripts
	return scripts
}

// gradleProjectScript is a project directory's build script and its path.
func (r *jvmReader) gradleProjectScript(dir string) (string, string) {
	for _, name := range []string{"build.gradle.kts", "build.gradle"} {
		file := joinRootDir(dir, name)
		if content, ok := r.files.read(file, 512<<10); ok {
			return string(content), file
		}
	}
	return "", ""
}

// gradleSettingsFor finds the settings file that governs dir: the nearest
// one at or above it, as Gradle's own search does.
func (r *jvmReader) gradleSettingsFor(dir string) *gradleSettings {
	for _, ancestor := range ancestorDirs(dir) {
		if cached, ok := r.settings[ancestor]; ok {
			if cached != nil {
				return cached
			}
			continue
		}
		var found *gradleSettings
		for _, name := range []string{"settings.gradle.kts", "settings.gradle"} {
			file := joinRootDir(ancestor, name)
			if content, ok := r.files.read(file, 128<<10); ok {
				found = parseGradleSettings(ancestor, file, string(content))
				break
			}
		}
		r.settings[ancestor] = found
		if found != nil {
			return found
		}
	}
	return nil
}

// gradleCatalogFor is the settings root's default version catalog.
func (r *jvmReader) gradleCatalogFor(dir string) *gradleCatalog {
	if cached, ok := r.catalogs[dir]; ok {
		return cached
	}
	var catalog *gradleCatalog
	if content, ok := r.files.read(joinRootDir(dir, "gradle/libs.versions.toml"), 128<<10); ok {
		catalog = parseGradleCatalog(content)
	}
	r.catalogs[dir] = catalog
	return catalog
}

// gradleApplied expands a script's applied plugins through the convention
// scripts that define them, returning every plugin id and the text of each
// convention script it pulled in.
func gradleApplied(script string, catalog *gradleCatalog, conventions map[string]string) ([]string, []string) {
	var ids, texts []string
	seen := map[string]bool{}
	pending := gradlePluginIDs(script, catalog)
	for len(pending) > 0 && len(ids) < 128 {
		id := pending[0]
		pending = pending[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
		if convention, ok := conventions[id]; ok {
			texts = append(texts, convention)
			pending = append(pending, gradlePluginIDs(convention, catalog)...)
		}
	}
	return ids, texts
}

func pluginApplied(ids []string, names ...string) bool {
	for _, id := range ids {
		for _, name := range names {
			if id == name {
				return true
			}
		}
	}
	return false
}

// gradlePackaging decides how a project's runnable artifact is produced
// from the plugins it applies.
func gradlePackaging(ids []string, text string) (string, string) {
	shadow := pluginApplied(ids, "com.github.johnrengelman.shadow", "com.gradleup.shadow", "io.github.goooler.shadow")
	switch {
	case pluginApplied(ids, "org.springframework.boot") && pluginApplied(ids, "war"):
		return javaPackagingSpringBootWar, "the Spring Boot plugin's bootWar"
	case pluginApplied(ids, "org.springframework.boot"):
		return javaPackagingSpringBoot, "the Spring Boot plugin's bootJar"
	case pluginApplied(ids, "io.quarkus"):
		return javaPackagingQuarkus, "the Quarkus plugin's quarkusBuild"
	case pluginApplied(ids, "io.ktor.plugin"):
		return javaPackagingKtor, "the Ktor plugin's buildFatJar"
	case shadow:
		return javaPackagingShadow, "the Shadow plugin's shadowJar"
	case pluginApplied(ids, "application", "org.gradle.application", "io.micronaut.application"):
		return javaPackagingApplication, "the application plugin's installDist"
	case strings.Contains(text, "Main-Class"):
		return javaPackagingJar, "a Main-Class in the jar's manifest"
	case pluginApplied(ids, "war"):
		return javaPackagingWar, ""
	}
	return "", ""
}

// gradleAndroid says a project applies the Android Gradle plugin, which
// Gradle cannot even configure without the Android SDK.
func gradleAndroid(ids []string) bool {
	for _, id := range ids {
		if strings.HasPrefix(id, "com.android.") {
			return true
		}
	}
	return false
}
