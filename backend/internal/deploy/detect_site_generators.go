package deploy

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// The shape passes for static sites. applySiteGenerators runs before the
// ecosystems and static roots are settled: a generator's root becomes one
// candidate built by its generator, and what used to stand in for it at that
// root — its templates served raw, a Hugo Modules go.mod taken for a Go
// service, a docs requirements.txt taken for a Python service, a Tailwind
// package.json taken for a Node worker — is set aside with the reason.
// applyStaticSiteFacts runs after other platforms' files are read, so it sees
// the output directory the site is finally served from.

// siteReadBudget bounds what the site passes read beyond the walk, apart
// from detection's own limits: these files refine a plan and are never a
// reason to call a scan truncated.
type siteReadBudget struct {
	bytes int64
	files int
}

func (b *siteReadBudget) spend(n int64) bool {
	if b.files >= 512 || b.bytes+n > 4<<20 {
		return false
	}
	b.files++
	b.bytes += n
	return true
}

// siteTreeAt reads a root of the detected checkout: its files through the
// contained tree under the budget, its directories from what the walk saw.
func (s *repoShapeScan) siteTreeAt(tree detectionTree, root string, directories map[string]bool, budget *siteReadBudget) siteTree {
	return siteTree{
		read: func(relative string) ([]byte, bool) {
			full := joinRoot(root, relative)
			info, ok := tree.regular(full)
			if !ok || info.Size() > 256<<10 || !budget.spend(info.Size()) {
				return nil, false
			}
			content, ok := tree.read(full, 256<<10)
			if !ok {
				return nil, false
			}
			return manifestText(content), true
		},
		dir:  func(relative string) bool { return directories[joinRoot(root, relative)] },
		file: func(relative string) bool { return s.files[joinRoot(root, relative)] },
		workflows: func() [][]byte {
			names := make([]string, 0, len(s.workflows))
			for name := range s.workflows {
				names = append(names, name)
			}
			sort.Strings(names)
			contents := make([][]byte, 0, len(names))
			for _, name := range names {
				contents = append(contents, s.workflows[name])
			}
			return contents
		},
	}
}

// walkedDirectories are the directories the walk saw a file under, with
// their ancestors.
func (s *repoShapeScan) walkedDirectories() map[string]bool {
	directories := map[string]bool{}
	for file := range s.files {
		for directory := path.Dir(file); directory != "." && directory != "/" && !directories[directory]; directory = path.Dir(directory) {
			directories[directory] = true
		}
	}
	return directories
}

// siteMarkerNames are the files that can make their directory, or one above
// it, a site generator's root.
var siteMarkerNames = map[string]bool{
	"hugo.toml": true, "hugo.yaml": true, "hugo.yml": true, "hugo.json": true, "config.toml": true,
	"config.yaml": true, "config.yml": true, "config.json": true, "book.toml": true, "_config.yml": true,
	"_config.yaml": true, "mkdocs.yml": true, "mkdocs.yaml": true, "zensical.toml": true, "pelicanconf.py": true,
	"conf.py": true, ".readthedocs.yaml": true, ".readthedocs.yml": true,
}

// siteGeneratorRoots are the directories whose files may name a generator,
// in order. A theme's own configuration and its example site are part of the
// theme, not a site of the repository's.
func (s *repoShapeScan) siteGeneratorRoots() []string {
	seen := map[string]bool{}
	roots := []string{}
	add := func(root string) {
		if root == "." {
			root = ""
		}
		if !seen[root] {
			seen[root] = true
			roots = append(roots, root)
		}
	}
	for file := range s.files {
		name := strings.ToLower(path.Base(file))
		if !siteMarkerNames[name] {
			continue
		}
		directory := path.Dir(file)
		skipped := false
		for _, segment := range strings.Split(directory, "/") {
			switch strings.ToLower(segment) {
			case "themes", "examplesite", "node_modules", "vendor", "_site", "site-packages":
				skipped = true
			}
		}
		if skipped {
			continue
		}
		switch {
		case directory == "config/_default" || strings.HasSuffix(directory, "/config/_default"):
			add(path.Dir(path.Dir(directory)))
		case name == "conf.py":
			// Sphinx builds from the project above its docs directory, where
			// the requirements and the code autodoc imports are.
			parent := directory
			if path.Base(parent) == "source" {
				parent = path.Dir(parent)
			}
			switch path.Base(parent) {
			case "docs", "doc", "documentation":
				parent = path.Dir(parent)
			}
			add(parent)
		default:
			add(directory)
		}
	}
	sort.Strings(roots)
	return roots
}

