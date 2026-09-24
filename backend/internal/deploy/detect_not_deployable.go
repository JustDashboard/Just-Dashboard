package deploy

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// Not every repository is a service. A published npm library became a worker
// running `node dist/index.js`, which exits at once and restarts forever; a
// VS Code extension crashed on require('vscode'); a Tauri app's frontend was a
// high-confidence static site whose invoke() calls fail in a browser; an
// Android module inside a React Native repository was a Java service that
// cannot build without the Android SDK. Each is still shown — the operator may
// know better — but marked for what it is, never selected on its own, and
// refused by preflight with the reason instead of failing after the build.

// nodePackageShape is the part of package.json that says what a package is
// rather than how it runs.
type nodePackageShape struct {
	Bin              json.RawMessage   `json:"bin"`
	Exports          json.RawMessage   `json:"exports"`
	Types            string            `json:"types"`
	Typings          string            `json:"typings"`
	Files            json.RawMessage   `json:"files"`
	PublishConfig    json.RawMessage   `json:"publishConfig"`
	PeerDependencies map[string]string `json:"peerDependencies"`
	Engines          map[string]any    `json:"engines"`
	Contributes      json.RawMessage   `json:"contributes"`
	Workspaces       json.RawMessage   `json:"workspaces"`
	Main             string            `json:"main"`
	Module           string            `json:"module"`
}

func (p nodePackageShape) libraryFields() int {
	count := 0
	for _, present := range []bool{
		len(p.Exports) > 0, p.Types != "" || p.Typings != "", len(p.Files) > 0, len(p.PublishConfig) > 0,
		len(p.PeerDependencies) > 0, p.Module != "",
	} {
		if present {
			count++
		}
	}
	return count
}

var (
	cargoLibTableRE        = regexp.MustCompile(`(?m)^\s*\[lib\]\s*$`)
	dotnetWindowsOnlyRE    = regexp.MustCompile(`(?i)<TargetFrameworkVersion>\s*v[1-4]\.|<TargetFrameworks?>[^<]*-windows|<Use(WindowsForms|WPF)>\s*true`)
	dotnetLibraryOnlyRE    = regexp.MustCompile(`builds a library, not a program`)
	androidGradleRE        = regexp.MustCompile(`com\.android\.(application|library|tools\.build|dynamic-feature)|plugins\.android\.(application|library)|androidTarget\(`)
	browserManifestRE      = regexp.MustCompile(`"manifest_version"\s*:`)
	pyprojectScriptsRE     = regexp.MustCompile(`(?m)^\s*\[(project\.scripts|tool\.poetry\.scripts)\]\s*$`)
	pyprojectBuildSystemRE = regexp.MustCompile(`(?m)^\s*\[build-system\]\s*$`)
	pythonNotebookServer   = []string{"voila", "jupyter-server", "jupyterlab", "notebook", "panel", "mercury"}
)

