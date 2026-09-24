package deploy

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Repositories split into a frontend and a backend. The nested SPA used to
// earn a framework's high confidence while the Express or FastAPI server
// beside it earned a library's medium one, so the SPA was deployed on nginx
// and every /api call it made was answered with index.html by the SPA
// fallback. Two shapes are told apart here. When the server's own build
// compiles the frontend (`npm --prefix client run build`) the two are one
// deployment, and the frontend's candidate folds into the server's. When they
// are built separately but talk to each other — a Vite proxy to the API's
// port, an API URL variable on one side and a CORS origin on the other — they
// are companions: the API is selected, the frontend stays on offer, and
// preflight says the other half needs a project of its own.

var (
	frontendAPIVariableRE = regexp.MustCompile(`(^|_)(API|BACKEND|SERVER)_(URL|BASE|BASE_URL|ENDPOINT|HOST|ORIGIN)$`)
	backendOriginVariable = map[string]bool{
		"CORS_ORIGIN": true, "CORS_ORIGINS": true, "FRONTEND_URL": true, "CLIENT_URL": true, "ALLOWED_ORIGINS": true,
		"CORS_ALLOWED_ORIGINS": true, "WEB_URL": true, "APP_URL_FRONTEND": true, "CLIENT_ORIGIN": true, "FRONTEND_ORIGIN": true,
	}
	viteProxyPortRE = regexp.MustCompile(`(?:localhost|127\.0\.0\.1|0\.0\.0\.0):([0-9]{2,5})`)
	buildScripts    = []string{"build", "heroku-postbuild", "render-build", "vercel-build", "postinstall", "prebuild", "build:client", "build:all"}
)

func referencesDirectory(script, directory, packageName string) bool {
	quoted := regexp.QuoteMeta(directory)
	expression := `(?:--prefix[= ]|\bcd |--cwd[= ]|-C |--dir[= ]|--workspace[= ]|-w |--filter[= ])["']?(?:\./)?` + quoted + `(?:[/"' &;|)]|$)`
	if matched, _ := regexp.MatchString(expression, script); matched {
		return true
	}
	if packageName != "" {
		named := `(?:--workspace[= ]|-w |--filter[= ])["']?` + regexp.QuoteMeta(packageName) + `(?:["' &;|)]|$)`
		if matched, _ := regexp.MatchString(named, script); matched {
			return true
		}
	}
	return false
}

func serverCandidate(candidate DetectedCandidate) bool {
	return candidate.NotDeployable == "" && candidate.Demotion == "" && candidate.OutputDirectory == "" &&
		candidate.BuildMethod != BuildStatic && candidate.BuildMethod != BuildCompose &&
		(candidate.Profile == ProfileWeb || candidate.Profile == ProfileService)
}

func spaCandidate(candidate DetectedCandidate) bool {
	return candidate.NotDeployable == "" && candidate.Recipe == "node" && candidate.OutputDirectory != "" &&
		candidate.Profile == ProfileStatic
}