func (s *repoShapeScan) applySiteGenerators(result *DetectionResult, context shapeContext) {
	tree := openDetectionTree(s.root)
	defer tree.close()
	directories := s.walkedDirectories()
	budget := &siteReadBudget{}
	var submodules []GitSubmodule
	if len(s.gitModules) > 0 {
		submodules = parseGitModules(s.gitModules, "")
	}
	for _, root := range s.siteGeneratorRoots() {
		if context.ctx.Err() != nil {
			return
		}
		site := s.siteTreeAt(tree, root, directories, budget)
		generator, ok := readSiteGenerator(site)
		if !ok {
			continue
		}
		candidate := s.siteCandidate(root, generator, site, submodules, context)
		s.setAsideForSite(result, root, generator, candidate, context.markers)
		result.Candidates = append(result.Candidates, candidate)
	}
	// Frameworks another recipe builds whose output the catalogue could only
	// guess: Lume's in deno.json, Hexo's and Eleventy's in their own files.
	for index := range result.Candidates {
		candidate := &result.Candidates[index]
		marker := context.markers[candidate.Root]
		if candidate.BuildMethod != BuildRecipe || candidate.NotDeployable != "" || marker == nil {
			continue
		}
		site := s.siteTreeAt(tree, candidate.Root, directories, budget)
		switch {
		case candidate.Recipe == "deno" && len(marker.denoJSON) > 0:
			if generator, ok := lumeSite(site, parseDenoConfig(marker.denoJSON)); ok {
				applyLumeSite(candidate, generator)
			}
		case candidate.Recipe == "node" && candidate.Framework == "hexo":
			if output := hexoPublicDir(site.read); output != candidate.OutputDirectory {
				candidate.OutputDirectory = output
				candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(candidate.Root, "_config.yml"), Reason: "hexo generate writes public_dir " + output})
			}
		case candidate.Recipe == "node" && candidate.Framework == "eleventy":
			var manifest nodeManifest
			parseNodeManifest(marker.packageJSON, &manifest)
			if output, source := eleventyOutput(site.read, manifest); source != "" && output != candidate.OutputDirectory {
				candidate.OutputDirectory = output
				candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(candidate.Root, source), Reason: "Eleventy writes its output to " + output})
			}
		}
	}
}

// siteCandidateNames say what each generator's output is.
var siteCandidateNames = map[string]string{
	"mdbook": "mdBook book", "sphinx": "Sphinx documentation", "mkdocs": "MkDocs site", "zensical": "Zensical site",
}

func (s *repoShapeScan) siteCandidate(root string, generator siteGenerator, site siteTree, submodules []GitSubmodule, context shapeContext) DetectedCandidate {
	name := siteCandidateNames[generator.name]
	if name == "" {
		name = generator.label() + " site"
	}
	candidate := DetectedCandidate{
		Name: name + " in " + rootLabelOf(root), Profile: ProfileStatic, Confidence: ConfidenceHigh,
		Framework: generator.name, Recipe: generator.recipe, Port: 80,
		BuildCommand: generator.build, OutputDirectory: generator.output, RecipeIssue: boundedText(generator.issue, 512),
		NeedsDecision: []string{}, Variables: context.variables(root),
		StaticSite: &DetectedStaticSite{Generator: generator.name, VersionIssue: boundedText(generator.versionIssue, 512)},
	}
	for _, evidence := range generator.evidence {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(root, evidence.Path), Reason: evidence.Reason})
	}
	switch generator.recipe {
	case "site":
		candidate.StaticSite.Version, candidate.StaticSite.Declared = generator.version, boundedText(generator.declared, 256)
		candidate.StaticSite.Unpinned = generator.unpinned && generator.name != "jekyll"
		candidate.UnpinnedDependencies = generator.name == "jekyll" && generator.unpinned
	case "python":
		versionFile, _ := site.read(".python-version")
		runtimeFile, _ := site.read("runtime.txt")
		pyproject, _ := site.read("pyproject.toml")
		version, err := choosePythonRecipeVersion("", string(versionFile), string(runtimeFile), string(pyproject))
		if len(versionFile) == 0 && len(runtimeFile) == 0 && !pythonRequiresRE.Match(pyproject) && generator.pythonDeclared != "" {
			version, err = generator.pythonDeclared, nil
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(root, ".readthedocs.yaml"), Reason: "Python " + version + " from .readthedocs.yaml"})
		}
		if err == nil {
			candidate.PythonVersion = version
		}
		candidate.UnpinnedDependencies = generator.unpinned
		candidate.StaticSite.Unpinned = generator.unpinned
		if !generator.unpinned {
			files := map[string][]byte{}
			for _, manifest := range []string{"requirements.txt", "pyproject.toml", "uv.lock", "poetry.lock"} {
				if content, ok := site.read(manifest); ok {
					files[manifest] = content
				}
			}
			candidate.UnpinnedDependencies = len(files) > 0 && readPythonDependencies(files).unpinned
		}
	}
	for _, theme := range generator.themes {
		full := joinRoot(root, theme)
		for _, submodule := range submodules {
			if underRoot(full, submodule.Path) || underRoot(submodule.Path, full) {
				candidate.StaticSite.ThemeSubmodule = submodule.Path
				candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: submodule.Path,
					Reason: "the theme is the Git submodule " + submodule.Path + "; the build needs it fetched"})
				break
			}
		}
		if candidate.StaticSite.ThemeSubmodule != "" {
			break
		}
	}
	if generator.name == "hugo" {
		// Hugo writes its permalinks, sitemap and feeds from the base URL:
		// the planned domain when there is one, the site's root otherwise.
		candidate.Variables = mergeDetectedVariable(candidate.Variables, DetectedVariable{
			Name: "HUGO_BASEURL", Sources: []string{joinRoot(root, generator.config)}, Setup: "domain", Phase: "build",
			DomainTemplate: "{{scheme}}://{{hostname}}/",
			SetupReason:    "Hugo writes the sitemap, feeds and absolute links from it (the site is built for / until a domain is set)",
		})
	}
	if candidate.RecipeIssue != "" {
		candidate.Confidence = ConfidenceLow
	} else if generator.name == "jekyll" && !generator.gemfile {
		candidate.Confidence = ConfidenceMedium
	}
	return newDetectedCandidate(root, BuildRecipe, candidate)
}

