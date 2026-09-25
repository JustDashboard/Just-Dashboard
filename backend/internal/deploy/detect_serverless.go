package deploy

import (
	"encoding/json"
	"path"
	"sort"
	"strings"
)

// Serverless and edge code is written for a platform's function runtime, not
// for a process in a container. A Vite app with Vercel functions in api/ was
// deployed as a static site whose /api requests the SPA fallback answered
// with index.html and a 200; a create-cloudflare app's Worker disappeared and
// its client files, which the Cloudflare plugin writes to dist/client, were
// served from an empty dist/. Detection now names that code, fixes the output
// directory the plugin uses, and lets preflight say what will not run.

// wranglerConfig is the part of a Wrangler configuration detection reads:
// the Worker entry and where its static assets are.
type wranglerConfig struct {
	file   string
	main   string
	assets string
}

func (s *repoShapeScan) wrangler(root string) (wranglerConfig, bool) {
	if content, ok := s.file(root, "wrangler.toml"); ok {
		entries := readTOML(content)
		config := wranglerConfig{file: joinRoot(root, "wrangler.toml"), main: tomlText(entries, "", "main"),
			assets: tomlText(entries, "assets", "directory")}
		if config.assets == "" {
			config.assets = tomlText(entries, "site", "bucket")
		}
		return config, true
	}
	for _, name := range []string{"wrangler.jsonc", "wrangler.json"} {
		content, ok := s.file(root, name)
		if !ok {
			continue
		}
		var document struct {
			Main   string `json:"main"`
			Assets struct {
				Directory string `json:"directory"`
			} `json:"assets"`
		}
		if json.Unmarshal(content, &document) != nil && json.Unmarshal(denoJSONWithoutComments(content), &document) != nil {
			return wranglerConfig{file: joinRoot(root, name)}, true
		}
		return wranglerConfig{file: joinRoot(root, name), main: document.Main, assets: document.Assets.Directory}, true
	}
	return wranglerConfig{}, false
}

