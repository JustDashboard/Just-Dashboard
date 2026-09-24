package deploy

import (
	"encoding/json"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Prisma is the one JavaScript tool whose build step the recipe runs itself.
// Its client is generated code: Prisma 7 writes it into a directory the
// repository ignores, and Prisma 6 and earlier generate it from
// @prisma/client's install script, which pnpm 10 and Bun skip. Prisma 7's
// prisma.config.ts reads its datasource URL through env(), which throws when
// the variable is unset — even for `prisma generate`, which never connects.
// Everything here is read as text.

// nodePrismaFacts is what a package's Prisma configuration says.
type nodePrismaFacts struct {
	// config is the prisma.config file, relative to the package; env lists
	// the names it reads through env().
	config string
	env    []string
	// schema is the schema path prisma.config or package.json#prisma.schema
	// declares, migrations the migrations path prisma.config declares.
	schema, migrations string
	// found says a schema exists where Prisma looks for one, and provider
	// is its datasource provider.
	found    bool
	provider string
}

var (
	nodePrismaConfigNames = []string{
		"prisma.config.ts", "prisma.config.mts", "prisma.config.cts", "prisma.config.js", "prisma.config.mjs", "prisma.config.cjs",
		".config/prisma.ts", ".config/prisma.mts", ".config/prisma.cts", ".config/prisma.js", ".config/prisma.mjs", ".config/prisma.cjs",
	}
	// env("NAME"), env('NAME') and the typed env<...>("NAME").
	prismaEnvCallRE   = regexp.MustCompile(`\benv\s*(?:<[^>()]*>)?\s*\(\s*['"]([A-Za-z_][A-Za-z0-9_]*)['"]\s*\)`)
	prismaSchemaRE    = regexp.MustCompile(`\bschema\s*:\s*['"]([^'"\n]+)['"]`)
	prismaMigrationRE = regexp.MustCompile(`migrations\s*:\s*\{[^}]*?\bpath\s*:\s*['"]([^'"\n]+)['"]`)
)

// readPrismaFacts reads the Prisma configuration of the package in pkg.
func readPrismaFacts(pkg nodeFiles, manifest nodeInstallManifest) nodePrismaFacts {
	facts := nodePrismaFacts{}
	for _, name := range nodePrismaConfigNames {
		content, err := pkg.read(name, nodeConfigMaxBytes)
		if err != nil {
			continue
		}
		facts.config = name
		text := string(content)
		for _, match := range prismaEnvCallRE.FindAllStringSubmatch(text, -1) {
			if envNameRE.MatchString(match[1]) && !slices.Contains(facts.env, match[1]) && len(facts.env) < nodeListedNames {
				facts.env = append(facts.env, match[1])
			}
		}
		if match := prismaSchemaRE.FindStringSubmatch(text); match != nil {
			facts.schema = cleanPrismaPath(match[1])
		}
		if match := prismaMigrationRE.FindStringSubmatch(text); match != nil {
			facts.migrations = cleanPrismaPath(match[1])
		}
		break
	}
	if facts.schema == "" && len(manifest.Prisma) > 0 {
		var field struct {
			Schema string `json:"schema"`
		}
		if json.Unmarshal(manifest.Prisma, &field) == nil {
			facts.schema = cleanPrismaPath(field.Schema)
		}
	}
	locations := []string{"prisma/schema.prisma", "schema.prisma", "prisma/schema"}
	if facts.schema != "" {
		locations = []string{facts.schema}
	}
	for _, location := range locations {
		files := []string{}
		switch {
		case pkg.exists(location):
			files = []string{location}
		case pkg.dirExists(location):
			// A multi-file schema is a directory of .prisma files, one of
			// which holds the datasource.
			for _, name := range pkg.list(location, 64) {
				if strings.HasSuffix(name, ".prisma") {
					files = append(files, path.Join(location, name))
				}
			}
		}
		if len(files) == 0 {
			continue
		}
		facts.found = true
		for _, file := range files[:min(len(files), 8)] {
			if content, err := pkg.read(file, 64<<10); err == nil {
				if match := prismaProviderRE.FindSubmatch(content); match != nil {
					facts.provider = string(match[1])
					break
				}
			}
		}
		break
	}
	return facts
}

func cleanPrismaPath(value string) string {
	value = strings.TrimPrefix(strings.TrimSpace(value), "./")
	value = strings.TrimSuffix(value, "/")
	if value == "" || !safeRelativePath(value) {
		return ""
	}
	return path.Clean(value)
}

// list names the regular files directly in a directory, sorted and bounded.
func (f nodeFiles) list(rel string, limit int) []string {
	if f.root == nil || !safeRelativePath(rel) {
		return nil
	}
	directory, err := f.root.Open(f.name(rel))
	if err != nil {
		return nil
	}
	defer directory.Close()
	entries, err := directory.ReadDir(limit)
	if err != nil && len(entries) == 0 {
		return nil
	}
	names := []string{}
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

// prismaPlaceholder is the value `prisma generate` gets for a name
// prisma.config reads through env() when the build supplies none: a recipe
// constant shaped like the schema's provider, pointing at nothing, since
// generate only needs the name to resolve. It is never a credential.
func prismaPlaceholder(provider, name string) string {
	if !strings.Contains(name, "URL") && !strings.Contains(name, "URI") && !strings.Contains(name, "DSN") {
		return "prisma-generate"
	}
	switch provider {
	case "mysql":
		return "mysql://127.0.0.1:3306/prisma-generate"
	case "sqlserver":
		return "sqlserver://127.0.0.1:1433;database=prisma-generate"
	case "sqlite":
		return "file:./prisma-generate.db"
	case "mongodb":
		return "mongodb://127.0.0.1:27017/prisma-generate"
	}
	return "postgresql://127.0.0.1:5432/prisma-generate"
}

// nodePrismaPlan is what the recipe runs for Prisma.
type nodePrismaPlan struct {
	// generate is the step between the install and the build.
	generate string
	// defaults are the env() placeholders, applied to the install, to
	// generate and, when the build itself runs generate, to the build.
	defaults []nodeEnvDefault
	build    bool
}

// planPrisma adds `prisma generate` after the install when the CLI is
// installed and a schema exists, and gives every step that may run generate
// a placeholder for the names prisma.config reads through env(). A real
// value still wins: the placeholder applies only when the step's shell has
// no value, and a variable is never widened to the install on its own,
// since the install also runs every dependency's install script.
func planPrisma(facts nodeInstallFacts, plan *nodeInstallPlan) {
	prisma := facts.prisma
	cli := facts.manifest.has("prisma") || facts.settings.has("prisma")
	if !cli && !facts.manifest.has("@prisma/client") && !facts.settings.has("@prisma/client") {
		return
	}
	for _, name := range prisma.env {
		plan.prisma.defaults = append(plan.prisma.defaults, nodeEnvDefault{name, prismaPlaceholder(prisma.provider, name)})
	}
	if cli && prisma.found {
		plan.prisma.generate = nodeExecRunner(plan.manager) + " prisma generate"
		plan.findings = append(plan.findings, nodeFinding("prisma_generate_added", PreflightPass,
			"The build generates the Prisma client", plan.prisma.generate,
			"It runs after the install and before the build, whatever the install's script policy ran: pnpm 10 and Bun skip @prisma/client's install script, and Prisma 7 generates into a directory the repository ignores. OpenSSL is installed so Prisma's engines find the TLS library.",
			"", "configuration.build"))
	}
	generates, connects := false, false
	for _, segment := range nodeReachedSegments(facts.manifest.Scripts, []string{plan.build}, false) {
		generates = generates || strings.Contains(segment, "prisma generate")
		connects = connects || strings.Contains(segment, "prisma migrate") || strings.Contains(segment, "prisma db ")
	}
	// A build that migrates needs the real database, and a placeholder
	// would only turn a named missing variable into a refused connection.
	plan.prisma.build = generates && !connects
	if len(prisma.env) == 0 {
		return
	}
	steps := "the install"
	if plan.prisma.generate != "" {
		steps += ", " + plan.prisma.generate
	}
	if plan.prisma.build {
		steps += " and the build command, which runs prisma generate,"
	}
	plan.findings = append(plan.findings, nodeFinding("prisma_config_env", PreflightPass,
		"Prisma's configuration can load without a database value", strings.Join(prisma.env, ", ")+" ("+prisma.config+")",
		prisma.config+" reads these through env(), which throws when one is unset, and prisma generate never connects: "+steps+" run with "+prismaPlaceholder(prisma.provider, prisma.env[0])+" for any the build gives no value. The build's own value wins wherever it is mounted; the install receives a real one only when it is mapped to install and build.",
		"", "configuration.build"))
}
