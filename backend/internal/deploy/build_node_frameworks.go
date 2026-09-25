package deploy

import (
	"path"
	"slices"
	"strings"
)

// What the Node recipe renders around a framework's build for the commands
// the plan runs: the entry the build must write, the steps after the build
// that its server needs, the runtime environment, and how nginx serves a
// static site. It is decided with the plan's own start command, so an
// operator's start command gets the same treatment as a detected one.

// nodeServing is what the recipe adds for the configured commands.
type nodeServing struct {
	// entry is the file the build must have written, checked after it.
	entry string
	// afterBuild are RUN lines after the build command; runtimeEnv are ENV
	// lines of the server's runtime stage.
	afterBuild []string
	runtimeEnv []string
	site       nodeStaticSite
	notes      []nodeFrameworkNote
}

// nodeStaticSite is how nginx serves a framework's static output: which page
// answers a path with no file behind it, the path the site lives under, and
// whether /about is served from about.html.
type nodeStaticSite struct {
	spa       bool
	fallback  string
	base      string
	cleanURLs bool
}

// planNodeServing decides nodeServing for a plan. A static output directory
// is served as the framework writes it only when it is the framework's own
// output; a server's entry is checked when the start command, or a package
// script it runs, runs it.
func planNodeServing(manifest nodeManifest, framework nodeRecipeFramework, config BuildPlanConfig) nodeServing {
	serving := nodeServing{}
	resolution := framework.resolution
	if output := strings.TrimSpace(config.OutputDirectory); output != "" {
		serving.site = nodeStaticSite{spa: config.SPAFallback}
		if framework.name != "" && path.Clean(output) == path.Clean(resolution.Output) {
			serving.site.fallback, serving.site.base, serving.site.cleanURLs = resolution.Fallback, resolution.Base, resolution.CleanURLs
		}
		return serving
	}
	start := strings.TrimSpace(config.StartCommand)
	if standalone := nodeStandaloneServer(manifest.Scripts, start); standalone != "" {
		// next build writes the standalone server without the static assets
		// and public files it serves; Next.js's own Docker example copies
		// them in, and the server binds HOSTNAME, which in a container is
		// the container's own name.
		directory := path.Dir(standalone)
		serving.entry = standalone
		serving.afterBuild = append(serving.afterBuild,
			"mkdir -p "+directory+"/.next && cp -r .next/static "+directory+"/.next/ && if [ -d public ]; then cp -r public "+directory+"/; fi")
		serving.runtimeEnv = append(serving.runtimeEnv, "HOSTNAME=0.0.0.0")
		serving.notes = append(serving.notes, nodeFrameworkNote{file: "package.json",
			reason: "the start command runs " + standalone + "; the build copies .next/static and public beside it"})
		return serving
	}
	if resolution.Entry != "" && (frameworkDefaultStart(start, resolution.Start) || nodeStartRunsEntry(manifest.Scripts, start, resolution.Entry)) {
		serving.entry = resolution.Entry
	}
	return serving
}

// nodeStandaloneServer is the Next.js standalone server a start command, or
// a package script it runs, starts: `node .next/standalone/server.js`, or
// the member's server in a monorepo's standalone output.
func nodeStandaloneServer(scripts map[string]string, start string) string {
	if start == "" {
		return ""
	}
	for _, segment := range nodeReachedSegments(scripts, []string{start}, false) {
		for _, word := range nodeProgramWords(segment) {
			word = strings.TrimPrefix(word, "./")
			if strings.HasPrefix(word, ".next/standalone/") && strings.HasSuffix(word, "server.js") &&
				safeRelativePath(word) && nodeMemberPathRE.MatchString(word) {
				return path.Clean(word)
			}
		}
	}
	return ""
}

// nodeStaticServerLines configures nginx for a framework's static output.
// Output served at the root as nginx's default would is staticServerLines'
// own; a base path, a fallback page other than index.html or pages written
// as about.html get a server block that says so. The site is copied under
// its base path (nodeStaticTarget) rather than aliased, since nginx's alias
// and try_files do not combine reliably.
func nodeStaticServerLines(site nodeStaticSite) []string {
	if site.base == "" && site.fallback == "" && !site.cleanURLs {
		return staticServerLines(site.spa)
	}
	fallback := "=404"
	if site.spa {
		// A fallback page other than index.html is tried as a file first:
		// a framework writes it only in some of its modes (React Router's
		// __spa-fallback.html when "/" is prerendered).
		fallback = site.base + "/index.html"
		if site.fallback != "" && site.fallback != "index.html" {
			fallback = site.base + "/" + site.fallback + " " + fallback
		}
	}
	tries := "$uri $uri/ " + fallback
	if site.cleanURLs {
		// about.html before about/: Next.js writes both for a page, and the
		// directory holds only its data files.
		tries = "$uri $uri.html $uri/ " + fallback
	}
	conf := []string{
		"server {", "    listen 80;", "    server_name _;", "    root /usr/share/nginx/html;",
		"    index index.html index.htm;", "    absolute_redirect off;", `    location ~ /\.(?!well-known/) {`, "        deny all;", "    }",
	}
	if site.base != "" {
		conf = append(conf, "    location = / {", "        return 302 "+site.base+"/;", "    }")
	}
	conf = append(conf, "    location / {", "        try_files "+tries+";", "    }")
	if !site.spa {
		conf = append(conf, "    error_page 404 "+site.base+"/404.html;")
	}
	conf = append(conf, "}")
	quoted := make([]string, 0, len(conf))
	for _, line := range conf {
		quoted = append(quoted, "'"+line+"'")
	}
	return []string{"RUN printf '%s\\n' " + strings.Join(quoted, " ") + " > /etc/nginx/conf.d/default.conf"}
}

// nodeStaticTarget is where the site is copied in the nginx image.
func nodeStaticTarget(site nodeStaticSite) string {
	return "/usr/share/nginx/html" + site.base + "/"
}

// nodeMeteorRefusal is why a Meteor application is not built by the recipe.
const nodeMeteorRefusal = "Meteor builds with its own toolchain (meteor build), which the JavaScript recipe does not install; deploy it with a Dockerfile"

// nodeForeignRunners are programs of another language's toolchain, which no
// stage of the Node image provides; a command that reaches one stops with
// "not found" after the install, so it is refused before the build.
var nodeForeignRunners = []string{
	"deno", "python", "python3", "pip", "pip3", "uv", "poetry", "php", "composer", "ruby", "bundle", "rails",
	"java", "mvn", "gradle", "go", "cargo", "dotnet",
}

// nodeForeignRunner is the first foreign program the build (with the
// install's lifecycle scripts) or the start command reaches, and the field
// that runs it. python3 is in the build stage when a native addon brings the
// compilers in, so only a start command that reaches it, or a build without
// them, is refused.
func nodeForeignRunner(facts nodeInstallFacts, build, start string) (string, string) {
	compilers := slices.ContainsFunc(nodeNativeAddons, facts.present)
	for _, command := range []struct {
		text, field string
		lifecycle   bool
	}{{build, "configuration.build.buildCommand", true}, {start, "configuration.build.startCommand", false}} {
		tools := map[string]bool{}
		for _, segment := range nodeReachedSegments(facts.manifest.Scripts, []string{command.text}, command.lifecycle) {
			if words := nodeSegmentWords(segment); len(words) > 0 {
				tools[path.Base(words[0])] = true
			}
		}
		for _, runner := range nodeForeignRunners {
			if !tools[runner] || (runner == "python3" && compilers && command.field == "configuration.build.buildCommand") {
				continue
			}
			return runner, command.field
		}
	}
	return "", ""
}
