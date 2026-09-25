package deploy

import (
	"encoding/json"
	"path"
	"slices"
	"strings"
)

// An Nx integrated repository has one package.json at its root, which lists
// every application's framework, and describes each application in a
// project.json under apps/ (or wherever nx.json's workspaceLayout puts
// them). Read as a package, the root looked like a Next.js application with
// no build script; the applications, which have no package.json of their
// own, were never candidates. nx.json and project.json are JSON, read as
// data; each application project whose build this reader knows becomes a
// candidate that builds through `nx run <project>:build` at the root and
// serves what that build writes.

type nxWorkspace struct {
	projects []nxProject
}

// nxProject is one application project: its name, its directory under the
// workspace, and how it is built — the build target's executor and output
// path, or the Nx plugin that infers the target from the framework's own
// configuration file.
type nxProject struct {
	name, root     string
	executor       string
	outputPath     string
	target         string
	production     bool
	inferred       string
	viteOutput     string
	viteSPAOff     bool
	configurations []string
	// bundle is the file esbuild writes: outputFileName, else main's name
	// as JavaScript. ssr says an Angular application build also writes its
	// server (options.ssr set, outputMode not static).
	bundle string
	ssr    bool
}

// nxMaxProjects bounds how many project.json files one workspace reads.
const nxMaxProjects = 48

var nxProjectNameRE = nodePackageNameRE

func readNxWorkspace(files nodeFiles, nxJSON string) *nxWorkspace {
	var config struct {
		Plugins         []json.RawMessage `json:"plugins"`
		WorkspaceLayout struct {
			AppsDir string `json:"appsDir"`
		} `json:"workspaceLayout"`
	}
	if json.Unmarshal([]byte(nxJSON), &config) != nil {
		return nil
	}
	plugins := []string{}
	for _, raw := range config.Plugins {
		var name string
		var object struct {
			Plugin string `json:"plugin"`
		}
		if json.Unmarshal(raw, &name) == nil {
			plugins = append(plugins, name)
		} else if json.Unmarshal(raw, &object) == nil {
			plugins = append(plugins, object.Plugin)
		}
	}
	appsDir := "apps"
	if dir := strings.Trim(config.WorkspaceLayout.AppsDir, "/"); dir != "" && safeRelativePath(dir) && nodeMemberPathRE.MatchString(dir) {
		appsDir = path.Clean(dir)
	}
	directories := []string{}
	names, isDirectory := nodeDirectoryEntries(files, appsDir, 128)
	for _, name := range names {
		if !isDirectory[name] || strings.HasPrefix(name, ".") {
			continue
		}
		directory := path.Join(appsDir, name)
		if files.exists(path.Join(directory, "project.json")) {
			directories = append(directories, directory)
			continue
		}
		// Grouped applications: apps/<group>/<app>/project.json.
		children, childIsDirectory := nodeDirectoryEntries(files, directory, 64)
		for _, child := range children {
			if childIsDirectory[child] && files.exists(path.Join(directory, child, "project.json")) {
				directories = append(directories, path.Join(directory, child))
			}
		}
	}
	workspace := &nxWorkspace{}
	for _, directory := range directories {
		if len(workspace.projects) >= nxMaxProjects {
			break
		}
		if project, ok := readNxProject(files, directory, plugins); ok {
			workspace.projects = append(workspace.projects, project)
		}
	}
	if len(workspace.projects) == 0 {
		return nil
	}
	return workspace
}

func readNxProject(files nodeFiles, directory string, plugins []string) (nxProject, bool) {
	content, err := files.read(path.Join(directory, "project.json"), nodeConfigMaxBytes)
	if err != nil || !nodeMemberPathRE.MatchString(directory) {
		return nxProject{}, false
	}
	var document struct {
		Name        string `json:"name"`
		ProjectType string `json:"projectType"`
		Targets     map[string]struct {
			Executor       string                     `json:"executor"`
			Options        map[string]json.RawMessage `json:"options"`
			Configurations map[string]json.RawMessage `json:"configurations"`
		} `json:"targets"`
	}
	if json.Unmarshal(denoJSONWithoutComments(manifestText(content)), &document) != nil {
		return nxProject{}, false
	}
	if document.ProjectType != "" && document.ProjectType != "application" {
		return nxProject{}, false
	}
	project := nxProject{name: document.Name, root: directory}
	if project.name == "" {
		project.name = path.Base(directory)
	}
	if !nxProjectNameRE.MatchString(project.name) {
		return nxProject{}, false
	}
	option := func(options map[string]json.RawMessage, key string) string {
		var value string
		if raw, ok := options[key]; ok && json.Unmarshal(raw, &value) == nil {
			return value
		}
		return ""
	}
	if build, ok := document.Targets["build"]; ok && build.Executor != "" {
		project.executor = build.Executor
		if output := strings.TrimPrefix(strings.Trim(option(build.Options, "outputPath"), "/"), "./"); output != "" {
			output = strings.ReplaceAll(output, "{workspaceRoot}/", "")
			output = strings.ReplaceAll(output, "{projectRoot}", directory)
			if safeRelativePath(output) && nodeMemberPathRE.MatchString(output) {
				project.outputPath = path.Clean(output)
			}
		}
		project.target = option(build.Options, "target")
		if name := path.Base(option(build.Options, "outputFileName")); nodeScriptFile(name) && !strings.HasSuffix(name, ".ts") {
			project.bundle = name
		} else if main := path.Base(option(build.Options, "main")); nodeScriptFile(main) {
			project.bundle = strings.TrimSuffix(main, path.Ext(main)) + ".js"
		}
		if raw, ok := build.Options["ssr"]; ok && string(raw) != "false" && string(raw) != "null" && option(build.Options, "outputMode") != "static" {
			project.ssr = true
		}
		for name := range build.Configurations {
			project.configurations = append(project.configurations, name)
		}
		slices.Sort(project.configurations)
		project.production = slices.Contains(project.configurations, "production")
		return project, true
	}
	if document.ProjectType == "" {
		return nxProject{}, false
	}
	// Nx 16 and later infer a project's targets from its framework's own
	// configuration through a plugin nx.json lists.
	project.inferred = nxInferredPlugin(files, directory, plugins)
	if project.inferred == "@nx/vite/plugin" {
		for _, name := range nodeConfigNames("vite") {
			config, err := files.read(path.Join(directory, name), nodeConfigMaxBytes)
			if err != nil {
				continue
			}
			text := jsWithoutComments(string(config))
			if match := viteOutDirRE.FindStringSubmatch(text); match != nil {
				output := path.Join(directory, match[1])
				if safeRelativePath(output) && nodeMemberPathRE.MatchString(output) {
					project.viteOutput = output
				}
			}
			break
		}
	}
	return project, project.inferred != ""
}