// setAsideForSite removes what stood in for the site at its root: its
// templates and committed output served as a plain site, a Go module with
// no Go code, a Python or Node package that only builds the site.
func (s *repoShapeScan) setAsideForSite(result *DetectionResult, root string, generator siteGenerator, site DetectedCandidate, markers map[string]*detectedMarkers) {
	label := generator.label()
	goCode := false
	for file := range s.files {
		if strings.HasSuffix(file, ".go") && !strings.HasSuffix(file, "_test.go") && underRoot(file, root) &&
			!strings.Contains("/"+file, "/themes/") && !strings.Contains("/"+file, "/node_modules/") {
			goCode = true
			break
		}
	}
	docsRoots := map[string]bool{root: true}
	if generator.sphinxSource != "" {
		docsRoots[joinRoot(root, generator.sphinxSource)] = true
		docsRoots[path.Dir(joinRoot(root, generator.sphinxSource))] = true
	}
	removeCandidates(result, func(candidate DetectedCandidate) bool {
		var reason, kind string
		switch {
		case candidate.ID == site.ID:
			return false
		case candidate.BuildMethod == BuildStatic && underRoot(candidate.Root, root):
			kind = "template"
			reason = joinRoot(candidate.Root, "index.html") + " is one of the " + label + " site's own files; its build output is what is served"
		case candidate.Root != root && !docsRoots[candidate.Root]:
			return false
		case candidate.Recipe == "go" && generator.name == "hugo" && !goCode && s.absenceKnown(root):
			kind = "tooling"
			reason = "go.mod declares the Hugo site's modules; there is no Go program in " + rootLabelOf(root)
		case candidate.Recipe == "python" && candidate.BuildMethod == BuildRecipe && generator.recipe == "python" &&
			(candidate.Framework == "python" || candidate.Framework == "") && candidate.StartCommand == "":
			kind = "tooling"
			reason = "the Python manifest in " + rootLabelOf(candidate.Root) + " has no server of its own to run; the " + label + " site it builds is served as files"
		case candidate.Recipe == "node" && candidate.BuildMethod == BuildRecipe && candidate.Root == root &&
			s.nodeToolingOnly(root, markers[candidate.Root]):
			kind = "asset-pipeline"
			reason = "package.json builds the " + label + " site's assets; the site's build installs it"
		default:
			return false
		}
		s.addSetAside(DetectionSetAside{Path: rootLabelOf(candidate.Root), Kind: kind, Reason: boundedText(reason, 512)})
		return true
	})
}