func (s *repoShapeScan) applyNotDeployable(result *DetectionResult, context shapeContext) {
	desktopRoots := map[string]string{}
	removeCandidates(result, func(candidate DetectedCandidate) bool {
		marker := context.markers[candidate.Root]
		if candidate.Recipe == "java" && marker != nil && androidGradleRE.Match(append(append([]byte(nil), marker.gradleBuild...), marker.pomXML...)) {
			s.addSetAside(DetectionSetAside{Path: joinRoot(candidate.Root, marker.gradleBuildPath), Kind: "mobile-app",
				Reason: "Android app module, not deployable here: it builds with the Android SDK for a phone"})
			return true
		}
		if hostedFunctionsRoot(candidate.Root) != "" {
			s.addSetAside(DetectionSetAside{Path: rootLabelOf(candidate.Root), Kind: "hosted-functions",
				Reason: hostedFunctionsRoot(candidate.Root) + "; those functions run on that platform, not in a container here"})
			return true
		}
		return false
	})
	for index := range result.Candidates {
		candidate := &result.Candidates[index]
		marker := context.markers[candidate.Root]
		if _, action := s.file(candidate.Root, "action.yml"); action || s.hasFile(candidate.Root, "action.yaml") {
			candidate.NotDeployable = "github-action"
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(candidate.Root, "action.yml"),
				Reason: "action.yml defines a GitHub Action; it runs inside workflows, not as a service"})
			continue
		}
		switch candidate.Recipe {
		case "node":
			s.classifyNodePackage(candidate, marker, desktopRoots)
		case "python":
			s.classifyPythonProject(candidate, marker)
		case "rust":
			s.classifyRustCrate(candidate, marker, desktopRoots)
		case "dotnet":
			if marker != nil {
				names := make([]string, 0, len(marker.csprojs))
				for name := range marker.csprojs {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					if dotnetWindowsOnlyRE.Match(marker.csprojs[name]) {
						candidate.NotDeployable = "windows-only"
						candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(candidate.Root, name),
							Reason: name + " targets Windows (.NET Framework, WinForms or WPF); a Linux container cannot run it"})
						break
					}
				}
			}
			if candidate.NotDeployable == "" && dotnetLibraryOnlyRE.MatchString(strings.Join(candidate.NeedsDecision, " ")) {
				candidate.NotDeployable = "library"
			}
		case "go":
			if s.hasFile(candidate.Root, "wails.json") {
				candidate.NotDeployable = "desktop-app"
				candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(candidate.Root, "wails.json"),
					Reason: "wails.json: a Wails desktop application"})
				desktopRoots[candidate.Root] = "wails"
				continue
			}
			roots := []string{}
			for root, marker := range context.markers {
				if marker.goMod != "" {
					roots = append(roots, root)
				}
			}
			if context.goSources != nil {
				if library, examples := context.goSources.libraryModule(candidate.Root, roots); library {
					candidate.NotDeployable = "library"
					reason := "no main package in the module; it is a library"
					if len(examples) > 0 {
						sort.Strings(examples)
						reason = "main packages only under " + examples[0] + "; the module itself is a library"
					}
					candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(candidate.Root, "go.mod"), Reason: reason})
				}
			}
		}
	}
	// A desktop shell's frontend is a website only in the sense that it is
	// HTML: its calls into the shell have nothing to answer them. It sits at
	// the shell's root (Tauri) or in its frontend folder (Wails).
	shellRoots := make([]string, 0, len(desktopRoots))
	for root := range desktopRoots {
		shellRoots = append(shellRoots, root)
	}
	sort.Strings(shellRoots)
	for index := range result.Candidates {
		candidate := &result.Candidates[index]
		if candidate.NotDeployable != "" || candidate.DesktopShell != "" || candidate.Recipe != "node" {
			continue
		}
		for _, root := range shellRoots {
			if candidate.Root == root || candidate.Root == joinRoot(root, "frontend") || candidate.Root == joinRoot(root, "ui") {
				candidate.DesktopShell = desktopRoots[root]
				candidate.Demotion = "frontend of a " + frameworkDisplayName(desktopRoots[root]) + " desktop application; its calls into the shell need the desktop app"
				break
			}
		}
	}
	if len(result.Candidates) == 0 {
		directories := make([]string, 0, len(s.notebooks))
		for directory := range s.notebooks {
			directories = append(directories, directory)
		}
		sort.Strings(directories)
		for _, directory := range directories {
			s.addSetAside(DetectionSetAside{Path: rootLabelOf(directory), Kind: "notebook",
				Reason: "Jupyter notebooks: they run in a notebook server, not as a service"})
		}
	}
}

func (s *repoShapeScan) hasFile(root, key string) bool {
	_, ok := s.file(root, key)
	return ok
}