// nxInferredPlugin is the plugin that infers the project's build from the
// configuration file in its directory.
func nxInferredPlugin(files nodeFiles, directory string, plugins []string) string {
	for _, candidate := range []struct{ plugin, kind string }{{"@nx/next/plugin", "next"}, {"@nx/vite/plugin", "vite"}} {
		if !slices.Contains(plugins, candidate.plugin) {
			continue
		}
		for _, name := range nodeConfigNames(candidate.kind) {
			if files.exists(path.Join(directory, name)) {
				return candidate.plugin
			}
		}
	}
	return ""
}

// nxCandidate is what one application project serves: the framework it is
// built with, its build and start commands on the runner, its static output
// or port, and the decision a build this reader does not know leaves open.
type nxCandidate struct {
	framework    string
	build, start string
	output       string
	spa          bool
	port         int
	decision     string
	evidence     string
}

// nxBuildCommand runs the project's build target through the workspace's
// own Nx, without its daemon (a build container has no one to talk to) and
// without Nx Cloud, which would otherwise want a token.
func (p nxProject) buildCommand(runner string) string {
	command := "NX_DAEMON=false NX_NO_CLOUD=true " + nodeExecRunner(runner) + " nx run " + p.name + ":build"
	if p.production {
		command += " --configuration=production"
	}
	return command
}

func (p nxProject) candidate(runner string) nxCandidate {
	exec := nodeExecRunner(runner)
	result := nxCandidate{build: p.buildCommand(runner)}
	output := p.outputPath
	if output == "" {
		output = path.Join("dist", p.root)
	}
	executor := strings.TrimPrefix(strings.TrimPrefix(p.executor, "@nx/"), "@nrwl/")
	switch {
	case p.inferred == "@nx/next/plugin":
		result.framework, result.port = "nextjs", 3000
		result.start = exec + " next start " + p.root
		result.evidence = "Nx infers " + p.name + "'s build from its next.config; next start serves " + p.root + "/.next"
	case p.inferred == "@nx/vite/plugin":
		result.framework, result.spa = "vite", true
		result.output = p.viteOutput
		if result.output == "" {
			result.output = path.Join(p.root, "dist")
		}
		result.evidence = "Nx infers " + p.name + "'s build from its vite.config; the site is " + result.output
	case executor == "next:build":
		result.framework, result.port = "nextjs", 3000
		result.start = exec + " next start " + output
		result.evidence = p.executor + " writes " + p.name + " to " + output + ", which next start serves"
	case executor == "vite:build", executor == "rsbuild:build", executor == "rspack:rspack":
		result.framework, result.output, result.spa = "vite", output, true
		result.evidence = p.executor + " writes " + p.name + "'s site to " + output
	case executor == "webpack:webpack" && p.target != "node":
		result.framework, result.output, result.spa = "", output, true
		result.evidence = p.executor + " writes " + p.name + "'s site to " + output
	case executor == "webpack:webpack", executor == "esbuild:esbuild", executor == "node:build", executor == "node:webpack":
		// webpack names its bundle after the entry chunk, main; esbuild
		// after the main file, unless outputFileName says otherwise.
		bundle := "main.js"
		if executor == "esbuild:esbuild" && p.bundle != "" {
			bundle = p.bundle
		}
		result.port = 3000
		result.start = "node " + path.Join(output, bundle)
		result.evidence = p.executor + " bundles " + p.name + " into " + path.Join(output, bundle)
	case strings.HasSuffix(p.executor, ":application") && p.ssr:
		entry := path.Join(output, "server", "server.mjs")
		result.framework, result.port = "angular", 4000
		result.start = "node " + entry
		result.evidence = p.executor + " writes " + p.name + "'s server to " + entry
	case strings.HasSuffix(p.executor, ":application") || strings.HasSuffix(p.executor, ":browser-esbuild") || strings.HasSuffix(p.executor, ":browser"):
		result.framework, result.spa = "angular", true
		result.output = output
		if strings.HasSuffix(p.executor, ":application") {
			result.output = path.Join(output, "browser")
		}
		result.evidence = p.executor + " writes " + p.name + "'s site to " + result.output
	default:
		result.decision = "confirm how the Nx project " + p.name + " is served: its build executor " + boundedEvidence(p.executor) + " is not one detection knows"
		result.evidence = "Nx project " + p.name + " builds with " + boundedEvidence(p.executor)
	}
	return result
}