// applyLumeSite makes a Deno candidate the Lume site it is: built by its
// build task, its output served by nginx.
func applyLumeSite(candidate *DetectedCandidate, generator siteGenerator) {
	candidate.Name = "Lume site in " + rootLabelOf(candidate.Root)
	candidate.Framework, candidate.Profile, candidate.Port = "lume", ProfileStatic, 80
	candidate.BuildCommand, candidate.StartCommand, candidate.OutputDirectory = generator.build, "", generator.output
	candidate.NeedsDecision = removeDecision(candidate.NeedsDecision, "add a start task to deno.json or choose the entry file to run")
	candidate.StaticSite = &DetectedStaticSite{Generator: "lume"}
	candidate.RecipeIssue = boundedText(generator.issue, 512)
	candidate.Confidence = ConfidenceHigh
	if candidate.RecipeIssue != "" {
		candidate.Confidence = ConfidenceLow
	}
	candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(candidate.Root, "deno.json"), Reason: "deno.json imports Lume, a static site generator"})
	for _, evidence := range generator.evidence {
		candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(candidate.Root, evidence.Path), Reason: evidence.Reason})
	}
}

// applyStaticSiteFacts records, for every candidate that serves static
// output, the sub-path its framework built it for, the page SvelteKit falls
// back to, and the hosting rules the server will apply.
func (s *repoShapeScan) applyStaticSiteFacts(result *DetectionResult, context shapeContext) {
	tree := openDetectionTree(s.root)
	defer tree.close()
	directories := s.walkedDirectories()
	budget := &siteReadBudget{}
	for index := range result.Candidates {
		candidate := &result.Candidates[index]
		static := candidate.BuildMethod == BuildStatic || (candidate.BuildMethod == BuildRecipe && candidate.OutputDirectory != "")
		if !static || candidate.NotDeployable != "" || context.ctx.Err() != nil {
			continue
		}
		site := s.siteTreeAt(tree, candidate.Root, directories, budget)
		if candidate.Recipe == "node" {
			var manifest nodeManifest
			if content, ok := site.read("package.json"); ok {
				parseNodeManifest(content, &manifest)
			}
			base, source, expression := staticBasePath(site.read, candidate.Framework, candidate.OutputDirectory, manifest)
			switch {
			case base != "":
				facts := candidate.staticSite()
				facts.BasePath, facts.BasePathSource = base, joinRoot(candidate.Root, source)
				candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: facts.BasePathSource,
					Reason: "built for the sub-path " + base + "/; served there, with / redirecting to it"})
			case expression:
				facts := candidate.staticSite()
				facts.BasePathExpression, facts.BasePathSource = true, joinRoot(candidate.Root, source)
				candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: facts.BasePathSource,
					Reason: "the base path is computed in " + source + "; the site is served at the root"})
			}
			if candidate.Framework == "sveltekit" && !candidate.SPAFallback {
				if fallback := svelteKitFallback(site.read); fallback != "" {
					candidate.SPAFallback = true
					candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(candidate.Root, "svelte.config.js"),
						Reason: "adapter-static writes " + fallback + " for the routes it did not prerender; nginx falls back to it"})
				}
			}
		}
		var above siteFiles
		if candidate.Root != "" {
			if content, ok := s.file("", "netlify.toml"); ok {
				if base, ok := platformPath("", tomlText(readTOML(content), "build", "base")); ok && base == candidate.Root {
					above = func(relative string) ([]byte, bool) { return content, relative == "netlify.toml" }
				}
			}
		}
		rules := readHostingRules(site.read, above)
		if len(rules.files) == 0 {
			continue
		}
		facts := candidate.staticSite()
		facts.HostingRules, facts.HostingRulesLeftOut = rules.translated(), rules.unsupported
		if facts.HostingRules > 0 {
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(candidate.Root, rules.files[0]),
				Reason: boundedText(strings.Join(rules.files, ", ")+": "+pluralRules(facts.HostingRules)+" applied by the static server", 512)})
		}
		if rules.spa && !candidate.SPAFallback {
			candidate.SPAFallback = true
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(candidate.Root, rules.files[0]), Reason: "a rule rewrites every path to /index.html"})
		}
		// The rules this server applies are no longer "unsupported" in the
		// other platform's own reading of the file.
		for manifest := range candidate.PlatformManifests {
			switch candidate.PlatformManifests[manifest].Platform {
			case "netlify", "vercel":
				candidate.PlatformManifests[manifest].Redirects = 0
			}
		}
	}
}

func pluralRules(count int) string {
	if count == 1 {
		return "1 redirect or header rule"
	}
	return fmt.Sprintf("%d redirect and header rules", count)
}