func (s *repoShapeScan) classifyNodePackage(candidate *DetectedCandidate, marker *detectedMarkers, desktopRoots map[string]string) {
	if marker == nil || len(marker.packageJSON) == 0 {
		return
	}
	var manifest nodeManifest
	var shape nodePackageShape
	if !parseNodeManifest(marker.packageJSON, &manifest) || json.Unmarshal(manifestText(marker.packageJSON), &shape) != nil {
		return
	}
	packagePath := joinRoot(candidate.Root, "package.json")
	mark := func(kind, reason string) {
		candidate.NotDeployable = kind
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: packagePath, Reason: reason})
	}
	switch {
	case shape.Engines["vscode"] != nil || len(shape.Contributes) > 0:
		mark("editor-extension", "engines.vscode: a Visual Studio Code extension")
		return
	case manifest.has("wxt") || manifest.has("plasmo") || manifest.has("@crxjs/vite-plugin") || s.browserManifest(candidate.Root):
		mark("browser-extension", "a browser extension (manifest_version, WXT, Plasmo or CRXJS)")
		return
	case manifest.has("electron") && (shape.Main != "" || strings.Contains(manifest.Scripts["start"], "electron") ||
		strings.Contains(manifest.Scripts["dev"], "electron")):
		// electron as a devDependency of a web app that also ships a desktop
		// build is not this; a main process entry or an electron start is.
		mark("desktop-app", "electron: an Electron desktop application")
		return
	case (manifest.has("react-native") || manifest.has("expo")) && !manifest.has("react-native-web"):
		mark("mobile-app", "react-native without react-native-web: a mobile application")
		return
	case manifest.has("expo") && manifest.has("react-native-web"):
		s.expoWebExport(candidate, marker, manifest)
		return
	}
	if manifest.has("@tauri-apps/api") || manifest.has("@tauri-apps/cli") || s.hasFile(candidate.Root, "src-tauri/tauri.conf.json") {
		desktopRoots[candidate.Root] = "tauri"
	}
	server := matchNodeServerLibrary(manifest) != "" || manifest.Scripts["start"] != ""
	framework := matchNodeFramework(manifest)
	libraryFramework := framework == nil || framework.Name == "vite" || framework.Name == "parcel"
	switch {
	case !server && libraryFramework && len(shape.Bin) > 0 && shape.libraryFields() == 0 && framework == nil:
		mark("cli", "package.json declares only a bin: a command-line tool, not a server")
	case !server && libraryFramework && (shape.libraryFields() >= 2 || (framework == nil && shape.libraryFields() >= 1 && candidate.Profile != ProfileWeb)):
		mark("library", "package.json publishes a library (exports, types, files, peerDependencies) and starts nothing")
	case !server && framework == nil && len(shape.Workspaces) > 0:
		candidate.Demotion = "workspace root; the applications are its member packages"
	}
}

// browserManifest looks for a WebExtension manifest at a package root or in
// the folders extension templates keep it in.
func (s *repoShapeScan) browserManifest(root string) bool {
	for _, directory := range []string{root, joinRoot(root, "public"), joinRoot(root, "src"), joinRoot(root, "static")} {
		if content, ok := s.file(directory, "manifest.json"); ok && browserManifestRE.Match(content) {
			return true
		}
	}
	return false
}

// expoWebExport builds an Expo app's web target: `expo export --platform web`
// writes a site to dist/, which is what a web deployment of it serves.
func (s *repoShapeScan) expoWebExport(candidate *DetectedCandidate, marker *detectedMarkers, manifest nodeManifest) {
	output := "single"
	if content, ok := s.file(candidate.Root, "app.json"); ok {
		var config struct {
			Expo struct {
				Web struct {
					Output string `json:"output"`
				} `json:"web"`
			} `json:"expo"`
		}
		if json.Unmarshal(content, &config) == nil && config.Expo.Web.Output != "" {
			output = config.Expo.Web.Output
		}
	}
	runner := candidate.PackageManager
	if runner == "" {
		runner = "npm"
	}
	if output == "server" {
		candidate.RecipeIssue = "Expo web output \"server\" needs Expo's own server runtime; set expo.web.output to single or static, or use a Dockerfile"
		return
	}
	candidate.Name = "Expo web app"
	candidate.Framework = "expo"
	candidate.BuildCommand = nodeExecRunner(runner) + " expo export --platform web"
	candidate.StartCommand = ""
	candidate.Profile, candidate.OutputDirectory, candidate.Port = ProfileStatic, "dist", 80
	candidate.SPAFallback = true
	candidate.Confidence = ConfidenceHigh
	candidate.NeedsDecision = removeDecision(candidate.NeedsDecision, "choose a start command or static output")
	candidate.RecipeIssue = ""
	candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(candidate.Root, "package.json"),
		Reason: "expo with react-native-web: expo export --platform web writes the " + output + " web build to dist/"})
}

