package deploy

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

// nodeScriptNameRE is a package script's name as a start command may run it.
var nodeScriptNameRE = regexp.MustCompile(`^[A-Za-z0-9:_.@/+-]+$`)

// A package's `start` script is what its author runs, which is not always
// what a server should run: the Angular CLI writes "start": "ng serve",
// older Astro templates "astro dev", Express tutorials "nodemon index.js".
// A development server rebuilds on every change, answers on localhost only
// (ng serve, vite, astro dev) and on its own port, so a deployment that runs
// one either never becomes ready or serves development builds. The start
// command is chosen past such a script: the framework's own entry, another
// script that serves a build, or the watcher's command without the watcher.

// nodeProgramWords are a simple command's words from the program on: what
// runs before it (assignments, env, cross-env) and the runners that only
// find it (npx, bunx, pnpm exec, yarn exec) are dropped.
func nodeProgramWords(segment string) []string {
	words := nodeSegmentWords(segment)
	for len(words) > 0 {
		switch {
		case words[0] == "npx" || words[0] == "bunx" || words[0] == "pnpx":
			words = words[1:]
			for len(words) > 0 && strings.HasPrefix(words[0], "-") {
				words = words[1:]
			}
		case len(words) > 1 && (words[0] == "pnpm" || words[0] == "yarn" || words[0] == "npm") && words[1] == "exec":
			words = words[2:]
			if len(words) > 0 && words[0] == "--" {
				words = words[1:]
			}
		default:
			return words
		}
	}
	return words
}

// nodeDevServer names the development server or file watcher a script body
// starts first, or "" when it starts neither.
func nodeDevServer(body string) string {
	segments := nodeCommandSegments(body)
	if len(segments) == 0 {
		return ""
	}
	for _, segment := range segments {
		if label := nodeDevServerSegment(segment); label != "" {
			return label
		}
	}
	return ""
}

func nodeDevServerSegment(segment string) string {
	words := nodeProgramWords(segment)
	if len(words) == 0 {
		return ""
	}
	program := path.Base(words[0])
	sub := ""
	if len(words) > 1 {
		sub = words[1]
	}
	flag := strings.HasPrefix(sub, "-")
	switch program {
	case "ng":
		if sub == "serve" || sub == "s" {
			return "ng serve"
		}
	case "vite":
		if sub == "" || sub == "dev" || sub == "serve" || flag {
			return "vite"
		}
	case "astro":
		if sub == "" || sub == "dev" || flag {
			return "astro dev"
		}
	case "next", "nuxt", "nuxi", "react-router", "vinxi", "vike", "waku", "svelte-kit", "keystone", "rsbuild", "qwik":
		if sub == "dev" {
			return program + " dev"
		}
	case "remix":
		if sub == "dev" || sub == "vite:dev" {
			return "remix " + sub
		}
	case "vue-cli-service", "webpack", "webpack-cli", "rspack":
		if sub == "serve" || (program == "rspack" && sub == "dev") {
			return program + " " + sub
		}
	case "webpack-dev-server":
		return "webpack-dev-server"
	case "react-scripts", "docusaurus", "farm":
		if sub == "start" || (program == "farm" && sub == "dev") {
			return program + " " + sub
		}
	case "gatsby", "strapi", "medusa":
		if sub == "develop" || sub == "dev" {
			return program + " " + sub
		}
	case "vitepress", "vuepress", "rspress", "slidev":
		if sub == "dev" || (program == "slidev" && (sub == "" || flag)) {
			return program + " dev"
		}
	case "parcel":
		if sub == "" || sub == "serve" || (!flag && sub != "build" && sub != "watch") {
			return "parcel"
		}
	case "eleventy", "@11ty/eleventy":
		if slices.Contains(words, "--serve") {
			return "eleventy --serve"
		}
	case "rw", "redwood":
		if sub == "dev" {
			return "rw dev"
		}
	case "node":
		if len(words) > 2 && words[1] == "ace" && words[2] == "serve" {
			return "node ace serve"
		}
		for _, word := range words[1:] {
			if word == "--watch" || strings.HasPrefix(word, "--watch=") || strings.HasPrefix(word, "--watch-path") {
				return "node --watch"
			}
		}
	case "nodemon", "ts-node-dev", "tsnd", "node-dev":
		return program
	case "tsx":
		if sub == "watch" {
			return "tsx watch"
		}
	case "bun":
		for _, word := range words[1:] {
			if word == "--hot" || word == "--watch" {
				return "bun " + word
			}
		}
	case "nest":
		if sub == "start" && (slices.Contains(words, "--watch") || slices.Contains(words, "-w")) {
			return "nest start --watch"
		}
	}
	return ""
}