func (s *repoShapeScan) applySplitRepository(result *DetectionResult, context shapeContext) {
	merged := map[string]bool{}
	for index := range result.Candidates {
		server := &result.Candidates[index]
		if !serverCandidate(*server) || server.Recipe != "node" {
			continue
		}
		marker := context.markers[server.Root]
		var manifest nodeManifest
		if marker == nil || !parseNodeManifest(marker.packageJSON, &manifest) {
			continue
		}
		for _, frontend := range result.Candidates {
			if !spaCandidate(frontend) || merged[frontend.ID] || frontend.Root == server.Root || !underRoot(frontend.Root, server.Root) {
				continue
			}
			relative := strings.TrimPrefix(strings.TrimPrefix(frontend.Root, server.Root), "/")
			frontendName := ""
			if frontendMarker := context.markers[frontend.Root]; frontendMarker != nil {
				var frontendManifest nodeManifest
				if parseNodeManifest(frontendMarker.packageJSON, &frontendManifest) {
					frontendName = frontendManifest.Name
				}
			}
			script := ""
			for _, name := range buildScripts {
				if referencesDirectory(manifest.Scripts[name], relative, frontendName) {
					script = name
					break
				}
			}
			if script == "" {
				continue
			}
			merged[frontend.ID] = true
			runner := server.PackageManager
			if runner == "" {
				runner = "npm"
			}
			if server.BuildCommand == "" && script != "postinstall" && script != "prebuild" {
				server.BuildCommand = runner + " run " + script
			}
			reason := "the " + script + " script builds " + relative + "/ into this deployment"
			for _, reference := range s.sources.static {
				if underRoot(reference.file, server.Root) && strings.Contains(reference.path, relative) {
					reason += "; the server serves " + boundedText(reference.path, 96)
					break
				}
			}
			server.Evidence = append(server.Evidence, DetectionEvidence{Path: joinRoot(server.Root, "package.json"), Reason: reason})
			if server.RecipeIssue == "" {
				server.Confidence = ConfidenceHigh
			}
			s.addSetAside(DetectionSetAside{Path: rootLabelOf(frontend.Root), Kind: "static-files",
				Reason: "frontend built and served by the application in " + rootLabelOf(server.Root)})
		}
	}
	removeCandidates(result, func(candidate DetectedCandidate) bool { return merged[candidate.ID] })

	servers := []int{}
	for index, candidate := range result.Candidates {
		if serverCandidate(candidate) {
			servers = append(servers, index)
		}
	}
	if len(servers) == 0 {
		return
	}
	for index := range result.Candidates {
		frontend := &result.Candidates[index]
		if !spaCandidate(*frontend) || frontend.Demotion != "" {
			continue
		}
		proxyPort := 0
		for _, name := range []string{"vite.config.ts", "vite.config.js", "vite.config.mts", "vite.config.mjs", "vite.config.cjs"} {
			if content, ok := s.file(frontend.Root, name); ok && strings.Contains(string(content), "proxy") {
				if match := viteProxyPortRE.FindSubmatch(content); match != nil {
					proxyPort, _ = strconv.Atoi(string(match[1]))
					break
				}
			}
		}
		apiVariable := ""
		for _, variable := range frontend.Variables {
			if frontendAPIVariableRE.MatchString(variable.Name) {
				apiVariable = variable.Name
				break
			}
		}
		pairable := []int{}
		for _, candidateIndex := range servers {
			server := result.Candidates[candidateIndex]
			if underRoot(server.Root, frontend.Root) {
				continue
			}
			pairable = append(pairable, candidateIndex)
		}
		partner := -1
		switch {
		case proxyPort > 0:
			for _, candidateIndex := range pairable {
				if result.Candidates[candidateIndex].Port == proxyPort {
					partner = candidateIndex
				}
			}
			if partner < 0 && len(pairable) == 1 {
				partner = pairable[0]
			}
		case len(pairable) == 1:
			origin := false
			for _, variable := range result.Candidates[pairable[0]].Variables {
				origin = origin || backendOriginVariable[variable.Name]
			}
			if apiVariable != "" || origin {
				partner = pairable[0]
			}
		}
		if partner < 0 {
			continue
		}
		server := &result.Candidates[partner]
		signal := ""
		switch {
		case proxyPort > 0:
			signal = "its development server proxies API calls to localhost:" + strconv.Itoa(proxyPort)
		case apiVariable != "":
			signal = "it reads the API address from " + apiVariable
		default:
			signal = "the API allows its origin through a CORS variable"
		}
		frontend.Companions = appendUnique(frontend.Companions, server.Root)
		server.Companions = appendUnique(server.Companions, frontend.Root)
		frontend.Demotion = "the frontend of the API in " + rootLabelOf(server.Root) + "; " + signal
		frontend.Evidence = append(frontend.Evidence, DetectionEvidence{Path: rootLabelOf(frontend.Root), Reason: frontend.Demotion})
		server.Evidence = append(server.Evidence, DetectionEvidence{Path: rootLabelOf(server.Root),
			Reason: "the single-page frontend in " + rootLabelOf(frontend.Root) + " calls this API; it deploys as a second project"})
	}
	for index := range result.Candidates {
		sort.Strings(result.Candidates[index].Companions)
	}
}