func removeDecision(decisions []string, remove string) []string {
	kept := decisions[:0]
	for _, decision := range decisions {
		if decision != remove {
			kept = append(kept, decision)
		}
	}
	return kept
}

func (s *repoShapeScan) classifyPythonProject(candidate *DetectedCandidate, marker *detectedMarkers) {
	if marker == nil || candidate.Framework != "python" || candidate.StartCommand != "" {
		return
	}
	deps := readPythonDependencies(marker.pythonFiles)
	for _, server := range pythonNotebookServer {
		if deps.has(server) {
			return
		}
	}
	pyproject := marker.pythonFiles["pyproject.toml"]
	switch {
	case s.notebooksUnder(candidate.Root) > 0 && !s.topLevelPython(candidate.Root):
		candidate.NotDeployable = "notebook"
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: rootLabelOf(candidate.Root),
			Reason: "Jupyter notebooks and no application entry point: they run in a notebook server, not as a service"})
	case pyprojectBuildSystemRE.Match(pyproject) && !pyprojectScriptsRE.Match(pyproject) && !s.topLevelPython(candidate.Root):
		// A build backend with no console script and no script at the root
		// is a package to install, not a program; console scripts are how
		// bots and servers start too, so they stay the operator's question.
		candidate.NotDeployable = "library"
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(candidate.Root, "pyproject.toml"),
			Reason: "pyproject packages a library: a build backend, no web framework and no script to run"})
	}
}

// topLevelPython says whether a root holds a Python script of its own,
// beyond packaging and test configuration.
func (s *repoShapeScan) topLevelPython(root string) bool {
	for file := range s.files {
		if !strings.HasSuffix(file, ".py") || !underRoot(file, root) {
			continue
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(file, root), "/")
		switch relative {
		case "setup.py", "conftest.py", "noxfile.py", "fabfile.py", "docs/conf.py":
			continue
		}
		if !strings.Contains(relative, "/") {
			return true
		}
	}
	return false
}

func (s *repoShapeScan) notebooksUnder(root string) int {
	count := 0
	for directory, notebooks := range s.notebooks {
		if underRoot(directory, root) || directory == root {
			count += notebooks
		}
	}
	return count
}

func (s *repoShapeScan) classifyRustCrate(candidate *DetectedCandidate, marker *detectedMarkers, desktopRoots map[string]string) {
	if marker == nil {
		return
	}
	manifest := parseCargoManifest(marker.cargoToml)
	cargo := joinRoot(candidate.Root, "Cargo.toml")
	if manifest.deps["tauri"] {
		candidate.NotDeployable = "desktop-app"
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: cargo, Reason: "tauri: the native shell of a Tauri desktop application"})
		parent := candidate.Root
		if strings.HasSuffix(parent, "src-tauri") {
			parent = strings.TrimSuffix(strings.TrimSuffix(parent, "src-tauri"), "/")
		}
		desktopRoots[parent] = "tauri"
		return
	}
	if manifest.workspace && !manifest.hasPkg {
		return
	}
	binaries := s.rustBinaries[candidate.Root] || len(manifest.bins) > 0
	if !binaries && (cargoLibTableRE.Match(marker.cargoToml) || s.files[joinRoot(candidate.Root, "src/lib.rs")]) {
		candidate.NotDeployable = "library"
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: cargo, Reason: "library crate: no src/main.rs, src/bin/ or [[bin]] target"})
	}
}

// hostedFunctionsRoot names the platform that runs a root's code when the
// root is one of its functions directories.
func hostedFunctionsRoot(root string) string {
	segments := strings.Split(root, "/")
	for index := 0; index+1 < len(segments); index++ {
		if segments[index] == "supabase" && segments[index+1] == "functions" {
			return "Supabase Edge Function"
		}
		if segments[index] == "netlify" && segments[index+1] == "functions" {
			return "Netlify Function"
		}
	}
	return ""
}