// nodeWatchedCommand is the command a watcher runs, without the watcher:
// `nodemon server.js` is `node server.js`, `tsx watch src/index.ts` is
// `tsx src/index.ts`, `bun --hot src/index.ts` is `bun src/index.ts`. The
// program the result runs must be one the image has: node and bun always,
// tsx and ts-node only when the package installs them. ok is false for a
// dev server, which has no production form here, and for a watcher whose
// options this reader does not know.
func nodeWatchedCommand(body string, declared func(string) bool) (string, bool) {
	segments := nodeCommandSegments(body)
	if len(segments) != 1 {
		return "", false
	}
	prefix := nodeAssignmentPrefix(segments[0])
	words := nodeProgramWords(segments[0])
	if len(words) < 2 {
		return "", false
	}
	program := path.Base(words[0])
	var command []string
	switch {
	case program == "nodemon":
		file, rest, ok := nodeWatchedFile(words[1:], map[string]bool{"-w": true, "--watch": true, "-e": true, "--ext": true, "-i": true, "--ignore": true, "--delay": true, "-d": true, "--signal": true, "-s": true})
		if !ok || slices.Contains(words, "--exec") || slices.Contains(words, "-x") {
			return "", false
		}
		runner, known := nodeScriptRunner(file, declared)
		if !known {
			return "", false
		}
		command = append([]string{runner, file}, rest...)
	case program == "tsx" && words[1] == "watch":
		kept := []string{}
		skipValue := false
		for _, word := range words[2:] {
			switch {
			case skipValue:
				skipValue = false
			case word == "--clear-screen=false" || word == "--clear-screen" || word == "--no-clear-screen":
			case word == "--ignore" || word == "--include" || word == "--exclude":
				skipValue = true
			case strings.HasPrefix(word, "--ignore=") || strings.HasPrefix(word, "--include=") || strings.HasPrefix(word, "--exclude="):
			default:
				kept = append(kept, word)
			}
		}
		if len(kept) == 0 || !declared("tsx") {
			return "", false
		}
		command = append([]string{"tsx"}, kept...)
	case program == "node":
		kept := []string{"node"}
		for _, word := range words[1:] {
			if word == "--watch" || strings.HasPrefix(word, "--watch=") || strings.HasPrefix(word, "--watch-path") || word == "--watch-preserve-output" {
				continue
			}
			kept = append(kept, word)
		}
		if len(kept) < 2 {
			return "", false
		}
		command = kept
	case program == "bun":
		kept := []string{"bun"}
		for _, word := range words[1:] {
			if word == "--hot" || word == "--watch" || word == "--no-clear-screen" {
				continue
			}
			kept = append(kept, word)
		}
		if len(kept) > 2 && kept[1] == "run" && nodeScriptFile(kept[2]) {
			kept = append(kept[:1], kept[2:]...)
		}
		if len(kept) < 2 || !nodeScriptFile(kept[1]) {
			return "", false
		}
		command = kept
	case program == "ts-node-dev" || program == "tsnd":
		file, _, ok := nodeWatchedFile(words[1:], map[string]bool{"--ignore-watch": true, "-r": true, "--require": true, "--watch": true, "--deps": true, "--interval": true, "--debounce": true})
		if !ok || !declared("ts-node") {
			return "", false
		}
		command = []string{"ts-node", "--transpile-only", file}
	default:
		return "", false
	}
	return prefix + strings.Join(command, " "), true
}

// nodeAssignmentPrefix keeps the NAME=value assignments a segment starts
// with, which a watcher passes on to the program it runs.
func nodeAssignmentPrefix(segment string) string {
	prefix := ""
	for _, word := range strings.Fields(segment) {
		if !nodeAssignmentRE.MatchString(word) {
			break
		}
		prefix += word + " "
	}
	return prefix
}

// nodeWatchedFile is the script a watcher's arguments run: the first word
// that is not an option or an option's value, and the arguments after it.
func nodeWatchedFile(words []string, valued map[string]bool) (string, []string, bool) {
	for index := 0; index < len(words); index++ {
		word := words[index]
		if strings.HasPrefix(word, "-") {
			if valued[word] {
				index++
			}
			continue
		}
		if !nodeScriptFile(word) {
			return "", nil, false
		}
		return word, words[index+1:], true
	}
	return "", nil, false
}