func (s *repoShapeScan) applyServerless(result *DetectionResult, context shapeContext) {
	// Firebase deploys its functions directory itself; the package there is
	// Firebase's, not a service of this server's.
	hosted := map[string]string{}
	for root := range s.roots {
		content, ok := s.file(root, "firebase.json")
		if !ok {
			continue
		}
		var document struct {
			Functions json.RawMessage `json:"functions"`
		}
		if json.Unmarshal(content, &document) != nil || len(document.Functions) == 0 {
			continue
		}
		var single struct {
			Source string `json:"source"`
		}
		var many []struct {
			Source string `json:"source"`
		}
		sources := []string{}
		if json.Unmarshal(document.Functions, &many) == nil {
			for _, entry := range many {
				sources = append(sources, entry.Source)
			}
		} else if json.Unmarshal(document.Functions, &single) == nil {
			sources = append(sources, single.Source)
		}
		for _, source := range sources {
			if source == "" {
				source = "functions"
			}
			if functionsRoot, ok := platformPath(root, source); ok {
				hosted[functionsRoot] = "Firebase Cloud Functions declared in " + joinRoot(root, "firebase.json")
			}
		}
	}
	removeCandidates(result, func(candidate DetectedCandidate) bool {
		reason, ok := hosted[candidate.Root]
		if ok {
			s.addSetAside(DetectionSetAside{Path: rootLabelOf(candidate.Root), Kind: "hosted-functions",
				Reason: reason + "; they run on Firebase, not in a container here"})
		}
		return ok
	})
	for index := range result.Candidates {
		candidate := &result.Candidates[index]
		if candidate.NotDeployable != "" || candidate.BuildMethod == BuildDockerfile || candidate.BuildMethod == BuildCompose {
			continue
		}
		marker := context.markers[candidate.Root]
		var manifest nodeManifest
		hasManifest := marker != nil && parseNodeManifest(marker.packageJSON, &manifest)
		if config, ok := s.wrangler(candidate.Root); ok {
			if hasManifest && manifest.has("@cloudflare/vite-plugin") && candidate.OutputDirectory != "" {
				candidate.OutputDirectory = "dist/client"
				candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: joinRoot(candidate.Root, "package.json"),
					Reason: "@cloudflare/vite-plugin writes the client build to dist/client"})
			}
			if entry := strings.TrimPrefix(config.main, "./"); entry != "" && len(entry) <= 1024 {
				switch {
				case wranglerAdapterOutput(entry):
					candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: config.file,
						Reason: "Worker entry " + boundedEvidence(entry) + " is written by the framework's Cloudflare adapter; the Node build serves the same application"})
				default:
					// Only an application with no server of its own loses its
					// API with the Worker. One the recipe starts in Node keeps
					// serving; the Worker is a second runtime that stays behind.
					ownServer := candidate.StartCommand != "" && !strings.Contains(candidate.StartCommand, entry)
					candidate.ServerlessCode = append(candidate.ServerlessCode, DetectedServerlessCode{
						Platform: "cloudflare-workers", Entry: entry, Paths: []string{config.file}, Blocking: !ownServer,
					})
					candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: config.file,
						Reason: "Worker entry " + boundedEvidence(entry) + " runs only on Cloudflare Workers"})
				}
			}
		}
		static := candidate.Profile == ProfileStatic || candidate.BuildMethod == BuildStatic
		if !static {
			continue
		}
		groups := map[string][]string{}
		for _, file := range s.functionFiles {
			if !underRoot(file, candidate.Root) || ownedByNestedCandidate(file, candidate.Root, result.Candidates) {
				continue
			}
			hostedElsewhere := false
			for functionsRoot := range hosted {
				hostedElsewhere = hostedElsewhere || underRoot(file, functionsRoot)
			}
			if hostedElsewhere {
				continue
			}
			relative := strings.TrimPrefix(strings.TrimPrefix(file, candidate.Root), "/")
			first, rest, _ := strings.Cut(relative, "/")
			switch {
			case first == "api" && rest != "":
				groups["vercel"] = append(groups["vercel"], "/api/"+strings.TrimSuffix(rest, path.Ext(rest)))
			case first == "netlify" && strings.HasPrefix(rest, "functions/"):
				groups["netlify"] = append(groups["netlify"], relative)
			case first == "functions" && rest != "":
				groups["cloudflare-pages"] = append(groups["cloudflare-pages"], relative)
			}
		}
		platforms := make([]string, 0, len(groups))
		for platform := range groups {
			platforms = append(platforms, platform)
		}
		sort.Strings(platforms)
		for _, platform := range platforms {
			paths := groups[platform]
			sort.Strings(paths)
			if len(paths) > 16 {
				paths = paths[:16]
			}
			candidate.ServerlessCode = append(candidate.ServerlessCode, DetectedServerlessCode{Platform: platform, Paths: paths})
			candidate.Evidence = append(candidate.Evidence, DetectionEvidence{Path: rootLabelOf(candidate.Root),
				Reason: serverlessLabel(platform) + " functions beside a static site: " + strings.Join(paths[:min(len(paths), 3)], ", ")})
		}
	}
}

// wranglerAdapterOutputs are the Worker entries a framework's Cloudflare
// adapter writes at build time (OpenNext, Astro, SvelteKit, Nitro): the
// application behind them is the one the framework itself builds.
var wranglerAdapterOutputs = []string{".open-next/", "dist/_worker.js", ".svelte-kit/cloudflare/", ".output/server/"}

func wranglerAdapterOutput(entry string) bool {
	for _, prefix := range wranglerAdapterOutputs {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}

func serverlessLabel(platform string) string {
	switch platform {
	case "vercel":
		return "Vercel"
	case "netlify":
		return "Netlify"
	case "cloudflare-pages":
		return "Cloudflare Pages"
	case "cloudflare-workers":
		return "Cloudflare Workers"
	}
	return platform
}
