package deploy

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// A .NET project is read the way MSBuild and the dotnet CLI would read it,
// as data: its project file (C#, F# or Visual Basic) with the properties
// Directory.Build.props sets for it, the projects it references, and the
// files that decide how it restores and which SDK builds it —
// Directory.Packages.props, NuGet.config, global.json. A web project in a
// solution's src/ references siblings and inherits shared props from above
// its directory, so the recipe builds it from the directory that holds all
// of them (build_dotnet.go).

// dotnetProjectExtensions are the project files the .NET recipe builds.
var dotnetProjectExtensions = []string{".csproj", ".fsproj", ".vbproj"}

func dotnetProjectFile(name string) bool {
	for _, extension := range dotnetProjectExtensions {
		if strings.HasSuffix(strings.ToLower(name), extension) {
			return true
		}
	}
	return false
}

// msbuildDocument is what a project or props file declares: its SDKs,
// properties in document order, items by type, and the commands its
// targets run.
type msbuildDocument struct {
	sdks       []string
	properties [][2]string
	items      map[string][]string
	commands   []string
	// importsAbove says the file imports the next Directory.Build.props up,
	// which MSBuild otherwise stops at.
	importsAbove bool
}

func parseMSBuild(content []byte) msbuildDocument {
	document := msbuildDocument{items: map[string][]string{}}
	decoder := xml.NewDecoder(bytes.NewReader(content))
	decoder.Strict = false
	decoder.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
	var stack []string
	var text strings.Builder
	for tokens := 0; tokens < 200_000; tokens++ {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		switch element := token.(type) {
		case xml.StartElement:
			name := element.Name.Local
			parent := ""
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			attribute := func(key string) string {
				for _, attr := range element.Attr {
					if strings.EqualFold(attr.Name.Local, key) {
						return strings.TrimSpace(attr.Value)
					}
				}
				return ""
			}
			switch {
			case name == "Project" && len(stack) == 0:
				for _, sdk := range strings.Split(attribute("Sdk"), ";") {
					if sdk = strings.TrimSpace(sdk); sdk != "" {
						document.sdks = append(document.sdks, dotnetSdkName(sdk))
					}
				}
			case name == "Sdk" && parent == "Project":
				if sdk := attribute("Name"); sdk != "" {
					document.sdks = append(document.sdks, dotnetSdkName(sdk))
				}
			case name == "Import":
				if sdk := attribute("Sdk"); sdk != "" {
					document.sdks = append(document.sdks, dotnetSdkName(sdk))
				}
				if strings.Contains(attribute("Project"), "GetPathOfFileAbove") {
					document.importsAbove = true
				}
			case parent == "ItemGroup":
				include := attribute("Include")
				if include != "" && len(document.items[name]) < 256 {
					document.items[name] = append(document.items[name], include)
				}
			case name == "Exec":
				if command := attribute("Command"); command != "" && len(document.commands) < 64 {
					document.commands = append(document.commands, command)
				}
			}
			stack = append(stack, name)
			text.Reset()
		case xml.CharData:
			if text.Len() < 4096 {
				text.Write(element)
			}
		case xml.EndElement:
			if len(stack) >= 2 && stack[len(stack)-2] == "PropertyGroup" && len(document.properties) < 1024 {
				document.properties = append(document.properties, [2]string{element.Name.Local, strings.TrimSpace(text.String())})
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			text.Reset()
		}
	}
	return document
}

// dotnetSdkName drops an SDK reference's version: Aspire.AppHost.Sdk/9.0.0.
func dotnetSdkName(sdk string) string {
	name, _, _ := strings.Cut(strings.TrimSpace(sdk), "/")
	return name
}

// Project kinds, which decide the runtime image and whether a project is
// offered at all.
const (
	dotnetKindWeb         = "web"
	dotnetKindWorker      = "worker"
	dotnetKindExe         = "exe"
	dotnetKindLibrary     = "library"
	dotnetKindTest        = "test"
	dotnetKindAppHost     = "apphost"
	dotnetKindBlazorWasm  = "blazor-wasm"
	dotnetPublishDisabled = "-p:PublishAot=false -p:PublishSingleFile=false -p:PublishReadyToRun=false -p:SelfContained=false -p:PublishTrimmed=false"
)

// dotnetProjectFacts is one project file as the recipe builds it.
type dotnetProjectFacts struct {
	// file is the project's checkout path, dir its directory and name the
	// file name.
	file, dir, name string
	kind            string
	// targets are the supported releases its target frameworks name
	// ("8.0", "10.0"); targetText is what it declares, for messages.
	targets     []string
	targetText  string
	multiTarget bool
	assembly    string
	// native lists the publish settings that make publish write a native or
	// single-file executable instead of the dll the recipe runs.
	native     []string
	packages   []string
	references []string
	// spaRoot is a single-page application the project's publish builds
	// with npm (SpaRoot, an Exec running a package manager, or a referenced
	// JavaScript project), as a checkout directory.
	spaRoot string
	props   []string
}

// dotnetReader reads the projects of a checkout, caching every file.
type dotnetReader struct {
	files    *buildFiles
	projects map[string]*dotnetProjectFacts
}

func newDotnetReader(files *buildFiles) *dotnetReader {
	return &dotnetReader{files: files, projects: map[string]*dotnetProjectFacts{}}
}

// directoryProps is the Directory.Build.props chain that applies to a
// directory, nearest first: MSBuild imports the nearest one, and it imports
// the next one up only when it says so.
func (r *dotnetReader) directoryProps(dir string) []string {
	var chain []string
	for _, ancestor := range ancestorDirs(dir) {
		file := joinRootDir(ancestor, "Directory.Build.props")
		if !r.files.regular(file) {
			continue
		}
		chain = append(chain, file)
		content, _ := r.files.read(file, 256<<10)
		if !parseMSBuild(content).importsAbove || len(chain) >= 8 {
			break
		}
	}
	return chain
}

// nearestFile is the first of names found at or above dir.
func (r *dotnetReader) nearestFile(dir string, names ...string) string {
	for _, ancestor := range ancestorDirs(dir) {
		for _, name := range names {
			if file := joinRootDir(ancestor, name); r.files.regular(file) {
				return file
			}
		}
	}
	return ""
}

// nugetConfigNames are the spellings NuGet looks for, case-sensitively.
var nugetConfigNames = []string{"NuGet.Config", "nuget.config", "NuGet.config"}

// nugetConfigs are every NuGet.config at or above dir: NuGet merges them all.
func (r *dotnetReader) nugetConfigs(dir string) []string {
	var files []string
	for _, ancestor := range ancestorDirs(dir) {
		for _, name := range nugetConfigNames {
			if file := joinRootDir(ancestor, name); r.files.regular(file) {
				files = append(files, file)
			}
		}
	}
	return files
}

// project reads one project file, with the props that apply to it merged
// under its own properties.
func (r *dotnetReader) project(file string) *dotnetProjectFacts {
	file = cleanBuildPath(file)
	if cached, ok := r.projects[file]; ok {
		return cached
	}
	r.projects[file] = nil
	content, ok := r.files.read(file, 512<<10)
	if !ok {
		return nil
	}
	facts := &dotnetProjectFacts{file: file, dir: path.Dir(file), name: path.Base(file)}
	document := parseMSBuild(content)
	properties := map[string]string{"MSBuildProjectName": strings.TrimSuffix(facts.name, path.Ext(facts.name))}
	facts.props = r.directoryProps(facts.dir)
	for index := len(facts.props) - 1; index >= 0; index-- {
		props, _ := r.files.read(facts.props[index], 256<<10)
		for _, property := range parseMSBuild(props).properties {
			properties[property[0]] = property[1]
		}
	}
	for _, property := range document.properties {
		properties[property[0]] = property[1]
	}
	expand := func(value string) string {
		for depth := 0; depth < 4 && strings.Contains(value, "$("); depth++ {
			value = msbuildPropertyRE.ReplaceAllStringFunc(value, func(reference string) string {
				if resolved, ok := properties[reference[2:len(reference)-1]]; ok {
					return resolved
				}
				return reference
			})
		}
		return value
	}
	for _, include := range document.items["PackageReference"] {
		facts.packages = append(facts.packages, strings.ToLower(include))
	}
	for _, include := range document.items["ProjectReference"] {
		if reference, ok := joinBuildPath(facts.dir, expand(include)); ok {
			facts.references = append(facts.references, reference)
		}
	}
	facts.assembly = strings.TrimSuffix(facts.name, path.Ext(facts.name))
	if assembly := expand(properties["AssemblyName"]); assembly != "" && !strings.Contains(assembly, "$(") &&
		safeRelativePath(assembly) && !strings.ContainsAny(assembly, "/\\") {
		facts.assembly = assembly
	}
	facts.kind = dotnetKind(document, properties, facts.packages, expand)
	for _, setting := range []string{"PublishAot", "PublishSingleFile", "PublishReadyToRun", "SelfContained", "PublishTrimmed"} {
		if strings.EqualFold(expand(properties[setting]), "true") {
			facts.native = append(facts.native, setting)
		}
	}
	targets := expand(properties["TargetFramework"])
	if multiple := expand(properties["TargetFrameworks"]); multiple != "" && targets == "" {
		targets, facts.multiTarget = multiple, true
	}
	facts.targetText = targets
	for _, target := range strings.Split(targets, ";") {
		if match := dotnetVersionRE.FindStringSubmatch(strings.TrimSpace(target)); match != nil && slices.Contains(dotnetRecipeVersions, match[1]) &&
			!slices.Contains(facts.targets, match[1]) {
			facts.targets = append(facts.targets, match[1])
		}
	}
	sort.Slice(facts.targets, func(i, j int) bool {
		return slices.Index(dotnetRecipeVersions, facts.targets[i]) < slices.Index(dotnetRecipeVersions, facts.targets[j])
	})
	if !facts.multiTarget && strings.Contains(targets, ";") {
		// <TargetFramework> names one framework; a list there is an error
		// MSBuild itself reports, and planDotnetToolchain names it.
		facts.targets = nil
	}
	if spa := expand(properties["SpaRoot"]); spa != "" {
		if dir, ok := joinBuildPath(facts.dir, spa); ok {
			facts.spaRoot = dir
		}
	}
	for _, reference := range facts.references {
		if strings.HasSuffix(strings.ToLower(reference), ".esproj") && facts.spaRoot == "" {
			facts.spaRoot = path.Dir(reference)
		}
	}
	if facts.spaRoot == "" {
		for _, command := range document.commands {
			if dotnetNodeCommandRE.MatchString(command) && r.files.regular(joinRootDir(facts.dir, "package.json")) {
				facts.spaRoot = facts.dir
				break
			}
		}
	}
	if facts.spaRoot != "" && !r.files.regular(joinRootDir(facts.spaRoot, "package.json")) {
		facts.spaRoot = ""
	}
	r.projects[file] = facts
	return facts
}

var (
	msbuildPropertyRE   = regexp.MustCompile(`\$\(([A-Za-z_][A-Za-z0-9_.-]*)\)`)
	dotnetNodeCommandRE = regexp.MustCompile(`(?:^|[\s;&|])(?:npm|npx|yarn|pnpm)\s`)
)

// dotnetKind classifies a project by its SDK, output type and references.
func dotnetKind(document msbuildDocument, properties map[string]string, packages []string, expand func(string) string) string {
	has := func(sdk string) bool {
		return slices.ContainsFunc(document.sdks, func(name string) bool { return strings.EqualFold(name, sdk) })
	}
	property := func(name string) string { return strings.ToLower(expand(properties[name])) }
	references := func(names ...string) bool {
		for _, name := range names {
			if slices.Contains(packages, name) {
				return true
			}
		}
		return false
	}
	exe := property("OutputType") == "exe" || property("OutputType") == "winexe"
	aspnet := slices.ContainsFunc(document.items["FrameworkReference"], func(name string) bool {
		return strings.EqualFold(name, "Microsoft.AspNetCore.App")
	})
	switch {
	case property("IsTestProject") == "true" || has("MSTest.Sdk") ||
		references("microsoft.net.test.sdk", "xunit", "xunit.v3", "nunit", "mstest.testframework", "tunit"):
		return dotnetKindTest
	case has("Aspire.AppHost.Sdk") || property("IsAspireHost") == "true" || references("aspire.hosting.apphost"):
		return dotnetKindAppHost
	case has("Microsoft.NET.Sdk.BlazorWebAssembly"):
		return dotnetKindBlazorWasm
	case has("Microsoft.NET.Sdk.Web") && property("OutputType") != "library":
		return dotnetKindWeb
	case has("Microsoft.NET.Sdk.Worker"):
		return dotnetKindWorker
	case exe && aspnet:
		return dotnetKindWeb
	case exe:
		return dotnetKindExe
	}
	return dotnetKindLibrary
}

// dotnetBuild is where and how a project builds: the directory its build
// runs from, and the SDK pin that applies there.
type dotnetBuild struct {
	// context holds the project, every project it references, and each
	// props, package-version, NuGet and global.json file that applies.
	context string
	sdk     toolchainPin
	// rollForward is global.json's policy for the pin.
	rollForward string
	nuget       []string
	closure     []*dotnetProjectFacts
}

// build works out the context a project publishes from.
func (r *dotnetReader) build(project *dotnetProjectFacts) dotnetBuild {
	var result dotnetBuild
	dirs := []string{project.dir}
	seen := map[string]bool{project.file: true}
	pending := []*dotnetProjectFacts{project}
	for len(pending) > 0 && len(seen) <= 64 {
		current := pending[0]
		pending = pending[1:]
		result.closure = append(result.closure, current)
		for _, props := range current.props {
			dirs = append(dirs, path.Dir(props))
		}
		for _, name := range []string{"Directory.Build.targets", "Directory.Packages.props"} {
			if file := r.nearestFile(current.dir, name); file != "" {
				dirs = append(dirs, path.Dir(file))
			}
		}
		for _, reference := range current.references {
			if seen[reference] {
				continue
			}
			seen[reference] = true
			dirs = append(dirs, path.Dir(reference))
			if referenced := r.project(reference); referenced != nil {
				pending = append(pending, referenced)
			}
		}
	}
	// The SDK is resolved from the directory dotnet runs in, and the
	// recipe runs it in the project's own; NuGet merges every config at or
	// above the project.
	if file := r.nearestFile(project.dir, "global.json"); file != "" {
		dirs = append(dirs, path.Dir(file))
		result.sdk, result.rollForward = globalJSONPin(r.files, file)
	}
	result.nuget = r.nugetConfigs(project.dir)
	for _, file := range result.nuget {
		dirs = append(dirs, path.Dir(file))
	}
	if project.spaRoot != "" {
		dirs = append(dirs, project.spaRoot)
	}
	result.context = commonDir(dirs)
	if result.sdk.version == "" {
		for _, dir := range ancestorDirs(project.dir) {
			if pin := dotnetVersionPin(r.files, dir); pin.version != "" {
				result.sdk = pin
				break
			}
			if dir == result.context {
				break
			}
		}
	}
	return result
}

// globalJSONPin reads global.json's sdk.version and sdk.rollForward.
func globalJSONPin(files *buildFiles, file string) (toolchainPin, string) {
	content, ok := files.read(file, 64<<10)
	if !ok {
		return toolchainPin{}, ""
	}
	var document struct {
		SDK struct {
			Version     string `json:"version"`
			RollForward string `json:"rollForward"`
		} `json:"sdk"`
	}
	if json.Unmarshal(stripJSONComments(content), &document) != nil {
		return toolchainPin{}, ""
	}
	version := strings.TrimSpace(document.SDK.Version)
	if !dotnetSDKVersionRE.MatchString(version) {
		return toolchainPin{}, ""
	}
	return toolchainPin{version: version, source: file}, strings.TrimSpace(document.SDK.RollForward)
}