// nodeScriptFile says a word is a script file, not a subcommand.
func nodeScriptFile(word string) bool {
	switch path.Ext(word) {
	case ".js", ".mjs", ".cjs", ".ts", ".mts", ".cts", ".tsx", ".jsx":
		return safeRelativePath(strings.TrimPrefix(word, "./"))
	}
	return false
}

// nodeScriptRunner is the program that runs a file outside a watcher:
// node for JavaScript, tsx or ts-node for TypeScript when the package
// installs one, since Node 20 does not strip types.
func nodeScriptRunner(file string, declared func(string) bool) (string, bool) {
	switch path.Ext(file) {
	case ".js", ".mjs", ".cjs":
		return "node", true
	}
	switch {
	case declared("tsx"):
		return "tsx", true
	case declared("ts-node"):
		return "ts-node", true
	}
	return "", false
}

// nodeProductionScripts are the scripts a package serves a build with when
// its `start` runs a development server, in the order they are preferred.
var nodeProductionScripts = []string{"start:prod", "start:production", "prod", "production", "serve:prod", "serve"}

// nodeServingScript is the first of names whose script exists and does not
// start a development server, directly or through a script it runs.
func nodeServingScript(scripts map[string]string, names []string) string {
	for _, name := range names {
		if strings.TrimSpace(scripts[name]) != "" && nodeScriptDevServer(scripts, name) == "" {
			return name
		}
	}
	return ""
}

// nodeScriptDevServer names the development server or watcher a package
// script starts, following the scripts it runs (`"start": "npm run dev"`).
func nodeScriptDevServer(scripts map[string]string, name string) string {
	for _, segment := range nodeReachedSegments(scripts, []string{"npm run " + name}, false) {
		if label := nodeDevServerSegment(segment); label != "" {
			return label
		}
	}
	return ""
}

// nodeRunsFile says a script body serves a built file: node, tsx or bun on
// a script file, not `vite preview` or `serve`.
func nodeRunsFile(body string) bool {
	for _, segment := range nodeCommandSegments(body) {
		words := nodeProgramWords(segment)
		if len(words) < 2 {
			continue
		}
		switch path.Base(words[0]) {
		case "node", "tsx", "ts-node", "bun":
			for _, word := range words[1:] {
				if strings.HasPrefix(word, "-") {
					continue
				}
				if word == "run" {
					continue
				}
				if nodeScriptFile(word) || strings.HasPrefix(word, "./") || strings.HasPrefix(word, "dist/") || strings.HasPrefix(word, "build/") ||
					strings.HasPrefix(word, "server") || word == "." {
					return true
				}
				break
			}
		}
	}
	return false
}

// nodeScriptRunsEntry says a script body runs the given entry file, written
// with or without its extension or a leading ./.
func nodeScriptRunsEntry(body, entry string) bool {
	if entry == "" {
		return false
	}
	bare := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(entry, ".js"), ".mjs"), ".cjs")
	for _, segment := range nodeCommandSegments(body) {
		for _, word := range nodeProgramWords(segment) {
			word = strings.TrimPrefix(word, "./")
			if word == entry || word == bare || word == path.Dir(entry) && path.Base(entry) == "index.js" {
				return true
			}
		}
	}
	return false
}

// nodeStartRunsEntry says a start command, or a package script it runs,
// ends in the given entry file.
func nodeStartRunsEntry(scripts map[string]string, start, entry string) bool {
	if entry == "" || strings.TrimSpace(start) == "" {
		return false
	}
	for _, segment := range nodeReachedSegments(scripts, []string{start}, false) {
		if nodeScriptRunsEntry(segment, entry) {
			return true
		}
	}
	return false
}

// nodeDevScripts names each package script that starts a development
// server or a watcher, with what it starts, for preflight to judge a start
// command the operator writes later.
func nodeDevScripts(scripts map[string]string) map[string]string {
	found := map[string]string{}
	names := make([]string, 0, len(scripts))
	for name := range scripts {
		names = append(names, name)
	}
	for _, name := range slices.Sorted(slices.Values(names)) {
		if len(found) >= 32 || len(name) > 64 || !nodeScriptNameRE.MatchString(name) {
			continue
		}
		if label := nodeScriptDevServer(scripts, name); label != "" {
			found[name] = label
		}
	}
	if len(found) == 0 {
		return nil
	}
	return found
}
